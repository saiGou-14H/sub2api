//go:build unit

package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexTicketCooldownAtomicMaxAndIsolation(t *testing.T) {
	c, mr, control, ticket := ticketCacheFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := 1; i <= 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := ticket.Key
			k.Model = "other"
			errs <- c.ExtendProbeCooldown(ctx, "one", k, ticket.CapturedAt.Add(time.Duration(i)*time.Minute))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	deadline, err := c.client.Get(ctx, codexCooldownKey(ticket.Key)).Int64()
	require.NoError(t, err)
	require.Equal(t, ticket.CapturedAt.Add(24*time.Minute).UnixMilli(), deadline)
	// A late successful probe must not erase another model's rejection.
	ok, err := c.CommitIfCurrent(ctx, "one", ticket)
	require.NoError(t, err)
	require.True(t, ok)
	control.Settings.Revision++
	ok, err = c.PublishControl(ctx, control)
	require.NoError(t, err)
	require.True(t, ok)
	k := ticket.Key
	k.Revision = control.Settings.Revision
	k.Model = "new-model"
	active, err := c.ProbeCooldownActive(ctx, k)
	require.NoError(t, err)
	require.True(t, active)
	require.NoError(t, c.ExtendProbeCooldown(ctx, "one", k, ticket.CapturedAt.Add(time.Minute)))
	deadline, err = c.client.Get(ctx, codexCooldownKey(k)).Int64()
	require.NoError(t, err)
	require.Equal(t, ticket.CapturedAt.Add(24*time.Minute).UnixMilli(), deadline)
	for _, other := range []string{"identity", "account"} {
		changed := k
		if other == "identity" {
			changed.IdentityScope = "different"
		} else {
			changed.AccountID++
		}
		active, err = c.ProbeCooldownActive(ctx, changed)
		require.NoError(t, err)
		require.False(t, active)
	}
	// Observed rejections must survive a configuration/owner handoff.
	require.NoError(t, c.ExtendProbeCooldown(ctx, "one", ticket.Key, ticket.CapturedAt.Add(time.Hour)))
	require.NoError(t, c.ExtendProbeCooldown(ctx, "old-owner", k, ticket.CapturedAt.Add(30*time.Minute)))
	deadline, err = c.client.Get(ctx, codexCooldownKey(k)).Int64()
	require.NoError(t, err)
	require.Equal(t, ticket.CapturedAt.Add(time.Hour).UnixMilli(), deadline)
	mr.SetTime(ticket.CapturedAt.Add(61 * time.Minute))
	active, err = c.ProbeCooldownActive(ctx, k)
	require.NoError(t, err)
	require.False(t, active)
}
