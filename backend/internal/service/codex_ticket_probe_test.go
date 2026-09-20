//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type codexProbeAccounts struct {
	AccountRepository
	account *Account
	calls   int
}

func (r *codexProbeAccounts) GetByID(context.Context, int64) (*Account, error) {
	r.calls++
	return r.account, nil
}

type codexProbeUpstream struct {
	HTTPUpstream
	call func(*http.Request, string, int64, int) (*http.Response, error)
}

func (u *codexProbeUpstream) Do(r *http.Request, p string, id int64, n int) (*http.Response, error) {
	return u.call(r, p, id, n)
}
func codexProbeAccount() *Account {
	return &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "private-token", "chatgpt_account_id": "identity"}, Extra: map[string]any{CodexTurnStateEnabledExtraKey: true}}
}

const codexProbeComplete = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"

func TestCodexProbeRequestIsolation(t *testing.T) {
	repo := &codexProbeAccounts{account: codexProbeAccount()}
	sessions := map[string]bool{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	svc.httpUpstream = &codexProbeUpstream{call: func(r *http.Request, p string, id int64, n int) (*http.Response, error) {
		require.Equal(t, chatgptCodexURL, r.URL.String())
		require.Equal(t, "http://127.0.0.1:8888", p)
		require.Equal(t, int64(7), id)
		require.Equal(t, 4, n)
		require.Equal(t, HTTPUpstreamProfileCodexHarvest, HTTPUpstreamProfileFromContext(r.Context()))
		require.True(t, HTTPUpstreamRedirectsDisabled(r.Context()))
		deadline, ok := r.Context().Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 30*time.Second)
		require.Equal(t, "Bearer private-token", r.Header.Get("Authorization"))
		require.NotEmpty(t, r.Header.Get("User-Agent"))
		require.NotEmpty(t, r.Header.Get("originator"))
		require.NotEmpty(t, r.Header.Get("version"))
		require.NotEmpty(t, r.Header.Get("X-Codex-Window-ID"))
		require.Empty(t, r.Header.Get("X-Codex-Turn-State"))
		session := r.Header.Get("session_id")
		require.NotEmpty(t, session)
		require.False(t, sessions[session])
		sessions[session] = true
		require.Equal(t, session, r.Header.Get("conversation_id"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "requested-model", body["model"])
		require.Equal(t, true, body["stream"])
		require.Equal(t, false, body["store"])
		require.NotContains(t, body, "previous_response_id")
		require.NotContains(t, body, "tools")
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Codex-Turn-State": []string{"private-state"}}, Body: io.NopCloser(strings.NewReader(codexProbeComplete))}, nil
	}}
	for i := 0; i < 2; i++ {
		result, err := svc.ProbeCodexTicket(context.Background(), 7, "requested-model", "http://127.0.0.1:8888")
		require.NoError(t, err)
		require.True(t, result.Completed)
		require.Equal(t, "private-state", result.State)
		require.Equal(t, CodexTicketIdentityScope(repo.account), result.IdentityScope)
	}
	require.Equal(t, 2, repo.calls)
}

type codexProbeTokenCache struct{ OpenAITokenCache }

func (*codexProbeTokenCache) GetAccessToken(context.Context, string) (string, error) {
	return "fresh-cache-token", nil
}

func TestCodexProbeUsesTokenProvider(t *testing.T) {
	repo := &codexProbeAccounts{account: codexProbeAccount()}
	svc := &OpenAIGatewayService{accountRepo: repo, openAITokenProvider: NewOpenAITokenProvider(repo, &codexProbeTokenCache{}, nil)}
	svc.httpUpstream = &codexProbeUpstream{call: func(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		require.Equal(t, "Bearer fresh-cache-token", r.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Codex-Turn-State": []string{"state"}}, Body: io.NopCloser(strings.NewReader(codexProbeComplete))}, nil
	}}
	_, err := svc.ProbeCodexTicket(context.Background(), 7, "model", "http://localhost:1")
	require.NoError(t, err)
}

func TestCodexProbeProxySchemesAndPluginRolloutMiss(t *testing.T) {
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			account := codexProbeAccount()
			for stablePluginBucket(account.ID) == 0 {
				account.ID++
			}
			manager := &PluginManager{}
			manager.route.Store(&pluginRoute{rolloutPercent: 1})
			require.False(t, manager.ShouldRouteOpenAIOAuth(account))
			called := false
			svc := &OpenAIGatewayService{accountRepo: &codexProbeAccounts{account: account}, pluginManager: manager}
			proxyURL := scheme + "://127.0.0.1:8080"
			svc.httpUpstream = &codexProbeUpstream{call: func(req *http.Request, p string, _ int64, _ int) (*http.Response, error) {
				called = true
				require.Equal(t, proxyURL, p)
				require.Equal(t, HTTPUpstreamProfileCodexHarvest, HTTPUpstreamProfileFromContext(req.Context()))
				return &http.Response{StatusCode: 200, Header: http.Header{"X-Codex-Turn-State": []string{"state"}}, Body: io.NopCloser(strings.NewReader(codexProbeComplete))}, nil
			}}
			result, err := svc.ProbeCodexTicket(context.Background(), account.ID, "model", proxyURL)
			require.NoError(t, err)
			require.True(t, called)
			require.True(t, result.Completed)
		})
	}
}

func TestCodexProbeRejectsBeforeTransport(t *testing.T) {
	for _, tc := range []struct {
		name, proxy, code string
		enabled           any
		plugin            bool
	}{
		{"empty proxy", "", "invalid_proxy", true, false}, {"unsupported scheme", "ftp://localhost:1", "invalid_proxy", true, false},
		{"bad port", "http://localhost:99999", "invalid_proxy", true, false}, {"missing host", "http://:80", "invalid_proxy", true, false},
		{"false", "http://localhost:1", "account_disabled", false, false}, {"string true", "http://localhost:1", "account_disabled", "true", false},
		{"plugin", "http://localhost:1", "transport_unsupported", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := codexProbeAccount()
			a.Extra[CodexTurnStateEnabledExtraKey] = tc.enabled
			svc := &OpenAIGatewayService{accountRepo: &codexProbeAccounts{account: a}, httpUpstream: &codexProbeUpstream{call: func(*http.Request, string, int64, int) (*http.Response, error) {
				t.Fatal("unexpected transport")
				return nil, nil
			}}}
			if tc.plugin {
				svc.pluginManager = &PluginManager{}
				svc.pluginManager.route.Store(&pluginRoute{rolloutPercent: 100})
			}
			got, err := svc.ProbeCodexTicket(context.Background(), 7, "model", tc.proxy)
			require.EqualError(t, err, tc.code)
			require.Equal(t, map[string]string{"invalid_proxy": "proxy_unavailable", "account_disabled": "identity_unresolved", "transport_unsupported": "transport_unsupported", "http_status": "", "transport_error": "proxy_unavailable", "completion_missing": "stream_failed", "state_missing": "stream_failed", "headers_too_large": "stream_failed"}[tc.code], got.ErrorCode)
			require.Empty(t, got.State)
		})
	}
}

func TestCodexProbeResponseFailures(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		body, state  string
		transportErr bool
		hugeHeaders  bool
		code         string
	}{
		{name: "rate limit", status: 429, body: "private-body", code: "http_status"},
		{name: "unavailable", status: 503, code: "http_status"},
		{name: "transport", transportErr: true, code: "transport_error"},
		{name: "missing completion", status: 200, body: "data: [DONE]\n\n", state: "private-state", code: "completion_missing"},
		{name: "missing state", status: 200, body: codexProbeComplete, code: "state_missing"},
		{name: "headers", status: 200, body: codexProbeComplete, hugeHeaders: true, code: "headers_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{accountRepo: &codexProbeAccounts{account: codexProbeAccount()}}
			svc.httpUpstream = &codexProbeUpstream{call: func(*http.Request, string, int64, int) (*http.Response, error) {
				if tc.transportErr {
					return nil, errors.New("private-token private-state private-body")
				}
				h := http.Header{"Retry-After": []string{"60"}}
				if tc.state != "" {
					h.Set("X-Codex-Turn-State", tc.state)
				}
				if tc.hugeHeaders {
					h.Set("Big", strings.Repeat("x", codexProbeHeaderLimit))
				}
				return &http.Response{StatusCode: tc.status, Header: h, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}}
			got, err := svc.ProbeCodexTicket(context.Background(), 7, "model", "http://localhost:1")
			require.EqualError(t, err, tc.code)
			require.Equal(t, map[string]string{"invalid_proxy": "proxy_unavailable", "account_disabled": "identity_unresolved", "transport_unsupported": "transport_unsupported", "http_status": "", "transport_error": "proxy_unavailable", "completion_missing": "stream_failed", "state_missing": "stream_failed", "headers_too_large": "stream_failed"}[tc.code], got.ErrorCode)
			require.Empty(t, got.State)
			require.False(t, got.Completed)
			require.Equal(t, tc.status, got.HTTPStatus)
			if tc.status == 429 || tc.status == 503 {
				require.Equal(t, "60", got.RetryAfter)
			}
		})
	}
}

func TestCodexProbeSSEBoundsAndCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"complete", codexProbeComplete, true}, {"CRLF", strings.ReplaceAll(codexProbeComplete, "\n", "\r\n"), true},
		{"empty", "", false}, {"done", "data: [DONE]\n\n", false}, {"truncated", strings.TrimSuffix(codexProbeComplete, "\n"), false},
		{"wrong status", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"incomplete\"}}\n\n", false},
		{"failed", "data: {\"type\":\"response.failed\"}\n\n" + codexProbeComplete, false},
		{"error event", "event: error\n\ndata: {}\n\n" + codexProbeComplete, false},
		{"error", "data: {\"type\":\"error\"}\n\n" + codexProbeComplete, false},
		{"incomplete", "data: {\"type\":\"response.incomplete\"}\n\n" + codexProbeComplete, false},
		{"malformed", "data: bad private-body\n\n" + codexProbeComplete, false},
		{"long line", "data: " + strings.Repeat("x", codexProbeFrameLimit) + "\n\n" + codexProbeComplete, false},
		{"frame bound", strings.Repeat(":comment\n", codexProbeFrameLimit/8) + "\n" + codexProbeComplete, false},
		{"body bound", strings.Repeat(":comment\n\n", codexProbeBodyLimit/10+1) + codexProbeComplete, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := readCodexProbeCompletion(strings.NewReader(tc.body))
			require.Equal(t, tc.ok, code == "", code)
		})
	}
}
