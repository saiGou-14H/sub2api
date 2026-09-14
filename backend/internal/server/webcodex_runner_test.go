//go:build unit

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/runner"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Frozen G2 fixture, not a claim that the migrating DSH implements these capabilities.
const webCodexRegistration = `{"client_id":"node-a","agent_instance_id":"process-a","agent_protocol_generation":2,"capabilities":{"shell":true,"file_read":true,"file_write":true,"artifact_export_chunk_read":true,"artifact_export_streaming_metadata":true,"structured_file_delete":true,"apply_text_edit_occurrence":true,"jobs":true,"async_jobs":true,"async_shell_jobs":true,"structured_validation_argv":true,"structured_cargo_test_count_assertion":true,"structured_go_test_json":true,"structured_go_test_tool":true,"structured_go_test_packages":true,"structured_process_argv":true,"structured_script_payload":true,"internal_posix_script":true,"structured_execution_jobs":true,"lsp_read_only_navigation":true,"lsp_call_hierarchy":true,"project_lifecycle":true,"project_path_registration":true}}`
const webCodexFixtureToken = "fabricated-runner-token-for-route-test"
const webCodexFixtureOwner = "sub2api_user_42"

func webCodexTestConfig() *config.Config {
	return &config.Config{WebCodexRunner: config.WebCodexRunnerConfig{Enabled: true,
		MaxRunners: 2, MaxPendingPerRunner: 4, OnlineWindowSeconds: 30, MaxBodyBytes: 8192, MaxTokenBytes: 1024}}
}

type webCodexTestUsers struct {
	service.UserRepository
	status string
}

func (u *webCodexTestUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	if id != 42 {
		return nil, service.ErrUserNotFound
	}
	return &service.User{ID: 42, Username: "mutable-display-name", Status: u.status}, nil
}

func webCodexSetup(t *testing.T) (*WebCodexRunner, *gin.Engine, sqlmock.Sqlmock, *webCodexTestUsers) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	users := &webCodexTestUsers{status: "active"}
	runtime, err := ProvideWebCodexRunner(webCodexTestConfig(), db, users)
	require.NoError(t, err)
	t.Cleanup(runtime.Close)
	router := gin.New()
	runtime.mount(router)
	return runtime, router, mock, users
}

func webCodexExpectCredential(mock sqlmock.Sqlmock, kind string, touch bool) {
	sum := sha256.Sum256([]byte(webCodexFixtureToken))
	hash := hex.EncodeToString(sum[:])
	rows := sqlmock.NewRows([]string{"id", "user_id", "name", "key_hash", "key_prefix", "created_at", "last_used_at", "revoked_at", "scopes", "expires_at", "kind", "allowed_client_id"}).
		AddRow("fixture-key", int64(42), "fixture", hash, "fixture", int64(1), nil, nil, "agent:register agent:poll agent:result", nil, kind, "node-a")
	mock.ExpectQuery(`SELECT .* FROM wc_api_keys WHERE key_hash = \$1 AND revoked_at IS NULL`).WithArgs(hash).WillReturnRows(rows)
	if touch {
		mock.ExpectExec(`UPDATE wc_api_keys SET last_used_at = \$2 WHERE id = \$1`).WithArgs("fixture-key", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

func webCodexSend(router http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestWebCodexRunnerDisabledHasNoRoutesOrDependencies(t *testing.T) {
	runtime, err := ProvideWebCodexRunner(&config.Config{}, nil, nil)
	require.NoError(t, err)
	require.Nil(t, runtime.registry)
	router := gin.New()
	runtime.mount(router)
	runtime.Close()
	runtime.Close()
	for _, action := range []string{"register", "poll", "result", "offline"} {
		require.Equal(t, http.StatusNotFound, webCodexSend(router, http.MethodPost, "/api/shell/agent/"+action, "", "{}").Code)
	}
}

func TestWebCodexRunnerCORSPreflightDoesNotEnableOrAuthenticate(t *testing.T) {
	const origin = "https://runner-fixture.example"
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			queries := 0
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_, _ string) error {
				queries++
				return nil
			})))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			// A trap expectation records an accidental lookup even if authentication
			// turns its empty result into the expected 401. It must remain unused.
			mock.ExpectQuery("unexpected credential lookup").WillReturnRows(sqlmock.NewRows([]string{"id"}))
			cfg := webCodexTestConfig()
			cfg.WebCodexRunner.Enabled = enabled
			runtime, err := ProvideWebCodexRunner(cfg, db, &webCodexTestUsers{status: "active"})
			require.NoError(t, err)
			t.Cleanup(runtime.Close)
			router := gin.New()
			router.Use(middleware.CORS(config.CORSConfig{AllowedOrigins: []string{origin}}))
			runtime.mount(router)
			for _, action := range []string{"register", "poll", "result", "offline"} {
				path := "/api/shell/agent/" + action
				preflight := httptest.NewRequest(http.MethodOptions, path, nil)
				preflight.Header.Set("Origin", origin)
				preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
				preflight.Header.Set("Access-Control-Request-Headers", "Authorization")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, preflight)
				require.Equal(t, http.StatusNoContent, rec.Code)
				require.Equal(t, origin, rec.Header().Get("Access-Control-Allow-Origin"))
				post := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
				post.Header.Set("Origin", origin)
				rec = httptest.NewRecorder()
				router.ServeHTTP(rec, post)
				expected := http.StatusNotFound
				if enabled {
					expected = http.StatusUnauthorized
				}
				require.Equal(t, expected, rec.Code)
			}
			require.Zero(t, queries, "preflight and unauthenticated POST must never query")
		})
	}
}

func TestWebCodexRunnerEnabledRequiresHostDependencies(t *testing.T) {
	_, err := ProvideWebCodexRunner(nil, nil, nil)
	require.Error(t, err)
	_, err = ProvideWebCodexRunner(webCodexTestConfig(), nil, nil)
	require.ErrorContains(t, err, "requires host database and users")
	cfg := webCodexTestConfig()
	cfg.WebCodexRunner.MaxTokenBytes = 0
	_, err = ProvideWebCodexRunner(cfg, nil, nil)
	require.ErrorContains(t, err, "max_token_bytes")
}

func TestWebCodexRunnerMountedOriginalLifecycle(t *testing.T) {
	runtime, router, mock, _ := webCodexSetup(t)
	webCodexExpectCredential(mock, "agent", true)
	registered := webCodexSend(router, http.MethodPost, "/api/shell/agent/register", webCodexFixtureToken, webCodexRegistration)
	require.Equal(t, http.StatusOK, registered.Code, registered.Body.String())
	require.Contains(t, registered.Body.String(), `"owner":"sub2api_user_42"`)
	pending, err := runtime.registry.Enqueue(runner.Access{Username: webCodexFixtureOwner}, protocol.RunnerRequest{
		RequestID: "request-a", ClientID: "node-a", Kind: "run_shell", Command: "printf fixture", TimeoutSecs: 1, RequestedBy: webCodexFixtureOwner, CreatedAt: 1})
	require.NoError(t, err)
	webCodexExpectCredential(mock, "agent", true)
	polled := webCodexSend(router, http.MethodPost, "/api/shell/agent/poll", webCodexFixtureToken, `{"client_id":"node-a","agent_instance_id":"process-a"}`)
	require.Equal(t, http.StatusOK, polled.Code, polled.Body.String())
	require.Contains(t, polled.Body.String(), `"request_id":"request-a"`)
	webCodexExpectCredential(mock, "agent", true)
	completed := webCodexSend(router, http.MethodPost, "/api/shell/agent/result", webCodexFixtureToken, `{"client_id":"node-a","agent_instance_id":"process-a","request_id":"request-a","exit_code":0,"stdout":"fixture","command_execution_state":"completed"}`)
	require.Equal(t, http.StatusOK, completed.Code, completed.Body.String())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outcome := pending.Wait(ctx)
	require.NoError(t, outcome.Err)
	require.True(t, outcome.Dispatched)
	require.Equal(t, "fixture", *outcome.Result.Stdout)
	webCodexExpectCredential(mock, "agent", true)
	offline := webCodexSend(router, http.MethodPost, "/api/shell/agent/offline", webCodexFixtureToken, `{"client_id":"node-a","agent_instance_id":"process-a"}`)
	require.Equal(t, http.StatusOK, offline.Code, offline.Body.String())
}

func TestWebCodexRunnerMountedAuthenticationAndProtocolRejections(t *testing.T) {
	_, router, mock, users := webCodexSetup(t)
	for _, action := range []string{"register", "poll", "result", "offline"} {
		path := "/api/shell/agent/" + action
		require.Equal(t, http.StatusUnauthorized, webCodexSend(router, http.MethodPost, path, "", "{}").Code)
		rec := webCodexSend(router, http.MethodGet, path, "", "")
		require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		require.Equal(t, "POST", rec.Header().Get("Allow"))
	}
	webCodexExpectCredential(mock, "user", true)
	require.Equal(t, http.StatusForbidden, webCodexSend(router, http.MethodPost, "/api/shell/agent/register", webCodexFixtureToken, webCodexRegistration).Code)
	webCodexExpectCredential(mock, "agent", true)
	partial := strings.Replace(webCodexRegistration, `"jobs":true`, `"jobs":false`, 1)
	require.Equal(t, http.StatusBadRequest, webCodexSend(router, http.MethodPost, "/api/shell/agent/register", webCodexFixtureToken, partial).Code)
	webCodexExpectCredential(mock, "agent", true)
	spoof := strings.Replace(webCodexRegistration, `"client_id":"node-a"`, `"client_id":"node-a","owner":"sub2api_user_99"`, 1)
	require.Equal(t, http.StatusForbidden, webCodexSend(router, http.MethodPost, "/api/shell/agent/register", webCodexFixtureToken, spoof).Code)
	webCodexExpectCredential(mock, "agent", true)
	require.Equal(t, http.StatusRequestEntityTooLarge, webCodexSend(router, http.MethodPost, "/api/shell/agent/register", webCodexFixtureToken, strings.Repeat("x", 8193)).Code)
	users.status = "disabled"
	webCodexExpectCredential(mock, "agent", false)
	require.Equal(t, http.StatusUnauthorized, webCodexSend(router, http.MethodPost, "/api/shell/agent/register", webCodexFixtureToken, webCodexRegistration).Code)
	mock.ExpectQuery(`SELECT .* FROM wc_api_keys`).WillReturnError(errors.New("fixture-private-database-error"))
	rec := webCodexSend(router, http.MethodPost, "/api/shell/agent/register", webCodexFixtureToken, webCodexRegistration)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.NotContains(t, rec.Body.String(), "fixture-private-database-error")
}

func TestWebCodexRunnerHTTPShutdownSettlesRegistryWaiters(t *testing.T) {
	runtime, router, mock, _ := webCodexSetup(t)
	webCodexExpectCredential(mock, "agent", true)
	require.Equal(t, http.StatusOK, webCodexSend(router, http.MethodPost, "/api/shell/agent/register", webCodexFixtureToken, webCodexRegistration).Code)
	pending, err := runtime.registry.Enqueue(runner.Access{Username: webCodexFixtureOwner}, protocol.RunnerRequest{
		RequestID: "request-stop", ClientID: "node-a", Kind: "run_shell", Command: "printf fixture", TimeoutSecs: 1, RequestedBy: webCodexFixtureOwner, CreatedAt: 1})
	require.NoError(t, err)
	srv := ProvideHTTPServer(webCodexTestConfig(), router, runtime)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))
	outcome := pending.Wait(ctx)
	require.ErrorIs(t, outcome.Err, runner.ErrClosed)
	require.False(t, outcome.Dispatched)
	runtime.Close()
	webCodexExpectCredential(mock, "agent", true)
	require.Equal(t, http.StatusServiceUnavailable, webCodexSend(router, http.MethodPost, "/api/shell/agent/register", webCodexFixtureToken, webCodexRegistration).Code)
}
