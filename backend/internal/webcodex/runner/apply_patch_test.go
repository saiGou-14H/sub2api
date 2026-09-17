// SPDX-License-Identifier: Apache-2.0
// Source: WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// requests.rs:583-605. Host matrix adopts the dedicated three-bit gate;
// the original generic ingress does not check patch capabilities.
package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func applyPatchInput(id string, content *string) protocol.RunnerRequest {
	req := input(id)
	req.Kind, req.Command, req.Path, req.Content = "file_apply_patch", "", stringPointer("."), content
	return req
}

func patchCapabilities() protocol.RunnerCapabilities {
	return protocol.RunnerCapabilities{ApplyPatch: true, ApplyPatchMatchMetadata: true, ApplyPatchMatchingMode: true}
}

func assertPatchQueueEmpty(t *testing.T, r *Registry) {
	t.Helper()
	r.mu.Lock()
	queued, retained := len(r.nodes["node-a"].queue), len(r.pending)
	r.mu.Unlock()
	if queued != 0 || retained != 0 {
		t.Fatalf("rejected request retained: queue=%d pending=%d", queued, retained)
	}
	if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
		t.Fatalf("rejected patch dispatched: %v %v", delivered, err)
	}
}

func TestApplyPatchSynchronousOpaqueDispatch(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	// Isolate per-request policy only after full G2 registration. Neither
	// FileWrite nor legacy StrictMatching is part of this host route's gate.
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities = patchCapabilities()
	r.mu.Unlock()
	for index, content := range []*string{
		nil, stringPointer(""), stringPointer(`null`), stringPointer(`not JSON`),
		stringPointer(`{"patch":"*** Begin Patch\n*** Add File: 目录/😀\n+hello\n*** End Patch"}`),
		stringPointer(`{"patch":"first","patch":"last","wide":18446744073709551615,"bad":"\ud800","number":1e400}`),
		// A text-edit-looking payload is opaque for patch, not selector-scanned.
		stringPointer(`{"changes":[{"edits":[{"line_scope":{},"occurrence":1}]}]}`),
	} {
		req := applyPatchInput(fmt.Sprintf("patch-%d", index), content)
		maxBytes := ^uint64(0)
		req.MaxBytes = &maxBytes
		if _, err := r.Enqueue(Access{Username: "bob"}, req); !errors.Is(err, ErrForbidden) {
			t.Fatalf("owner bypass: %v", err)
		}
		assertPatchQueueEmpty(t, r)
		pending, err := r.Enqueue(Access{Username: "alice"}, req)
		if err != nil {
			t.Fatal(err)
		}
		delivered, err := poll(r, "node-a", "process-a")
		if err != nil || delivered == nil || !reflect.DeepEqual(*delivered, req) {
			t.Fatalf("opaque patch changed: %v %v", delivered, err)
		}
		wire, err := json.Marshal(delivered)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := protocol.ReadRequest(wire)
		if err != nil || !reflect.DeepEqual(decoded, req) {
			t.Fatalf("polled patch wire changed: %v", err)
		}
		if again, err := poll(r, "node-a", "process-a"); err != nil || again != nil {
			t.Fatalf("patch replay: %v %v", again, err)
		}
		stdout, stderr, code := "fixture supplied patch result: 目录/😀", "fixture stderr", int32(7)
		response := protocol.RunnerResultPayload{RunnerResultRequest: protocol.RunnerResultRequest{RequestID: req.RequestID, ClientID: "node-a", AgentInstanceID: "process-a", ExitCode: &code, Stdout: &stdout, Stderr: &stderr}}
		if err := r.Complete(testPrincipal("node-a"), response); err != nil {
			t.Fatal(err)
		}
		out := wait(t, pending)
		if out.Err != nil || !out.Dispatched || !reflect.DeepEqual(out.Result, &response) {
			t.Fatalf("supplied patch result changed: %+v", out)
		}
		if err := r.Complete(testPrincipal("node-a"), response); err == nil {
			t.Fatal("duplicate completion accepted")
		}
	}
}

func TestApplyPatchThreeBitCapabilityMatrix(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		t.Run(fmt.Sprintf("mask_%03b", mask), func(t *testing.T) {
			r := testRegistry(t)
			register(t, r, "node-a", "process-a")
			caps := protocol.RunnerCapabilities{
				ApplyPatch: mask&1 != 0, ApplyPatchMatchMetadata: mask&2 != 0, ApplyPatchMatchingMode: mask&4 != 0,
				// Unrelated and legacy bits cannot substitute for a missing bit.
				FileRead: true, FileWrite: true, StructuredFileDelete: true, ApplyPatchStrictMatching: true,
			}
			r.mu.Lock()
			r.nodes["node-a"].registration.Capabilities = caps
			r.mu.Unlock()
			req := applyPatchInput("matrix", nil)
			pending, err := r.Enqueue(Access{Username: "alice"}, req)
			if mask != 7 {
				if pending != nil || err == nil || !strings.Contains(err.Error(), "capability not advertised") {
					t.Fatalf("patch capability bypass: %v %v", pending, err)
				}
				assertPatchQueueEmpty(t, r)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered == nil || !reflect.DeepEqual(*delivered, req) {
				t.Fatalf("all patch bits rejected: %v %v", delivered, err)
			}
		})
	}
	t.Run("FileWrite_only", func(t *testing.T) {
		r := testRegistry(t)
		register(t, r, "node-a", "process-a")
		r.mu.Lock()
		r.nodes["node-a"].registration.Capabilities = protocol.RunnerCapabilities{FileWrite: true}
		r.mu.Unlock()
		if _, err := r.Enqueue(Access{Username: "alice"}, applyPatchInput("write-only", nil)); err == nil || !strings.Contains(err.Error(), "capability not advertised") {
			t.Fatalf("FileWrite granted patch: %v", err)
		}
		assertPatchQueueEmpty(t, r)
	})
}

func TestApplyPatchCapabilityWithdrawalBeforePoll(t *testing.T) {
	for _, bit := range []string{"apply_patch", "match_metadata", "matching_mode"} {
		t.Run(bit, func(t *testing.T) {
			r := testRegistry(t)
			register(t, r, "node-a", "process-a")
			r.mu.Lock()
			r.nodes["node-a"].registration.Capabilities = patchCapabilities()
			r.mu.Unlock()
			pending, err := r.Enqueue(Access{Username: "alice"}, applyPatchInput("withdraw", nil))
			if err != nil {
				t.Fatal(err)
			}
			r.mu.Lock()
			caps := &r.nodes["node-a"].registration.Capabilities
			switch bit {
			case "apply_patch":
				caps.ApplyPatch = false
			case "match_metadata":
				caps.ApplyPatchMatchMetadata = false
			case "matching_mode":
				caps.ApplyPatchMatchingMode = false
			}
			r.mu.Unlock()
			if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
				t.Fatalf("withdrawn patch dispatched: %v %v", delivered, err)
			}
			out := wait(t, pending)
			if out.Err == nil || !strings.Contains(out.Err.Error(), "capability withdrawn") || out.Dispatched || out.Result != nil {
				t.Fatalf("withdrawal outcome: %+v", out)
			}
			r.mu.Lock()
			r.nodes["node-a"].registration.Capabilities = patchCapabilities()
			r.mu.Unlock()
			assertPatchQueueEmpty(t, r)
		})
	}
}

func TestApplyPatchRegistrationBaselineUnchanged(t *testing.T) {
	r := testRegistry(t)
	body := registration(t, "node-a", "process-a")
	body.Capabilities = patchCapabilities()
	if _, err := r.Register(testPrincipal("node-a"), body); err == nil {
		t.Fatal("partial patch executor bypassed full G2 admission")
	}
	body = registration(t, "node-a", "process-a")
	body.Capabilities.ApplyPatch, body.Capabilities.ApplyPatchMatchMetadata, body.Capabilities.ApplyPatchMatchingMode, body.Capabilities.ApplyPatchStrictMatching = false, false, false, false
	if _, err := r.Register(testPrincipal("node-a"), body); err != nil {
		t.Fatalf("optional patch bits added to G2 baseline: %v", err)
	}
	if _, err := r.Enqueue(Access{Username: "alice"}, applyPatchInput("unsupported", nil)); err == nil {
		t.Fatal("full baseline falsely implied patch support")
	}
	assertPatchQueueEmpty(t, r)
}

func TestApplyPatchCanonicalRejectionNeverQueues(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities = patchCapabilities()
	r.mu.Unlock()
	for name, change := range map[string]func(*protocol.RunnerRequest){
		"sha":         func(q *protocol.RunnerRequest) { q.ExpectedSHA256 = stringPointer("") },
		"prefix":      func(q *protocol.RunnerRequest) { q.ExpectedPrefix = stringPointer("") },
		"dirs":        func(q *protocol.RunnerRequest) { q.CreateDirs = true },
		"line":        func(q *protocol.RunnerRequest) { q.StartLine = new(uint64) },
		"command":     func(q *protocol.RunnerRequest) { q.Command = "echo no" },
		"stdin":       func(q *protocol.RunnerRequest) { q.Stdin = stringPointer("") },
		"job":         func(q *protocol.RunnerRequest) { q.JobID = stringPointer("") },
		"job_context": func(q *protocol.RunnerRequest) { q.JobContext = json.RawMessage(`{}`) },
		"typed": func(q *protocol.RunnerRequest) {
			q.Process = &protocol.ShellProcessArgv{Executable: "echo", Args: []string{}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			req := applyPatchInput("invalid", nil)
			change(&req)
			if _, err := r.Enqueue(Access{Username: "alice"}, req); err == nil || errors.Is(err, protocol.ErrUnsupported) || errors.Is(err, protocol.ErrUnknownKind) {
				t.Fatalf("invalid patch admitted/misclassified: %v", err)
			}
			assertPatchQueueEmpty(t, r)
		})
	}
}
