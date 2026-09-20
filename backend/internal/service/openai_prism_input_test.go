package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestPrismInputCanonicalizesEasyMessagesWithoutLosingFields(t *testing.T) {
	input := prismInput(&apicompat.ResponsesRequest{Input: json.RawMessage(`[{"role":"user","content":"Reply exactly PRISM_OK.","sequence":9007199254740993123,"unknown":null},{"role":"assistant","content":"previous answer"},{"role":"user","content":[{"type":"input_text","text":"continue"}]},{"type":"function_call_output","call_id":"call-test","output":"tool result","large":9007199254740993123}]`)})
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(input, &items))
	require.Len(t, items, 4)
	require.JSONEq(t, `{"type":"message","role":"user","content":[{"type":"input_text","text":"Reply exactly PRISM_OK."}],"sequence":9007199254740993123,"unknown":null}`, string(items[0]))
	require.Contains(t, string(items[0]), "9007199254740993123")
	require.JSONEq(t, `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"previous answer"}]}`, string(items[1]))
	require.JSONEq(t, `{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}`, string(items[2]))
	require.JSONEq(t, `{"type":"function_call_output","call_id":"call-test","output":"tool result","large":9007199254740993123}`, string(items[3]))
	require.Contains(t, string(items[3]), "9007199254740993123")
}
