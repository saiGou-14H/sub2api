package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestRestoreOpenAIWebPromptToolOutputsOnlyOutputContinuation(t *testing.T) {
	responsesReq := &apicompat.ResponsesRequest{
		PreviousResponseID: "resp_previous",
		Input: json.RawMessage(`[{
			"type":"function_call_output",
			"call_id":"call_exec",
			"output":"created test.py"
		}]`),
	}
	chatReq := &apicompat.ChatCompletionsRequest{}

	restoreOpenAIWebPromptToolOutputs(responsesReq, chatReq)

	require.Len(t, chatReq.Messages, 1)
	require.Equal(t, "tool", chatReq.Messages[0].Role)
	require.Equal(t, "call_exec", chatReq.Messages[0].ToolCallID)
	require.JSONEq(t, `"created test.py"`, string(chatReq.Messages[0].Content))
}

func TestRestoreOpenAIWebPromptToolOutputsSupportsAllOutputFamilies(t *testing.T) {
	responsesReq := &apicompat.ResponsesRequest{Input: json.RawMessage(`[
		{"type":"custom_tool_call_output","call_id":"custom_1","output":{"ok":true}},
		{"type":"tool_search_output","call_id":"search_1","output":["a","b"]},
		{"type":"mcp_tool_call_output","call_id":"mcp_1","output":"mcp result"}
	]`)}
	chatReq := &apicompat.ChatCompletionsRequest{}

	restoreOpenAIWebPromptToolOutputs(responsesReq, chatReq)

	require.Len(t, chatReq.Messages, 3)
	require.Equal(t, []string{"custom_1", "search_1", "mcp_1"}, []string{
		chatReq.Messages[0].ToolCallID,
		chatReq.Messages[1].ToolCallID,
		chatReq.Messages[2].ToolCallID,
	})
	require.JSONEq(t, `"{\"ok\":true}"`, string(chatReq.Messages[0].Content))
	require.JSONEq(t, `"[\"a\",\"b\"]"`, string(chatReq.Messages[1].Content))
}

func TestRestoreOpenAIWebPromptToolOutputsDedupesAndPrecedesNewUserTurn(t *testing.T) {
	responsesReq := &apicompat.ResponsesRequest{Input: json.RawMessage(`[
		{"type":"function_call_output","call_id":"call_1","output":"already present"},
		{"role":"user","content":"continue"}
	]`)}
	chatReq := &apicompat.ChatCompletionsRequest{Messages: []apicompat.ChatMessage{
		{Role: "tool", ToolCallID: "call_1", Content: json.RawMessage(`"already present"`)},
		{Role: "user", Content: json.RawMessage(`"continue"`)},
	}}

	restoreOpenAIWebPromptToolOutputs(responsesReq, chatReq)

	require.Len(t, chatReq.Messages, 2)
	require.Equal(t, "tool", chatReq.Messages[0].Role)
	require.Equal(t, "user", chatReq.Messages[1].Role)
}

func TestRestoreOpenAIWebPromptToolOutputsPayloadUsesPromptText(t *testing.T) {
	responsesReq := &apicompat.ResponsesRequest{
		Model: "auto",
		Tools: []apicompat.ResponsesTool{{
			Type:       "function",
			Name:       "exec_command",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
		}},
		Input: json.RawMessage(`[{"type":"function_call_output","call_id":"call_exec","output":"OK"}]`),
	}
	promptTools, err := NewOpenAIWebPromptToolsFromResponsesRequest(responsesReq)
	require.NoError(t, err)
	chatReq, err := responsesToOpenAIWebChatRequest(responsesReq, nil)
	require.NoError(t, err)
	restoreOpenAIWebPromptToolOutputs(responsesReq, chatReq)

	body, err := NewOpenAIWebTransport(nil, OpenAIWebTransportOptions{}).BuildConversationPayloadWithOptions(OpenAIWebConversationOptions{
		Request: chatReq, PromptTools: promptTools,
	})
	require.NoError(t, err)
	bodyText := string(body)
	require.Contains(t, bodyText, "Previous tool result (call_id=call_exec):")
	require.Contains(t, bodyText, "OK")
	require.NotContains(t, bodyText, `"tools"`)
	require.False(t, strings.Contains(bodyText, `"tool_call_id"`))
}
