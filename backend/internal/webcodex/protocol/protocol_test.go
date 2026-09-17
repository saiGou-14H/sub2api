// SPDX-License-Identifier: Apache-2.0
// Cases ported from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9
// runner_operation.rs tests and runner-registry/src/tests/protocol.rs.
package protocol

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

const registerJSON = `{"client_id":"runner-1","agent_instance_id":"instance-1","agent_protocol_generation":2,"capabilities":{"shell":true}}`
const shellJSON = `{"request_id":"r1","client_id":"runner-1","command":"echo ok","timeout_secs":120,"requested_by":"alice","created_at":0}`

func ptr[T any](v T) *T { return &v }
func mutate(t *testing.T, base string, key string, value any, remove bool) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(base), &m); err != nil {
		t.Fatal(err)
	}
	if remove {
		delete(m, key)
	} else {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		m[key] = b
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func request(t *testing.T, kind string) RunnerRequest {
	t.Helper()
	r, err := ReadRequest([]byte(shellJSON))
	if err != nil {
		t.Fatal(err)
	}
	r.Kind = kind
	if kind != "run_shell" {
		r.Command = ""
	}
	return r
}

func TestRegistrationWireDefaultsAndPresence(t *testing.T) {
	r, err := ReadRegisterRequest([]byte(registerJSON))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Capabilities.Shell || r.Capabilities.FileRead || r.Capabilities.FileWrite || r.Capabilities.StructuredProcessArgv || r.JobConcurrencyLimit != nil || r.Policy != nil {
		t.Fatalf("invented defaults: %+v", r)
	}
	if err = ValidateRegistration(r); err != nil {
		t.Fatal(err)
	}
	if err = ValidateGenerationCapabilities(r.Capabilities); err == nil {
		t.Fatal("generation must not synthesize baseline bits")
	}
	for _, key := range []string{"client_id", "agent_instance_id", "agent_protocol_generation", "capabilities"} {
		for _, remove := range []bool{true, false} {
			t.Run(key+string(rune('0'+boolInt(remove))), func(t *testing.T) {
				if _, err := ReadRegisterRequest(mutate(t, registerJSON, key, nil, remove)); err == nil {
					t.Fatal("accepted missing/null required field")
				}
			})
		}
	}
	for _, caps := range []any{map[string]any{}, map[string]any{"shell": nil}, map[string]any{"shell": "true"}, true} {
		if _, err := ReadRegisterRequest(mutate(t, registerJSON, "capabilities", caps, false)); err == nil {
			t.Fatalf("accepted invalid capabilities %v", caps)
		}
	}
	r, err = ReadRegisterRequest(mutate(t, registerJSON, "capabilities", map[string]bool{"shell": false}, false))
	if err != nil || r.Capabilities.Shell {
		t.Fatalf("explicit false lost: %v", err)
	}
	var c RunnerCapabilities
	if err = json.Unmarshal([]byte(`{}`), &c); err != nil || !c.Shell || c.FileRead {
		t.Fatal("view capabilities serde defaults lost")
	}
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestRegistrationGenerationAndNumericRanges(t *testing.T) {
	for _, n := range []uint16{0, 1, 3, 65535} {
		r, err := ReadRegisterRequest(mutate(t, registerJSON, "agent_protocol_generation", n, false))
		if err != nil {
			t.Fatal(err)
		}
		if err = ValidateRegistration(r); err == nil {
			t.Fatalf("accepted unsupported generation %d", n)
		}
	}
	for _, raw := range []string{"-1", "65536", "2.5", "2e0", "\"2\""} {
		b := strings.Replace(registerJSON, `"agent_protocol_generation":2`, `"agent_protocol_generation":`+raw, 1)
		if _, err := ReadRegisterRequest([]byte(b)); err == nil {
			t.Fatalf("accepted generation %s", raw)
		}
	}
	for _, value := range []any{nil, uint64(0), uint64(1), uint64(64), uint64(65)} {
		r, err := ReadRegisterRequest(mutate(t, registerJSON, "job_concurrency_limit", value, false))
		if err != nil {
			t.Fatal(err)
		}
		err = ValidateRegistration(r)
		wantErr := value == uint64(0) || value == uint64(65)
		if (err != nil) != wantErr {
			t.Fatalf("concurrency %v: %v", value, err)
		}
	}
	r, err := ReadRegisterRequest(mutate(t, registerJSON, "process_started_at", int64(0), false))
	if err != nil || r.ProcessStartedAt == nil || *r.ProcessStartedAt != 0 {
		t.Fatal("explicit zero lost")
	}
}

func TestRegistrationMetadataShapesAndHostContext(t *testing.T) {
	// Job inventory's source-derived sequence form is covered by the Job tests.
	for _, key := range []string{"policy", "coding_agent_inventory", "build", "host_context"} {
		if _, err := ReadRegisterRequest(mutate(t, registerJSON, key, []any{}, false)); err == nil {
			t.Fatalf("accepted array for %s", key)
		}
	}
	if _, err := ReadRegisterRequest(mutate(t, registerJSON, "coding_agent_providers", map[string]any{}, false)); err == nil {
		t.Fatal("providers must be array")
	}
	if _, err := ReadRegisterRequest(mutate(t, registerJSON, "host_context", map[string]string{"claims": "bad"}, false)); err == nil {
		t.Fatal("host_context must be closed")
	}
	r, err := ReadRegisterRequest(mutate(t, registerJSON, "host_context", map[string]string{"role": " build_host ", "runtime": " Go "}, false))
	if err != nil {
		t.Fatal(err)
	}
	h, err := r.HostContext.Normalized()
	if err != nil || *h.Role != "build_host" || *h.Runtime != "Go" {
		t.Fatalf("normalize: %v", err)
	}
	for _, role := range []string{"", "Build", "a.b", strings.Repeat("x", 65)} {
		r.HostContext = &RunnerHostContext{Role: &role}
		if ValidateRegistration(r) == nil {
			t.Fatalf("accepted role %q", role)
		}
	}
}

func TestRequestRequiredNullDefaultAndIntegers(t *testing.T) {
	r, err := ReadRequest([]byte(shellJSON))
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "run_shell" || r.CreateDirs || r.CreatedAt != 0 {
		t.Fatal("defaults wrong")
	}
	for _, key := range []string{"request_id", "client_id", "command", "timeout_secs", "requested_by", "created_at"} {
		for _, remove := range []bool{true, false} {
			if _, err := ReadRequest(mutate(t, shellJSON, key, nil, remove)); err == nil {
				t.Fatalf("accepted required missing/null %s", key)
			}
		}
	}
	for _, key := range []string{"kind", "create_dirs"} {
		if _, err := ReadRequest(mutate(t, shellJSON, key, nil, false)); err == nil {
			t.Fatalf("accepted null %s", key)
		}
	}
	for _, key := range []string{"max_bytes", "start_line", "end_line"} {
		a, err := ReadRequest(mutate(t, shellJSON, key, uint64(0), false))
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(a)
		if err != nil || !strings.Contains(string(b), `"`+key+`":0`) {
			t.Fatalf("zero disappeared: %s %v", b, err)
		}
		if _, err = ReadRequest(mutate(t, shellJSON, key, nil, false)); err != nil {
			t.Fatal(err)
		}
	}
	r, err = ReadRequest(mutate(t, shellJSON, "timeout_secs", uint64(math.MaxUint64), false))
	if err != nil || r.TimeoutSecs != math.MaxUint64 {
		t.Fatal("u64 precision lost")
	}
	r, err = ReadRequest(mutate(t, shellJSON, "created_at", int64(math.MinInt64), false))
	if err != nil || r.CreatedAt != math.MinInt64 {
		t.Fatal("i64 precision lost")
	}
	for _, pair := range [][2]string{{"timeout_secs", "18446744073709551616"}, {"timeout_secs", "-1"}, {"created_at", "9223372036854775808"}, {"max_bytes", "-1"}} {
		raw := mutate(t, shellJSON, pair[0], json.RawMessage(pair[1]), false)
		if _, err := ReadRequest(raw); err == nil {
			t.Fatalf("accepted range overflow %v", pair)
		}
	}
	// RunnerRequest does not have deny_unknown_fields; request_kind is ignored.
	r, err = ReadRequest(mutate(t, shellJSON, "request_kind", "file_write", false))
	if err != nil || r.Kind != "run_shell" {
		t.Fatal("invented request_kind alias")
	}
	if _, err = ReadRequest([]byte(strings.TrimSuffix(shellJSON, "}") + `,"command":"evil"}`)); err == nil {
		t.Fatal("accepted duplicate known field")
	}
}

func TestTypedProcessPreservesArgvAndMandatoryEmptyCommand(t *testing.T) {
	r := request(t, "run_process")
	r.Process = &ShellProcessArgv{Executable: "tool", Args: []string{"a b", "single'quote", "\"double\"", "$HOME; echo NO", "猫", ""}}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"command":""`) {
		t.Fatal("empty command omitted")
	}
	inv, err := DecodeRequest(b)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := inv.Operation.(ProcessOperation)
	if !ok || !reflect.DeepEqual(p.Process.Args, r.Process.Args) {
		t.Fatal("argv was reinterpreted")
	}
	if inv.Metadata.RequestID != "r1" || inv.Metadata.RequestedBy != "alice" {
		t.Fatal("metadata lost")
	}
	if _, err = DecodeRequest(mutate(t, string(b), "command", nil, true)); err == nil {
		t.Fatal("typed request accepted missing command")
	}
	r.Command = " "
	if _, err = r.DecodeInvocation(); err == nil {
		t.Fatal("typed command must be exactly empty")
	}
	for _, raw := range []string{`{"executable":"x","args":null}`, `{"executable":"x","args":[null]}`, `{"args":[]}`, `{"executable":null}`} {
		if _, err := ReadRequest(mutate(t, shellJSON, "process", json.RawMessage(raw), false)); err == nil {
			t.Fatalf("accepted process %s", raw)
		}
	}
	var p0 ShellProcessArgv
	if err = json.Unmarshal([]byte(`{"executable":"tool"}`), &p0); err != nil || p0.Args == nil {
		t.Fatal("args default must be empty vector")
	}
}

func TestProcessShellModesAndBounds(t *testing.T) {
	for _, p := range []ShellProcessArgv{{"/bin/bash", []string{"-lc", "id"}}, {`C:\Windows\cmd.exe`, []string{"/C", "id"}}, {"pwsh", []string{"-Command", "id"}}, {"sh", []string{"-C"}}} {
		if ValidateProcessArgv(p) == nil {
			t.Fatalf("accepted shell mode %+v", p)
		}
	}
	if err := ValidateProcessArgv(ShellProcessArgv{"bash", []string{"--", "-c"}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []ShellProcessArgv{{"", nil}, {"x\x00", nil}, {strings.Repeat("x", 1025), nil}, {"x", make([]string, 257)}, {"x", []string{strings.Repeat("a", 8193)}}, {"x", []string{strings.Repeat("a", 8192), strings.Repeat("b", 8192)}}} {
		if ValidateProcessArgv(p) == nil {
			t.Fatal("accepted oversized process")
		}
	}
	r := request(t, "run_process")
	r.Process = &ShellProcessArgv{Executable: "x"}
	for _, timeout := range []uint64{0, 121, 3600} {
		r.TimeoutSecs = timeout
		if _, err := r.DecodeInvocation(); err == nil {
			t.Fatalf("accepted direct timeout %d", timeout)
		}
	}
}

func TestScriptLimitsAndWhitespace(t *testing.T) {
	r := request(t, "run_script")
	r.Script = &ShellScriptPayload{Language: ScriptBash, Script: " ", Args: []string{"x y", "猫"}}
	if _, err := r.DecodeInvocation(); err != nil {
		t.Fatal("whitespace script must be valid", err)
	}
	r.Script.Script = strings.Repeat("#", ScriptMaxBytes)
	b, _ := json.Marshal(r)
	if _, err := DecodeRequest(b); err != nil {
		t.Fatal("script body must not use command envelope size", err)
	}
	r.Script.Script += "#"
	if _, err := r.DecodeInvocation(); err == nil {
		t.Fatal("accepted oversized script")
	}
	r.Script.Script = ""
	if _, err := r.DecodeInvocation(); err == nil {
		t.Fatal("accepted zero-byte script")
	}
	r.Script.Script = "x"
	r.Script.Language = "python"
	if _, err := r.DecodeInvocation(); err == nil {
		t.Fatal("accepted unknown language")
	}
	if _, err := ReadRequest(mutate(t, shellJSON, "script", map[string]any{"language": "python", "script": "x"}, false)); err == nil {
		t.Fatal("wire enum accepted unknown language")
	}
}

func TestShellEnvelopeAppliesOnlyToCommand(t *testing.T) {
	r := request(t, "run_shell")
	r.Command = strings.Repeat("x", RawShellWireMaxBytes)
	r.Stdin = ptr(strings.Repeat("s", RawShellWireMaxBytes))
	b, _ := json.Marshal(r)
	if _, err := DecodeRequest(b); err != nil {
		t.Fatal("whole request capped at shell command size", err)
	}
	if ValidateAuthoredShellCommand(r.Command) == nil {
		t.Fatal("authored bound must stay 16000")
	}
	r.Command += "x"
	if _, err := r.DecodeInvocation(); err == nil {
		t.Fatal("accepted oversized wire command")
	}
	r.Command = "\x00"
	if _, err := r.DecodeInvocation(); err == nil {
		t.Fatal("accepted NUL")
	}
	r.Command = " "
	if _, err := r.DecodeInvocation(); err == nil {
		t.Fatal("accepted empty shell")
	}
}

func TestFileOperations(t *testing.T) {
	r := request(t, "file_read")
	r.Path = ptr("x")
	r.StartLine = ptr(uint64(1))
	r.EndLine = ptr(uint64(2))
	inv, err := r.DecodeInvocation()
	if err != nil {
		t.Fatal(err)
	}
	f := inv.Operation.(FileOperation)
	if f.WireKind() != "file_read" || f.Payload.Path != "x" {
		t.Fatal("file fields lost")
	}
	r.StartLine = ptr(uint64(0))
	if _, err = r.DecodeInvocation(); err == nil {
		t.Fatal("accepted zero line")
	}
	r.StartLine = nil
	if _, err = r.DecodeInvocation(); err == nil {
		t.Fatal("accepted partial range")
	}
	r = request(t, "file_write")
	r.Path = ptr("x")
	r.Content = ptr("")
	r.CreateDirs = true
	r.ExpectedSHA256 = ptr("original-expectation")
	if _, err = r.DecodeInvocation(); err != nil {
		t.Fatal(err)
	}
	r.Kind = "file_list"
	if _, err = r.DecodeInvocation(); err == nil {
		t.Fatal("accepted write fields on list")
	}
	r.ExpectedSHA256 = nil
	r.CreateDirs = false
	if _, err = r.DecodeInvocation(); err != nil {
		t.Fatal("original decoder allows content on list", err)
	}
}

func TestUnknownConflictingAndDeferredNeverFallback(t *testing.T) {
	r := request(t, "unknown_future")
	if _, err := r.DecodeInvocation(); !errors.Is(err, ErrUnknownKind) {
		t.Fatal(err)
	}
	r = request(t, "run_shell")
	r.Process = &ShellProcessArgv{Executable: "x"}
	if _, err := r.DecodeInvocation(); err == nil {
		t.Fatal("accepted conflicting process")
	}
	r = request(t, "run_process")
	r.Process = &ShellProcessArgv{Executable: "x"}
	r.LSP = json.RawMessage(`{"anything":true}`)
	if _, err := r.DecodeInvocation(); err == nil {
		t.Fatal("accepted conflicting lsp")
	}
	for _, kind := range []string{"start_job", "run_internal_posix_script", "file_save_project_artifact", "register_project", "computer_control", "validation", "lsp", "persistent_shell", "mcp_gateway", "plugin_gateway", "coding_agent", "skill_store", "ssh_resource", "runner_config"} {
		r = request(t, kind)
		if _, err := r.DecodeInvocation(); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	r = request(t, "run_shell")
	r.JobContext = json.RawMessage(`{"ssh_resource":"remote"}`)
	if _, err := r.DecodeInvocation(); !errors.Is(err, ErrUnsupported) {
		t.Fatal("SSH must not dispatch locally", err)
	}
	r = request(t, "mcp_gateway")
	r.MCPGateway = json.RawMessage(`{"operation":"tools_call","arguments":{"n":18446744073709551615}}`)
	b, _ := json.Marshal(r)
	wire, err := ReadRequest(b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire.MCPGateway), "18446744073709551615") {
		t.Fatal("deferred data lost precision")
	}
	if _, err = wire.DecodeInvocation(); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestPollResultOfflineAndViewCompatibility(t *testing.T) {
	poll, err := ReadPollPayload([]byte(`{"client_id":"c","agent_instance_id":"i","tool_providers":{"future":1},"project_inventory_page":{"generation":"g"}}`))
	if err != nil || poll.ClientID != "c" || !present(poll.ToolProviders) {
		t.Fatal("flattened poll lost data", err)
	}
	result, err := ReadResultPayload([]byte(`{"client_id":"c","agent_instance_id":"i","request_id":"r","exit_code":0,"duration_ms":18446744073709551615,"command_execution_state":"completed","mcp_gateway":{"dispatch_state":"completed"}}`))
	if err != nil || result.ExitCode == nil || *result.ExitCode != 0 || result.DurationMS == nil || *result.DurationMS != math.MaxUint64 || !present(result.MCPGateway) {
		t.Fatal("flattened result lost data", err)
	}
	b, _ := json.Marshal(result)
	if strings.Contains(string(b), `"result"`) || !strings.Contains(string(b), `"command_execution_state":"completed"`) {
		t.Fatalf("result not flat: %s", b)
	}
	for _, bad := range []string{`{"client_id":"c","request_id":"r"}`, `{"client_id":"c","agent_instance_id":"i","request_id":"r","exit_code":2147483648}`, `{"client_id":"c","agent_instance_id":"i","request_id":"r","command_execution_state":"success"}`} {
		if _, err := ReadResultPayload([]byte(bad)); err == nil {
			t.Fatalf("accepted invalid result %s", bad)
		}
	}
	result, err = ReadResultPayload([]byte(`{"client_id":"c","agent_instance_id":"i","request_id":"r","exit_code":null}`))
	if err != nil || result.ExitCode != nil || result.DurationMS != nil || result.CommandExecutionState != nil {
		t.Fatal("absent result became zero", err)
	}
	if _, err := ReadOfflineRequest([]byte(`{"client_id":"c"}`)); err == nil {
		t.Fatal("offline omitted instance accepted")
	}
	var v RunnerView
	err = json.Unmarshal([]byte(`{"client_id":"c","status":"offline","connected":false,"last_seen":0,"capabilities":{},"pending_requests":0,"agent_protocol_generation":2}`), &v)
	if err != nil || v.Transport != "polling" || v.AgentInstanceID != "" || !v.Capabilities.Shell || v.Projects == nil {
		t.Fatal("stale view defaults wrong", err)
	}
	var response RunnerPollResponse
	if err = json.Unmarshal([]byte(`{"success":false}`), &response); err != nil || response.Request != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(`{}`), &response); err == nil {
		t.Fatal("response success required")
	}
}
