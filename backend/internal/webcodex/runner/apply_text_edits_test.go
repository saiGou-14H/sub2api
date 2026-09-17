// SPDX-License-Identifier: Apache-2.0
// Source: WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// runner-registry requests.rs:328-357,391-557, plus the host FileWrite matrix.
// Generic selector routing only; dedicated business dispatch remains deferred.
package runner

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func applyTextEditsInput(id string, content *string) protocol.RunnerRequest {
	req := input(id)
	req.Kind, req.Command, req.Path, req.Content = "file_apply_text_edits", "", stringPointer("."), content
	return req
}

func TestApplyTextEditsSynchronousDispatch(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	// First satisfy full G2 admission, then isolate the host mutation-matrix bit.
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities = protocol.RunnerCapabilities{FileWrite: true}
	r.mu.Unlock()
	// Generic occurrence-only ingress keeps opaque content and adds no selector gate.
	for _, content := range []*string{nil, stringPointer(`{"changes":[]}`), stringPointer(`{"changes":[],"changes":[{}]}`), stringPointer(`not JSON`), stringPointer(`{"changes":[{"edits":[{"occurrence":18446744073709551615,"line_scope":null}]}]}`)} {
		req := applyTextEditsInput("apply-text-edits", content)
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
			t.Fatalf("opaque edits changed: %v %v", delivered, err)
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
		stdout, code := "fixture supplied text-edit result", int32(0)
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

func TestApplyTextEditsCapabilityRouting(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	for _, caps := range []protocol.RunnerCapabilities{{}, {FileRead: true}, {StructuredFileDelete: true}, {ApplyTextEditOccurrence: true}, {ApplyTextEditLineScope: true}, {ApplyTextEditOccurrence: true, ApplyTextEditLineScope: true}} {
		r.mu.Lock()
		r.nodes["node-a"].registration.Capabilities = caps
		r.mu.Unlock()
		if _, err := r.Enqueue(Access{Username: "alice"}, applyTextEditsInput("missing-file-write", nil)); err == nil || !strings.Contains(err.Error(), "capability not advertised") {
			t.Fatalf("FileWrite bypass with %+v: %v", caps, err)
		}
		if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
			t.Fatalf("capability rejection queued: %v %v", delivered, err)
		}
	}
}

func TestApplyTextEditsCapabilityWithdrawalBeforePoll(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	pending, err := r.Enqueue(Access{Username: "alice"}, applyTextEditsInput("withdrawn", nil))
	if err != nil {
		t.Fatal(err)
	}
	// Isolate poll's capability recheck after original-valid admission.
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities.FileWrite = false
	r.mu.Unlock()
	if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
		t.Fatalf("withdrawn FileWrite dispatched: %v %v", delivered, err)
	}
	out := wait(t, pending)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "capability withdrawn") || out.Dispatched {
		t.Fatalf("withdrawal outcome: %+v", out)
	}
}

func TestApplyTextEditsGenericSelectorRequirements(t *testing.T) {
	// Requirements follow generic requests.rs:328-357,391-415; these are presence
	// checks, not the Runner's typed edit/selector validation.
	cases := []struct {
		name, content         string
		lineScope, occurrence bool
	}{
		{"empty", "", false, false},
		{"null", `null`, false, false},
		{"malformed", `{"changes":[{"edits":[{"line_scope":{}}]}]`, false, false},
		{"trailing_json", `{"changes":[{"edits":[{"line_scope":{}}]}]} {}`, false, false},
		{"positional_payload", `[[{"edits":[{"line_scope":{}}]}]]`, false, false},
		{"positional_change", `{"changes":[["edit","file",[{"line_scope":{}}]]]}`, false, false},
		{"positional_edit", `{"changes":[{"edits":[["replace_exact",{"line_scope":{}}]]}]}`, false, false},
		{"nonarray_changes", `{"changes":{"edits":[{"line_scope":{}}]}}`, false, false},
		{"nonarray_edits", `{"changes":[{"edits":{"line_scope":{}}}]}`, false, false},
		{"irrelevant_fields", `{"line_scope":{},"occurrence":1,"changes":[{"line_scope":{},"edits":[{"metadata":{"line_scope":{}}}]}]}`, false, false},
		{"null_selectors", `{"changes":[{"edits":[{"line_scope": null ,"occurrence": null}]}]}`, false, false},
		{"occurrence_only", `{"changes":[{"edits":[{"occurrence":18446744073709551615}]}]}`, false, false},
		{"invalid_occurrence_only", `{"changes":[{"edits":[{"occurrence":false}]}]}`, false, false},
		{"scope", `{"changes":[{"edits":[{"line_scope":{"start_line":1,"end_line":2}}]}]}`, true, false},
		{"malformed_scope", `{"changes":[{"edits":[{"line_scope":false}]}]}`, true, false},
		{"empty_scope", `{"changes":[{"edits":[{"line_scope":""}]}]}`, true, false},
		{"both", `{"changes":[{"edits":[{"line_scope":{},"occurrence":18446744073709551615}]}]}`, true, true},
		{"invalid_occurrence_with_scope", `{"changes":[{"edits":[{"line_scope":{},"occurrence":false}]}]}`, true, true},
		{"across_changes", `{"changes":[null,{},false,{"edits":[null,[],{"occurrence":1}]},{"edits":[{"line_scope":{}}]}]}`, true, true},
		{"across_changes_reverse", `{"changes":[{"edits":[{"line_scope":{}}]},{"edits":[{"occurrence":1}]}]}`, true, true},
		{"duplicate_changes_clear", `{"changes":[{"edits":[{"line_scope":{}}]}],"changes":null}`, false, false},
		{"duplicate_changes_set", `{"changes":null,"changes":[{"edits":[{"line_scope":{}}]}]}`, true, false},
		{"duplicate_edits_clear", `{"changes":[{"edits":[{"line_scope":{}}],"edits":[]}]}`, false, false},
		{"duplicate_edits_set", `{"changes":[{"edits":[],"edits":[{"line_scope":{}}]}]}`, true, false},
		{"duplicate_scope_clear", `{"changes":[{"edits":[{"line_scope":{},"line_scope":null,"occurrence":1}]}]}`, false, false},
		{"duplicate_scope_set", `{"changes":[{"edits":[{"line_scope":null,"line_scope":{}}]}]}`, true, false},
		{"duplicate_occurrence_clear", `{"changes":[{"edits":[{"line_scope":{},"occurrence":1,"occurrence":null}]}]}`, true, false},
		{"duplicate_occurrence_set", `{"changes":[{"edits":[{"line_scope":{},"occurrence":null,"occurrence":1}]}]}`, true, true},
		{"raw_wide_integers", `{"wide":18446744073709551615,"negative":-9223372036854775808,"changes":[{"edits":[{"line_scope":{"start_line":18446744073709551615},"occurrence":9007199254740993}]}]}`, true, true},
		{"escaped_field_names", `{"changes":[{"edits":[{"line_\u0073cope":{},"occurrenc\u0065":1}]}]}`, true, true},
	}
	capabilities := []struct {
		name string
		caps protocol.RunnerCapabilities
	}{
		{"file_write", protocol.RunnerCapabilities{FileWrite: true}},
		{"occurrence", protocol.RunnerCapabilities{FileWrite: true, ApplyTextEditOccurrence: true}},
		{"line_scope", protocol.RunnerCapabilities{FileWrite: true, ApplyTextEditLineScope: true}},
		{"all", protocol.RunnerCapabilities{FileWrite: true, ApplyTextEditOccurrence: true, ApplyTextEditLineScope: true}},
		{"no_file_write", protocol.RunnerCapabilities{ApplyTextEditOccurrence: true, ApplyTextEditLineScope: true}},
	}
	for _, tc := range cases {
		for _, capability := range capabilities {
			t.Run(tc.name+"/"+capability.name, func(t *testing.T) {
				r := testRegistry(t)
				register(t, r, "node-a", "process-a")
				r.mu.Lock()
				r.nodes["node-a"].registration.Capabilities = capability.caps
				r.mu.Unlock()
				req := applyTextEditsInput("selector", stringPointer(tc.content))
				want := capability.caps.FileWrite && (!tc.lineScope || capability.caps.ApplyTextEditLineScope && (!tc.occurrence || capability.caps.ApplyTextEditOccurrence))
				pending, err := r.Enqueue(Access{Username: "alice"}, req)
				if !want {
					if pending != nil || err == nil || !strings.Contains(err.Error(), "capability not advertised") {
						t.Fatalf("selector capability bypass: %v %v", pending, err)
					}
					r.mu.Lock()
					queued, retained := len(r.nodes["node-a"].queue), len(r.pending)
					r.mu.Unlock()
					if queued != 0 || retained != 0 {
						t.Fatalf("denied selector was retained: queue=%d pending=%d", queued, retained)
					}
					if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
						t.Fatalf("denied selector dispatched: %v %v", delivered, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				delivered, err := poll(r, "node-a", "process-a")
				if err != nil || delivered == nil || !reflect.DeepEqual(*delivered, req) {
					t.Fatalf("selector content changed/blocked: %v %v", delivered, err)
				}
				if again, err := poll(r, "node-a", "process-a"); err != nil || again != nil {
					t.Fatalf("selector request replay: %v %v", again, err)
				}
			})
		}
	}
}

func TestApplyTextEditsSelectorWithdrawalBeforePoll(t *testing.T) {
	for _, withdrawn := range []string{"file_write", "line_scope", "occurrence"} {
		t.Run(withdrawn, func(t *testing.T) {
			r := testRegistry(t)
			body := registration(t, "node-a", "process-a")
			body.Capabilities.ApplyTextEditLineScope = true
			if _, err := r.Register(testPrincipal("node-a"), body); err != nil {
				t.Fatal(err)
			}
			req := applyTextEditsInput("withdraw-selector", stringPointer(`{"changes":[{"edits":[{"line_scope":{}}]},{"edits":[{"occurrence":1}]}]}`))
			pending, err := r.Enqueue(Access{Username: "alice"}, req)
			if err != nil {
				t.Fatal(err)
			}
			r.mu.Lock()
			caps := &r.nodes["node-a"].registration.Capabilities
			switch withdrawn {
			case "file_write":
				caps.FileWrite = false
			case "line_scope":
				caps.ApplyTextEditLineScope = false
			case "occurrence":
				caps.ApplyTextEditOccurrence = false
			}
			r.mu.Unlock()
			if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
				t.Fatalf("withdrawn selector dispatched: %v %v", delivered, err)
			}
			out := wait(t, pending)
			if out.Err == nil || !strings.Contains(out.Err.Error(), "capability withdrawn") || out.Dispatched || out.Result != nil {
				t.Fatalf("selector withdrawal outcome: %+v", out)
			}
			// Restoring capabilities cannot replay a request already settled as unstarted.
			r.mu.Lock()
			r.nodes["node-a"].registration.Capabilities = body.Capabilities
			r.mu.Unlock()
			if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
				t.Fatalf("settled selector replayed: %v %v", delivered, err)
			}
		})
	}
}

func TestApplyTextEditsSelectorScanIsKindScoped(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	r.mu.Lock()
	r.nodes["node-a"].registration.Capabilities = protocol.RunnerCapabilities{StructuredFileDelete: true}
	r.mu.Unlock()
	req := applyTextEditsInput("delete-opaque", stringPointer(`{"changes":[{"edits":[{"line_scope":{},"occurrence":1}]}]}`))
	req.Kind = "file_delete_project_files"
	if _, err := r.Enqueue(Access{Username: "alice"}, req); err != nil {
		t.Fatal(err)
	}
	if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered == nil || !reflect.DeepEqual(*delivered, req) {
		t.Fatalf("selector scan crossed kind boundary: %v %v", delivered, err)
	}
}

func TestApplyTextEditsCanonicalRejection(t *testing.T) {
	r := testRegistry(t)
	for _, missing := range []string{"file_write", "apply_text_edit_occurrence"} {
		body := registration(t, "node-a", "process-a")
		if missing == "file_write" {
			body.Capabilities.FileWrite = false
		} else {
			body.Capabilities.ApplyTextEditOccurrence = false
		}
		if _, err := r.Register(testPrincipal("node-a"), body); err == nil {
			t.Fatalf("G2 baseline relaxed: %s", missing)
		}
	}
	register(t, r, "node-a", "process-a")
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
			req := applyTextEditsInput("invalid-text-edits", nil)
			change(&req)
			if _, err := r.Enqueue(Access{Username: "alice"}, req); err == nil || errors.Is(err, protocol.ErrUnsupported) || errors.Is(err, protocol.ErrUnknownKind) {
				t.Fatalf("invalid edits admitted/misclassified: %v", err)
			}
			if delivered, err := poll(r, "node-a", "process-a"); err != nil || delivered != nil {
				t.Fatalf("invalid edits queued: %v %v", delivered, err)
			}
		})
	}
}
