// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"errors"
	"strings"
	"testing"
)

func TestGlobalObservationDoesNotGrantManagedExecution(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	observer := Access{Username: "bob", GlobalVisibility: true}
	view, err := r.View(observer, "node-a")
	if err != nil || view.Owner == nil || *view.Owner != "alice" {
		t.Fatalf("global observer denied: %+v %v", view, err)
	}
	if _, err = r.Enqueue(observer, input("not-authorized")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("observation granted execution: %v", err)
	}
	if _, err = r.View(Access{Username: "bob"}, "node-a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unprivileged observer admitted: %v", err)
	}
}

func TestReplacementCannotChangeAuthenticatedPartition(t *testing.T) {
	for _, kind := range []string{"shared_key", "project_grant"} {
		t.Run(kind, func(t *testing.T) {
			r := testRegistry(t)
			principal := testPrincipal("node-a")
			access := Access{Username: "alice", GroupKind: kind, GroupID: "grant-a"}
			if kind == "shared_key" {
				principal.Kind = SharedKey
				principal.SharedKeyHash = strings.Repeat("a", 64)
				access.GroupID = principal.SharedKeyHash
			} else {
				principal.ProjectGrantID = access.GroupID
			}
			if _, err := r.Register(principal, registration(t, "node-a", "process-a")); err != nil {
				t.Fatal(err)
			}
			pending, err := r.Enqueue(access, input("partition-owned"))
			if err != nil {
				t.Fatal(err)
			}
			defer pending.Cancel(nil)
			for _, instance := range []string{"process-a", "process-b"} {
				if _, err = r.Register(Principal{Kind: Bootstrap}, registration(t, "node-a", instance)); !errors.Is(err, ErrForbidden) {
					t.Fatalf("bootstrap changed %s partition: %v", kind, err)
				}
			}
			view, err := r.View(access, "node-a")
			if err != nil || view.AgentInstanceID != "process-a" || view.PendingRequests != 1 {
				t.Fatalf("failed takeover mutated lease: %+v %v", view, err)
			}
		})
	}
}
