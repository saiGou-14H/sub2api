// SPDX-License-Identifier: Apache-2.0
// Source: WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// runner_operation.rs:218-228,299,943-969,1372-1413. Raw outer codec only.
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestApplyPatchOpaqueRoundtrip(t *testing.T) {
	contents := []*string{
		nil, ptr(""), ptr(`null`), ptr(`not JSON`), ptr(`{}`), ptr(`["*** Begin Patch\n*** End Patch"]`),
		ptr(`{"patch":"*** Begin Patch\n*** Add File: 目录/😀\n+text\n*** End Patch","matching_mode":"strict"}`),
		ptr(`{"patch":"first","patch":"last","wide":18446744073709551615}`),
		ptr(`{"bad":"\ud800","overflow":1e400}`), ptr("nul\x00content"),
	}
	for index, content := range contents {
		for maxIndex, maxBytes := range []*uint64{nil, ptr(uint64(0)), ptr(^uint64(0))} {
			t.Run(fmt.Sprintf("content_%d/max_%d", index, maxIndex), func(t *testing.T) {
				r := request(t, "file_apply_patch")
				r.Path, r.Cwd, r.Content, r.MaxBytes = ptr("目录/😀"), ptr("/项目"), content, maxBytes
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
					t.Fatalf("public decode differs: %v", err)
				}
				file, ok := inv.Operation.(FileOperation)
				want := FilePayload{Cwd: ptr("/项目"), Path: "目录/😀", Content: content, MaxBytes: maxBytes}
				if !ok || file.WireKind() != r.Kind || !reflect.DeepEqual(file.Payload, want) || inv.Metadata != (InvocationMetadata{r.RequestID, r.ClientID, r.RequestedBy, r.CreatedAt}) {
					t.Fatalf("canonical patch changed: %+v", inv)
				}
				if _, err := decoded.DecodeJobInvocation(); !errors.Is(err, ErrUnsupported) {
					t.Fatalf("Job invocation: %v", err)
				}
				if _, err := DecodeJobRequest(wire); !errors.Is(err, ErrUnsupported) {
					t.Fatalf("Job decoder: %v", err)
				}
				*decoded.Path, *decoded.Cwd = "changed", "changed"
				if decoded.MaxBytes != nil {
					*decoded.MaxBytes = 7
				}
				if decoded.Content != nil {
					*decoded.Content = "changed"
				}
				if !reflect.DeepEqual(file.Payload, want) {
					t.Fatal("DTO mutation retargeted canonical patch")
				}
			})
		}
	}
	if reflect.TypeFor[RunnerRequest]().NumField() != 27 || reflect.TypeFor[FilePayload]().NumField() != 9 {
		t.Fatal("original request/File fields changed")
	}
}

func TestApplyPatchRawCompatibility(t *testing.T) {
	// ShellFileOpRequest's business content/max_bytes rules in validation.rs are
	// not the raw RunnerRequest conversion in runner_operation.rs.
	base := `{"request_id":"r","client_id":"c","kind":"file_apply_patch","command":"","timeout_secs":0,"requested_by":"alice","created_at":0,"path":"."}`
	for _, extra := range []string{
		``, `,"content":null,"max_bytes":null,"cwd":null,"expected_sha256":null,"expected_prefix":null,"start_line":null,"end_line":null,"create_dirs":false`,
		`,"max_bytes":18446744073709551615`, `,"cwd":"","content":"","max_bytes":0`,
		`,"content":"{bad options"`, `,"patch":"future","matching_mode":"invalid","unknown":true`,
	} {
		if _, err := DecodeRequest([]byte(base[:len(base)-1] + extra + `}`)); err != nil {
			t.Fatalf("business patch validation moved into raw codec: %v", err)
		}
	}
	for name, change := range map[string]func(*RunnerRequest){
		"path_missing": func(r *RunnerRequest) { r.Path = nil },
		"path_empty":   func(r *RunnerRequest) { r.Path = ptr("") },
		"path_nul":     func(r *RunnerRequest) { r.Path = ptr("a\x00b") },
		"cwd_nul":      func(r *RunnerRequest) { r.Cwd = ptr("a\x00b") },
		"sha":          func(r *RunnerRequest) { r.ExpectedSHA256 = ptr("") },
		"prefix":       func(r *RunnerRequest) { r.ExpectedPrefix = ptr("") },
		"dirs":         func(r *RunnerRequest) { r.CreateDirs = true },
		"start":        func(r *RunnerRequest) { r.StartLine = ptr(uint64(0)) },
		"end":          func(r *RunnerRequest) { r.EndLine = ptr(uint64(0)) },
		"both_lines":   func(r *RunnerRequest) { r.StartLine, r.EndLine = ptr(uint64(1)), ptr(uint64(2)) },
		"command":      func(r *RunnerRequest) { r.Command = "echo no" },
		"stdin":        func(r *RunnerRequest) { r.Stdin = ptr("") },
		"job_id":       func(r *RunnerRequest) { r.JobID = ptr("") },
		"job_context":  func(r *RunnerRequest) { r.JobContext = json.RawMessage(`{}`) },
		"process":      func(r *RunnerRequest) { r.Process = &ShellProcessArgv{Executable: "echo", Args: []string{}} },
		"script": func(r *RunnerRequest) {
			r.Script = &ShellScriptPayload{Language: ScriptSh, Script: "true", Args: []string{}}
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
				t.Fatalf("invalid raw patch admitted/misclassified: %v", err)
			}
			wire, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeRequest(wire); err == nil || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrUnknownKind) {
				t.Fatalf("public patch decoder admitted/misclassified invalid request: %v", err)
			}
		})
	}
	for _, extra := range []string{`,"content":{}`, `,"content":null,"content":null`, `,"content":"\ud800"`, `,"max_bytes":18446744073709551616`, `,"max_bytes":-1`, `,"max_bytes":1.5`, `,"max_bytes":1e1`} {
		if _, err := ReadRequest([]byte(base[:len(base)-1] + extra + `}`)); err == nil {
			t.Fatalf("invalid outer wire accepted: %s", extra)
		}
	}
	unknown := strings.Replace(base, "file_apply_patch", "file_future_patch", 1)
	if _, err := DecodeRequest([]byte(unknown)); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("unknown classification: %v", err)
	}
}
