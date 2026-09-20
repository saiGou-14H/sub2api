//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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

func TestCodexTicketPolicyDoesNotPenalizeAccount(t *testing.T) {
	// No scheduler is configured: an attempt to initialize/report one would fail.
	s := &OpenAIGatewayService{}
	for _, err := range []error{ErrCodexTicketMissing, fmt.Errorf("builder: %w", ErrCodexTicketControlUnavailable), NewOpenAIWSClientCloseError(1008, "reconnect", ErrCodexTicketReconnectRequired)} {
		require.False(t, s.ReportOpenAIAccountScheduleResult(&Account{ID: 1}, "model", false, nil, err))
		require.False(t, s.ObserveOpenAIAccountHealthFailure(context.Background(), &Account{ID: 1}, err))
	}
}
