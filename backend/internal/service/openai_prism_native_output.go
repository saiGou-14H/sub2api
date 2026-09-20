package service

import (
	"bytes"
	"encoding/json"
)

// Filter the actual remote result before the optional prompt bridge sees it.
// A tool already executed in the sandbox must never ask the client to run it
// again. The bridge creates its own validated client calls later in the flow.
func prismNativeResponsesPayload(payload []byte, secrets []string) []byte {
	var source map[string]json.RawMessage
	if json.Unmarshal(payload, &source) != nil {
		return []byte(`{"output":[]}`)
	}
	result := make(map[string]any)
	for _, key := range []string{"id", "model", "created_at"} {
		if raw, ok := source[key]; ok {
			result[key] = raw
		}
	}
	output := make([]map[string]any, 0)
	var items []map[string]json.RawMessage
	_ = json.Unmarshal(source["output"], &items)
	for _, item := range items {
		var kind string
		_ = json.Unmarshal(item["type"], &kind)
		if kind != "message" && kind != "reasoning" {
			continue
		}
		clean := map[string]any{"type": kind, "status": "completed"}
		var id string
		if json.Unmarshal(item["id"], &id) == nil && id != "" {
			clean["id"] = prismReplaceSecrets(id, secrets)
		}
		field := "content"
		if kind == "reasoning" {
			field = "summary"
		} else {
			clean["role"] = "assistant"
		}
		parts := make([]map[string]any, 0)
		var content []map[string]json.RawMessage
		_ = json.Unmarshal(item[field], &content)
		for _, part := range content {
			var partType, text string
			_ = json.Unmarshal(part["type"], &partType)
			textField := "text"
			if partType == "refusal" {
				textField = "refusal"
			}
			if (kind == "message" && partType != "output_text" && partType != "refusal") || (kind == "reasoning" && partType != "summary_text") || json.Unmarshal(part[textField], &text) != nil {
				continue
			}
			cleanPart := map[string]any{"type": partType, textField: prismReplaceSecrets(text, secrets)}
			if partType == "output_text" {
				cleanPart["annotations"] = []any{}
				cleanPart["logprobs"] = []any{}
			}
			parts = append(parts, cleanPart)
		}
		clean[field] = parts
		output = append(output, clean)
	}
	result["output"] = output
	if usage := prismNativeUsage(source["usage"]); len(usage) > 0 {
		result["usage"] = usage
	}
	encoded, _ := json.Marshal(result)
	return encoded
}

func prismNativeUsage(raw json.RawMessage) map[string]any {
	var usage map[string]json.RawMessage
	_ = json.Unmarshal(raw, &usage)
	result := make(map[string]any)
	for _, name := range []string{"input_tokens", "output_tokens", "total_tokens", "cached_tokens", "reasoning_tokens", "audio_tokens", "image_tokens", "text_tokens"} {
		value := bytes.TrimSpace(usage[name])
		var number int64
		if len(value) > 0 && string(value) != "null" && json.Unmarshal(value, &number) == nil && number >= 0 {
			result[name] = number
		}
	}
	for _, name := range []string{"input_tokens_details", "output_tokens_details"} {
		if nested := usage[name]; len(nested) > 0 {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(nested, &fields)
			// Detail objects are one level deep in Responses usage.
			delete(fields, "input_tokens_details")
			delete(fields, "output_tokens_details")
			flat, _ := json.Marshal(fields)
			result[name] = prismNativeUsage(flat)
		}
	}
	return result
}
