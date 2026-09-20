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

type statekitBudgetStore struct {
	*ticketCooldownStore
	limit, used int
	committed   []CodexTicket
	retry       CodexTicketRetry
}

func (s *statekitBudgetStore) AcquireProbeBudget(context.Context, string, uint64, int) (bool, error) {
	if s.used >= s.limit {
		return false, nil
	}
	s.used++
	return true, nil
}
func (s *statekitBudgetStore) CommitIfCurrent(_ context.Context, _ string, t CodexTicket) (bool, error) {
	s.committed = append(s.committed, t)
	return true, nil
}
func (s *statekitBudgetStore) WriteRetry(_ context.Context, _ string, _ CodexTicketKey, retry CodexTicketRetry) error {
	s.retry = retry
	return nil
}

func TestCodexTicketStatekitHarvestBudgetsAndVerification(t *testing.T) {
	for _, tc := range []struct {
		name          string
		limit         int
		verifyModel   string
		responseState string
		status        int
		wantCommit    bool
		wantCalls     int
		wantError     string
	}{
		{"verified", 2, "same", "", 200, true, 2, ""},
		{"only_capture_budget", 1, "same", "", 200, false, 1, "verification_failed"},
		{"wrong_verification_model", 2, "different", "", 200, false, 2, "model_mismatch"},
		{"signal312", 2, "same", "gAAAAA" + strings.Repeat("s", 306), 200, false, 2, "state_312"},
		{"verify_auth", 2, "same", "", 401, false, 2, "authentication_failed"},
		{"verify_rate", 2, "same", "", 429, false, 2, "rate_limited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, accounts, base, k := ticketRuntimeFixture()
			// Keep an old valid ticket: renewal failures must neither delete nor extend it.
			old := *base.v.Ticket
			store := &statekitBudgetStore{ticketCooldownStore: &ticketCooldownStore{ticketTestStore: base}, limit: tc.limit}
			r.cache = store
			accounts.a.Credentials["access_token"] = "private-token"
			gateway := &OpenAIGatewayService{accountRepo: accounts, codexTicketRuntime: r}
			calls := 0
			gateway.httpUpstream = &codexProbeUpstream{call: func(req *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
				calls++
				model := k.Model
				state := old.State
				status := 200
				if calls == 1 {
					require.Equal(t, "http://harvest.invalid:8080", proxy)
					require.Empty(t, req.Header.Get("X-Codex-Turn-State"))
				} else {
					require.Empty(t, proxy, "explicit direct account verifies without harvesting proxy")
					require.Equal(t, old.State, req.Header.Get("X-Codex-Turn-State"))
					state = tc.responseState
					status = tc.status
					if tc.verifyModel != "same" {
						model = tc.verifyModel
					}
				}
				body := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"" + model + "\"}}\n\n"
				h := http.Header{}
				if state != "" {
					h.Set("X-Codex-Turn-State", state)
				}
				return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
			}}
			r.harvest(context.Background(), "owner", base.v.Control.Settings, "http://harvest.invalid:8080", k, gateway.ProbeCodexTicket)
			require.Equal(t, tc.wantCalls, calls)
			if tc.wantCommit {
				require.Len(t, store.committed, 1)
				require.True(t, store.committed[0].Verified)
				require.Equal(t, k.Model, store.committed[0].ActualModel)
				require.Equal(t, k.Model, store.committed[0].VerificationModel)
			} else {
				require.Empty(t, store.committed)
				require.Equal(t, tc.wantError, store.retry.ErrorCode)
			}
			require.Equal(t, old, *base.v.Ticket)
			if tc.status == 401 || tc.status == 429 {
				require.True(t, store.until.After(base.v.ServerTime))
				changed := k
				changed.PolicyScope = "another-plan-and-route"
				blocked, err := store.ProbeCooldownActive(context.Background(), changed)
				require.NoError(t, err)
				require.True(t, blocked)
			}
		})
	}
}

type statekitReceiptStore struct {
	*ticketObservationStore
	invalidations int
}

func (s *statekitReceiptStore) InvalidateCodexTicket(_ context.Context, t CodexTicket, reason string) (bool, error) {
	row := s.rows[t.Key]
	if row.Ticket == nil || row.Ticket.Key != t.Key || row.Ticket.State != t.State || !row.Ticket.CapturedAt.Equal(t.CapturedAt) {
		return false, nil
	}
	s.invalidations++
	row.Ticket = nil
	row.Observation.InvalidationCount++
	now := s.v.ServerTime
	row.Observation.LastInvalidatedAt = &now
	row.Observation.LastInvalidationReason = &reason
	s.rows[t.Key] = row
	return true, nil
}
func TestCodexTicketStatekitWatcherCancelsPlanAndRouteChange(t *testing.T) {
	for _, change := range []string{"plan", "route"} {
		t.Run(change, func(t *testing.T) {
			r, accounts, _, k := ticketRuntimeFixture()
			ctx, stop := r.watchCodexTicketAccount(context.Background(), k, 5*time.Millisecond)
			defer stop()
			accounts.mu.Lock()
			if change == "plan" {
				accounts.a.Extra = map[string]any{CodexTurnStateEnabledExtraKey: true, CodexTurnStatePlanExtraKey: "team"}
			} else {
				id := int64(9)
				accounts.a.ProxyID = &id
			}
			accounts.mu.Unlock()
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("plan/route change did not cancel current harvest")
			}
		})
	}
}

type statekitNoReadReceiptStore struct{ *statekitReceiptStore }

func (*statekitNoReadReceiptStore) ReadForRequest(context.Context, int64, string, string, ...string) (CodexTicketRuntimeView, error) {
	panic("invalidator must not use the ordinary Redis request reader")
}
func TestCodexTicketReceiptInvalidationDoesNotPerformOrdinaryCacheRead(t *testing.T) {
	r, accounts, base, k, _ := ticketObservationFixture()
	store := &statekitReceiptStore{ticketObservationStore: base}
	r.cache = store
	receipt, err := r.ApplyWithReceipt(context.Background(), accounts.rows[1], k.Model, http.Header{})
	require.NoError(t, err)
	require.NotNil(t, receipt)
	r.cache = &statekitNoReadReceiptStore{statekitReceiptStore: store}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	require.True(t, r.InvalidateReceipt(ctx, *receipt, "model_mismatch"))
	require.Equal(t, 1, store.invalidations)
}

func TestCodexTicketReceiptFencesPlanRouteIdentityAndRevision(t *testing.T) {
	for _, change := range []string{"none", "plan", "route", "identity", "revision", "disabled", "bad_reason", "new_ticket"} {
		t.Run(change, func(t *testing.T) {
			r, accounts, base, k, _ := ticketObservationFixture()
			store := &statekitReceiptStore{ticketObservationStore: base}
			r.cache = store
			receipt, err := r.ApplyWithReceipt(context.Background(), accounts.rows[1], k.Model, http.Header{})
			require.NoError(t, err)
			require.NotNil(t, receipt)
			raw, err := json.Marshal(receipt)
			require.NoError(t, err)
			require.JSONEq(t, `{}`, string(raw))
			reason := "model_mismatch"
			switch change {
			case "plan":
				accounts.rows[1].Extra[CodexTurnStatePlanExtraKey] = "team"
			case "route":
				id := int64(8)
				accounts.rows[1].ProxyID = &id
			case "identity":
				accounts.rows[1].Credentials = map[string]any{"chatgpt_account_id": "new"}
			case "revision":
				r.settings.(*ticketTestSettings).cfg.Revision++
			case "disabled":
				accounts.rows[1].Extra[CodexTurnStateEnabledExtraKey] = false
			case "bad_reason":
				reason = "private-upstream-free-text"
			case "new_ticket":
				copy := *base.rows[k].Ticket
				copy.CapturedAt = copy.CapturedAt.Add(time.Millisecond)
				row := base.rows[k]
				row.Ticket = &copy
				base.rows[k] = row
			}
			matched := r.InvalidateReceipt(context.Background(), *receipt, reason)
			require.Equal(t, change == "none", matched)
			if matched {
				require.Equal(t, 1, store.invalidations)
				require.False(t, r.InvalidateReceipt(context.Background(), *receipt, reason))
			} else {
				require.Zero(t, store.invalidations)
			}
		})
	}
}
