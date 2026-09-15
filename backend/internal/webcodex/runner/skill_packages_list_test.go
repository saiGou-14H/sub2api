// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func packageListInput(id string) protocol.RunnerRequest {
	r := input(id)
	r.Kind, r.Command = "file_skill_list_packages", ""
	cwd, path, content := "/project", ".agents/skills", `{"limit":257}`
	r.Cwd, r.Path, r.Content = &cwd, &path, &content
	return r
}

func TestSkillPackagesListDispatchAndResult(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	// Full G2 admission is unchanged. Isolate the per-request dispatch bit only
	// after valid registration, without claiming this partial fixture can register.
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities = protocol.RunnerCapabilities{FileRead: true}
	r.mu.Unlock()
	request := packageListInput("list-request")
	if _, err := r.Enqueue(Access{Username: "bob"}, request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner bypass: %v", err)
	}
	pending, err := r.Enqueue(Access{Username: "alice"}, request)
	if err != nil {
		t.Fatal(err)
	}
	delivered, err := poll(r, "node-a", "process-a")
	if err != nil || delivered == nil || !reflect.DeepEqual(*delivered, request) {
		t.Fatalf("request changed: %v %v", delivered, err)
	}
	if again, err := poll(r, "node-a", "process-a"); err != nil || again != nil {
		t.Fatalf("duplicate listing: %v %v", again, err)
	}
	// Fixture-supplied stdout, not a filesystem scan or validation of a list DTO.
	stdout, stderr, code := `{"format":"webcodex.skill_package_list.v1","entries":[{"name":"demo","kind":"dir"},{"name":"link","kind":"symlink"}],"truncated":false}`, "", int32(0)
	response := protocol.RunnerResultPayload{RunnerResultRequest: protocol.RunnerResultRequest{RequestID: request.RequestID, ClientID: "node-a", AgentInstanceID: "process-a", ExitCode: &code, Stdout: &stdout, Stderr: &stderr}}
	if err := r.Complete(testPrincipal("node-a"), response); err != nil {
		t.Fatal(err)
	}
	out := wait(t, pending)
	if out.Err != nil || !out.Dispatched || !reflect.DeepEqual(out.Result, &response) {
		t.Fatalf("result changed: %+v", out)
	}
}

func TestSkillPackagesListFileReadGate(t *testing.T) {
	r := testRegistry(t)
	body := registration(t, "node-a", "process-a")
	body.Capabilities.FileRead = false
	if _, err := r.Register(testPrincipal("node-a"), body); err == nil {
		t.Fatal("missing baseline FileRead accepted")
	}
	register(t, r, "node-a", "process-a")
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities.FileRead = false
	r.mu.Unlock()
	if _, err := r.Enqueue(Access{Username: "alice"}, packageListInput("disabled-list")); err == nil || !strings.Contains(err.Error(), "capability not advertised") {
		t.Fatalf("FileRead bypass: %v", err)
	}
	if req, err := poll(r, "node-a", "process-a"); err != nil || req != nil {
		t.Fatalf("denied request queued: %v %v", req, err)
	}
}

func TestSkillPackagesListOpaqueOptionsAndCanonicalRejection(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	for _, content := range []*string{nil, stringPointer(`[257]`), stringPointer(`{"limit":1,"limit":2}`)} {
		req := packageListInput("opaque-list")
		req.Cwd, req.Content = nil, content
		pending, err := r.Enqueue(Access{Username: "alice"}, req)
		if err != nil {
			t.Fatalf("executor validation moved into registry: %v", err)
		}
		delivered, err := poll(r, "node-a", "process-a")
		if err != nil || delivered == nil || !reflect.DeepEqual(*delivered, req) {
			t.Fatalf("opaque options changed: %v %v", delivered, err)
		}
		pending.Cancel(nil)
	}
	req := packageListInput("bad-range")
	line := uint64(1)
	req.StartLine, req.EndLine = &line, &line
	if _, err := r.Enqueue(Access{Username: "alice"}, req); err == nil {
		t.Fatal("listing range admitted")
	}
	req = packageListInput("deferred-list")
	req.Kind = "file_project_overview"
	if _, err := r.Enqueue(Access{Username: "alice"}, req); !errors.Is(err, protocol.ErrUnsupported) {
		t.Fatalf("deferred operation admitted: %v", err)
	}
	if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
		t.Fatalf("rejected request queued: %v %v", delivered, err)
	}
}

func stringPointer(value string) *string { return &value }
