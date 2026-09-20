//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type ticketObservationAccounts struct {
	AccountRepository
	rows  map[int64]*Account
	reads int
}

func (a *ticketObservationAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	return a.rows[id], nil
}
func (a *ticketObservationAccounts) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	a.reads++
	out := []*Account{}
	for _, id := range ids {
		if a.rows[id] != nil {
			out = append(out, a.rows[id])
		}
	}
	return out, nil
}

type ticketObservationStore struct {
	*ticketTestStore
	rows          map[CodexTicketKey]CodexTicketAccountSnapshot
	writes, reads int
	fail          bool
	keys          []CodexTicketKey
	deadline      time.Duration
	readDelay     time.Duration
	batchError    error
}

func (s *ticketObservationStore) ReadForRequest(_ context.Context, id int64, scope, model string) (CodexTicketRuntimeView, error) {
	v := s.v
	v.Ticket = s.rows[CodexTicketKey{v.Control.Settings.Revision, id, scope, model}].Ticket
	return v, s.err
}
func (s *ticketObservationStore) ReadCodexTicketAccounts(_ context.Context, keys []CodexTicketKey) (map[CodexTicketKey]CodexTicketAccountSnapshot, error) {
	s.reads++
	time.Sleep(s.readDelay)
	if s.batchError != nil {
		return nil, s.batchError
	}
	s.keys = keys
	out := map[CodexTicketKey]CodexTicketAccountSnapshot{}
	for _, k := range keys {
		out[k] = s.rows[k]
	}
	return out, nil
}
func (s *ticketObservationStore) RecordCodexTicketDecision(ctx context.Context, k CodexTicketKey, outcome, reason, id string) error {
	s.writes++
	if d, ok := ctx.Deadline(); ok {
		s.deadline = time.Until(d)
	}
	if s.fail {
		return errors.New("secret failure")
	}
	row := s.rows[k]
	row.Observation.LastOutcome = &outcome
	row.Observation.LastReason = &reason
	if outcome == "header_set" {
		row.Observation.InjectionCount++
		now := time.Now()
		row.Observation.LastInjectedAt = &now
		row.Observation.LastRequestID = nil
		if id != "" {
			row.Observation.LastRequestID = &id
		}
	}
	s.rows[k] = row
	return nil
}
func ticketObservationFixture() (*CodexTicketRuntime, *ticketObservationAccounts, *ticketObservationStore, CodexTicketKey, CodexTicketKey) {
	r, a, s, k := ticketRuntimeFixture()
	cfg := s.v.Control.Settings
	cfg.Models = cfg.Models[:1]
	s.v.Control.Settings = cfg
	r.settings = &ticketTestSettings{cfg: cfg}
	b := *a.a
	b.ID = 2
	kb := k
	kb.AccountID = 2
	tb := *s.v.Ticket
	tb.Key = kb
	tb.State = "gAAAAA" + strings.Repeat("b", len(tb.State)-6)
	accounts := &ticketObservationAccounts{rows: map[int64]*Account{1: a.a, 2: &b}}
	store := &ticketObservationStore{ticketTestStore: s, rows: map[CodexTicketKey]CodexTicketAccountSnapshot{k: {Ticket: s.v.Ticket}, kb: {Ticket: &tb}}}
	r.accounts = accounts
	r.cache = store
	return r, accounts, store, k, kb
}
func TestCodexTicketAccountObservationApplyAndQueryIsolation(t *testing.T) {
	r, a, s, ka, kb := ticketObservationFixture()
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "de305d54-75b4-431b-adb2-eb6b9e546014")
	for _, k := range []CodexTicketKey{ka, kb, kb} {
		h := http.Header{}
		require.NoError(t, r.Apply(ctx, a.rows[k.AccountID], k.Model, h))
		require.Equal(t, s.rows[k].Ticket.State, h.Get("X-Codex-Turn-State"))
	}
	out, err := r.AccountStatuses(ctx, []int64{1, 2})
	require.NoError(t, err)
	require.Equal(t, 1, a.reads)
	require.Equal(t, 1, s.reads)
	require.EqualValues(t, 1, out.Accounts[0].Models[0].InjectionCount)
	require.EqualValues(t, 2, out.Accounts[1].Models[0].InjectionCount)
	require.Equal(t, "header_set", *out.Accounts[0].Models[0].LastOutcome)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "gAAAAA")
	require.NotContains(t, string(raw), ka.IdentityScope)
	require.Contains(t, string(raw), `"last_error_code":null`)
	a.rows[1].Credentials = map[string]any{"chatgpt_account_id": "new"}
	out, err = r.AccountStatuses(ctx, []int64{1, 2})
	require.NoError(t, err)
	require.Zero(t, out.Accounts[0].Models[0].InjectionCount)
	require.Equal(t, "waiting", out.Accounts[0].Status)
	require.EqualValues(t, 2, out.Accounts[1].Models[0].InjectionCount)
	r.settings.(*ticketTestSettings).cfg.Revision++
	out, err = r.AccountStatuses(ctx, []int64{1, 2})
	require.NoError(t, err)
	require.Equal(t, "waiting", out.Accounts[1].Status)
	require.Zero(t, out.Accounts[1].Models[0].InjectionCount)
}
func TestCodexTicketObservationOnlyHeaderSetAndBestEffort(t *testing.T) {
	r, a, s, k, _ := ticketObservationFixture()
	ctx := context.Background()
	h := http.Header{}
	s.fail = true
	require.NoError(t, r.Apply(ctx, a.rows[1], k.Model, h))
	require.NotEmpty(t, h.Get("X-Codex-Turn-State"))
	require.LessOrEqual(t, s.deadline, 150*time.Millisecond)
	s.fail = false
	r.ObserveCompactSkip(ctx, a.rows[1], k.Model)
	require.Zero(t, s.rows[k].Observation.InjectionCount)
	require.Equal(t, "compact", *s.rows[k].Observation.LastReason)
	row := s.rows[k]
	row.Ticket = nil
	s.rows[k] = row
	require.ErrorIs(t, r.Apply(ctx, a.rows[1], k.Model, h), ErrCodexTicketMissing)
	require.Zero(t, s.rows[k].Observation.InjectionCount)
	require.Equal(t, "rejected", *s.rows[k].Observation.LastOutcome)
	writes := s.writes
	require.NoError(t, r.Apply(ctx, a.rows[1], "unconfigured", h))
	a.rows[1].Extra = map[string]any{}
	require.NoError(t, r.Apply(ctx, a.rows[1], k.Model, h))
	require.Equal(t, writes, s.writes)
}
func TestCodexTicketObservationRequestIDOnlyCanonicalServerContext(t *testing.T) {
	valid := "de305d54-75b4-431b-adb2-eb6b9e546014"
	for _, id := range []string{valid, "", strings.Repeat("x", 4096), valid + "\r\n", "client-free-text", strings.ToUpper(valid), "urn:uuid:" + valid} {
		t.Run(id[:min(len(id), 36)], func(t *testing.T) {
			r, a, _, k, _ := ticketObservationFixture()
			ctx := context.WithValue(context.Background(), ctxkey.RequestID, valid)
			ctx = context.WithValue(ctx, ctxkey.ClientRequestID, id)
			headers := http.Header{"X-Request-Id": []string{valid}, "X-Client-Request-Id": []string{valid}}
			require.NoError(t, r.Apply(ctx, a.rows[1], k.Model, headers))
			out, err := r.AccountStatuses(ctx, []int64{1})
			require.NoError(t, err)
			if id == valid {
				require.Equal(t, valid, *out.Accounts[0].Models[0].LastRequestID)
			} else {
				require.Nil(t, out.Accounts[0].Models[0].LastRequestID)
				raw, err := json.Marshal(out)
				require.NoError(t, err)
				require.Contains(t, string(raw), `"last_request_id":null`)
			}
		})
	}
}

func TestCodexTicketAccountExpiryCooldownAndUnavailable(t *testing.T) {
	r, _, s, k, kb := ticketObservationFixture()
	ctx := context.Background()
	now := s.v.ServerTime
	row := s.rows[k]
	row.Ticket = nil
	row.Metadata = &CodexTicketMetadata{now.Add(-time.Hour), now.Add(-time.Second)}
	s.rows[k] = row
	out, err := r.AccountStatuses(ctx, []int64{1, 2})
	require.NoError(t, err)
	require.Equal(t, "expired", out.Accounts[0].Status)
	require.Equal(t, "ready", out.Accounts[1].Status)
	row.Retry = CodexTicketRetry{NextAttemptAt: now.Add(time.Minute), ErrorCode: "secret-token"}
	row.CooldownUntil = now.Add(time.Hour)
	s.rows[k] = row
	out, err = r.AccountStatuses(ctx, []int64{1, 2})
	require.NoError(t, err)
	require.Equal(t, "backoff", out.Accounts[0].Status)
	require.Equal(t, row.CooldownUntil, *out.Accounts[0].Models[0].NextAttemptAt)
	require.Equal(t, "probe_failed", *out.Accounts[0].Models[0].LastErrorCode)
	s.rows[kb].Ticket.ExpiresAt = now.Add(time.Second)
	out, err = r.AccountStatuses(ctx, []int64{2})
	require.NoError(t, err)
	require.Equal(t, "refreshing", out.Accounts[0].Status)
	s.err = errors.New("redis down")
	out, err = r.AccountStatuses(ctx, []int64{1, 2})
	require.NoError(t, err)
	require.Nil(t, out.AppliedRevision)
	require.Equal(t, "unavailable", out.Accounts[0].Status)
	require.Nil(t, out.Accounts[1].Models[0].LastOutcome)
}
