package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func promptStreamFixture(t *testing.T) (*OpenAIWebPromptTools, *openAIWebResponsesReader, string) {
	t.Helper()
	p, err := NewOpenAIWebPromptToolsFromResponsesRequest(&apicompat.ResponsesRequest{
		Model: "auto", Tools: []apicompat.ResponsesTool{
			{Type: "function", Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)},
			{Type: "custom", Name: "exec"},
		},
	})
	require.NoError(t, err)
	r := newOpenAIWebResponsesBodyWithPromptTools(nil, "auto", nil, p).(*openAIWebResponsesReader)
	header := fmt.Sprintf(`{"protocol":%q,"nonce":%q,"schema_hash":%q,"event":"tool_call","start":"tool_call_start","calls":[`, p.Protocol, p.Nonce, p.SchemaHash)
	return p, r, header
}

func promptStreamPatch(text, operation string) openAIWebSSEFrame {
	body, _ := json.Marshal(map[string]any{"o": operation, "p": "/message/content/parts/0", "v": text})
	return openAIWebSSEFrame{data: string(body)}
}

func promptStreamEvents(t *testing.T, raw string) []apicompat.ResponsesStreamEvent {
	t.Helper()
	var events []apicompat.ResponsesStreamEvent
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var event apicompat.ResponsesStreamEvent
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
		events = append(events, event)
	}
	return events
}

func TestOpenAIWebPromptStreamBeforeUpstreamCompletes(t *testing.T) {
	p, _, header := promptStreamFixture(t)
	source, writer := io.Pipe()
	r := newOpenAIWebResponsesBodyWithPromptTools(source, "auto", nil, p)
	t.Cleanup(func() { _ = writer.Close(); _ = r.Close() })
	chunks := make(chan string, 64)
	errors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			chunks <- scanner.Text()
		}
		errors <- scanner.Err()
	}()
	write := func(text string) {
		t.Helper()
		frame := promptStreamPatch(text, "append")
		_, err := io.WriteString(writer, "data: "+frame.data+"\n\n")
		require.NoError(t, err)
	}
	write(header + `{"name":"lookup","arguments":{"city":"Sh`)
	var received string
	deadline := time.After(2 * time.Second)
waitDelta:
	for {
		select {
		case line := <-chunks:
			received += line + "\n"
			if strings.Contains(line, `"type":"response.function_call_arguments.delta"`) {
				break waitDelta
			}
		case <-deadline:
			t.Fatal("tool delta was buffered until upstream completion")
		}
	}
	require.Contains(t, received, `\"city\":\"Sh`)
	require.NotContains(t, received, "response.output_item.done")
	require.NotContains(t, received, p.Nonce)
	write(`anghai"}}],"end":"tool_call_end"}`)
	_, err := io.WriteString(writer, "data: [DONE]\n\n")
	require.NoError(t, err)
	select {
	case err := <-errors:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not finish")
	}
}

func TestOpenAIWebPromptStreamFragmentedFunctionAndCustom(t *testing.T) {
	for _, call := range []struct{ name, raw, expected, delta string }{
		{"function", `{"name":"lookup","arguments":{"city":"a , b \\ c \" d \u4e2d\ud83d\ude00","nested":[{"x":true}]}}`, `{"city":"a , b \\ c \" d \u4e2d\ud83d\ude00","nested":[{"x":true}]}`, "response.function_call_arguments.delta"},
		{"encoded", `{"name":"lookup","arguments":"{\"city\":\"a , b\"}"}`, `{"city":"a , b"}`, "response.function_call_arguments.delta"},
		{"custom", `{"name":"exec","type":"custom","input":"a , b \\ c \" d \u4e2d\ud83d\ude00\n"}`, "a , b \\ c \" d \u4e2d\U0001f600\n", "response.custom_tool_call_input.delta"},
		{"custom_legacy", `{"name":"exec","type":"custom","arguments":{"input":"Get-Location"}}`, "Get-Location", "response.custom_tool_call_input.delta"},
	} {
		t.Run(call.name, func(t *testing.T) {
			_, r, header := promptStreamFixture(t)
			text := header + call.raw + `],"end":"tool_call_end"}`
			for _, b := range []byte(text) {
				r.convertFrame(promptStreamPatch(string(b), "append"))
			}
			require.False(t, r.failed, r.output.String())
			before := r.output.String()
			require.NotContains(t, before, "response.output_item.done")
			r.convertFrame(openAIWebSSEFrame{data: "[DONE]"})
			events := promptStreamEvents(t, r.output.String())
			var joined string
			count, added, completed := 0, 0, 0
			state := apicompat.NewResponsesEventToChatState()
			var chatJoined string
			for i, event := range events {
				require.Equal(t, i, event.SequenceNumber)
				if event.Type == call.delta {
					joined += event.Delta
					count++
				}
				if event.Type == "response.output_item.added" {
					added++
				}
				if event.Type == "response.completed" {
					completed++
					require.Len(t, event.Response.Output, 1)
					item := event.Response.Output[0]
					if item.Type == "custom_tool_call" {
						require.Equal(t, joined, item.Input)
					} else {
						require.Equal(t, joined, item.Arguments)
					}
				}
				for _, chunk := range apicompat.ResponsesEventToChatChunks(&event, state) {
					for _, choice := range chunk.Choices {
						for _, tool := range choice.Delta.ToolCalls {
							chatJoined += tool.Function.Arguments
						}
					}
				}
			}
			require.Greater(t, count, 2)
			require.Equal(t, call.expected, joined)
			require.Equal(t, joined, chatJoined)
			require.Equal(t, 1, added)
			require.Equal(t, 1, completed)
		})
	}
}

func TestOpenAIWebPromptStreamRejectsInvalidCompletion(t *testing.T) {
	for _, variant := range []string{"truncated", "missing_end", "bad_end", "schema", "duplicate", "unknown", "rewrite", "blocked", "eof", "trailing"} {
		t.Run(variant, func(t *testing.T) {
			p, r, header := promptStreamFixture(t)
			prefix := header + `{"name":"lookup","arguments":{"city":"Sh`
			r.convertFrame(promptStreamPatch(prefix, "append"))
			require.Contains(t, r.output.String(), "response.function_call_arguments.delta")
			suffix := `anghai"}}],"end":"tool_call_end"}`
			switch variant {
			case "truncated":
				suffix = ""
			case "missing_end":
				suffix = `anghai"}}]}`
			case "bad_end":
				suffix = `anghai"}}],"end":"bad"}`
			case "schema":
				suffix = `anghai","bad":1}}],"end":"tool_call_end"}`
				p.Tools[0].Parameters = json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"additionalProperties":false}`)
			case "duplicate":
				suffix = `anghai"}}],"end":"tool_call_end","nonce":"other"}`
			case "unknown":
				suffix = `anghai"}},{"name":"other","arguments":{}}],"end":"tool_call_end"}`
			case "rewrite":
				r.convertFrame(promptStreamPatch(strings.Replace(prefix, `"Sh`, `"Be`, 1), "replace"))
			case "blocked":
				r.convertFrame(openAIWebSSEFrame{data: `{"blocked":true}`})
			case "trailing":
				suffix += " unexpected"
			}
			r.convertFrame(promptStreamPatch(suffix, "append"))
			r.finish(variant != "eof")
			output := r.output.String()
			require.True(t, r.failed, output)
			require.Contains(t, output, "response.failed")
			require.NotContains(t, output, "response.output_item.done")
			require.NotContains(t, output, "response.completed")
			require.NotContains(t, output, p.Nonce)
			require.Equal(t, 1, strings.Count(output, "data: [DONE]"))
		})
	}
}

func TestOpenAIWebPromptStreamDoesNotMisclassifyPlainText(t *testing.T) {
	_, r, _ := promptStreamFixture(t)
	r.convertFrame(promptStreamPatch(`{"answer":"tool_call_start"}`, "append"))
	r.finish(true)
	require.False(t, r.failed)
	require.NotContains(t, r.output.String(), "response.function_call_arguments")
	require.Contains(t, r.output.String(), "response.output_text.done")
}
