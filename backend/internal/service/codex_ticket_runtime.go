package service

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

type CodexTicketRuntime struct {
	settings          CodexTicketSettingsRepository
	proxies           ProxyRepository
	accounts          AccountRepository
	cache             CodexTicketRuntimeStore
	mu                sync.Mutex
	probe             CodexTicketProbe
	pager             OAuthRefreshCandidatePager
	cancel            context.CancelFunc
	done              chan struct{}
	started           bool
	pending, inflight int
	ready             map[CodexTicketKey]time.Time
	lastError         string
	knownSettings     *CodexTicketSettings
	pendingCooldowns  map[CodexTicketKey]time.Time
}

func NewCodexTicketRuntime(s CodexTicketSettingsRepository, p ProxyRepository, a AccountRepository, c CodexTicketRuntimeStore) *CodexTicketRuntime {
	pager, _ := a.(OAuthRefreshCandidatePager)
	return &CodexTicketRuntime{settings: s, proxies: p, accounts: a, cache: c, pager: pager, ready: make(map[CodexTicketKey]time.Time)}
}
func (r *CodexTicketRuntime) SetProbe(p CodexTicketProbe) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probe = p
}
func (r *CodexTicketRuntime) SetCandidatePager(p OAuthRefreshCandidatePager) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pager = p
}
func (r *CodexTicketRuntime) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return
	}
	r.started = true
	ctx, r.cancel = context.WithCancel(ctx)
	r.done = make(chan struct{})
	go func() { defer close(r.done); r.run(ctx) }()
}
func (r *CodexTicketRuntime) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *CodexTicketRuntime) setError(code string) { r.mu.Lock(); r.lastError = code; r.mu.Unlock() }

// Apply re-reads the account immediately before making the decision. The caller
// supplies a supported HTTP path and final upstream model, after its echo guard.
func (r *CodexTicketRuntime) Apply(ctx context.Context, a *Account, model string, h http.Header) error {
	// Shared request builders also handle API-key, Web and Prism accounts.
	// They must exit before any repository/cache dependency or failure policy.
	if !CodexTicketAccountSupported(a) || h == nil {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	fresh, err := r.accounts.GetByID(readCtx, a.ID)
	cancel()
	if err != nil {
		return r.unavailableDecision(ctx, a, model)
	}
	if fresh == nil || !fresh.CodexTurnStateEnabled() || !CodexTicketAccountSupported(fresh) {
		return nil
	}
	scope := CodexTicketIdentityScope(fresh)
	// The request may already carry credentials from the supplied snapshot.
	// Never attach the new identity's ticket to an older identity's request.
	if scope != CodexTicketIdentityScope(a) {
		scope = ""
	}
	v, err := r.cache.ReadForRequest(ctx, fresh.ID, scope, model)
	if err != nil {
		return r.unavailableDecision(ctx, fresh, model)
	}
	c := v.Control
	cfg := c.Settings
	r.rememberSettings(cfg)
	if !cfg.Enabled || !codexTicketModelEnabled(cfg, model) {
		return nil
	}
	k := CodexTicketKey{cfg.Revision, fresh.ID, scope, model}
	outcome, reason := "skipped", "ticket_missing"
	defer func() { r.observeDecision(ctx, k, outcome, reason) }()
	now := v.ServerTime
	if now.IsZero() || c.ValidUntilMS <= now.UnixMilli() {
		reason = "control_unavailable"
		if string(cfg.MissingPolicy) == "reject" {
			outcome = "rejected"
			return ErrCodexTicketControlUnavailable
		}
		return nil
	}
	if c.ProxyState == "active" && (c.ProxyExpiresAtMS == 0 || c.ProxyExpiresAtMS > now.UnixMilli()) && v.Ticket != nil && ValidateCodexTicket(*v.Ticket, k, cfg, now) == nil {
		h.Set("X-Codex-Turn-State", v.Ticket.State)
		outcome, reason = "header_set", "ticket_ready"
		return nil
	}
	if c.ProxyState != "active" || (c.ProxyExpiresAtMS > 0 && c.ProxyExpiresAtMS <= now.UnixMilli()) {
		reason = "proxy_unavailable"
	}
	if string(cfg.MissingPolicy) == "reject" {
		outcome = "rejected"
		return ErrCodexTicketMissing
	}
	return nil
}

// On a degraded path, fail closed only for an explicitly opted-in account and
// a known enabled reject policy (prefer a fresh DB read). No stale ticket is ever injected.
func (r *CodexTicketRuntime) unavailableDecision(ctx context.Context, a *Account, model string) error {
	if a == nil || !a.CodexTurnStateEnabled() {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cfg, err := r.settings.Load(readCtx)
	if err == nil {
		r.rememberSettings(cfg)
	}
	r.mu.Lock()
	known := r.knownSettings
	r.mu.Unlock()
	if known != nil && known.Enabled && codexTicketModelEnabled(*known, model) && string(known.MissingPolicy) == "reject" {
		return ErrCodexTicketControlUnavailable
	}
	return nil
}

func (r *CodexTicketRuntime) rememberSettings(cfg CodexTicketSettings) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.knownSettings == nil || cfg.Revision >= r.knownSettings.Revision {
		cfg.Models = append([]string(nil), cfg.Models...)
		r.knownSettings = &cfg
	}
}

// countEligibleReady validates the local observed set against fresh account
// eligibility. All batches share one deadline; database failures are not empty
// successful snapshots. No mutex is held during database I/O.
func (r *CodexTicketRuntime) countEligibleReady(ctx context.Context, revision uint64, now time.Time) (int, error) {
	started := time.Now()
	r.mu.Lock()
	snapshot := make(map[CodexTicketKey]time.Time, len(r.ready))
	uniqueIDs := make(map[int64]struct{})
	for key, expiry := range r.ready {
		if key.Revision != revision || !expiry.After(now) {
			delete(r.ready, key)
			continue
		}
		snapshot[key] = expiry
		uniqueIDs[key.AccountID] = struct{}{}
	}
	r.mu.Unlock()
	if len(snapshot) == 0 {
		return 0, nil
	}
	ids := make([]int64, 0, len(uniqueIDs))
	for id := range uniqueIDs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	scopes := make(map[int64]string, len(ids))
	for start := 0; start < len(ids); start += 100 {
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
		accounts, err := r.accounts.GetByIDs(readCtx, ids[start:end])
		if err != nil || readCtx.Err() != nil {
			return 0, ErrCodexTicketControlUnavailable
		}
		for _, account := range accounts {
			if account != nil && account.CodexTurnStateEnabled() && CodexTicketAccountSupported(account) {
				if scope := CodexTicketIdentityScope(account); scope != "" {
					scopes[account.ID] = scope
				}
			}
		}
	}
	now = now.Add(time.Since(started))
	count := 0
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, observedExpiry := range snapshot {
		currentExpiry, exists := r.ready[key]
		if !exists {
			continue
		}
		if scopes[key.AccountID] != key.IdentityScope || !currentExpiry.After(now) {
			// A newer concurrent commit will be checked on the next status read.
			if currentExpiry.Equal(observedExpiry) {
				delete(r.ready, key)
			}
			continue
		}
		count++
	}
	return count, nil
}

func (r *CodexTicketRuntime) Status(ctx context.Context) (CodexTicketRuntimeStatus, error) {
	s := CodexTicketRuntimeStatus{Phase: "degraded", CounterScope: "local_instance_observed", SupportedTransports: []string{"responses_http", "chat_completions_http", "messages_http", "responses_websocket_http_bridge"}}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cfg, err := r.settings.Load(readCtx)
	if err != nil {
		return s, ErrCodexTicketControlUnavailable
	}
	r.rememberSettings(cfg)
	s.DesiredRevision = strconv.FormatUint(cfg.Revision, 10)
	v, err := r.cache.ReadForRequest(ctx, 0, "", "")
	if err != nil {
		return s, ErrCodexTicketControlUnavailable
	}
	rev := strconv.FormatUint(v.Control.Settings.Revision, 10)
	s.AppliedRevision = &rev
	s.ProxyState = &v.Control.ProxyState
	verifiedAt := time.Now()
	s.ReadyCount, err = r.countEligibleReady(ctx, cfg.Revision, v.ServerTime)
	if err != nil {
		return s, ErrCodexTicketControlUnavailable
	}
	v.ServerTime = v.ServerTime.Add(time.Since(verifiedAt))
	if v.Control.ValidUntilMS <= v.ServerTime.UnixMilli() {
		return s, ErrCodexTicketControlUnavailable
	}
	r.mu.Lock()
	s.PendingCount = r.pending
	s.InflightCount = r.inflight
	if r.lastError != "" {
		e := r.lastError
		s.LastErrorCode = &e
	}
	r.mu.Unlock()
	// A save can race the DB/control reads in either direction. Equality is
	// required before any ready/collecting claim, even when local tickets exist.
	switch {
	case cfg.Revision != v.Control.Settings.Revision:
		s.Phase = "applying"
	case !v.Control.Settings.Enabled:
		s.Phase = "disabled"
	case v.Control.ProxyState != "active" || (v.Control.ProxyExpiresAtMS > 0 && v.Control.ProxyExpiresAtMS <= v.ServerTime.UnixMilli()):
		s.Phase = "proxy_unavailable"
	case s.LastErrorCode != nil:
		s.Phase = "degraded"
	case s.InflightCount > 0:
		s.Phase = "collecting"
	case s.ReadyCount > 0:
		s.Phase = "ready"
	default:
		s.Phase = "waiting"
	}
	return s, nil
}
