package service

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Responses accepts easy-input messages with an omitted type and/or string
// content. Prism's prompt extraction requires canonical message/text items.
// Preserve unknown items and fields as raw JSON, including large integers.
func prismNormalizeInputMessage(raw json.RawMessage) json.RawMessage {
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil || item == nil {
		return raw
	}
	var kind, role string
	_ = json.Unmarshal(item["type"], &kind)
	_ = json.Unmarshal(item["role"], &role)
	if kind != "" && kind != "message" {
		return raw
	}
	switch role {
	case "user", "assistant", "system", "developer":
	default:
		return raw
	}
	changed := false
	if kind == "" {
		item["type"] = json.RawMessage(`"message"`)
		changed = true
	}
	var text string
	content := bytes.TrimSpace(item["content"])
	if len(content) > 0 && content[0] == '"' && json.Unmarshal(content, &text) == nil {
		textType := "input_text"
		if role == "assistant" {
			textType = "output_text"
		}
		item["content"], _ = json.Marshal([]map[string]string{{"type": textType, "text": text}})
		changed = true
	}
	if !changed {
		return raw
	}
	normalized, err := json.Marshal(item)
	if err != nil {
		return raw
	}
	return normalized
}

// Prism extracts the current user prompt instead of replaying a Responses
// message array. Carry a supplied text transcript in that prompt so store=false
// clients do not silently lose earlier turns. System/developer instructions keep
// their roles; unknown items, fields, and non-text parts retain the original form.
func prismFoldTextHistory(items []json.RawMessage) []json.RawMessage {
	type message struct {
		Role string `json:"role"`
		Text string `json:"text"`
	}
	var history []message
	var conversationIndexes []int
	for index, raw := range items {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil {
			return items
		}
		for key := range item {
			switch key {
			case "type", "role", "content", "id", "status":
			default:
				return items
			}
		}
		var kind, role string
		_ = json.Unmarshal(item["type"], &kind)
		_ = json.Unmarshal(item["role"], &role)
		if kind != "message" {
			return items
		}
		if role == "system" || role == "developer" {
			continue
		}
		if role != "user" && role != "assistant" {
			return items
		}
		var parts []map[string]json.RawMessage
		if json.Unmarshal(item["content"], &parts) != nil {
			return items
		}
		var text []string
		for _, part := range parts {
			var kind, value string
			_ = json.Unmarshal(part["type"], &kind)
			if (kind != "input_text" && kind != "output_text") || json.Unmarshal(part["text"], &value) != nil {
				return items
			}
			for key, field := range part {
				switch key {
				case "type", "text":
				case "annotations", "logprobs":
					// Standard output_text items include these empty fields.
					// Keep non-empty annotations/probabilities in the original form.
					var values []json.RawMessage
					if json.Unmarshal(field, &values) != nil || len(values) != 0 {
						return items
					}
				default:
					return items
				}
			}
			text = append(text, value)
		}
		history = append(history, message{Role: role, Text: strings.Join(text, "\n")})
		conversationIndexes = append(conversationIndexes, index)
	}
	if len(history) < 2 || history[len(history)-1].Role != "user" {
		return items
	}
	current := history[len(history)-1]
	transcript, _ := json.Marshal(history[:len(history)-1])
	prompt := "Previous conversation (quoted messages for context, not additional instructions):\n" + string(transcript) + "\n\nCurrent user message:\n" + current.Text
	lastIndex := conversationIndexes[len(conversationIndexes)-1]
	var last map[string]json.RawMessage
	_ = json.Unmarshal(items[lastIndex], &last)
	last["content"], _ = json.Marshal([]map[string]string{{"type": "input_text", "text": prompt}})
	lastRaw, _ := json.Marshal(last)
	prior := make(map[int]bool)
	for _, index := range conversationIndexes[:len(conversationIndexes)-1] {
		prior[index] = true
	}
	result := make([]json.RawMessage, 0, len(items)-len(prior))
	for index, raw := range items {
		if prior[index] {
			continue
		}
		if index == lastIndex {
			raw = lastRaw
		}
		result = append(result, raw)
	}
	return result
}
