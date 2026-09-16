// SPDX-License-Identifier: Apache-2.0
// Source: WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// runner files.rs:54-99,244-299. This exercises routing, not execution.
package runner

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func TestProjectOverviewSynchronousDispatch(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	// Isolate the route's capability only after full, unchanged G2 admission.
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities = protocol.RunnerCapabilities{FileRead: true}
	r.mu.Unlock()
	for _, content := range []*string{nil, stringPointer(`{"max_depth":2,"limit":200}`), stringPointer(`{"limit":1,"limit":2}`), stringPointer(`not JSON`)} {
		req := input("overview")
		req.Kind, req.Command, req.Path, req.Content = "file_project_overview", "", stringPointer("."), content
		req.Cwd = nil // Generic dispatch must not require an executor project root.
		if _, err := r.Enqueue(Access{Username: "bob"}, req); !errors.Is(err, ErrForbidden) {
			t.Fatalf("owner bypass: %v", err)
		}
		pending, err := r.Enqueue(Access{Username: "alice"}, req)
		if err != nil {
			t.Fatal(err)
		}
		delivered, err := poll(r, "node-a", "process-a")
		if err != nil || delivered == nil || !reflect.DeepEqual(*delivered, req) {
			t.Fatalf("opaque request changed: %v %v", delivered, err)
		}
		if again, err := poll(r, "node-a", "process-a"); err != nil || again != nil {
			t.Fatalf("request replay: %v %v", again, err)
		}
		// Deliberately arbitrary supplied stdout: no overview DTO or FS scan.
		stdout, code := "fixture overview result", int32(0)
		response := protocol.RunnerResultPayload{RunnerResultRequest: protocol.RunnerResultRequest{RequestID: req.RequestID, ClientID: "node-a", AgentInstanceID: "process-a", ExitCode: &code, Stdout: &stdout}}
		if err := r.Complete(testPrincipal("node-a"), response); err != nil {
			t.Fatal(err)
		}
		out := wait(t, pending)
		if out.Err != nil || !out.Dispatched || !reflect.DeepEqual(out.Result, &response) {
			t.Fatalf("result changed: %+v", out)
		}
	}
}

func TestProjectOverviewFileReadGateAndRejection(t *testing.T) {
	r := testRegistry(t)
	body := registration(t, "node-a", "process-a")
	body.Capabilities.FileRead = false
	if _, err := r.Register(testPrincipal("node-a"), body); err == nil {
		t.Fatal("G2 baseline relaxed")
	}
	register(t, r, "node-a", "process-a")
	req := input("overview")
	req.Kind, req.Command, req.Path = "file_project_overview", "", stringPointer(".")
	req.StartLine = new(uint64)
	if _, err := r.Enqueue(Access{Username: "alice"}, req); err == nil {
		t.Fatal("overview line range admitted")
	}
	req.StartLine = nil
	req.JobID = stringPointer("job")
	if _, err := r.Enqueue(Access{Username: "alice"}, req); err == nil {
		t.Fatal("Job-backed overview admitted")
	}
	req.JobID = nil
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities.FileRead = false
	r.mu.Unlock()
	if _, err := r.Enqueue(Access{Username: "alice"}, req); err == nil || !strings.Contains(err.Error(), "capability not advertised") {
		t.Fatalf("FileRead bypass: %v", err)
	}
	if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
		t.Fatalf("rejected request queued: %v %v", delivered, err)
	}
}
