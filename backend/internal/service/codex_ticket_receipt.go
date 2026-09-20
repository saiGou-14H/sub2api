package service

import (
	"context"
	"time"
)

// A receipt exists only after server-side injection. It is not a public DTO.
type CodexTicketReceipt struct {
	Key        CodexTicketKey `json:"-"`
	CapturedAt time.Time      `json:"-"`
	state      string
}

type CodexTicketInvalidationStore interface {
	InvalidateCodexTicket(context.Context, CodexTicket, string) (bool, error)
}

func (r *CodexTicketRuntime) InvalidateReceipt(ctx context.Context, receipt CodexTicketReceipt, reason string) bool {
	if reason != "model_mismatch" && reason != "state_312" {
		return false
	}
	store, ok := r.cache.(CodexTicketInvalidationStore)
	if !ok || receipt.state == "" || receipt.CapturedAt.IsZero() {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	a, err := r.accounts.GetByID(ctx, receipt.Key.AccountID)
	if err != nil || !CodexTicketAccountSupported(a) || a.ID != receipt.Key.AccountID || !a.CodexTurnStateEnabled() || CodexTicketIdentityScope(a) != receipt.Key.IdentityScope {
		return false
	}
	a, policy, _, err := r.resolveCodexTicketPolicy(ctx, a, time.Now())
	if err != nil || policy != receipt.Key.PolicyScope {
		return false
	}
	cfg, err := r.settings.Load(ctx)
	if err != nil || !cfg.Enabled || cfg.Revision != receipt.Key.Revision || !codexTicketModelEnabled(cfg, receipt.Key.Model) {
		return false
	}
	// The bounded atomic mutation checks fresh shared control and model membership.
	// Do not precede it with the normal request-path Redis read: that client's
	// socket timeout may outlive this response observer's context deadline.
	if ctx.Err() != nil || CodexTicketPolicyScope(a, time.Now()) != policy {
		return false
	}
	matched, err := store.InvalidateCodexTicket(ctx, CodexTicket{Key: receipt.Key, State: receipt.state, CapturedAt: receipt.CapturedAt}, reason)
	if err != nil || !matched {
		return false
	}
	r.mu.Lock()
	delete(r.ready, receipt.Key)
	r.mu.Unlock()
	return true
}
