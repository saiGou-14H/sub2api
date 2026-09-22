//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexProbeCookieBundleRemainsFromCaptureResponse(t *testing.T) {
	a := codexProbeAccount()
	calls := 0
	svc := &OpenAIGatewayService{accountRepo: &codexProbeAccounts{account: a}}
	svc.httpUpstream = &codexProbeUpstream{call: func(req *http.Request, route string, _ int64, _ int) (*http.Response, error) {
		calls++
		if calls == 1 {
			require.Empty(t, req.Header.Get("Cookie"))
			return codexProbeSuccessResponse("model", codexProbeValidCandidate()), nil
		}
		require.Empty(t, route)
		require.Equal(t, "cflb=fixture-cflb; oailb=fixture-oailb", req.Header.Get("Cookie"))
		r := codexProbeSuccessResponse("model", "gAAAAA"+strings.Repeat("z", 286))
		r.Header["Set-Cookie"] = []string{"cflb=new-c; Max-Age=240; Path=/", "oailb=new-o; Max-Age=240; Path=/"}
		return r, nil
	}}
	got, err := svc.ProbeCodexTicket(codexProbeTestContext(a, "model"), a.ID, "model", "http://localhost:1")
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.True(t, got.Verified)
	require.Equal(t, codexProbeValidCandidate(), got.State)
	require.Equal(t, "cflb=fixture-cflb; oailb=fixture-oailb", CodexTicketCookieHeader(got.Cookies, time.Now()))
	require.False(t, got.CapturedAt.IsZero())
	require.False(t, got.VerifiedAt.Before(got.CapturedAt))
	require.Equal(t, got.CapturedAt.Add(240*time.Second), CodexTicketCookieExpiry(got.Cookies))
}

func TestCodexProbeCookieBundleCannotUseLaterResponseToFillMissingCookie(t *testing.T) {
	a := codexProbeAccount()
	calls := 0
	svc := &OpenAIGatewayService{accountRepo: &codexProbeAccounts{account: a}}
	svc.httpUpstream = &codexProbeUpstream{call: func(*http.Request, string, int64, int) (*http.Response, error) {
		calls++
		r := codexProbeSuccessResponse("model", codexProbeValidCandidate())
		r.Header["Set-Cookie"] = []string{"cflb=only-one; Max-Age=240"}
		return r, nil
	}}
	got, err := svc.ProbeCodexTicket(codexProbeTestContext(a, "model"), a.ID, "model", "http://localhost:1")
	require.EqualError(t, err, "cookie_missing")
	require.Equal(t, 1, calls, "missing capture pair prevents verification")
	require.Empty(t, got.Cookies)
	require.Empty(t, got.State)
	require.False(t, got.Completed)
}

func TestCodexProbeCookieBundleClearedOnUncompletedOrWrongModel(t *testing.T) {
	for _, body := range []string{
		codexProbeCompleteModel("different-model"),
		"data: [DONE]\n\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"incomplete\",\"model\":\"model\"}}\n\n",
	} {
		for _, failingStage := range []int{1, 2} {
			a := codexProbeAccount()
			calls := 0
			svc := &OpenAIGatewayService{accountRepo: &codexProbeAccounts{account: a}}
			svc.httpUpstream = &codexProbeUpstream{call: func(*http.Request, string, int64, int) (*http.Response, error) {
				calls++
				r := codexProbeSuccessResponse("model", codexProbeValidCandidate())
				if calls == failingStage {
					r.Body = io.NopCloser(strings.NewReader(body))
				}
				return r, nil
			}}
			got, err := svc.ProbeCodexTicket(codexProbeTestContext(a, "model"), a.ID, "model", "http://localhost:1")
			require.Error(t, err)
			require.Empty(t, got.Cookies)
			require.Empty(t, got.State)
			require.False(t, got.Verified)
		}
	}
}

func TestCodexTicketCookieBundleValidationAndInjection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*CodexTicket)
	}{
		{"missing pair", func(v *CodexTicket) { v.Cookies = nil }},
		{"partial pair", func(v *CodexTicket) { v.Cookies = v.Cookies[:1] }},
		{"cookie expired", func(v *CodexTicket) { v.Cookies[0].ExpiresAt = v.CapturedAt }},
		{"overlong bundle", func(v *CodexTicket) { v.ExpiresAt = v.CapturedAt.Add(241 * time.Second) }},
		{"wrong cookie domain", func(v *CodexTicket) { v.Cookies[0].Domain = "example.com" }},
		{"wrong account", func(v *CodexTicket) { v.Key.AccountID++ }},
		{"wrong model", func(v *CodexTicket) { v.Key.Model = "other" }},
		{"wrong route", func(v *CodexTicket) { v.Key.PolicyScope = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, a, s, k := ticketRuntimeFixture()
			tc.mutate(s.v.Ticket)
			h := http.Header{}
			_, err := r.ApplyWithReceipt(context.Background(), a.a, k.Model, h)
			require.ErrorIs(t, err, ErrCodexTicketMissing)
			require.Empty(t, h.Get("Cookie"))
			require.Empty(t, h.Get("X-Codex-Turn-State"))
		})
	}
	r, a, s, k := ticketRuntimeFixture()
	s.v.Ticket.BundleID = "immutable-cookie-bundle"
	h := http.Header{"Cookie": {"cflb=caller; login=never-forward"}, "cookie": {"oailb=lowercase-caller"}, "x-codex-turn-state": {"lowercase-echo"}}
	receipt, err := r.ApplyWithReceipt(context.Background(), a.a, k.Model, h)
	require.NoError(t, err)
	require.Equal(t, "cflb=fixture-cflb; oailb=fixture-oailb", h.Get("Cookie"))
	require.Equal(t, "immutable-cookie-bundle", receipt.bundleID)
	require.NotContains(t, h, "cookie")
	require.NotContains(t, h, "x-codex-turn-state")
	encoded, err := json.Marshal(codexTicketSnapshotStatus(CodexTicketAccountSnapshot{Ticket: s.v.Ticket}, k, s.v.Control.Settings, s.v.ServerTime))
	require.NoError(t, err)
	for _, secret := range []string{"fixture-cflb", "fixture-oailb", s.v.Ticket.State, "immutable-cookie-bundle", "cookies"} {
		require.NotContains(t, string(encoded), secret)
	}
}

type cookieBundleCommitStore struct {
	*ticketTestStore
	committed *CodexTicket
}

func (s *cookieBundleCommitStore) CommitIfCurrent(_ context.Context, _ string, ticket CodexTicket) (bool, error) {
	s.committed = &ticket
	return true, nil
}

func TestCodexTicketCookieBundleCommitDoesNotRestartLifetime(t *testing.T) {
	r, _, base, k := ticketRuntimeFixture()
	store := &cookieBundleCommitStore{ticketTestStore: base}
	r.cache = store
	captured := time.Now().Add(-100 * time.Second)
	cookies := append([]CodexTicketCookie(nil), base.v.Ticket.Cookies...)
	cookies[0].ExpiresAt = captured.Add(180 * time.Second)
	cookies[1].ExpiresAt = captured.Add(240 * time.Second)
	result := CodexTicketProbeResult{State: base.v.Ticket.State, Cookies: cookies, CapturedAt: captured, HTTPStatus: 200, Completed: true, Verified: true, IdentityScope: k.IdentityScope, PolicyScope: k.PolicyScope, ActualModel: k.Model, VerificationModel: k.Model}
	r.harvest(context.Background(), "owner", base.v.Control.Settings, "http://proxy", k, func(context.Context, int64, string, string) (CodexTicketProbeResult, error) { return result, nil })
	require.NotNil(t, store.committed)
	require.NotEmpty(t, store.committed.BundleID)
	require.Equal(t, 180*time.Second, store.committed.ExpiresAt.Sub(store.committed.CapturedAt))
	require.InDelta(t, 80, store.committed.ExpiresAt.Sub(base.v.ServerTime).Seconds(), 1)
	require.Equal(t, captured.Add(180*time.Second), cookies[0].ExpiresAt, "adapter data remains immutable")
}
