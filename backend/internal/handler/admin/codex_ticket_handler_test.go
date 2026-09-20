package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func codexTicketValidBody(t *testing.T) string {
	t.Helper()
	cfg := service.DefaultCodexTicketSettings()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	delete(fields, "revision")
	raw, err = json.Marshal(map[string]any{"expected_revision": "0", "settings": fields})
	require.NoError(t, err)
	return string(raw)
}
func TestCodexTicketPutPresenceAndStrictBody(t *testing.T) {
	valid := codexTicketValidBody(t)
	_, cfg, err := decodeCodexTicketPut(strings.NewReader(valid))
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	require.Nil(t, cfg.HarvestProxyID)
	for _, body := range []string{"{}", valid + " {}", strings.Replace(valid, `"enabled":false,`, "", 1), strings.Replace(valid, `"enabled":false`, `"enabled":null`, 1), strings.Replace(valid, `"harvest_proxy_id":null,`, "", 1), strings.Replace(valid, `"models":`, `"unknown":0,"models":`, 1), strings.Replace(valid, `"expected_revision":"0"`, `"expected_revision":0`, 1), strings.Replace(valid, `"expected_revision":"0"`, `"expected_revision":"9007199254740992"`, 1)} {
		_, _, err := decodeCodexTicketPut(strings.NewReader(body))
		require.Error(t, err, body)
	}
}

type codexTicketHandlerRepo struct {
	cfg   service.CodexTicketSettings
	saves int
}

func (r *codexTicketHandlerRepo) Load(context.Context) (service.CodexTicketSettings, error) {
	return r.cfg, nil
}
func (r *codexTicketHandlerRepo) Save(_ context.Context, expected uint64, cfg service.CodexTicketSettings) (service.CodexTicketSettings, error) {
	r.saves++
	cfg.Revision = expected + 1
	r.cfg = cfg
	return cfg, nil
}
func TestCodexTicketPutCommittedRuntimeFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &codexTicketHandlerRepo{cfg: service.DefaultCodexTicketSettings()}
	h := NewCodexTicketHandler(repo, nil)
	h.SetStatusProvider(func(context.Context, service.CodexTicketSettings) (any, error) {
		return nil, errors.New("password=never-return-this")
	})
	router := gin.New()
	router.PUT("/settings", h.Put)
	router.GET("/status", h.Status)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/settings", strings.NewReader(codexTicketValidBody(t)))
	router.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	require.Equal(t, 1, repo.saves)
	require.Contains(t, rec.Body.String(), `"revision":"1"`)
	require.Contains(t, rec.Body.String(), `"runtime":null`)
	require.Contains(t, rec.Body.String(), codexTicketControlUnavailable)
	require.NotContains(t, rec.Body.String(), "never-return-this")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/status", nil))
	require.Equal(t, 503, rec.Code)
}

type codexTicketProxyRepo struct {
	service.ProxyRepository
	proxy *service.Proxy
}

func (r codexTicketProxyRepo) GetByID(context.Context, int64) (*service.Proxy, error) {
	return r.proxy, nil
}
func TestCodexTicketSelectedProxyNeverSerializesCredentials(t *testing.T) {
	id := int64(7)
	cfg := service.DefaultCodexTicketSettings()
	cfg.HarvestProxyID = &id
	h := NewCodexTicketHandler(&codexTicketHandlerRepo{}, codexTicketProxyRepo{proxy: &service.Proxy{ID: id, Name: "selected", Protocol: "http", Host: "proxy.invalid", Port: 8080, Status: service.StatusActive, Username: "secret-user", Password: "secret-password"}})
	raw, err := json.Marshal(h.view(context.Background(), cfg))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"state":"active"`)
	require.NotContains(t, string(raw), "secret-user")
	require.NotContains(t, string(raw), "secret-password")
	require.NotContains(t, string(raw), "username")
	require.NotContains(t, string(raw), "password")
}

func TestCodexTicketNoStatusProviderIsUnavailable(t *testing.T) {
	h := NewCodexTicketHandler(&codexTicketHandlerRepo{}, nil)
	out := h.view(context.Background(), service.DefaultCodexTicketSettings())
	require.Nil(t, out.Runtime)
	require.NotNil(t, out.RuntimeErrorReason)
}
