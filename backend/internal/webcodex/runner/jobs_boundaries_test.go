// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func TestJobsDispatchEvidenceSurvivesLegacyQueuedAndSaturation(t *testing.T) {
	r, err := NewRegistry(Options{MaxRunners: 1, MaxPendingPerRunner: 1, MaxJobsPerRunner: 2, OnlineWindow: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	register(t, r, "node-a", "process-a")
	startJob(t, r, "a")
	p := jobPoll(t, r)
	if len(r.pending) != 0 || len(r.requestToJob) != 1 {
		t.Fatal("active Job retained polling capacity")
	}
	syncInput := input(p.RequestID)
	if _, err := r.Enqueue(Access{Username: "alice"}, syncInput); err == nil {
		t.Fatal("synchronous request captured Job request ID")
	}
	u := jobUpdate("a", "queued")
	jobApply(t, r, u)
	v, err := r.StopJob(Access{Username: "alice"}, "job-a", "alice")
	if err != nil || v.Status != "stop_requested" || v.EndedAt != nil || v.CommandExecutionState != nil {
		t.Fatal("dispatched queued treated as local stop", v, err)
	}
	stop := jobPoll(t, r)
	if stop.Kind != "stop_job" {
		t.Fatal(stop.Kind)
	}
	u.Status = "running"
	jobApply(t, r, u)
	_, err = r.StopJob(Access{Username: "alice"}, "job-a", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if next, err := poll(r, "node-a", "process-a"); err != nil || next != nil {
		t.Fatal("Runner state regression replayed accepted stop", next, err)
	}
	// Full polling queue is still bounded and rejection does not register a stop.
	startJob(t, r, "b")
	jobPoll(t, r)
	pending, err := r.Enqueue(Access{Username: "alice"}, input("sync-busy"))
	if err != nil {
		t.Fatal(err)
	}
	before := getJob(t, r, "b")
	bindings := len(r.requestToJob)
	if _, err = r.StopJob(Access{Username: "alice"}, "job-b", "alice"); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if r.jobs["job-b"].stopRegistered || !reflect.DeepEqual(before, getJob(t, r, "b")) || len(r.requestToJob) != bindings || len(r.pending) != 1 || len(r.nodes["node-a"].queue) != 1 {
		t.Fatal("failed stop mutated Job")
	}
	pending.Cancel(nil)
	if _, err = r.StopJob(Access{Username: "alice"}, "job-b", "alice"); err != nil {
		t.Fatal(err)
	}
	if len(r.pending) != 1 || len(r.nodes["node-a"].queue) != 1 {
		t.Fatal("stop did not consume pending capacity")
	}
	if _, err := r.Enqueue(Access{Username: "alice"}, input("blocked-by-stop")); !errors.Is(err, ErrCapacity) {
		t.Fatal("stop bypassed pending budget", err)
	}
	if p := jobPoll(t, r); p.Kind != "stop_job" {
		t.Fatal("missing stop control", p.Kind)
	}
	if len(r.pending) != 0 {
		t.Fatal("polled stop retained pending capacity")
	}
}

func TestJobsCapturedSharedGroupAndManagedOwnerIsolation(t *testing.T) {
	r := jobsRegistry(t, 8)
	shared := Principal{Kind: SharedKey, SharedKeyHash: strings.Repeat("a", 64), Scopes: []string{ScopeRegister, ScopePoll, ScopeJobUpdate}}
	access := Access{GroupKind: "shared_key", GroupID: shared.SharedKeyHash}
	if _, err := r.Register(shared, registration(t, "shared-node", "shared-process")); err != nil {
		t.Fatal(err)
	}
	invocation := jobInvocation("shared")
	invocation.Metadata.ClientID = "shared-node"
	v, err := r.StartProcessJob(access, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Poll(shared, protocol.RunnerPollPayload{RunnerPollRequest: protocol.RunnerPollRequest{ClientID: "shared-node", AgentInstanceID: "shared-process"}}); err != nil {
		t.Fatal(err)
	}
	update := jobUpdate("shared", "running")
	update.ClientID = "shared-node"
	update.AgentInstanceID = "shared-process"
	if _, err := r.UpdateJob(shared, update); err != nil {
		t.Fatal(err)
	}
	for _, foreign := range []Access{{Username: "alice"}, {GroupKind: "shared_key", GroupID: strings.Repeat("b", 64)}, {GroupKind: "project_grant", GroupID: shared.SharedKeyHash}} {
		if _, err := r.GetJob(foreign, v.JobID); !errors.Is(err, ErrUnknownJob) {
			t.Fatal("foreign observed shared Job", err)
		}
		if _, err := r.StopJob(foreign, v.JobID, "whoever"); !errors.Is(err, ErrUnknownJob) {
			t.Fatal("foreign stopped shared Job", err)
		}
	}
	foreign := shared
	foreign.SharedKeyHash = strings.Repeat("b", 64)
	if _, err := r.UpdateJob(foreign, update); !errors.Is(err, ErrForbidden) {
		t.Fatal("foreign shared update", err)
	}
	// Job-level partition remains authoritative even after a registration is gone.
	r.mu.Lock()
	delete(r.nodes, "shared-node")
	r.mu.Unlock()
	v, err = r.GetJob(access, v.JobID)
	if err != nil || v.Status != "lost" {
		t.Fatal(v, err)
	}
	if _, err := r.GetJob(Access{GroupKind: "shared_key", GroupID: strings.Repeat("b", 64)}, v.JobID); !errors.Is(err, ErrUnknownJob) {
		t.Fatal(err)
	}
}

func TestJobsTerminalReplayDoesNotExtendRetentionAndCapabilityRefusal(t *testing.T) {
	r := jobsRegistry(t, 2)
	startJob(t, r, "a")
	jobPoll(t, r)
	u := jobUpdate("a", "failed")
	u.CommandExecutionState = jobPtr(protocol.CommandOutcomeUnknown)
	u.Finished = true
	jobApply(t, r, u)
	observed := *r.jobs["job-a"].observedTerminal
	if err := r.Offline(jobPrincipal(), protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); err != nil {
		t.Fatal(err)
	}
	jobApply(t, r, u)
	if r.nodes["node-a"].disconnectedAt != nil {
		t.Fatal("terminal replay did not refresh current instance")
	}
	jobApply(t, r, u)
	if observed != *r.jobs["job-a"].observedTerminal {
		t.Fatal("replay changed terminal observation")
	}
	r.jobs["job-a"].observedTerminal = jobPtr(time.Now().Add(-jobTerminalRetention + time.Minute))
	jobApply(t, r, u)
	if time.Since(*r.jobs["job-a"].observedTerminal) < jobTerminalRetention-time.Minute {
		t.Fatal("replay extended TTL")
	}
	for _, mutate := range []func(*protocol.RunnerRegisterRequest){
		func(v *protocol.RunnerRegisterRequest) { v.Capabilities.JobStateReconciliation = true },
		func(v *protocol.RunnerRegisterRequest) { v.JobInventory = []byte(`{"active_complete":true,"jobs":[]}`) },
	} {
		registration := registration(t, "node-a", "process-a")
		mutate(&registration)
		if _, err := r.Register(jobPrincipal(), registration); !errors.Is(err, protocol.ErrUnsupported) {
			t.Fatal("reconciliation admitted", err)
		}
	}
	u.LogSnapshot = &protocol.ShellJobLogSnapshot{}
	if _, err := r.UpdateJob(jobPrincipal(), u); !errors.Is(err, protocol.ErrUnsupported) {
		t.Fatal("terminal bypassed snapshot gate", err)
	}
}
