package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestOpenAIPrismToolBridgeIsOptionalAndTransportScoped(t *testing.T) {
	account := prismGatewayTestAccount()
	req := prismPromptTestRequest()
	_, prompt, err := prepareOpenAIPrismTools(account, req)
	var upstreamErr *OpenAIPrismHTTPError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, http.StatusBadRequest, upstreamErr.StatusCode)
	require.Nil(t, prompt)
	account.Extra["prism_prompt_tool_bridge"] = true
	prepared, prompt, err := prepareOpenAIPrismTools(account, req)
	require.NoError(t, err)
	require.NotNil(t, prompt)
	require.Empty(t, prepared.Tools)
	for _, transport := range []string{OpenAITransportCodex, OpenAITransportWeb} {
		account.Extra[OpenAIWebTransportExtraKey] = transport
		require.False(t, account.IsPrismPromptToolBridgeEnabled())
	}
	account.Extra[OpenAIWebTransportExtraKey] = OpenAITransportPrism
	for _, setting := range []any{nil, false, "true"} {
		account.Extra["prism_prompt_tool_bridge"] = setting
		require.False(t, account.IsPrismPromptToolBridgeEnabled())
	}
	plain := &apicompat.ResponsesRequest{Input: json.RawMessage(`"protocol-looking ordinary text"`), Instructions: "unchanged instruction"}
	prepared, prompt, err = prepareOpenAIPrismTools(account, plain)
	require.NoError(t, err)
	require.Nil(t, prompt)
	require.Equal(t, plain.Input, prepared.Input)
	require.Equal(t, plain.Instructions, prepared.Instructions)
	prepared.Input[0] = '['
	require.Equal(t, byte('"'), plain.Input[0], "native mode must not mutate its caller")
}

func prismPromptTestRequest() *apicompat.ResponsesRequest {
	return &apicompat.ResponsesRequest{
		Model:        "gpt-test",
		Instructions: "Use the available tools.",
		Input:        json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_text","text":"list files"}]}]`),
		Tools: []apicompat.ResponsesTool{
			{Type: "function", Name: "list_files", Description: "List local files", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)},
			{Type: "custom", Name: "shell", Description: "Run a shell command", Format: json.RawMessage(`{"type":"grammar","definition":"command"}`)},
		},
	}
}

func TestPrepareOpenAIPrismPromptToolsRemovesNativeToolsAndRendersInstruction(t *testing.T) {
	req := prismPromptTestRequest()
	prepared, prompt, err := prepareOpenAIPrismPromptTools(req)
	require.NoError(t, err)
	require.NotNil(t, prompt)
	require.Empty(t, prepared.Tools)
	require.Empty(t, prepared.Instructions)
	var items []map[string]any
	require.NoError(t, json.Unmarshal(prepared.Input, &items))
	require.GreaterOrEqual(t, len(items), 3)
	encoded := string(prepared.Input)
	require.Contains(t, encoded, "Use the available tools.")
	require.Contains(t, encoded, "list_files")
	require.Contains(t, encoded, "shell")
	// The original request remains untouched for downstream continuation logic.
	require.Len(t, req.Tools, 2)
	require.Equal(t, "Use the available tools.", req.Instructions)
}

func TestOpenAIPrismToolBridgePreservesNativeFileParts(t *testing.T) {
	req := prismPromptTestRequest()
	req.Input = json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect the file"},{"type":"input_file","filename":"example.tex","project_path":"example.tex"}]}]`)
	prepared, _, err := prepareOpenAIPrismPromptTools(req)
	require.NoError(t, err)
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(prepared.Input, &items))
	require.JSONEq(t, string(req.Input), "["+string(items[len(items)-1])+"]")
}

func TestPrepareOpenAIPrismPromptToolsPreservesHistoricalCallsAndOrphanOutputs(t *testing.T) {
	req := prismPromptTestRequest()
	req.Input = json.RawMessage(`[
		{"type":"function_call","call_id":"call_1","name":"list_files","arguments":{"path":"."}},
		{"type":"function_call_output","call_id":"call_1","output":{"files":["a.txt"]}},
		{"type":"custom_tool_call_output","call_id":"orphan_custom","output":["ok"]},
		{"type":"tool_search_output","call_id":"orphan_search","output":{"tools":["list_files"]}},
		{"type":"mcp_tool_call_output","call_id":"orphan_mcp","output":"done"}
	]`)
	prepared, _, err := prepareOpenAIPrismPromptTools(req)
	require.NoError(t, err)
	encoded := string(prepared.Input)
	require.Contains(t, encoded, "Previous assistant tool calls")
	require.Contains(t, encoded, `path`)
	require.Contains(t, encoded, `call_id=call_1`)
	require.Contains(t, encoded, `call_id=orphan_custom`)
	require.Contains(t, encoded, `call_id=orphan_search`)
	require.Contains(t, encoded, `call_id=orphan_mcp`)
	require.Contains(t, encoded, "files")
	require.Contains(t, encoded, "ok")
}

func prismSSE(frames ...string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(strings.Join(frames, "") + "data: [DONE]\n\n"))
}

func prismDelta(text string) string {
	b, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": text})
	return "event: response.output_text.delta\ndata: " + string(b) + "\n\n"
}

func prismCompleted() string {
	b, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_prism_test", "model": "gpt-test", "status": "completed"}})
	return "event: response.completed\ndata: " + string(b) + "\n\n"
}

func TestOpenAIPrismPromptToolBodyBuffersNormalTextUntilTerminal(t *testing.T) {
	prepared, prompt, err := prepareOpenAIPrismPromptTools(prismPromptTestRequest())
	require.NoError(t, err)
	require.NotNil(t, prepared)
	sse := prismSSE(prismDelta("hello "), prismDelta("world"), prismCompleted())
	raw, readErr := io.ReadAll(newOpenAIPrismPromptToolBody(sse, prompt))
	require.NoError(t, readErr)
	value := string(raw)
	require.Contains(t, value, "response.created")
	require.Contains(t, value, "response.output_text.delta")
	require.Contains(t, value, `"delta":"hello world"`)
	require.Contains(t, value, "response.completed")
	require.NotContains(t, value, "response.failed")
}

func TestOpenAIPrismPromptToolBodyEmitsFunctionLifecycle(t *testing.T) {
	_, prompt, err := prepareOpenAIPrismPromptTools(prismPromptTestRequest())
	require.NoError(t, err)
	envelope := map[string]any{
		"protocol": prompt.Protocol, "nonce": prompt.Nonce, "schema_hash": prompt.SchemaHash,
		"event": openAIWebPromptToolEvent, "start": openAIWebPromptToolStartSignal, "end": openAIWebPromptToolEndSignal,
		"calls": []any{map[string]any{"name": "list_files", "type": "function", "arguments": map[string]string{"path": "."}}},
	}
	encoded, _ := json.Marshal(envelope)
	raw, readErr := io.ReadAll(newOpenAIPrismPromptToolBody(prismSSE(prismDelta(string(encoded)), prismCompleted()), prompt))
	require.NoError(t, readErr)
	value := string(raw)
	for _, event := range []string{"response.created", "response.output_item.added", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.output_item.done", "response.completed"} {
		require.Contains(t, value, event)
	}
	require.NotContains(t, value, "response.output_text.delta")
	require.Contains(t, value, `"type":"function_call"`)
	require.Contains(t, value, `"arguments":"{\"path\":\".\"}"`)
}

func TestOpenAIPrismPromptToolBodyEmitsCustomLifecycle(t *testing.T) {
	_, prompt, err := prepareOpenAIPrismPromptTools(prismPromptTestRequest())
	require.NoError(t, err)
	envelope := map[string]any{
		"protocol": prompt.Protocol, "nonce": prompt.Nonce, "schema_hash": prompt.SchemaHash,
		"event": openAIWebPromptToolEvent, "start": openAIWebPromptToolStartSignal, "end": openAIWebPromptToolEndSignal,
		"calls": []any{map[string]any{"name": "shell", "type": "custom", "input": "pwd"}},
	}
	encoded, _ := json.Marshal(envelope)
	raw, readErr := io.ReadAll(newOpenAIPrismPromptToolBody(prismSSE(prismDelta(string(encoded)), prismCompleted()), prompt))
	require.NoError(t, readErr)
	value := string(raw)
	require.Contains(t, value, "response.custom_tool_call_input.delta")
	require.Contains(t, value, "response.custom_tool_call_input.done")
	require.Contains(t, value, `"type":"custom_tool_call"`)
	require.Contains(t, value, `"input":"pwd"`)
}

func TestOpenAIPrismPromptToolBodyRejectsMalformedEnvelopeAndRequiredProse(t *testing.T) {
	_, prompt, err := prepareOpenAIPrismPromptTools(prismPromptTestRequest())
	require.NoError(t, err)
	bad := `{"protocol":"` + prompt.Protocol + `","nonce":"wrong","schema_hash":"` + prompt.SchemaHash + `","event":"tool_call","start":"tool_call_start","end":"tool_call_end","calls":[]}`
	raw, readErr := io.ReadAll(newOpenAIPrismPromptToolBody(prismSSE(prismDelta(bad), prismCompleted()), prompt))
	require.NoError(t, readErr)
	value := string(raw)
	require.Contains(t, value, "response.failed")
	require.Contains(t, value, "tool_protocol_error")
	require.NotContains(t, value, `"status":"completed"`)

	req := prismPromptTestRequest()
	req.ToolChoice = json.RawMessage(`"required"`)
	_, requiredPrompt, err := prepareOpenAIPrismPromptTools(req)
	require.NoError(t, err)
	raw, readErr = io.ReadAll(newOpenAIPrismPromptToolBody(prismSSE(prismDelta("I cannot use a tool."), prismCompleted()), requiredPrompt))
	require.NoError(t, readErr)
	require.Contains(t, string(raw), "tool_protocol_error")
}

func TestOpenAIPrismPromptToolBodyPreservesUpstreamFailure(t *testing.T) {
	failure := `{"type":"response.failed","response":{"id":"resp_failed","status":"failed","error":{"code":"upstream_failure","message":"provider unavailable"}}}`
	sse := "event: response.failed\ndata: " + failure + "\n\ndata: [DONE]\n\n"
	raw, readErr := io.ReadAll(newOpenAIPrismPromptToolBody(io.NopCloser(strings.NewReader(sse)), nil))
	require.NoError(t, readErr)
	value := string(raw)
	require.Contains(t, value, "event: response.failed")
	require.Contains(t, value, "provider unavailable")
	require.NotContains(t, value, "event: response.completed")
}

type prismErrorReader struct{}

func (prismErrorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (prismErrorReader) Close() error             { return nil }

func TestOpenAIPrismPromptToolBodyDoesNotSwallowSourceReadError(t *testing.T) {
	r := newOpenAIPrismPromptToolBody(prismErrorReader{}, nil)
	_, err := io.ReadAll(r)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read failed")
}

func TestOpenAIPrismPromptInputStringAndObjectOutputRemainText(t *testing.T) {
	req := prismPromptTestRequest()
	req.Input = json.RawMessage(`"hello"`)
	prepared, _, err := prepareOpenAIPrismPromptTools(req)
	require.NoError(t, err)
	require.Contains(t, string(prepared.Input), "hello")
	require.True(t, bytes.HasPrefix(bytes.TrimSpace(prepared.Input), []byte("[")))
}
