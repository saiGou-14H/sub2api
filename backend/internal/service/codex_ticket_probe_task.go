package service

import (
	"context"
	"errors"
)

type codexTicketProbeTaskKey struct{}
type codexTicketProbeTask struct {
	Key      CodexTicketKey
	Settings CodexTicketSettings
	// consume=true immediately before the verification request, false afterward.
	VerificationGuard func(context.Context, bool) (*Account, string, error)
}

func (r *CodexTicketRuntime) codexVerificationGuard(owner string, cfg CodexTicketSettings, k CodexTicketKey) func(context.Context, bool) (*Account, string, error) {
	return func(ctx context.Context, consume bool) (*Account, string, error) {
		fail := func() (*Account, string, error) { return nil, "", errors.New("verification_unavailable") }
		if ctx.Err() != nil || !r.codexTicketAccountCurrent(ctx, k) {
			return fail()
		}
		desired, err := r.settings.Load(ctx)
		if err != nil || !desired.Enabled || desired.Revision != k.Revision || !codexTicketModelEnabled(desired, k.Model) {
			return fail()
		}
		current, err := r.cache.CheckCurrent(ctx, owner, k.Revision)
		if err != nil || !current {
			return fail()
		}
		blocked, err := r.probeRestrictionActive(ctx, k)
		if err != nil || blocked {
			return fail()
		}
		now, err := r.cache.Now(ctx)
		if err != nil {
			return fail()
		}
		a, err := r.accounts.GetByID(ctx, k.AccountID)
		if err != nil {
			return fail()
		}
		a, policy, route, err := r.resolveCodexTicketPolicy(ctx, a, now)
		if err != nil || !a.CodexTurnStateEnabled() || CodexTicketIdentityScope(a) != k.IdentityScope || policy != k.PolicyScope {
			return fail()
		}
		if consume {
			ok, err := r.cache.AcquireProbeBudget(ctx, owner, k.Revision, cfg.MaxProbesPerMinute)
			if err != nil || !ok {
				return fail()
			}
			current, err = r.cache.CheckCurrent(ctx, owner, k.Revision)
			if err != nil || !current {
				return fail()
			}
			blocked, err = r.probeRestrictionActive(ctx, k)
			if err != nil || blocked {
				return fail()
			}
		}
		return a, route, nil
	}
}
