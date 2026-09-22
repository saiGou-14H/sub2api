//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func codexProbeTestContext(account *Account, model string) context.Context {
	cfg := DefaultCodexTicketSettings()
	cfg.Models = []string{model}
	cfg.Revision = 1
	if derived, err := CodexTicketAccountSettings(cfg, account); err == nil {
		cfg = derived
	}
	task := codexTicketProbeTask{
		Key:               CodexTicketKey{Revision: cfg.Revision, AccountID: account.ID, IdentityScope: CodexTicketIdentityScope(account), Model: model, PolicyScope: CodexTicketPolicyScope(account, time.Now())},
		Settings:          cfg,
		VerificationGuard: func(context.Context, bool) (*Account, string, error) { fresh := *account; return &fresh, "", nil },
	}
	return context.WithValue(context.Background(), codexTicketProbeTaskKey{}, task)
}

func codexProbeCompleteModel(model string) string {
	return fmt.Sprintf("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":%q}}\n\n", model)
}
func codexProbeValidCandidate() string {
	return "gAAAAA" + strings.Repeat("a", DefaultCodexTicketSettings().TargetLength-6)
}
func codexProbeSuccessResponse(model, state string) *http.Response {
	h := http.Header{}
	if state != "" {
		h.Set("X-Codex-Turn-State", state)
	}
	h.Add("Set-Cookie", "cflb=fixture-cflb; Domain=.chatgpt.com; Path=/; Max-Age=240; HttpOnly; Secure")
	h.Add("Set-Cookie", "oailb=fixture-oailb; Domain=.chatgpt.com; Path=/; Max-Age=240; HttpOnly; Secure")
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(codexProbeCompleteModel(model)))}
}

const codexProbeComplete = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"

func TestCodexProbeRequestIsolation(t *testing.T) {
	repo := &codexProbeAccounts{account: codexProbeAccount()}
	sessions := map[string]bool{}
	calls := 0
	var captureDeadline time.Time
	svc := &OpenAIGatewayService{accountRepo: repo}
	svc.httpUpstream = &codexProbeUpstream{call: func(r *http.Request, p string, id int64, n int) (*http.Response, error) {
		calls++
		capture := calls%2 == 1
		require.Equal(t, chatgptCodexURL, r.URL.String())
		if capture {
			require.Equal(t, "http://127.0.0.1:8888", p)
		} else {
			require.Empty(t, p, "verification uses the account's direct business route")
		}
		require.Equal(t, int64(7), id)
		require.Equal(t, 4, n)
		require.Equal(t, HTTPUpstreamProfileCodexHarvest, HTTPUpstreamProfileFromContext(r.Context()))
		require.True(t, HTTPUpstreamRedirectsDisabled(r.Context()))
		deadline, ok := r.Context().Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 30*time.Second)
		if capture {
			captureDeadline = deadline
		} else {
			require.True(t, captureDeadline.Equal(deadline), "both stages share the original 30s deadline")
		}
		require.Equal(t, "Bearer private-token", r.Header.Get("Authorization"))
		require.NotEmpty(t, r.Header.Get("User-Agent"))
		require.NotEmpty(t, r.Header.Get("originator"))
		require.NotEmpty(t, r.Header.Get("version"))
		require.NotEmpty(t, r.Header.Get("X-Codex-Window-ID"))
		if capture {
			require.Empty(t, r.Header.Get("X-Codex-Turn-State"))
			require.Empty(t, r.Header.Get("Cookie"))
		} else {
			require.Equal(t, codexProbeValidCandidate(), r.Header.Get("X-Codex-Turn-State"))
			require.Equal(t, "cflb=fixture-cflb; oailb=fixture-oailb", r.Header.Get("Cookie"))
		}
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
		if capture {
			return codexProbeSuccessResponse("requested-model", codexProbeValidCandidate()), nil
		}
		return codexProbeSuccessResponse("requested-model", ""), nil
	}}
	for i := 0; i < 2; i++ {
		ctx := codexProbeTestContext(repo.account, "requested-model")
		task := ctx.Value(codexTicketProbeTaskKey{}).(codexTicketProbeTask)
		guardCalls := []bool{}
		task.VerificationGuard = func(_ context.Context, consume bool) (*Account, string, error) {
			guardCalls = append(guardCalls, consume)
			fresh := *repo.account
			return &fresh, "", nil
		}
		ctx = context.WithValue(ctx, codexTicketProbeTaskKey{}, task)
		result, err := svc.ProbeCodexTicket(ctx, 7, "requested-model", "http://127.0.0.1:8888")
		require.NoError(t, err)
		require.True(t, result.Completed)
		require.True(t, result.Verified)
		require.False(t, result.VerifiedAt.IsZero())
		require.Equal(t, "requested-model", result.ActualModel)
		require.Equal(t, "requested-model", result.VerificationModel)
		require.Equal(t, []bool{true, false}, guardCalls)
		require.Equal(t, codexProbeValidCandidate(), result.State)
		require.Equal(t, CodexTicketIdentityScope(repo.account), result.IdentityScope)
		require.Equal(t, CodexTicketPolicyScope(repo.account, time.Now()), result.PolicyScope)
	}
	require.Equal(t, 2, repo.calls)
	require.Equal(t, 4, calls)
	require.Len(t, sessions, 4)
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
		return codexProbeSuccessResponse("model", codexProbeValidCandidate()), nil
	}}
	_, err := svc.ProbeCodexTicket(codexProbeTestContext(repo.account, "model"), 7, "model", "http://localhost:1")
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
			calls := 0
			svc := &OpenAIGatewayService{accountRepo: &codexProbeAccounts{account: account}, pluginManager: manager}
			proxyURL := scheme + "://127.0.0.1:8080"
			svc.httpUpstream = &codexProbeUpstream{call: func(req *http.Request, p string, _ int64, _ int) (*http.Response, error) {
				calls++
				if calls == 1 {
					require.Equal(t, proxyURL, p)
					require.Empty(t, req.Header.Get("X-Codex-Turn-State"))
				} else {
					require.Empty(t, p)
					require.Equal(t, codexProbeValidCandidate(), req.Header.Get("X-Codex-Turn-State"))
				}
				require.Equal(t, HTTPUpstreamProfileCodexHarvest, HTTPUpstreamProfileFromContext(req.Context()))
				return codexProbeSuccessResponse("model", codexProbeValidCandidate()), nil
			}}
			result, err := svc.ProbeCodexTicket(codexProbeTestContext(account, "model"), account.ID, "model", proxyURL)
			require.NoError(t, err)
			require.Equal(t, 2, calls)
			require.True(t, result.Verified)
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
		{"empty proxy", "", "proxy_unavailable", true, false}, {"unsupported scheme", "ftp://localhost:1", "proxy_unavailable", true, false},
		{"bad port", "http://localhost:99999", "proxy_unavailable", true, false}, {"missing host", "http://:80", "proxy_unavailable", true, false},
		{"false", "http://localhost:1", "identity_unresolved", false, false}, {"string true", "http://localhost:1", "identity_unresolved", "true", false},
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
			got, err := svc.ProbeCodexTicket(codexProbeTestContext(a, "model"), 7, "model", tc.proxy)
			require.EqualError(t, err, tc.code)
			require.Equal(t, tc.code, got.ErrorCode)
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
		{name: "missing state", status: 200, body: codexProbeCompleteModel("model"), code: "state_missing"},
		{name: "headers", status: 200, body: codexProbeCompleteModel("model"), hugeHeaders: true, code: "headers_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := codexProbeAccount()
			svc := &OpenAIGatewayService{accountRepo: &codexProbeAccounts{account: a}}
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
			got, err := svc.ProbeCodexTicket(codexProbeTestContext(a, "model"), 7, "model", "http://localhost:1")
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

func TestCodexProbeTwoStageModelAndStateFailures(t *testing.T) {
	for _, tc := range []struct {
		name              string
		stage             int
		body, state, code string
	}{
		{"capture model missing", 1, codexProbeComplete, codexProbeValidCandidate(), "model_mismatch"},
		{"capture model wrong", 1, codexProbeCompleteModel("other-model"), codexProbeValidCandidate(), "model_mismatch"},
		{"capture malformed state", 1, codexProbeCompleteModel("model"), "private-state", "verification_failed"},
		{"capture length312", 1, codexProbeCompleteModel("model"), "gAAAAA" + strings.Repeat("a", 306), "state_312"},
		{"verify model missing", 2, codexProbeComplete, "", "model_mismatch"},
		{"verify model wrong", 2, codexProbeCompleteModel("other-model"), "", "model_mismatch"},
		{"verify state312", 2, codexProbeCompleteModel("model"), "gAAAAA" + strings.Repeat("a", 306), "state_312"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := codexProbeAccount()
			calls := 0
			svc := &OpenAIGatewayService{accountRepo: &codexProbeAccounts{account: a}}
			svc.httpUpstream = &codexProbeUpstream{call: func(req *http.Request, route string, _ int64, _ int) (*http.Response, error) {
				calls++
				if calls == 1 {
					require.Equal(t, "http://localhost:1", route)
					require.Empty(t, req.Header.Get("X-Codex-Turn-State"))
				} else {
					require.Empty(t, route)
					require.Equal(t, codexProbeValidCandidate(), req.Header.Get("X-Codex-Turn-State"))
				}
				if calls == tc.stage {
					h := http.Header{}
					if tc.state != "" {
						h.Set("X-Codex-Turn-State", tc.state)
					}
					return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
				}
				return codexProbeSuccessResponse("model", codexProbeValidCandidate()), nil
			}}
			result, err := svc.ProbeCodexTicket(codexProbeTestContext(a, "model"), a.ID, "model", "http://localhost:1")
			require.EqualError(t, err, tc.code)
			require.Equal(t, tc.code, result.ErrorCode)
			require.False(t, result.Verified)
			require.False(t, result.Completed)
			require.Empty(t, result.State)
			require.Equal(t, tc.stage, calls)
		})
	}
}

func TestCodexProbeVerificationGuardRejectsWithoutReplay(t *testing.T) {
	for _, denyAt := range []int{1, 2} {
		t.Run(fmt.Sprint(denyAt), func(t *testing.T) {
			a := codexProbeAccount()
			ctx := codexProbeTestContext(a, "model")
			task := ctx.Value(codexTicketProbeTaskKey{}).(codexTicketProbeTask)
			guards := []bool{}
			calls := 0
			task.VerificationGuard = func(_ context.Context, consume bool) (*Account, string, error) {
				guards = append(guards, consume)
				if len(guards) == denyAt {
					return nil, "", errors.New("budget or fresh fence denied")
				}
				return a, "", nil
			}
			ctx = context.WithValue(ctx, codexTicketProbeTaskKey{}, task)
			svc := &OpenAIGatewayService{accountRepo: &codexProbeAccounts{account: a}, httpUpstream: &codexProbeUpstream{call: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				calls++
				if calls == 1 {
					require.Empty(t, req.Header.Get("X-Codex-Turn-State"))
					return codexProbeSuccessResponse("model", codexProbeValidCandidate()), nil
				}
				require.Equal(t, codexProbeValidCandidate(), req.Header.Get("X-Codex-Turn-State"))
				return codexProbeSuccessResponse("model", ""), nil
			}}}
			result, err := svc.ProbeCodexTicket(ctx, a.ID, "model", "http://localhost:1")
			require.EqualError(t, err, "verification_failed")
			require.Empty(t, result.State)
			require.False(t, result.Verified)
			require.False(t, result.Completed)
			require.Equal(t, denyAt, calls)
			require.Len(t, guards, denyAt)
			require.True(t, guards[0])
			if denyAt == 2 {
				require.False(t, guards[1])
			}
		})
	}
}

func TestCodexProbeRequiresControllerTaskBeforeTransport(t *testing.T) {
	a := codexProbeAccount()
	repo := &codexProbeAccounts{account: a}
	svc := &OpenAIGatewayService{accountRepo: repo, httpUpstream: &codexProbeUpstream{call: func(*http.Request, string, int64, int) (*http.Response, error) {
		t.Fatal("unbudgeted transport")
		return nil, nil
	}}}
	// This deliberately omits the helper: unbudgeted direct calls must fail.
	result, err := svc.ProbeCodexTicket(context.Background(), a.ID, "model", "http://localhost:1")
	require.EqualError(t, err, "verification_failed")
	require.Empty(t, result.State)
	require.Zero(t, repo.calls)
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
