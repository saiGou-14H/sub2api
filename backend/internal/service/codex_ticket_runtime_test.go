//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ticketTestAccounts struct {
	AccountRepository
	mu sync.Mutex
	a  *Account
}

func (a *ticketTestAccounts) GetByID(context.Context, int64) (*Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.a == nil {
		return nil, errors.New("missing")
	}
	copy := *a.a
	return &copy, nil
}

func (a *ticketTestAccounts) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.a == nil {
		return nil, nil
	}
	for _, id := range ids {
		if id == a.a.ID {
			copy := *a.a
			return []*Account{&copy}, nil
		}
	}
	return nil, nil
}

type ticketTestSettings struct {
	CodexTicketSettingsRepository
	cfg CodexTicketSettings
}

func (s *ticketTestSettings) Load(context.Context) (CodexTicketSettings, error) { return s.cfg, nil }

type ticketTestStore struct {
	CodexTicketRuntimeStore
	v       CodexTicketRuntimeView
	err     error
	commits int
	budget  bool
}

func (s *ticketTestStore) ReadForRequest(context.Context, int64, string, string, ...string) (CodexTicketRuntimeView, error) {
	return s.v, s.err
}
func (s *ticketTestStore) ReadRetry(context.Context, CodexTicketKey) (CodexTicketRetry, error) {
	return CodexTicketRetry{}, nil
}
func (s *ticketTestStore) Now(context.Context) (time.Time, error) { return s.v.ServerTime, nil }
func (s *ticketTestStore) AcquireProbeBudget(context.Context, string, uint64, int) (bool, error) {
	return s.budget, nil
}
func (s *ticketTestStore) CheckCurrent(context.Context, string, uint64) (bool, error) {
	return true, nil
}
func (s *ticketTestStore) CommitIfCurrent(context.Context, string, CodexTicket) (bool, error) {
	s.commits++
	return true, nil
}
func (s *ticketTestStore) WriteRetry(context.Context, string, CodexTicketKey, CodexTicketRetry) error {
	return nil
}
func ticketRuntimeFixture() (*CodexTicketRuntime, *ticketTestAccounts, *ticketTestStore, CodexTicketKey) {
	cfg := DefaultCodexTicketSettings()
	cfg.Enabled = true
	cfg.Revision = 2
	cfg.MissingPolicy = CodexTicketReject
	a := &Account{ID: 1, Platform: "openai", Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Extra: map[string]any{"codex_turn_state_enabled": true}, Credentials: map[string]any{"chatgpt_account_id": "identity"}}
	accounts := &ticketTestAccounts{a: a}
	now := time.Now()
	key := CodexTicketKey{Revision: cfg.Revision, AccountID: a.ID, IdentityScope: CodexTicketIdentityScope(a), Model: cfg.Models[0], PolicyScope: CodexTicketPolicyScope(a, now)}
	ticket := &CodexTicket{Key: key, State: "gAAAAA" + strings.Repeat("a", cfg.TargetLength-6), CapturedAt: now, ExpiresAt: now.Add(time.Hour), Verified: true, VerifiedAt: now, ActualModel: key.Model, VerificationModel: key.Model, TargetLength: cfg.TargetLength}
	store := &ticketTestStore{v: CodexTicketRuntimeView{Control: CodexTicketControl{Settings: cfg, ProxyState: "active", ValidUntilMS: now.Add(6 * time.Second).UnixMilli()}, Ticket: ticket, ServerTime: now}, budget: true}
	return NewCodexTicketRuntime(&ticketTestSettings{cfg: cfg}, nil, accounts, store), accounts, store, key
}
func TestCodexTicketApplyFreshAccountSwitchAndIdentity(t *testing.T) {
	r, a, s, key := ticketRuntimeFixture()
	stale := *a.a
	h := http.Header{"X-Codex-Turn-State": []string{"echo"}}
	require.NoError(t, r.Apply(context.Background(), &stale, key.Model, h))
	require.Equal(t, s.v.Ticket.State, h.Get("X-Codex-Turn-State"))
	a.a.Extra = map[string]any{"codex_turn_state_enabled": false}
	h.Set("X-Codex-Turn-State", "echo")
	require.NoError(t, r.Apply(context.Background(), &stale, key.Model, h))
	require.Equal(t, "echo", h.Get("X-Codex-Turn-State"))
	a.a.Extra = map[string]any{"codex_turn_state_enabled": true}
	a.a.Credentials = map[string]any{"chatgpt_account_id": "changed"}
	require.ErrorIs(t, r.Apply(context.Background(), &stale, key.Model, h), ErrCodexTicketMissing)
	a.a.Credentials = map[string]any{"chatgpt_account_id": "identity"}
	require.NoError(t, r.Apply(context.Background(), &stale, key.Model, h))
	require.Equal(t, s.v.Ticket.State, h.Get("X-Codex-Turn-State"))
}
func TestCodexTicketApplyFreshnessAndMissingPolicy(t *testing.T) {
	r, a, s, k := ticketRuntimeFixture()
	h := http.Header{}
	s.v.Ticket.ExpiresAt = s.v.ServerTime
	require.ErrorIs(t, r.Apply(context.Background(), a.a, k.Model, h), ErrCodexTicketMissing)
	s.err = errors.New("redis down")
	require.ErrorIs(t, r.Apply(context.Background(), a.a, k.Model, h), ErrCodexTicketControlUnavailable)
	require.NoError(t, r.Apply(context.Background(), a.a, "not-configured", h))
	r.settings.(*ticketTestSettings).cfg.MissingPolicy = CodexTicketPassthrough
	require.NoError(t, r.Apply(context.Background(), a.a, k.Model, h))
}
func TestCodexTicketHarvestPrecommitAccountRecheck(t *testing.T) {
	r, a, s, k := ticketRuntimeFixture()
	cfg := s.v.Control.Settings
	r.harvest(context.Background(), "owner", cfg, "http://proxy", k, func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
		a.a.Extra = map[string]any{"codex_turn_state_enabled": false}
		return CodexTicketProbeResult{State: s.v.Ticket.State, IdentityScope: k.IdentityScope, HTTPStatus: 200, Completed: true, Verified: true, PolicyScope: k.PolicyScope, ActualModel: k.Model, VerificationModel: k.Model}, nil
	})
	require.Zero(t, s.commits)
}
func TestCodexTicketHarvestNoBudgetNoProbe(t *testing.T) {
	r, _, s, k := ticketRuntimeFixture()
	s.budget = false
	called := false
	r.harvest(context.Background(), "owner", s.v.Control.Settings, "http://proxy", k, func(context.Context, int64, string, string) (CodexTicketProbeResult, error) {
		called = true
		return CodexTicketProbeResult{}, nil
	})
	require.False(t, called)
}
func TestCodexTicketRetryBounds(t *testing.T) {
	require.GreaterOrEqual(t, CodexTicketRetryDelay(8, 0, 0), time.Hour)
	require.Equal(t, 2*time.Hour, CodexTicketRetryDelay(1, 2*time.Hour, 1))
	now := time.Now().Truncate(time.Second)
	require.Equal(t, 120*time.Second, parseCodexRetryAfter("120", now))
	require.Equal(t, time.Minute, parseCodexRetryAfter(now.Add(time.Minute).UTC().Format(http.TimeFormat), now))
	require.Zero(t, parseCodexRetryAfter("invalid", now))
}
func TestValidateCodexTicketExpiryAndActualLength(t *testing.T) {
	_, _, s, k := ticketRuntimeFixture()
	ticket := *s.v.Ticket
	require.NoError(t, ValidateCodexTicket(ticket, k, s.v.Control.Settings, s.v.ServerTime))
	ticket.State += "a"
	require.Error(t, ValidateCodexTicket(ticket, k, s.v.Control.Settings, s.v.ServerTime))
	ticket = *s.v.Ticket
	ticket.ExpiresAt = time.Time{}
	require.Error(t, ValidateCodexTicket(ticket, k, s.v.Control.Settings, s.v.ServerTime))
}

func TestCodexTicketUnavailableUsesKnownPolicyWithoutInjecting(t *testing.T) {
	r, a, store, key := ticketRuntimeFixture()
	h := http.Header{}
	require.NoError(t, r.Apply(context.Background(), a.a, key.Model, h))
	known := r.settings.(*ticketTestSettings).cfg
	failed := &ticketFailingSettings{cfg: known}
	failed.fail.Store(true)
	r.settings = failed
	store.err = errors.New("redis unavailable")
	h.Set("X-Codex-Turn-State", "guarded-echo")
	require.ErrorIs(t, r.Apply(context.Background(), a.a, key.Model, h), ErrCodexTicketControlUnavailable)
	require.Equal(t, "guarded-echo", h.Get("X-Codex-Turn-State"))
	// A newer confirmed disabled revision supersedes the remembered reject policy.
	known.Revision++
	known.Enabled = false
	r.rememberSettings(known)
	require.NoError(t, r.Apply(context.Background(), a.a, key.Model, h))
	require.Equal(t, "guarded-echo", h.Get("X-Codex-Turn-State"))
}

func TestCodexTicketAccountReadFailureDoesNotGateDisabledFeature(t *testing.T) {
	r, accounts, _, key := ticketRuntimeFixture()
	account := *accounts.a
	accounts.a = nil
	r.settings.(*ticketTestSettings).cfg.Enabled = false
	require.NoError(t, r.Apply(context.Background(), &account, key.Model, http.Header{}))
}

func TestCodexTicketStatusContainsSummaryOnly(t *testing.T) {
	r, _, store, key := ticketRuntimeFixture()
	r.ready[key] = store.v.Ticket.ExpiresAt
	status, err := r.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, "local_instance_observed", status.CounterScope)
	require.Equal(t, 1, status.ReadyCount)
	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), store.v.Ticket.State)
	require.NotContains(t, string(encoded), key.IdentityScope)
	require.NotContains(t, string(encoded), "proxy_url")
}

func TestCodexTicketApplyDoesNotMutateAccountSnapshot(t *testing.T) {
	r, accounts, _, key := ticketRuntimeFixture()
	beforeExtra, err := json.Marshal(accounts.a.Extra)
	require.NoError(t, err)
	beforeCredentials, err := json.Marshal(accounts.a.Credentials)
	require.NoError(t, err)
	require.NoError(t, r.Apply(context.Background(), accounts.a, key.Model, http.Header{}))
	afterExtra, err := json.Marshal(accounts.a.Extra)
	require.NoError(t, err)
	afterCredentials, err := json.Marshal(accounts.a.Credentials)
	require.NoError(t, err)
	require.Equal(t, string(beforeExtra), string(afterExtra))
	require.Equal(t, string(beforeCredentials), string(afterCredentials))
}

func TestCodexTicketUnsupportedAccountNeverReadsDependencies(t *testing.T) {
	for _, account := range []*Account{
		{ID: 1, Platform: "openai", Type: AccountTypeAPIKey},
		{ID: 2, Platform: "openai", Type: AccountTypeOAuth, Extra: map[string]any{OpenAIWebTransportExtraKey: OpenAITransportPrism, "codex_turn_state_enabled": true}},
	} {
		r := NewCodexTicketRuntime(nil, nil, nil, nil)
		h := http.Header{}
		h.Set("X-Codex-Turn-State", "guarded-echo")
		require.NoError(t, r.Apply(context.Background(), account, "gpt-6-astra", h))
		require.Equal(t, "guarded-echo", h.Get("X-Codex-Turn-State"))
	}
}

type ticketDeadlineAccounts struct {
	AccountRepository
	deadline time.Time
	bounded  bool
}

func (a *ticketDeadlineAccounts) GetByID(ctx context.Context, _ int64) (*Account, error) {
	a.deadline, a.bounded = ctx.Deadline()
	return nil, errors.New("account read failed")
}
func TestCodexTicketAccountReadFailurePolicyAndDeadline(t *testing.T) {
	for _, tc := range []struct {
		name           string
		optIn, enabled bool
		policy         CodexTicketMissingPolicy
		reject         bool
	}{
		{"default account off", false, true, CodexTicketReject, false},
		{"global off", true, false, CodexTicketReject, false},
		{"passthrough", true, true, CodexTicketPassthrough, false},
		{"explicit reject", true, true, CodexTicketReject, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, a, _, key := ticketRuntimeFixture()
			a.a.Extra = map[string]any{"codex_turn_state_enabled": tc.optIn}
			cfg := r.settings.(*ticketTestSettings)
			cfg.cfg.Enabled = tc.enabled
			cfg.cfg.MissingPolicy = tc.policy
			accounts := &ticketDeadlineAccounts{}
			r.accounts = accounts
			r.cache = nil
			h := http.Header{}
			h.Set("X-Codex-Turn-State", "guarded-echo")
			started := time.Now()
			err := r.Apply(context.Background(), a.a, key.Model, h)
			if tc.reject {
				require.ErrorIs(t, err, ErrCodexTicketControlUnavailable)
			} else {
				require.NoError(t, err)
			}
			require.True(t, accounts.bounded)
			require.Greater(t, accounts.deadline.Sub(started), time.Duration(0))
			require.LessOrEqual(t, accounts.deadline.Sub(started), 2*time.Second+100*time.Millisecond)
			require.Equal(t, "guarded-echo", h.Get("X-Codex-Turn-State"))
		})
	}
}

func TestCodexTicketStatusRevisionMismatchNeverReady(t *testing.T) {
	for _, applied := range []uint64{1, 3} {
		r, _, store, key := ticketRuntimeFixture()
		r.ready[key] = store.v.Ticket.ExpiresAt
		store.v.Control.Settings.Revision = applied
		status, err := r.Status(context.Background())
		require.NoError(t, err)
		require.Equal(t, "applying", status.Phase)
		require.NotNil(t, status.AppliedRevision)
		require.NotEqual(t, status.DesiredRevision, *status.AppliedRevision)
	}
}

func TestCodexTicketDisabledAccountDoesNotReadRedis(t *testing.T) {
	r, accounts, _, key := ticketRuntimeFixture()
	stale := *accounts.a
	accounts.a.Extra = map[string]any{"codex_turn_state_enabled": false}
	// Any accidental cache access panics: account-off must exit before it.
	r.cache = nil
	h := http.Header{}
	h.Set("X-Codex-Turn-State", "guarded-echo")
	require.NoError(t, r.Apply(context.Background(), &stale, key.Model, h))
	require.Equal(t, "guarded-echo", h.Get("X-Codex-Turn-State"))
}
