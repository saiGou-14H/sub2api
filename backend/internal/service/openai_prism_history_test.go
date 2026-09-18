package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestPrismFullTextHistoryReachesCurrentPrompt(t *testing.T) {
	raw := prismInput(&apicompat.ResponsesRequest{Instructions: "Follow the rules.", Input: json.RawMessage(`[{"role":"user","content":"Remember 731."},{"role":"assistant","content":"PRISM_OK"},{"role":"user","content":[{"type":"input_text","text":"What number did I ask you to remember?"}]}]`)})
	var items []struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(raw, &items))
	require.Len(t, items, 2)
	require.Equal(t, "system", items[0].Role)
	require.Equal(t, "Follow the rules.", items[0].Content[0].Text)
	require.Equal(t, "user", items[1].Role)
	require.Len(t, items[1].Content, 1)
	text := items[1].Content[0].Text
	require.Contains(t, text, `{"role":"user","text":"Remember 731."}`)
	require.Contains(t, text, `{"role":"assistant","text":"PRISM_OK"}`)
	require.True(t, strings.HasSuffix(text, "Current user message:\nWhat number did I ask you to remember?"))
}

func TestPrismFullHistoryAcceptsResponsesOutputMessage(t *testing.T) {
	raw := prismInput(&apicompat.ResponsesRequest{Input: json.RawMessage(`[{"role":"user","content":"Remember 731."},{"type":"message","id":"msg_previous","status":"completed","role":"assistant","content":[{"type":"output_text","text":"PRISM_OK","annotations":[],"logprobs":[]}]},{"role":"user","content":"Which number?"}]`)})
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &items))
	require.Len(t, items, 1)
	var message struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(items[0], &message))
	require.Contains(t, message.Content[0].Text, `{"role":"user","text":"Remember 731."}`)
	require.Contains(t, message.Content[0].Text, `{"role":"assistant","text":"PRISM_OK"}`)
	require.True(t, strings.HasSuffix(message.Content[0].Text, "Current user message:\nWhich number?"))
}

func TestPrismTextHistoryDoesNotFlattenFilesOrUnknownItems(t *testing.T) {
	for _, raw := range []string{
		`[{"type":"message","role":"user","content":[{"type":"input_file","file_id":"file-test"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"read it"}]}]`,
		`[{"type":"message","role":"user","content":[{"type":"input_text","text":"earlier"}],"future":9007199254740993123},{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}]`,
		`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"earlier","annotations":[{"type":"url_citation","url":"https://example.com"}]}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}]`,
		`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"earlier","logprobs":[{"token":"earlier"}]}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}]`,
	} {
		var items []json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(raw), &items))
		out, _ := json.Marshal(prismFoldTextHistory(items))
		require.JSONEq(t, raw, string(out))
	}
}
