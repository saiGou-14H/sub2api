package service

// The optional Prism client-tool bridge is separate from Prism's built-in
// tools, which execute in the upstream sandbox. The captured protocol does
// not demonstrate registration of arbitrary client tools or submit_tool_outputs.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/google/uuid"
)

func prepareOpenAIPrismTools(account *Account, req *apicompat.ResponsesRequest) (*apicompat.ResponsesRequest, *OpenAIWebPromptTools, error) {
	if account.IsPrismPromptToolBridgeEnabled() {
		return prepareOpenAIPrismPromptTools(req)
	}
	if req == nil {
		return nil, nil, errors.New("responses request is nil")
	}
	if len(req.Tools) > 0 {
		return nil, nil, &OpenAIPrismHTTPError{StatusCode: http.StatusBadRequest, Message: "Prism runs built-in tools in its remote sandbox; client tool declarations require the optional prism_prompt_tool_bridge setting"}
	}
	// Native mode leaves messages and instructions unchanged, including text
	// that happens to resemble a bridge envelope. It never invokes its parser.
	prepared := *req
	prepared.Input = append(json.RawMessage(nil), req.Input...)
	return &prepared, nil, nil
}

// prepareOpenAIPrismPromptTools returns a copy of req suitable for the Prism
// wire protocol and the request-scoped prompt registry.  The caller's request
// is never mutated; native tool declarations and Instructions are represented
// by textual Responses message items instead.
func prepareOpenAIPrismPromptTools(req *apicompat.ResponsesRequest) (*apicompat.ResponsesRequest, *OpenAIWebPromptTools, error) {
	if req == nil {
		return nil, nil, errors.New("responses request is nil")
	}
	prepared := *req
	prepared.Input = append(json.RawMessage(nil), req.Input...)
	prepared.Tools = append([]apicompat.ResponsesTool(nil), req.Tools...)
	if len(req.Tools) == 0 {
		return &prepared, nil, nil
	}
	prompt, err := NewOpenAIWebPromptToolsFromResponsesRequest(req)
	if err != nil {
		return nil, nil, err
	}
	input, err := encodeOpenAIPrismPromptInput(req.Input, prompt)
	if err != nil {
		return nil, nil, err
	}
	messages := make([]any, 0, 2)
	if strings.TrimSpace(req.Instructions) != "" {
		messages = append(messages, openAIPrismPromptMessage("system", req.Instructions))
	}
	if instruction := strings.TrimSpace(strings.ReplaceAll(prompt.Instruction(), "remote ChatGPT Web model", "remote Prism model")); instruction != "" {
		messages = append(messages, openAIPrismPromptMessage("system", instruction))
	}
	var existing []any
	if len(bytes.TrimSpace(input)) > 0 && !bytes.Equal(bytes.TrimSpace(input), []byte("null")) {
		if err := json.Unmarshal(input, &existing); err != nil {
			return nil, nil, fmt.Errorf("encode Prism prompt input: %w", err)
		}
	}
	messages = append(messages, existing...)
	prepared.Input, err = json.Marshal(messages)
	if err != nil {
		return nil, nil, fmt.Errorf("encode Prism prompt input: %w", err)
	}
	prepared.Tools = nil
	prepared.Instructions = ""
	return &prepared, prompt, nil
}

func openAIPrismPromptMessage(role, text string) map[string]any {
	return map[string]any{
		"type": "message", "role": role,
		"content": []map[string]string{{"type": "input_text", "text": text}},
	}
}

func encodeOpenAIPrismPromptInput(raw json.RawMessage, prompt *OpenAIWebPromptTools) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return json.RawMessage(`[]`), nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("parse Responses input string: %w", err)
		}
		return json.Marshal([]any{openAIPrismPromptMessage("user", text)})
	}
	if raw[0] != '[' {
		return json.Marshal([]any{openAIPrismPromptMessage("user", compactOpenAIPrismJSON(raw))})
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parse Responses input array: %w", err)
	}
	result := make([]any, 0, len(items))
	for _, item := range items {
		converted, err := encodeOpenAIPrismPromptInputItem(item, prompt)
		if err != nil {
			return nil, err
		}
		result = append(result, converted...)
	}
	return json.Marshal(result)
}

func encodeOpenAIPrismPromptInputItem(raw json.RawMessage, prompt *OpenAIWebPromptTools) ([]any, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return []any{openAIPrismPromptMessage("user", compactOpenAIPrismJSON(raw))}, nil
	}
	var typ, role, name, callID, namespace string
	_ = json.Unmarshal(obj["type"], &typ)
	_ = json.Unmarshal(obj["role"], &role)
	_ = json.Unmarshal(obj["name"], &name)
	_ = json.Unmarshal(obj["call_id"], &callID)
	_ = json.Unmarshal(obj["namespace"], &namespace)
	if callID == "" {
		_ = json.Unmarshal(obj["tool_call_id"], &callID)
	}
	typ = strings.ToLower(strings.TrimSpace(typ))
	switch typ {
	case "function_call", "custom_tool_call":
		var args string
		if rawArgs := bytes.TrimSpace(obj["arguments"]); len(rawArgs) > 0 {
			if json.Unmarshal(rawArgs, &args) != nil {
				args = compactOpenAIPrismJSON(rawArgs)
			}
		} else if rawInput := bytes.TrimSpace(obj["input"]); len(rawInput) > 0 {
			if json.Unmarshal(rawInput, &args) != nil {
				args = compactOpenAIPrismJSON(rawInput)
			}
		}
		if name == "" {
			name = "unknown_tool"
		}
		if prompt != nil && namespace != "" {
			candidate := flattenOpenAIWebPromptToolName(namespace, name)
			if _, ok := prompt.toolByName(candidate); ok {
				name = candidate
			}
		}
		callType := "function"
		if typ == "custom_tool_call" {
			callType = "custom"
		}
		call := apicompat.ChatToolCall{ID: callID, Type: callType, Function: apicompat.ChatFunctionCall{Name: name, Arguments: args}}
		text := ""
		if prompt != nil {
			text = "Previous assistant tool call: " + prompt.EncodeAssistantToolCalls([]apicompat.ChatToolCall{call})
		} else {
			text = fmt.Sprintf("Previous assistant tool call: %s", compactOpenAIPrismJSON(raw))
		}
		return []any{openAIPrismPromptMessage("assistant", text)}, nil
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "mcp_tool_call_output":
		output := openAIPrismOutputText(obj["output"])
		if output == "" && obj["result"] != nil {
			output = openAIPrismOutputText(obj["result"])
		}
		text := output
		if prompt != nil {
			text = prompt.EncodeToolResult(callID, output)
		} else {
			text = fmt.Sprintf("Previous tool result (call_id=%s):\n%s", strings.TrimSpace(callID), output)
		}
		return []any{openAIPrismPromptMessage("user", text)}, nil
	case "message", "", "input_text", "output_text":
		content := bytes.TrimSpace(obj["content"])
		if (typ == "message" || typ == "") && len(content) > 0 && content[0] == '[' {
			// Native Prism file references (filename/project_path) remain native
			// content parts even when client tool history is text-bridged.
			return []any{json.RawMessage(append([]byte(nil), raw...))}, nil
		}
		if role == "" {
			role = "user"
		}
		text := openAIPrismMessageText(obj)
		if text == "" {
			text = compactOpenAIPrismJSON(raw)
		}
		return []any{openAIPrismPromptMessage(role, text)}, nil
	default:
		return []any{json.RawMessage(append([]byte(nil), raw...))}, nil
	}
}

func openAIPrismMessageText(obj map[string]json.RawMessage) string {
	content := bytes.TrimSpace(obj["content"])
	if len(content) == 0 || bytes.Equal(content, []byte("null")) {
		if raw := bytes.TrimSpace(obj["text"]); len(raw) > 0 {
			return openAIPrismOutputText(raw)
		}
		return ""
	}
	if content[0] == '"' {
		var text string
		if json.Unmarshal(content, &text) == nil {
			return text
		}
	}
	var parts []json.RawMessage
	if json.Unmarshal(content, &parts) == nil {
		var b strings.Builder
		for _, part := range parts {
			var value map[string]json.RawMessage
			if json.Unmarshal(part, &value) == nil {
				if text := openAIPrismOutputText(value["text"]); text != "" {
					_, _ = b.WriteString(text)
					continue
				}
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			_, _ = b.WriteString(compactOpenAIPrismJSON(part))
		}
		return b.String()
	}
	return compactOpenAIPrismJSON(content)
}

func openAIPrismOutputText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return compactOpenAIPrismJSON(raw)
}

func compactOpenAIPrismJSON(raw []byte) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err == nil {
		return compact.String()
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
	return strings.TrimSpace(string(raw))
}

// openAIPrismPromptReader buffers Prism's native Responses SSE until the
// terminal event.  Prompt tool validation is intentionally performed once on
// the complete text, after ParseResponse can validate nonce, schema and args.
type openAIPrismPromptReader struct {
	source            io.ReadCloser
	reader            *bufio.Reader
	prompt            *OpenAIWebPromptTools
	output            bytes.Buffer
	text              strings.Builder
	responseID        string
	model             string
	createdAt         int64
	sequence          int
	usage             any
	completed         map[string]any
	failedPayload     []byte
	failedEvent       string
	terminal          bool
	finished          bool
	closed            bool
	sourceErr         error
	returnedSourceErr bool
}

func newOpenAIPrismPromptToolBody(source io.ReadCloser, prompt *OpenAIWebPromptTools) io.ReadCloser {
	if source == nil {
		source = io.NopCloser(strings.NewReader(""))
	}
	return &openAIPrismPromptReader{source: source, reader: bufio.NewReaderSize(source, 64*1024), prompt: prompt, createdAt: time.Now().Unix(), responseID: "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")}
}

func (r *openAIPrismPromptReader) Close() error {
	if r == nil || r.closed {
		return nil
	}
	r.closed = true
	if r.source != nil {
		return r.source.Close()
	}
	return nil
}

func (r *openAIPrismPromptReader) Read(p []byte) (int, error) {
	if r == nil || len(p) == 0 || r.closed {
		if r != nil && len(p) == 0 && !r.closed {
			return 0, nil
		}
		return 0, io.EOF
	}
	for r.output.Len() == 0 && !r.finished {
		frame, err := readOpenAIWebSSEFrame(r.reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				r.finish()
				break
			}
			r.sourceErr = err
			r.terminal = true
			r.finish()
			break
		}
		r.consume(frame)
	}
	if r.output.Len() > 0 {
		return r.output.Read(p)
	}
	if r.sourceErr != nil && !r.returnedSourceErr {
		r.returnedSourceErr = true
		return 0, r.sourceErr
	}
	return 0, io.EOF
}

func (r *openAIPrismPromptReader) consume(frame openAIWebSSEFrame) {
	data := strings.TrimSpace(frame.data)
	if data == "[DONE]" {
		r.finish()
		return
	}
	if data == "" {
		return
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		r.sourceErr = fmt.Errorf("decode Prism Responses SSE: %w", err)
		r.terminal = true
		r.finish()
		return
	}
	typ := strings.TrimSpace(frame.event)
	if typ == "" {
		if candidate, ok := value["type"].(string); ok {
			typ = candidate
		}
	}
	if response, ok := value["response"].(map[string]any); ok {
		r.captureResponse(response)
	}
	if usage, ok := value["usage"]; ok {
		r.usage = usage
	}
	if errorValue, ok := value["error"]; ok {
		hasError := false
		switch current := errorValue.(type) {
		case nil:
		case string:
			hasError = strings.TrimSpace(current) != ""
		default:
			hasError = true
		}
		if hasError {
			r.failedEvent = "response.failed"
			r.failedPayload, _ = json.Marshal(value)
			r.terminal = true
		}
	}
	if id, ok := value["response_id"].(string); ok && strings.TrimSpace(id) != "" {
		r.responseID = id
	}
	switch typ {
	case "response.created":
		if response, ok := value["response"].(map[string]any); ok {
			r.captureResponse(response)
		}
	case "response.output_text.delta":
		if delta, ok := value["delta"].(string); ok {
			_, _ = r.text.WriteString(delta)
		}
	case "response.output_text.done":
		if text, ok := value["text"].(string); ok && r.text.Len() == 0 {
			_, _ = r.text.WriteString(text)
		}
	case "response.output_item.done":
		r.captureMessageItem(value["item"])
	case "response.completed":
		if response, ok := value["response"].(map[string]any); ok {
			if status, _ := response["status"].(string); strings.EqualFold(status, "failed") || strings.EqualFold(status, "incomplete") {
				r.failedEvent = "response." + strings.ToLower(strings.TrimSpace(status))
				r.failedPayload, _ = json.Marshal(value)
			}
		}
		r.terminal = true
	case "response.failed", "response.incomplete":
		r.failedEvent = typ
		r.failedPayload, _ = json.Marshal(value)
		r.terminal = true
	case "error", "response.error":
		r.failedEvent = "response.failed"
		r.failedPayload, _ = json.Marshal(value)
		r.terminal = true
	}
}

func (r *openAIPrismPromptReader) captureResponse(response map[string]any) {
	if response == nil {
		return
	}
	if id, ok := response["id"].(string); ok && strings.TrimSpace(id) != "" {
		r.responseID = id
	}
	if model, ok := response["model"].(string); ok && strings.TrimSpace(model) != "" {
		r.model = model
	}
	if created, ok := response["created_at"].(float64); ok && created > 0 {
		r.createdAt = int64(created)
	}
	if usage, ok := response["usage"]; ok {
		r.usage = usage
	}
	if r.completed == nil {
		r.completed = make(map[string]any)
	}
	for key, value := range response {
		r.completed[key] = value
	}
}

func (r *openAIPrismPromptReader) captureMessageItem(raw any) {
	item, ok := raw.(map[string]any)
	if !ok {
		return
	}
	if r.text.Len() == 0 {
		if content, ok := item["content"].([]any); ok {
			for _, part := range content {
				if p, ok := part.(map[string]any); ok {
					if text, ok := p["text"].(string); ok {
						_, _ = r.text.WriteString(text)
					}
				}
			}
		}
	}
}

func (r *openAIPrismPromptReader) finish() {
	if r == nil || r.finished {
		return
	}
	r.finished = true
	if r.sourceErr != nil {
		r.emitFailed("upstream_error", redactOpenAIWebSecret(r.sourceErr.Error()))
		return
	}
	if len(r.failedPayload) > 0 {
		event := r.failedEvent
		if event == "" {
			event = "response.failed"
		}
		fmt.Fprintf(&r.output, "event: %s\ndata: %s\n\n", event, r.failedPayload)
		_, _ = r.output.WriteString("data: [DONE]\n\n")
		return
	}
	if !r.terminal {
		r.emitFailed("upstream_incomplete_response", "Prism stream ended before a terminal response")
		return
	}
	text := r.text.String()
	if r.prompt != nil {
		calls, recognized, err := r.prompt.ParseResponse(text)
		if err == nil && recognized && len(calls) == 0 {
			err = errors.New("prompt tool envelope contained no calls")
		}
		if err == nil && !recognized && (r.prompt.Choice == "required" || r.prompt.ChoiceName != "") {
			err = errors.New("model did not satisfy the required tool_choice")
		}
		if err != nil {
			r.emitFailed("tool_protocol_error", redactOpenAIWebSecret(err.Error()))
			return
		}
		if recognized {
			r.emitPromptCalls(calls)
			return
		}
	}
	if text == "" {
		r.emitFailed("upstream_empty_response", "Prism upstream completed without assistant text")
		return
	}
	r.emitMessage(text)
}

func (r *openAIPrismPromptReader) responseObject(status string, output []any) map[string]any {
	if output == nil {
		output = []any{}
	}
	response := map[string]any{"id": r.responseID, "object": "response", "created_at": r.createdAt, "status": status, "model": r.model, "output": output}
	if r.completed != nil {
		for _, key := range []string{"service_tier", "parallel_tool_calls", "incomplete_details"} {
			if value, ok := r.completed[key]; ok {
				response[key] = value
			}
		}
	}
	if r.usage != nil {
		response["usage"] = r.usage
	}
	return response
}

func (r *openAIPrismPromptReader) emit(typ string, payload map[string]any) {
	payload["type"] = typ
	payload["sequence_number"] = r.sequence
	r.sequence++
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(&r.output, "event: %s\ndata: %s\n\n", typ, encoded)
}

func (r *openAIPrismPromptReader) emitFailed(code, message string) {
	response := r.responseObject("failed", nil)
	response["error"] = map[string]any{"code": code, "message": message}
	r.emit("response.failed", map[string]any{"response": response})
	_, _ = r.output.WriteString("data: [DONE]\n\n")
}

func (r *openAIPrismPromptReader) emitMessage(text string) {
	itemID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	item := map[string]any{"id": itemID, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}
	r.emit("response.created", map[string]any{"response": r.responseObject("in_progress", nil)})
	r.emit("response.output_item.added", map[string]any{"response_id": r.responseID, "output_index": 0, "item": item})
	r.emit("response.content_part.added", map[string]any{"response_id": r.responseID, "item_id": itemID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}, "logprobs": []any{}}})
	r.emit("response.output_text.delta", map[string]any{"response_id": r.responseID, "item_id": itemID, "output_index": 0, "content_index": 0, "delta": text})
	part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{}}
	r.emit("response.output_text.done", map[string]any{"response_id": r.responseID, "item_id": itemID, "output_index": 0, "content_index": 0, "text": text})
	r.emit("response.content_part.done", map[string]any{"response_id": r.responseID, "item_id": itemID, "output_index": 0, "content_index": 0, "part": part})
	item["status"] = "completed"
	item["content"] = []any{part}
	r.emit("response.output_item.done", map[string]any{"response_id": r.responseID, "output_index": 0, "item": item})
	r.emit("response.completed", map[string]any{"response": r.responseObject("completed", []any{item})})
	_, _ = r.output.WriteString("data: [DONE]\n\n")
}

func (r *openAIPrismPromptReader) emitPromptCalls(calls []OpenAIWebPromptToolCall) {
	items := make([]any, 0, len(calls))
	r.emit("response.created", map[string]any{"response": r.responseObject("in_progress", nil)})
	for index, call := range calls {
		itemID := "fc_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		callID := "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		value, itemType, field, deltaEvent, doneEvent := string(call.Arguments), "function_call", "arguments", "response.function_call_arguments.delta", "response.function_call_arguments.done"
		if call.Type == "custom" {
			value, itemType, field, deltaEvent, doneEvent = call.Input, "custom_tool_call", "input", "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done"
		}
		item := map[string]any{"id": itemID, "call_id": callID, "name": call.TargetName, "type": itemType, "status": "in_progress", field: ""}
		if item["name"] == "" {
			item["name"] = call.Name
		}
		if call.Namespace != "" {
			item["namespace"] = call.Namespace
		}
		r.emit("response.output_item.added", map[string]any{"response_id": r.responseID, "output_index": index, "item": item})
		if value != "" {
			r.emit(deltaEvent, map[string]any{"response_id": r.responseID, "item_id": itemID, "output_index": index, "call_id": callID, "name": item["name"], "delta": value})
		}
		r.emit(doneEvent, map[string]any{"response_id": r.responseID, "item_id": itemID, "output_index": index, "call_id": callID, "name": item["name"], field: value})
		item["status"] = "completed"
		item[field] = value
		r.emit("response.output_item.done", map[string]any{"response_id": r.responseID, "output_index": index, "item": item})
		items = append(items, item)
	}
	r.emit("response.completed", map[string]any{"response": r.responseObject("completed", items)})
	_, _ = r.output.WriteString("data: [DONE]\n\n")
}
