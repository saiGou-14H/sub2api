//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexTicketPlanStrictSelectionAndAccountSettings(t *testing.T) {
	cfg := DefaultCodexTicketSettings()
	cfg.TargetLength = 312
	for _, tc := range []struct {
		plan string
		want int
	}{{"inherit", 312}, {"pro", 292}, {"team", 332}} {
		t.Run(tc.plan, func(t *testing.T) {
			a := &Account{Extra: map[string]any{CodexTurnStatePlanExtraKey: tc.plan}}
			require.NoError(t, ValidateCodexTurnStateExtra(a.Extra))
			require.Equal(t, tc.plan, CodexTicketPlan(a))
			derived, err := CodexTicketAccountSettings(cfg, a)
			require.NoError(t, err)
			require.Equal(t, tc.want, derived.TargetLength)
			require.Equal(t, 312, cfg.TargetLength)
			require.Equal(t, cfg.TTLSeconds, derived.TTLSeconds)
			require.Equal(t, cfg.Revision, derived.Revision)
		})
	}
	for _, value := range []any{nil, "", "PRO", "Team", " pro", "team ", "plus", true, 292, []string{"pro"}, map[string]any{"plan": "team"}} {
		a := &Account{Extra: map[string]any{CodexTurnStatePlanExtraKey: value}}
		require.Error(t, ValidateCodexTurnStateExtra(a.Extra))
		require.Empty(t, CodexTicketPlan(a))
		_, err := CodexTicketAccountSettings(cfg, a)
		require.ErrorIs(t, err, ErrCodexTicketInvalidConfig)
	}
	for _, extra := range []map[string]any{nil, {}, {CodexTurnStateEnabledExtraKey: false}} {
		a := &Account{Name: "Team", Extra: extra, Credentials: map[string]any{"plan_type": "team", "chatgpt_plan_type": "pro"}}
		require.NoError(t, ValidateCodexTurnStateExtra(extra))
		require.Equal(t, "inherit", CodexTicketPlan(a))
		derived, err := CodexTicketAccountSettings(cfg, a)
		require.NoError(t, err)
		require.Equal(t, 312, derived.TargetLength)
	}
	require.Error(t, ValidateCodexTurnStateExtra(map[string]any{CodexTurnStateEnabledExtraKey: "true", CodexTurnStatePlanExtraKey: "pro"}))
	require.Empty(t, CodexTicketPlan(nil))
	_, err := CodexTicketAccountSettings(cfg, nil)
	require.Error(t, err)
}

func codexPolicyFixture() (*Account, time.Time) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	id := int64(7)
	expiry := now.Add(time.Hour)
	return &Account{ID: 1, Extra: map[string]any{CodexTurnStatePlanExtraKey: "pro"}, ProxyID: &id, Proxy: &Proxy{ID: id, Protocol: "http", Host: "proxy.example", Port: 8080, Username: "secret-user", Password: "secret-password", Status: StatusActive, ExpiresAt: &expiry}}, now
}

func TestCodexTicketPolicyScopeRouteValidation(t *testing.T) {
	a, now := codexPolicyFixture()
	proxied := CodexTicketPolicyScope(a, now)
	require.Len(t, proxied, 64)
	direct := &Account{Extra: a.Extra}
	require.NotEmpty(t, CodexTicketPolicyScope(direct, now))
	require.NotEqual(t, proxied, CodexTicketPolicyScope(direct, now))
	require.Empty(t, CodexTicketPolicyScope(nil, now))
	for _, mutate := range []func(*Account){
		func(a *Account) { a.Extra = map[string]any{CodexTurnStatePlanExtraKey: "invalid"} },
		func(a *Account) { a.ProxyID = nil },
		func(a *Account) { a.Proxy = nil },
		func(a *Account) { *a.ProxyID = 0 },
		func(a *Account) { a.Proxy.ID++ },
		func(a *Account) { a.Proxy.Status = "disabled" },
		func(a *Account) { a.Proxy.ExpiresAt = &now },
		func(a *Account) { a.Proxy.Protocol = "ftp" },
		func(a *Account) { a.Proxy.Host = "" },
		func(a *Account) { a.Proxy.Host = "proxy.example/path" },
		func(a *Account) { a.Proxy.Host = "proxy.example:8080" },
		func(a *Account) { a.Proxy.Host = "proxy.example\n" },
		func(a *Account) { a.Proxy.Host = "user@proxy.example" },
		func(a *Account) { a.Proxy.Port = 0 },
		func(a *Account) { a.Proxy.Port = 65536 },
	} {
		candidate, _ := codexPolicyFixture()
		mutate(candidate)
		require.Empty(t, CodexTicketPolicyScope(candidate, now))
	}
	for _, protocol := range []string{"http", "https", "socks5", "socks5h"} {
		candidate, _ := codexPolicyFixture()
		candidate.Proxy.Protocol = protocol
		require.NotEmpty(t, CodexTicketPolicyScope(candidate, now))
	}
	a.Proxy.Host = "2001:db8::1"
	require.NotEmpty(t, CodexTicketPolicyScope(a, now))
}

func TestCodexTicketPolicyScopeTracksPolicyNotIdentity(t *testing.T) {
	a, now := codexPolicyFixture()
	baseline := CodexTicketPolicyScope(a, now)
	for _, mutate := range []func(*Account){
		func(a *Account) { a.Extra = map[string]any{CodexTurnStatePlanExtraKey: "team"} },
		func(a *Account) { *a.ProxyID++; a.Proxy.ID++ },
		func(a *Account) { a.Proxy.Protocol = "https" },
		func(a *Account) { a.Proxy.Host = "another.example" },
		func(a *Account) { a.Proxy.Port++ },
		func(a *Account) { a.Proxy.Username += "changed" },
		func(a *Account) { a.Proxy.Password += "changed" },
		func(a *Account) { expiry := a.Proxy.ExpiresAt.Add(time.Second); a.Proxy.ExpiresAt = &expiry },
		func(a *Account) { a.Proxy.ExpiresAt = nil },
	} {
		candidate, _ := codexPolicyFixture()
		mutate(candidate)
		scope := CodexTicketPolicyScope(candidate, now)
		require.NotEmpty(t, scope)
		require.NotEqual(t, baseline, scope)
	}
	a.ID++
	a.Name = "other"
	a.Credentials = map[string]any{"chatgpt_account_id": "other-identity"}
	a.Proxy.Name = "renamed"
	require.Equal(t, baseline, CodexTicketPolicyScope(a, now), "identity and display metadata are not policy")
	local := a.Proxy.ExpiresAt.In(time.FixedZone("other", 3600))
	a.Proxy.ExpiresAt = &local
	require.Equal(t, baseline, CodexTicketPolicyScope(a, now), "same expiry instant must canonicalize")
	require.NotContains(t, baseline, "secret")
	require.NotContains(t, baseline, "proxy.example")
	require.True(t, strings.IndexFunc(baseline, func(r rune) bool { return !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) }) < 0)
}

type codexPolicyProxyRepository struct {
	ProxyRepository
	p     *Proxy
	err   error
	calls int
	id    int64
}

func (r *codexPolicyProxyRepository) GetByID(_ context.Context, id int64) (*Proxy, error) {
	r.calls++
	r.id = id
	return r.p, r.err
}

func TestResolveCodexTicketPolicyFreshReadCopyAndSafeErrors(t *testing.T) {
	a, now := codexPolicyFixture()
	fresh := *a.Proxy
	fresh.Password = "fresh-secret"
	repo := &codexPolicyProxyRepository{p: &fresh}
	runtime := &CodexTicketRuntime{proxies: repo}
	hydrated, scope, route, err := runtime.resolveCodexTicketPolicy(context.Background(), a, now)
	require.NoError(t, err)
	require.Equal(t, 1, repo.calls)
	require.Equal(t, *a.ProxyID, repo.id)
	require.NotSame(t, a, hydrated)
	require.NotSame(t, &fresh, hydrated.Proxy)
	require.NotSame(t, a.ProxyID, hydrated.ProxyID)
	require.Equal(t, "secret-password", a.Proxy.Password)
	require.Equal(t, fresh.URL(), route)
	require.NotEqual(t, CodexTicketPolicyScope(a, now), scope)
	require.Equal(t, CodexTicketPolicyScope(hydrated, now), scope)
	hydrated.Proxy.Password = "mutated"
	*hydrated.Proxy.ExpiresAt = now
	*hydrated.ProxyID = 99
	require.Equal(t, "fresh-secret", fresh.Password)
	require.True(t, fresh.ExpiresAt.After(now))
	require.EqualValues(t, 7, *a.ProxyID)
	repo.err = errors.New("password=fresh-secret token=private")
	hydrated, scope, route, err = runtime.resolveCodexTicketPolicy(context.Background(), a, now)
	require.ErrorIs(t, err, ErrCodexTicketPolicyUnavailable)
	require.Nil(t, hydrated)
	require.Empty(t, scope)
	require.Empty(t, route)
	require.NotContains(t, err.Error(), "fresh-secret")
	require.NotContains(t, err.Error(), "private")
	repo.err = nil
	repo.p = nil
	_, _, _, err = runtime.resolveCodexTicketPolicy(context.Background(), a, now)
	require.Error(t, err, "missing fresh proxy cannot use old preload")
}

func TestResolveCodexTicketPolicyDirectAndPreloadedFallback(t *testing.T) {
	a, now := codexPolicyFixture()
	hydrated, scope, route, err := resolveCodexTicketAccountPolicy(context.Background(), nil, a, now)
	require.NoError(t, err)
	require.NotEmpty(t, scope)
	require.Equal(t, a.Proxy.URL(), route)
	require.NotSame(t, a.Proxy, hydrated.Proxy)
	a.Proxy = nil
	_, _, _, err = resolveCodexTicketAccountPolicy(context.Background(), nil, a, now)
	require.Error(t, err)
	direct := &Account{}
	repo := &codexPolicyProxyRepository{err: errors.New("must not read")}
	hydrated, scope, route, err = resolveCodexTicketAccountPolicy(context.Background(), repo, direct, now)
	require.NoError(t, err)
	require.NotNil(t, hydrated)
	require.NotEmpty(t, scope)
	require.Empty(t, route)
	require.Zero(t, repo.calls)
	direct.Proxy = &Proxy{}
	_, _, _, err = resolveCodexTicketAccountPolicy(context.Background(), repo, direct, now)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err = resolveCodexTicketAccountPolicy(ctx, repo, &Account{}, now)
	require.Error(t, err)
	require.Zero(t, repo.calls)
}
