package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/google/uuid"
)

func (r *CodexTicketRuntime) observeDecision(ctx context.Context, k CodexTicketKey, outcome, reason string) {
	store, ok := r.cache.(CodexTicketObservationStore)
	if !ok || k.IdentityScope == "" || k.PolicyScope == "" {
		return
	}
	id, _ := ctx.Value(ctxkey.ClientRequestID).(string)
	// Only the internal server-generated correlation context, never request headers.
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		id = ""
	}
	writeCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	_ = store.RecordCodexTicketDecision(writeCtx, k, outcome, reason, id)
}

// ObserveCompactSkip does not touch headers or change the account echo guard.
func (r *CodexTicketRuntime) ObserveCompactSkip(ctx context.Context, a *Account, model string) {
	if !CodexTicketAccountSupported(a) || !a.CodexTurnStateEnabled() {
		return
	}
	if _, ok := r.cache.(CodexTicketObservationStore); !ok {
		return
	}
	op, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	fresh, err := r.accounts.GetByID(op, a.ID)
	if err != nil || !CodexTicketAccountSupported(fresh) || !fresh.CodexTurnStateEnabled() || CodexTicketIdentityScope(fresh) == "" || CodexTicketIdentityScope(fresh) != CodexTicketIdentityScope(a) {
		return
	}
	fresh, policy, _, err := r.resolveCodexTicketPolicy(op, fresh, time.Now())
	if err != nil || CodexTicketPolicyScope(a, time.Now()) != policy {
		return
	}
	v, err := r.cache.ReadForRequest(op, a.ID, CodexTicketIdentityScope(fresh), model, policy)
	if err != nil || !v.Control.Settings.Enabled || !codexTicketModelEnabled(v.Control.Settings, model) || v.ServerTime.IsZero() || v.Control.ValidUntilMS <= v.ServerTime.UnixMilli() {
		return
	}
	if CodexTicketPolicyScope(fresh, v.ServerTime) != policy {
		return
	}
	r.observeDecision(op, CodexTicketKey{Revision: v.Control.Settings.Revision, AccountID: a.ID, IdentityScope: CodexTicketIdentityScope(fresh), Model: model, PolicyScope: policy}, "skipped", "compact")
}
