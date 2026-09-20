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

type CodexTicketMetadata struct{ CapturedAt, ExpiresAt time.Time }
type CodexTicketObservation struct {
	InjectionCount int64      `json:"injection_count"`
	LastInjectedAt *time.Time `json:"last_injected_at"`
	LastRequestID  *string    `json:"last_request_id"`
	LastOutcome    *string    `json:"last_outcome"`
	LastReason     *string    `json:"last_reason"`
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
	Model         string     `json:"model"`
	Status        string     `json:"status"`
	CapturedAt    *time.Time `json:"captured_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	RefreshAt     *time.Time `json:"refresh_at"`
	NextAttemptAt *time.Time `json:"next_attempt_at"`
	LastErrorCode *string    `json:"last_error_code"`
	CodexTicketObservation
}
type CodexTicketAccountStatus struct {
	AccountID int64                    `json:"account_id"`
	Enabled   bool                     `json:"enabled"`
	Supported bool                     `json:"supported"`
	Status    string                   `json:"status"`
	Models    []CodexTicketModelStatus `json:"models"`
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
	if err != nil || len(cfg.Models) > 16 {
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
		if a != nil {
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
	clockBase := out.ServerTime
	current := fresh && cfg.Revision == view.Control.Settings.Revision
	keys := make([]CodexTicketKey, 0, len(ids)*len(cfg.Models))
	for _, id := range ids {
		a := byID[id]
		if current && cfg.Enabled && CodexTicketAccountSupported(a) && a.CodexTurnStateEnabled() && CodexTicketIdentityScope(a) != "" {
			for _, m := range cfg.Models {
				if a.IsSchedulableForModelWithContext(ctx, m) {
					keys = append(keys, CodexTicketKey{cfg.Revision, id, CodexTicketIdentityScope(a), m})
				}
			}
		}
	}
	snapshots := map[CodexTicketKey]CodexTicketAccountSnapshot{}
	store, ok := r.cache.(CodexTicketObservationStore)
	var readErr error
	if len(keys) > 0 {
		if !ok {
			readErr = ErrCodexTicketControlUnavailable
		} else {
			snapshots, readErr = store.ReadCodexTicketAccounts(ctx, keys)
		}
	}
	out.ServerTime = out.ServerTime.Add(time.Since(observedAt))
	if fresh && view.Control.ValidUntilMS <= out.ServerTime.UnixMilli() {
		fresh = false
		out.AppliedRevision = nil
		out.ProxyState = "unavailable"
	}
	switch out.ProxyState {
	case "active", "inactive", "expired", "missing", "unsupported", "unavailable":
	default:
		out.ProxyState = "unavailable"
		fresh = false
		out.AppliedRevision = nil
	}
	for _, id := range ids {
		a := byID[id]
		row := CodexTicketAccountStatus{AccountID: id, Status: "unavailable", Models: make([]CodexTicketModelStatus, 0, len(cfg.Models))}
		if a != nil {
			row.Supported = CodexTicketAccountSupported(a)
			row.Enabled = a.CodexTurnStateEnabled()
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
			case CodexTicketIdentityScope(a) == "" || !a.IsSchedulableForModelWithContext(ctx, model):
				m.Status = "inactive"
			case readErr != nil:
			default:
				key := CodexTicketKey{cfg.Revision, id, CodexTicketIdentityScope(a), model}
				snapshot, exists := snapshots[key]
				if !exists || snapshot.Unavailable {
					break
				}
				validMetadata := func(meta *CodexTicketMetadata) bool {
					return meta == nil || (!meta.CapturedAt.IsZero() && meta.ExpiresAt.After(meta.CapturedAt) && !meta.CapturedAt.After(out.ServerTime.Add(5*time.Second)) && meta.ExpiresAt.Sub(meta.CapturedAt) <= time.Duration(cfg.TTLSeconds)*time.Second)
				}
				if !validMetadata(snapshot.Metadata) {
					break
				}
				if ticket := snapshot.Ticket; ticket != nil {
					// Validate expired raw payloads at their last valid instant so
					// expiry remains distinguishable from corrupted ticket data.
					validationTime := out.ServerTime
					if !ticket.ExpiresAt.After(validationTime) {
						validationTime = ticket.ExpiresAt.Add(-time.Nanosecond)
					}
					if !validMetadata(&CodexTicketMetadata{ticket.CapturedAt, ticket.ExpiresAt}) || ValidateCodexTicket(*ticket, key, cfg, validationTime) != nil {
						break
					}
				}
				m.CodexTicketObservation = snapshot.Observation
				next := snapshot.Retry.NextAttemptAt
				if snapshot.CooldownUntil.After(next) {
					next = snapshot.CooldownUntil
				}
				if next.After(out.ServerTime) {
					m.NextAttemptAt = &next
				}
				if snapshot.Retry.ErrorCode != "" {
					code := codexTicketPublicError(snapshot.Retry.ErrorCode)
					m.LastErrorCode = &code
				}
				meta := snapshot.Metadata
				if snapshot.Ticket != nil {
					meta = &CodexTicketMetadata{snapshot.Ticket.CapturedAt, snapshot.Ticket.ExpiresAt}
				}
				if meta != nil {
					captured, expiry := meta.CapturedAt.UTC(), meta.ExpiresAt.UTC()
					refresh := expiry.Add(-time.Duration(cfg.RefreshBeforeSeconds) * time.Second)
					m.CapturedAt = &captured
					m.ExpiresAt = &expiry
					m.RefreshAt = &refresh
				}
				switch {
				case !a.IsSchedulableForModelWithContext(ctx, model):
					m.Status = "inactive"
				case out.ProxyState != "active" || (view.Control.ProxyExpiresAtMS > 0 && view.Control.ProxyExpiresAtMS <= out.ServerTime.UnixMilli()):
					m.Status = "proxy_unavailable"
				case snapshot.Ticket != nil && ValidateCodexTicket(*snapshot.Ticket, key, cfg, out.ServerTime) == nil:
					m.Status = "ready"
					if m.RefreshAt != nil && !m.RefreshAt.After(out.ServerTime) {
						m.Status = "refreshing"
					}
				case m.NextAttemptAt != nil:
					m.Status = "backoff"
				case m.ExpiresAt != nil && !m.ExpiresAt.After(out.ServerTime):
					m.Status = "expired"
				case snapshot.Ticket != nil:
					m.Status = "unavailable"
				default:
					m.Status = "waiting"
				}
			}
			row.Models = append(row.Models, m)
		}
		row.Status = codexTicketAggregateStatus(row.Models)
		out.Accounts = append(out.Accounts, row)
	}
	// Account qualification and serialization preparation consume time too.
	// Recheck deadlines once more after all potentially expensive work.
	out.ServerTime = clockBase.Add(time.Since(observedAt))
	controlExpired := fresh && view.Control.ValidUntilMS <= out.ServerTime.UnixMilli()
	proxyExpired := fresh && view.Control.ProxyExpiresAtMS > 0 && view.Control.ProxyExpiresAtMS <= out.ServerTime.UnixMilli()
	if controlExpired {
		out.AppliedRevision = nil
		out.ProxyState = "unavailable"
	} else if proxyExpired {
		out.ProxyState = "expired"
	}
	for i := range out.Accounts {
		row := &out.Accounts[i]
		for j := range row.Models {
			m := &row.Models[j]
			if controlExpired && row.Supported && row.Enabled {
				*m = CodexTicketModelStatus{Model: m.Model, Status: "unavailable"}
				continue
			}
			if m.NextAttemptAt != nil && !m.NextAttemptAt.After(out.ServerTime) {
				m.NextAttemptAt = nil
			}
			switch m.Status {
			case "ready", "refreshing", "backoff", "expired", "waiting":
				if proxyExpired && current && cfg.Enabled {
					m.Status = "proxy_unavailable"
					continue
				}
			}
			switch m.Status {
			case "ready", "refreshing":
				if m.ExpiresAt == nil || !m.ExpiresAt.After(out.ServerTime) {
					m.Status = "expired"
					if m.NextAttemptAt != nil {
						m.Status = "backoff"
					}
				} else if m.RefreshAt != nil && !m.RefreshAt.After(out.ServerTime) {
					m.Status = "refreshing"
				}
			case "backoff":
				if m.NextAttemptAt == nil {
					m.Status = "waiting"
					if m.ExpiresAt != nil && !m.ExpiresAt.After(out.ServerTime) {
						m.Status = "expired"
					}
				}
			}
		}
		row.Status = codexTicketAggregateStatus(row.Models)
	}
	return out, nil
}

func codexTicketAggregateStatus(models []CodexTicketModelStatus) string {
	// Prefer actionable degradation to a partial ready claim.
	for _, state := range []string{"unavailable", "unsupported", "disabled", "inactive", "proxy_unavailable", "backoff", "expired", "refreshing", "waiting", "ready"} {
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
	case "probe_timeout", "probe_failed", "authentication_failed", "upstream_unavailable", "state_mismatch", "transport_unsupported", "identity_unresolved", "token_unavailable", "stream_failed", "proxy_unavailable", "rate_limited":
		return code
	}
	return "probe_failed"
}
