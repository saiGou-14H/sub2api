//go:build unit

package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexTicketCookieBundleRoundtripIsolationAndAtomicReplacement(t *testing.T) {
	c, mr, _, a := ticketCacheFixture(t)
	ctx := context.Background()
	a.BundleID = "capture-a"
	for i := range a.Cookies {
		a.Cookies[i].ExpiresAt = a.Cookies[i].ExpiresAt.UTC()
	}
	b := a
	b.Key.AccountID++
	b.BundleID = "capture-b"
	b.Cookies = append([]service.CodexTicketCookie(nil), a.Cookies...)
	b.Cookies[0].Value = "account-b-cookie"
	for _, ticket := range []service.CodexTicket{a, b} {
		ok, err := c.CommitIfCurrent(ctx, "one", ticket)
		require.NoError(t, err)
		require.True(t, ok)
	}
	view, err := c.ReadForRequest(ctx, a.Key.AccountID, a.Key.IdentityScope, a.Key.Model, a.Key.PolicyScope)
	require.NoError(t, err)
	require.Equal(t, a.Cookies, view.Ticket.Cookies)
	require.Equal(t, a.BundleID, view.Ticket.BundleID)
	require.LessOrEqual(t, mr.TTL(codexPayloadKey(a.Key)), 240*time.Second)
	meta, err := c.client.Get(ctx, codexMetadataKey(a.Key)).Result()
	require.NoError(t, err)
	for _, secret := range []string{a.State, a.Cookies[0].Value, a.Cookies[1].Value, a.BundleID, "cookies"} {
		require.NotContains(t, meta, secret)
	}
	wrongModel, err := c.ReadForRequest(ctx, a.Key.AccountID, a.Key.IdentityScope, "other-model", a.Key.PolicyScope)
	require.NoError(t, err)
	require.Nil(t, wrongModel.Ticket)

	// Even identical state/capture time must not let an old receipt invalidate
	// a newly committed cookie generation. No partial mutation of the old pair.
	fresh := a
	fresh.BundleID = "capture-new"
	fresh.Cookies = append([]service.CodexTicketCookie(nil), a.Cookies...)
	fresh.Cookies[0].Value = "new-cflb"
	fresh.Cookies[1].Value = "new-oailb"
	ok, err := c.CommitIfCurrent(ctx, "one", fresh)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.InvalidateCodexTicket(ctx, a, "model_mismatch")
	require.NoError(t, err)
	require.False(t, ok)
	view, err = c.ReadForRequest(ctx, a.Key.AccountID, a.Key.IdentityScope, a.Key.Model, a.Key.PolicyScope)
	require.NoError(t, err)
	require.Equal(t, fresh.Cookies, view.Ticket.Cookies)
	view, err = c.ReadForRequest(ctx, b.Key.AccountID, b.Key.IdentityScope, b.Key.Model, b.Key.PolicyScope)
	require.NoError(t, err)
	require.Equal(t, b.Cookies, view.Ticket.Cookies)
	ok, err = c.InvalidateCodexTicket(ctx, fresh, "model_mismatch")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestCodexTicketCookieBundleCacheRejectsInvalidOrOverlong(t *testing.T) {
	for _, change := range []func(*service.CodexTicket){
		func(t *service.CodexTicket) { t.Cookies = nil },
		func(t *service.CodexTicket) { t.Cookies = t.Cookies[:1] },
		func(t *service.CodexTicket) { t.Cookies[0].ExpiresAt = t.CapturedAt.Add(10 * time.Second) },
		func(t *service.CodexTicket) { t.ExpiresAt = t.CapturedAt.Add(time.Hour) },
		func(t *service.CodexTicket) { t.Cookies[0].Value = strings.Repeat("x", 20<<10) },
	} {
		c, _, _, ticket := ticketCacheFixture(t)
		change(&ticket)
		ok, err := c.CommitIfCurrent(context.Background(), "one", ticket)
		require.Error(t, err)
		require.False(t, ok)
	}
}
