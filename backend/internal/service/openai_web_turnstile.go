package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The browser reference treats a challenge program that produces no output
// as an empty optional token and continues the sentinel handshake. Keep this
// distinct from malformed payload errors so callers can mirror that behavior
// without hiding transport or decoding failures.
var errOpenAIWebTurnstileNoToken = errors.New("turnstile challenge did not produce a token")

// SolveOpenAIWebTurnstileToken evaluates the small, data-driven Turnstile
// program returned in the chat-requirements prepare response. The reference
// ChatGPT Web client decodes dx with the exact p token it submitted and then
// executes a deliberately limited set of browser-like operations. Keeping the
// evaluator local avoids a JavaScript runtime and, importantly, never logs or
// retains the access token.
func SolveOpenAIWebTurnstileToken(dx, p string) (string, error) {
	dx = strings.TrimSpace(dx)
	if dx == "" {
		return "", errors.New("turnstile challenge payload is empty")
	}
	raw, err := decodeOpenAIWebTurnstileBase64(dx)
	if err != nil {
		return "", errors.New("turnstile challenge payload is invalid")
	}
	decoded := string(raw)
	if !utf8.ValidString(decoded) {
		return "", errors.New("turnstile challenge payload is not UTF-8")
	}
	programJSON := openAIWebTurnstileXOR(decoded, p)
	var program []any
	if err := json.Unmarshal([]byte(programJSON), &program); err != nil {
		return "", errors.New("turnstile challenge program is invalid")
	}

	vm := newOpenAIWebTurnstileVM(p, program)
	vm.run()
	if vm.result == "" {
		return "", errOpenAIWebTurnstileNoToken
	}
	return vm.result, nil
}

// BuildOpenAIWebTurnstileToken is kept as a descriptive alias for embedders
// that use the same naming convention as the requirements-token builder.
func BuildOpenAIWebTurnstileToken(dx, p string) (string, error) {
	return SolveOpenAIWebTurnstileToken(dx, p)
}

type openAIWebTurnstileCallable struct {
	opcode int
}

type openAIWebTurnstileOrderedMap struct {
	keys   []string
	values map[string]any
}

func newOpenAIWebTurnstileOrderedMap() *openAIWebTurnstileOrderedMap {
	return &openAIWebTurnstileOrderedMap{values: make(map[string]any)}
}

func (m *openAIWebTurnstileOrderedMap) add(key string, value any) {
	if _, exists := m.values[key]; !exists {
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

type openAIWebTurnstileVM struct {
	values    map[float64]any
	program   []any
	result    string
	startedAt time.Time
}

func newOpenAIWebTurnstileVM(p string, program []any) *openAIWebTurnstileVM {
	vm := &openAIWebTurnstileVM{
		values:    make(map[float64]any),
		program:   program,
		startedAt: time.Now(),
	}
	for _, opcode := range []int{1, 2, 3, 5, 6, 7, 8, 14, 15, 17, 18, 19, 20, 21, 23, 24} {
		vm.values[float64(opcode)] = openAIWebTurnstileCallable{opcode: opcode}
	}
	vm.values[9] = program
	vm.values[10] = "window"
	vm.values[16] = p
	return vm
}

func (vm *openAIWebTurnstileVM) run() {
	for _, rawInstruction := range vm.program {
		instruction, ok := rawInstruction.([]any)
		if !ok || len(instruction) == 0 {
			continue
		}
		// The challenge program copies opcode callables into randomized
		// registers, so instruction[0] is not necessarily an integer opcode
		// after the first few instructions (for example, 41.31). Resolve the
		// numeric register first and execute the callable stored there.
		opcodeRegister, ok := openAIWebTurnstileNumber(instruction[0])
		if !ok {
			continue
		}
		callable, ok := vm.values[opcodeRegister].(openAIWebTurnstileCallable)
		if !ok {
			continue
		}
		// The browser evaluator intentionally ignores an individual malformed
		// instruction and continues with the remaining program.
		_, _ = vm.execute(callable.opcode, instruction[1:])
	}
}

func (vm *openAIWebTurnstileVM) execute(opcode int, args []any) (any, error) {
	register := func(value any) (any, error) {
		key, ok := openAIWebTurnstileNumber(value)
		if !ok {
			return nil, errors.New("invalid turnstile register")
		}
		return vm.values[key], nil
	}
	setRegister := func(value, stored any) error {
		key, ok := openAIWebTurnstileNumber(value)
		if !ok {
			return errors.New("invalid turnstile register")
		}
		vm.values[key] = stored
		return nil
	}

	switch opcode {
	case 1: // XOR two register values.
		if len(args) < 2 {
			return nil, errors.New("turnstile xor arguments are incomplete")
		}
		left, _ := register(args[0])
		right, _ := register(args[1])
		return nil, setRegister(args[0], openAIWebTurnstileXOR(openAIWebTurnstileString(left), openAIWebTurnstileString(right)))

	case 2: // Set a register to an immediate value.
		if len(args) < 2 {
			return nil, errors.New("turnstile assignment arguments are incomplete")
		}
		return nil, setRegister(args[0], args[1])

	case 3: // Emit base64(value). The value is passed directly, not dereferenced.
		if len(args) < 1 {
			return nil, errors.New("turnstile output argument is missing")
		}
		value, ok := args[0].(string)
		if !ok {
			return nil, errors.New("turnstile output value is not a string")
		}
		vm.result = base64.StdEncoding.EncodeToString([]byte(value))
		return nil, nil

	case 5: // Append a list item or concatenate string-like values.
		if len(args) < 2 {
			return nil, errors.New("turnstile append arguments are incomplete")
		}
		current, _ := register(args[0])
		incoming, _ := register(args[1])
		if list, ok := current.([]any); ok {
			vm.values[openAIWebTurnstileRegister(args[0])] = append(append([]any(nil), list...), incoming)
			return nil, nil
		}
		if openAIWebTurnstileStringLike(current) || openAIWebTurnstileStringLike(incoming) {
			return nil, setRegister(args[0], openAIWebTurnstileString(current)+openAIWebTurnstileString(incoming))
		}
		return nil, setRegister(args[0], "NaN")

	case 6: // Build a dotted property name, with the document.location alias.
		if len(args) < 3 {
			return nil, errors.New("turnstile property arguments are incomplete")
		}
		left, _ := register(args[1])
		right, _ := register(args[2])
		leftString, leftOK := left.(string)
		rightString, rightOK := right.(string)
		if leftOK && rightOK {
			value := leftString + "." + rightString
			if value == "window.document.location" {
				value = "https://chatgpt.com/"
			}
			return nil, setRegister(args[0], value)
		}
		return nil, nil

	case 7: // Call a register-held function with dereferenced arguments.
		if len(args) < 1 {
			return nil, errors.New("turnstile call target is missing")
		}
		target, _ := register(args[0])
		callArgs := make([]any, 0, len(args)-1)
		for _, arg := range args[1:] {
			value, _ := register(arg)
			callArgs = append(callArgs, value)
		}
		if targetName, ok := target.(string); ok && targetName == "window.Reflect.set" {
			if len(callArgs) < 3 {
				return nil, errors.New("turnstile Reflect.set arguments are incomplete")
			}
			object, ok := callArgs[0].(*openAIWebTurnstileOrderedMap)
			if !ok {
				return nil, errors.New("turnstile Reflect.set target is invalid")
			}
			object.add(openAIWebTurnstileString(callArgs[1]), callArgs[2])
			return nil, nil
		}
		return vm.invoke(target, callArgs)

	case 8: // Copy a register value.
		if len(args) < 2 {
			return nil, errors.New("turnstile copy arguments are incomplete")
		}
		value, _ := register(args[1])
		return nil, setRegister(args[0], value)

	case 14: // JSON.parse(register).
		if len(args) < 2 {
			return nil, errors.New("turnstile JSON.parse arguments are incomplete")
		}
		value, _ := register(args[1])
		text, ok := value.(string)
		if !ok {
			return nil, errors.New("turnstile JSON.parse input is not a string")
		}
		var parsed any
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			return nil, err
		}
		return nil, setRegister(args[0], parsed)

	case 15: // JSON.stringify(register).
		if len(args) < 2 {
			return nil, errors.New("turnstile JSON.stringify arguments are incomplete")
		}
		value, _ := register(args[1])
		encoded, err := openAIWebTurnstileJSONMarshal(value)
		if err != nil {
			return nil, err
		}
		return nil, setRegister(args[0], encoded)

	case 17: // Call a browser primitive or a register-held function.
		if len(args) < 2 {
			return nil, errors.New("turnstile invoke arguments are incomplete")
		}
		target, _ := register(args[1])
		callArgs := make([]any, 0, len(args)-2)
		for _, arg := range args[2:] {
			value, _ := register(arg)
			callArgs = append(callArgs, value)
		}
		switch target {
		case "window.performance.now":
			elapsedMillis := float64(time.Since(vm.startedAt).Nanoseconds())/1e6 + rand.Float64()
			return nil, setRegister(args[0], elapsedMillis)
		case "window.Object.create":
			return nil, setRegister(args[0], newOpenAIWebTurnstileOrderedMap())
		case "window.Math.random":
			return nil, setRegister(args[0], rand.Float64())
		case "window.Object.keys":
			if len(callArgs) > 0 && callArgs[0] == "window.localStorage" {
				return nil, setRegister(args[0], []any{
					"STATSIG_LOCAL_STORAGE_INTERNAL_STORE_V4",
					"STATSIG_LOCAL_STORAGE_STABLE_ID",
					"client-correlated-secret",
					"oai/apps/capExpiresAt",
					"oai-did",
					"STATSIG_LOCAL_STORAGE_LOGGING_REQUEST",
					"UiState.isNavigationCollapsed.1",
				})
			}
			return nil, nil
		default:
			if _, ok := target.(openAIWebTurnstileCallable); ok {
				value, invokeErr := vm.invoke(target, callArgs)
				if invokeErr != nil {
					return nil, invokeErr
				}
				return nil, setRegister(args[0], value)
			}
			return nil, nil
		}

	case 18: // Base64 decode a register value.
		if len(args) < 1 {
			return nil, errors.New("turnstile base64 decode argument is missing")
		}
		value, _ := register(args[0])
		decoded, err := decodeOpenAIWebTurnstileBase64(openAIWebTurnstileString(value))
		if err != nil || !utf8.Valid(decoded) {
			return nil, errors.New("turnstile base64 decode failed")
		}
		return nil, setRegister(args[0], string(decoded))

	case 19: // Base64 encode a register value.
		if len(args) < 1 {
			return nil, errors.New("turnstile base64 encode argument is missing")
		}
		value, _ := register(args[0])
		return nil, setRegister(args[0], base64.StdEncoding.EncodeToString([]byte(openAIWebTurnstileString(value))))

	case 20: // Conditional call; arguments are dereferenced before invocation.
		if len(args) < 3 {
			return nil, errors.New("turnstile conditional arguments are incomplete")
		}
		left, _ := register(args[0])
		right, _ := register(args[1])
		if !reflect.DeepEqual(left, right) {
			return nil, nil
		}
		target, _ := register(args[2])
		callArgs := make([]any, 0, len(args)-3)
		for _, arg := range args[3:] {
			value, _ := register(arg)
			callArgs = append(callArgs, value)
		}
		_, err := vm.invoke(target, callArgs)
		return nil, err

	case 21:
		return nil, nil

	case 23: // Conditional call with raw (non-dereferenced) arguments.
		if len(args) < 2 {
			return nil, errors.New("turnstile callback arguments are incomplete")
		}
		guard, _ := register(args[0])
		if guard == nil {
			return nil, nil
		}
		target, _ := register(args[1])
		_, err := vm.invoke(target, args[2:])
		return nil, err

	case 24: // Build a dotted property name.
		if len(args) < 3 {
			return nil, errors.New("turnstile dotted-property arguments are incomplete")
		}
		left, _ := register(args[1])
		right, _ := register(args[2])
		leftString, leftOK := left.(string)
		rightString, rightOK := right.(string)
		if leftOK && rightOK {
			return nil, setRegister(args[0], leftString+"."+rightString)
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported turnstile opcode %d", opcode)
	}
}

func (vm *openAIWebTurnstileVM) invoke(target any, args []any) (any, error) {
	callable, ok := target.(openAIWebTurnstileCallable)
	if !ok {
		return nil, nil
	}
	return vm.execute(callable.opcode, args)
}

func openAIWebTurnstileNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, !math.IsNaN(number) && !math.IsInf(number, 0)
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func openAIWebTurnstileInt(value any) (int, bool) {
	number, ok := openAIWebTurnstileNumber(value)
	if !ok || number != math.Trunc(number) || number < math.MinInt || number > math.MaxInt {
		return 0, false
	}
	return int(number), true
}

func openAIWebTurnstileRegister(value any) float64 {
	register, _ := openAIWebTurnstileNumber(value)
	return register
}

func openAIWebTurnstileString(value any) string {
	switch current := value.(type) {
	case nil:
		return "undefined"
	case string:
		switch current {
		case "window.Math":
			return "[object Math]"
		case "window.Reflect":
			return "[object Reflect]"
		case "window.performance":
			return "[object Performance]"
		case "window.localStorage":
			return "[object Storage]"
		case "window.Object":
			return "function Object() { [native code] }"
		case "window.Reflect.set":
			return "function set() { [native code] }"
		case "window.performance.now":
			return "function () { [native code] }"
		case "window.Object.create":
			return "function create() { [native code] }"
		case "window.Object.keys":
			return "function keys() { [native code] }"
		case "window.Math.random":
			return "function random() { [native code] }"
		default:
			return current
		}
	case float64:
		return strconv.FormatFloat(current, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(current), 'g', -1, 32)
	case int:
		return strconv.Itoa(current)
	case int64:
		return strconv.FormatInt(current, 10)
	case bool:
		if current {
			return "True"
		}
		return "False"
	case []string:
		return strings.Join(current, ",")
	case []any:
		allStrings := true
		parts := make([]string, 0, len(current))
		for _, item := range current {
			itemString, ok := item.(string)
			if !ok {
				allStrings = false
				break
			}
			parts = append(parts, itemString)
		}
		if allStrings {
			return strings.Join(parts, ",")
		}
		return fmt.Sprint(current)
	case json.Number:
		return current.String()
	default:
		return fmt.Sprint(value)
	}
}

func openAIWebTurnstileStringLike(value any) bool {
	switch value.(type) {
	case string, float64, float32, int, int64:
		return true
	default:
		return false
	}
}

func openAIWebTurnstileXOR(text, key string) string {
	if key == "" {
		return text
	}
	textRunes := []rune(text)
	keyRunes := []rune(key)
	for index := range textRunes {
		textRunes[index] ^= keyRunes[index%len(keyRunes)]
	}
	return string(textRunes)
}

func decodeOpenAIWebTurnstileBase64(value string) ([]byte, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return r
		}
	}, value)
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(cleaned)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid base64")
}

func openAIWebTurnstileJSONMarshal(value any) (string, error) {
	if _, ok := value.(*openAIWebTurnstileOrderedMap); ok {
		// Python's OrderedMap helper is intentionally not JSON serializable;
		// the reference evaluator catches this instruction-level exception.
		return "", errors.New("ordered map is not JSON serializable")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	encoded = openAIWebTurnstileEscapeNonASCII(encoded)
	var spaced bytes.Buffer
	inString := false
	escaped := false
	for _, character := range encoded {
		if inString {
			spaced.WriteByte(character)
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == '"' {
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
			spaced.WriteByte(character)
		case ',':
			spaced.WriteString(", ")
		case ':':
			spaced.WriteString(": ")
		default:
			spaced.WriteByte(character)
		}
	}
	return spaced.String(), nil
}

func openAIWebTurnstileEscapeNonASCII(value []byte) []byte {
	var output bytes.Buffer
	inString := false
	escaped := false
	for index := 0; index < len(value); {
		character := value[index]
		if !inString {
			output.WriteByte(character)
			if character == '"' {
				inString = true
			}
			index++
			continue
		}
		if escaped {
			output.WriteByte(character)
			escaped = false
			index++
			continue
		}
		if character == '\\' {
			output.WriteByte(character)
			escaped = true
			index++
			continue
		}
		if character == '"' {
			output.WriteByte(character)
			inString = false
			index++
			continue
		}
		if character < utf8.RuneSelf {
			output.WriteByte(character)
			index++
			continue
		}
		runeValue, size := utf8.DecodeRune(value[index:])
		if runeValue == utf8.RuneError && size == 1 {
			output.WriteByte(character)
			index++
			continue
		}
		if runeValue <= 0xffff {
			_, _ = fmt.Fprintf(&output, "\\u%04x", runeValue)
		} else {
			// JSON's ensure_ascii representation uses a UTF-16 surrogate pair.
			codePoint := runeValue - 0x10000
			high := 0xd800 + (codePoint >> 10)
			low := 0xdc00 + (codePoint & 0x3ff)
			_, _ = fmt.Fprintf(&output, "\\u%04x\\u%04x", high, low)
		}
		index += size
	}
	return output.Bytes()
}
