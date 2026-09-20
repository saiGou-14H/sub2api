//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ticketTimeoutMarker struct{}
type ticketTimeoutStore struct {
	*ticketTestStore
	retry               CodexTicketRetry
	writes              int
	writeContextError   error
	writeDeadline       time.Time
	generationValue     any
	cancelOnFailureRead context.CancelFunc
	nowCalls            int
}

func (s *ticketTimeoutStore) Now(ctx context.Context) (time.Time, error) {
	s.nowCalls++
	if s.nowCalls == 2 && s.cancelOnFailureRead != nil {
		s.cancelOnFailureRead()
	}
	return s.ticketTestStore.Now(ctx)
}
func (s *ticketTimeoutStore) ReadRetry(context.Context, CodexTicketKey) (CodexTicketRetry, error) {
	return s.retry, nil
}
func (s *ticketTimeoutStore) WriteRetry(ctx context.Context, _ string, _ CodexTicketKey, retry CodexTicketRetry) error {
	s.writes++
	s.retry = retry
	s.writeContextError = ctx.Err()
	s.writeDeadline, _ = ctx.Deadline()
	s.generationValue = ctx.Value(ticketTimeoutMarker{})
	return ctx.Err()
}
func TestCodexTicketProbeDeadlineRecordsBackoffUsingGeneration(t *testing.T) {
	r, _, base, key := ticketRuntimeFixture()
	store := &ticketTimeoutStore{ticketTestStore: base}
	r.cache = store
	generation := context.WithValue(context.Background(), ticketTimeoutMarker{}, "generation")
	task, cancel := context.WithTimeout(generation, 50*time.Millisecond)
	defer cancel()
	called := false
	r.harvestWithTaskContext(generation, task, "owner", base.v.Control.Settings, "http://proxy", key, func(ctx context.Context, _ int64, _, _ string) (CodexTicketProbeResult, error) {
		called = true
		<-ctx.Done()
		return CodexTicketProbeResult{}, ctx.Err()
	})
	require.True(t, called)
	require.Equal(t, 1, store.writes)
	require.Equal(t, 1, store.retry.Attempt)
	require.Equal(t, "probe_timeout", store.retry.ErrorCode)
	require.GreaterOrEqual(t, store.retry.NextAttemptAt.Sub(base.v.ServerTime), 30*time.Second)
	require.NoError(t, store.writeContextError)
	require.Equal(t, "generation", store.generationValue)
	require.False(t, store.writeDeadline.IsZero())
	require.LessOrEqual(t, time.Until(store.writeDeadline), 2*time.Second)
	require.Zero(t, store.commits)
	// A new scheduling attempt obeys the stored delay instead of probing again.
	called = false
	r.harvest(generation, "owner", base.v.Control.Settings, "http://proxy", key, func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
		called = true
		return CodexTicketProbeResult{}, nil
	})
	require.False(t, called)
}
func TestCodexTicketGenerationCancellationDoesNotRecordRetry(t *testing.T) {
	r, _, base, key := ticketRuntimeFixture()
	store := &ticketTimeoutStore{ticketTestStore: base}
	r.cache = store
	generation, cancel := context.WithCancel(context.Background())
	defer cancel()
	task, stop := context.WithTimeout(generation, time.Second)
	defer stop()
	r.harvestWithTaskContext(generation, task, "owner", base.v.Control.Settings, "http://proxy", key, func(ctx context.Context, _ int64, _, _ string) (CodexTicketProbeResult, error) {
		cancel()
		return CodexTicketProbeResult{}, ctx.Err()
	})
	require.Zero(t, store.writes)
	require.Zero(t, store.commits)
}
func TestCodexTicketGenerationCanceledDuringTimeoutBookkeepingDoesNotWrite(t *testing.T) {
	r, _, base, key := ticketRuntimeFixture()
	generation, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &ticketTimeoutStore{ticketTestStore: base, cancelOnFailureRead: cancel}
	r.cache = store
	task, stop := context.WithTimeout(generation, 50*time.Millisecond)
	defer stop()
	r.harvestWithTaskContext(generation, task, "owner", base.v.Control.Settings, "http://proxy", key, func(ctx context.Context, _ int64, _, _ string) (CodexTicketProbeResult, error) {
		<-ctx.Done()
		return CodexTicketProbeResult{}, ctx.Err()
	})
	require.Zero(t, store.writes)
	require.ErrorIs(t, generation.Err(), context.Canceled)
}
