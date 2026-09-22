package service

import (
	"context"
	"strconv"
	"time"
)

// Observation storage is optional and never participates in the request policy.
type CodexTicketObservationStore interface {
	RecordCodexTicketDecision(context.Context, CodexTicketKey, string, string, string) error
	ReadCodexTicketAccounts(context.Context, []CodexTicketKey) (map[CodexTicketKey]CodexTicketAccountSnapshot, error)
}
type CodexTicketMetadata struct {
	CapturedAt, ExpiresAt          time.Time
	VerifiedAt                     time.Time
	ActualModel, VerificationModel string
}
type CodexTicketObservation struct {
	InjectionCount         int64      `json:"injection_count"`
	LastInjectedAt         *time.Time `json:"last_injected_at"`
	LastRequestID          *string    `json:"last_request_id"`
	LastOutcome            *string    `json:"last_outcome"`
	LastReason             *string    `json:"last_reason"`
	InvalidationCount      int64      `json:"invalidation_count"`
	LastInvalidatedAt      *time.Time `json:"last_invalidated_at"`
	LastInvalidationReason *string    `json:"last_invalidation_reason"`
}
type CodexTicketAccountSnapshot struct {
	Ticket        *CodexTicket
	Metadata      *CodexTicketMetadata
	Retry         CodexTicketRetry
	CooldownUntil time.Time
	Observation   CodexTicketObservation
	Unavailable   bool
}
type CodexTicketModelStatus struct {
	Model             string     `json:"model"`
	Status            string     `json:"status"`
	CapturedAt        *time.Time `json:"captured_at"`
	ExpiresAt         *time.Time `json:"expires_at"`
	RefreshAt         *time.Time `json:"refresh_at"`
	NextAttemptAt     *time.Time `json:"next_attempt_at"`
	LastErrorCode     *string    `json:"last_error_code"`
	Verified          bool       `json:"verified"`
	VerifiedAt        *time.Time `json:"verified_at"`
	ActualModel       *string    `json:"actual_model"`
	VerificationModel *string    `json:"verification_model"`
	CodexTicketObservation
}
type CodexTicketAccountStatus struct {
	AccountID    int64                    `json:"account_id"`
	Enabled      bool                     `json:"enabled"`
	Supported    bool                     `json:"supported"`
	Plan         string                   `json:"plan"`
	TargetLength int                      `json:"target_length"`
	Status       string                   `json:"status"`
	Models       []CodexTicketModelStatus `json:"models"`
}
type CodexTicketAccountsStatus struct {
	DesiredRevision string                     `json:"desired_revision"`
	AppliedRevision *string                    `json:"applied_revision"`
	GlobalEnabled   bool                       `json:"global_enabled"`
	ProxyState      string                     `json:"proxy_state"`
	ServerTime      time.Time                  `json:"server_time"`
	CounterScope    string                     `json:"counter_scope"`
	Accounts        []CodexTicketAccountStatus `json:"accounts"`
}

type codexTicketStatusPolicy struct {
	account  *Account
	settings CodexTicketSettings
	scope    string
	failure  string
}

func (r *CodexTicketRuntime) AccountStatuses(ctx context.Context, ids []int64) (CodexTicketAccountsStatus, error) {
	out := CodexTicketAccountsStatus{ProxyState: "unavailable", ServerTime: time.Now().UTC(), CounterScope: "shared_cache_window", Accounts: make([]CodexTicketAccountStatus, 0, len(ids))}
	if len(ids) == 0 || len(ids) > 100 {
		return out, ErrCodexTicketControlUnavailable
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return out, ErrCodexTicketControlUnavailable
		}
		seen[id] = true
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cfg, err := r.settings.Load(ctx)
	if err != nil || len(cfg.Models) == 0 || len(cfg.Models) > 16 {
		return out, ErrCodexTicketControlUnavailable
	}
	out.DesiredRevision = strconv.FormatUint(cfg.Revision, 10)
	out.GlobalEnabled = cfg.Enabled
	accounts, err := r.accounts.GetByIDs(ctx, ids)
	if err != nil {
		return out, ErrCodexTicketControlUnavailable
	}
	byID := map[int64]*Account{}
	for _, a := range accounts {
		if a != nil && seen[a.ID] {
			byID[a.ID] = a
		}
	}
	view, controlErr := r.cache.ReadForRequest(ctx, 0, "", "")
	observedAt := time.Now()
	fresh := controlErr == nil && !view.ServerTime.IsZero() && view.Control.ValidUntilMS > view.ServerTime.UnixMilli()
	if fresh {
		out.ServerTime = view.ServerTime.UTC()
		rev := strconv.FormatUint(view.Control.Settings.Revision, 10)
		out.AppliedRevision = &rev
		out.ProxyState = view.Control.ProxyState
	}
	switch out.ProxyState {
	case "active", "inactive", "expired", "missing", "unsupported", "unavailable":
	default:
		fresh = false
		out.AppliedRevision = nil
		out.ProxyState = "unavailable"
	}
	clockBase := out.ServerTime
	current := fresh && cfg.Revision == view.Control.Settings.Revision
	policies := make(map[int64]codexTicketStatusPolicy, len(ids))
	keys := make([]CodexTicketKey, 0, len(ids)*len(cfg.Models))
	for _, id := range ids {
		a := byID[id]
		p := codexTicketStatusPolicy{account: a}
		if a == nil {
			policies[id] = p
			continue
		}
		p.settings, err = CodexTicketAccountSettings(cfg, a)
		if err != nil {
			p.failure = "unavailable"
			policies[id] = p
			continue
		}
		if current && cfg.Enabled && CodexTicketAccountSupported(a) && a.CodexTurnStateEnabled() && CodexTicketIdentityScope(a) != "" {
			// GetByIDs already bulk-preloads this DB snapshot's Proxy relation.
			// Validate it without per-account proxy queries; absent or stale routes
			// remain unusable rather than falling back to direct transport.
			hydrated, scope, _, policyErr := resolveCodexTicketAccountPolicy(ctx, nil, a, clockBase.Add(time.Since(observedAt)))
			if policyErr != nil || hydrated == nil || scope == "" {
				p.failure = "proxy_unavailable"
				if ctx.Err() != nil {
					p.failure = "unavailable"
				}
			} else {
				p.account = hydrated
				p.scope = scope
				for _, model := range cfg.Models {
					if hydrated.IsSchedulableForModelWithContext(ctx, model) {
						keys = append(keys, CodexTicketKey{Revision: cfg.Revision, AccountID: id, IdentityScope: CodexTicketIdentityScope(hydrated), Model: model, PolicyScope: scope})
					}
				}
			}
		}
		policies[id] = p
	}
	snapshots := map[CodexTicketKey]CodexTicketAccountSnapshot{}
	var readErr error
	if len(keys) > 0 {
		if store, ok := r.cache.(CodexTicketObservationStore); ok {
			snapshots, readErr = store.ReadCodexTicketAccounts(ctx, keys)
		} else {
			readErr = ErrCodexTicketControlUnavailable
		}
	}
	out.ServerTime = clockBase.Add(time.Since(observedAt))
	if fresh && (ctx.Err() != nil || view.Control.ValidUntilMS <= out.ServerTime.UnixMilli()) {
		fresh = false
		out.AppliedRevision = nil
		out.ProxyState = "unavailable"
	}
	for _, id := range ids {
		p := policies[id]
		a := p.account
		row := CodexTicketAccountStatus{AccountID: id, Plan: "inherit", Status: "unavailable", Models: make([]CodexTicketModelStatus, 0, len(cfg.Models))}
		if a != nil {
			row.Supported = CodexTicketAccountSupported(a)
			row.Enabled = a.CodexTurnStateEnabled()
			if plan := CodexTicketPlan(a); plan != "" {
				row.Plan = plan
			}
			row.TargetLength = p.settings.TargetLength
		}
		for _, model := range cfg.Models {
			m := CodexTicketModelStatus{Model: model, Status: "unavailable"}
			switch {
			case a == nil:
			case !row.Supported:
				m.Status = "unsupported"
			case !row.Enabled:
				m.Status = "disabled"
			case !fresh:
			case !current:
				m.Status = "waiting"
			case !cfg.Enabled:
				m.Status = "disabled"
			case p.failure != "":
				m.Status = p.failure
			case CodexTicketIdentityScope(a) == "" || !a.IsSchedulableForModelWithContext(ctx, model):
				m.Status = "inactive"
			case p.scope == "" || CodexTicketPolicyScope(a, out.ServerTime) != p.scope:
				m.Status = "proxy_unavailable"
			case readErr != nil:
			default:
				key := CodexTicketKey{Revision: cfg.Revision, AccountID: id, IdentityScope: CodexTicketIdentityScope(a), Model: model, PolicyScope: p.scope}
				snapshot, exists := snapshots[key]
				if exists {
					m = codexTicketSnapshotStatus(snapshot, key, p.settings, out.ServerTime)
				}
				if m.Status != "unavailable" && (out.ProxyState != "active" || (view.Control.ProxyExpiresAtMS > 0 && view.Control.ProxyExpiresAtMS <= out.ServerTime.UnixMilli())) {
					m.Status = "proxy_unavailable"
					m.Verified = false
				}
			}
			row.Models = append(row.Models, m)
		}
		row.Status = codexTicketAggregateStatus(row.Models)
		out.Accounts = append(out.Accounts, row)
	}
	// Include all qualification and rendering preparation in the final clock.
	// No policy/route whose deadline elapsed while another account was processed
	// can retain a ready claim or expose its old observations.
	out.ServerTime = clockBase.Add(time.Since(observedAt))
	controlExpired := fresh && (ctx.Err() != nil || view.Control.ValidUntilMS <= out.ServerTime.UnixMilli())
	proxyExpired := fresh && view.Control.ProxyExpiresAtMS > 0 && view.Control.ProxyExpiresAtMS <= out.ServerTime.UnixMilli()
	if controlExpired {
		out.AppliedRevision = nil
		out.ProxyState = "unavailable"
	} else if proxyExpired {
		out.ProxyState = "expired"
	}
	for i := range out.Accounts {
		row := &out.Accounts[i]
		p := policies[row.AccountID]
		routeExpired := p.scope != "" && CodexTicketPolicyScope(p.account, out.ServerTime) != p.scope
		for j := range row.Models {
			m := &row.Models[j]
			if row.Supported && row.Enabled && (controlExpired || (current && cfg.Enabled && routeExpired)) {
				state := "proxy_unavailable"
				if controlExpired {
					state = "unavailable"
				}
				*m = CodexTicketModelStatus{Model: m.Model, Status: state}
				continue
			}
			if m.NextAttemptAt != nil && !m.NextAttemptAt.After(out.ServerTime) {
				m.NextAttemptAt = nil
			}
			switch m.Status {
			case "ready", "refreshing", "backoff", "invalidated", "expired", "waiting":
				if proxyExpired && current && cfg.Enabled {
					m.Status = "proxy_unavailable"
					m.Verified = false
					continue
				}
			}
			switch m.Status {
			case "ready", "refreshing":
				if m.ExpiresAt == nil || !m.ExpiresAt.After(out.ServerTime) {
					m.Verified = false
					m.Status = codexTicketNoCurrentTicketStatus(*m, out.ServerTime)
				} else if m.RefreshAt != nil && !m.RefreshAt.After(out.ServerTime) {
					m.Status = "refreshing"
				}
			case "backoff":
				if m.NextAttemptAt == nil {
					m.Status = codexTicketNoCurrentTicketStatus(*m, out.ServerTime)
				}
			}
		}
		row.Status = codexTicketAggregateStatus(row.Models)
	}
	return out, nil
}

func codexTicketMetadataValid(meta *CodexTicketMetadata, key CodexTicketKey, cfg CodexTicketSettings, now time.Time) bool {
	if meta == nil {
		return true
	}
	if meta.CapturedAt.IsZero() || !meta.ExpiresAt.After(meta.CapturedAt) || meta.CapturedAt.After(now.Add(5*time.Second)) || meta.ExpiresAt.Sub(meta.CapturedAt) > time.Duration(cfg.TTLSeconds)*time.Second {
		return false
	}
	if meta.VerifiedAt.IsZero() {
		return meta.ActualModel == "" && meta.VerificationModel == ""
	}
	return !meta.VerifiedAt.Before(meta.CapturedAt) && !meta.VerifiedAt.After(meta.ExpiresAt) && !meta.VerifiedAt.After(now.Add(5*time.Second)) && meta.ActualModel == key.Model && meta.VerificationModel == key.Model
}

func codexTicketSnapshotStatus(snapshot CodexTicketAccountSnapshot, key CodexTicketKey, cfg CodexTicketSettings, now time.Time) CodexTicketModelStatus {
	m := CodexTicketModelStatus{Model: key.Model, Status: "unavailable"}
	if snapshot.Unavailable || !codexTicketMetadataValid(snapshot.Metadata, key, cfg, now) {
		return m
	}
	meta := snapshot.Metadata
	if t := snapshot.Ticket; t != nil {
		if t.Key != key {
			return m
		}
		meta = &CodexTicketMetadata{CapturedAt: t.CapturedAt, ExpiresAt: t.ExpiresAt, VerifiedAt: t.VerifiedAt, ActualModel: t.ActualModel, VerificationModel: t.VerificationModel}
		if !codexTicketMetadataValid(meta, key, cfg, now) {
			return m
		}
	}
	obs := snapshot.Observation
	if obs.InvalidationCount < 0 {
		return m
	}
	if obs.InvalidationCount > 0 {
		if obs.LastInvalidatedAt == nil || obs.LastInvalidatedAt.IsZero() || obs.LastInvalidatedAt.After(now.Add(5*time.Second)) || obs.LastInvalidationReason == nil {
			return m
		}
		switch *obs.LastInvalidationReason {
		case "model_mismatch", "state_312":
		default:
			return m
		}
	} else if obs.LastInvalidatedAt != nil || obs.LastInvalidationReason != nil {
		return m
	}
	m.CodexTicketObservation = obs
	next := snapshot.Retry.NextAttemptAt
	if snapshot.CooldownUntil.After(next) {
		next = snapshot.CooldownUntil
	}
	if next.After(now) {
		next = next.UTC()
		m.NextAttemptAt = &next
	}
	if snapshot.Retry.ErrorCode != "" {
		code := codexTicketPublicError(snapshot.Retry.ErrorCode)
		m.LastErrorCode = &code
	}
	if meta != nil {
		captured, expiry := meta.CapturedAt.UTC(), meta.ExpiresAt.UTC()
		refresh := codexTicketRefreshAt(captured, expiry, cfg)
		m.CapturedAt = &captured
		m.ExpiresAt = &expiry
		m.RefreshAt = &refresh
		if !meta.VerifiedAt.IsZero() {
			verified := meta.VerifiedAt.UTC()
			actual, verification := meta.ActualModel, meta.VerificationModel
			m.VerifiedAt = &verified
			m.ActualModel = &actual
			m.VerificationModel = &verification
		}
	}
	if snapshot.Ticket != nil && ValidateCodexTicket(*snapshot.Ticket, key, cfg, now) == nil {
		m.Verified = true
		m.Status = "ready"
		if m.RefreshAt != nil && !m.RefreshAt.After(now) {
			m.Status = "refreshing"
		}
		return m
	}
	m.Status = codexTicketNoCurrentTicketStatus(m, now)
	if m.Status == "waiting" && snapshot.Ticket != nil {
		m.Status = "unavailable"
	}
	return m
}

func codexTicketNoCurrentTicketStatus(m CodexTicketModelStatus, now time.Time) string {
	if m.NextAttemptAt != nil && m.NextAttemptAt.After(now) {
		return "backoff"
	}
	if m.InvalidationCount > 0 && m.LastInvalidatedAt != nil && (m.CapturedAt == nil || !m.LastInvalidatedAt.Before(*m.CapturedAt)) {
		return "invalidated"
	}
	if m.ExpiresAt != nil && !m.ExpiresAt.After(now) {
		return "expired"
	}
	return "waiting"
}

func codexTicketAggregateStatus(models []CodexTicketModelStatus) string {
	for _, state := range []string{"unavailable", "unsupported", "disabled", "inactive", "proxy_unavailable", "backoff", "invalidated", "expired", "refreshing", "waiting", "ready"} {
		for _, m := range models {
			if m.Status == state {
				return state
			}
		}
	}
	return "unavailable"
}
func codexTicketPublicError(code string) string {
	switch code {
	case "probe_timeout", "probe_failed", "authentication_failed", "upstream_unavailable", "state_mismatch", "transport_unsupported", "identity_unresolved", "token_unavailable", "stream_failed", "proxy_unavailable", "rate_limited", "model_mismatch", "verification_failed", "state_312", "cookie_missing":
		return code
	}
	return "probe_failed"
}
