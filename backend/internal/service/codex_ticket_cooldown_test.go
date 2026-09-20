//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func (s *ticketTestStore) ProbeCooldownActive(context.Context, CodexTicketKey) (bool, error) {
	return false, nil
}
func (s *ticketTestStore) ExtendProbeCooldown(context.Context, string, CodexTicketKey, time.Time) error {
	return nil
}

type ticketCooldownStore struct {
	*ticketTestStore
	until          time.Time
	identity       string
	account        int64
	reads, budgets int
	failRead       bool
}

func (s *ticketCooldownStore) ProbeCooldownActive(_ context.Context, k CodexTicketKey) (bool, error) {
	s.reads++
	if s.failRead {
		return false, errors.New("cache unavailable")
	}
	return k.AccountID == s.account && k.IdentityScope == s.identity && s.until.After(s.v.ServerTime), nil
}
func (s *ticketCooldownStore) ExtendProbeCooldown(_ context.Context, _ string, k CodexTicketKey, until time.Time) error {
	s.identity, s.account = k.IdentityScope, k.AccountID
	if until.After(s.until) {
		s.until = until
	}
	return nil
}
func (s *ticketCooldownStore) AcquireProbeBudget(context.Context, string, uint64, int) (bool, error) {
	s.budgets++
	return true, nil
}
func TestCodexTicketSharedCooldownAcrossModelsAndRevisions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  CodexTicketProbeResult
		minimum time.Duration
	}{
		{"401", CodexTicketProbeResult{HTTPStatus: 401}, time.Hour},
		{"403", CodexTicketProbeResult{HTTPStatus: 403}, time.Hour},
		{"429", CodexTicketProbeResult{HTTPStatus: 429, RetryAfter: "7200"}, 2 * time.Hour},
		{"SSE quota", CodexTicketProbeResult{HTTPStatus: 200, ErrorCode: "quota_limited"}, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, base, key := ticketRuntimeFixture()
			s := &ticketCooldownStore{ticketTestStore: base}
			r.cache = s
			ctx := context.Background()
			r.recordProbeFailure(ctx, "owner", key, CodexTicketRetry{}, tc.result, errors.New("redacted"))
			require.GreaterOrEqual(t, s.until.Sub(base.v.ServerTime), tc.minimum)
			cfg := base.v.Control.Settings
			for _, revision := range []uint64{key.Revision, key.Revision + 1} {
				for _, model := range []string{key.Model, "other-model"} {
					k := key
					k.Revision, k.Model = revision, model
					cfg.Revision = revision
					r.harvest(ctx, "owner", cfg, "http://different-proxy", k, func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
						t.Fatal("probe during shared cooldown")
						return CodexTicketProbeResult{}, nil
					})
				}
			}
			require.Zero(t, s.budgets, "blocked accounts must not consume global budget")
			base.v.ServerTime = s.until.Add(time.Second)
			called := false
			r.harvest(ctx, "owner", cfg, "http://proxy", key, func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
				called = true
				return CodexTicketProbeResult{HTTPStatus: 503}, errors.New("failed")
			})
			require.True(t, called, "cooldown must expire without manual reset")
		})
	}
}
func TestCodexTicketCooldownDoesNotAffectBusinessApply(t *testing.T) {
	r, accounts, base, key := ticketRuntimeFixture()
	s := &ticketCooldownStore{ticketTestStore: base, until: base.v.ServerTime.Add(time.Hour), identity: key.IdentityScope, account: key.AccountID}
	r.cache = s
	h := http.Header{}
	require.NoError(t, r.Apply(context.Background(), accounts.a, key.Model, h))
	require.Equal(t, base.v.Ticket.State, h.Get("X-Codex-Turn-State"))
	require.Zero(t, s.reads)
	s.failRead = true
	r.harvest(context.Background(), "owner", base.v.Control.Settings, "http://proxy", key, func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
		t.Fatal("cache failure must stop probe")
		return CodexTicketProbeResult{}, nil
	})
	require.Zero(t, s.budgets)
}
func TestCodexTicketOrdinaryFailureDoesNotCreateSharedCooldown(t *testing.T) {
	r, _, base, key := ticketRuntimeFixture()
	s := &ticketCooldownStore{ticketTestStore: base}
	r.cache = s
	r.recordProbeFailure(context.Background(), "owner", key, CodexTicketRetry{}, CodexTicketProbeResult{HTTPStatus: 200, ErrorCode: "stream_failed"}, errors.New("capacity"))
	require.True(t, s.until.IsZero())
}
func TestCodexProbeStableQuotaCodesRedacted(t *testing.T) {
	for _, tc := range []struct{ name, payload, want string }{
		{"nested rate", `{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"SECRET"}}}`, "quota_limited"},
		{"nested quota", `{"type":"response.failed","response":{"error":{"code":"insufficient_quota"}}}`, "quota_limited"},
		{"error object", `{"type":"error","error":{"code":"rate_limit_exceeded"}}`, "quota_limited"},
		{"top level", `{"type":"error","code":"insufficient_quota"}`, "quota_limited"},
		{"event only", `{"code":"rate_limit_exceeded"}`, "quota_limited"},
		{"capacity", `{"type":"response.failed","response":{"error":{"code":"server_is_overloaded"}}}`, "response_failed"},
		{"slow down", `{"type":"error","code":"slow_down"}`, "response_failed"},
		{"message not code", `{"type":"error","message":"rate_limit_exceeded SECRET"}`, "response_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "event: error\ndata: " + tc.payload + "\n\n"
			require.Equal(t, tc.want, readCodexProbeCompletion(strings.NewReader(body)))
			svc := &OpenAIGatewayService{accountRepo: &codexProbeAccounts{account: codexProbeAccount()}}
			svc.httpUpstream = &codexProbeUpstream{call: func(*http.Request, string, int64, int) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Retry-After": []string{"7200"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}}
			result, err := svc.ProbeCodexTicket(context.Background(), 7, "model", "http://localhost:1")
			require.EqualError(t, err, tc.want)
			require.NotContains(t, err.Error(), "SECRET")
			require.Empty(t, result.State)
			require.False(t, result.Completed)
			require.Equal(t, 200, result.HTTPStatus)
			require.Equal(t, "7200", result.RetryAfter)
			expected := "stream_failed"
			if tc.want == "quota_limited" {
				expected = "quota_limited"
			}
			require.Equal(t, expected, result.ErrorCode)
		})
	}
}
