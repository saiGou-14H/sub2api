// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func TestOfflineRequiresRegistrationScope(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	queued, err := r.Enqueue(Access{Username: "alice"}, input("keep-queued"))
	if err != nil {
		t.Fatal(err)
	}
	body := protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}
	principal := testPrincipal("node-a")
	principal.Scopes = []string{ScopePoll}
	if err = r.Offline(principal, body); !errors.Is(err, ErrForbidden) {
		t.Fatalf("poll scope allowed shutdown: %v", err)
	}
	view, err := r.View(Access{Username: "alice"}, "node-a")
	if err != nil || !view.Connected || view.PendingRequests != 1 {
		t.Fatalf("unauthorized offline changed state: %+v %v", view, err)
	}
	principal.Scopes = []string{ScopeRegister}
	if err = r.Offline(principal, body); err != nil {
		t.Fatalf("registration scope denied shutdown: %v", err)
	}
	if got := wait(t, queued); got.Err == nil || got.Dispatched {
		t.Fatalf("offline did not settle undelivered request: %+v", got)
	}
	view, err = r.View(Access{Username: "alice"}, "node-a")
	if err != nil || view.Connected || view.Status != "stale" || view.PendingRequests != 0 || view.DisconnectedAt == nil || view.LastSeen >= *view.DisconnectedAt {
		t.Fatalf("offline projection: %+v %v", view, err)
	}
}

func TestRegistrationNormalizesDescriptiveHostContext(t *testing.T) {
	r := testRegistry(t)
	body := registration(t, "node-a", "process-a")
	role, runtime := " build_host ", "  Linux  "
	body.HostContext = &protocol.RunnerHostContext{Role: &role, Runtime: &runtime}
	view, err := r.Register(testPrincipal("node-a"), body)
	if err != nil {
		t.Fatal(err)
	}
	if view.HostContext == nil || *view.HostContext.Role != "build_host" || *view.HostContext.Runtime != "Linux" {
		t.Fatalf("host hints not normalized: %+v", view.HostContext)
	}
	if role != " build_host " || runtime != "  Linux  " {
		t.Fatal("caller host hints mutated")
	}
	pending, err := r.Enqueue(Access{Username: "alice"}, input("pending-count"))
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Cancel(nil)
	if _, err = poll(r, "node-a", "process-a"); err != nil {
		t.Fatal(err)
	}
	view, err = r.View(Access{Username: "alice"}, "node-a")
	if err != nil || view.PendingRequests != 0 {
		t.Fatalf("in-flight request counted as undelivered queue: %+v %v", view, err)
	}
}
