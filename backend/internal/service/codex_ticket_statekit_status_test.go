//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type statekitStatusAccounts struct {
	AccountRepository
	rows  map[int64]*Account
	calls int
}

func (a *statekitStatusAccounts) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	a.calls++
	out := make([]*Account, 0, len(ids))
	for _, id := range ids {
		if a.rows[id] != nil {
			out = append(out, a.rows[id])
		}
	}
	return out, nil
}

type statekitStatusSettings struct {
	CodexTicketSettingsRepository
	cfg CodexTicketSettings
}

func (s *statekitStatusSettings) Load(context.Context) (CodexTicketSettings, error) {
	return s.cfg, nil
}

type statekitStatusStore struct {
	CodexTicketRuntimeStore
	view  CodexTicketRuntimeView
	rows  map[CodexTicketKey]CodexTicketAccountSnapshot
	keys  []CodexTicketKey
	reads int
	delay time.Duration
}

func (s *statekitStatusStore) ReadForRequest(context.Context, int64, string, string, ...string) (CodexTicketRuntimeView, error) {
	return s.view, nil
}
func (s *statekitStatusStore) RecordCodexTicketDecision(context.Context, CodexTicketKey, string, string, string) error {
	return nil
}
func (s *statekitStatusStore) ReadCodexTicketAccounts(_ context.Context, keys []CodexTicketKey) (map[CodexTicketKey]CodexTicketAccountSnapshot, error) {
	s.reads++
	s.keys = append([]CodexTicketKey(nil), keys...)
	time.Sleep(s.delay)
	out := map[CodexTicketKey]CodexTicketAccountSnapshot{}
	for _, k := range keys {
		out[k] = s.rows[k]
	}
	return out, nil
}

type statekitStatusProxies struct {
	ProxyRepository
	proxy *Proxy
	calls int
	err   error
}

func (p *statekitStatusProxies) GetByID(context.Context, int64) (*Proxy, error) {
	p.calls++
	return p.proxy, p.err
}

func statekitStatusFixture(t *testing.T) (*CodexTicketRuntime, *statekitStatusAccounts, *statekitStatusStore, CodexTicketKey) {
	t.Helper()
	now := time.Now().UTC()
	cfg := DefaultCodexTicketSettings()
	cfg.CookiePinMode = CodexTicketCookiePinOptional
	cfg.TTLSeconds = 3600
	cfg.RefreshBeforeSeconds = 600
	cfg.Enabled = true
	cfg.Revision = 7
	cfg.Models = []string{"gpt-statekit"}
	a := &Account{ID: 1, Platform: "openai", Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Extra: map[string]any{CodexTurnStateEnabledExtraKey: true, CodexTurnStatePlanExtraKey: "pro"}, Credentials: map[string]any{"chatgpt_account_id": "private-identity", "access_token": "private-token"}}
	accounts := &statekitStatusAccounts{rows: map[int64]*Account{1: a}}
	k := CodexTicketKey{Revision: cfg.Revision, AccountID: a.ID, IdentityScope: CodexTicketIdentityScope(a), Model: cfg.Models[0], PolicyScope: CodexTicketPolicyScope(a, now)}
	ticket := &CodexTicket{Key: k, State: "gAAAAA" + strings.Repeat("a", 286), CapturedAt: now.Add(-time.Minute), ExpiresAt: now.Add(50 * time.Minute), Verified: true, VerifiedAt: now.Add(-30 * time.Second), ActualModel: k.Model, VerificationModel: k.Model, TargetLength: 292}
	store := &statekitStatusStore{view: CodexTicketRuntimeView{Control: CodexTicketControl{Settings: cfg, ProxyState: "active", ValidUntilMS: now.Add(6 * time.Second).UnixMilli()}, ServerTime: now}, rows: map[CodexTicketKey]CodexTicketAccountSnapshot{k: {Ticket: ticket}}}
	runtime := NewCodexTicketRuntime(&statekitStatusSettings{cfg: cfg}, nil, accounts, store)
	return runtime, accounts, store, k
}
func statekitInvalidation(now time.Time) CodexTicketObservation {
	why := "model_mismatch"
	return CodexTicketObservation{InvalidationCount: 1, LastInvalidatedAt: &now, LastInvalidationReason: &why}
}

func TestCodexTicketStatekitStatusPlansAndPolicyIsolation(t *testing.T) {
	r, a, s, k := statekitStatusFixture(t)
	ctx := context.Background()
	row := s.rows[k]
	row.Observation = statekitInvalidation(s.view.ServerTime)
	row.Observation.InjectionCount = 9
	s.rows[k] = row
	out, err := r.AccountStatuses(ctx, []int64{1})
	require.NoError(t, err)
	require.Equal(t, "pro", out.Accounts[0].Plan)
	require.Equal(t, 292, out.Accounts[0].TargetLength)
	require.True(t, out.Accounts[0].Models[0].Verified)
	require.EqualValues(t, 9, out.Accounts[0].Models[0].InjectionCount)
	a.rows[1].Extra[CodexTurnStatePlanExtraKey] = "team"
	out, err = r.AccountStatuses(ctx, []int64{1})
	require.NoError(t, err)
	require.Equal(t, "team", out.Accounts[0].Plan)
	require.Equal(t, 332, out.Accounts[0].TargetLength)
	require.Equal(t, "waiting", out.Accounts[0].Status)
	require.Zero(t, out.Accounts[0].Models[0].InjectionCount)
	require.Zero(t, out.Accounts[0].Models[0].InvalidationCount)
	require.NotEqual(t, k.PolicyScope, s.keys[0].PolicyScope)
	teamKey := s.keys[0]
	team := *row.Ticket
	team.Key = teamKey
	team.State = "gAAAAA" + strings.Repeat("b", 326)
	team.TargetLength = 332
	s.rows[teamKey] = CodexTicketAccountSnapshot{Ticket: &team}
	out, err = r.AccountStatuses(ctx, []int64{1})
	require.NoError(t, err)
	require.Equal(t, "ready", out.Accounts[0].Status)
	require.True(t, out.Accounts[0].Models[0].Verified)
	a.rows[1].Extra[CodexTurnStatePlanExtraKey] = "inherit"
	r.settings.(*statekitStatusSettings).cfg.TargetLength = 332
	out, err = r.AccountStatuses(ctx, []int64{1})
	require.NoError(t, err)
	require.Equal(t, "inherit", out.Accounts[0].Plan)
	require.Equal(t, 332, out.Accounts[0].TargetLength)
	require.Equal(t, "waiting", out.Accounts[0].Status)
}

func TestCodexTicketStatekitStatusFreshRouteAndNoOldHistory(t *testing.T) {
	r, a, s, k := statekitStatusFixture(t)
	ctx := context.Background()
	id := int64(12)
	a.rows[1].ProxyID = &id
	expiry := s.view.ServerTime.Add(time.Hour)
	proxies := &statekitStatusProxies{proxy: &Proxy{ID: id, Protocol: "http", Host: "private-proxy.example", Port: 8080, Username: "secret-user", Password: "secret-password", Status: StatusActive, ExpiresAt: &expiry}}
	r.proxies = proxies
	a.rows[1].Proxy = proxies.proxy
	// GetByIDs supplies a fresh proxy preload; no per-account proxy query runs.
	out, err := r.AccountStatuses(ctx, []int64{1})
	require.NoError(t, err)
	require.Zero(t, proxies.calls)
	require.Equal(t, "waiting", out.Accounts[0].Status)
	require.NotEqual(t, k.PolicyScope, s.keys[0].PolicyScope)
	routed := s.keys[0]
	ticket := *s.rows[k].Ticket
	ticket.Key = routed
	s.rows[routed] = CodexTicketAccountSnapshot{Ticket: &ticket, Observation: CodexTicketObservation{InjectionCount: 3}}
	out, err = r.AccountStatuses(ctx, []int64{1})
	require.NoError(t, err)
	require.Equal(t, "ready", out.Accounts[0].Status)
	proxies.proxy.Password = "changed-secret"
	out, err = r.AccountStatuses(ctx, []int64{1})
	require.NoError(t, err)
	require.Equal(t, "waiting", out.Accounts[0].Status)
	require.Zero(t, out.Accounts[0].Models[0].InjectionCount)
	reads := s.reads
	proxies.err = errors.New("password=changed-secret")
	a.rows[1].Proxy = nil // A missing preloaded relation must not query or fall back.
	out, err = r.AccountStatuses(ctx, []int64{1})
	require.NoError(t, err)
	require.Equal(t, "proxy_unavailable", out.Accounts[0].Status)
	require.Equal(t, reads, s.reads)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	for _, secret := range []string{"private-proxy", "changed-secret", "secret-user", routed.PolicyScope, k.IdentityScope, "private-token"} {
		require.NotContains(t, string(raw), secret)
	}
}

func TestCodexTicketStatekitStatusBatchProxyPreloadAndExpiry(t *testing.T) {
	r, a, s, _ := statekitStatusFixture(t)
	cfg := r.settings.(*statekitStatusSettings).cfg
	cfg.Models = append(cfg.Models, "another-model")
	r.settings.(*statekitStatusSettings).cfg = cfg
	s.view.Control.Settings = cfg
	b := *a.rows[1]
	b.ID = 2
	a.rows[2] = &b
	id := int64(12)
	a.rows[1].ProxyID = &id
	b.ProxyID = &id
	expiry := s.view.ServerTime.Add(time.Hour)
	p := &statekitStatusProxies{proxy: &Proxy{ID: id, Protocol: "http", Host: "proxy.example", Port: 8080, Status: StatusActive, ExpiresAt: &expiry}}
	r.proxies = p
	a.rows[1].Proxy = p.proxy
	b.Proxy = p.proxy
	out, err := r.AccountStatuses(context.Background(), []int64{1, 2})
	require.NoError(t, err)
	require.Len(t, out.Accounts, 2)
	require.Equal(t, 1, a.calls)
	require.Zero(t, p.calls)
	require.Len(t, s.keys, 4)
	require.Equal(t, 1, s.reads)
	expiry = s.view.ServerTime.Add(10 * time.Millisecond)
	s.delay = 30 * time.Millisecond
	out, err = r.AccountStatuses(context.Background(), []int64{1})
	require.NoError(t, err)
	require.Equal(t, "proxy_unavailable", out.Accounts[0].Status)
	require.False(t, out.Accounts[0].Models[0].Verified)
	require.Nil(t, out.Accounts[0].Models[0].LastOutcome)
}

func TestCodexTicketStatekitStatusMaximumBatchDoesNotQueryProxyPerAccount(t *testing.T) {
	r, a, s, _ := statekitStatusFixture(t)
	cfg := r.settings.(*statekitStatusSettings).cfg
	cfg.Models = make([]string, 16)
	for i := range cfg.Models {
		cfg.Models[i] = "model-" + strconv.Itoa(i)
	}
	r.settings.(*statekitStatusSettings).cfg = cfg
	s.view.Control.Settings = cfg
	proxyID := int64(12)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.example", Port: 8080, Status: StatusActive}
	proxies := &statekitStatusProxies{proxy: proxy, err: errors.New("must not query")}
	r.proxies = proxies
	ids := make([]int64, 100)
	base := *a.rows[1]
	for i := range ids {
		id := int64(i + 1)
		ids[i] = id
		account := base
		account.ID = id
		account.ProxyID = &proxyID
		account.Proxy = proxy
		a.rows[id] = &account
	}
	out, err := r.AccountStatuses(context.Background(), ids)
	require.NoError(t, err)
	require.Len(t, out.Accounts, 100)
	require.Equal(t, 1, a.calls)
	require.Zero(t, proxies.calls)
	require.Equal(t, 1, s.reads)
	require.Len(t, s.keys, 1600)
}

func TestCodexTicketStatekitStatusInvalidatedAndNewVerifiedTicket(t *testing.T) {
	r, _, s, k := statekitStatusFixture(t)
	ctx := context.Background()
	row := s.rows[k]
	saved := row.Ticket
	row.Ticket = nil
	row.Observation = statekitInvalidation(s.view.ServerTime)
	s.rows[k] = row
	out, err := r.AccountStatuses(ctx, []int64{1})
	require.NoError(t, err)
	require.Equal(t, "invalidated", out.Accounts[0].Status)
	require.False(t, out.Accounts[0].Models[0].Verified)
	row.Retry = CodexTicketRetry{NextAttemptAt: s.view.ServerTime.Add(time.Minute), ErrorCode: "verification_failed"}
	s.rows[k] = row
	out, err = r.AccountStatuses(ctx, []int64{1})
	require.NoError(t, err)
	require.Equal(t, "backoff", out.Accounts[0].Status)
	require.Equal(t, "verification_failed", *out.Accounts[0].Models[0].LastErrorCode)
	row.Ticket = saved
	s.rows[k] = row
	out, err = r.AccountStatuses(ctx, []int64{1})
	require.NoError(t, err)
	require.Equal(t, "ready", out.Accounts[0].Status)
	require.True(t, out.Accounts[0].Models[0].Verified)
	require.EqualValues(t, 1, out.Accounts[0].Models[0].InvalidationCount)
	s.view.Control.ProxyState = "expired"
	out, err = r.AccountStatuses(ctx, []int64{1})
	require.NoError(t, err)
	require.Equal(t, "proxy_unavailable", out.Accounts[0].Status)
	require.False(t, out.Accounts[0].Models[0].Verified)
}

func TestCodexTicketStatekitStatusNewTicketExpiryKeepsInvalidationHistory(t *testing.T) {
	for _, rawPresent := range []bool{true, false} {
		t.Run(strconv.FormatBool(rawPresent), func(t *testing.T) {
			r, _, s, k := statekitStatusFixture(t)
			now := s.view.ServerTime
			row := s.rows[k]
			row.Observation = statekitInvalidation(now.Add(-2 * time.Minute))
			row.Ticket.CapturedAt = now.Add(-time.Minute)
			row.Ticket.VerifiedAt = now.Add(-30 * time.Second)
			row.Ticket.ExpiresAt = now.Add(-time.Second)
			if !rawPresent {
				row.Metadata = &CodexTicketMetadata{CapturedAt: row.Ticket.CapturedAt, ExpiresAt: row.Ticket.ExpiresAt, VerifiedAt: row.Ticket.VerifiedAt, ActualModel: k.Model, VerificationModel: k.Model}
				row.Ticket = nil
			}
			s.rows[k] = row
			out, err := r.AccountStatuses(context.Background(), []int64{1})
			require.NoError(t, err)
			m := out.Accounts[0].Models[0]
			require.Equal(t, "expired", m.Status)
			require.False(t, m.Verified)
			require.EqualValues(t, 1, m.InvalidationCount)
			require.Equal(t, "model_mismatch", *m.LastInvalidationReason)
			require.True(t, m.LastInvalidatedAt.Before(*m.CapturedAt))
			row.Retry.NextAttemptAt = now.Add(time.Minute)
			s.rows[k] = row
			out, err = r.AccountStatuses(context.Background(), []int64{1})
			require.NoError(t, err)
			require.Equal(t, "backoff", out.Accounts[0].Status)
		})
	}
}

func TestCodexTicketStatekitStatusMetadataCannotVerifyAndRedactsModels(t *testing.T) {
	for _, kind := range []string{"expired_metadata", "fresh_metadata", "wrong_model", "future_verification", "unverified_raw", "empty"} {
		t.Run(kind, func(t *testing.T) {
			r, _, s, k := statekitStatusFixture(t)
			now := s.view.ServerTime
			row := s.rows[k]
			switch kind {
			case "empty":
				row = CodexTicketAccountSnapshot{}
			case "unverified_raw":
				row.Ticket.Verified = false
			default:
				row.Ticket = nil
				row.Metadata = &CodexTicketMetadata{CapturedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute), VerifiedAt: now.Add(-30 * time.Second), ActualModel: k.Model, VerificationModel: k.Model}
				switch kind {
				case "expired_metadata":
					row.Metadata.ExpiresAt = now.Add(-time.Second)
				case "wrong_model":
					row.Metadata.ActualModel = "secret-upstream-free-text"
				case "future_verification":
					row.Metadata.VerifiedAt = now.Add(10 * time.Second)
				}
			}
			s.rows[k] = row
			out, err := r.AccountStatuses(context.Background(), []int64{1})
			require.NoError(t, err)
			m := out.Accounts[0].Models[0]
			require.False(t, m.Verified)
			expected := map[string]string{"expired_metadata": "expired", "fresh_metadata": "waiting", "wrong_model": "unavailable", "future_verification": "unavailable", "unverified_raw": "unavailable", "empty": "waiting"}[kind]
			require.Equal(t, expected, m.Status)
			raw, err := json.Marshal(out)
			require.NoError(t, err)
			for _, secret := range []string{"secret-upstream-free-text", "gAAAAA", k.PolicyScope, k.IdentityScope, "private-identity", "private-token"} {
				require.NotContains(t, string(raw), secret)
			}
			if kind == "empty" {
				for _, field := range []string{"verified_at", "actual_model", "verification_model", "last_invalidated_at", "last_invalidation_reason"} {
					require.Contains(t, string(raw), `"`+field+`":null`)
				}
			}
			if kind == "expired_metadata" {
				require.NotNil(t, m.VerifiedAt)
				require.Equal(t, k.Model, *m.ActualModel)
				require.Equal(t, k.Model, *m.VerificationModel)
			}
		})
	}
}

func TestCodexTicketStatekitStatusInvalidPlanSkipsCacheAndSafeErrors(t *testing.T) {
	r, a, s, _ := statekitStatusFixture(t)
	a.rows[1].Extra[CodexTurnStatePlanExtraKey] = "secret-invalid-plan"
	out, err := r.AccountStatuses(context.Background(), []int64{1})
	require.NoError(t, err)
	require.Equal(t, "unavailable", out.Accounts[0].Status)
	require.Zero(t, s.reads)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret-invalid-plan")
	for _, code := range []string{"model_mismatch", "verification_failed", "state_312"} {
		require.Equal(t, code, codexTicketPublicError(code))
	}
	require.Equal(t, "probe_failed", codexTicketPublicError("secret-raw-error"))
	require.Equal(t, "invalidated", codexTicketAggregateStatus([]CodexTicketModelStatus{{Status: "expired"}, {Status: "invalidated"}}))
	require.Equal(t, "backoff", codexTicketAggregateStatus([]CodexTicketModelStatus{{Status: "invalidated"}, {Status: "backoff"}}))
	require.Equal(t, "proxy_unavailable", codexTicketAggregateStatus([]CodexTicketModelStatus{{Status: "backoff"}, {Status: "proxy_unavailable"}}))
}
