package repository

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/runner"
	"github.com/stretchr/testify/require"
)

func webCodexMock(t *testing.T) (*WebCodexAPIKeyRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	return NewWebCodexAPIKeyRepository(db), mock
}

func wcRows(values ...driver.Value) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"id", "user_id", "name", "key_hash", "key_prefix", "created_at", "last_used_at", "revoked_at", "scopes", "expires_at", "kind", "allowed_client_id"})
	if values != nil {
		rows.AddRow(values...)
	}
	return rows
}

func TestWebCodexAPIKeyOriginalFields(t *testing.T) {
	repo, mock := webCodexMock(t)
	ctx := context.Background()
	last, revoked, expires := int64(0), int64(-1), int64(math.MaxInt64)
	client := "client.A-1"
	key := &WebCodexAPIKey{ID: "original-key-id", UserID: math.MaxInt64, Name: "原 token", KeyHash: "fixture-hash", KeyPrefix: "wc_agent_fixture", CreatedAt: math.MinInt64, LastUsedAt: &last, RevokedAt: &revoked, Scopes: "agent:poll\u0085future:scope agent:poll admin", ExpiresAt: &expires, Kind: "", AllowedClientID: &client}
	values := []driver.Value{key.ID, key.UserID, key.Name, key.KeyHash, key.KeyPrefix, key.CreatedAt, last, revoked, key.Scopes, expires, key.Kind, client}
	mock.ExpectExec(`INSERT INTO wc_api_keys (` + webCodexKeyColumns + `) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`).WithArgs(values...).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Insert(ctx, key))
	mock.ExpectQuery(`SELECT ` + webCodexKeyColumns + ` FROM wc_api_keys WHERE id = $1`).WithArgs(key.ID).WillReturnRows(wcRows(values...))
	actual, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, key, actual)
}

func TestWebCodexAPIKeyNullLifecycleRoundTrip(t *testing.T) {
	repo, mock := webCodexMock(t)
	key := &WebCodexAPIKey{ID: "null-lifecycle", UserID: 7, Name: "fixture", KeyHash: "fixture-hash", KeyPrefix: "fixture", CreatedAt: 1, Kind: "user"}
	values := []driver.Value{key.ID, key.UserID, key.Name, key.KeyHash, key.KeyPrefix, key.CreatedAt, nil, nil, "", nil, "user", nil}
	mock.ExpectExec(`INSERT INTO wc_api_keys (` + webCodexKeyColumns + `) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`).WithArgs(values...).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Insert(context.Background(), key))
	mock.ExpectQuery(`SELECT ` + webCodexKeyColumns + ` FROM wc_api_keys WHERE id = $1`).WithArgs(key.ID).WillReturnRows(wcRows(values...))
	actual, err := repo.GetByID(context.Background(), key.ID)
	require.NoError(t, err)
	require.Equal(t, key, actual)
	require.Nil(t, actual.LastUsedAt)
	require.Nil(t, actual.RevokedAt)
	require.Nil(t, actual.ExpiresAt)
	require.Nil(t, actual.AllowedClientID)
}

func TestWebCodexAPIKeyLookupFreshProjection(t *testing.T) {
	repo, mock := webCodexMock(t)
	query := `SELECT ` + webCodexKeyColumns + ` FROM wc_api_keys WHERE key_hash = $1 AND revoked_at IS NULL`
	for _, kind := range []string{"agent", "user", ""} {
		mock.ExpectQuery(query).WithArgs("exact hash").WillReturnRows(wcRows("key", int64(math.MaxInt64), "label", "exact hash", "prefix", int64(1), nil, nil, "agent:poll future:scope agent:poll admin", nil, kind, nil))
		key, err := repo.GetByHash(context.Background(), "exact hash")
		require.NoError(t, err)
		require.Equal(t, "sub2api_user_9223372036854775807", key.UserID)
		require.Equal(t, kind, key.Kind)
		require.Equal(t, "agent:poll future:scope agent:poll admin", key.Scopes)
		require.Nil(t, key.AllowedClientID)
		require.Nil(t, key.ExpiresAt)
		key.Scopes = "caller mutation"
	}
	// A fresh query observes a removed/revoked row, with no positive cache.
	mock.ExpectQuery(query).WithArgs("exact hash").WillReturnRows(wcRows())
	key, err := repo.GetByHash(context.Background(), "exact hash")
	require.NoError(t, err)
	require.Nil(t, key)
}

func TestWebCodexAPIKeyRevokeAndTouch(t *testing.T) {
	repo, mock := webCodexMock(t)
	ctx := context.Background()
	for _, requested := range []int64{41, 99} {
		mock.ExpectQuery(`UPDATE wc_api_keys SET revoked_at = COALESCE(revoked_at, $2) WHERE id = $1 RETURNING `+webCodexKeyColumns).WithArgs("key", requested).WillReturnRows(wcRows("key", int64(7), "label", "hash", "prefix", int64(1), nil, int64(41), "", nil, "user", nil))
		key, err := repo.Revoke(ctx, "key", requested)
		require.NoError(t, err)
		require.Equal(t, int64(41), *key.RevokedAt)
	}
	mock.ExpectQuery(`UPDATE wc_api_keys SET revoked_at = COALESCE(revoked_at, $2) WHERE id = $1 RETURNING `+webCodexKeyColumns).WithArgs("missing", int64(3)).WillReturnRows(wcRows())
	key, err := repo.Revoke(ctx, "missing", 3)
	require.NoError(t, err)
	require.Nil(t, key)
	mock.ExpectExec(`UPDATE wc_api_keys SET last_used_at = $2 WHERE id = $1`).WithArgs("key", int64(0)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateLastUsed(ctx, "key", 0))
	mock.ExpectExec(`UPDATE wc_api_keys SET last_used_at = $2 WHERE id = $1`).WithArgs("missing", int64(-1)).WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, repo.UpdateLastUsed(ctx, "missing", -1))
}

func TestWebCodexAPIKeyFailures(t *testing.T) {
	repo, mock := webCodexMock(t)
	ctx := context.Background()
	query := `SELECT ` + webCodexKeyColumns + ` FROM wc_api_keys WHERE key_hash = $1 AND revoked_at IS NULL`
	mock.ExpectQuery(query).WithArgs("x").WillReturnError(errors.New("fixture database failure"))
	row, err := repo.GetByHash(ctx, "x")
	require.Error(t, err)
	require.Nil(t, row)
	mock.ExpectQuery(query).WithArgs("x").WillReturnRows(wcRows("key", "invalid bigint", "label", "x", "prefix", int64(1), nil, nil, "", nil, "user", nil))
	row, err = repo.GetByHash(ctx, "x")
	require.Error(t, err)
	require.Nil(t, row)
	mock.ExpectQuery(query).WithArgs("x").WillReturnRows(wcRows("key", int64(0), "label", "x", "prefix", int64(1), nil, nil, "", nil, "user", nil))
	row, err = repo.GetByHash(ctx, "x")
	require.Error(t, err)
	require.Nil(t, row)
	require.Error(t, repo.Insert(ctx, nil))
	require.Error(t, repo.Insert(ctx, &WebCodexAPIKey{ID: "id", UserID: -1}))
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	row, err = repo.GetByHash(cancelled, "x")
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, row)
	require.ErrorIs(t, repo.UpdateLastUsed(cancelled, "x", 1), context.Canceled)
	require.ErrorIs(t, repo.Insert(cancelled, &WebCodexAPIKey{}), context.Canceled)
	_, err = repo.Revoke(cancelled, "x", 1)
	require.ErrorIs(t, err, context.Canceled)
	_, err = repo.GetByID(cancelled, "x")
	require.ErrorIs(t, err, context.Canceled)
	_, err = NewWebCodexAPIKeyRepository(nil).GetByHash(ctx, "x")
	require.ErrorIs(t, err, runner.ErrAgentCredentialUnavailable)
}

type wcUserFixture struct {
	service.UserRepository
	get func(context.Context, int64) (*service.User, error)
}

func (f wcUserFixture) GetByID(ctx context.Context, id int64) (*service.User, error) {
	return f.get(ctx, id)
}

func TestWebCodexHostUserResolver(t *testing.T) {
	deleted := time.Unix(1, 0)
	for _, tc := range []struct {
		name        string
		user        *service.User
		err         error
		active      bool
		unavailable bool
	}{
		{name: "active profile has no authority", user: &service.User{ID: 7, Username: "renamed", Status: service.StatusActive, Role: service.RoleAdmin, Balance: -1, AllowedGroups: []int64{99}}, active: true},
		{name: "disabled", user: &service.User{ID: 7, Status: "disabled"}},
		{name: "deleted even when returned", user: &service.User{ID: 7, Status: service.StatusActive, DeletedAt: &deleted}},
		{name: "wrong ID", user: &service.User{ID: 8, Status: service.StatusActive}},
		{name: "missing", err: service.ErrUserNotFound},
		{name: "nil row"},
		{name: "private error", err: errors.New("secret fixture failure"), unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolve := NewWebCodexHostUserResolver(wcUserFixture{get: func(ctx context.Context, id int64) (*service.User, error) {
				require.Equal(t, int64(7), id)
				return tc.user, tc.err
			}})
			user, err := resolve(context.Background(), 7)
			if tc.unavailable {
				require.ErrorIs(t, err, runner.ErrAgentCredentialUnavailable)
				require.NotContains(t, err.Error(), "secret")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.active, user.Active)
		})
	}
	resolve := NewWebCodexHostUserResolver(nil)
	_, err := resolve(context.Background(), 7)
	require.ErrorIs(t, err, runner.ErrAgentCredentialUnavailable)
	_, err = resolve(context.Background(), 0)
	require.ErrorIs(t, err, runner.ErrAgentCredentialInvalid)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = resolve(ctx, 7)
	require.ErrorIs(t, err, context.Canceled)
}

func TestWebCodexPersistedAuthenticationFixture(t *testing.T) {
	// Test-only UTF-8 token bytes; SQL rows are fabricated, never real credentials.
	const token = " fixture-é-token "
	digest := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(digest[:])
	repo, mock := webCodexMock(t)
	enabled := true
	resolve := NewWebCodexHostUserResolver(wcUserFixture{get: func(_ context.Context, id int64) (*service.User, error) {
		require.Equal(t, int64(7), id)
		status := service.StatusActive
		if !enabled {
			status = "disabled"
		}
		return &service.User{ID: id, Status: status, Username: "mutable name", Role: service.RoleAdmin}, nil
	}})
	auth, err := runner.NewAgentTokenAuthenticator(repo, resolve, runner.AgentTokenOptions{MaxTokenBytes: 1024, Now: func() time.Time { return time.Unix(40, 0) }})
	require.NoError(t, err)
	query := `SELECT ` + webCodexKeyColumns + ` FROM wc_api_keys WHERE key_hash = $1 AND revoked_at IS NULL`
	for _, kind := range []string{"agent", "user", ""} {
		mock.ExpectQuery(query).WithArgs(hash).WillReturnRows(wcRows("key", int64(7), "name", hash, "prefix", int64(1), nil, nil, "agent:poll\u0085future:scope agent:poll admin", int64(41), kind, "client"))
		mock.ExpectExec(`UPDATE wc_api_keys SET last_used_at = $2 WHERE id = $1`).WithArgs("key", int64(40)).WillReturnError(errors.New("ignored touch failure"))
		principal, err := auth(context.Background(), token)
		require.NoError(t, err)
		require.Equal(t, "sub2api_user_7", principal.Username)
		require.Equal(t, []string{"agent:poll", "future:scope", "agent:poll", "admin"}, principal.Scopes)
		if kind == "agent" {
			require.Equal(t, runner.AgentToken, principal.Kind)
		} else {
			require.EqualValues(t, "api_token", principal.Kind)
		}
	}
	enabled = false
	mock.ExpectQuery(query).WithArgs(hash).WillReturnRows(wcRows("key", int64(7), "name", hash, "prefix", int64(1), nil, nil, "agent:poll", nil, "agent", "client"))
	_, err = auth(context.Background(), token)
	require.ErrorIs(t, err, runner.ErrAgentCredentialInvalid)
	enabled = true
	mock.ExpectQuery(query).WithArgs(hash).WillReturnRows(wcRows())
	_, err = auth(context.Background(), token)
	require.ErrorIs(t, err, runner.ErrAgentCredentialInvalid)
}
