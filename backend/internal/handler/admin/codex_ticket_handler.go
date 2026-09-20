package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Status providers must return a credential-free public DTO. Set during wiring,
// before serving requests; no controller is started by this handler.
type CodexTicketStatusProvider func(context.Context, service.CodexTicketSettings) (any, error)
type CodexTicketHandler struct {
	repo    service.CodexTicketSettingsRepository
	proxies service.ProxyRepository
	status  CodexTicketStatusProvider
}

func NewCodexTicketHandler(repo service.CodexTicketSettingsRepository, proxies service.ProxyRepository) *CodexTicketHandler {
	return &CodexTicketHandler{repo: repo, proxies: proxies}
}
func (h *CodexTicketHandler) SetStatusProvider(provider CodexTicketStatusProvider) {
	h.status = provider
}

type codexTicketProxySummary struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	State    string `json:"state"`
}
type codexTicketSettingsResponse struct {
	Settings           service.CodexTicketSettings `json:"settings"`
	SelectedProxy      *codexTicketProxySummary    `json:"selected_proxy"`
	Runtime            any                         `json:"runtime"`
	RuntimeErrorReason *string                     `json:"runtime_error_reason"`
}

const codexTicketControlUnavailable = "CODEX_TICKET_CONTROL_UNAVAILABLE"

func (h *CodexTicketHandler) view(ctx context.Context, cfg service.CodexTicketSettings) codexTicketSettingsResponse {
	out := codexTicketSettingsResponse{Settings: cfg}
	if cfg.HarvestProxyID != nil {
		out.SelectedProxy = &codexTicketProxySummary{ID: *cfg.HarvestProxyID, State: "missing"}
		if h.proxies != nil {
			p, err := h.proxies.GetByID(ctx, *cfg.HarvestProxyID)
			if err == nil && p != nil {
				out.SelectedProxy = &codexTicketProxySummary{ID: p.ID, Name: p.Name, Protocol: p.Protocol, Host: p.Host, Port: p.Port, State: service.CodexTicketProxyState(p, time.Now())}
			}
			if err != nil && !errors.Is(err, service.ErrProxyNotFound) {
				out.SelectedProxy.State = "unavailable"
			}
		}
	}
	reason := codexTicketControlUnavailable
	if h.status == nil {
		out.RuntimeErrorReason = &reason
		return out
	}
	runtime, err := h.status(ctx, cfg)
	if err != nil {
		out.RuntimeErrorReason = &reason
		return out
	}
	out.Runtime = runtime
	return out
}
func (h *CodexTicketHandler) Get(c *gin.Context) {
	cfg, err := h.repo.Load(c.Request.Context())
	if codexTicketRespondError(c, err) {
		return
	}
	response.Success(c, h.view(c.Request.Context(), cfg))
}
func (h *CodexTicketHandler) Status(c *gin.Context) {
	cfg, err := h.repo.Load(c.Request.Context())
	if codexTicketRespondError(c, err) {
		return
	}
	out := h.view(c.Request.Context(), cfg)
	if out.RuntimeErrorReason != nil {
		response.ErrorWithDetails(c, 503, "runtime status unavailable", codexTicketControlUnavailable, nil)
		return
	}
	response.Success(c, out.Runtime)
}
func (h *CodexTicketHandler) Put(c *gin.Context) {
	expected, cfg, err := decodeCodexTicketPut(http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024))
	if err != nil {
		response.ErrorWithDetails(c, 400, "invalid complete settings body", "CODEX_TICKET_INVALID_BODY", nil)
		return
	}
	cfg, err = service.NormalizeCodexTicketSettings(cfg)
	if codexTicketRespondError(c, err) {
		return
	}
	cfg, err = h.repo.Save(c.Request.Context(), expected, cfg)
	if codexTicketRespondError(c, err) {
		return
	}
	response.Success(c, h.view(c.Request.Context(), cfg))
}

// Avoid logging raw storage/driver errors, which may contain JSON or credentials.
func codexTicketRespondError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	status := infraerrors.FromError(err)
	response.ErrorWithDetails(c, int(status.Code), status.Message, status.Reason, status.Metadata)
	return true
}
func decodeCodexTicketPut(body io.Reader) (uint64, service.CodexTicketSettings, error) {
	var cfg service.CodexTicketSettings
	invalid := errors.New("invalid complete settings body")
	var outer map[string]json.RawMessage
	dec := json.NewDecoder(body)
	if err := dec.Decode(&outer); err != nil {
		return 0, cfg, invalid
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return 0, cfg, invalid
	}
	if len(outer) != 2 || outer["expected_revision"] == nil || outer["settings"] == nil {
		return 0, cfg, invalid
	}
	var rev string
	if err := json.Unmarshal(outer["expected_revision"], &rev); err != nil || rev == "" {
		return 0, cfg, invalid
	}
	for _, c := range rev {
		if c < '0' || c > '9' {
			return 0, cfg, invalid
		}
	}
	expected, err := strconv.ParseUint(rev, 10, 64)
	if err != nil || expected > service.CodexTicketMaxRevision {
		return 0, cfg, invalid
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(outer["settings"], &fields); err != nil {
		return 0, cfg, invalid
	}
	required := []string{"schema_version", "enabled", "harvest_proxy_id", "models", "target_length", "ttl_seconds", "refresh_before_seconds", "missing_ticket_policy", "max_concurrency", "max_probes_per_minute"}
	if len(fields) != len(required) {
		return 0, cfg, invalid
	}
	for _, key := range required {
		v, ok := fields[key]
		if !ok || (key != "harvest_proxy_id" && bytes.Equal(bytes.TrimSpace(v), []byte("null"))) {
			return 0, cfg, invalid
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(outer["settings"]))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return 0, cfg, invalid
	}
	return expected, cfg, nil
}
