package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// This error is deliberately constant: repository and URL errors can contain
// proxy credentials. Callers must never return those errors or the policy hash.
var ErrCodexTicketPolicyUnavailable = errors.New("codex ticket account policy unavailable")

// CodexTicketPolicyScope binds state to explicit plan and business route only.
// Account ID, upstream identity, model and revision remain separate key parts.
// An empty scope always means unusable policy, never a direct-route fallback.
func CodexTicketPolicyScope(a *Account, now time.Time) string {
	plan := CodexTicketPlan(a)
	if plan == "" {
		return ""
	}
	policy := struct {
		Version                    int
		Plan, Route                string
		ProxyID                    int64
		Protocol, Host             string
		Port                       int
		Username, Password, Status string
		ExpiresAt                  *time.Time
	}{Version: 1, Plan: plan, Route: "direct"}
	if a.ProxyID == nil {
		if a.Proxy != nil {
			return ""
		}
	} else {
		p := a.Proxy
		if *a.ProxyID <= 0 || p == nil || p.ID != *a.ProxyID || !p.IsActive() || p.IsExpired(now) || !validCodexTicketBusinessProxy(p) {
			return ""
		}
		policy.Route = "proxy"
		policy.ProxyID = *a.ProxyID
		policy.Protocol = p.Protocol
		policy.Host = p.Host
		policy.Port = p.Port
		policy.Username = p.Username
		policy.Password = p.Password
		policy.Status = p.Status
		if p.ExpiresAt != nil {
			expiry := p.ExpiresAt.UTC()
			policy.ExpiresAt = &expiry
		}
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validCodexTicketBusinessProxy(p *Proxy) bool {
	if p.Port < 1 || p.Port > 65535 || p.Host == "" || strings.TrimSpace(p.Host) != p.Host || strings.ContainsAny(p.Host, "/\\?#@[]%") {
		return false
	}
	for _, r := range p.Host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	// Unbracketed IPv6 is accepted by Proxy.URL's net.JoinHostPort. Other
	// colon-bearing hosts would create an ambiguous or invalid endpoint.
	if strings.Contains(p.Host, ":") && net.ParseIP(p.Host) == nil {
		return false
	}
	raw := p.URL()
	if !validCodexProbeProxy(raw) {
		return false
	}
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Hostname() == p.Host && parsed.Port() == strconv.Itoa(p.Port) && parsed.Path == ""
}

// resolveCodexTicketAccountPolicy hydrates a fresh fixed proxy when a repository
// is available. A failed read never falls back to the potentially stale preload.
// With no repository, a complete preload may be validated for isolated callers.
// Neither the input account nor a repository-owned Proxy is modified.
func resolveCodexTicketAccountPolicy(ctx context.Context, proxies ProxyRepository, a *Account, now time.Time) (*Account, string, string, error) {
	if a == nil || CodexTicketPlan(a) == "" || ctx == nil || ctx.Err() != nil {
		return nil, "", "", ErrCodexTicketPolicyUnavailable
	}
	started := time.Now()
	hydrated := *a
	if a.ProxyID != nil {
		id := *a.ProxyID
		if id <= 0 {
			return nil, "", "", ErrCodexTicketPolicyUnavailable
		}
		hydrated.ProxyID = &id
		p := a.Proxy
		if proxies != nil {
			var err error
			p, err = proxies.GetByID(ctx, id)
			if err != nil || ctx.Err() != nil {
				return nil, "", "", ErrCodexTicketPolicyUnavailable
			}
		}
		if p == nil {
			return nil, "", "", ErrCodexTicketPolicyUnavailable
		}
		proxyCopy := *p
		if p.ExpiresAt != nil {
			expiry := *p.ExpiresAt
			proxyCopy.ExpiresAt = &expiry
		}
		hydrated.Proxy = &proxyCopy
	}
	scope := CodexTicketPolicyScope(&hydrated, now.Add(time.Since(started)))
	if scope == "" {
		return nil, "", "", ErrCodexTicketPolicyUnavailable
	}
	businessProxyURL := ""
	if hydrated.Proxy != nil {
		businessProxyURL = hydrated.Proxy.URL()
	}
	return &hydrated, scope, businessProxyURL, nil
}

func (r *CodexTicketRuntime) resolveCodexTicketPolicy(ctx context.Context, a *Account, now time.Time) (*Account, string, string, error) {
	if r == nil {
		return nil, "", "", ErrCodexTicketPolicyUnavailable
	}
	return resolveCodexTicketAccountPolicy(ctx, r.proxies, a, now)
}
