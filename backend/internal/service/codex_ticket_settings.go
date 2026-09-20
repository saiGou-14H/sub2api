package service

import (
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"strings"
	"time"
)

var (
	ErrCodexTicketInvalidConfig    = infraerrors.New(422, "CODEX_TICKET_INVALID_CONFIG", "invalid Codex ticket configuration")
	ErrCodexTicketProxyRequired    = infraerrors.New(422, "CODEX_TICKET_PROXY_REQUIRED", "harvest proxy must be selected")
	ErrCodexTicketProxyUnavailable = infraerrors.New(422, "CODEX_TICKET_PROXY_UNAVAILABLE", "harvest proxy unavailable")
	ErrCodexTicketRevisionConflict = infraerrors.New(409, "CODEX_TICKET_REVISION_CONFLICT", "configuration revision changed")
	ErrCodexTicketProxyInUse       = infraerrors.New(409, "CODEX_TICKET_PROXY_IN_USE", "proxy is selected for Codex ticket harvesting")
)

func DefaultCodexTicketSettings() CodexTicketSettings {
	return CodexTicketSettings{SchemaVersion: 1, Models: []string{"gpt-6-astra", "gpt-5.6-sol"}, TargetLength: 292, TTLSeconds: 3600, RefreshBeforeSeconds: 600, MissingPolicy: CodexTicketPassthrough, MaxConcurrency: 2, MaxProbesPerMinute: 12}
}

func NormalizeCodexTicketSettings(in CodexTicketSettings) (CodexTicketSettings, error) {
	out := in
	if in.HarvestProxyID != nil {
		id := *in.HarvestProxyID
		out.HarvestProxyID = &id
	}
	out.Models = make([]string, 0, len(in.Models))
	seen := make(map[string]bool)
	for _, raw := range in.Models {
		model := strings.TrimSpace(raw)
		if model == "" || len(model) > 128 || strings.ContainsAny(model, "\r\n\x00") {
			return out, ErrCodexTicketInvalidConfig
		}
		if !seen[model] {
			out.Models = append(out.Models, model)
			seen[model] = true
		}
	}
	if out.SchemaVersion != 1 || out.Revision > CodexTicketMaxRevision || len(out.Models) == 0 || len(out.Models) > 16 {
		return out, ErrCodexTicketInvalidConfig
	}
	if out.Enabled && out.HarvestProxyID == nil {
		return out, ErrCodexTicketProxyRequired
	}
	if out.HarvestProxyID != nil && *out.HarvestProxyID <= 0 {
		return out, ErrCodexTicketInvalidConfig
	}
	if out.TargetLength < 64 || out.TargetLength > 4096 || out.TTLSeconds < 60 || out.TTLSeconds > 3600 || out.RefreshBeforeSeconds < 0 || out.RefreshBeforeSeconds >= out.TTLSeconds {
		return out, ErrCodexTicketInvalidConfig
	}
	if out.MissingPolicy != CodexTicketPassthrough && out.MissingPolicy != CodexTicketReject {
		return out, ErrCodexTicketInvalidConfig
	}
	if out.MaxConcurrency < 1 || out.MaxConcurrency > 8 || out.MaxProbesPerMinute < 1 || out.MaxProbesPerMinute > 60 {
		return out, ErrCodexTicketInvalidConfig
	}
	return out, nil
}

// CodexTicketProxyState returns only a safe status; it never exposes connection credentials.
func CodexTicketProxyState(p *Proxy, now time.Time) string {
	if p == nil {
		return "missing"
	}
	switch p.Protocol {
	case "http", "https", "socks5", "socks5h":
	default:
		return "unsupported"
	}
	if p.IsExpired(now) || p.Status == StatusExpired {
		return "expired"
	}
	if !p.IsActive() {
		return "inactive"
	}
	return "active"
}
