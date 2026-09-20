//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexTicketHTTPBuildersUseFinalModelAndOptIn(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			runtime, accounts, store, key := ticketRuntimeFixture()
			s := &OpenAIGatewayService{accountRepo: accounts, codexTicketRuntime: runtime}
			build := func(path, model string) (*http.Request, error, *httptest.ResponseRecorder) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest("POST", path, strings.NewReader("{}"))
				c.Request.Header.Set("X-Codex-Turn-State", "client-echo")
				body := []byte(fmt.Sprintf(`{"model":%q,"stream":true}`, model))
				var req *http.Request
				var err error
				if passthrough {
					req, err = s.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, accounts.a, body, "test-token")
				} else {
					req, err = s.buildUpstreamRequest(context.Background(), c, accounts.a, body, "test-token", true, "", true)
				}
				return req, err, w
			}
			request, err, _ := build("/v1/responses", key.Model)
			require.NoError(t, err)
			require.Equal(t, store.v.Ticket.State, request.Header.Get("X-Codex-Turn-State"))
			require.Equal(t, "Bearer test-token", request.Header.Get("Authorization"))
			require.Equal(t, chatgptCodexURL, request.URL.String())
			require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(request.Context()))
			request, err, _ = build("/v1/responses", "unconfigured-final-model")
			require.NoError(t, err)
			require.Equal(t, "client-echo", request.Header.Get("X-Codex-Turn-State"))
			request, err, _ = build("/v1/responses/compact", key.Model)
			require.NoError(t, err)
			require.Equal(t, "client-echo", request.Header.Get("X-Codex-Turn-State"))
			store.v.Ticket = nil
			request, err, recorder := build("/v1/responses", key.Model)
			require.Nil(t, request)
			require.ErrorIs(t, err, ErrCodexTicketMissing)
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.Contains(t, recorder.Body.String(), "CODEX_TICKET_MISSING")
			accounts.a.Extra[CodexTurnStateEnabledExtraKey] = false
			request, err, _ = build("/v1/responses", key.Model)
			require.NoError(t, err)
			require.Equal(t, "client-echo", request.Header.Get("X-Codex-Turn-State"))
		})
	}
}

func TestCodexTicketHTTPCompactV2PreservesEchoGuard(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, hasTicket := range []bool{false, true} {
			for _, sameAccount := range []bool{false, true} {
				t.Run(fmt.Sprintf("passthrough=%t/ticket=%t/same-account=%t", passthrough, hasTicket, sameAccount), func(t *testing.T) {
					runtime, accounts, store, key := ticketRuntimeFixture()
					store.v.Control.Settings.MissingPolicy = CodexTicketReject
					if !hasTicket {
						store.v.Ticket = nil
					}
					s := &OpenAIGatewayService{accountRepo: accounts, codexTicketRuntime: runtime}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					c.Request.Header.Set("X-Codex-Turn-State", "client-echo")
					c.Request.Header.Set("session_id", "compact-session")
					origin := *accounts.a
					if !sameAccount {
						origin.ID++
					}
					s.noteOpenAICodexTurnStateProvenance(c, &origin)
					body := []byte(fmt.Sprintf(`{"model":%q,"stream":true,"input":[{"type":"compaction_trigger"},{"role":"user","content":"hi"}]}`, key.Model))
					var req *http.Request
					var err error
					if passthrough {
						req, err = s.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, accounts.a, body, "test-token")
					} else {
						req, err = s.buildUpstreamRequest(context.Background(), c, accounts.a, body, "test-token", true, "", true)
					}
					require.NoError(t, err, "V2 compact must bypass missing-ticket rejection")
					want := ""
					if sameAccount {
						want = "client-echo"
					}
					require.Equal(t, want, req.Header.Get("X-Codex-Turn-State"))
				})
			}
		}
	}
}

func TestCodexTicketHTTPForwardUsesActualFinalModel(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, targetConfigured := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%t/target-configured=%t", passthrough, targetConfigured), func(t *testing.T) {
				runtime, accounts, store, key := ticketRuntimeFixture()
				requestModel, mappedModel := "astra-public", key.Model
				if !targetConfigured {
					requestModel, mappedModel = key.Model, "gpt-5.4"
					store.v.Ticket = nil
				}
				finalModel := mappedModel
				if passthrough {
					// Main deliberately ignores normal account model_mapping in passthrough.
					// Keep a conflicting mapping to prove policy uses the unchanged wire model.
					requestModel, mappedModel = mappedModel, requestModel
					finalModel = requestModel
				}
				accounts.a.Credentials["access_token"] = "test-token"
				accounts.a.Credentials["model_mapping"] = map[string]any{requestModel: mappedModel}
				accounts.a.Extra["openai_passthrough"] = passthrough
				accounts.a.Extra["openai_oauth_responses_websockets_v2_mode"] = OpenAIWSIngressModeOff
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_mapped\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n")),
				}}
				s := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: accounts, httpUpstream: upstream, codexTicketRuntime: runtime}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0")
				c.Request.Header.Set("X-Codex-Turn-State", "client-echo")
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				body := []byte(fmt.Sprintf(`{"model":%q,"stream":true,"input":[{"role":"user","content":"hi"}]}`, requestModel))
				_, err := s.Forward(context.Background(), c, accounts.a, body)
				require.NoError(t, err)
				require.NotNil(t, upstream.lastReq)
				require.Equal(t, finalModel, gjson.GetBytes(upstream.lastBody, "model").String())
				want := "client-echo"
				if targetConfigured {
					want = store.v.Ticket.State
				}
				require.Equal(t, want, upstream.lastReq.Header.Get("X-Codex-Turn-State"))
			})
		}
	}
}

func TestCodexTicketPolicyDoesNotPenalizeAccount(t *testing.T) {
	// No scheduler is configured: an attempt to initialize/report one would fail.
	s := &OpenAIGatewayService{}
	for _, err := range []error{ErrCodexTicketMissing, fmt.Errorf("builder: %w", ErrCodexTicketControlUnavailable), NewOpenAIWSClientCloseError(1008, "reconnect", ErrCodexTicketReconnectRequired)} {
		require.False(t, s.ReportOpenAIAccountScheduleResult(&Account{ID: 1}, "model", false, nil, err))
		require.False(t, s.ObserveOpenAIAccountHealthFailure(context.Background(), &Account{ID: 1}, err))
	}
}
