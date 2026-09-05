package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

type openAIWebTestUpstream struct {
	responses       []*http.Response
	requests        []*http.Request
	proxies         []string
	prepareResponse func(*http.Request) *http.Response
}

func (u *openAIWebTestUpstream) Do(req *http.Request, proxyURL string, _ int64, _ int) (*http.Response, error) {
	u.requests = append(u.requests, req)
	u.proxies = append(u.proxies, proxyURL)
	if strings.HasSuffix(req.URL.Path, "/sentinel/chat-requirements/prepare") && u.prepareResponse != nil {
		return u.prepareResponse(req), nil
	}
	if len(u.responses) == 0 {
		return nil, io.EOF
	}
	response := u.responses[0]
	u.responses = u.responses[1:]
	return response, nil
}

func (u *openAIWebTestUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func openAIWebTestResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestOpenAIWebTransportBuildConversationPayload(t *testing.T) {
	maxTokens := 64
	maxCompletionTokens := 128
	transport := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{
		DeviceID:  "device-test",
		SessionID: "session-test",
	})
	req := &apicompat.ChatCompletionsRequest{
		Model:               "auto",
		Instructions:        "be concise",
		MaxTokens:           &maxTokens,
		MaxCompletionTokens: &maxCompletionTokens,
		Messages: []apicompat.ChatMessage{
			{Role: "user", Content: []byte(`"hello"`)},
			{Role: "developer", Content: []byte(`"rules"`)},
		},
		ReasoningEffort: "xhigh",
	}
	body, err := transport.BuildConversationPayloadWithOptions(OpenAIWebConversationOptions{
		Request:         req,
		ConversationID:  "conversation-test",
		ParentMessageID: "parent-test",
	})
	require.NoError(t, err)
	require.Contains(t, string(body), `"action":"next"`)
	require.Contains(t, string(body), `"conversation_id":"conversation-test"`)
	require.NotContains(t, string(body), `"conversation_origin"`)
	require.NotContains(t, string(body), `"model_response_contracts"`)
	require.NotContains(t, string(body), `"thinking_effort"`)
	require.NotContains(t, string(body), "access_token")
	require.NotContains(t, string(body), "max_tokens")
	require.NotContains(t, string(body), "max_completion_tokens")
}

func TestOpenAIWebConversationPayloadMatchesPlusHarContract(t *testing.T) {
	transport := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{})
	body, err := transport.BuildConversationPayload(&apicompat.ChatCompletionsRequest{
		Model: "gpt-5.6-sol-wm",
		Messages: []apicompat.ChatMessage{{
			Role:    "user",
			Content: json.RawMessage(`"hello"`),
		}},
	})
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "tpp", payload["conversation_origin"])
	require.Equal(t, []any{"v1"}, payload["supported_encodings"])
	contracts, ok := payload["model_response_contracts"].([]any)
	require.True(t, ok)
	require.Len(t, contracts, 1)
	contract := contracts[0].(map[string]any)
	require.Equal(t, "photo_upload_action.v1", contract["id"])
	require.Equal(t, float64(1), contract["protocol_version"])
	require.Equal(t, []any{"cap:image", "cap:file", "placement:end"}, contract["presets"])
	message := payload["messages"].([]any)[0].(map[string]any)
	metadata := message["metadata"].(map[string]any)
	require.Equal(t, []any{}, metadata["selected_sources"])
}

func TestOpenAIWebTransportPromptToolsNeverSerializeNativeToolFields(t *testing.T) {
	parallel := true
	request := &apicompat.ChatCompletionsRequest{
		Model:             "auto",
		ParallelToolCalls: &parallel,
		ToolChoice:        json.RawMessage(`"required"`),
		FunctionCall:      json.RawMessage(`"auto"`),
		Tools: []apicompat.ChatTool{{Type: "function", Function: &apicompat.ChatFunction{
			Name:        "lookup",
			Description: "Look up a city",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}}},
		Messages: []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
	}
	promptTools, err := NewOpenAIWebPromptToolsFromChatRequest(request)
	require.NoError(t, err)
	require.NotNil(t, promptTools)

	body, err := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{}).BuildConversationPayloadWithOptions(OpenAIWebConversationOptions{
		Request:     request,
		PromptTools: promptTools,
	})
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	_, hasTools := payload["tools"]
	_, hasFunctions := payload["functions"]
	_, hasToolChoice := payload["tool_choice"]
	_, hasFunctionCall := payload["function_call"]
	_, hasParallelToolCalls := payload["parallel_tool_calls"]
	require.False(t, hasTools)
	require.False(t, hasFunctions)
	require.False(t, hasToolChoice)
	require.False(t, hasFunctionCall)
	require.False(t, hasParallelToolCalls)
	require.Contains(t, string(body), promptTools.Protocol)
	// The caller's public request remains intact for response conversion and
	// diagnostics; only the transport-local copy is sanitized.
	require.Len(t, request.Tools, 1)
	require.NotEmpty(t, request.ToolChoice)
}

func TestOpenAIWebTransportPromptToolsSanitizeToolHistoryMetadata(t *testing.T) {
	request := &apicompat.ChatCompletionsRequest{
		Model: "auto",
		Tools: []apicompat.ChatTool{{Type: "function", Function: &apicompat.ChatFunction{
			Name:       "lookup",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
		}}},
		Messages: []apicompat.ChatMessage{
			{Role: "assistant", ToolCalls: []apicompat.ChatToolCall{{ID: "call_1", Type: "function", Function: apicompat.ChatFunctionCall{Name: "lookup", Arguments: `{}`}}}},
			{Role: "tool", ToolCallID: "call_1", Content: json.RawMessage(`"{}"`)},
		},
	}
	promptTools, err := NewOpenAIWebPromptToolsFromChatRequest(request)
	require.NoError(t, err)
	body, err := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{}).BuildConversationPayloadWithOptions(OpenAIWebConversationOptions{
		Request: request, PromptTools: promptTools,
	})
	require.NoError(t, err)
	require.NotContains(t, string(body), `"tool_calls"`)
	require.NotContains(t, string(body), `"tool_call_id"`)
	require.Contains(t, string(body), "Previous assistant tool calls")
	require.Contains(t, string(body), "Previous tool result (call_id=call_1)")
}

func TestOpenAIWebTransportAcceptsAndDropsResponsesMaxOutputTokens(t *testing.T) {
	maxOutputTokens := 128
	responsesRequest := &apicompat.ResponsesRequest{
		Model:           "auto",
		Input:           json.RawMessage(`"hello"`),
		MaxOutputTokens: &maxOutputTokens,
	}
	require.NoError(t, ValidateOpenAIWebResponsesRequest(responsesRequest))

	chatRequest, err := apicompat.ResponsesToChatCompletionsRequest(responsesRequest)
	require.NoError(t, err)
	require.NotNil(t, chatRequest.MaxCompletionTokens)

	body, err := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{}).BuildConversationPayload(chatRequest)
	require.NoError(t, err)
	require.NotContains(t, string(body), "max_output_tokens")
	require.NotContains(t, string(body), "max_completion_tokens")
	require.NotContains(t, string(body), "max_tokens")
}

func TestNormalizeOpenAIWebModelCatalog(t *testing.T) {
	models := []string{
		"auto",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-5.5",
	}
	require.Equal(t, models, OpenAIWebModels())
	transport := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{})
	for _, model := range models {
		model := model
		t.Run("payload/"+model, func(t *testing.T) {
			body, err := transport.BuildConversationPayload(&apicompat.ChatCompletionsRequest{
				Model:    model,
				Messages: []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
			})
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(body, &payload))
			require.Equal(t, model, payload["model"])
		})
	}

	tests := []struct {
		name      string
		input     string
		canonical string
		supported bool
	}{
		{name: "auto", input: "auto", canonical: "auto", supported: true},
		{name: "sol with whitespace", input: "  gpt-5.6-sol  ", canonical: "gpt-5.6-sol", supported: true},
		{name: "uppercase is not rewritten", input: "GPT-5.6-TERRA", canonical: "", supported: false},
		{name: "luna", input: "gpt-5.6-luna", canonical: "gpt-5.6-luna", supported: true},
		{name: "gpt 5.5", input: " gpt-5.5 ", canonical: "gpt-5.5", supported: true},
		{name: "future slug", input: "gpt-5.6-preview", canonical: "gpt-5.6-preview", supported: true},
		{name: "empty", input: "", canonical: "", supported: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			canonical, supported := NormalizeOpenAIWebModel(tc.input)
			require.Equal(t, tc.canonical, canonical)
			require.Equal(t, tc.supported, supported)
		})
	}
}

func TestOpenAIWebTransportDiscoversVerbatimModelSlugs(t *testing.T) {
	upstream := &openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "application/json", `{"default_model_slug":"gpt-5-6","models":[{"slug":"gpt-5-6"},{"slug":"gpt-5.6-sol-wm","is_work_mode_model":true},{"slug":"gpt-5-6"},{"slug":""}]}`),
	}}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{SkipBootstrap: true})
	snapshot, err := transport.DiscoverOpenAIWebModelCatalog(context.Background(), &Account{ID: 7}, "token")
	require.NoError(t, err)
	require.Equal(t, []string{"auto", "gpt-5-6", "gpt-5.6-sol-wm"}, snapshot.Models)
	require.Equal(t, []string{"gpt-5.6-sol-wm"}, snapshot.WorkModeModels)
	require.Equal(t, "gpt-5-6", snapshot.DefaultModelSlug)
	require.Equal(t, "/backend-api/models", upstream.requests[0].URL.Path)
	require.Equal(t, "iim=false&is_gizmo=false&supports_model_picker_upgrade_presets=true", upstream.requests[0].URL.RawQuery)
}

func TestAccountOpenAIWebWorkModeClassificationUsesCatalogAndSuffix(t *testing.T) {
	account := &Account{Extra: map[string]any{}}
	require.False(t, account.IsOpenAIWebWorkModeModel("auto"))
	require.True(t, account.IsOpenAIWebWorkModeModel("gpt-5.6-sol-wm"))
	account.SetOpenAIWebModelCatalogSnapshot(OpenAIWebModelCatalogSnapshot{
		Models:           []string{"auto", "gpt-5.6-sol"},
		WorkModeModels:   []string{"gpt-5.6-sol"},
		DefaultModelSlug: "auto",
		SyncedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	require.True(t, account.IsOpenAIWebWorkModeModel("gpt-5.6-sol"))
	require.False(t, account.IsOpenAIWebWorkModeModel("auto"))
	account.SetOpenAIWebModelCatalogSnapshot(OpenAIWebModelCatalogSnapshot{
		Models:           []string{"auto", "gpt-5.6-sol"},
		WorkModeModels:   []string{"gpt-5.6-sol"},
		DefaultModelSlug: "gpt-5.6-sol",
		SyncedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	require.True(t, account.IsOpenAIWebWorkModeModel("auto"))
}

func TestAccountOpenAIWebModelCatalogSnapshotControlsSupport(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		OpenAIWebTransportExtraKey: OpenAITransportWeb,
	}}
	require.True(t, account.IsModelSupported("gpt-future"), "valid slugs remain routable before first discovery")
	account.SetOpenAIWebModelCatalogSnapshot(OpenAIWebModelCatalogSnapshot{
		Models: []string{"gpt-5-6", "gpt.5-custom-wm"}, SyncedAt: time.Now().UTC().Format(time.RFC3339),
	})
	require.True(t, account.IsModelSupported("gpt-5-6"))
	require.True(t, account.IsModelSupported("gpt.5-custom-wm"))
	require.False(t, account.IsModelSupported("gpt-unknown"))
}

func TestValidateOpenAIWebModelRejectsMalformedModelAsOpenAIRequestError(t *testing.T) {
	request := &apicompat.ChatCompletionsRequest{
		Model:    "gpt 5.6",
		Messages: []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
	}
	err := ValidateOpenAIWebChatCompletionsRequest(request)
	var requestErr *OpenAIWebRequestError
	require.ErrorAs(t, err, &requestErr)
	require.Equal(t, "model", requestErr.Param)
	require.Contains(t, requestErr.Error(), "not supported by ChatGPT web transport")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	require.ErrorIs(t, writeOpenAIWebRequestError(c, err), requestErr)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Param   string `json:"param"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "invalid_request_error", payload.Error.Type)
	require.Equal(t, "model", payload.Error.Param)
	require.Equal(t, requestErr.Error(), payload.Error.Message)
}

func TestValidateOpenAIWebResponsesModelRejectsMalformedModelAsOpenAIRequestError(t *testing.T) {
	err := ValidateOpenAIWebResponsesRequest(&apicompat.ResponsesRequest{Model: "gpt 5.6"})
	var requestErr *OpenAIWebRequestError
	require.ErrorAs(t, err, &requestErr)
	require.Equal(t, "model", requestErr.Param)
}

func TestOpenAIWebTransportRejectsUnsupportedChatParameters(t *testing.T) {
	tests := []struct {
		name  string
		param string
		req   *apicompat.ChatCompletionsRequest
	}{
		{name: "stop", param: "stop", req: &apicompat.ChatCompletionsRequest{Stop: json.RawMessage(`"END"`)}},
		{name: "tools", param: "tools", req: &apicompat.ChatCompletionsRequest{Tools: []apicompat.ChatTool{{Type: "function"}}}},
		{name: "structured output", param: "response_format", req: &apicompat.ChatCompletionsRequest{ResponseFormat: json.RawMessage(`{"type":"json_object"}`)}},
		{name: "tool history", param: "messages[0].role", req: &apicompat.ChatCompletionsRequest{Messages: []apicompat.ChatMessage{{Role: "tool", Content: json.RawMessage(`"result"`)}}}},
	}
	transport := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Model = "auto"
			if len(tc.req.Messages) == 0 {
				tc.req.Messages = []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}}
			}
			_, err := transport.BuildConversationPayload(tc.req)
			var requestErr *OpenAIWebRequestError
			require.ErrorAs(t, err, &requestErr)
			require.Equal(t, tc.param, requestErr.Param)
		})
	}
}

func TestOpenAIWebTransportMapsMinimalReasoningEffortToMin(t *testing.T) {
	request := &apicompat.ChatCompletionsRequest{
		Model:           "gpt-5.6-sol-wm",
		ReasoningEffort: "minimal",
		Messages:        []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
	}
	body, err := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{}).BuildConversationPayload(request)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "min", payload["thinking_effort"])
}

func TestOpenAIWebTransportAcceptsAndDropsChatSamplingParameters(t *testing.T) {
	temperature := 0.2
	topP := 0.7
	request := &apicompat.ChatCompletionsRequest{
		Model:       "auto",
		Temperature: &temperature,
		TopP:        &topP,
		Messages:    []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
	}
	body, err := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{}).BuildConversationPayload(request)
	require.NoError(t, err)
	require.NotContains(t, string(body), "temperature")
	require.NotContains(t, string(body), "top_p")
}

func TestOpenAIWebTransportRejectsInvalidChatSamplingParameters(t *testing.T) {
	for _, tc := range []struct {
		name  string
		param string
		value float64
	}{
		{name: "temperature below minimum", param: "temperature", value: -0.1},
		{name: "temperature above maximum", param: "temperature", value: 2.1},
		{name: "top p below minimum", param: "top_p", value: -0.1},
		{name: "top p above maximum", param: "top_p", value: 1.1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &apicompat.ChatCompletionsRequest{
				Model:    "auto",
				Messages: []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
			}
			if tc.param == "temperature" {
				req.Temperature = &tc.value
			} else {
				req.TopP = &tc.value
			}
			err := ValidateOpenAIWebChatCompletionsRequest(req)
			var requestErr *OpenAIWebRequestError
			require.ErrorAs(t, err, &requestErr)
			require.Equal(t, tc.param, requestErr.Param)
		})
	}
}

func TestValidateOpenAIWebRequestsAcceptsOnlyEquivalentDefaults(t *testing.T) {
	maxTokens := 64
	maxCompletionTokens := 128
	maxOutputTokens := 256
	one := 1.0
	trueValue := true
	falseValue := false
	require.NoError(t, ValidateOpenAIWebChatCompletionsRequest(&apicompat.ChatCompletionsRequest{
		MaxTokens:           &maxTokens,
		MaxCompletionTokens: &maxCompletionTokens,
		Temperature:         &one,
		TopP:                &one,
		ParallelToolCalls:   &trueValue,
		ToolChoice:          json.RawMessage(`"none"`),
		FunctionCall:        json.RawMessage(`"auto"`),
		ReasoningEffort:     "none",
		ServiceTier:         "default",
		Stop:                json.RawMessage(`[]`),
		ResponseFormat:      json.RawMessage(`{"type":"text"}`),
	}))
	require.NoError(t, ValidateOpenAIWebResponsesRequest(&apicompat.ResponsesRequest{
		MaxOutputTokens:   &maxOutputTokens,
		Temperature:       &one,
		TopP:              &one,
		ParallelToolCalls: &trueValue,
		ToolChoice:        json.RawMessage(`"none"`),
		Include:           []string{"reasoning.encrypted_content"},
		Store:             &falseValue,
		ServiceTier:       "auto",
		PromptCacheKey:    "cache-hint",
		Reasoning:         &apicompat.ResponsesReasoning{Effort: "high", Summary: "auto"},
		Text:              &apicompat.ResponsesText{Format: json.RawMessage(`{"type":"text"}`), Verbosity: "low"},
	}))
}

func TestValidateOpenAIWebResponsesRequestAcceptsSamplingAndParallelDefaults(t *testing.T) {
	temperature := 0.2
	topP := 0.7
	parallel := false
	req := &apicompat.ResponsesRequest{
		Model:             "auto",
		Input:             json.RawMessage(`"hello"`),
		Temperature:       &temperature,
		TopP:              &topP,
		ParallelToolCalls: &parallel,
	}
	require.NoError(t, ValidateOpenAIWebResponsesRequest(req))
	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	body, err := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{}).BuildConversationPayload(chatReq)
	require.NoError(t, err)
	require.NotContains(t, string(body), "temperature")
	require.NotContains(t, string(body), "top_p")
}

func TestValidateOpenAIWebResponsesRequestRejectsInvalidSamplingParameters(t *testing.T) {
	for _, tc := range []struct {
		name  string
		param string
		value float64
	}{
		{name: "temperature below minimum", param: "temperature", value: -0.1},
		{name: "temperature above maximum", param: "temperature", value: 2.1},
		{name: "top p below minimum", param: "top_p", value: -0.1},
		{name: "top p above maximum", param: "top_p", value: 1.1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &apicompat.ResponsesRequest{Model: "auto", Input: json.RawMessage(`"hello"`)}
			if tc.param == "temperature" {
				req.Temperature = &tc.value
			} else {
				req.TopP = &tc.value
			}
			err := ValidateOpenAIWebResponsesRequest(req)
			var requestErr *OpenAIWebRequestError
			require.ErrorAs(t, err, &requestErr)
			require.Equal(t, tc.param, requestErr.Param)
		})
	}
}

func TestValidateOpenAIWebResponsesRequestRejectsLostParameters(t *testing.T) {
	store := true
	tests := []struct {
		name  string
		param string
		req   *apicompat.ResponsesRequest
	}{
		{name: "invalid reasoning summary", param: "reasoning.summary", req: &apicompat.ResponsesRequest{Reasoning: &apicompat.ResponsesReasoning{Summary: "verbose"}}},
		{name: "invalid verbosity", param: "text.verbosity", req: &apicompat.ResponsesRequest{Text: &apicompat.ResponsesText{Verbosity: "tiny"}}},
		{name: "store", param: "store", req: &apicompat.ResponsesRequest{Store: &store}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOpenAIWebResponsesRequest(tc.req)
			var requestErr *OpenAIWebRequestError
			require.ErrorAs(t, err, &requestErr)
			require.Equal(t, tc.param, requestErr.Param)
		})
	}
}

func TestValidateOpenAIWebResponsesRequestAcceptsPreviousResponseForGatewayStateBridge(t *testing.T) {
	err := ValidateOpenAIWebResponsesRequest(&apicompat.ResponsesRequest{PreviousResponseID: "resp_previous"})
	require.NoError(t, err)
}

func TestValidateOpenAIWebRequestsRejectInvalidTokenLimits(t *testing.T) {
	zero := 0
	for _, tc := range []struct {
		name  string
		param string
		err   error
	}{
		{name: "chat max tokens", param: "max_tokens", err: ValidateOpenAIWebChatCompletionsRequest(&apicompat.ChatCompletionsRequest{MaxTokens: &zero})},
		{name: "chat max completion tokens", param: "max_completion_tokens", err: ValidateOpenAIWebChatCompletionsRequest(&apicompat.ChatCompletionsRequest{MaxCompletionTokens: &zero})},
		{name: "responses max output tokens", param: "max_output_tokens", err: ValidateOpenAIWebResponsesRequest(&apicompat.ResponsesRequest{MaxOutputTokens: &zero})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requestErr *OpenAIWebRequestError
			require.ErrorAs(t, tc.err, &requestErr)
			require.Equal(t, tc.param, requestErr.Param)
		})
	}
}

func TestUsesOpenAIWebProtocolRequiresExplicitOAuthLikeOptIn(t *testing.T) {
	webAccount := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeSetupToken,
		Extra:    map[string]any{OpenAIWebTransportExtraKey: " WEB "},
	}
	require.True(t, UsesOpenAIWebProtocol(webAccount))
	require.False(t, UsesOpenAIWebProtocol(&Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeSetupToken,
		Extra:    map[string]any{OpenAIWebTransportExtraKey: "codex"},
	}))
	require.False(t, UsesOpenAIWebProtocol(&Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra:    map[string]any{OpenAIWebTransportExtraKey: "web"},
	}))
}

func TestOpenAIWebTransportDoHandshakeUsesProxyAndConvertsSSE(t *testing.T) {
	bootstrapResponse := openAIWebTestResponse(http.StatusOK, "text/html", `<html data-build="build-test"><script src="/static/sdk.js"></script></html>`)
	bootstrapResponse.Header.Add("Set-Cookie", "oai-web-session=session-test; Path=/; Secure; HttpOnly")
	upstream := &openAIWebTestUpstream{responses: []*http.Response{
		bootstrapResponse,
		openAIWebTestResponse(http.StatusOK, "application/json", `{"status":"ok","conduit_token":"conduit-test"}`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"prepare_token":"prepare-test"}`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"token":"requirements-test","so_token":"so-test"}`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"status":"ok"}`),
		openAIWebTestResponse(http.StatusOK, "text/event-stream", strings.Join([]string{
			`data: {"conversation_id":"conv-test","o":"append","p":"/message/content/parts/0","v":"OK"}`,
			"",
			`data: {"conversation_id":"conv-test","is_complete":true}`,
			"",
		}, "\n")),
	}}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{BaseURL: "https://web.test"})
	proxyID := int64(7)
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, ProxyID: &proxyID, Proxy: &Proxy{Protocol: "socks5h", Host: "127.0.0.1", Port: 7897}, Concurrency: 2}
	// The wire payload must use the canonical selector while the outward
	// response continues to identify the caller's selected model.
	req := &apicompat.ChatCompletionsRequest{Model: " gpt-5.6-luna ", Messages: []apicompat.ChatMessage{{Role: "user", Content: []byte(`"hello"`)}}}
	resp, err := transport.Do(context.Background(), account, "test-access-token", OpenAIWebConversationOptions{Request: req})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, upstream.requests, 6)
	require.Equal(t, "socks5h://127.0.0.1:7897", upstream.proxies[0])
	require.Equal(t, http.MethodGet, upstream.requests[0].Method)
	require.Equal(t, "u=1, i", upstream.requests[0].Header.Get("Priority"))
	require.Equal(t, openAIWebDefaultSecCHUA, upstream.requests[0].Header.Get("Sec-Ch-Ua"))
	require.Equal(t, "document", upstream.requests[0].Header.Get("Sec-Fetch-Dest"))
	require.Equal(t, "navigate", upstream.requests[0].Header.Get("Sec-Fetch-Mode"))
	require.Equal(t, "none", upstream.requests[0].Header.Get("Sec-Fetch-Site"))
	require.Equal(t, "?1", upstream.requests[0].Header.Get("Sec-Fetch-User"))
	require.Equal(t, "1", upstream.requests[0].Header.Get("Upgrade-Insecure-Requests"))
	require.Equal(t, OpenAIWebConversationPreparePath, upstream.requests[1].URL.Path)
	require.Contains(t, upstream.requests[1].Header.Get("Cookie"), "oai-web-session=session-test")
	require.Equal(t, "no-token", upstream.requests[1].Header.Get("X-Conduit-Token"))
	require.Equal(t, "empty", upstream.requests[1].Header.Get("Sec-Fetch-Dest"))
	require.Equal(t, "cors", upstream.requests[1].Header.Get("Sec-Fetch-Mode"))
	require.Equal(t, "same-origin", upstream.requests[1].Header.Get("Sec-Fetch-Site"))
	prepareBody, readErr := io.ReadAll(upstream.requests[1].Body)
	require.NoError(t, readErr)
	var preparePayload map[string]any
	require.NoError(t, json.Unmarshal(prepareBody, &preparePayload))
	require.Equal(t, "gpt-5.6-luna", preparePayload["model"])
	require.Equal(t, "next", preparePayload["action"])
	require.Equal(t, "none", preparePayload["client_prepare_state"])
	require.Equal(t, "debounced", preparePayload["client_prepare_dispatch"])
	require.NotNil(t, preparePayload["partial_query"])
	require.Equal(t, OpenAIWebRequirementsPath+"/prepare", upstream.requests[2].URL.Path)
	require.Equal(t, OpenAIWebRequirementsPath+"/finalize", upstream.requests[3].URL.Path)
	finalizeBody, readErr := io.ReadAll(upstream.requests[3].Body)
	require.NoError(t, readErr)
	require.JSONEq(t, `{"prepare_token":"prepare-test","proofofwork":"","turnstile":""}`, string(finalizeBody))
	require.Equal(t, OpenAIWebSentinelPingPath, upstream.requests[4].URL.Path)
	require.Equal(t, "requirements-test", upstream.requests[4].Header.Get("OpenAI-Sentinel-Chat-Requirements-Token"))
	require.Equal(t, "prepare-test", upstream.requests[4].Header.Get("OpenAI-Sentinel-Chat-Requirements-Prepare-Token"))
	require.NotEmpty(t, upstream.requests[4].Header.Get("OpenAI-Sentinel-Extra-Data"))
	require.Equal(t, OpenAIWebConversationPath, upstream.requests[5].URL.Path)
	require.NotEmpty(t, upstream.requests[1].Header.Get("X-Oai-Turn-Trace-Id"))
	require.Equal(t, upstream.requests[1].Header.Get("X-Oai-Turn-Trace-Id"), upstream.requests[5].Header.Get("X-Oai-Turn-Trace-Id"))
	require.Contains(t, upstream.requests[5].Header.Get("Cookie"), "oai-web-session=session-test")
	require.Equal(t, "Bearer test-access-token", upstream.requests[5].Header.Get("Authorization"))
	require.Equal(t, "requirements-test", upstream.requests[5].Header.Get("OpenAI-Sentinel-Chat-Requirements-Token"))
	require.Equal(t, "conduit-test", upstream.requests[5].Header.Get("X-Conduit-Token"))
	conversationBody, readErr := io.ReadAll(upstream.requests[5].Body)
	require.NoError(t, readErr)
	var conversationPayload map[string]any
	require.NoError(t, json.Unmarshal(conversationBody, &conversationPayload))
	require.Equal(t, "success", conversationPayload["client_prepare_state"])
	require.Equal(t, "gpt-5.6-luna", conversationPayload["model"])
	converted, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.NoError(t, resp.Body.Close())
	require.Contains(t, string(converted), `"type":"response.output_text.delta"`)
	require.Contains(t, string(converted), `"delta":"OK"`)
	require.Contains(t, string(converted), `"type":"response.completed"`)
	require.Contains(t, string(converted), `"model":"gpt-5.6-luna"`)
}

func TestOpenAIWebTransportBootstrapAcceptsCurrentLargeShell(t *testing.T) {
	const build = "large-build"
	body := `<html data-build="` + build + `"><script src="/sdk.js"></script>` + strings.Repeat("x", (4<<20)+1) + `</html>`
	transport := NewOpenAIWebTransportFromUpstream(&openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "text/html", body),
	}}, OpenAIWebTransportOptions{BaseURL: "https://web.test"})

	bootstrap, err := transport.Bootstrap(context.Background(), &Account{ID: 88}, "token")
	require.NoError(t, err)
	require.Equal(t, build, bootstrap.DataBuild)
	require.Equal(t, []string{"/sdk.js"}, bootstrap.ScriptSources)
}

func TestOpenAIWebTransportAcceptsLargeRequirementsBody(t *testing.T) {
	prepareBody := `{"prepare_token":"prepare-test","padding":"` + strings.Repeat("x", 128<<10) + `"}`
	upstream := &openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "application/json", prepareBody),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"token":"requirements-test"}`),
	}}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{
		BaseURL:       "https://web.test",
		SkipBootstrap: true,
	})

	requirements, err := transport.GetRequirements(context.Background(), &Account{ID: 89}, "token")
	require.NoError(t, err)
	require.Equal(t, "requirements-test", requirements.Token)
}

func TestOpenAIWebTransportRejectsInteractiveChallenge(t *testing.T) {
	upstream := &openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "text/html", "<html></html>"),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"arkose":{"required":true}}`),
	}}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{})
	_, err := transport.GetRequirements(context.Background(), &Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken}, "opaque-test-token")
	var challengeErr *OpenAIWebChallengeError
	require.ErrorAs(t, err, &challengeErr)
	require.Equal(t, "arkose", challengeErr.Kind)
}

func TestOpenAIWebTransportSolvesTurnstileAndForwardsToken(t *testing.T) {
	const dx = "PTJKWEZCSQ8YSBgDBEczSjJKWEZDSQ9QDylDMFBCVVlUR0QvSXZCAUddR0cZCRsUEFcvSXZFAUdfR1ZcO0UjTFlGVQFDcFg0XElaVkVLRCgv"
	const expectedToken = "aGVsbG8gd29ybGQ="

	upstream := &openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "application/json", `{"token":"requirements-test"}`),
	}}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{
		BaseURL:       "https://web.test",
		SkipBootstrap: true,
	})
	upstream.prepareResponse = func(req *http.Request) *http.Response {
		var payload struct {
			P string `json:"p"`
		}
		body, readErr := io.ReadAll(req.Body)
		if readErr != nil || json.Unmarshal(body, &payload) != nil || payload.P == "" {
			return openAIWebTestResponse(http.StatusBadRequest, "application/json", `{"error":"invalid fixture request"}`)
		}
		// Re-envelope a fixed program with the exact p token generated by this
		// request. The program itself is the public, credential-free fixture.
		fixtureRaw, decodeErr := decodeOpenAIWebTurnstileBase64(dx)
		if decodeErr != nil {
			return openAIWebTestResponse(http.StatusInternalServerError, "application/json", `{"error":"invalid fixture"}`)
		}
		fixtureJSON := openAIWebTurnstileXOR(string(fixtureRaw), "fixture-p-token")
		actualDX := base64.StdEncoding.EncodeToString([]byte(openAIWebTurnstileXOR(fixtureJSON, payload.P)))
		return openAIWebTestResponse(http.StatusOK, "application/json", `{"prepare_token":"prepare-test","turnstile":{"required":true,"dx":"`+actualDX+`"}}`)
	}

	requirements, err := transport.GetRequirements(context.Background(), &Account{ID: 90}, "opaque-test-token")
	require.NoError(t, err)
	require.Equal(t, expectedToken, requirements.TurnstileToken)
	require.Len(t, upstream.requests, 2)
	finalizeBody, err := io.ReadAll(upstream.requests[1].Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"prepare_token":"prepare-test","proofofwork":"","turnstile":"`+expectedToken+`"}`, string(finalizeBody))

	request, err := transport.BuildConversationRequest(context.Background(), &Account{ID: 90}, "opaque-test-token", requirements, OpenAIWebConversationOptions{
		Request: &apicompat.ChatCompletionsRequest{Model: "auto", Messages: []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}}},
	})
	require.NoError(t, err)
	require.Equal(t, expectedToken, request.Header.Get("OpenAI-Sentinel-Turnstile-Token"))
}

func TestOpenAIWebSentinelExtraDataUsesPresenceSignals(t *testing.T) {
	raw, err := openAIWebSentinelExtraData(OpenAIWebRequirements{
		Token:          "requirements",
		PrepareToken:   "prepare",
		ProofToken:     "proof",
		TurnstileToken: "turnstile",
	}, openAIWebSentinelPingOptions{
		SequenceNumber: 7,
		Source:         "conversation_heartbeat",
		ConversationID: "conversation",
		LastMessageID:  "message",
	})
	require.NoError(t, err)
	decoded, err := base64.StdEncoding.DecodeString(raw)
	require.NoError(t, err)
	var payload struct {
		V              int            `json:"v"`
		SequenceNumber int            `json:"sequence_number"`
		Signals        map[string]any `json:"signals"`
		ConversationID string         `json:"conversation_id"`
		LastMessageID  string         `json:"last_message_id"`
	}
	require.NoError(t, json.Unmarshal(decoded, &payload))
	require.Equal(t, 1, payload.V)
	require.Equal(t, 7, payload.SequenceNumber)
	require.Equal(t, "conversation", payload.ConversationID)
	require.Equal(t, "message", payload.LastMessageID)
	require.Equal(t, "conversation_heartbeat", payload.Signals["ping_source"])
	require.Equal(t, "1", payload.Signals["proof_token_present"])
	require.Equal(t, "0", payload.Signals["so_token_present"])
}

func TestOpenAIWebConversationPreparePayloadCarriesAttachmentMIMEs(t *testing.T) {
	body := []byte(`{"action":"next","model":"auto","parent_message_id":"client-created-root","timezone":"Asia/Shanghai","timezone_offset_min":-480,"conversation_mode":{"kind":"primary_assistant"},"system_hints":[],"messages":[{"id":"message","metadata":{"attachments":[{"mimeType":"application/pdf"},{"mime_type":"text/plain"}]}}]}`)
	prepared, err := openAIWebConversationPreparePayload(body)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(prepared, &payload))
	require.Equal(t, "success", payload["client_prepare_state"])
	require.Equal(t, "immediate", payload["client_prepare_dispatch"])
	require.Equal(t, "file_picker", payload["client_prepare_source"])
	require.Equal(t, []any{"application/pdf", "text/plain"}, payload["attachment_mime_types"])
	require.NotContains(t, string(prepared), "conversation_origin")
	require.NotContains(t, string(prepared), "model_response_contracts")
	require.NotContains(t, string(prepared), "partial_query")
}

func TestOpenAIWebConversationPreparePayloadCarriesWorkModeFields(t *testing.T) {
	body := []byte(`{"action":"next","model":"gpt-5.6-sol-wm","parent_message_id":"root","timezone":"Asia/Shanghai","timezone_offset_min":-480,"conversation_mode":{"kind":"primary_assistant"},"system_hints":[],"thinking_effort":"min","messages":[{"id":"message","metadata":{}}]}`)
	prepared, err := openAIWebConversationPreparePayload(body)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(prepared, &payload))
	require.Equal(t, "tpp", payload["conversation_origin"])
	require.Equal(t, "min", payload["thinking_effort"])
	contracts, ok := payload["model_response_contracts"].([]any)
	require.True(t, ok)
	require.Len(t, contracts, 1)
	require.Equal(t, "photo_upload_action.v1", contracts[0].(map[string]any)["id"])
	require.NotContains(t, payload, "partial_query")
}

func TestOpenAIWebConversationPreparePayloadOmitsPartialQueryOnContinuation(t *testing.T) {
	body := []byte(`{"action":"next","model":"auto","conversation_id":"conversation-test","parent_message_id":"parent-test","timezone":"Asia/Shanghai","timezone_offset_min":-480,"conversation_mode":{"kind":"primary_assistant"},"system_hints":[],"messages":[{"id":"message","metadata":{}}]}`)
	prepared, err := openAIWebConversationPreparePayload(body)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(prepared, &payload))
	require.Equal(t, "conversation-test", payload["conversation_id"])
	require.NotContains(t, payload, "partial_query")
}

func TestOpenAIWebTransportRejectsMissingTurnstileProgramWithoutLeakingToken(t *testing.T) {
	const accessToken = "opaque-turnstile-access-token"
	upstream := &openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "application/json", `{"prepare_token":"prepare-test","turnstile":{"required":true}}`),
	}}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{
		BaseURL:       "https://web.test",
		SkipBootstrap: true,
	})
	_, err := transport.GetRequirements(context.Background(), &Account{ID: 91}, accessToken)
	require.EqualError(t, err, "ChatGPT web turnstile challenge: turnstile challenge payload is empty")
	require.NotContains(t, err.Error(), accessToken)
}

func TestOpenAIWebTransportContinuesWhenTurnstileProgramHasNoOutput(t *testing.T) {
	// This matches the reference client's `solve(...) or ""` behavior for a
	// newer sensor program that does not emit a browser token.
	upstream := &openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "application/json", `{"token":"requirements-test"}`),
	}}
	upstream.prepareResponse = func(req *http.Request) *http.Response {
		var payload struct {
			P string `json:"p"`
		}
		body, readErr := io.ReadAll(req.Body)
		if readErr != nil || json.Unmarshal(body, &payload) != nil || payload.P == "" {
			return openAIWebTestResponse(http.StatusBadRequest, "application/json", `{"error":"invalid fixture request"}`)
		}
		program, marshalErr := json.Marshal([]any{[]any{2, float64(30), "window"}})
		if marshalErr != nil {
			return openAIWebTestResponse(http.StatusInternalServerError, "application/json", `{"error":"invalid fixture"}`)
		}
		envelope := openAIWebTurnstileXOR(string(program), payload.P)
		dx := base64.StdEncoding.EncodeToString([]byte(envelope))
		return openAIWebTestResponse(http.StatusOK, "application/json", `{"prepare_token":"prepare-test","turnstile":{"required":true,"dx":"`+dx+`"}}`)
	}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{
		BaseURL:       "https://web.test",
		SkipBootstrap: true,
	})
	requirements, err := transport.GetRequirements(context.Background(), &Account{ID: 92}, "opaque-test-token")
	require.NoError(t, err)
	require.Equal(t, "requirements-test", requirements.Token)
	require.Empty(t, requirements.TurnstileToken)
}

func TestOpenAIWebTransportRedactsExactTokenFromHTTPError(t *testing.T) {
	const accessToken = "opaque-test-access-token"
	err := webHTTPError("/test", http.StatusUnauthorized, []byte(`{"message":"token opaque-test-access-token is invalid"}`), accessToken)
	require.NotContains(t, err.Error(), accessToken)
	require.Contains(t, err.Error(), "<redacted-token>")
}

func TestConvertOpenAIWebConversationSSECumulativePatch(t *testing.T) {
	body := []byte(strings.Join([]string{
		`data: {"o":"append","p":"/message/content/parts/0","v":"hel"}`,
		"",
		`data: {"o":"patch","p":"","v":{"message":{"content":{"parts":["hello"]}}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n"))
	converted, err := ConvertOpenAIWebConversationSSE(body, "auto")
	require.NoError(t, err)
	require.Contains(t, string(converted), `"delta":"hel"`)
	require.Contains(t, string(converted), `"delta":"lo"`)
	require.NotContains(t, string(converted), `"delta":"hello"`)
	require.Contains(t, string(converted), `"text":"hello"`)
}

func TestConvertOpenAIWebConversationSSEIgnoresNonAssistantMessage(t *testing.T) {
	body := []byte(strings.Join([]string{
		`data: {"message":{"author":{"role":"user"},"content":{"parts":["do not echo"]}}}`,
		"",
		`data: {"message":{"author":{"role":"assistant"},"content":{"parts":["safe"]}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n"))
	converted, err := ConvertOpenAIWebConversationSSE(body, "auto")
	require.NoError(t, err)
	require.NotContains(t, string(converted), "do not echo")
	require.Contains(t, string(converted), `"delta":"safe"`)
}

func TestBuildOpenAIWebProofTokenEasyDifficulty(t *testing.T) {
	proof, err := BuildOpenAIWebProofToken("seed", "ff", "ua", nil, "", 1)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(proof, "gAAAAAB"))
	_, err = BuildOpenAIWebProofToken("seed", "zz", "ua", nil, "", 1)
	require.Error(t, err)
}

func TestBuildOpenAIWebLegacyRequirementsTokenUsesReferenceBrowserFlag(t *testing.T) {
	const prefix = "gAAAAAC"
	token := BuildOpenAIWebLegacyRequirementsToken("test-user-agent", nil, "build-test")
	require.True(t, strings.HasPrefix(token, prefix))

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(token, prefix))
	require.NoError(t, err)
	var config []any
	require.NoError(t, json.Unmarshal(raw, &config))
	require.GreaterOrEqual(t, len(config), 25)
	// The Python reference build_pow_config emits 1 at index 3. Keep this
	// assertion close to the encoder so a protocol drift cannot go unnoticed.
	flag, ok := config[3].(float64)
	require.True(t, ok)
	require.Equal(t, float64(1), flag)
}

func TestOpenAIWebTransportBootstrapCacheIsolatedByAccountAndToken(t *testing.T) {
	upstream := &openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "text/html", `<html data-build="build-a"><script src="/a.js"></script></html>`),
		openAIWebTestResponse(http.StatusOK, "text/html", `<html data-build="build-b"><script src="/b.js"></script></html>`),
		openAIWebTestResponse(http.StatusOK, "text/html", `<html data-build="build-c"><script src="/c.js"></script></html>`),
	}}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{BaseURL: "https://web.test"})
	accountA := &Account{ID: 101, Platform: PlatformOpenAI, Type: AccountTypeSetupToken}
	accountB := &Account{ID: 202, Platform: PlatformOpenAI, Type: AccountTypeSetupToken}

	first, err := transport.Bootstrap(context.Background(), accountA, "token-a")
	require.NoError(t, err)
	require.Equal(t, "build-a", first.DataBuild)
	// A caller must not be able to mutate the cached slice through the return
	// value.
	first.ScriptSources[0] = "/mutated.js"
	repeated, err := transport.Bootstrap(context.Background(), accountA, "token-a")
	require.NoError(t, err)
	require.Equal(t, "/a.js", repeated.ScriptSources[0])

	otherAccount, err := transport.Bootstrap(context.Background(), accountB, "token-a")
	require.NoError(t, err)
	require.Equal(t, "build-b", otherAccount.DataBuild)
	otherToken, err := transport.Bootstrap(context.Background(), accountA, "token-c")
	require.NoError(t, err)
	require.Equal(t, "build-c", otherToken.DataBuild)
	require.Len(t, upstream.requests, 3)
}

func TestOpenAIWebTransportHandlesNilAndNon2xxResponses(t *testing.T) {
	transport := NewOpenAIWebTransportFromUpstream(&openAIWebTestUpstream{responses: []*http.Response{nil}}, OpenAIWebTransportOptions{BaseURL: "https://web.test"})
	_, err := transport.Bootstrap(context.Background(), &Account{ID: 1}, "token")
	require.EqualError(t, err, "ChatGPT web bootstrap returned no response")

	transport = NewOpenAIWebTransportFromUpstream(&openAIWebTestUpstream{responses: []*http.Response{
		{StatusCode: http.StatusUnauthorized},
	}}, OpenAIWebTransportOptions{BaseURL: "https://web.test"})
	_, err = transport.Bootstrap(context.Background(), &Account{ID: 1}, "secret-token")
	var httpErr *OpenAIWebHTTPError
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusUnauthorized, httpErr.StatusCode)
	require.NotContains(t, err.Error(), "secret-token")

	transport = NewOpenAIWebTransportFromUpstream(&openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "text/html", "<html></html>"),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"conduit_token":"conduit-test"}`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"prepare_token":"p"}`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"token":"r"}`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"status":"ok"}`),
		openAIWebTestResponse(http.StatusBadGateway, "application/json", `{"message":"bad gateway"}`),
	}}, OpenAIWebTransportOptions{BaseURL: "https://web.test"})
	_, err = transport.Do(context.Background(), &Account{ID: 1}, "secret-token", OpenAIWebConversationOptions{
		Request: &apicompat.ChatCompletionsRequest{Model: "auto", Messages: []apicompat.ChatMessage{{Role: "user", Content: []byte(`"hi"`)}}},
	})
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusBadGateway, httpErr.StatusCode)

	transport = NewOpenAIWebTransportFromUpstream(&openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "text/html", "<html></html>"),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"conduit_token":"conduit-test"}`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"prepare_token":"p"}`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"token":"r"}`),
		openAIWebTestResponse(http.StatusOK, "application/json", `{"status":"ok"}`),
		{StatusCode: http.StatusOK},
	}}, OpenAIWebTransportOptions{BaseURL: "https://web.test"})
	_, err = transport.Do(context.Background(), &Account{ID: 1}, "secret-token", OpenAIWebConversationOptions{
		Request: &apicompat.ChatCompletionsRequest{Model: "auto", Messages: []apicompat.ChatMessage{{Role: "user", Content: []byte(`"hi"`)}}},
	})
	require.EqualError(t, err, "ChatGPT web conversation returned no response body")

	_, err = readAndCloseWebBody(&http.Response{StatusCode: http.StatusOK}, 128)
	require.EqualError(t, err, "nil web response body")
}

func TestConvertOpenAIWebConversationSSEPreservesUsageAndTerminalOrdering(t *testing.T) {
	body := []byte(strings.Join([]string{
		`data: {"o":"append","p":"/message/content/parts/0","v":"OK"}`,
		"",
		`data: {"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
		"",
		`data: {"is_complete":true,"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n"))
	converted, err := ConvertOpenAIWebConversationSSE(body, "auto")
	require.NoError(t, err)
	output := string(converted)
	require.Equal(t, 1, strings.Count(output, `"type":"response.completed"`))
	require.Contains(t, output, `"usage":{"completion_tokens":2,"prompt_tokens":3}`)
	completedIndex := strings.Index(output, `"type":"response.completed"`)
	doneIndex := strings.Index(output, "data: [DONE]")
	require.GreaterOrEqual(t, completedIndex, 0)
	require.Greater(t, doneIndex, completedIndex)
}

func TestConvertOpenAIWebConversationSSEFailureDoesNotEmitCompleted(t *testing.T) {
	body := []byte(strings.Join([]string{
		`data: {"error":"upstream rejected request"}`,
		"",
		"data: [DONE]",
		"",
	}, "\n"))
	converted, err := ConvertOpenAIWebConversationSSE(body, "auto")
	require.NoError(t, err)
	output := string(converted)
	require.Contains(t, output, `"type":"response.failed"`)
	require.NotContains(t, output, `"type":"response.completed"`)
	require.True(t, strings.HasSuffix(output, "data: [DONE]\n\n"))
}

func TestConvertOpenAIWebConversationSSEEmptyResponseFails(t *testing.T) {
	converted, err := ConvertOpenAIWebConversationSSE([]byte("data: [DONE]\n\n"), "auto")
	require.NoError(t, err)
	output := string(converted)
	require.Contains(t, output, `"code":"upstream_empty_response"`)
	require.Contains(t, output, `"type":"response.failed"`)
	require.NotContains(t, output, `"type":"response.completed"`)
}

func TestConvertOpenAIWebConversationSSEEOFIsIncomplete(t *testing.T) {
	// No blank line or [DONE] follows this delta. The adapter must not claim a
	// completed turn merely because partial text was received.
	converted, err := ConvertOpenAIWebConversationSSE([]byte(`data: {"o":"append","p":"/message/content/parts/0","v":"partial"}`), "auto")
	require.NoError(t, err)
	output := string(converted)
	require.Contains(t, output, `"type":"response.incomplete"`)
	require.NotContains(t, output, `"type":"response.completed"`)
	require.Contains(t, output, `"status":"incomplete"`)
}

func TestOpenAIWebAttachmentUploadBuildsMetadataOnlyFileMessage(t *testing.T) {
	const uploadURL = "https://uploads.test/blob?sig=anonymous"
	upstream := &openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "application/json", `{"file_id":"file_pdf_test","library_file_id":"libfile_pdf_test","upload_url":"`+uploadURL+`"}`),
		openAIWebTestResponse(http.StatusCreated, "text/plain", ""),
		openAIWebTestResponse(http.StatusOK, "text/event-stream", strings.Join([]string{
			`{"file_id":"file_pdf_test","event":"file.processing.started","progress":0.0}`,
			`{"file_id":"file_pdf_test","event":"file.processing.file_ready","progress":100.0}`,
			`{"file_id":"file_pdf_test","event":"file.processing.completed","progress":100.0,"extra":{"metadata_object_id":"libfile_pdf_processed"}}`,
		}, "\n")),
	}}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{BaseURL: "https://web.test"})
	dataURI := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString([]byte("pdf-bytes"))
	req := &apicompat.ChatCompletionsRequest{
		Model: "auto",
		Messages: []apicompat.ChatMessage{{
			Role:    "user",
			Content: []byte(`[{"type":"text","text":"summarize"},{"type":"file","file":{"filename":"report.pdf","file_data":"` + dataURI + `"}}]`),
		}},
	}
	conversationReq, err := transport.BuildConversationRequest(context.Background(), &Account{ID: 91, Platform: PlatformOpenAI, Type: AccountTypeSetupToken}, "test-access-token", OpenAIWebRequirements{Token: "requirements-test"}, OpenAIWebConversationOptions{Request: req})
	require.NoError(t, err)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, http.MethodPost, upstream.requests[0].Method)
	require.Equal(t, "/backend-api/files", upstream.requests[0].URL.Path)
	require.Equal(t, http.MethodPut, upstream.requests[1].Method)
	require.Equal(t, uploadURL, upstream.requests[1].URL.String())
	require.Empty(t, upstream.requests[1].Header.Get("Authorization"))
	require.Equal(t, "BlockBlob", upstream.requests[1].Header.Get("x-ms-blob-type"))
	require.Equal(t, "2020-04-08", upstream.requests[1].Header.Get("x-ms-version"))
	require.Equal(t, "application/pdf", upstream.requests[1].Header.Get("Content-Type"))
	require.Equal(t, http.MethodPost, upstream.requests[2].Method)
	require.Equal(t, "/backend-api/files/process_upload_stream", upstream.requests[2].URL.Path)

	var metadata map[string]any
	metadataBody, readErr := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, readErr)
	require.NoError(t, json.Unmarshal(metadataBody, &metadata))
	require.Equal(t, "report.pdf", metadata["file_name"])
	require.Equal(t, float64(len("pdf-bytes")), metadata["file_size"])
	require.Equal(t, "ace_upload", metadata["use_case"])
	require.Equal(t, true, metadata["supports_direct_azure_multipart"])
	require.Equal(t, "chat_composer", metadata["entry_surface"])
	require.Equal(t, "file_picker", metadata["selection_method"])
	require.Equal(t, "none", metadata["mime_resolution_source"])
	require.Equal(t, float64(-480), metadata["timezone_offset_min"])
	require.Equal(t, false, metadata["reset_rate_limits"])
	require.Equal(t, true, metadata["store_in_library"])
	require.Equal(t, "opportunistic", metadata["library_persistence_mode"])

	var processPayload map[string]any
	processBody, readErr := io.ReadAll(upstream.requests[2].Body)
	require.NoError(t, readErr)
	require.NoError(t, json.Unmarshal(processBody, &processPayload))
	require.Equal(t, "file_pdf_test", processPayload["file_id"])
	require.Equal(t, "ace_upload", processPayload["use_case"])
	require.Equal(t, false, processPayload["index_for_retrieval"])
	require.Equal(t, "report.pdf", processPayload["file_name"])
	require.Equal(t, "chat_composer", processPayload["entry_surface"])
	processMetadata := processPayload["metadata"].(map[string]any)
	require.Equal(t, true, processMetadata["store_in_library"])
	require.Equal(t, false, processMetadata["is_temporary_chat"])

	conversationBody, readErr := io.ReadAll(conversationReq.Body)
	require.NoError(t, readErr)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(conversationBody, &payload))
	messages, ok := payload["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 1)
	message, ok := messages[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "text", message["content"].(map[string]any)["content_type"])
	parts := message["content"].(map[string]any)["parts"].([]any)
	require.Equal(t, []any{"summarize"}, parts)
	attachments := message["metadata"].(map[string]any)["attachments"].([]any)
	require.Len(t, attachments, 1)
	attachment := attachments[0].(map[string]any)
	require.Equal(t, "file_pdf_test", attachment["id"])
	require.Equal(t, "application/pdf", attachment["mime_type"])
	require.Equal(t, "report.pdf", attachment["name"])
	require.Equal(t, "local", attachment["source"])
	require.Equal(t, "libfile_pdf_processed", attachment["library_file_id"])
	require.NotContains(t, attachment, "width")
	require.NotContains(t, attachment, "height")
}

func TestOpenAIWebAttachmentExistingFileIDSkipsUpload(t *testing.T) {
	transport := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{BaseURL: "https://web.test"})
	req := &apicompat.ChatCompletionsRequest{
		Model: "auto",
		Messages: []apicompat.ChatMessage{{
			Role:    "user",
			Content: []byte(`[{"type":"input_file","file_id":"file_existing","filename":"existing.pdf","mime_type":"application/pdf","file_data":"data:application/pdf;base64,cGRm"}]`),
		}},
	}
	conversationReq, err := transport.BuildConversationRequest(context.Background(), nil, "test-access-token", OpenAIWebRequirements{Token: "requirements-test"}, OpenAIWebConversationOptions{Request: req})
	require.NoError(t, err)
	conversationBody, readErr := io.ReadAll(conversationReq.Body)
	require.NoError(t, readErr)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(conversationBody, &payload))
	message := payload["messages"].([]any)[0].(map[string]any)
	content := message["content"].(map[string]any)
	require.Equal(t, "text", content["content_type"])
	require.Equal(t, []any{""}, content["parts"])
	attachment := message["metadata"].(map[string]any)["attachments"].([]any)[0].(map[string]any)
	require.Equal(t, "application/pdf", attachment["mime_type"])
	require.Equal(t, "existing.pdf", attachment["name"])
	require.NotContains(t, attachment, "source")
}

func TestNormalizeOpenAIWebFileIDAcceptsSupportedPointerSchemes(t *testing.T) {
	for _, value := range []string{"file-service://file_existing", "sediment://file_existing"} {
		fileID, err := normalizeOpenAIWebFileID(value)
		require.NoError(t, err)
		require.Equal(t, "file_existing", fileID)
	}
}

func TestOpenAIWebImageDataURIUploadIncludesDimensions(t *testing.T) {
	upstream := &openAIWebTestUpstream{responses: []*http.Response{
		openAIWebTestResponse(http.StatusOK, "application/json", `{"file_id":"file_image_test","upload_url":"https://uploads.test/image"}`),
		openAIWebTestResponse(http.StatusCreated, "text/plain", ""),
		openAIWebTestResponse(http.StatusOK, "text/event-stream", `{"file_id":"file_image_test","event":"file.processing.completed","progress":100.0,"extra":{"metadata_object_id":"libfile_image_processed"}}`),
	}}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{BaseURL: "https://web.test"})
	const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	req := &apicompat.ChatCompletionsRequest{
		Model: "auto",
		Messages: []apicompat.ChatMessage{{
			Role:    "user",
			Content: []byte(`[{"type":"input_image","image_url":"data:image/png;base64,` + onePixelPNG + `"}]`),
		}},
	}
	conversationReq, err := transport.BuildConversationRequest(context.Background(), &Account{ID: 92, Platform: PlatformOpenAI, Type: AccountTypeSetupToken}, "test-access-token", OpenAIWebRequirements{Token: "requirements-test"}, OpenAIWebConversationOptions{Request: req})
	require.NoError(t, err)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "image/png", upstream.requests[1].Header.Get("Content-Type"))
	metadataBody, readErr := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, readErr)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal(metadataBody, &metadata))
	require.Equal(t, float64(1), metadata["width"])
	require.Equal(t, float64(1), metadata["height"])

	conversationBody, readErr := io.ReadAll(conversationReq.Body)
	require.NoError(t, readErr)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(conversationBody, &payload))
	message := payload["messages"].([]any)[0].(map[string]any)
	pointer := message["content"].(map[string]any)["parts"].([]any)[0].(map[string]any)
	require.Equal(t, float64(1), pointer["width"])
	require.Equal(t, float64(1), pointer["height"])
	require.Greater(t, pointer["size_bytes"].(float64), float64(0))
	attachment := message["metadata"].(map[string]any)["attachments"].([]any)[0].(map[string]any)
	require.Equal(t, "local", attachment["source"])
	require.Equal(t, "libfile_image_processed", attachment["library_file_id"])
}

func TestParseOpenAIWebUploadProcessStreamAcceptsRawAndSSELines(t *testing.T) {
	body := []byte(strings.Join([]string{
		`data: {"file_id":"file_pdf","event":"file.processing.started"}`,
		`{"file_id":"file_pdf","event":"file.processing.completed","extra":{"metadata_object_id":"libfile_pdf"}}`,
		"data: [DONE]",
	}, "\n"))
	libraryID, err := parseOpenAIWebUploadProcessStream(body, "file_pdf")
	require.NoError(t, err)
	require.Equal(t, "libfile_pdf", libraryID)
}

func TestParseOpenAIWebUploadProcessStreamRejectsIncompleteOrFailedStream(t *testing.T) {
	_, err := parseOpenAIWebUploadProcessStream([]byte(`{"file_id":"file_pdf","event":"file.processing.file_ready"}`), "file_pdf")
	require.EqualError(t, err, "ChatGPT web attachment processing ended before completion")

	_, err = parseOpenAIWebUploadProcessStream([]byte(`{"file_id":"file_pdf","event":"file.processing.failed","message":"unsupported format"}`), "file_pdf")
	require.EqualError(t, err, "unsupported format")
}

func TestParseOpenAIWebUploadProcessStreamAllowsPlainStatusResponse(t *testing.T) {
	libraryID, err := parseOpenAIWebUploadProcessStream([]byte(`{"status":"success"}`), "file_pdf")
	require.NoError(t, err)
	require.Empty(t, libraryID)
}

func TestOpenAIWebFileAttachmentUsesMetadataOnlyForCommonMIMEs(t *testing.T) {
	cases := []struct {
		name     string
		fileID   string
		mime     string
		filename string
	}{
		{name: "pdf", fileID: "file_pdf", mime: "application/pdf", filename: "report.pdf"},
		{name: "text", fileID: "file_text", mime: "text/plain", filename: "notes.txt"},
		{name: "zip", fileID: "file_zip", mime: "application/zip", filename: "archive.zip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{BaseURL: "https://web.test"})
			req := &apicompat.ChatCompletionsRequest{
				Model: "auto",
				Messages: []apicompat.ChatMessage{{
					Role:    "user",
					Content: []byte(`[{"type":"file","file":{"file_id":"` + tc.fileID + `","filename":"` + tc.filename + `","mime_type":"` + tc.mime + `"}}]`),
				}},
			}
			conversationReq, err := transport.BuildConversationRequest(context.Background(), nil, "test-access-token", OpenAIWebRequirements{Token: "requirements-test"}, OpenAIWebConversationOptions{Request: req})
			require.NoError(t, err)
			conversationBody, readErr := io.ReadAll(conversationReq.Body)
			require.NoError(t, readErr)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(conversationBody, &payload))
			message := payload["messages"].([]any)[0].(map[string]any)
			content := message["content"].(map[string]any)
			require.Equal(t, "text", content["content_type"])
			require.Equal(t, []any{""}, content["parts"])
			attachment := message["metadata"].(map[string]any)["attachments"].([]any)[0].(map[string]any)
			require.Equal(t, tc.mime, attachment["mime_type"])
			require.Equal(t, tc.filename, attachment["name"])
		})
	}
}

func TestOpenAIWebAttachmentRejectsRemoteURL(t *testing.T) {
	transport := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{BaseURL: "https://web.test"})
	req := &apicompat.ChatCompletionsRequest{
		Model: "auto",
		Messages: []apicompat.ChatMessage{{
			Role:    "user",
			Content: []byte(`[{"type":"image_url","image_url":{"url":"https://example.test/image.png"}}]`),
		}},
	}
	_, err := transport.BuildConversationRequest(context.Background(), nil, "test-access-token", OpenAIWebRequirements{Token: "requirements-test"}, OpenAIWebConversationOptions{Request: req})
	require.EqualError(t, err, "ChatGPT web transport does not support remote attachment URLs")
}

func TestOpenAIWebResponsesBodyCapturesPrivateConversationCursor(t *testing.T) {
	source := io.NopCloser(strings.NewReader(strings.Join([]string{
		`data: {"conversation_id":"conv-web-1","message":{"id":"msg-web-1","author":{"role":"assistant"},"content":{"parts":["hello"]}}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")))
	body := NewOpenAIWebResponsesBody(source, "auto")
	_, err := io.ReadAll(body)
	require.NoError(t, err)
	provider, ok := body.(OpenAIWebConversationStateProvider)
	require.True(t, ok)
	conversationID, parentMessageID := provider.OpenAIWebConversationState()
	require.Equal(t, "conv-web-1", conversationID)
	require.Equal(t, "msg-web-1", parentMessageID)
}
