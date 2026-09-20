//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexTicketStatusEvictsIneligibleAccounts(t *testing.T) {
	cases := []struct {
		name   string
		change func(*ticketTestAccounts)
	}{
		{"disabled", func(a *ticketTestAccounts) { a.a.Extra = map[string]any{"codex_turn_state_enabled": false} }},
		{"deleted", func(a *ticketTestAccounts) { a.a = nil }},
		{"identity_changed", func(a *ticketTestAccounts) { a.a.Credentials = map[string]any{"chatgpt_account_id": "new-identity"} }},
		{"unsupported", func(a *ticketTestAccounts) { a.a.Type = AccountTypeAPIKey }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, accounts, store, key := ticketRuntimeFixture()
			r.ready[key] = store.v.Ticket.ExpiresAt
			status, err := r.Status(context.Background())
			require.NoError(t, err)
			require.Equal(t, "ready", status.Phase)
			tc.change(accounts)
			status, err = r.Status(context.Background())
			require.NoError(t, err)
			require.Zero(t, status.ReadyCount)
			require.Equal(t, "waiting", status.Phase)
			require.Empty(t, r.ready)
		})
	}
}

type ticketStatusBatchAccounts struct {
	AccountRepository
	base       Account
	batchSizes []int
	deadlines  []time.Time
	failAt     int
}

func (a *ticketStatusBatchAccounts) GetByIDs(ctx context.Context, ids []int64) ([]*Account, error) {
	a.batchSizes = append(a.batchSizes, len(ids))
	deadline, _ := ctx.Deadline()
	a.deadlines = append(a.deadlines, deadline)
	if a.failAt == len(a.batchSizes) {
		return nil, errors.New("database unavailable")
	}
	out := make([]*Account, 0, len(ids))
	for _, id := range ids {
		copy := a.base
		copy.ID = id
		out = append(out, &copy)
	}
	return out, nil
}
func TestCodexTicketStatusBatchesAndDeduplicatesAccountReads(t *testing.T) {
	r, accounts, store, key := ticketRuntimeFixture()
	repo := &ticketStatusBatchAccounts{base: *accounts.a}
	r.accounts = repo
	for id := int64(1); id <= 201; id++ {
		k := key
		k.AccountID = id
		r.ready[k] = store.v.Ticket.ExpiresAt
	}
	other := key
	other.Model = store.v.Control.Settings.Models[1]
	r.ready[other] = store.v.Ticket.ExpiresAt
	started := time.Now()
	status, err := r.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, 202, status.ReadyCount)
	require.Equal(t, []int{100, 100, 1}, repo.batchSizes)
	for _, deadline := range repo.deadlines {
		require.False(t, deadline.IsZero())
		require.True(t, deadline.Equal(repo.deadlines[0]))
		require.LessOrEqual(t, deadline.Sub(started), 2*time.Second+100*time.Millisecond)
	}
}
func TestCodexTicketStatusBatchFailureReturnsUnavailable(t *testing.T) {
	r, accounts, store, key := ticketRuntimeFixture()
	repo := &ticketStatusBatchAccounts{base: *accounts.a, failAt: 2}
	r.accounts = repo
	for id := int64(1); id <= 101; id++ {
		k := key
		k.AccountID = id
		r.ready[k] = store.v.Ticket.ExpiresAt
	}
	status, err := r.Status(context.Background())
	require.ErrorIs(t, err, ErrCodexTicketControlUnavailable)
	require.NotEqual(t, "ready", status.Phase)
	require.Equal(t, []int{100, 1}, repo.batchSizes)
	require.Len(t, r.ready, 101, "failed read must not be interpreted as deleted accounts")
}
