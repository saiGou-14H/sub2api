// SPDX-License-Identifier: Apache-2.0
// Source: WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// runner_operation.rs:943-969,1372-1413; runner files.rs:244-299.
package protocol

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestProjectOverviewOpaqueContentAndJobBoundary(t *testing.T) {
	// Options are parsed only by the source executor. Even invalid options must
	// survive this generic codec unchanged; these cases do not assert execution.
	for _, content := range []*string{nil, ptr(""), ptr(`{}`), ptr(`{"max_depth":null,"limit":null}`), ptr(`{"max_depth":0,"limit":18446744073709551615,"future":true}`), ptr(`{"max_depth":1,"max_depth":2}`), ptr(`[2,200]`), ptr(`null`), ptr(`not JSON`)} {
		r := request(t, "file_project_overview")
		r.Path, r.Content, r.MaxBytes = ptr("."), content, ptr(^uint64(0))
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
		want := FilePayload{Path: ".", Content: content, MaxBytes: r.MaxBytes}
		if !ok || file.WireKind() != r.Kind || !reflect.DeepEqual(file.Payload, want) || inv.Metadata != (InvocationMetadata{r.RequestID, r.ClientID, r.RequestedBy, r.CreatedAt}) {
			t.Fatalf("canonical overview changed: %+v", inv)
		}
		if _, err := decoded.DecodeJobInvocation(); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("Job invocation: %v", err)
		}
		if _, err := DecodeJobRequest(wire); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("Job request: %v", err)
		}
		*decoded.Path, *decoded.MaxBytes = "changed", 0
		if decoded.Content != nil {
			*decoded.Content = "changed"
		}
		if file.Payload.Path != "." || *file.Payload.MaxBytes != ^uint64(0) || (file.Payload.Content != nil && *file.Payload.Content == "changed") {
			t.Fatal("wire mutation retargeted canonical overview")
		}
	}
	if reflect.TypeFor[RunnerRequest]().NumField() != 27 || reflect.TypeFor[FilePayload]().NumField() != 9 {
		t.Fatal("original field sets changed")
	}
}

func TestProjectOverviewFileCompatibility(t *testing.T) {
	base := `{"request_id":"r","client_id":"c","kind":"file_project_overview","command":"","timeout_secs":0,"requested_by":"alice","created_at":0,"path":"."}`
	for _, extra := range []string{``, `,"content":null,"cwd":null,"start_line":null,"end_line":null`, `,"max_depth":1,"limit":20`, `,"cwd":"/missing","content":"{\"limit\":-1}"`} {
		if _, err := DecodeRequest([]byte(base[:len(base)-1] + extra + `}`)); err != nil {
			t.Fatalf("executor-only fields rejected: %v", err)
		}
	}
	for name, change := range map[string]func(*RunnerRequest){
		"missing_path":    func(r *RunnerRequest) { r.Path = nil },
		"empty_path":      func(r *RunnerRequest) { r.Path = ptr("") },
		"nul_path":        func(r *RunnerRequest) { r.Path = ptr("a\x00b") },
		"nul_cwd":         func(r *RunnerRequest) { r.Cwd = ptr("a\x00b") },
		"start_line_zero": func(r *RunnerRequest) { r.StartLine = ptr(uint64(0)) },
		"end_line":        func(r *RunnerRequest) { r.EndLine = ptr(uint64(1)) },
		"both_lines":      func(r *RunnerRequest) { r.StartLine, r.EndLine = ptr(uint64(1)), ptr(uint64(2)) },
		"sha":             func(r *RunnerRequest) { r.ExpectedSHA256 = ptr("") },
		"prefix":          func(r *RunnerRequest) { r.ExpectedPrefix = ptr("") },
		"create_dirs":     func(r *RunnerRequest) { r.CreateDirs = true },
		"command":         func(r *RunnerRequest) { r.Command = "echo no" },
		"stdin":           func(r *RunnerRequest) { r.Stdin = ptr("") },
		"job_id":          func(r *RunnerRequest) { r.JobID = ptr("") },
		"job_context":     func(r *RunnerRequest) { r.JobContext = json.RawMessage(`{}`) },
		"process":         func(r *RunnerRequest) { r.Process = &ShellProcessArgv{Executable: "echo", Args: []string{}} },
	} {
		t.Run(name, func(t *testing.T) {
			r, err := ReadRequest([]byte(base))
			if err != nil {
				t.Fatal(err)
			}
			change(&r)
			if _, err := r.DecodeInvocation(); err == nil || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrUnknownKind) {
				t.Fatalf("expected canonical invalid: %v", err)
			}
		})
	}
	for _, extra := range []string{`,"content":{}`, `,"content":null,"content":null`} {
		if _, err := ReadRequest([]byte(base[:len(base)-1] + extra + `}`)); err == nil {
			t.Fatal("invalid outer content accepted")
		}
	}
}
