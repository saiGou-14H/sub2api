// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

// Independent fixture from runner_protocol.rs:379-402; never enable a capability
// in production based on this test fixture or a generation number.
const completeRegistration = `{"client_id":"node-a","agent_instance_id":"process-a","agent_protocol_generation":2,"capabilities":{"shell":true,"file_read":true,"file_write":true,"artifact_export_chunk_read":true,"artifact_export_streaming_metadata":true,"structured_file_delete":true,"apply_text_edit_occurrence":true,"jobs":true,"async_jobs":true,"async_shell_jobs":true,"structured_validation_argv":true,"structured_cargo_test_count_assertion":true,"structured_go_test_json":true,"structured_go_test_tool":true,"structured_go_test_packages":true,"structured_process_argv":true,"structured_script_payload":true,"internal_posix_script":true,"structured_execution_jobs":true,"lsp_read_only_navigation":true,"lsp_call_hierarchy":true,"project_lifecycle":true,"project_path_registration":true}}`

func testPrincipal(clientID string) Principal {
	return Principal{Kind: AgentToken, Username: "alice", AllowedClientID: clientID, Scopes: []string{ScopeRegister, ScopePoll, ScopeResult}}
}
func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := NewRegistry(Options{MaxRunners: 8, MaxPendingPerRunner: 64, OnlineWindow: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}
func registration(t *testing.T, clientID, instanceID string) protocol.RunnerRegisterRequest {
	t.Helper()
	v, err := protocol.ReadRegisterRequest([]byte(completeRegistration))
	if err != nil {
		t.Fatal(err)
	}
	v.ClientID = clientID
	v.AgentInstanceID = instanceID
	return v
}
func register(t *testing.T, r *Registry, clientID, instanceID string) {
	t.Helper()
	if _, err := r.Register(testPrincipal(clientID), registration(t, clientID, instanceID)); err != nil {
		t.Fatal(err)
	}
}
func input(id string) protocol.RunnerRequest {
	return protocol.RunnerRequest{RequestID: id, ClientID: "node-a", Kind: "run_shell", Command: "printf test", TimeoutSecs: 30, RequestedBy: "alice", CreatedAt: 1}
}
func poll(r *Registry, clientID, instanceID string) (*protocol.RunnerRequest, error) {
	return r.Poll(testPrincipal(clientID), protocol.RunnerPollPayload{RunnerPollRequest: protocol.RunnerPollRequest{ClientID: clientID, AgentInstanceID: instanceID}})
}
func result(clientID, instanceID, id string) protocol.RunnerResultPayload {
	code := int32(0)
	stdout := "test"
	state := protocol.CommandCompleted
	return protocol.RunnerResultPayload{RunnerResultRequest: protocol.RunnerResultRequest{ClientID: clientID, AgentInstanceID: instanceID, RequestID: id, ExitCode: &code, Stdout: &stdout}, CommandExecutionState: &state}
}
func wait(t *testing.T, p *Pending) Outcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return p.Wait(ctx)
}

func TestGenerationAndCredentialAdmission(t *testing.T) {
	r := testRegistry(t)
	minimal, err := protocol.ReadRegisterRequest([]byte(`{"client_id":"node-a","agent_instance_id":"process-a","agent_protocol_generation":2,"capabilities":{"shell":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Register(testPrincipal("node-a"), minimal); err == nil || !strings.Contains(err.Error(), "capability") {
		t.Fatalf("partial generation accepted: %v", err)
	}
	for _, kind := range []string{"", "user_token", "oauth2_token", "api_key"} {
		p := testPrincipal("node-a")
		p.Kind = kind
		if _, err = r.Register(p, registration(t, "node-a", "process-a")); !errors.Is(err, ErrForbidden) {
			t.Fatalf("credential kind %q admitted: %v", kind, err)
		}
	}
	p := testPrincipal("node-a")
	p.AllowedClientID = "node-b"
	if _, err = r.Register(p, registration(t, "node-a", "process-a")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("client mismatch: %v", err)
	}
	p = testPrincipal("node-a")
	p.Scopes = nil
	if _, err = r.Register(p, registration(t, "node-a", "process-a")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing scope: %v", err)
	}
	body := registration(t, "node-a", "process-a")
	bob := "bob"
	body.Owner = &bob
	if _, err = r.Register(testPrincipal("node-a"), body); !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner mismatch: %v", err)
	}
	view, err := r.Register(testPrincipal("node-a"), registration(t, "node-a", "process-a"))
	if err != nil {
		t.Fatal(err)
	}
	if view.Owner == nil || *view.Owner != "alice" || !view.Connected {
		t.Fatalf("effective owner/lease missing: %+v", view)
	}
	*view.Owner = "bob"
	if _, err = r.Enqueue(Access{Username: "bob", GlobalVisibility: true}, input("not-bobs")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("snapshot mutation or admin bypass: %v", err)
	}
}

func TestPollOnceAndExactResultOwnership(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	register(t, r, "node-b", "process-b")
	p, err := r.Enqueue(Access{Username: "alice"}, input("request-a"))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Complete(testPrincipal("node-a"), result("node-a", "process-a", "request-a")); err == nil {
		t.Fatal("undispatched result accepted")
	}
	request, err := poll(r, "node-a", "process-a")
	if err != nil || request == nil || request.RequestID != "request-a" {
		t.Fatalf("poll: %+v %v", request, err)
	}
	request.Command = "mutated"
	if request, err = poll(r, "node-a", "process-a"); err != nil || request != nil {
		t.Fatalf("duplicate delivery: %+v %v", request, err)
	}
	if err = r.Complete(testPrincipal("node-b"), result("node-b", "process-b", "request-a")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-node result: %v", err)
	}
	if err = r.Complete(testPrincipal("node-a"), result("node-a", "process-a", "request-a")); err != nil {
		t.Fatal(err)
	}
	outcome := wait(t, p)
	if outcome.Err != nil || !outcome.Dispatched || outcome.Result == nil || *outcome.Result.Stdout != "test" {
		t.Fatalf("outcome: %+v", outcome)
	}
	if err = r.Complete(testPrincipal("node-a"), result("node-a", "process-a", "request-a")); !errors.Is(err, ErrUnknown) {
		t.Fatalf("consumed result: %v", err)
	}
	*outcome.Result.Stdout = "mutated"
	if fresh := wait(t, p); *fresh.Result.Stdout != "test" {
		t.Fatal("caller mutated stored outcome")
	}
}

func TestInstanceReplacementAndDelayedOffline(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	sent, _ := r.Enqueue(Access{Username: "alice"}, input("sent"))
	if _, err := poll(r, "node-a", "process-a"); err != nil {
		t.Fatal(err)
	}
	queued, _ := r.Enqueue(Access{Username: "alice"}, input("queued"))
	register(t, r, "node-a", "process-b")
	if got := wait(t, sent); !errors.Is(got.Err, ErrStale) || !got.Dispatched {
		t.Fatalf("sent evidence: %+v", got)
	}
	if got := wait(t, queued); !errors.Is(got.Err, ErrStale) || got.Dispatched {
		t.Fatalf("queued evidence: %+v", got)
	}
	if req, err := poll(r, "node-a", "process-b"); err != nil || req != nil {
		t.Fatalf("replacement inherited work: %+v %v", req, err)
	}
	if _, err := poll(r, "node-a", "process-a"); !errors.Is(err, ErrStale) {
		t.Fatalf("stale poll: %v", err)
	}
	if _, err := r.Register(testPrincipal("node-a"), registration(t, "node-a", "process-a")); !errors.Is(err, ErrStale) {
		t.Fatalf("retired reclaim: %v", err)
	}
	if err := r.Complete(testPrincipal("node-a"), result("node-a", "process-a", "sent")); !errors.Is(err, ErrStale) {
		t.Fatalf("stale complete: %v", err)
	}
	if err := r.Offline(testPrincipal("node-a"), protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); err != nil {
		t.Fatal(err)
	}
	view, err := r.View(Access{Username: "alice"}, "node-a")
	if err != nil || !view.Connected || view.AgentInstanceID != "process-b" || view.PendingRequests != 0 {
		t.Fatalf("new lease changed: %+v %v", view, err)
	}
}

func TestReconnectDoesNotRedeliver(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	p, _ := r.Enqueue(Access{Username: "alice"}, input("sent"))
	if _, err := poll(r, "node-a", "process-a"); err != nil {
		t.Fatal(err)
	}
	register(t, r, "node-a", "process-a")
	if req, err := poll(r, "node-a", "process-a"); err != nil || req != nil {
		t.Fatalf("redelivered on reconnect: %+v %v", req, err)
	}
	if err := r.Complete(testPrincipal("node-a"), result("node-a", "process-a", "sent")); err != nil {
		t.Fatal(err)
	}
	if got := wait(t, p); got.Err != nil {
		t.Fatal(got.Err)
	}
}

func TestCancellationAndClosePreserveDispatchEvidence(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	p, _ := r.Enqueue(Access{Username: "alice"}, input("cancel-before"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := p.Wait(ctx); !errors.Is(got.Err, context.Canceled) || got.Dispatched {
		t.Fatalf("before: %+v", got)
	}
	if req, err := poll(r, "node-a", "process-a"); err != nil || req != nil {
		t.Fatalf("cancelled delivered: %+v %v", req, err)
	}
	p, _ = r.Enqueue(Access{Username: "alice"}, input("cancel-after"))
	if _, err := poll(r, "node-a", "process-a"); err != nil {
		t.Fatal(err)
	}
	if got := p.Wait(ctx); !errors.Is(got.Err, context.Canceled) || !got.Dispatched {
		t.Fatalf("after: %+v", got)
	}
	p, _ = r.Enqueue(Access{Username: "alice"}, input("on-close"))
	r.Close()
	if got := wait(t, p); !errors.Is(got.Err, ErrClosed) || got.Dispatched {
		t.Fatalf("close: %+v", got)
	}
	if _, err := r.Enqueue(Access{Username: "alice"}, input("closed")); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed admitted: %v", err)
	}
}

func TestSharedKeyIsolationIgnoresOwner(t *testing.T) {
	r := testRegistry(t)
	p := Principal{Kind: SharedKey, SharedKeyHash: strings.Repeat("a", 64), Scopes: []string{ScopeRegister, ScopePoll, ScopeResult}}
	body := registration(t, "node-a", "process-a")
	value := "arbitrary"
	body.Owner = &value
	view, err := r.Register(p, body)
	if err != nil || view.Owner != nil {
		t.Fatalf("shared registration: %+v %v", view, err)
	}
	other := p
	other.SharedKeyHash = strings.Repeat("b", 64)
	if _, err = r.Register(other, registration(t, "node-a", "process-b")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("group hijack: %v", err)
	}
	if _, err = r.Enqueue(Access{Username: "arbitrary"}, input("owner-does-not-grant")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner granted access: %v", err)
	}
	pending, err := r.Enqueue(Access{GroupKind: "shared_key", GroupID: p.SharedKeyHash}, input("group-owned"))
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Cancel(nil)
	if _, err = r.Poll(other, protocol.RunnerPollPayload{RunnerPollRequest: protocol.RunnerPollRequest{ClientID: "node-a", AgentInstanceID: "process-a"}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross group poll: %v", err)
	}
}

func TestCapabilitiesAndUnsupportedDispatch(t *testing.T) {
	r := testRegistry(t)
	body := registration(t, "node-a", "process-a")
	body.Capabilities.Shell = false
	if _, err := r.Register(testPrincipal("node-a"), body); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue(Access{Username: "alice"}, input("disabled-shell")); err == nil {
		t.Fatal("disabled shell admitted")
	}
	for _, kind := range []string{"run_process", "run_script"} {
		typed := input(kind)
		typed.Kind, typed.Command = kind, ""
		if kind == "run_process" {
			typed.Process = &protocol.ShellProcessArgv{Executable: "printf", Args: []string{"typed"}}
		} else {
			typed.Script = &protocol.ShellScriptPayload{Language: protocol.ScriptSh, Script: "printf typed"}
		}
		pending, err := r.Enqueue(Access{Username: "alice"}, typed)
		if err != nil {
			t.Fatalf("typed capability depends on raw shell: %v", err)
		}
		pending.Cancel(nil)
	}
	req := input("future")
	req.Kind = "computer_observe"
	if _, err := r.Enqueue(Access{Username: "alice"}, req); err == nil {
		t.Fatal("unknown family admitted")
	}
	req = input("job")
	id := "job-1"
	req.JobID = &id
	if _, err := r.Enqueue(Access{Username: "alice"}, req); err == nil {
		t.Fatal("Job request admitted as synchronous shell")
	}
}

func TestConcurrentPollingDeliversOneRequestOnce(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	const count = 32
	pending := make([]*Pending, count)
	for i := 0; i < count; i++ {
		var err error
		pending[i], err = r.Enqueue(Access{Username: "alice"}, input(fmt.Sprintf("parallel-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := make(chan string, count)
	errs := make(chan error, count)
	var group sync.WaitGroup
	for i := 0; i < count; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			req, err := poll(r, "node-a", "process-a")
			if err != nil {
				errs <- err
				return
			}
			if req == nil {
				errs <- errors.New("missing request")
				return
			}
			seen <- req.RequestID
			errs <- r.Complete(testPrincipal("node-a"), result("node-a", "process-a", req.RequestID))
		}()
	}
	group.Wait()
	close(seen)
	close(errs)
	ids := map[string]bool{}
	for id := range seen {
		if ids[id] {
			t.Fatalf("duplicate %s", id)
		}
		ids[id] = true
	}
	if len(ids) != count {
		t.Fatalf("got %d requests", len(ids))
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range pending {
		if got := wait(t, p); got.Err != nil || !got.Dispatched {
			t.Fatalf("outcome: %+v", got)
		}
	}
}
