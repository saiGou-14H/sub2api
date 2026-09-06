package service

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Prefixes are provisional: only finish() may validate the complete envelope
// and publish executable output_item.done events. JSON framing uses the standard
// decoder; the sole incomplete scalar we expose is a safely decoded string.
type openAIWebJSONPrefix struct {
	raw      string
	fields   map[string]*openAIWebJSONPrefix
	elements []*openAIWebJSONPrefix
	complete bool
}

func readOpenAIWebJSONPrefix(dec *json.Decoder, text string, depth int) (*openAIWebJSONPrefix, error) {
	if depth > openAIWebPromptToolMaxDepth+4 {
		return nil, errors.New("prompt tool envelope exceeds depth limit")
	}
	start := int(dec.InputOffset())
	for start < len(text) && strings.ContainsRune(" \t\r\n:,", rune(text[start])) {
		start++
	}
	node := &openAIWebJSONPrefix{raw: text[start:]}
	token, err := dec.Token()
	if err != nil {
		return node, err
	}
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			node.fields = make(map[string]*openAIWebJSONPrefix)
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return node, err
				}
				name, ok := key.(string)
				if !ok {
					return node, errors.New("invalid prompt tool object key")
				}
				if _, exists := node.fields[name]; exists {
					return node, errors.New("duplicate prompt tool JSON key")
				}
				child, err := readOpenAIWebJSONPrefix(dec, text, depth+1)
				node.fields[name] = child
				if err != nil {
					return node, err
				}
			}
		case '[':
			for dec.More() {
				child, err := readOpenAIWebJSONPrefix(dec, text, depth+1)
				node.elements = append(node.elements, child)
				if err != nil {
					return node, err
				}
			}
		default:
			return node, errors.New("invalid prompt tool JSON delimiter")
		}
		if _, err := dec.Token(); err != nil {
			return node, err
		}
	}
	node.raw = text[start:dec.InputOffset()]
	node.complete = true
	return node, nil
}

func (n *openAIWebJSONPrefix) stringValue() (string, bool) {
	if n == nil || !n.complete {
		return "", false
	}
	var value string
	err := json.Unmarshal([]byte(n.raw), &value)
	return value, err == nil
}

func (n *openAIWebJSONPrefix) stringPrefix() (string, bool) {
	if n == nil || !strings.HasPrefix(n.raw, `"`) {
		return "", false
	}
	if n.complete {
		return n.stringValue()
	}
	// Hold back incomplete escapes, UTF-8 characters, and UTF-16 surrogate
	// pairs. json.Unmarshal performs all actual JSON unescaping/validation.
	end := 1
	for end < len(n.raw) {
		if n.raw[end] == '\\' {
			size := 2
			if end+1 >= len(n.raw) {
				break
			}
			if n.raw[end+1] == 'u' {
				size = 6
				if end+size > len(n.raw) {
					break
				}
				hex := strings.ToLower(n.raw[end+2 : end+6])
				if hex >= "d800" && hex <= "dbff" {
					size = 12
				}
			}
			if end+size > len(n.raw) {
				break
			}
			end += size
			continue
		}
		if !utf8.FullRuneInString(n.raw[end:]) {
			break
		}
		_, size := utf8.DecodeRuneInString(n.raw[end:])
		end += size
	}
	var value string
	err := json.Unmarshal([]byte(n.raw[:end]+`"`), &value)
	return value, err == nil
}

type openAIWebPromptStreamCall struct {
	call   OpenAIWebPromptToolCall
	itemID string
	callID string
	sent   string
}

func (c *openAIWebPromptStreamCall) item(status, value string) map[string]any {
	name := c.call.TargetName
	if name == "" {
		name = strings.TrimPrefix(c.call.Name, c.call.Namespace+"__")
	}
	typ, field := "function_call", "arguments"
	if c.call.Type == "custom" {
		typ, field = "custom_tool_call", "input"
	}
	item := map[string]any{"id": c.itemID, "call_id": c.callID, "name": name,
		"type": typ, "status": status, field: value}
	if c.call.Namespace != "" {
		item["namespace"] = c.call.Namespace
	}
	return item
}

func (r *openAIWebResponsesReader) emitPromptCallPrefix(index int, call OpenAIWebPromptToolCall, value string) error {
	if index > len(r.promptStreamCalls) {
		return errors.New("prompt tool stream has a missing call")
	}
	if index == len(r.promptStreamCalls) {
		state := openAIWebPromptStreamCall{call: call,
			itemID: "fc_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			callID: "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")}
		r.promptStreamCalls = append(r.promptStreamCalls, state)
		r.emit("response.output_item.added", map[string]any{
			"response_id": r.responseID, "output_index": index, "item": state.item("in_progress", ""),
		})
	}
	state := &r.promptStreamCalls[index]
	if call.Name != state.call.Name || call.Type != state.call.Type || call.Namespace != state.call.Namespace || !strings.HasPrefix(value, state.sent) {
		return errors.New("prompt tool stream rewrote an emitted call")
	}
	delta := value[len(state.sent):]
	if delta != "" {
		event := "response.function_call_arguments.delta"
		if call.Type == "custom" {
			event = "response.custom_tool_call_input.delta"
		}
		r.emit(event, map[string]any{"response_id": r.responseID, "item_id": state.itemID,
			"output_index": index, "call_id": state.callID, "name": state.item("in_progress", "")["name"], "delta": delta})
		state.sent = value
	}
	return nil
}

func (r *openAIWebResponsesReader) streamPromptToolPrefix() error {
	text := strings.TrimSpace(r.text)
	if !strings.HasPrefix(text, "{") {
		if len(r.promptStreamCalls) > 0 {
			return errors.New("prompt tool stream replaced its envelope")
		}
		return nil // Prose/fenced legacy envelopes retain buffered classification.
	}
	if len(text) > (openAIWebPromptToolMaxBytes+4096)*openAIWebPromptToolMaxCalls {
		return errors.New("prompt tool envelope exceeds size limit")
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	root, err := readOpenAIWebJSONPrefix(dec, text, 0)
	if root == nil {
		return err
	}
	_, recognized := root.fields["protocol"]
	r.promptEnvelopeSeen = r.promptEnvelopeSeen || recognized
	if !recognized {
		return nil
	}
	// A decoder reports unterminated strings/objects as SyntaxError rather
	// than io.ErrUnexpectedEOF. Once the protocol marker is present, every
	// such error is treated as an incomplete prefix; ParseResponse performs
	// the strict final validation when the upstream terminal frame arrives.
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !r.promptEnvelopeSeen {
		return nil
	}
	if root.complete && strings.TrimSpace(text[dec.InputOffset():]) != "" {
		return errors.New("unexpected text after prompt tool envelope")
	}
	p := r.promptTools
	for key, expected := range map[string]string{"protocol": p.Protocol, "nonce": p.Nonce, "schema_hash": p.SchemaHash} {
		value, complete := root.fields[key].stringValue()
		if !complete {
			return nil
		}
		if value != expected {
			return errors.New("prompt tool envelope nonce, protocol, or schema hash mismatch")
		}
	}
	// Require explicit start signals for speculative streaming. Legacy envelopes
	// and out-of-order headers still work via final validation.
	for key, expected := range map[string]string{"event": openAIWebPromptToolEvent, "start": openAIWebPromptToolStartSignal} {
		value, complete := root.fields[key].stringValue()
		if !complete {
			return nil
		}
		if value != expected {
			return errors.New("invalid prompt tool start signal")
		}
	}
	if end, complete := root.fields["end"].stringValue(); complete && end != openAIWebPromptToolEndSignal {
		return errors.New("invalid prompt tool end signal")
	}
	calls := root.fields["calls"]
	if calls == nil {
		calls = root.fields["tools"]
	} else if root.fields["tools"] != nil {
		return errors.New("prompt tool envelope must not contain both calls and tools")
	}
	if calls == nil {
		return nil
	}
	if len(calls.elements) > openAIWebPromptToolMaxCalls {
		return errors.New("prompt tool envelope exceeds call count limit")
	}
	for index, node := range calls.elements {
		if node == nil {
			break
		}
		name, complete := node.fields["name"].stringValue()
		if !complete {
			break
		}
		tool, ok := p.toolByName(name)
		if !ok || p.Choice == "none" || (p.ChoiceName != "" && p.ChoiceName != name) {
			return errors.New("prompt tool stream selected a disallowed tool")
		}
		if typ, ok := node.fields["type"].stringValue(); ok && typ != "" && !strings.EqualFold(strings.TrimSpace(typ), tool.Type) {
			return errors.New("prompt tool stream type mismatch")
		}
		if ns, ok := node.fields["namespace"].stringValue(); ok && ns != "" && ns != tool.Namespace {
			return errors.New("prompt tool stream namespace mismatch")
		}
		value := ""
		if tool.Type == "custom" {
			input := node.fields["input"]
			if input == nil {
				input = node.fields["arguments"]
				if input != nil && input.fields != nil {
					input = input.fields["input"]
				}
			}
			value, ok = input.stringPrefix()
			if !ok {
				break
			}
		} else {
			args := node.fields["arguments"]
			if args == nil {
				break
			}
			if strings.HasPrefix(args.raw, `"`) {
				value, ok = args.stringPrefix()
				if !ok {
					break
				}
				value = strings.TrimLeft(value, " \t\r\n")
				if args.complete {
					value = strings.TrimSpace(value)
				}
			} else if strings.HasPrefix(args.raw, "{") {
				value = args.raw
			} else {
				break
			}
		}
		if len(value) > openAIWebPromptToolMaxBytes {
			return errors.New("prompt tool arguments exceed size limit")
		}
		call := OpenAIWebPromptToolCall{Name: tool.Name, TargetName: tool.TargetName, Type: tool.Type, Namespace: tool.Namespace}
		if err := r.emitPromptCallPrefix(index, call, value); err != nil {
			return err
		}
	}
	return nil
}

func (r *openAIWebResponsesReader) failPromptStream(message string) {
	r.failed = true
	r.start()
	response := r.responseObject("failed", nil)
	response["error"] = map[string]any{"code": "tool_protocol_error", "message": message}
	r.emit("response.failed", map[string]any{"response": response})
	r.finished = true
	_, _ = r.output.WriteString("data: [DONE]\n\n")
}
