// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func skillReadInput(id string) protocol.RunnerRequest {
	r := input(id)
	r.Kind, r.Command = "file_skill_read_file", ""
	cwd, path, content := "/project", ".agents/skills/demo/SKILL.md", `{"package_root":".agents/skills/demo","max_file_bytes":524288}`
	start, end := uint64(1), uint64(20)
	r.Cwd, r.Path, r.Content, r.StartLine, r.EndLine = &cwd, &path, &content, &start, &end
	return r
}

func TestSkillFileReadSynchronousDispatchAndResult(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	// The test-only node already passed unchanged full-G2 admission. Remove
	// unrelated capabilities in memory to isolate dispatch's FileRead dependency.
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities = protocol.RunnerCapabilities{FileRead: true}
	r.mu.Unlock()
	request := skillReadInput("skill-read")
	if _, err := r.Enqueue(Access{Username: "bob"}, request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner bypass: %v", err)
	}
	pending, err := r.Enqueue(Access{Username: "alice"}, request)
	if err != nil {
		t.Fatal(err)
	}
	delivered, err := poll(r, "node-a", "process-a")
	if err != nil || delivered == nil || !reflect.DeepEqual(*delivered, request) {
		t.Fatalf("poll changed request: %+v %v", delivered, err)
	}
	if again, err := poll(r, "node-a", "process-a"); err != nil || again != nil {
		t.Fatalf("duplicate delivery: %v %v", again, err)
	}
	code, elapsed, stdout, stderr := int32(0), uint64(7), `{"format":"webcodex.skill_file_read.v1","content":"","sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","file_bytes":0,"total_lines":0,"start_line":1,"limit":20,"returned_lines":0,"end_line":null,"has_more":false,"next_start_line":null}`, ""
	response := protocol.RunnerResultPayload{RunnerResultRequest: protocol.RunnerResultRequest{ClientID: "node-a", AgentInstanceID: "process-a", RequestID: request.RequestID, ExitCode: &code, DurationMS: &elapsed, Stdout: &stdout, Stderr: &stderr}}
	// Supplied fixture result, not filesystem execution or Go validation of the skill DTO.
	if err := r.Complete(testPrincipal("node-a"), response); err != nil {
		t.Fatal(err)
	}
	outcome := wait(t, pending)
	if outcome.Err != nil || !outcome.Dispatched || !reflect.DeepEqual(outcome.Result, &response) {
		t.Fatalf("result changed: %+v", outcome)
	}
}

func TestSkillFileReadCapabilityDenialDoesNotRelaxGeneration(t *testing.T) {
	r := testRegistry(t)
	body := registration(t, "node-a", "process-a")
	body.Capabilities.FileRead = false
	if _, err := r.Register(testPrincipal("node-a"), body); err == nil {
		t.Fatal("G2 accepted missing FileRead")
	}
	register(t, r, "node-a", "process-a")
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities.FileRead = false
	r.mu.Unlock()
	for _, kind := range []string{"file_read", "file_list", "file_skill_read_file"} {
		req := skillReadInput(kind)
		req.Kind = kind
		if kind == "file_list" {
			req.StartLine, req.EndLine = nil, nil
		}
		if _, err := r.Enqueue(Access{Username: "alice"}, req); err == nil || !strings.Contains(err.Error(), "capability not advertised") {
			t.Fatalf("%s bypassed FileRead: %v", kind, err)
		}
	}
	if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
		t.Fatalf("denied request queued: %v %v", delivered, err)
	}
}

func TestSkillFileReadDoesNotValidateExecutorOptions(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	req := skillReadInput("opaque-options")
	req.Cwd, req.Content, req.StartLine, req.EndLine = nil, nil, nil, nil
	pending, err := r.Enqueue(Access{Username: "alice"}, req)
	if err != nil {
		t.Fatalf("skill executor validation moved into registry: %v", err)
	}
	delivered, err := poll(r, "node-a", "process-a")
	if err != nil || delivered == nil || !reflect.DeepEqual(*delivered, req) {
		t.Fatalf("opaque request changed: %v %v", delivered, err)
	}
	pending.Cancel(nil)
}
