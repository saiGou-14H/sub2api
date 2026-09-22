package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type codexWSSettings struct {
	CodexTicketSettingsRepository
	mu      sync.Mutex
	enabled bool
	err     error
}

func (s *codexWSSettings) Load(context.Context) (CodexTicketSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := DefaultCodexTicketSettings()
	c.CookiePinMode = CodexTicketCookiePinOptional
	c.TTLSeconds = 3600
	c.RefreshBeforeSeconds = 600
	c.Enabled = s.enabled
	return c, s.err
}
func (s *codexWSSettings) enable() { s.mu.Lock(); defer s.mu.Unlock(); s.enabled = true }

type codexWSAccounts struct {
	AccountRepository
	mu  sync.Mutex
	a   *Account
	err error
}

func (s *codexWSAccounts) GetByID(context.Context, int64) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.a, s.err
}
func codexWSAccount(mode string, optin bool) *Account {
	return &Account{ID: 901, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "ws-identity"}, Extra: map[string]any{CodexTurnStateEnabledExtraKey: optin, "openai_oauth_responses_websockets_v2_mode": mode}}
}
func TestCodexTicketWSBridgeDecision(t *testing.T) {
	for _, tc := range []struct {
		name          string
		global, optin bool
		want          bool
	}{
		{"default-disabled", false, false, false}, {"global-disabled", false, true, false}, {"account-default-off", true, false, false}, {"both-enabled", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := codexWSAccount(OpenAIWSIngressModeCtxPool, tc.optin)
			r := NewCodexTicketRuntime(&codexWSSettings{enabled: tc.global}, nil, &codexWSAccounts{a: a}, nil)
			require.Equal(t, tc.want, r.ShouldBridgeWebSocket(context.Background(), a))
		})
	}
	a := codexWSAccount(OpenAIWSIngressModeCtxPool, true)
	r := NewCodexTicketRuntime(&codexWSSettings{err: errors.New("storage unavailable")}, nil, &codexWSAccounts{a: a}, nil)
	require.False(t, r.ShouldBridgeWebSocket(context.Background(), a), "unknown global policy must not affect native WS")
	known := DefaultCodexTicketSettings()
	r.rememberSettings(known)
	require.False(t, r.ShouldBridgeWebSocket(context.Background(), a), "known disabled policy must preserve native WS on storage failure")
	known.Enabled = true
	known.Revision++
	r.rememberSettings(known)
	require.True(t, r.ShouldBridgeWebSocket(context.Background(), a), "known enabled opt-in uses HTTP Apply during storage failure")
	require.False(t, r.ShouldBridgeWebSocket(context.Background(), codexWSAccount(OpenAIWSIngressModeCtxPool, false)))
	require.False(t, r.ShouldBridgeWebSocket(context.Background(), &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}))
	r.settings = &codexWSSettings{enabled: true}
	r.accounts = &codexWSAccounts{err: errors.New("account unavailable")}
	require.True(t, r.ShouldBridgeWebSocket(context.Background(), a))
	require.False(t, r.ShouldBridgeWebSocket(context.Background(), codexWSAccount(OpenAIWSIngressModeCtxPool, false)), "fresh account read failure must not opt in a default-off snapshot")
}

type codexWSNativeConn struct{ *stagedPassthroughConn }

func (c *codexWSNativeConn) WriteJSON(ctx context.Context, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteFrame(ctx, coderws.MessageText, raw)
}

func TestCodexTicketWSNativeLaterEnableRequiresReconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{OpenAIWSIngressModePassthrough, OpenAIWSIngressModeCtxPool} {
		for _, toggle := range []string{"global", "account"} {
			t.Run(mode+"/"+toggle, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				upstream := newStagedPassthroughConn()
				cfg := passthroughLifecycleConfig()
				cfg.Gateway.OpenAIWS.OAuthEnabled = true
				cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 5
				account := codexWSAccount(mode, toggle == "global")
				settings := &codexWSSettings{enabled: toggle == "account"}
				accounts := &codexWSAccounts{a: account}
				svc := newPassthroughLifecycleService(cfg, upstream)
				svc.codexTicketRuntime = NewCodexTicketRuntime(settings, nil, accounts, nil)
				if mode == OpenAIWSIngressModeCtxPool {
					pool := newOpenAIWSConnPool(cfg)
					pool.setClientDialerForTest(&stagedPassthroughDialer{conn: &codexWSNativeConn{upstream}})
					svc.openaiWSPool = pool
					defer pool.Close()
				}
				server, serverErr := startPassthroughHookRecordingServer(t, ctx, svc, account, nil)
				defer server.Close()
				client := dialPassthroughLifecycleClient(t, server)
				defer client.CloseNow()
				requirePassthroughUpstreamWrite(t, upstream, time.Second)
				upstream.Send(`{"type":"response.completed","response":{"id":"resp_first","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)
				_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
				require.NoError(t, err)
				if toggle == "global" {
					settings.enable()
				} else {
					accounts.mu.Lock()
					accounts.a = codexWSAccount(mode, true)
					accounts.mu.Unlock()
				}
				require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6-astra"}`)))
				// Read the close frame so coder/websocket can finish its close handshake.
				_, closeErr := readPassthroughLifecycleFrame(t, client, 3*time.Second)
				require.Error(t, closeErr)
				if mode == OpenAIWSIngressModePassthrough {
					require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(closeErr))
					require.ErrorContains(t, closeErr, "CODEX_TICKET_RECONNECT_REQUIRED")
				}
				select {
				case err := <-serverErr:
					require.ErrorContains(t, err, "CODEX_TICKET_RECONNECT_REQUIRED")
				case <-time.After(4 * time.Second):
					t.Fatal("native connection did not reject newly enabled feature")
				}
				select {
				case payload := <-upstream.writes:
					t.Fatalf("later native frame escaped guard: %s", payload)
				default:
				}
			})
		}
	}
}

type codexWSHTTPUpstream struct {
	HTTPUpstream
	mu      sync.Mutex
	headers []http.Header
}

func (u *codexWSHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.headers = append(u.headers, req.Header.Clone())
	n := len(u.headers)
	u.mu.Unlock()
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_bridge_%d\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n", n)))}, nil
}

type codexWSCache struct {
	CodexTicketRuntimeStore
	settings *codexWSSettings
	missing  bool
	reject   bool
}

func (c codexWSCache) ReadForRequest(ctx context.Context, id int64, scope string, model string, policy ...string) (CodexTicketRuntimeView, error) {
	cfg, err := c.settings.Load(ctx)
	if err != nil {
		return CodexTicketRuntimeView{}, err
	}
	now := time.Now()
	policyScope := ""
	if len(policy) > 0 {
		policyScope = policy[0]
	}
	ticket := &CodexTicket{Key: CodexTicketKey{Revision: cfg.Revision, AccountID: id, IdentityScope: scope, Model: model, PolicyScope: policyScope}, State: "gAAAAA" + strings.Repeat("a", cfg.TargetLength-6), CapturedAt: now, ExpiresAt: now.Add(time.Hour), Verified: true, VerifiedAt: now, ActualModel: model, VerificationModel: model, TargetLength: cfg.TargetLength}
	if c.missing {
		ticket = nil
	}
	if c.reject {
		cfg.MissingPolicy = CodexTicketReject
	}
	return CodexTicketRuntimeView{Control: CodexTicketControl{Settings: cfg, ProxyState: "active", ValidUntilMS: now.Add(6 * time.Second).UnixMilli()}, Ticket: ticket, ServerTime: now}, nil
}
func TestCodexTicketWSCompactV2BypassesExperiment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing=%t", missing), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 5
			account := codexWSAccount(OpenAIWSIngressModePassthrough, true)
			settings := &codexWSSettings{enabled: true}
			upstream := &codexWSHTTPUpstream{}
			svc := newPassthroughLifecycleService(cfg, newStagedPassthroughConn())
			svc.httpUpstream = upstream
			svc.codexTicketRuntime = NewCodexTicketRuntime(settings, nil, &codexWSAccounts{a: account}, codexWSCache{settings: settings, missing: missing, reject: true})
			server, serverErr := startPassthroughHookRecordingServer(t, ctx, svc, account, nil)
			defer server.Close()
			client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-6-astra","input":[{"type":"compaction_trigger"}]}`)
			defer client.CloseNow()
			_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
			require.NoError(t, err)
			upstream.mu.Lock()
			headers := append([]http.Header(nil), upstream.headers...)
			upstream.mu.Unlock()
			require.Len(t, headers, 1, "compact must reach HTTP upstream even under reject policy without a ticket")
			require.Empty(t, headers[0].Get("X-Codex-Turn-State"), "compact must not inject an available experimental ticket")
			if !missing {
				require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6-astra","input":[{"role":"user","content":"hi"}]}`)))
				_, err = readPassthroughLifecycleFrame(t, client, 3*time.Second)
				require.NoError(t, err)
				upstream.mu.Lock()
				headers = append([]http.Header(nil), upstream.headers...)
				upstream.mu.Unlock()
				require.Len(t, headers, 2)
				require.Equal(t, "gAAAAA"+strings.Repeat("a", 286), headers[1].Get("X-Codex-Turn-State"), "a subsequent generation must still apply ticket policy")
			}
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case err := <-serverErr:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("bridge did not finish")
			}
		})
	}
}

func TestCodexTicketWSOptInForcesBridgeAndKeepsItAfterDisable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 5
	account := codexWSAccount(OpenAIWSIngressModePassthrough, true)
	settings := &codexWSSettings{enabled: true}
	accounts := &codexWSAccounts{a: account}
	upstream := &codexWSHTTPUpstream{}
	svc := newPassthroughLifecycleService(cfg, newStagedPassthroughConn())
	svc.httpUpstream = upstream
	svc.codexTicketRuntime = NewCodexTicketRuntime(settings, nil, accounts, codexWSCache{settings: settings})
	server, serverErr := startPassthroughHookRecordingServer(t, ctx, svc, account, nil)
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-6-astra"}`)
	defer client.CloseNow()
	for turn := 1; turn <= 2; turn++ {
		_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
		require.NoError(t, err)
		if turn == 1 {
			settings.mu.Lock()
			settings.enabled = false
			settings.mu.Unlock()
			require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6-astra"}`)))
		}
	}
	upstream.mu.Lock()
	require.Len(t, upstream.headers, 2)
	require.Equal(t, "gAAAAA"+strings.Repeat("a", 286), upstream.headers[0].Get("X-Codex-Turn-State"), "bridge must execute HTTP Apply")
	require.Empty(t, upstream.headers[1].Get("X-Codex-Turn-State"), "disabled later turn must stop injection while retaining bridge")
	upstream.mu.Unlock()
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("bridge did not finish")
	}
}
