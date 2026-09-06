package service

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// restoreOpenAIWebPromptToolOutputs repairs Responses continuation requests
// that contain only tool output items. Codex clients commonly send
// previous_response_id plus function_call_output without replaying the
// preceding function_call. The generic Responses-to-Chat bridge intentionally
// drops that orphan tool message, but the Web Prompt Tool transport needs it in
// order to tell ChatGPT what the local tool returned.
func restoreOpenAIWebPromptToolOutputs(responsesReq *apicompat.ResponsesRequest, chatReq *apicompat.ChatCompletionsRequest) {
	if responsesReq == nil || chatReq == nil {
		return
	}

	input := bytes.TrimSpace(responsesReq.Input)
	if len(input) == 0 || bytes.Equal(input, []byte("null")) || input[0] != '[' {
		return
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(input, &rawItems); err != nil {
		return
	}

	existing := make(map[string]struct{})
	for _, message := range chatReq.Messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "tool") && strings.TrimSpace(message.ToolCallID) != "" {
			existing[strings.TrimSpace(message.ToolCallID)] = struct{}{}
		}
	}

	type recoveredOutput struct {
		callID  string
		content json.RawMessage
	}
	var recovered []recoveredOutput
	lastOutputIndex := -1
	for index, raw := range rawItems {
		var item struct {
			Type       string          `json:"type"`
			CallID     string          `json:"call_id"`
			ToolCallID string          `json:"tool_call_id"`
			Output     json.RawMessage `json:"output"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "function_call_output", "custom_tool_call_output", "tool_search_output", "mcp_tool_call_output":
		default:
			continue
		}
		callID := strings.TrimSpace(item.CallID)
		if callID == "" {
			callID = strings.TrimSpace(item.ToolCallID)
		}
		if callID == "" {
			continue
		}
		lastOutputIndex = index
		if _, ok := existing[callID]; ok {
			continue
		}
		existing[callID] = struct{}{}
		recovered = append(recovered, recoveredOutput{
			callID:  callID,
			content: openAIWebPromptToolOutputContent(item.Output),
		})
	}
	if len(recovered) == 0 {
		return
	}

	insertAt := len(chatReq.Messages)
	if lastOutputIndex >= 0 && responsesInputHasUserAfter(rawItems, lastOutputIndex) {
		for index, message := range chatReq.Messages {
			if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
				insertAt = index
				break
			}
		}
	}

	toolMessages := make([]apicompat.ChatMessage, 0, len(recovered))
	for _, output := range recovered {
		toolMessages = append(toolMessages, apicompat.ChatMessage{
			Role:       "tool",
			ToolCallID: output.callID,
			Content:    output.content,
		})
	}
	chatReq.Messages = append(chatReq.Messages[:insertAt], append(toolMessages, chatReq.Messages[insertAt:]...)...)
}

func openAIWebPromptToolOutputContent(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	text := ""
	if len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if err := json.Unmarshal(raw, &text); err != nil {
			text = string(raw)
		}
	}
	content, _ := json.Marshal(text)
	return content
}

func responsesInputHasUserAfter(items []json.RawMessage, index int) bool {
	for _, raw := range items[index+1:] {
		var item struct {
			Type string `json:"type"`
			Role string `json:"role"`
		}
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Role), "user") || strings.EqualFold(strings.TrimSpace(item.Type), "input_text") || strings.EqualFold(strings.TrimSpace(item.Type), "input_image") || strings.EqualFold(strings.TrimSpace(item.Type), "input_file") {
			return true
		}
	}
	return false
}
