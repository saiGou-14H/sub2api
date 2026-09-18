package service

import (
	"bytes"
	"encoding/json"
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
