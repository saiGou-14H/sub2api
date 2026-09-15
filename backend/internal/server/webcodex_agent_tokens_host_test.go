//go:build unit

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type managementTestOptions struct {
	cfg      *config.Config
	settings *service.SettingService
	redis    *redis.Client
	audit    *service.AuditLogService
}

type managementSettings struct {
	service.SettingRepository
	values map[string]string
}

func (s *managementSettings) GetValue(_ context.Context, key string) (string, error) {
	v, ok := s.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return v, nil
}
func (s *managementSettings) Get(_ context.Context, key string) (*service.Setting, error) {
	v, ok := s.values[key]
	if !ok {
		return nil, service.ErrSettingNotFound
	}
	return &service.Setting{Key: key, Value: v}, nil
}
func (s *managementSettings) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}
func (s *managementSettings) SetMultiple(_ context.Context, values map[string]string) error {
	for k, v := range values {
		s.values[k] = v
	}
	return nil
}
func managementHostSettings(t *testing.T, backend bool) *service.SettingService {
	t.Helper()
	repo := &managementSettings{values: map[string]string{}}
	settings := service.NewSettingService(repo, &config.Config{})
	require.NoError(t, settings.UpdateSettings(context.Background(), &service.SystemSettings{BackendModeEnabled: backend}))
	require.NoError(t, settings.SetPanelRateLimitSettings(context.Background(), &service.PanelRateLimitSettings{Enabled: true, UserRPM: 1, HeavyRPM: 1, PublicIPRPM: 1, ExemptAdmin: true}))
	t.Cleanup(func() {
		require.NoError(t, settings.UpdateSettings(context.Background(), &service.SystemSettings{BackendModeEnabled: false}))
	})
	return settings
}

// Redis hooks execute only the real limiter's command boundary; no socket opens.
type managementRedis struct {
	count int64
	keys  []string
}

func (*managementRedis) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("unexpected network access in management fixture")
	}
}
func (h *managementRedis) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		switch cmd.Name() {
		case "evalsha", "eval":
			h.count++
			h.keys = append(h.keys, cmd.Args()[3].(string))
			cmd.(*redis.Cmd).SetVal([]any{h.count, int64(0)})
		case "pttl":
			cmd.(*redis.DurationCmd).SetVal(time.Minute)
		default:
			return errors.New("unexpected Redis command in management fixture")
		}
		return nil
	}
}
func (*managementRedis) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(context.Context, []redis.Cmder) error {
		return errors.New("unexpected pipeline in management fixture")
	}
}
func managementRedisClient(t *testing.T) (*redis.Client, *managementRedis) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "fixture.invalid:1", MaxRetries: -1})
	hook := &managementRedis{}
	client.AddHook(hook)
	t.Cleanup(func() { _ = client.Close() })
	return client, hook
}

type managementAudit struct {
	service.AuditLogRepository
	mu   sync.Mutex
	logs []*service.AuditLog
}

func (r *managementAudit) BatchInsert(_ context.Context, logs []*service.AuditLog) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, logs...)
	return int64(len(logs)), nil
}
func managementAuditService(t *testing.T) (*service.AuditLogService, *managementAudit) {
	t.Helper()
	repo := &managementAudit{}
	audit := service.NewAuditLogService(repo, nil)
	audit.Start()
	t.Cleanup(audit.Stop)
	return audit, repo
}

func TestWebCodexAgentTokenHostPanelPolicy(t *testing.T) {
	settings := managementHostSettings(t, true)
	client, redisHook := managementRedisClient(t)
	audit, records := managementAuditService(t)
	f := newManagementFixture(t, true, managementTestOptions{settings: settings, redis: client, audit: audit})
	f.users.users[42].Role = service.RoleAdmin
	staleAdmin := f.jwt(t, 42)
	f.users.users[42].Role = service.RoleUser
	f.send(t, "create", staleAdmin, managementCreate, 403)
	require.Zero(t, redisHook.count, "backend mode rejects before limiter")
	require.Equal(t, 1, f.users.calls, "backend mode uses current JWT lookup role before handler")
	f.users.users[42].Role = service.RoleAdmin
	f.mock.ExpectQuery(regexp.QuoteMeta(managementListSQL)).WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows(managementSafeColumns))
	f.send(t, "list", staleAdmin, `{"username":"sub2api_user_42"}`, 200)
	require.Zero(t, redisHook.count, "current admin exemption is the existing host policy")
	// Restore normal panel mode; one user's shared bucket covers every management action.
	require.NoError(t, settings.UpdateSettings(context.Background(), &service.SystemSettings{BackendModeEnabled: false}))
	f.users.users[42].Role = service.RoleUser
	f.mock.ExpectQuery(regexp.QuoteMeta(managementListSQL)).WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows(managementSafeColumns))
	f.send(t, "list", staleAdmin, `{"username":"sub2api_user_42"}`, 200)
	for _, action := range []string{"create", "register_hash", "revoke"} {
		f.send(t, action, staleAdmin, managementCreate, 429)
	}
	require.Equal(t, int64(4), redisHook.count)
	for _, key := range redisHook.keys {
		require.Equal(t, "rate_limit:panel:global:user:42", key)
	}
	// The polling ingress still reaches its own Runner auth, not panel backend/limiter.
	require.NoError(t, settings.UpdateSettings(context.Background(), &service.SystemSettings{BackendModeEnabled: true}))
	require.Equal(t, 401, webCodexSend(f.router, http.MethodPost, "/api/shell/agent/register", staleAdmin, webCodexRegistration).Code)
	require.Equal(t, int64(4), redisHook.count)
	audit.Stop()
	records.mu.Lock()
	defer records.mu.Unlock()
	require.Len(t, records.logs, 2, "only requests admitted by backend mode and limiter reach audit")
	for _, entry := range records.logs {
		require.Equal(t, int64(42), *entry.ActorUserID)
		require.Equal(t, 200, entry.StatusCode)
		require.Equal(t, "<credential-bearing body omitted>", entry.RequestBody)
	}
}

func TestWebCodexAgentTokenBearerOnlyAndNoStore(t *testing.T) {
	f := newManagementFixture(t, true)
	token := f.jwt(t, 42)
	for _, mode := range []string{"cookie", "query", "form", "basic", "invalid", "empty"} {
		path := "/api/agent-tokens/create"
		body := managementCreate
		if mode == "query" {
			path += "?access_token=" + token
		}
		if mode == "form" {
			body = "access_token=" + token
		}
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Origin", "https://foreign.example")
		switch mode {
		case "cookie":
			req.Header.Set("Cookie", "access_token="+token+"; jwt="+token+"; session="+token)
		case "basic":
			req.Header.Set("Authorization", "Basic "+token)
		case "invalid":
			req.Header.Set("Authorization", "Bearer fabricated-invalid-jwt")
		case "empty":
			req.Header.Set("Authorization", "Bearer ")
		}
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		require.Equal(t, 401, rec.Code)
		require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		require.False(t, strings.Contains(rec.Body.String(), token))
	}
	require.Zero(t, f.users.calls)
}

func TestWebCodexAgentTokenConfiguredBodyBound(t *testing.T) {
	for _, limit := range []int64{512, 128 * 1024} {
		cfg := webCodexTestConfig()
		cfg.WebCodexRunner.MaxBodyBytes = limit
		f := newManagementFixture(t, true, managementTestOptions{cfg: cfg})
		token := f.jwt(t, 42)
		body := `{"username":"sub2api_user_42"}`
		body += strings.Repeat(" ", int(limit)-len(body))
		f.mock.ExpectQuery(regexp.QuoteMeta(managementListSQL)).WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows(managementSafeColumns))
		f.send(t, "list", token, body, 200)
		for _, action := range []string{"create", "register_hash", "list", "revoke"} {
			f.send(t, action, token, body+" ", 413)
		}
	}
}

func TestWebCodexAgentTokenCreationAuditDoesNotRecordSecret(t *testing.T) {
	audit, records := managementAuditService(t)
	f := newManagementFixture(t, true, managementTestOptions{audit: audit})
	jwt := f.jwt(t, 42)
	var id, hash, prefix string
	const secretName = "fabricated-sensitive-name-never-audited"
	f.mock.ExpectExec(`INSERT INTO wc_api_keys`).WithArgs(managementCapture{&id}, int64(42), secretName, managementCapture{&hash}, managementCapture{&prefix}, sqlmock.AnyArg(), nil, nil, strings.Join(defaultAgentTokenScopes(), " "), nil, "agent", "node-a").WillReturnResult(sqlmock.NewResult(0, 1))
	created := f.send(t, "create", jwt, `{"username":"sub2api_user_42","client_id":"node-a","name":"`+secretName+`"}`, 200)
	plaintext := managementString(t, created, "token")
	audit.Stop()
	records.mu.Lock()
	defer records.mu.Unlock()
	require.Len(t, records.logs, 1)
	entry := records.logs[0]
	require.Equal(t, int64(42), *entry.ActorUserID)
	require.Equal(t, 200, entry.StatusCode)
	require.True(t, entry.RequestBody == "<credential-bearing body omitted>")
	raw, err := json.Marshal(entry)
	require.NoError(t, err)
	for _, secret := range []string{plaintext, hash, secretName, jwt} {
		require.False(t, strings.Contains(string(raw), secret), "audit exposed a fabricated secret")
	}
}
