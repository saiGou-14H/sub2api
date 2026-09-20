package service

import (
	"context"
	"time"
)

func codexProbeRestrictionDelay(result CodexTicketProbeResult, now time.Time) time.Duration {
	auth := result.HTTPStatus == 401 || result.HTTPStatus == 403
	quota := result.ErrorCode == "quota_limited"
	if !auth && !quota && result.HTTPStatus != 429 {
		return 0
	}
	delay := CodexTicketRetryDelay(1, parseCodexRetryAfter(result.RetryAfter, now), 0)
	if (auth || quota) && delay < time.Hour {
		delay = time.Hour
	}
	return delay
}

func codexProbeRestrictionKey(k CodexTicketKey) CodexTicketKey {
	return CodexTicketKey{AccountID: k.AccountID, IdentityScope: k.IdentityScope}
}

// Only bookkeeping may outlive request cancellation. No account eligibility
// check is appropriate here: a real rejection still applies after re-enabling
// the same identity. referenceTime is Redis time advanced by monotonic elapsed.
func (r *CodexTicketRuntime) recordObservedProbeRestriction(parent context.Context, k CodexTicketKey, result CodexTicketProbeResult, referenceTime time.Time) bool {
	delay := codexProbeRestrictionDelay(result, referenceTime)
	if delay == 0 {
		return true
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Second)
	defer cancel()
	key := codexProbeRestrictionKey(k)
	until := referenceTime.Add(delay)
	r.mu.Lock()
	if r.pendingCooldowns == nil {
		r.pendingCooldowns = make(map[CodexTicketKey]time.Time)
	}
	if until.After(r.pendingCooldowns[key]) {
		r.pendingCooldowns[key] = until
	}
	r.mu.Unlock()
	return r.flushProbeRestriction(ctx, key)
}

// If Redis publication fails, retain the restriction locally. Any pending
// publication stops all new probe admissions, bounding this set by the workers
// already in flight rather than by the number of scanned accounts.
func (r *CodexTicketRuntime) flushProbeRestriction(ctx context.Context, k CodexTicketKey) bool {
	key := codexProbeRestrictionKey(k)
	r.mu.Lock()
	until, exists := r.pendingCooldowns[key]
	r.mu.Unlock()
	if !exists {
		return true
	}
	return r.flushProbeRestrictionSnapshot(ctx, key, until)
}

func (r *CodexTicketRuntime) flushProbeRestrictionSnapshot(ctx context.Context, key CodexTicketKey, until time.Time) bool {
	if err := r.cache.ExtendProbeCooldown(ctx, "", key, until); err != nil {
		if ctx.Err() == nil || ctx.Err() == context.DeadlineExceeded {
			r.setError("retry_unavailable")
		}
		return false
	}
	r.mu.Lock()
	if r.pendingCooldowns[key].Equal(until) {
		delete(r.pendingCooldowns, key)
	}
	r.mu.Unlock()
	return true
}

func (r *CodexTicketRuntime) probeRestrictionActive(ctx context.Context, k CodexTicketKey) (bool, error) {
	r.mu.Lock()
	pending := make(map[CodexTicketKey]time.Time, len(r.pendingCooldowns))
	for key, until := range r.pendingCooldowns {
		pending[key] = until
	}
	r.mu.Unlock()
	for key, until := range pending {
		if !r.flushProbeRestrictionSnapshot(ctx, key, until) {
			return true, nil
		}
	}
	// A concurrent in-flight response may have added or extended a restriction
	// after the snapshot. Leave it for the next bounded admission check.
	r.mu.Lock()
	remaining := len(r.pendingCooldowns) != 0
	r.mu.Unlock()
	if remaining {
		return true, nil
	}
	return r.cache.ProbeCooldownActive(ctx, k)
}
