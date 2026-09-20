//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexTicketAccountsDelayedReadRechecksDeadlines(t *testing.T) {
	for _, kind := range []string{"ticket", "control", "proxy"} {
		t.Run(kind, func(t *testing.T) {
			r, _, s, k, _ := ticketObservationFixture()
			now := s.v.ServerTime
			s.readDelay = 30 * time.Millisecond
			switch kind {
			case "ticket":
				s.rows[k].Ticket.ExpiresAt = now.Add(10 * time.Millisecond)
			case "control":
				s.v.Control.ValidUntilMS = now.Add(10 * time.Millisecond).UnixMilli()
			case "proxy":
				s.v.Control.ProxyExpiresAtMS = now.Add(10 * time.Millisecond).UnixMilli()
			}
			out, err := r.AccountStatuses(context.Background(), []int64{1})
			require.NoError(t, err)
			require.True(t, out.ServerTime.After(now.Add(10*time.Millisecond)))
			want := map[string]string{"ticket": "expired", "control": "unavailable", "proxy": "proxy_unavailable"}[kind]
			require.Equal(t, want, out.Accounts[0].Status)
			if kind == "control" {
				require.Nil(t, out.AppliedRevision)
				require.Nil(t, out.Accounts[0].Models[0].LastOutcome)
			}
		})
	}
}

func TestCodexTicketAccountsMetadataIsNotReadiness(t *testing.T) {
	for _, kind := range []string{"fresh_metadata_only", "zero_capture", "future_capture", "excessive_ttl", "wrong_scope", "corrupt_batch"} {
		t.Run(kind, func(t *testing.T) {
			r, _, s, k, _ := ticketObservationFixture()
			now := s.v.ServerTime
			row := s.rows[k]
			row.Ticket = nil
			row.Metadata = &CodexTicketMetadata{now, now.Add(time.Minute)}
			switch kind {
			case "zero_capture":
				row.Metadata.CapturedAt = time.Time{}
			case "future_capture":
				row.Metadata.CapturedAt = now.Add(10 * time.Second)
			case "excessive_ttl":
				row.Metadata.ExpiresAt = now.Add(2 * time.Hour)
			case "wrong_scope":
				wrong := *s.rows[k].Ticket
				wrong.Key.AccountID = 2
				row.Ticket = &wrong
			case "corrupt_batch":
				s.batchError = errors.New("redis telemetry failed")
			}
			s.rows[k] = row
			out, err := r.AccountStatuses(context.Background(), []int64{1})
			require.NoError(t, err)
			want := "unavailable"
			if kind == "fresh_metadata_only" {
				want = "waiting"
			}
			require.Equal(t, want, out.Accounts[0].Status)
			require.Nil(t, out.Accounts[0].Models[0].LastOutcome)
		})
	}
}

func TestCodexTicketAccountsUnsupportedDoesNotReadOrObserve(t *testing.T) {
	r, a, s, k, _ := ticketObservationFixture()
	old := *a.rows[1]
	a.rows[1].Type = AccountTypeAPIKey
	out, err := r.AccountStatuses(context.Background(), []int64{1})
	require.NoError(t, err)
	require.Equal(t, "unsupported", out.Accounts[0].Status)
	require.Zero(t, s.reads)
	r.ObserveCompactSkip(context.Background(), &old, k.Model)
	require.Zero(t, s.writes)
}
