//go:build unit

package repository

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func ticketCacheFixture(t *testing.T) (*codexTicketCache, *miniredis.Miniredis, service.CodexTicketControl, service.CodexTicket) {
	t.Helper()
	mr := miniredis.RunT(t)
	// Freeze server time away from minute boundaries so budget tests cannot
	// accidentally straddle two legitimate windows.
	mr.SetTime(time.Date(2026, time.January, 2, 3, 4, 10, 0, time.UTC))
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &codexTicketCache{client}
	ctx := context.Background()
	now, e := cache.Now(ctx)
	require.NoError(t, e)
	cfg := service.DefaultCodexTicketSettings()
	cfg.Enabled = true
	cfg.Revision = 1
	control := service.CodexTicketControl{Owner: "one", Settings: cfg, ProxyState: "active", ValidUntilMS: now.Add(6 * time.Second).UnixMilli()}
	cookieExpiry := now.Add(time.Hour)
	cookies := []service.CodexTicketCookie{{Name: "cflb", Value: "fixture-cflb", Domain: ".chatgpt.com", Path: "/", ExpiresAt: cookieExpiry}, {Name: "oailb", Value: "fixture-oailb", Domain: ".chatgpt.com", Path: "/", ExpiresAt: cookieExpiry}}
	ticket := service.CodexTicket{Key: service.CodexTicketKey{Revision: 1, AccountID: 3, IdentityScope: "scope", Model: cfg.Models[0], PolicyScope: "policy"}, State: "gAAAAA" + strings.Repeat("a", cfg.TargetLength-6), Cookies: cookies, CapturedAt: now, ExpiresAt: now.Add(time.Duration(cfg.TTLSeconds) * time.Second), Verified: true, VerifiedAt: now, ActualModel: cfg.Models[0], VerificationModel: cfg.Models[0], TargetLength: cfg.TargetLength}
	ok, e := cache.TryAcquireLeader(ctx, "one", 15*time.Second)
	require.NoError(t, e)
	require.True(t, ok)
	ok, e = cache.PublishControl(ctx, control)
	require.NoError(t, e)
	require.True(t, ok)
	return cache, mr, control, ticket
}
func TestCodexTicketCacheFencingAndIsolation(t *testing.T) {
	c, _, control, ticket := ticketCacheFixture(t)
	ctx := context.Background()
	ok, e := c.CommitIfCurrent(ctx, "one", ticket)
	require.NoError(t, e)
	require.True(t, ok)
	view, e := c.ReadForRequest(ctx, 3, "scope", ticket.Key.Model, ticket.Key.PolicyScope)
	require.NoError(t, e)
	require.Equal(t, ticket.State, view.Ticket.State)
	view, e = c.ReadForRequest(ctx, 3, "other", ticket.Key.Model, ticket.Key.PolicyScope)
	require.NoError(t, e)
	require.Nil(t, view.Ticket)
	ok, e = c.CommitIfCurrent(ctx, "old", ticket)
	require.NoError(t, e)
	require.False(t, ok)
	control.Settings.Revision = 2
	ok, e = c.PublishControl(ctx, control)
	require.NoError(t, e)
	require.True(t, ok)
	ok, e = c.CommitIfCurrent(ctx, "one", ticket)
	require.Error(t, e)
	require.False(t, ok)
	view, e = c.ReadForRequest(ctx, 3, "scope", ticket.Key.Model, ticket.Key.PolicyScope)
	require.NoError(t, e)
	require.Nil(t, view.Ticket)
	control.Settings.Revision = 1
	ok, e = c.PublishControl(ctx, control)
	require.NoError(t, e)
	require.False(t, ok)
}
func TestCodexTicketCacheOwnerRenewRelease(t *testing.T) {
	c, _, _, _ := ticketCacheFixture(t)
	ctx := context.Background()
	require.NoError(t, c.client.Set(ctx, codexPrefix+"leader", "two", 15*time.Second).Err())
	ok, e := c.RenewLeader(ctx, "one", time.Minute)
	require.NoError(t, e)
	require.False(t, ok)
	require.NoError(t, c.ReleaseLeader(ctx, "one"))
	owner, e := c.client.Get(ctx, codexPrefix+"leader").Result()
	require.NoError(t, e)
	require.Equal(t, "two", owner)
}
func TestCodexTicketCacheFreshnessBudgetAndProxyExpiry(t *testing.T) {
	c, mr, control, ticket := ticketCacheFixture(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		ok, e := c.AcquireProbeBudget(ctx, "one", 1, 2)
		require.NoError(t, e)
		require.True(t, ok)
	}
	ok, e := c.AcquireProbeBudget(ctx, "one", 1, 2)
	require.NoError(t, e)
	require.False(t, ok)
	// Publishing an old snapshot must retain only its remaining lifetime.
	now := ticket.CapturedAt
	mr.SetTime(now.Add(4 * time.Second))
	ok, e = c.PublishControl(ctx, control)
	require.NoError(t, e)
	require.True(t, ok)
	require.LessOrEqual(t, mr.TTL(codexPrefix+"control"), 2*time.Second)
	mr.SetTime(now.Add(7 * time.Second))
	ok, e = c.PublishControl(ctx, control)
	require.NoError(t, e)
	require.False(t, ok)
	ok, e = c.CheckCurrent(ctx, "one", 1)
	require.NoError(t, e)
	require.False(t, ok)
	_, e = c.ReadForRequest(ctx, 3, "scope", ticket.Key.Model, ticket.Key.PolicyScope)
	require.ErrorIs(t, e, service.ErrCodexTicketControlUnavailable)
	control.ValidUntilMS = now.Add(12 * time.Second).UnixMilli()
	control.ProxyExpiresAtMS = now.Add(6 * time.Second).UnixMilli()
	ok, e = c.PublishControl(ctx, control)
	require.NoError(t, e)
	require.True(t, ok)
	ok, e = c.CommitIfCurrent(ctx, "one", ticket)
	require.NoError(t, e)
	require.False(t, ok)
}
func TestCodexTicketCacheRejectsMalformedPayload(t *testing.T) {
	c, _, _, ticket := ticketCacheFixture(t)
	ticket.State = strings.Repeat("\r", len(ticket.State))
	ok, e := c.CommitIfCurrent(context.Background(), "one", ticket)
	require.Error(t, e)
	require.False(t, ok)
}

func TestCodexTicketCacheConcurrentBudgetIsSharedAcrossRevisions(t *testing.T) {
	cache, _, control, _ := ticketCacheFixture(t)
	ctx := context.Background()
	type outcome struct {
		allowed bool
		err     error
	}
	results := make(chan outcome, 24)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := cache.AcquireProbeBudget(ctx, "one", 1, 3)
			results <- outcome{ok, err}
		}()
	}
	wg.Wait()
	close(results)
	allowed := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.allowed {
			allowed++
		}
	}
	require.Equal(t, 3, allowed)
	control.Settings.Revision++
	ok, err := cache.PublishControl(ctx, control)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.AcquireProbeBudget(ctx, "one", control.Settings.Revision, 3)
	require.NoError(t, err)
	require.False(t, ok, "saving settings must not reset the shared minute budget")
}

func TestCodexTicketCacheTakeoverFencesRetryAndDoesNotExtendTicket(t *testing.T) {
	cache, mr, control, ticket := ticketCacheFixture(t)
	ctx := context.Background()
	ok, err := cache.CommitIfCurrent(ctx, "one", ticket)
	require.NoError(t, err)
	require.True(t, ok)
	ttl := mr.TTL(codexPayloadKey(ticket.Key))
	retry := service.CodexTicketRetry{Attempt: 1, NextAttemptAt: ticket.CapturedAt.Add(time.Minute), ErrorCode: "rate_limited"}
	require.NoError(t, cache.WriteRetry(ctx, "one", ticket.Key, retry))
	require.Equal(t, ttl, mr.TTL(codexPayloadKey(ticket.Key)), "failure must not extend a valid ticket")
	require.NoError(t, cache.client.Set(ctx, codexPrefix+"leader", "two", 15*time.Second).Err())
	control.Owner = "two"
	ok, err = cache.PublishControl(ctx, control)
	require.NoError(t, err)
	require.True(t, ok)
	retry.Attempt = 8
	require.NoError(t, cache.WriteRetry(ctx, "one", ticket.Key, retry))
	stored, err := cache.ReadRetry(ctx, ticket.Key)
	require.NoError(t, err)
	require.Equal(t, 1, stored.Attempt, "stale owner must not overwrite retry state")
	ok, err = cache.CommitIfCurrent(ctx, "one", ticket)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, cache.ReleaseLeader(ctx, "one"))
	view, err := cache.ReadForRequest(ctx, ticket.Key.AccountID, ticket.Key.IdentityScope, ticket.Key.Model, ticket.Key.PolicyScope)
	require.NoError(t, err)
	require.Equal(t, "two", view.Control.Owner)
	ok, err = cache.CommitIfCurrent(ctx, "two", ticket)
	require.NoError(t, err)
	require.True(t, ok)
	stored, err = cache.ReadRetry(ctx, ticket.Key)
	require.NoError(t, err)
	require.Zero(t, stored.Attempt, "successful commit clears backoff")
}
