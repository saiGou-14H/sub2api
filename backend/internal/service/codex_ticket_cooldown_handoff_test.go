//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ticketRestrictionFailureStore struct {
	*ticketCooldownStore
	failWrite       bool
	writes, retries int
	writeErr        error
	remaining       time.Duration
}

func (s *ticketRestrictionFailureStore) ExtendProbeCooldown(ctx context.Context, owner string, k CodexTicketKey, until time.Time) error {
	s.writes++
	s.writeErr = ctx.Err()
	if deadline, ok := ctx.Deadline(); ok {
		s.remaining = time.Until(deadline)
	}
	if s.failWrite {
		return errors.New("redis unavailable")
	}
	return s.ticketCooldownStore.ExtendProbeCooldown(ctx, owner, k, until)
}
func (s *ticketRestrictionFailureStore) WriteRetry(context.Context, string, CodexTicketKey, CodexTicketRetry) error {
	s.retries++
	return nil
}

func TestCodexTicketObservedRejectionSurvivesCancellation(t *testing.T) {
	for _, scenario := range []string{"generation", "disabled", "ordinary-cancel"} {
		t.Run(scenario, func(t *testing.T) {
			r, accounts, base, key := ticketRuntimeFixture()
			s := &ticketRestrictionFailureStore{ticketCooldownStore: &ticketCooldownStore{ticketTestStore: base}}
			r.cache = s
			generation, cancel := context.WithCancel(context.Background())
			defer cancel()
			r.harvest(generation, "owner", base.v.Control.Settings, "http://proxy", key, func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
				if scenario == "disabled" {
					accounts.mu.Lock()
					accounts.a.Extra = map[string]any{CodexTurnStateEnabledExtraKey: false}
					accounts.mu.Unlock()
				}
				cancel()
				if scenario == "ordinary-cancel" {
					return CodexTicketProbeResult{}, context.Canceled
				}
				return CodexTicketProbeResult{HTTPStatus: 429, RetryAfter: "3600"}, errors.New("http_status")
			})
			require.Zero(t, s.retries)
			require.Zero(t, base.commits)
			if scenario == "ordinary-cancel" {
				require.Zero(t, s.writes)
				require.Empty(t, r.lastError)
				return
			}
			require.Equal(t, 1, s.writes)
			require.NoError(t, s.writeErr)
			require.Positive(t, s.remaining)
			require.LessOrEqual(t, s.remaining, 2*time.Second)
			accounts.mu.Lock()
			accounts.a.Extra = map[string]any{CodexTurnStateEnabledExtraKey: true}
			accounts.mu.Unlock()
			next := key
			next.Revision++
			next.Model = "other"
			r.harvest(context.Background(), "new-owner", base.v.Control.Settings, "http://new-proxy", next, func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
				t.Fatal("new admission after observed rejection")
				return CodexTicketProbeResult{}, nil
			})
			require.Equal(t, 1, s.budgets)
		})
	}
}
func TestCodexTicketFailedRestrictionPublicationStopsNewAdmissions(t *testing.T) {
	r, _, base, key := ticketRuntimeFixture()
	s := &ticketRestrictionFailureStore{ticketCooldownStore: &ticketCooldownStore{ticketTestStore: base}, failWrite: true}
	r.cache = s
	generation, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.harvest(generation, "owner", base.v.Control.Settings, "http://proxy", key, func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
		cancel()
		return CodexTicketProbeResult{HTTPStatus: 429, RetryAfter: "3600"}, errors.New("http_status")
	})
	require.Equal(t, "retry_unavailable", r.lastError)
	require.Zero(t, s.retries)
	next := key
	next.Revision++
	next.Model = "other"
	r.harvest(context.Background(), "new-owner", base.v.Control.Settings, "http://new-proxy", next, func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
		t.Fatal("publication failure bypassed")
		return CodexTicketProbeResult{}, nil
	})
	require.Equal(t, 1, s.budgets)
	other := key
	other.IdentityScope = "other-identity"
	blocked, err := r.probeRestrictionActive(context.Background(), other)
	require.NoError(t, err)
	require.True(t, blocked, "unpublished rejection must stop all identities")
	s.failWrite = false
	blocked, err = r.probeRestrictionActive(context.Background(), next)
	require.NoError(t, err)
	require.True(t, blocked)
	require.Empty(t, r.pendingCooldowns)
}

func TestCodexTicketUnpublishedRejectionPausesOtherAccountUntilRecovery(t *testing.T) {
	r, accounts, base, key := ticketRuntimeFixture()
	s := &ticketRestrictionFailureStore{ticketCooldownStore: &ticketCooldownStore{ticketTestStore: base}, failWrite: true}
	r.cache = s
	require.False(t, r.recordObservedProbeRestriction(context.Background(), key, CodexTicketProbeResult{HTTPStatus: 429, RetryAfter: "3600"}, base.v.ServerTime))
	accounts.mu.Lock()
	accounts.a.ID++
	other := key
	other.AccountID = accounts.a.ID
	accounts.mu.Unlock()
	called := false
	probe := func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
		called = true
		return CodexTicketProbeResult{HTTPStatus: 503}, errors.New("ordinary failure")
	}
	r.harvest(context.Background(), "owner", base.v.Control.Settings, "http://proxy", other, probe)
	require.False(t, called)
	require.Zero(t, s.budgets)
	require.Len(t, r.pendingCooldowns, 1)
	s.failWrite = false
	r.harvest(context.Background(), "owner", base.v.Control.Settings, "http://proxy", other, probe)
	require.True(t, called)
	require.Equal(t, 1, s.budgets)
	require.Empty(t, r.pendingCooldowns)
	blocked, err := r.probeRestrictionActive(context.Background(), key)
	require.NoError(t, err)
	require.True(t, blocked, "publishing A must preserve its cooldown while B resumes")
}
