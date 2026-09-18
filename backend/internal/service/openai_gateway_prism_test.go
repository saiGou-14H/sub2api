package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func prismGatewayTestService(upstream *prismTestUpstream) *OpenAIGatewayService {
	service := &OpenAIGatewayService{httpUpstream: upstream}
	service.openAIPrismTransportFactory = func() *OpenAIPrismTransport {
		return NewOpenAIPrismTransportFromUpstream(upstream, OpenAIPrismTransportOptions{BaseURL: "https://prism.openai.com", PollInterval: time.Millisecond, PollLimit: 4})
	}
	return service
}

func prismGatewayTestAccount() *Account {
	return &Account{ID: 99, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Credentials: map[string]any{"access_token": "access-secret"}, Extra: map[string]any{OpenAIWebTransportExtraKey: OpenAITransportPrism}, Concurrency: 1}
}

func prismGatewayTestContext(path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

func TestOpenAIGatewayPrismRoutingProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		path string
		body string
		call func(*OpenAIGatewayService, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
	}{
		{"responses", "/v1/responses", `{"model":"gpt-5.6-sol","stream":true,"input":"hello"}`, func(s *OpenAIGatewayService, c *gin.Context, a *Account, b []byte) (*OpenAIForwardResult, error) {
			return s.Forward(c.Request.Context(), c, a, b)
		}},
		{"chat", "/v1/chat/completions", `{"model":"gpt-5.6-sol","stream":true,"messages":[{"role":"user","content":"hello"}]}`, func(s *OpenAIGatewayService, c *gin.Context, a *Account, b []byte) (*OpenAIForwardResult, error) {
			return s.ForwardAsChatCompletions(c.Request.Context(), c, a, b, "", "")
		}},
		{"anthropic", "/v1/messages", `{"model":"gpt-5.6-sol","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`, func(s *OpenAIGatewayService, c *gin.Context, a *Account, b []byte) (*OpenAIForwardResult, error) {
			return s.ForwardAsAnthropic(c.Request.Context(), c, a, b, "", "")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &prismTestUpstream{}
			service := prismGatewayTestService(upstream)
			account := prismGatewayTestAccount()
			c, recorder := prismGatewayTestContext(tt.path, tt.body)
			c.Request.Header.Set("X-Prism-Progress", "true")
			result, err := tt.call(service, c, account, []byte(tt.body))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, OpenAIPrismStartPath, result.UpstreamEndpoint)
			require.Equal(t, OpenAIPrismStartPath, GetActualOpenAIUpstreamEndpoint(c))
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Body.String(), "hello")
			require.Contains(t, recorder.Body.String(), "event: prism.file_sync\n")
			require.Contains(t, recorder.Body.String(), `"status":"not_required"`)
		})
	}
}

// This fake exercises the HAR wire contract through the public forwarders. It
// generates distinct projects/conversations/responses and never reaches a host.
type prismGatewayUpstream struct {
	starts      []map[string]any
	calls       []string
	tool        bool
	errorPath   string
	errorStatus int
	errorCause  error
}

func (u *prismGatewayUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls = append(u.calls, req.URL.Path)
	if req.URL.Path == u.errorPath {
		if u.errorCause != nil {
			return nil, u.errorCause
		}
		return &http.Response{StatusCode: u.errorStatus, Header: http.Header{"Retry-After": []string{"30"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"provider unavailable"}}`))}, nil
	}
	if resp, handled, err := prismTestBootstrapResponse(req); handled {
		return resp, err
	}
	var v any
	switch req.URL.Path {
	case "/auth/session":
		v = map[string]any{"user": map[string]any{"app_metadata": map[string]string{"user_id": "test-user"}}}
	case OpenAIPrismProjectPath:
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		v = map[string]any{"uuid": body["project_uuid"]}
	case OpenAIPrismProjectAccessPath:
		v = map[string]bool{"accessible": true}
	case OpenAIPrismBackendNewPath:
		v = map[string]string{"url": "https://prism.openai.com/s/sandboxes/proxy", "token": "sandbox-test-secret"}
	case OpenAIPrismConversationHistoryPath:
		v = map[string]any{"items": []any{}}
	case OpenAIPrismStartPath:
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		u.starts = append(u.starts, body)
		v = map[string]any{"status": "started", "request_id": fmt.Sprintf("req_%d", len(u.starts)), "conversation_id": body["conversationId"], "turn_state": map[string]any{"opaque": "opaque-test-secret"}, "codex_listen_snapshot": map[string]any{"conversation_id": body["conversationId"]}}
	case OpenAIPrismStatusPath:
		text := "hello"
		if u.tool {
			start := u.starts[len(u.starts)-1]
			input, _ := start["input"].([]any)
			for _, raw := range input {
				msg, _ := raw.(map[string]any)
				parts, _ := msg["content"].([]any)
				for _, part := range parts {
					partObject, _ := part.(map[string]any)
					content, _ := partObject["text"].(string)
					const marker = "The exact request-scoped protocol declaration is: "
					if i := strings.Index(content, marker); i >= 0 {
						var declaration map[string]any
						if err := json.NewDecoder(strings.NewReader(content[i+len(marker):])).Decode(&declaration); err != nil {
							return nil, err
						}
						delete(declaration, "tools")
						declaration["calls"] = []any{map[string]any{"name": "list_files", "type": "function", "arguments": map[string]string{"path": "."}}}
						data, _ := json.Marshal(declaration)
						text = string(data)
					}
				}
			}
		}
		v = map[string]any{"status": "completed", "response": map[string]any{"status": "success", "payload": map[string]any{"id": fmt.Sprintf("resp_%d", len(u.starts)), "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": text}}}}}}}
	default:
		return nil, fmt.Errorf("unexpected Prism path: %s", req.URL.Path)
	}
	data, err := json.Marshal(v)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(data))}, err
}
func (u *prismGatewayUpstream) DoWithTLS(r *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, n)
}
func prismIntegrationService(u *prismGatewayUpstream) *OpenAIGatewayService {
	s := &OpenAIGatewayService{httpUpstream: u}
	s.openAIPrismTransportFactory = func() *OpenAIPrismTransport {
		return NewOpenAIPrismTransportFromUpstream(u, OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 4})
	}
	return s
}
func TestOpenAIGatewayPrismProtocolsAndTools(t *testing.T) {
	for _, protocol := range []string{"responses", "chat", "anthropic"} {
		for _, stream := range []bool{false, true} {
			for _, useTools := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%v/tools=%v", protocol, stream, useTools), func(t *testing.T) {
					u := &prismGatewayUpstream{tool: useTools}
					s := prismIntegrationService(u)
					a := prismGatewayTestAccount()
					a.Extra["prism_prompt_tool_bridge"] = useTools
					payload := map[string]any{"model": OpenAIPrismDefaultModel, "stream": stream}
					schema := map[string]any{"type": "object", "properties": map[string]any{"path": map[string]string{"type": "string"}}, "required": []string{"path"}}
					tool := map[string]any{"type": "function", "name": "list_files", "parameters": schema}
					if protocol == "responses" {
						payload["input"] = "hello"
					} else {
						payload["messages"] = []any{map[string]string{"role": "user", "content": "hello"}}
					}
					if protocol == "anthropic" {
						payload["max_tokens"] = 128
						tool = map[string]any{"name": "list_files", "input_schema": schema}
					}
					if protocol == "chat" {
						tool = map[string]any{"type": "function", "function": map[string]any{"name": "list_files", "parameters": schema}}
					}
					if useTools {
						payload["tools"] = []any{tool}
					}
					raw, _ := json.Marshal(payload)
					c, w := prismGatewayTestContext("/v1/"+protocol, string(raw))
					var result *OpenAIForwardResult
					var err error
					switch protocol {
					case "responses":
						result, err = s.Forward(c.Request.Context(), c, a, raw)
					case "chat":
						result, err = s.ForwardAsChatCompletions(c.Request.Context(), c, a, raw, "", "")
					case "anthropic":
						result, err = s.ForwardAsAnthropic(c.Request.Context(), c, a, raw, "", "")
					}
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, 200, w.Code)
					require.Equal(t, OpenAIPrismStartPath, result.UpstreamEndpoint)
					require.Equal(t, stream, result.Stream)
					require.Contains(t, u.calls, OpenAIPrismStartPath)
					require.Contains(t, u.calls, OpenAIPrismStatusPath)
					require.NotContains(t, w.Body.String(), "sandbox-test-secret")
					require.NotContains(t, w.Body.String(), "opaque-test-secret")
					require.Empty(t, w.Header().Get("X-Prism-Project-ID"))
					require.Empty(t, w.Header().Get("Set-Cookie"))
					if useTools {
						require.Contains(t, w.Body.String(), "list_files")
						require.NotContains(t, w.Body.String(), "schema_hash")
						switch protocol {
						case "responses":
							require.Contains(t, w.Body.String(), "function_call")
						case "chat":
							require.Contains(t, w.Body.String(), "tool_calls")
						case "anthropic":
							require.Contains(t, w.Body.String(), "tool_use")
						}
					} else {
						require.Contains(t, w.Body.String(), "hello")
					}
					if !stream {
						require.Equal(t, "not_required", w.Header().Get("X-Prism-Sync-Status"))
						require.True(t, json.Valid(w.Body.Bytes()))
					} else {
						require.Contains(t, w.Body.String(), ": prism file sync not_required\n\n")
					}
				})
			}
		}
	}
}
func TestOpenAIGatewayPrismContinuationIsolation(t *testing.T) {
	u := &prismGatewayUpstream{}
	s := prismIntegrationService(u)
	a := prismGatewayTestAccount()
	call := func(apiKey int64, account *Account, previous string) (*http.Response, error) {
		c, _ := prismGatewayTestContext("/v1/responses", "")
		c.Set("api_key", &APIKey{ID: apiKey})
		return s.doOpenAIPrism(c.Request.Context(), c, account, &apicompat.ResponsesRequest{Model: OpenAIPrismDefaultModel, Input: json.RawMessage(`"hi"`), Stream: true, PreviousResponseID: previous})
	}
	resp, err := call(1, a, "")
	require.NoError(t, err)
	_ = resp.Body.Close()
	first := u.starts[0]
	resp, err = call(1, a, "resp_1")
	require.NoError(t, err)
	_ = resp.Body.Close()
	second := u.starts[1]
	require.Equal(t, first["conversationId"], second["conversationId"])
	firstMetadata, ok := first["metadata"].(map[string]any)
	require.True(t, ok)
	secondMetadata, ok := second["metadata"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, firstMetadata["projectId"], secondMetadata["projectId"])
	require.NotEmpty(t, secondMetadata["codex_listen_snapshot"])
	require.Equal(t, 1, strings.Count(strings.Join(u.calls, "|"), OpenAIPrismBackendNewPath))
	for _, key := range []int64{1, 2} {
		resp, err = call(key, a, "")
		require.NoError(t, err)
		_ = resp.Body.Close()
		last := u.starts[len(u.starts)-1]
		require.NotEqual(t, first["conversationId"], last["conversationId"])
		lastMetadata, ok := last["metadata"].(map[string]any)
		require.True(t, ok)
		require.NotEqual(t, firstMetadata["projectId"], lastMetadata["projectId"])
	}
	before := len(u.calls)
	_, err = call(2, a, "resp_1")
	require.Error(t, err)
	other := *a
	other.ID++
	_, err = call(1, &other, "resp_1")
	require.Error(t, err)
	other = *a
	other.Credentials = map[string]any{"access_token": "rotated-secret"}
	_, err = call(1, &other, "resp_1")
	require.Error(t, err)
	require.Len(t, u.calls, before)
}
func TestOpenAIGatewayPrismErrors(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			u := &prismGatewayUpstream{errorPath: OpenAIPrismStartPath, errorStatus: status}
			s := prismIntegrationService(u)
			a := prismGatewayTestAccount()
			body := `{"model":"gpt-5.6-sol","input":"hi"}`
			c, w := prismGatewayTestContext("/v1/responses", body)
			_, err := s.Forward(c.Request.Context(), c, a, []byte(body))
			require.Error(t, err)
			if status == 400 {
				require.Equal(t, status, w.Code)
				require.Contains(t, w.Body.String(), "provider unavailable")
			} else {
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.Equal(t, status, failover.StatusCode)
				require.Equal(t, "30", failover.ResponseHeaders.Get("Retry-After"))
				require.False(t, c.Writer.Written())
			}
		})
	}
	u := &prismGatewayUpstream{}
	s := prismIntegrationService(u)
	a := prismGatewayTestAccount()
	c, _ := prismGatewayTestContext("/v1/responses", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.doOpenAIPrism(ctx, c, a, &apicompat.ResponsesRequest{Model: OpenAIPrismDefaultModel})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, u.calls)
}

func TestOpenAIGatewayPrismPollFailureDoesNotRestart(t *testing.T) {
	for _, protocol := range []string{"responses", "chat", "anthropic"} {
		for _, status := range []int{429, 503, 0} {
			t.Run(fmt.Sprintf("%s/status=%d", protocol, status), func(t *testing.T) {
				u := &prismGatewayUpstream{errorPath: OpenAIPrismStatusPath, errorStatus: status}
				if status == 0 {
					u.errorCause = errors.New("connection reset")
				}
				s := prismIntegrationService(u)
				a := prismGatewayTestAccount()
				body := []byte(`{"model":"gpt-5.6-sol","input":"hi","messages":[{"role":"user","content":"hi"}],"max_tokens":128}`)
				c, w := prismGatewayTestContext("/v1/"+protocol, string(body))
				var err error
				switch protocol {
				case "responses":
					_, err = s.Forward(c.Request.Context(), c, a, body)
				case "chat":
					_, err = s.ForwardAsChatCompletions(c.Request.Context(), c, a, body, "", "")
				case "anthropic":
					_, err = s.ForwardAsAnthropic(c.Request.Context(), c, a, body, "", "")
				}
				var started *OpenAIPrismStartedError
				require.ErrorAs(t, err, &started)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover))
				require.Len(t, u.starts, 1)
				require.True(t, c.Writer.Written())
				require.True(t, json.Valid(w.Body.Bytes()))
				if status == 0 {
					require.Equal(t, http.StatusBadGateway, w.Code)
				} else {
					require.Equal(t, status, w.Code)
					require.Equal(t, "30", w.Header().Get("Retry-After"))
				}
			})
		}
	}
}
