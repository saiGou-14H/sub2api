//go:build unit

package server

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/runner"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const managementOwner = "sub2api_user_42"
const managementOther = "sub2api_user_84"
const managementCreate = `{"username":"sub2api_user_42","client_id":"node-a"}`

var managementSafeColumns = []string{"id", "user_id", "name", "key_prefix", "created_at", "last_used_at", "revoked_at", "scopes", "expires_at", "kind", "allowed_client_id"}

const managementListSQL = `SELECT id, user_id, name, key_prefix, created_at, last_used_at, revoked_at, scopes, expires_at, kind, allowed_client_id FROM wc_api_keys WHERE user_id = $1 AND kind = 'agent' ORDER BY created_at DESC LIMIT 200`
const managementRevokeSQL = `UPDATE wc_api_keys SET revoked_at = COALESCE(revoked_at, $3) WHERE id = $1 AND user_id = $2 AND kind = 'agent' RETURNING id, user_id, name, key_prefix, created_at, last_used_at, revoked_at, scopes, expires_at, kind, allowed_client_id`

type managementUsers struct {
	service.UserRepository
	users map[int64]*service.User
	calls int
	read  func(int64, int) (*service.User, error)
}

func (r *managementUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	r.calls++
	if r.read != nil {
		return r.read(id, r.calls)
	}
	if u := r.users[id]; u != nil {
		copy := *u
		return &copy, nil
	}
	return nil, service.ErrUserNotFound
}
func (*managementUsers) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}
func (*managementUsers) UpdateUserLastActiveAt(context.Context, int64, time.Time) error { return nil }

type managementFixture struct {
	router  *gin.Engine
	runtime *WebCodexRunner
	users   *managementUsers
	mock    sqlmock.Sqlmock
	auth    *service.AuthService
}

func newManagementFixture(t *testing.T, enabled bool, options ...managementTestOptions) *managementFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	now := time.Now()
	users := &managementUsers{users: map[int64]*service.User{
		42: {ID: 42, Username: "mutable-display", Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 1, LastActiveAt: &now},
		84: {ID: 84, Username: "other-display", Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 1, LastActiveAt: &now},
	}}
	cfg := webCodexTestConfig()
	var opts managementTestOptions
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.cfg != nil {
		cfg = opts.cfg
	}
	cfg.WebCodexRunner.Enabled = enabled
	cfg.JWT.Secret = "fabricated-management-jwt-signing-key"
	cfg.JWT.AccessTokenExpireMinutes = 60
	auth := service.NewAuthService(nil, users, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	userSvc := service.NewUserService(users, nil, nil, nil)
	jwt := middleware.NewJWTAuthMiddleware(auth, userSvc, nil, nil)
	runtime, err := ProvideWebCodexRunner(cfg, db, users)
	require.NoError(t, err)
	t.Cleanup(runtime.Close)
	router := gin.New()
	router.Use(middleware.CORS(config.CORSConfig{AllowedOrigins: []string{"https://fixture.example"}}))
	runtime.mount(router)
	runtime.mountManagement(router, jwt, middleware.BackendModeUserGuard(opts.settings), middleware.NewPanelRateLimiter(opts.redis, opts.settings).Global(), middleware.NewAuditLogMiddleware(opts.audit))
	return &managementFixture{router: router, runtime: runtime, users: users, mock: mock, auth: auth}
}
func (f *managementFixture) jwt(t *testing.T, id int64) string {
	t.Helper()
	token, err := f.auth.GenerateToken(context.Background(), f.users.users[id])
	require.NoError(t, err)
	return token
}
func (f *managementFixture) send(t *testing.T, action, token, body string, status int) map[string]json.RawMessage {
	t.Helper()
	rec := webCodexSend(f.router, http.MethodPost, "/api/agent-tokens/"+action, token, body)
	// Do not print successful issuance payloads or bearer tokens on assertion failure.
	require.Equal(t, status, rec.Code)
	if status == http.StatusNotFound {
		return nil
	}
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	return result
}
func managementString(t *testing.T, body map[string]json.RawMessage, field string) string {
	t.Helper()
	var s string
	require.NoError(t, json.Unmarshal(body[field], &s))
	return s
}

type managementCapture struct{ value *string }

func (a managementCapture) Match(v driver.Value) bool {
	s, ok := v.(string)
	if ok {
		*a.value = s
	}
	return ok
}
func managementExpectInsert(mock sqlmock.Sqlmock, id, hash *string, user int64, name, prefix, scopes string, expires driver.Value, err error) {
	expectation := mock.ExpectExec(`INSERT INTO wc_api_keys`).WithArgs(managementCapture{id}, user, name, managementCapture{hash}, prefix, sqlmock.AnyArg(), nil, nil, scopes, expires, "agent", "node-a")
	if err != nil {
		expectation.WillReturnError(err)
	} else {
		expectation.WillReturnResult(sqlmock.NewResult(0, 1))
	}
}
func managementSafeRows(id string, user int64, revoked any) *sqlmock.Rows {
	return sqlmock.NewRows(managementSafeColumns).AddRow(id, user, "fixture", "wc_agent_public", int64(1), nil, revoked, "agent:register agent:poll", nil, "agent", "node-a")
}
func managementNoSecrets(t *testing.T, body map[string]json.RawMessage, token, hash string) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(raw), "key_hash"))
	require.False(t, strings.Contains(string(raw), "token_hash"))
	if token != "" {
		require.False(t, strings.Contains(string(raw), token))
	}
	if hash != "" {
		require.False(t, strings.Contains(string(raw), hash))
	}
}

func TestWebCodexAgentTokenCreateAuthenticateListRevoke(t *testing.T) {
	f := newManagementFixture(t, true)
	jwt := f.jwt(t, 42)
	var id, hash, prefix string
	f.mock.ExpectExec(`INSERT INTO wc_api_keys`).WithArgs(managementCapture{&id}, int64(42), "default", managementCapture{&hash}, managementCapture{&prefix}, sqlmock.AnyArg(), nil, nil, strings.Join(defaultAgentTokenScopes(), " "), nil, "agent", "node-a").WillReturnResult(sqlmock.NewResult(0, 1))
	created := f.send(t, "create", jwt, managementCreate, 200)
	token := managementString(t, created, "token")
	digest := sha256.Sum256([]byte(token))
	require.True(t, hash == hex.EncodeToString(digest[:]))
	require.True(t, len(token) == 73 && strings.HasPrefix(token, "wc_agent_") && prefix == token[:16])
	require.Equal(t, managementOwner, managementString(t, created, "username"))
	require.Equal(t, managementOwner, managementString(t, created, "user_id"))
	require.Len(t, created, 12)
	require.False(t, strings.Contains(string(created["token_prefix"]), token))
	// Actual verifier consumes exactly the stored row and original transport scopes.
	columns := []string{"id", "user_id", "name", "key_hash", "key_prefix", "created_at", "last_used_at", "revoked_at", "scopes", "expires_at", "kind", "allowed_client_id"}
	f.mock.ExpectQuery(`SELECT .* FROM wc_api_keys WHERE key_hash = \$1 AND revoked_at IS NULL`).WithArgs(hash).WillReturnRows(sqlmock.NewRows(columns).AddRow(id, int64(42), "default", hash, prefix, int64(1), nil, nil, strings.Join(defaultAgentTokenScopes(), " "), nil, "agent", "node-a"))
	f.mock.ExpectExec(`UPDATE wc_api_keys SET last_used_at = \$2 WHERE id = \$1`).WithArgs(id, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	verify, err := runner.NewAgentTokenAuthenticator(f.runtime.tokens.keys, repository.NewWebCodexHostUserResolver(f.users), runner.AgentTokenOptions{MaxTokenBytes: 1024, Now: time.Now})
	require.NoError(t, err)
	principal, err := verify(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, runner.AgentToken, principal.Kind)
	require.Equal(t, managementOwner, principal.Username)
	require.Equal(t, defaultAgentTokenScopes(), principal.Scopes)
	f.mock.ExpectQuery(regexp.QuoteMeta(managementListSQL)).WithArgs(int64(42)).WillReturnRows(managementSafeRows(id, 42, nil))
	listed := f.send(t, "list", jwt, `{"username":"sub2api_user_42"}`, 200)
	managementNoSecrets(t, listed, token, hash)
	require.JSONEq(t, `1`, string(listed["count"]))
	for i := 0; i < 2; i++ {
		f.mock.ExpectQuery(regexp.QuoteMeta(managementRevokeSQL)).WithArgs(id, int64(42), sqlmock.AnyArg()).WillReturnRows(managementSafeRows(id, 42, int64(123)))
		revoked := f.send(t, "revoke", jwt, fmt.Sprintf(`{"username":"sub2api_user_42","token_id":%q}`, id), 200)
		managementNoSecrets(t, revoked, token, hash)
		var summary map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(revoked["token"], &summary))
		require.JSONEq(t, `123`, string(summary["revoked_at"]))
		require.Len(t, summary, 11)
	}
	f.mock.ExpectQuery(`SELECT .* FROM wc_api_keys WHERE key_hash = \$1 AND revoked_at IS NULL`).WithArgs(hash).WillReturnRows(sqlmock.NewRows(columns))
	_, err = verify(context.Background(), token)
	require.ErrorIs(t, err, runner.ErrAgentCredentialInvalid)
}

func TestWebCodexAgentTokenRegisterDefaultsCollisionAndNoRevival(t *testing.T) {
	f := newManagementFixture(t, true)
	jwt := f.jwt(t, 42)
	const fakeHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	body := `{"username":"sub2api_user_42","client_id":" node-a ","token_hash":"sha256:` + strings.ToUpper(fakeHash) + `","token_prefix":" wc_agent_public ","scopes":[]}`
	var id, hash string
	managementExpectInsert(f.mock, &id, &hash, 42, "node-a", "wc_agent_public", strings.Join(defaultAgentTokenScopes(), " "), nil, nil)
	registered := f.send(t, "register_hash", jwt, body, 200)
	require.True(t, hash == fakeHash)
	managementNoSecrets(t, registered, "", hash)
	var summary map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(registered["token"], &summary))
	require.Len(t, registered, 2)
	require.JSONEq(t, `true`, string(registered["success"]))
	require.Len(t, summary, 7)
	for _, field := range []string{"id", "name", "token_prefix", "allowed_client_id", "scopes", "created_at", "expires_at"} {
		require.Contains(t, summary, field)
	}
	require.JSONEq(t, `null`, string(summary["expires_at"]))
	require.JSONEq(t, `"node-a"`, string(summary["name"]))
	require.JSONEq(t, `"node-a"`, string(summary["allowed_client_id"]))
	require.JSONEq(t, `["agent:register","agent:poll","agent:result","agent:job_update"]`, string(summary["scopes"]))
	for _, constraint := range []string{"wc_api_keys_key_hash_key", "wc_api_keys_pkey"} {
		managementExpectInsert(f.mock, &id, &hash, 42, "node-a", "wc_agent_public", strings.Join(defaultAgentTokenScopes(), " "), nil, &pq.Error{Code: "23505", Constraint: constraint, Detail: "private storage detail"})
		failed := f.send(t, "register_hash", jwt, body, 409)
		managementNoSecrets(t, failed, "private storage detail", hash)
	}
	// Unique index includes revoked records. No UPDATE or upsert expectation is supplied.
}

func TestWebCodexAgentTokenScopesAndExpiry(t *testing.T) {
	for _, tc := range []struct {
		name, action, extra, scopes string
		expiry                      driver.Value
	}{
		{"create-empty", "create", `,"scopes":[]`, "", nil},
		{"create-null", "create", `,"scopes":null`, strings.Join(defaultAgentTokenScopes(), " "), nil},
		{"create-normalized", "create", `,"scopes":[" agent:poll ","","agent:poll","agent:result"]`, "agent:poll agent:result", nil},
		{"register-omitted", "register_hash", ``, strings.Join(defaultAgentTokenScopes(), " "), nil},
		{"register-null-metadata", "register_hash", `,"name":null,"expires_at":null`, strings.Join(defaultAgentTokenScopes(), " "), nil},
		{"register-int64", "register_hash", `,"expires_at":9223372036854775807`, strings.Join(defaultAgentTokenScopes(), " "), int64(9223372036854775807)},
		{"register-blank-not-default", "register_hash", `,"scopes":[" "]`, "", nil},
		{"create-int64", "create", `,"expires_at":9223372036854775807`, strings.Join(defaultAgentTokenScopes(), " "), int64(9223372036854775807)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagementFixture(t, true)
			jwt := f.jwt(t, 42)
			base := strings.TrimSuffix(managementCreate, "}")
			name := "default"
			if tc.action == "register_hash" {
				base += `,"token_hash":"` + strings.Repeat("a", 64) + `","token_prefix":"wc_agent_public"`
				name = "node-a"
			}
			var id, hash, prefix string
			f.mock.ExpectExec(`INSERT INTO wc_api_keys`).WithArgs(managementCapture{&id}, int64(42), name, managementCapture{&hash}, managementCapture{&prefix}, sqlmock.AnyArg(), nil, nil, tc.scopes, tc.expiry, "agent", "node-a").WillReturnResult(sqlmock.NewResult(0, 1))
			result := f.send(t, tc.action, jwt, base+tc.extra+`}`, 200)
			if tc.action == "register_hash" {
				var summary map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(result["token"], &summary))
				wantScopes := strings.Fields(tc.scopes)
				if wantScopes == nil {
					wantScopes = []string{}
				}
				encoded, err := json.Marshal(wantScopes)
				require.NoError(t, err)
				require.JSONEq(t, string(encoded), string(summary["scopes"]))
				expiry, err := json.Marshal(tc.expiry)
				require.NoError(t, err)
				require.JSONEq(t, string(expiry), string(summary["expires_at"]))
				require.JSONEq(t, `"node-a"`, string(summary["name"]))
			}
		})
	}
}

func TestWebCodexAgentTokenMalformedAndBoundedBodies(t *testing.T) {
	for _, tc := range []struct {
		name, action, body string
		status             int
	}{
		{"required", "create", `{"username":"sub2api_user_42"}`, 400},
		{"canonical", "create", `{"username":"mutable-display","client_id":"node-a"}`, 400},
		{"canonical-zero-pad", "create", `{"username":"sub2api_user_042","client_id":"node-a"}`, 400},
		{"client-invalid", "create", `{"username":"sub2api_user_42","client_id":"bad/path"}`, 400},
		{"name-unicode-limit", "create", `{"username":"sub2api_user_42","client_id":"node-a","name":"` + strings.Repeat("中", 129) + `"}`, 400},
		{"scope-admin", "create", `{"username":"sub2api_user_42","client_id":"node-a","scopes":["admin"]}`, 400},
		{"scope-unknown-agent", "create", `{"username":"sub2api_user_42","client_id":"node-a","scopes":["agent:mint"]}`, 400},
		{"expired", "create", `{"username":"sub2api_user_42","client_id":"node-a","expires_at":0}`, 400},
		{"hash-invalid", "register_hash", `{"username":"sub2api_user_42","client_id":"node-a","token_hash":"private","token_prefix":"wc_agent_public"}`, 400},
		{"prefix-plaintext", "register_hash", `{"username":"sub2api_user_42","client_id":"node-a","token_hash":"` + strings.Repeat("a", 64) + `","token_prefix":"wc_agent_` + strings.Repeat("b", 64) + `"}`, 400},
		{"unknown-token", "register_hash", `{"username":"sub2api_user_42","client_id":"node-a","token_hash":"` + strings.Repeat("a", 64) + `","token_prefix":"wc_agent_public","token":"private"}`, 400},
		{"empty-revoke", "revoke", `{"username":"sub2api_user_42","token_id":" "}`, 400},
		{"too-large", "create", managementCreate + strings.Repeat(" ", int(webCodexTestConfig().WebCodexRunner.MaxBodyBytes)), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagementFixture(t, true)
			f.send(t, tc.action, f.jwt(t, 42), tc.body, tc.status)
		})
	}
	f := newManagementFixture(t, true)
	f.mock.ExpectQuery(regexp.QuoteMeta(managementListSQL)).WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows(managementSafeColumns))
	body := `{"username":"sub2api_user_42"}`
	body += strings.Repeat(" ", int(webCodexTestConfig().WebCodexRunner.MaxBodyBytes)-len(body))
	result := f.send(t, "list", f.jwt(t, 42), body, 200)
	require.JSONEq(t, `[]`, string(result["tokens"]))
}

func TestWebCodexAgentTokenJWTAuthorityAndDefaultOff(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		f := newManagementFixture(t, enabled)
		for _, action := range []string{"create", "register_hash", "list", "revoke"} {
			for _, token := range []string{"", "wc_agent_fabricated", "sk-model-fabricated", "wc_acct_fabricated"} {
				rec := webCodexSend(f.router, http.MethodPost, "/api/agent-tokens/"+action, token, "{}")
				want := 401
				if !enabled {
					want = 404
				}
				require.Equal(t, want, rec.Code)
			}
			preflight := httptest.NewRequest(http.MethodOptions, "/api/agent-tokens/"+action, nil)
			preflight.Header.Set("Origin", "https://fixture.example")
			preflight.Header.Set("Access-Control-Request-Method", "POST")
			rec := httptest.NewRecorder()
			f.router.ServeHTTP(rec, preflight)
			require.Equal(t, 204, rec.Code)
		}
		require.Zero(t, f.users.calls)
	}
	f := newManagementFixture(t, true)
	jwt := f.jwt(t, 42)
	f.send(t, "create", jwt, `{"username":"sub2api_user_84","client_id":"node-a"}`, 403)
	f.users.users[42].Role = service.RoleAdmin
	adminJWT := f.jwt(t, 42)
	f.users.users[42].Role = service.RoleUser // current user role, not signed stale admin claim
	f.send(t, "list", adminJWT, `{"username":"sub2api_user_84"}`, 403)
	f.users.users[42].Role = service.RoleAdmin
	f.mock.ExpectQuery(regexp.QuoteMeta(managementListSQL)).WithArgs(int64(84)).WillReturnRows(sqlmock.NewRows(managementSafeColumns))
	f.send(t, "list", jwt, `{"username":"sub2api_user_84"}`, 200) // stale user claim doesn't erase current admin
	f.users.users[84].Status = "disabled"
	f.send(t, "create", jwt, `{"username":"sub2api_user_84","client_id":"node-a"}`, 403)
	f.mock.ExpectQuery(regexp.QuoteMeta(managementListSQL)).WithArgs(int64(84)).WillReturnRows(sqlmock.NewRows(managementSafeColumns))
	f.send(t, "list", jwt, `{"username":"sub2api_user_84"}`, 200) // original admin recovery behavior
	f.send(t, "list", jwt, `{"username":"sub2api_user_999"}`, 404)
	now := time.Now()
	f.users.users[84].DeletedAt = &now
	f.send(t, "list", jwt, `{"username":"sub2api_user_84"}`, 404)
	f.users.users[42].Status = "disabled"
	require.Equal(t, 401, webCodexSend(f.router, http.MethodPost, "/api/agent-tokens/list", jwt, `{"username":"sub2api_user_42"}`).Code)
}

func TestWebCodexAgentTokenCurrentHostRecheckAndErrors(t *testing.T) {
	for _, mode := range []string{"disabled", "deleted", "mismatch", "missing", "error"} {
		t.Run(mode, func(t *testing.T) {
			f := newManagementFixture(t, true)
			jwt := f.jwt(t, 42)
			f.users.read = func(id int64, n int) (*service.User, error) {
				copy := *f.users.users[42]
				if n > 1 {
					switch mode {
					case "disabled":
						copy.Status = "disabled"
					case "deleted":
						now := time.Now()
						copy.DeletedAt = &now
					case "mismatch":
						copy.ID = 84
					case "missing":
						return nil, service.ErrUserNotFound
					case "error":
						return nil, errors.New("private repository failure")
					}
				}
				return &copy, nil
			}
			expected := 401
			if mode == "error" {
				expected = 500
			}
			result := f.send(t, "create", jwt, managementCreate, expected)
			managementNoSecrets(t, result, "private repository failure", "")
		})
	}
}

func TestWebCodexAgentTokenStorageFailureAndHostTokenInvalidation(t *testing.T) {
	f := newManagementFixture(t, true)
	jwt := f.jwt(t, 42)
	var id, hash, prefix string
	f.mock.ExpectExec(`INSERT INTO wc_api_keys`).WithArgs(managementCapture{&id}, int64(42), "default", managementCapture{&hash}, managementCapture{&prefix}, sqlmock.AnyArg(), nil, nil, strings.Join(defaultAgentTokenScopes(), " "), nil, "agent", "node-a").WillReturnError(errors.New("private storage detail"))
	failed := f.send(t, "create", jwt, managementCreate, 500)
	managementNoSecrets(t, failed, "private storage detail", hash)
	require.NotContains(t, failed, "token")
	f.mock.ExpectQuery(regexp.QuoteMeta(managementListSQL)).WithArgs(int64(42)).WillReturnError(errors.New("private query detail"))
	failed = f.send(t, "list", jwt, `{"username":"sub2api_user_42"}`, 500)
	managementNoSecrets(t, failed, "private query detail", "")
	f.users.users[42].TokenVersion++
	require.Equal(t, 401, webCodexSend(f.router, http.MethodPost, "/api/agent-tokens/create", jwt, managementCreate).Code)
}

func TestWebCodexAgentTokenAdminIssuanceAndRecovery(t *testing.T) {
	f := newManagementFixture(t, true)
	f.users.users[42].Role = service.RoleAdmin
	jwt := f.jwt(t, 42)
	var id, hash, prefix string
	f.mock.ExpectExec(`INSERT INTO wc_api_keys`).WithArgs(managementCapture{&id}, int64(84), "default", managementCapture{&hash}, managementCapture{&prefix}, sqlmock.AnyArg(), nil, nil, strings.Join(defaultAgentTokenScopes(), " "), nil, "agent", "node-a").WillReturnResult(sqlmock.NewResult(0, 1))
	created := f.send(t, "create", jwt, `{"username":"sub2api_user_84","client_id":"node-a"}`, 200)
	require.Equal(t, managementOther, managementString(t, created, "user_id"))
	f.users.users[84].Status = "disabled"
	f.mock.ExpectQuery(regexp.QuoteMeta(managementRevokeSQL)).WithArgs(id, int64(84), sqlmock.AnyArg()).WillReturnRows(managementSafeRows(id, 84, int64(123)))
	f.send(t, "revoke", jwt, fmt.Sprintf(`{"username":"sub2api_user_84","token_id":%q}`, id), 200)
	hashBody := `{"username":"sub2api_user_84","client_id":"node-a","token_hash":"` + strings.Repeat("a", 64) + `","token_prefix":"wc_agent_public"}`
	f.send(t, "register_hash", jwt, hashBody, 403)
}

func TestWebCodexAgentTokenValidationPrecedesTargetLookup(t *testing.T) {
	for _, tc := range []struct{ action, body string }{
		{"create", `{"username":"sub2api_user_999","client_id":"bad/path"}`},
		{"register_hash", `{"username":"sub2api_user_999","client_id":"node-a","token_hash":"invalid","token_prefix":"wc_agent_public"}`},
		{"revoke", `{"username":"sub2api_user_999","token_id":" "}`},
	} {
		f := newManagementFixture(t, true)
		f.users.users[42].Role = service.RoleAdmin
		f.send(t, tc.action, f.jwt(t, 42), tc.body, 400)
		require.Equal(t, 2, f.users.calls, "JWT and caller recheck only; no target lookup")
	}
}

func TestWebCodexAgentTokenUnknownLengthBodyBound(t *testing.T) {
	f := newManagementFixture(t, true)
	body := managementCreate + strings.Repeat(" ", int(webCodexTestConfig().WebCodexRunner.MaxBodyBytes))
	request := httptest.NewRequest(http.MethodPost, "/api/agent-tokens/create", strings.NewReader(body))
	request.ContentLength = -1
	request.TransferEncoding = []string{"chunked"}
	request.Header.Set("Authorization", "Bearer "+f.jwt(t, 42))
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	require.Equal(t, 413, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
}

func TestWebCodexAgentTokenScopedRevokeDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner int64
		kind  string
		found bool
		code  int
	}{
		{"missing", 0, "", false, 404}, {"foreign", 84, "agent", true, 403}, {"non-agent", 42, "user", true, 400}, {"changed", 42, "agent", true, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagementFixture(t, true)
			f.mock.ExpectQuery(regexp.QuoteMeta(managementRevokeSQL)).WithArgs("fixture", int64(42), sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows(managementSafeColumns))
			rows := sqlmock.NewRows([]string{"user_id", "kind"})
			if tc.found {
				rows.AddRow(tc.owner, tc.kind)
			}
			f.mock.ExpectQuery(regexp.QuoteMeta(`SELECT user_id, kind FROM wc_api_keys WHERE id = $1`)).WithArgs("fixture").WillReturnRows(rows)
			f.send(t, "revoke", f.jwt(t, 42), `{"username":"sub2api_user_42","token_id":" fixture "}`, tc.code)
		})
	}
}
