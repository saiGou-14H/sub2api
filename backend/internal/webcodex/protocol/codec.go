// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9.
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

var rawType = reflect.TypeFor[json.RawMessage]()

// decodeObject implements serde's required/default/Option distinction. Unknown
// members remain ignored unless the original struct uses deny_unknown_fields.
// Field-by-field decoding also avoids Go embedded-Unmarshaler flattening traps.
func decodeObject(data []byte, out any, closed bool) error {
	if err := validateJSONUnicode(data); err != nil {
		return err
	}
	v := reflect.ValueOf(out).Elem()
	fields := map[string]reflect.Value{}
	tags := map[string]reflect.StructField{}
	var collect func(reflect.Value)
	collect = func(v reflect.Value) {
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.Anonymous {
				collect(v.Field(i))
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			fields[name] = v.Field(i)
			tags[name] = f
		}
	}
	collect(v)
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return fmt.Errorf("expected JSON object")
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return err
		}
		name := token.(string)
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return err
		}
		field, known := fields[name]
		if !known {
			if closed {
				return fmt.Errorf("unknown field %q", name)
			}
			continue
		}
		if seen[name] {
			return fmt.Errorf("duplicate field %q", name)
		}
		seen[name] = true
		null := bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
		if null {
			if field.Kind() != reflect.Pointer && field.Type() != rawType {
				return fmt.Errorf("%s cannot be null", name)
			}
			field.SetZero()
			continue
		}
		if shape := tags[name].Tag.Get("shape"); shape != "" {
			first := bytes.TrimSpace(raw)[0]
			if (shape == "object" && first != '{') || (shape == "array" && first != '[') {
				return fmt.Errorf("%s must be %s or null", name, shape)
			}
		}
		if err = json.Unmarshal(raw, field.Addr().Interface()); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		// serde Vec<String> rejects null elements (encoding/json otherwise makes "").
		if field.Kind() == reflect.Slice && field.Type() != rawType && field.Type().Elem().Kind() == reflect.String {
			var elems []json.RawMessage
			_ = json.Unmarshal(raw, &elems)
			for _, e := range elems {
				if bytes.Equal(e, []byte("null")) {
					return fmt.Errorf("%s cannot contain null", name)
				}
			}
		}
	}
	if _, err = d.Token(); err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON object")
	}
	for name, f := range tags {
		if f.Tag.Get("wire") == "required" && !seen[name] {
			return fmt.Errorf("missing required field %q", name)
		}
	}
	return nil
}

func (x *RunnerCapabilities) UnmarshalJSON(b []byte) error {
	type plain RunnerCapabilities
	v := plain{Shell: true}
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerCapabilities(v)
	return nil
}
func (x *RunnerBuildInfo) UnmarshalJSON(b []byte) error {
	type plain RunnerBuildInfo
	var v plain
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerBuildInfo(v)
	return nil
}
func (x *RunnerHostContext) UnmarshalJSON(b []byte) error {
	type plain RunnerHostContext
	var v plain
	if err := decodeObject(b, &v, true); err != nil {
		return err
	}
	*x = RunnerHostContext(v)
	return nil
}
func (x *RunnerRegisterRequest) UnmarshalJSON(b []byte) error {
	type plain RunnerRegisterRequest
	var v plain
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(b, &fields)
	var caps map[string]json.RawMessage
	_ = json.Unmarshal(fields["capabilities"], &caps)
	if _, ok := caps["shell"]; !ok {
		return fmt.Errorf("registration capabilities requires explicit shell")
	}
	*x = RunnerRegisterRequest(v)
	return nil
}
func (x *RunnerView) UnmarshalJSON(b []byte) error {
	type plain RunnerView
	v := plain{Transport: "polling", Projects: []RunnerProjectSummary{}}
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerView(v)
	return nil
}
func (x *RunnerProjectSummary) UnmarshalJSON(b []byte) error {
	type plain RunnerProjectSummary
	v := plain{AllowPatch: true, Hooks: []string{}}
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerProjectSummary(v)
	return nil
}
func (x *RunnerRegisterResponse) UnmarshalJSON(b []byte) error {
	type plain RunnerRegisterResponse
	var v plain
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerRegisterResponse(v)
	return nil
}
func (x *RunnerPollRequest) UnmarshalJSON(b []byte) error {
	type plain RunnerPollRequest
	var v plain
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerPollRequest(v)
	return nil
}
func (x *RunnerPollPayload) UnmarshalJSON(b []byte) error {
	type plain RunnerPollPayload
	var v plain
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerPollPayload(v)
	return nil
}
func (x *RunnerPollResponse) UnmarshalJSON(b []byte) error {
	type plain RunnerPollResponse
	var v plain
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerPollResponse(v)
	return nil
}
func (x *RunnerOfflineRequest) UnmarshalJSON(b []byte) error {
	type plain RunnerOfflineRequest
	var v plain
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerOfflineRequest(v)
	return nil
}
func (x *RunnerOfflineResponse) UnmarshalJSON(b []byte) error {
	type plain RunnerOfflineResponse
	var v plain
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerOfflineResponse(v)
	return nil
}
func (x *RunnerResultResponse) UnmarshalJSON(b []byte) error {
	type plain RunnerResultResponse
	var v plain
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerResultResponse(v)
	return nil
}
func (x *RunnerResultRequest) UnmarshalJSON(b []byte) error {
	type plain RunnerResultRequest
	var v plain
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerResultRequest(v)
	return nil
}
func (x *RunnerResultPayload) UnmarshalJSON(b []byte) error {
	type plain RunnerResultPayload
	var v plain
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerResultPayload(v)
	return nil
}
func (x *RunnerRequest) UnmarshalJSON(b []byte) error {
	type plain RunnerRequest
	v := plain{Kind: "run_shell"}
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = RunnerRequest(v)
	return nil
}
func (x *ShellProcessArgv) UnmarshalJSON(b []byte) error {
	type plain ShellProcessArgv
	v := plain{Args: []string{}}
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = ShellProcessArgv(v)
	return nil
}
func (x *ShellScriptPayload) UnmarshalJSON(b []byte) error {
	type plain ShellScriptPayload
	v := plain{Args: []string{}}
	if err := decodeObject(b, &v, false); err != nil {
		return err
	}
	*x = ShellScriptPayload(v)
	return nil
}
func (x *ShellScriptLanguage) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	switch ShellScriptLanguage(s) {
	case ScriptSh, ScriptBash, ScriptPowershell:
		*x = ShellScriptLanguage(s)
		return nil
	}
	return fmt.Errorf("unknown script language")
}
func (x *ShellCommandExecutionState) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	switch ShellCommandExecutionState(s) {
	case CommandNotStarted, CommandOutcomeUnknown, CommandTimedOut, CommandCompleted:
		*x = ShellCommandExecutionState(s)
		return nil
	}
	return fmt.Errorf("unknown command_execution_state")
}

func read[T any](b []byte) (T, error) { var v T; err := json.Unmarshal(b, &v); return v, err }
func ReadRegisterRequest(b []byte) (RunnerRegisterRequest, error) {
	return read[RunnerRegisterRequest](b)
}
func ReadRequest(b []byte) (RunnerRequest, error)               { return read[RunnerRequest](b) }
func ReadPollRequest(b []byte) (RunnerPollRequest, error)       { return read[RunnerPollRequest](b) }
func ReadPollPayload(b []byte) (RunnerPollPayload, error)       { return read[RunnerPollPayload](b) }
func ReadResultRequest(b []byte) (RunnerResultRequest, error)   { return read[RunnerResultRequest](b) }
func ReadResultPayload(b []byte) (RunnerResultPayload, error)   { return read[RunnerResultPayload](b) }
func ReadOfflineRequest(b []byte) (RunnerOfflineRequest, error) { return read[RunnerOfflineRequest](b) }
func DecodeRequest(b []byte) (Invocation, error) {
	r, err := ReadRequest(b)
	if err != nil {
		return Invocation{}, err
	}
	return r.DecodeInvocation()
}
