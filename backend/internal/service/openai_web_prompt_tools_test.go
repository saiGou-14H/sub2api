package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWebPromptToolsNormalizesStrictFunctionSchema(t *testing.T) {
	req := &apicompat.ChatCompletionsRequest{
		Model: "auto",
		Tools: []apicompat.ChatTool{{Type: "function", Function: &apicompat.ChatFunction{
			Name:       "lookup",
			Parameters: json.RawMessage(`{"properties":{"city":{"type":"string"}},"required":["city"]}`),
		}}},
	}
	prompt, err := NewOpenAIWebPromptToolsFromChatRequest(req)
	require.NoError(t, err)
	require.NotNil(t, prompt)
	require.Len(t, prompt.Tools, 1)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(prompt.Tools[0].Parameters, &schema))
	require.Equal(t, "object", schema["type"])
	require.Equal(t, false, schema["additionalProperties"])
	require.NotEmpty(t, prompt.Nonce)
	require.Len(t, prompt.SchemaHash, 24)
}

func TestOpenAIWebPromptToolsRejectsUnsafeSchemas(t *testing.T) {
	for name, schema := range map[string]string{
		"additional properties": `{"type":"object","additionalProperties":true}`,
		"unknown required":      `{"type":"object","properties":{},"required":["missing"]}`,
		"non object root":       `{"type":"string"}`,
		"invalid pattern":       `{"type":"object","properties":{"x":{"type":"string","pattern":"(?=x)"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewOpenAIWebPromptToolsFromChatRequest(&apicompat.ChatCompletionsRequest{
				Model: "auto",
				Tools: []apicompat.ChatTool{{Type: "function", Function: &apicompat.ChatFunction{Name: "bad", Parameters: json.RawMessage(schema)}}},
			})
			require.Error(t, err)
		})
	}
}

func TestOpenAIWebPromptToolsSupportsNativeToolFamilies(t *testing.T) {
	native := []string{"web_search", "web_search_preview", "x_search", "file_search", "code_interpreter", "shell", "local_shell", "computer_use", "image_generation", "remote_mcp", "mcp", "skills", "tool_search", "programmatic_tool_call"}
	tools := make([]apicompat.ChatTool, 0, len(native))
	for _, typ := range native {
		tools = append(tools, apicompat.ChatTool{Type: typ})
	}
	prompt, err := NewOpenAIWebPromptToolsFromChatRequest(&apicompat.ChatCompletionsRequest{Model: "auto", Tools: tools})
	require.NoError(t, err)
	require.Len(t, prompt.Tools, len(native))
	for _, tool := range prompt.Tools {
		require.True(t, strings.HasPrefix(tool.Name, "__sub2api_"))
		require.NotEmpty(t, tool.Parameters)
	}
}

func TestOpenAIWebPromptToolsResponsesChoiceMapsNamespacesAndNativeTypes(t *testing.T) {
	namespaceReq := &apicompat.ResponsesRequest{
		Model: "auto",
		Tools: []apicompat.ResponsesTool{{
			Type: "namespace", Name: "collaboration",
			Tools: []apicompat.ResponsesTool{{Type: "function", Name: "send", Parameters: json.RawMessage(`{"type":"object"}`)}},
		}},
		ToolChoice: json.RawMessage(`{"type":"function","namespace":"collaboration","name":"send"}`),
	}
	prompt, err := NewOpenAIWebPromptToolsFromResponsesRequest(namespaceReq)
	require.NoError(t, err)
	require.Equal(t, "named", prompt.Choice)
	require.Equal(t, "collaboration__send", prompt.ChoiceName)

	nativeReq := &apicompat.ResponsesRequest{
		Model:      "auto",
		Tools:      []apicompat.ResponsesTool{{Type: "web_search"}},
		ToolChoice: json.RawMessage(`{"type":"web_search"}`),
	}
	nativePrompt, err := NewOpenAIWebPromptToolsFromResponsesRequest(nativeReq)
	require.NoError(t, err)
	require.Equal(t, "named", nativePrompt.Choice)
	require.Equal(t, "__sub2api_web_search", nativePrompt.ChoiceName)
}

func TestSettingServiceOpenAIWebPromptToolsIsFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		err    error
		want   bool
	}{
		{name: "missing", values: map[string]string{}, want: false},
		{name: "enabled", values: map[string]string{SettingKeyEnableOpenAIWebPromptTools: "true"}, want: true},
		{name: "case and whitespace are accepted", values: map[string]string{SettingKeyEnableOpenAIWebPromptTools: " TRUE "}, want: true},
		{name: "other value", values: map[string]string{SettingKeyEnableOpenAIWebPromptTools: "1"}, want: false},
		{name: "repository error", values: map[string]string{}, err: errors.New("unavailable"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &panelRateLimitSettingRepo{values: tt.values, getValueErr: tt.err}
			svc := NewSettingService(repo, &config.Config{})
			require.Equal(t, tt.want, svc.IsOpenAIWebPromptToolsEnabled(context.Background()))
		})
	}
}

func TestOpenAIWebPromptToolsParseResponseValidatesNonceChoiceAndArguments(t *testing.T) {
	required := true
	prompt, err := NewOpenAIWebPromptToolsFromChatRequest(&apicompat.ChatCompletionsRequest{
		Model: "auto",
		Tools: []apicompat.ChatTool{{Type: "function", Function: &apicompat.ChatFunction{
			Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`), Strict: &required,
		}}},
		ToolChoice: json.RawMessage(`"required"`),
	})
	require.NoError(t, err)
	good, _ := json.Marshal(map[string]any{
		"protocol": prompt.Protocol, "nonce": prompt.Nonce, "schema_hash": prompt.SchemaHash,
		"calls": []any{map[string]any{"name": "lookup", "type": "function", "arguments": map[string]any{"city": "Shanghai"}}},
	})
	calls, recognized, err := prompt.ParseResponse(string(good))
	require.NoError(t, err)
	require.True(t, recognized)
	require.Len(t, calls, 1)
	require.JSONEq(t, `{"city":"Shanghai"}`, string(calls[0].Arguments))

	badNonce := bytes.Replace(good, []byte(prompt.Nonce), []byte("wrong"), 1)
	_, recognized, err = prompt.ParseResponse(string(badNonce))
	require.Error(t, err)
	require.True(t, recognized)

	badArgs, _ := json.Marshal(map[string]any{
		"protocol": prompt.Protocol, "nonce": prompt.Nonce, "schema_hash": prompt.SchemaHash,
		"calls": []any{map[string]any{"name": "lookup", "arguments": map[string]any{"city": 42}}},
	})
	_, _, err = prompt.ParseResponse(string(badArgs))
	require.Error(t, err)
}

func TestOpenAIWebPromptToolsReaderEmitsStandardFunctionCallEvents(t *testing.T) {
	prompt, err := NewOpenAIWebPromptToolsFromChatRequest(&apicompat.ChatCompletionsRequest{
		Model: "auto",
		Tools: []apicompat.ChatTool{{Type: "function", Function: &apicompat.ChatFunction{
			Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}}},
	})
	require.NoError(t, err)
	envelope, _ := json.Marshal(map[string]any{
		"protocol": prompt.Protocol, "nonce": prompt.Nonce, "schema_hash": prompt.SchemaHash,
		"calls": []any{map[string]any{"name": "lookup", "arguments": map[string]any{"city": "Shanghai"}}},
	})
	sse := "data: {\"conversation_id\":\"conv\",\"o\":\"append\",\"p\":\"/message/content/parts/0\",\"v\":" + mustPromptJSONString(string(envelope)) + "}\n\ndata: {\"conversation_id\":\"conv\",\"is_complete\":true}\n\n"
	reader := newOpenAIWebResponsesBodyWithPromptTools(io.NopCloser(strings.NewReader(sse)), "auto", nil, prompt)
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"type":"response.function_call_arguments.delta"`)
	require.Contains(t, string(raw), `"type":"response.completed"`)
	require.Contains(t, string(raw), `"type":"function_call"`)
	require.NotContains(t, string(raw), prompt.Nonce)
}

func TestOpenAIWebPromptToolsReaderRejectsPlainTextForRequiredChoice(t *testing.T) {
	prompt, err := NewOpenAIWebPromptToolsFromChatRequest(&apicompat.ChatCompletionsRequest{
		Model:      "auto",
		ToolChoice: json.RawMessage(`"required"`),
		Tools: []apicompat.ChatTool{{Type: "function", Function: &apicompat.ChatFunction{
			Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`),
		}}},
	})
	require.NoError(t, err)
	sse := "data: {\"conversation_id\":\"conv\",\"o\":\"append\",\"p\":\"/message/content/parts/0\",\"v\":\"plain answer\"}\n\ndata: {\"conversation_id\":\"conv\",\"is_complete\":true}\n\n"
	reader := newOpenAIWebResponsesBodyWithPromptTools(io.NopCloser(strings.NewReader(sse)), "auto", nil, prompt)
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"type":"response.failed"`)
	require.Contains(t, string(raw), `"code":"tool_protocol_error"`)
	require.NotContains(t, string(raw), `"type":"response.completed"`)
}

func TestOpenAIWebPromptToolsReaderCarriesParallelToolMetadata(t *testing.T) {
	parallel := true
	prompt, err := NewOpenAIWebPromptToolsFromChatRequest(&apicompat.ChatCompletionsRequest{
		Model:             "auto",
		ParallelToolCalls: &parallel,
		Tools: []apicompat.ChatTool{{Type: "function", Function: &apicompat.ChatFunction{
			Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`),
		}}},
	})
	require.NoError(t, err)
	envelope, _ := json.Marshal(map[string]any{
		"protocol": prompt.Protocol, "nonce": prompt.Nonce, "schema_hash": prompt.SchemaHash,
		"calls": []any{map[string]any{"name": "lookup", "arguments": map[string]any{}}, map[string]any{"name": "lookup", "arguments": map[string]any{}}},
	})
	sse := "data: {\"conversation_id\":\"conv\",\"o\":\"append\",\"p\":\"/message/content/parts/0\",\"v\":" + mustPromptJSONString(string(envelope)) + "}\n\ndata: {\"conversation_id\":\"conv\",\"is_complete\":true}\n\n"
	reader := newOpenAIWebResponsesBodyWithPromptTools(io.NopCloser(strings.NewReader(sse)), "auto", nil, prompt)
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"parallel_tool_calls":true`)
}

func mustPromptJSONString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
