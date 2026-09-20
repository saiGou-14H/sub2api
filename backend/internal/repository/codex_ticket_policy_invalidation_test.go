//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexTicketPolicyScopeAndAccountIsolation(t *testing.T) {
	c, _, _, a := ticketCacheFixture(t)
	ctx := context.Background()
	b := a
	b.Key.AccountID++
	b.State = "gAAAAA" + strings.Repeat("b", b.TargetLength-6)
	changed := a
	changed.Key.PolicyScope = "team-policy"
	changed.TargetLength = 332
	changed.State = "gAAAAA" + strings.Repeat("c", 326)
	for _, ticket := range []service.CodexTicket{a, b, changed} {
		ok, err := c.CommitIfCurrent(ctx, "one", ticket)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, c.RecordCodexTicketDecision(ctx, ticket.Key, "header_set", "ticket_ready", ""))
	}
	for _, ticket := range []service.CodexTicket{a, b, changed} {
		view, err := c.ReadForRequest(ctx, ticket.Key.AccountID, ticket.Key.IdentityScope, ticket.Key.Model, ticket.Key.PolicyScope)
		require.NoError(t, err)
		require.NotNil(t, view.Ticket)
		require.Equal(t, ticket.State, view.Ticket.State)
	}
	empty, err := c.ReadForRequest(ctx, a.Key.AccountID, a.Key.IdentityScope, a.Key.Model)
	require.NoError(t, err)
	require.Nil(t, empty.Ticket)
	// Legacy payloads never become eligible under the policy-aware format.
	raw, err := json.Marshal(a)
	require.NoError(t, err)
	legacy := fmt.Sprintf("%sticket:%d:%d:%s:%s", codexPrefix, a.Key.Revision, a.Key.AccountID, service.CodexTicketModelHash(a.Key.IdentityScope), service.CodexTicketModelHash(a.Key.Model))
	require.NoError(t, c.client.Set(ctx, legacy, raw, time.Hour).Err())
	empty, err = c.ReadForRequest(ctx, a.Key.AccountID, a.Key.IdentityScope, a.Key.Model, "new-policy")
	require.NoError(t, err)
	require.Nil(t, empty.Ticket)
	newKey := a.Key
	newKey.PolicyScope = "new-policy"
	rows, err := c.ReadCodexTicketAccounts(ctx, []service.CodexTicketKey{a.Key, b.Key, changed.Key, newKey})
	require.NoError(t, err)
	require.EqualValues(t, 1, rows[a.Key].Observation.InjectionCount)
	require.Zero(t, rows[newKey].Observation.InjectionCount)
	require.Nil(t, rows[newKey].Metadata)
	require.Equal(t, a.State, rows[a.Key].Ticket.State)
	require.Equal(t, b.State, rows[b.Key].Ticket.State)
	require.Equal(t, 332, rows[changed.Key].Ticket.TargetLength)
	until := a.CapturedAt.Add(time.Hour)
	require.NoError(t, c.ExtendProbeCooldown(ctx, "one", a.Key, until))
	blocked, err := c.ProbeCooldownActive(ctx, changed.Key)
	require.NoError(t, err)
	require.True(t, blocked)
	blocked, err = c.ProbeCooldownActive(ctx, b.Key)
	require.NoError(t, err)
	require.False(t, blocked)
	require.NoError(t, c.WriteRetry(ctx, "one", a.Key, service.CodexTicketRetry{Attempt: 1, NextAttemptAt: until}))
	retry, err := c.ReadRetry(ctx, changed.Key)
	require.NoError(t, err)
	require.Zero(t, retry.Attempt)
}

func TestCodexTicketCommitRequiresVerifiedModelAndPolicy(t *testing.T) {
	for _, kind := range []string{"policy", "verified", "verified_at", "verified_before_capture", "verified_after_expiry", "actual_model", "verification_model", "target"} {
		t.Run(kind, func(t *testing.T) {
			c, _, _, ticket := ticketCacheFixture(t)
			switch kind {
			case "policy":
				ticket.Key.PolicyScope = ""
			case "verified":
				ticket.Verified = false
			case "verified_at":
				ticket.VerifiedAt = time.Time{}
			case "verified_before_capture":
				ticket.VerifiedAt = ticket.CapturedAt.Add(-time.Second)
			case "verified_after_expiry":
				ticket.VerifiedAt = ticket.ExpiresAt.Add(time.Second)
			case "actual_model":
				ticket.ActualModel = "wrong"
			case "verification_model":
				ticket.VerificationModel = "wrong"
			case "target":
				ticket.TargetLength++
			}
			ok, err := c.CommitIfCurrent(context.Background(), "one", ticket)
			require.Error(t, err)
			require.False(t, ok)
		})
	}
}

func TestCodexTicketInvalidationConcurrentPreservesOtherState(t *testing.T) {
	c, mr, _, a := ticketCacheFixture(t)
	ctx := context.Background()
	b := a
	b.Key.AccountID++
	for _, ticket := range []service.CodexTicket{a, b} {
		ok, err := c.CommitIfCurrent(ctx, "one", ticket)
		require.NoError(t, err)
		require.True(t, ok)
	}
	require.NoError(t, c.RecordCodexTicketDecision(ctx, a.Key, "header_set", "ticket_ready", ""))
	mr.FastForward(time.Second)
	initialTTL := mr.TTL(codexObservationKey(a.Key))
	until := a.CapturedAt.Add(time.Hour)
	require.NoError(t, c.WriteRetry(ctx, "one", a.Key, service.CodexTicketRetry{Attempt: 1, NextAttemptAt: until}))
	require.NoError(t, c.ExtendProbeCooldown(ctx, "one", a.Key, until))
	// Invalidation callers only retain the original key/state/capture receipt.
	receipt := service.CodexTicket{Key: a.Key, State: a.State, CapturedAt: a.CapturedAt}
	type result struct {
		ok  bool
		err error
	}
	results := make(chan result, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := c.InvalidateCodexTicket(ctx, receipt, "state_312")
			results <- result{ok, err}
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for res := range results {
		require.NoError(t, res.err)
		if res.ok {
			count++
		}
	}
	require.Equal(t, 1, count)
	rows, err := c.ReadCodexTicketAccounts(ctx, []service.CodexTicketKey{a.Key, b.Key})
	require.NoError(t, err)
	require.False(t, rows[a.Key].Unavailable)
	require.Nil(t, rows[a.Key].Ticket)
	require.NotNil(t, rows[a.Key].Metadata)
	require.NotNil(t, rows[b.Key].Ticket)
	require.EqualValues(t, 1, rows[a.Key].Observation.InvalidationCount)
	require.EqualValues(t, 1, rows[a.Key].Observation.InjectionCount)
	require.Equal(t, "state_312", *rows[a.Key].Observation.LastInvalidationReason)
	require.Equal(t, "header_set", *rows[a.Key].Observation.LastOutcome)
	require.True(t, until.Equal(rows[a.Key].CooldownUntil))
	require.Equal(t, 1, rows[a.Key].Retry.Attempt)
	require.LessOrEqual(t, mr.TTL(codexObservationKey(a.Key)), initialTTL)
	// A new verified ticket remains usable while invalidation history survives.
	fresh := a
	fresh.State = "gAAAAA" + strings.Repeat("z", fresh.TargetLength-6)
	ok, err := c.CommitIfCurrent(ctx, "one", fresh)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.InvalidateCodexTicket(ctx, receipt, "state_312")
	require.NoError(t, err)
	require.False(t, ok)
	rows, err = c.ReadCodexTicketAccounts(ctx, []service.CodexTicketKey{a.Key})
	require.NoError(t, err)
	require.Equal(t, fresh.State, rows[a.Key].Ticket.State)
	require.EqualValues(t, 1, rows[a.Key].Observation.InvalidationCount)
	cfg := service.DefaultCodexTicketSettings()
	cfg.Revision = fresh.Key.Revision
	cfg.TargetLength = fresh.TargetLength
	require.NoError(t, service.ValidateCodexTicket(*rows[a.Key].Ticket, a.Key, cfg, a.CapturedAt))
}

func TestCodexTicketInvalidationLateCaptureAndFullScope(t *testing.T) {
	c, _, _, a := ticketCacheFixture(t)
	ctx := context.Background()
	newer := a
	newer.CapturedAt = a.CapturedAt.Add(time.Second)
	newer.VerifiedAt = newer.CapturedAt
	newer.ExpiresAt = newer.CapturedAt.Add(time.Hour)
	ok, err := c.CommitIfCurrent(ctx, "one", newer)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.InvalidateCodexTicket(ctx, a, "model_mismatch")
	require.NoError(t, err)
	require.False(t, ok, "same state with newer captured time is a different ticket")
	for _, mutate := range []func(*service.CodexTicket){
		func(x *service.CodexTicket) { x.Key.AccountID++ }, func(x *service.CodexTicket) { x.Key.PolicyScope = "other" }, func(x *service.CodexTicket) { x.Key.IdentityScope = "other" }, func(x *service.CodexTicket) { x.Key.Model = "other" }, func(x *service.CodexTicket) { x.Key.Revision++ },
	} {
		wrong := newer
		mutate(&wrong)
		raw, err := json.Marshal(wrong)
		require.NoError(t, err)
		require.NoError(t, c.client.Set(ctx, codexPayloadKey(newer.Key), raw, time.Hour).Err())
		ok, err = c.InvalidateCodexTicket(ctx, newer, "model_mismatch")
		require.NoError(t, err)
		require.False(t, ok, "payload Key must match even under the expected Redis key")
	}
	ok, err = c.CommitIfCurrent(ctx, "one", newer)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.InvalidateCodexTicket(ctx, newer, "model_mismatch")
	require.NoError(t, err)
	require.True(t, ok)
	rows, err := c.ReadCodexTicketAccounts(ctx, []service.CodexTicketKey{newer.Key})
	require.NoError(t, err)
	require.False(t, rows[newer.Key].Unavailable, "invalidation-only observation is valid")
	require.Nil(t, rows[newer.Key].Observation.LastOutcome)
	require.EqualValues(t, 1, rows[newer.Key].Observation.InvalidationCount)
}
