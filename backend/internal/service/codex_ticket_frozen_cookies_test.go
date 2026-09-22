//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// Model the shared store's whole-record replacement, never a separate cookie jar.
type frozenCookieStore struct{ *ticketTestStore }

func (s *frozenCookieStore) CommitIfCurrent(_ context.Context, _ string, ticket CodexTicket) (bool, error) {
	s.commits++
	s.v.Ticket = &ticket
	return true, nil
}

func TestCodexTicketFrozenBundleSurvivesRejectedRenewal(t *testing.T) {
	for _, tc := range []struct {
		name        string
		stage       int
		stateLength int
		body        string
		partial     bool
	}{
		{name: "capture_312", stage: 1, stateLength: 312},
		{name: "verification_312", stage: 2, stateLength: 312},
		{name: "capture_wrong_model", stage: 1, body: codexProbeCompleteModel("different-model")},
		{name: "verification_wrong_model", stage: 2, body: codexProbeCompleteModel("different-model")},
		{name: "capture_truncated", stage: 1, body: "data: [DONE]\n\n"},
		{name: "verification_truncated", stage: 2, body: "data: [DONE]\n\n"},
		{name: "capture_partial_pair", stage: 1, partial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, accounts, base, key := ticketRuntimeFixture()
			store := &frozenCookieStore{base}
			r.cache = store
			accounts.a.Credentials["access_token"] = "synthetic-token"
			original := *store.v.Ticket
			originalCookies := append([]CodexTicketCookie(nil), original.Cookies...)
			svc := &OpenAIGatewayService{accountRepo: accounts, codexTicketRuntime: r}
			calls := 0
			renewalOK := false
			candidate := "gAAAAA" + strings.Repeat("b", 286)
			svc.httpUpstream = &codexProbeUpstream{call: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				calls++
				if calls == 1 {
					require.Empty(t, req.Header.Get("Cookie"), "a new capture never replays a previous jar")
					require.Empty(t, req.Header.Get("X-Codex-Turn-State"))
				} else {
					require.Equal(t, candidate, req.Header.Get("X-Codex-Turn-State"))
					require.Equal(t, "cflb=candidate-c; oailb=candidate-o", req.Header.Get("Cookie"))
				}
				resp := codexProbeSuccessResponse(key.Model, candidate)
				resp.Header["Set-Cookie"] = []string{"cflb=candidate-c; Max-Age=240; Path=/", "oailb=candidate-o; Max-Age=240; Path=/"}
				if calls == 2 {
					// Even a successful verification must not renew or replace a member.
					resp.Header["Set-Cookie"] = []string{"cflb=verification-c; Max-Age=3600; Path=/", "oailb=verification-o; Max-Age=3600; Path=/"}
				}
				if !renewalOK && calls == tc.stage {
					resp.Header["Set-Cookie"] = []string{"cflb=discarded-c; Max-Age=240; Path=/", "oailb=discarded-o; Max-Age=240; Path=/"}
					if tc.stateLength != 0 {
						resp.Header.Set("X-Codex-Turn-State", "gAAAAA"+strings.Repeat("x", tc.stateLength-6))
					}
					if tc.body != "" {
						resp.Body = io.NopCloser(strings.NewReader(tc.body))
					}
					if tc.partial {
						resp.Header["Set-Cookie"] = resp.Header["Set-Cookie"][:1]
					}
				}
				return resp, nil
			}}
			r.harvest(context.Background(), "owner", base.v.Control.Settings, "http://harvest.invalid", key, svc.ProbeCodexTicket)
			require.Equal(t, tc.stage, calls)
			require.Zero(t, store.commits)
			require.Equal(t, original, *store.v.Ticket)
			require.Equal(t, originalCookies, store.v.Ticket.Cookies)
			headers := http.Header{}
			require.NoError(t, r.Apply(context.Background(), accounts.a, key.Model, headers))
			require.Equal(t, original.State, headers.Get("X-Codex-Turn-State"))
			require.Equal(t, "cflb=fixture-cflb; oailb=fixture-oailb", headers.Get("Cookie"))

			// A later fully verified capture replaces the entire bundle. Neither
			// rejected cookies nor successful verification cookies can leak into it.
			calls, renewalOK = 0, true
			r.harvest(context.Background(), "owner", base.v.Control.Settings, "http://harvest.invalid", key, svc.ProbeCodexTicket)
			require.Equal(t, 2, calls)
			require.Equal(t, 1, store.commits)
			require.NotEmpty(t, store.v.Ticket.BundleID)
			headers = http.Header{}
			require.NoError(t, r.Apply(context.Background(), accounts.a, key.Model, headers))
			require.Equal(t, candidate, headers.Get("X-Codex-Turn-State"))
			require.Equal(t, "cflb=candidate-c; oailb=candidate-o", headers.Get("Cookie"))
		})
	}
}

func TestCodexTicketFrozenBundleNeverLearnsBusinessResponseCookies(t *testing.T) {
	for _, tc := range []struct {
		name        string
		stateLength int
		status      int
		wrongModel  bool
		invalidates bool
	}{
		{"target_response", 292, 200, false, false},
		{"state_312", 312, 200, false, true},
		{"without_state", 0, 200, false, false},
		{"failed_312", 312, 500, false, false},
		{"wrong_model", 292, 200, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, accounts, base, key, otherKey := ticketObservationFixture()
			store := &statekitReceiptStore{ticketObservationStore: base}
			r.cache = store
			original := *base.rows[key].Ticket
			other := *base.rows[otherKey].Ticket
			req, err := http.NewRequest(http.MethodPost, chatgptCodexURL, nil)
			require.NoError(t, err)
			receipt, err := r.ApplyWithReceipt(context.Background(), accounts.rows[key.AccountID], key.Model, req.Header)
			require.NoError(t, err)
			require.NotNil(t, receipt)
			attachCodexTicketReceipt(req, receipt)
			model := key.Model
			if tc.wrongModel {
				model = "another-model"
			}
			state := ""
			if tc.stateLength > 0 {
				state = "gAAAAA" + strings.Repeat("z", tc.stateLength-6)
			}
			resp := codexProbeSuccessResponse(model, state)
			resp.StatusCode = tc.status
			resp.Header["Set-Cookie"] = []string{"cflb=business-c; Max-Age=3600; Path=/", "oailb=business-o; Max-Age=3600; Path=/"}
			svc := &OpenAIGatewayService{codexTicketRuntime: r}
			svc.observeCodexTicketResponse(req, resp)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, codexProbeCompleteModel(model), string(body))
			headers := http.Header{}
			err = r.Apply(context.Background(), accounts.rows[key.AccountID], key.Model, headers)
			if tc.invalidates {
				require.Equal(t, 1, store.invalidations)
				require.Nil(t, base.rows[key].Ticket)
				require.ErrorIs(t, err, ErrCodexTicketMissing)
				require.Empty(t, headers)
			} else {
				require.Zero(t, store.invalidations)
				require.Equal(t, original, *base.rows[key].Ticket)
				require.NoError(t, err)
				require.Equal(t, "cflb=fixture-cflb; oailb=fixture-oailb", headers.Get("Cookie"))
			}
			require.Equal(t, other, *base.rows[otherKey].Ticket)
		})
	}
}

type delayedCookieBody struct{ io.Reader }

func (b delayedCookieBody) Read(p []byte) (int, error) {
	time.Sleep(2 * time.Second)
	return b.Reader.Read(p)
}
func (b delayedCookieBody) Close() error { return nil }

func TestCodexProbeFrozenBundleExpiryDuringVerification(t *testing.T) {
	for _, mode := range []string{CodexTicketCookiePinRequired, CodexTicketCookiePinOptional} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				account := codexProbeAccount()
				ctx := codexProbeTestContext(account, "model")
				task := ctx.Value(codexTicketProbeTaskKey{}).(codexTicketProbeTask)
				task.Settings.CookiePinMode = mode
				ctx = context.WithValue(ctx, codexTicketProbeTaskKey{}, task)
				calls := 0
				svc := &OpenAIGatewayService{accountRepo: &codexProbeAccounts{account: account}}
				svc.httpUpstream = &codexProbeUpstream{call: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
					calls++
					resp := codexProbeSuccessResponse("model", codexProbeValidCandidate())
					if calls == 1 {
						resp.Header["Set-Cookie"] = []string{"cflb=short-c; Max-Age=1; Path=/", "oailb=short-o; Max-Age=1; Path=/"}
					} else {
						require.Equal(t, "cflb=short-c; oailb=short-o", req.Header.Get("Cookie"))
						resp.Body = delayedCookieBody{strings.NewReader(codexProbeCompleteModel("model"))}
					}
					return resp, nil
				}}
				result, err := svc.ProbeCodexTicket(ctx, account.ID, "model", "http://harvest.invalid")
				require.Equal(t, 2, calls)
				require.EqualError(t, err, "cookie_missing")
				require.False(t, result.Verified)
				require.Empty(t, result.State)
				require.Empty(t, result.Cookies)
			})
		})
	}
}
