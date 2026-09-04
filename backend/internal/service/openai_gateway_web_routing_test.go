package service

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayWebRoutingChatAndResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		path       string
		body       string
		stream     bool
		forward    func(*OpenAIGatewayService, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
		assertBody func(*testing.T, string)
	}{
		{
			name:   "chat_non_stream",
			path:   "/v1/chat/completions",
			body:   `{"model":"auto","messages":[{"role":"user","content":"hello"}],"temperature":0.2,"top_p":0.7,"max_tokens":32,"max_completion_tokens":64,"parallel_tool_calls":false,"tool_choice":"none","function_call":"auto","reasoning_effort":"minimal"}`,
			stream: false,
			forward: func(s *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.ForwardAsChatCompletions(c.Request.Context(), c, account, body, "", "")
			},
			assertBody: func(t *testing.T, body string) {
				require.Contains(t, body, `"object":"chat.completion"`)
				require.Contains(t, body, `"content":"OK"`)
			},
		},
		{
			name:   "chat_stream",
			path:   "/v1/chat/completions",
			body:   `{"model":"auto","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			stream: true,
			forward: func(s *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.ForwardAsChatCompletions(c.Request.Context(), c, account, body, "", "")
			},
			assertBody: func(t *testing.T, body string) {
				require.Contains(t, body, `"object":"chat.completion.chunk"`)
				require.Contains(t, body, `"content":"OK"`)
				require.Contains(t, body, "data: [DONE]")
			},
		},
		{
			name:   "responses_non_stream",
			path:   "/v1/responses",
			body:   `{"model":"auto","input":"hello","temperature":0.2,"top_p":0.7,"max_output_tokens":64,"parallel_tool_calls":false,"tool_choice":"none","include":["reasoning.encrypted_content"],"store":false,"service_tier":"auto","prompt_cache_key":"web-test","reasoning":{"effort":"minimal","summary":"auto"},"text":{"verbosity":"low"}}`,
			stream: false,
			forward: func(s *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.Forward(c.Request.Context(), c, account, body)
			},
			assertBody: func(t *testing.T, body string) {
				require.Contains(t, body, `"object":"response"`)
				require.Contains(t, body, `"text":"OK"`)
			},
		},
		{
			name:   "responses_stream",
			path:   "/v1/responses",
			body:   `{"model":"auto","stream":true,"input":"hello"}`,
			stream: true,
			forward: func(s *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.Forward(c.Request.Context(), c, account, body)
			},
			assertBody: func(t *testing.T, body string) {
				require.Contains(t, body, "event: response.output_text.delta")
				require.Contains(t, body, `"delta":"OK"`)
				require.Contains(t, body, "event: response.completed")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := newOpenAIWebGatewayTestUpstream()
			service := &OpenAIGatewayService{httpUpstream: upstream}
			service.openAIWebTransportFactory = func() *OpenAIWebTransport {
				return NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{BaseURL: "https://web.test"})
			}
			proxyID := int64(7)
			account := &Account{
				ID:       42,
				Name:     "web-account",
				Platform: PlatformOpenAI,
				Type:     AccountTypeSetupToken,
				Credentials: map[string]any{
					"access_token": "gateway-test-token",
					"model_mapping": map[string]any{
						OpenAIWebTestModel: "gpt-5.6-sol",
					},
				},
				Extra:       map[string]any{OpenAIWebTransportExtraKey: OpenAITransportWeb},
				ProxyID:     &proxyID,
				Proxy:       &Proxy{Protocol: "socks5h", Host: "127.0.0.1", Port: 7897},
				Concurrency: 1,
			}

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")

			result, err := tt.forward(service, c, account, []byte(tt.body))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.stream, result.Stream)
			require.Equal(t, OpenAIWebTestModel, result.BillingModel)
			require.Equal(t, OpenAIWebTestModel, result.UpstreamModel)
			require.Equal(t, OpenAIWebConversationPath, result.UpstreamEndpoint)
			require.Equal(t, OpenAIWebConversationPath, GetActualOpenAIUpstreamEndpoint(c))
			require.Equal(t, http.StatusOK, recorder.Code)
			tt.assertBody(t, recorder.Body.String())

			require.Len(t, upstream.requests, 6)
			require.Equal(t, OpenAIWebSentinelPingPath, upstream.requests[4].URL.Path)
			require.Equal(t, OpenAIWebConversationPath, upstream.requests[5].URL.Path)
			require.Equal(t, "Bearer gateway-test-token", upstream.requests[5].Header.Get("Authorization"))
			require.Equal(t, "socks5h://127.0.0.1:7897", upstream.proxies[5])
			require.Equal(t, "conduit-test", upstream.requests[5].Header.Get("X-Conduit-Token"))
			conversationBody, readErr := io.ReadAll(upstream.requests[5].Body)
			require.NoError(t, readErr)
			require.Contains(t, string(conversationBody), `"action":"next"`)
			require.Contains(t, string(conversationBody), `"model":"auto"`)
			require.Contains(t, string(conversationBody), `"parts":["hello"]`)
			require.NotContains(t, string(conversationBody), `"temperature"`)
			require.NotContains(t, string(conversationBody), `"top_p"`)
			require.NotContains(t, string(conversationBody), `"max_tokens"`)
			require.NotContains(t, string(conversationBody), `"max_completion_tokens"`)
			require.NotContains(t, string(conversationBody), `"max_output_tokens"`)
		})
	}
}

func TestOpenAIGatewayWebRoutingRejectsUnsupportedParametersBeforeNetwork(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name    string
		path    string
		body    string
		param   string
		forward func(*OpenAIGatewayService, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
	}{
		{
			name:  "responses previous response",
			path:  "/v1/responses",
			body:  `{"model":"auto","input":"hello","previous_response_id":"resp_previous"}`,
			param: "previous_response_id",
			forward: func(s *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.Forward(c.Request.Context(), c, account, body)
			},
		},
		{
			name:  "chat missing model",
			path:  "/v1/chat/completions",
			body:  `{"messages":[{"role":"user","content":"hello"}]}`,
			param: "model",
			forward: func(s *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.ForwardAsChatCompletions(c.Request.Context(), c, account, body, "", "")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := newOpenAIWebGatewayTestUpstream()
			service := &OpenAIGatewayService{httpUpstream: upstream}
			account := &Account{
				ID:          42,
				Platform:    PlatformOpenAI,
				Type:        AccountTypeSetupToken,
				Credentials: map[string]any{"access_token": "gateway-test-token"},
				Extra:       map[string]any{OpenAIWebTransportExtraKey: OpenAITransportWeb},
				Concurrency: 1,
			}

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")

			result, err := tt.forward(service, c, account, []byte(tt.body))
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.True(t, IsResponseCommitted(c))
			require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
			require.Equal(t, tt.param, gjson.Get(recorder.Body.String(), "error.param").String())
			require.Empty(t, upstream.requests)
		})
	}
}

func newOpenAIWebGatewayTestUpstream() *openAIWebTestUpstream {
	return &openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "text/html", `<html data-build="build-test"><script src="/static/sdk.js"></script></html>`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"status":"ok","conduit_token":"conduit-test"}`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"prepare_token":"prepare-test"}`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"token":"requirements-test"}`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{}`),
		openAIWebTestResponse(http.StatusOK, "text/event-stream", strings.Join([]string{
			`data: {"conversation_id":"conv-test","o":"append","p":"/message/content/parts/0","v":"OK"}`,
			"",
			`data: {"conversation_id":"conv-test","is_complete":true}`,
			"",
		}, "\n")),
	}}
}
