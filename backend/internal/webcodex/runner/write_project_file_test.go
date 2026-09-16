// SPDX-License-Identifier: Apache-2.0
// Source: WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// runner_operation.rs:943-969,1372-1413; runner main.rs:2347-2382.
// This exercises synchronous routing, not file execution.
package runner

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func TestWriteProjectFileSynchronousDispatch(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	// Isolate dispatch only after unchanged full G2 admission. FileRead is false:
	// this structured write uses FileWrite despite generic non-write validation.
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities = protocol.RunnerCapabilities{FileWrite: true}
	r.mu.Unlock()
	for _, content := range []*string{nil, stringPointer(`{"content":"猫😀","overwrite":false}`), stringPointer(`{"overwrite":false,"overwrite":true}`), stringPointer(`not JSON`)} {
		req := input("write-project-file")
		req.Kind, req.Command, req.Path, req.Cwd, req.Content = "file_write_project_file", "", stringPointer("目录/😀.txt"), nil, content
		max := ^uint64(0)
		req.MaxBytes = &max
		if _, err := r.Enqueue(Access{Username: "bob"}, req); !errors.Is(err, ErrForbidden) {
			t.Fatalf("owner bypass: %v", err)
		}
		if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
			t.Fatalf("owner rejection queued request: %v %v", delivered, err)
		}
		pending, err := r.Enqueue(Access{Username: "alice"}, req)
		if err != nil {
			t.Fatal(err)
		}
		delivered, err := poll(r, "node-a", "process-a")
		if err != nil || delivered == nil || !reflect.DeepEqual(*delivered, req) {
			t.Fatalf("opaque request changed: %v %v", delivered, err)
		}
		wire, err := json.Marshal(delivered)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := protocol.ReadRequest(wire)
		if err != nil || !reflect.DeepEqual(decoded, req) {
			t.Fatalf("poll wire changed: %v", err)
		}
		if again, err := poll(r, "node-a", "process-a"); err != nil || again != nil {
			t.Fatalf("request replay: %v %v", again, err)
		}
		// Arbitrary supplied output; no file write or effect DTO is manufactured.
		stdout, code := "fixture supplied 写入 result", int32(0)
		response := protocol.RunnerResultPayload{RunnerResultRequest: protocol.RunnerResultRequest{RequestID: req.RequestID, ClientID: "node-a", AgentInstanceID: "process-a", ExitCode: &code, Stdout: &stdout}}
		if err := r.Complete(testPrincipal("node-a"), response); err != nil {
			t.Fatal(err)
		}
		out := wait(t, pending)
		if out.Err != nil || !out.Dispatched || !reflect.DeepEqual(out.Result, &response) {
			t.Fatalf("result changed: %+v", out)
		}
		if err := r.Complete(testPrincipal("node-a"), response); err == nil {
			t.Fatal("duplicate completion accepted")
		}
	}
}

func TestWriteProjectFileCapabilityAndRejection(t *testing.T) {
	r := testRegistry(t)
	body := registration(t, "node-a", "process-a")
	body.Capabilities.FileWrite = false
	if _, err := r.Register(testPrincipal("node-a"), body); err == nil {
		t.Fatal("G2 baseline relaxed")
	}
	register(t, r, "node-a", "process-a")
	base := input("write-project-file")
	base.Kind, base.Command, base.Path = "file_write_project_file", "", stringPointer("file.txt")
	for name, change := range map[string]func(*protocol.RunnerRequest){
		"range":       func(q *protocol.RunnerRequest) { q.StartLine = new(uint64) },
		"job":         func(q *protocol.RunnerRequest) { q.JobID = stringPointer("job") },
		"job_context": func(q *protocol.RunnerRequest) { q.JobContext = json.RawMessage(`{}`) },
		"sha":         func(q *protocol.RunnerRequest) { q.ExpectedSHA256 = stringPointer("") },
		"prefix":      func(q *protocol.RunnerRequest) { q.ExpectedPrefix = stringPointer("") },
		"dirs":        func(q *protocol.RunnerRequest) { q.CreateDirs = true },
		"command":     func(q *protocol.RunnerRequest) { q.Command = "echo no" },
		"stdin":       func(q *protocol.RunnerRequest) { q.Stdin = stringPointer("") },
		"typed": func(q *protocol.RunnerRequest) {
			q.Process = &protocol.ShellProcessArgv{Executable: "echo", Args: []string{}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			req := base
			change(&req)
			if _, err := r.Enqueue(Access{Username: "alice"}, req); err == nil || errors.Is(err, protocol.ErrUnsupported) || errors.Is(err, protocol.ErrUnknownKind) {
				t.Fatalf("invalid write admitted/misclassified: %v", err)
			}
			if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
				t.Fatalf("rejected request queued: %v %v", delivered, err)
			}
		})
	}
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities = protocol.RunnerCapabilities{FileRead: true}
	r.mu.Unlock()
	if _, err := r.Enqueue(Access{Username: "alice"}, base); err == nil || !strings.Contains(err.Error(), "capability not advertised") {
		t.Fatalf("FileRead substituted for FileWrite: %v", err)
	}
	if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
		t.Fatalf("capability rejection queued request: %v %v", delivered, err)
	}
}
