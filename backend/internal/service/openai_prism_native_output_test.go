package service

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIPrismNativeResultNeverReplaysSandboxCalls(t *testing.T) {
	payload := []byte(`{"id":"resp_test","output":[{"type":"function_call","name":"exec_command","call_id":"sandbox-call","arguments":"private command"},{"type":"function_call_output","output":"private result"},{"type":"message","role":"assistant","metadata":{"sandbox_token":"unknown secret"},"content":[{"type":"output_text","text":"hello credential-secret","metadata":"private metadata","annotations":[{"internal":"private annotation"}]}]},{"type":"reasoning","encrypted_content":"private state","summary":[{"type":"summary_text","text":"summary"}]}],"usage":{"input_tokens":9007199254740993,"output_tokens":2,"input_tokens_details":{"cached_tokens":3,"sandbox_token":"private token"},"debug":"private usage"}}`)
	filtered := prismNativeResponsesPayload(payload, []string{"credential-secret"})
	for _, secret := range []string{"private", "unknown secret", "credential-secret", "exec_command", "function_call", "sandbox-call", "encrypted_content"} {
		require.NotContains(t, string(filtered), secret)
	}
	require.Contains(t, string(filtered), "hello [redacted]")
	require.Contains(t, string(filtered), "9007199254740993")
	require.Contains(t, string(filtered), `"cached_tokens":3`)
	for _, stream := range []bool{false, true} {
		response := prismResponsesHTTP(filtered, "req_test", "conversation_test", "project_test", stream)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		_ = response.Body.Close()
		require.Contains(t, string(body), "hello [redacted]")
		require.NotContains(t, string(body), "function_call")
	}
	// This formatter is also used after the prompt bridge validates a client
	// call. It must still preserve that independently generated call lifecycle.
	bridged := prismResponsesSSE([]byte(`{"id":"resp_bridge","output":[{"type":"function_call","name":"client_tool","call_id":"client-call","arguments":"{}"}]}`), "req_test", "", "")
	encoded, _ := io.ReadAll(bridged.Body)
	_ = bridged.Body.Close()
	require.Contains(t, string(encoded), "response.function_call_arguments.done")
	require.Contains(t, string(encoded), "client_tool")
}

func TestOpenAIPrismNativeUsageDoesNotInventCounts(t *testing.T) {
	filtered := prismNativeResponsesPayload([]byte(`{"output":[],"usage":{"input_tokens":null,"output_tokens":-1,"total_tokens":"100","unknown":10}}`), nil)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(filtered, &payload))
	require.NotContains(t, payload, "usage")
	require.False(t, strings.Contains(string(filtered), "unknown"))
}
