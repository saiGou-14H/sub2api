//go:build unit

package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ticketRefreshStore struct {
	*ticketTestStore
	cooldown bool
	retry    CodexTicketRetry
}

func (s *ticketRefreshStore) ProbeCooldownActive(context.Context, CodexTicketKey) (bool, error) {
	return s.cooldown, nil
}
func (s *ticketRefreshStore) ReadRetry(context.Context, CodexTicketKey) (CodexTicketRetry, error) {
	return s.retry, nil
}
func TestCodexTicketCollectorRefreshExpiryAndRestrictions(t *testing.T) {
	for _, tc := range []struct {
		name                                         string
		expiry                                       time.Duration
		missing, cooldown, retry, budgetDenied, want bool
	}{
		{name: "fresh", expiry: time.Hour},
		{name: "early_refresh", expiry: time.Second, want: true},
		{name: "expired", expiry: -time.Second, want: true},
		{name: "missing", missing: true, want: true},
		{name: "cooldown", missing: true, cooldown: true},
		{name: "retry", missing: true, retry: true},
		{name: "budget", missing: true, budgetDenied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, a, base, _ := ticketRuntimeFixture()
			cfg := base.v.Control.Settings
			cfg.Models = cfg.Models[:1]
			cfg.MaxConcurrency = 1
			base.v.Control.Settings = cfg
			if tc.missing {
				base.v.Ticket = nil
			} else {
				base.v.Ticket.ExpiresAt = base.v.ServerTime.Add(tc.expiry)
			}
			base.budget = !tc.budgetDenied
			s := &ticketRefreshStore{ticketTestStore: base, cooldown: tc.cooldown}
			if tc.retry {
				s.retry.NextAttemptAt = base.v.ServerTime.Add(time.Hour)
			}
			r.cache = s
			r.SetCandidatePager(ticketTestPager{*a.a})
			var probes atomic.Int32
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
			defer cancel()
			r.SetProbe(func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
				probes.Add(1)
				cancel()
				return CodexTicketProbeResult{}, context.Canceled
			})
			r.collect(ctx, "owner", cfg, "http://proxy")
			if tc.want {
				require.EqualValues(t, 1, probes.Load())
			} else {
				require.Zero(t, probes.Load())
			}
		})
	}
}
