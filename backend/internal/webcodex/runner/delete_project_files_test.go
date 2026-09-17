// SPDX-License-Identifier: Apache-2.0
// Source: WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// runner-registry requests.rs:806-848. Routing only, no deletion execution.
package runner

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func TestDeleteProjectFilesSynchronousDispatch(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	// Keep full G2 admission; isolate the original dispatch bit afterward.
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities = protocol.RunnerCapabilities{StructuredFileDelete: true}
	r.mu.Unlock()
	for _, content := range []*string{nil, stringPointer(`{"paths":["a","目录/😀"]}`), stringPointer(`{"paths":[],"paths":["b"]}`), stringPointer(`not JSON`)} {
		req := input("delete-project-files")
		req.Kind, req.Command, req.Path, req.Content = "file_delete_project_files", "", stringPointer("."), content
		if _, err := r.Enqueue(Access{Username: "bob"}, req); !errors.Is(err, ErrForbidden) {
			t.Fatalf("owner bypass: %v", err)
		}
		if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
			t.Fatalf("denied request queued: %v %v", delivered, err)
		}
		pending, err := r.Enqueue(Access{Username: "alice"}, req)
		if err != nil {
			t.Fatal(err)
		}
		delivered, err := poll(r, "node-a", "process-a")
		if err != nil || delivered == nil || !reflect.DeepEqual(*delivered, req) {
			t.Fatalf("opaque delete changed: %v %v", delivered, err)
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
		stdout, code := "fixture supplied delete result", int32(0)
		response := protocol.RunnerResultPayload{RunnerResultRequest: protocol.RunnerResultRequest{RequestID: req.RequestID, ClientID: "node-a", AgentInstanceID: "process-a", ExitCode: &code, Stdout: &stdout}}
		if err := r.Complete(testPrincipal("node-a"), response); err != nil {
			t.Fatal(err)
		}
		out := wait(t, pending)
		if out.Err != nil || !out.Dispatched || !reflect.DeepEqual(out.Result, &response) {
			t.Fatalf("supplied result changed: %+v", out)
		}
		if err := r.Complete(testPrincipal("node-a"), response); err == nil {
			t.Fatal("duplicate completion accepted")
		}
	}
}

func TestDeleteProjectFilesCapabilityAndRejection(t *testing.T) {
	r := testRegistry(t)
	body := registration(t, "node-a", "process-a")
	body.Capabilities.StructuredFileDelete = false
	if _, err := r.Register(testPrincipal("node-a"), body); err == nil {
		t.Fatal("G2 baseline relaxed")
	}
	register(t, r, "node-a", "process-a")
	base := input("delete-project-files")
	base.Kind, base.Command, base.Path = "file_delete_project_files", "", stringPointer(".")
	for name, change := range map[string]func(*protocol.RunnerRequest){
		"range":       func(q *protocol.RunnerRequest) { q.StartLine = new(uint64) },
		"job":         func(q *protocol.RunnerRequest) { q.JobID = stringPointer("") },
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
				t.Fatalf("invalid delete admitted/misclassified: %v", err)
			}
			if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
				t.Fatalf("invalid delete queued: %v %v", delivered, err)
			}
		})
	}
	for _, caps := range []protocol.RunnerCapabilities{{}, {FileRead: true}, {FileWrite: true}, {FileRead: true, FileWrite: true}} {
		r.mu.Lock()
		r.nodes["node-a"].registration.Capabilities = caps
		r.mu.Unlock()
		if _, err := r.Enqueue(Access{Username: "alice"}, base); err == nil || !strings.Contains(err.Error(), "capability not advertised") {
			t.Fatalf("StructuredFileDelete bypass with %+v: %v", caps, err)
		}
		if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
			t.Fatalf("capability rejection queued: %v %v", delivered, err)
		}
	}
}
