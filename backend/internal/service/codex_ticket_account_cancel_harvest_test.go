//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ticketCancelDuringStore struct {
	*ticketTimeoutStore
	cancel context.CancelFunc
}

func (s *ticketCancelDuringStore) CommitIfCurrent(ctx context.Context, _ string, _ CodexTicket) (bool, error) {
	s.cancel()
	return false, ctx.Err()
}
func (s *ticketCancelDuringStore) ExtendProbeCooldown(ctx context.Context, _ string, _ CodexTicketKey, _ time.Time) error {
	s.cancel()
	return ctx.Err()
}

func TestCodexTicketCancellationDuringStorageDoesNotReportFailure(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(map[bool]string{false: "cooldown", true: "commit"}[success], func(t *testing.T) {
			r, _, base, key := ticketRuntimeFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := &ticketCancelDuringStore{ticketTimeoutStore: &ticketTimeoutStore{ticketTestStore: base}, cancel: cancel}
			r.cache = store
			r.harvest(ctx, "owner", base.v.Control.Settings, "http://proxy", key, func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
				if success {
					return CodexTicketProbeResult{HTTPStatus: 200, Completed: true, IdentityScope: key.IdentityScope, State: base.v.Ticket.State, Cookies: append([]CodexTicketCookie(nil), base.v.Ticket.Cookies...), Verified: true, PolicyScope: key.PolicyScope, ActualModel: key.Model, VerificationModel: key.Model}, nil
				}
				return CodexTicketProbeResult{HTTPStatus: 429}, errors.New("synthetic limit")
			})
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			require.Zero(t, store.writes)
			require.Zero(t, store.commits)
			r.mu.Lock()
			defer r.mu.Unlock()
			require.Empty(t, r.lastError)
		})
	}
}

func TestCodexTicketHarvestAccountDisableCancelsWithoutRetry(t *testing.T) {
	r, repo, key := newTicketWatchFixture()
	base := r.cache.(*ticketTestStore)
	store := &ticketTimeoutStore{ticketTestStore: base}
	r.cache = store
	entered := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.harvest(context.Background(), "owner", base.v.Control.Settings, "http://proxy", key, func(ctx context.Context, _ int64, _, _ string) (CodexTicketProbeResult, error) {
			close(entered)
			<-ctx.Done()
			return CodexTicketProbeResult{}, ctx.Err()
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	repo.mu.Lock()
	repo.accounts[key.AccountID].Extra = map[string]any{CodexTurnStateEnabledExtraKey: false}
	repo.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("disabled account's in-flight probe was not canceled")
	}
	require.Zero(t, store.writes)
	require.Zero(t, store.commits)
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Empty(t, r.lastError)
}

func TestCodexTicketHarvestFailureAfterAccountDisableDoesNotPenalize(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "timeout"}[deadline], func(t *testing.T) {
			r, repo, key := newTicketWatchFixture()
			base := r.cache.(*ticketTestStore)
			store := &ticketTimeoutStore{ticketTestStore: base}
			r.cache = store
			task, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			r.harvestWithTaskContext(context.Background(), task, "owner", base.v.Control.Settings, "http://proxy", key, func(ctx context.Context, _ int64, _, _ string) (CodexTicketProbeResult, error) {
				repo.mu.Lock()
				repo.accounts[key.AccountID].Extra = map[string]any{CodexTurnStateEnabledExtraKey: false}
				repo.mu.Unlock()
				if deadline {
					<-ctx.Done()
					return CodexTicketProbeResult{}, ctx.Err()
				}
				return CodexTicketProbeResult{HTTPStatus: 503}, errors.New("synthetic failure")
			})
			require.Zero(t, store.writes)
			require.Zero(t, store.commits)
			r.mu.Lock()
			defer r.mu.Unlock()
			require.Empty(t, r.lastError)
		})
	}
}
