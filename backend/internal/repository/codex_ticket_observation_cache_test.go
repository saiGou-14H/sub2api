//go:build unit

package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type codexPipelineHook struct {
	pipelines int
	singles   int
}

func (h *codexPipelineHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *codexPipelineHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { h.singles++; return next(ctx, cmd) }
}
func (h *codexPipelineHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { h.pipelines++; return next(ctx, cmds) }
}
func TestCodexTicketObservationAccountIsolationAndExpiry(t *testing.T) {
	c, mr, _, a := ticketCacheFixture(t)
	ctx := context.Background()
	b := a
	b.Key.AccountID++
	b.State = "gAAAAA" + strings.Repeat("b", len(a.State)-6)
	b.ExpiresAt = b.ExpiresAt.Add(-time.Minute)
	for _, ticket := range []service.CodexTicket{a, b} {
		ok, err := c.CommitIfCurrent(ctx, "one", ticket)
		require.NoError(t, err)
		require.True(t, ok)
	}
	id := "de305d54-75b4-431b-adb2-eb6b9e546014"
	require.NoError(t, c.RecordCodexTicketDecision(ctx, a.Key, "header_set", "ticket_ready", id))
	require.NoError(t, c.RecordCodexTicketDecision(ctx, a.Key, "skipped", "compact", ""))
	require.NoError(t, c.RecordCodexTicketDecision(ctx, b.Key, "header_set", "ticket_ready", ""))
	require.NoError(t, c.RecordCodexTicketDecision(ctx, b.Key, "header_set", "ticket_ready", ""))
	rows, err := c.ReadCodexTicketAccounts(ctx, []service.CodexTicketKey{a.Key, b.Key})
	require.NoError(t, err)
	require.Equal(t, a.State, rows[a.Key].Ticket.State)
	require.Equal(t, b.State, rows[b.Key].Ticket.State)
	require.EqualValues(t, 1, rows[a.Key].Observation.InjectionCount)
	require.EqualValues(t, 2, rows[b.Key].Observation.InjectionCount)
	require.Equal(t, "skipped", *rows[a.Key].Observation.LastOutcome)
	require.Equal(t, id, *rows[a.Key].Observation.LastRequestID)
	require.Nil(t, rows[b.Key].Observation.LastRequestID)
	changed := a.Key
	changed.IdentityScope = "changed"
	revised := a.Key
	revised.Revision++
	rows, err = c.ReadCodexTicketAccounts(ctx, []service.CodexTicketKey{changed, revised})
	require.NoError(t, err)
	for _, row := range rows {
		require.Nil(t, row.Ticket)
		require.Nil(t, row.Metadata)
		require.Zero(t, row.Observation.InjectionCount)
	}
	// Refresh only A. B's bytes, metadata and observation are untouched.
	a.State = "gAAAAA" + strings.Repeat("c", len(a.State)-6)
	ok, err := c.CommitIfCurrent(ctx, "one", a)
	require.NoError(t, err)
	require.True(t, ok)
	rows, err = c.ReadCodexTicketAccounts(ctx, []service.CodexTicketKey{a.Key, b.Key})
	require.NoError(t, err)
	require.Equal(t, b.State, rows[b.Key].Ticket.State)
	require.True(t, b.ExpiresAt.Equal(rows[b.Key].Metadata.ExpiresAt))
	mr.FastForward(a.ExpiresAt.Sub(a.CapturedAt) + time.Second)
	rows, err = c.ReadCodexTicketAccounts(ctx, []service.CodexTicketKey{a.Key, b.Key})
	require.NoError(t, err)
	require.Nil(t, rows[a.Key].Ticket)
	require.NotNil(t, rows[a.Key].Metadata)
	require.True(t, a.ExpiresAt.Equal(rows[a.Key].Metadata.ExpiresAt))
	require.LessOrEqual(t, mr.TTL(codexObservationKey(a.Key)), 24*time.Hour)
	mr.FastForward(24 * time.Hour)
	rows, err = c.ReadCodexTicketAccounts(ctx, []service.CodexTicketKey{a.Key})
	require.NoError(t, err)
	require.Zero(t, rows[a.Key].Observation.InjectionCount)
	require.Nil(t, rows[a.Key].Metadata)
}
func TestCodexTicketAccountBatchPipelineCooldownAndCorruption(t *testing.T) {
	c, _, _, ticket := ticketCacheFixture(t)
	ctx := context.Background()
	until := ticket.CapturedAt.Add(time.Hour)
	require.NoError(t, c.ExtendProbeCooldown(ctx, "one", ticket.Key, until))
	require.NoError(t, c.WriteRetry(ctx, "one", ticket.Key, service.CodexTicketRetry{NextAttemptAt: until.Add(-time.Minute)}))
	hook := &codexPipelineHook{}
	c.client.AddHook(hook)
	keys := make([]service.CodexTicketKey, 1600)
	for i := range keys {
		keys[i] = ticket.Key
		keys[i].AccountID = int64(i + 1)
	}
	rows, err := c.ReadCodexTicketAccounts(ctx, keys)
	require.NoError(t, err)
	require.Len(t, rows, 1600)
	require.Equal(t, 1, hook.pipelines)
	require.Zero(t, hook.singles)
	require.True(t, until.Equal(rows[ticket.Key].CooldownUntil))
	require.NoError(t, c.client.Set(ctx, codexMetadataKey(ticket.Key), "secret-invalid-json", time.Hour).Err())
	rows, err = c.ReadCodexTicketAccounts(ctx, []service.CodexTicketKey{ticket.Key})
	require.NoError(t, err)
	require.True(t, rows[ticket.Key].Unavailable)
	_, err = c.ReadCodexTicketAccounts(ctx, append(keys, ticket.Key))
	require.Error(t, err)
	require.NoError(t, c.client.Close())
	_, err = c.ReadCodexTicketAccounts(ctx, []service.CodexTicketKey{ticket.Key})
	require.ErrorIs(t, err, service.ErrCodexTicketControlUnavailable)
}
