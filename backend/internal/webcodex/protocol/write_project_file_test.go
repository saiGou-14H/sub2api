// SPDX-License-Identifier: Apache-2.0
// Source: WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// runner_operation.rs:283-297,943-969,1372-1413. Codec only, not execution.
package protocol

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestWriteProjectFileOpaqueRoundtripAndJobBoundary(t *testing.T) {
	// The executor parses options; even malformed JSON and duplicate option
	// members remain opaque strings at the generic File boundary.
	for _, content := range []*string{nil, ptr(""), ptr(`{}`), ptr(`null`), ptr(`not JSON`), ptr(`{"content":"猫😀","overwrite":true,"expected_sha256":"abc","create_dirs":true}`), ptr(`{"overwrite":false,"overwrite":true}`), ptr(`["text",true]`), ptr("a\x00b")} {
		r := request(t, "file_write_project_file")
		r.Path, r.Cwd, r.Content, r.MaxBytes = ptr("目录/😀.txt"), ptr("/项目"), content, ptr(^uint64(0))
		r.TimeoutSecs, r.CreatedAt = ^uint64(0), -9223372036854775808
		wire, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := ReadRequest(wire)
		if err != nil || !reflect.DeepEqual(decoded, r) {
			t.Fatalf("wire changed: %v", err)
		}
		inv, err := decoded.DecodeInvocation()
		if err != nil {
			t.Fatal(err)
		}
		public, err := DecodeRequest(wire)
		if err != nil || !reflect.DeepEqual(public, inv) {
			t.Fatalf("public decoder differs: %v", err)
		}
		file, ok := inv.Operation.(FileOperation)
		want := FilePayload{Path: "目录/😀.txt", Cwd: ptr("/项目"), Content: content, MaxBytes: ptr(^uint64(0))}
		if !ok || file.WireKind() != r.Kind || !reflect.DeepEqual(file.Payload, want) || inv.Metadata != (InvocationMetadata{r.RequestID, r.ClientID, r.RequestedBy, r.CreatedAt}) {
			t.Fatalf("canonical write changed: %+v", inv)
		}
		if _, err := decoded.DecodeJobInvocation(); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("Job invocation: %v", err)
		}
		if _, err := DecodeJobRequest(wire); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("Job request: %v", err)
		}
		*decoded.Path, *decoded.Cwd, *decoded.MaxBytes = "changed", "changed", 0
		if decoded.Content != nil {
			*decoded.Content = "changed"
		}
		if !reflect.DeepEqual(file.Payload, want) {
			t.Fatal("DTO mutation retargeted canonical payload")
		}
	}
	if reflect.TypeFor[RunnerRequest]().NumField() != 27 || reflect.TypeFor[FilePayload]().NumField() != 9 {
		t.Fatal("original field sets changed")
	}
}

func TestWriteProjectFileCompatibility(t *testing.T) {
	base := `{"request_id":"r","client_id":"c","kind":"file_write_project_file","command":"","timeout_secs":0,"requested_by":"alice","created_at":0,"path":"."}`
	for _, extra := range []string{``, `,"content":null,"cwd":null,"start_line":null,"end_line":null,"expected_sha256":null,"expected_prefix":null,"create_dirs":false`, `,"overwrite":true,"future":1`, `,"cwd":"","content":"{bad options"`} {
		if _, err := DecodeRequest([]byte(base[:len(base)-1] + extra + `}`)); err != nil {
			t.Fatalf("executor-only validation introduced: %v", err)
		}
	}
	for _, path := range []string{" ", "/absolute", "../parent", "目录/😀"} {
		r, err := ReadRequest([]byte(base))
		if err != nil {
			t.Fatal(err)
		}
		r.Path = &path
		if _, err := r.DecodeInvocation(); err != nil {
			t.Fatalf("executor path check moved to codec: %v", err)
		}
	}
	for name, change := range map[string]func(*RunnerRequest){
		"missing_path": func(r *RunnerRequest) { r.Path = nil },
		"empty_path":   func(r *RunnerRequest) { r.Path = ptr("") },
		"nul_path":     func(r *RunnerRequest) { r.Path = ptr("a\x00b") },
		"nul_cwd":      func(r *RunnerRequest) { r.Cwd = ptr("a\x00b") },
		"start_zero":   func(r *RunnerRequest) { r.StartLine = ptr(uint64(0)) },
		"end_zero":     func(r *RunnerRequest) { r.EndLine = ptr(uint64(0)) },
		"both_lines":   func(r *RunnerRequest) { r.StartLine, r.EndLine = ptr(uint64(1)), ptr(uint64(2)) },
		"sha":          func(r *RunnerRequest) { r.ExpectedSHA256 = ptr("") },
		"prefix":       func(r *RunnerRequest) { r.ExpectedPrefix = ptr("") },
		"create_dirs":  func(r *RunnerRequest) { r.CreateDirs = true },
		"command":      func(r *RunnerRequest) { r.Command = "echo no" },
		"stdin":        func(r *RunnerRequest) { r.Stdin = ptr("") },
		"job_id":       func(r *RunnerRequest) { r.JobID = ptr("") },
		"job_context":  func(r *RunnerRequest) { r.JobContext = json.RawMessage(`{}`) },
		"process":      func(r *RunnerRequest) { r.Process = &ShellProcessArgv{Executable: "echo", Args: []string{}} },
		"script": func(r *RunnerRequest) {
			r.Script = &ShellScriptPayload{Language: ScriptSh, Script: "echo no", Args: []string{}}
		},
		"validation":       func(r *RunnerRequest) { r.Validation = json.RawMessage(`{}`) },
		"lsp":              func(r *RunnerRequest) { r.LSP = json.RawMessage(`{}`) },
		"persistent_shell": func(r *RunnerRequest) { r.PersistentShell = json.RawMessage(`{}`) },
		"mcp_gateway":      func(r *RunnerRequest) { r.MCPGateway = json.RawMessage(`{}`) },
		"plugin_gateway":   func(r *RunnerRequest) { r.PluginGateway = json.RawMessage(`{}`) },
		"coding_agent":     func(r *RunnerRequest) { r.CodingAgent = json.RawMessage(`{}`) },
	} {
		t.Run(name, func(t *testing.T) {
			r, err := ReadRequest([]byte(base))
			if err != nil {
				t.Fatal(err)
			}
			change(&r)
			if _, err := r.DecodeInvocation(); err == nil || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrUnknownKind) {
				t.Fatalf("expected invalid: %v", err)
			}
			wire, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeRequest(wire); err == nil || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrUnknownKind) {
				t.Fatalf("public decoder expected invalid: %v", err)
			}
		})
	}
	for _, extra := range []string{`,"content":{}`, `,"content":null,"content":null`, `,"content":"\ud800"`, `,"max_bytes":18446744073709551616`, `,"max_bytes":-1`} {
		if _, err := ReadRequest([]byte(base[:len(base)-1] + extra + `}`)); err == nil {
			t.Fatalf("invalid outer wire accepted: %s", extra)
		}
	}
	// Basic file_write alone owns these compatibility fields and requires content.
	r, err := ReadRequest([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	r.Kind = "file_write"
	if _, err := r.DecodeInvocation(); err == nil {
		t.Fatal("basic write no longer requires content")
	}
	r.Content, r.ExpectedSHA256, r.ExpectedPrefix, r.CreateDirs = ptr(""), ptr(""), ptr(""), true
	if _, err := r.DecodeInvocation(); err != nil {
		t.Fatalf("basic write compatibility regressed: %v", err)
	}
}
