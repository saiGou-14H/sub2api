// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

// Only fictional full-capability nodes are used; this is not a real DSH executor.
func reconciliationRegistry(t *testing.T) (*Registry, protocol.RunnerRegisterRequest) {
	t.Helper()
	r, err := NewRegistry(Options{MaxRunners: 8, MaxPendingPerRunner: 128, MaxJobsPerRunner: 200, OnlineWindow: time.Minute, JobRecoveryGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	reg := registration(t, "node-a", "process-a")
	reg.Capabilities.JobStateReconciliation = true
	setInventory(t, &reg, nil)
	if _, err = r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	return r, reg
}
func setInventory(t *testing.T, reg *protocol.RunnerRegisterRequest, snapshots []protocol.ShellJobSnapshot) {
	t.Helper()
	var err error
	reg.JobInventory, err = json.Marshal(protocol.ShellJobInventory{ActiveComplete: true, Jobs: snapshots})
	if err != nil {
		t.Fatal(err)
	}
}
func reconciliationStart(t *testing.T, r *Registry, id string) protocol.ShellJobSnapshot {
	t.Helper()
	in := jobInvocation(id)
	p := in.Operation.(protocol.JobProcessOperation)
	p.Context.RuntimeProjectID = jobPtr("agent:node-a:project")
	p.Context.WorkflowSessionID = nil
	in.Operation = p
	info, err := r.StartProcessJob(Access{Username: "alice"}, in)
	if err != nil {
		t.Fatal(err)
	}
	wire := jobPoll(t, r)
	c, err := protocol.ReadJobContext(wire.JobContext)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.ShellJobSnapshot{JobID: info.JobID, RequestID: *info.RequestID, Status: "running", UpdateSeq: 1, CreatedAt: info.CreatedAt, StartedAt: jobPtr(info.CreatedAt), Context: c, Stdout: protocol.ShellJobStreamSnapshot{FirstRetainedLine: 1, NextLine: 1}, Stderr: protocol.ShellJobStreamSnapshot{FirstRetainedLine: 1, NextLine: 1}}
}
func sequencedUpdate(id, status string, seq uint64) protocol.RunnerJobUpdateRequest {
	u := jobUpdate(id, status)
	u.UpdateSeq = &seq
	state, _ := protocol.ParseRunnerJobLifecycle(status)
	u.Finished = state.IsTerminal()
	if u.Finished {
		u.CommandExecutionState = jobPtr(protocol.CommandCompleted)
		u.ExitCode = jobPtr(int32(0))
	}
	return u
}
func TestReconciliationInventorySnapshotSequence(t *testing.T) {
	r, reg := reconciliationRegistry(t)
	s := reconciliationStart(t, r, "a")
	s.Stdout = protocol.ShellJobStreamSnapshot{Tail: "line\n", FirstRetainedLine: 5, NextLine: 6, Truncated: true}
	setInventory(t, &reg, []protocol.ShellJobSnapshot{s})
	if _, err := r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	if got := getJob(t, r, "a"); got.Status != "running" || *got.LastUpdateSeq != 1 || got.RecoveredAfterServerRestart {
		t.Fatalf("inventory: %+v", got)
	}
	u := sequencedUpdate("a", "completed", 2)
	u.LogSnapshot = &protocol.ShellJobLogSnapshot{Stdout: s.Stdout, Stderr: s.Stderr}
	want := jobApply(t, r, u)
	observed := r.jobs[s.JobID].observedTerminal
	u = sequencedUpdate("a", "running", 3)
	if got := jobApply(t, r, u); !reflect.DeepEqual(got, want) {
		t.Fatalf("terminal mutated: %+v", got)
	}
	s.UpdateSeq = 4
	setInventory(t, &reg, []protocol.ShellJobSnapshot{s})
	if _, err := r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	if got := getJob(t, r, "a"); !reflect.DeepEqual(got, want) || r.jobs[s.JobID].observedTerminal != observed {
		t.Fatal("register revived terminal")
	}
}
func TestReconciliationSameInstanceRecovery(t *testing.T) {
	r, reg := reconciliationRegistry(t)
	s := reconciliationStart(t, r, "a")
	setInventory(t, &reg, []protocol.ShellJobSnapshot{s})
	if _, err := r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	if err := r.Offline(jobPrincipal(), protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); err != nil {
		t.Fatal(err)
	}
	if r.jobs[s.JobID].recoveringSince == nil {
		t.Fatal("not recovering")
	}
	u := sequencedUpdate("a", "running", 2)
	if _, err := r.UpdateJob(jobPrincipal(), u); err == nil {
		t.Fatal("recovery accepted chunks only")
	}
	u.LogSnapshot = &protocol.ShellJobLogSnapshot{Stdout: s.Stdout, Stderr: s.Stderr}
	if got := jobApply(t, r, u); got.RecoveryState == nil || *got.RecoveryState != "reconciled" {
		t.Fatalf("recovery: %+v", got)
	}
	setInventory(t, &reg, nil)
	if _, err := r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	if got := getJob(t, r, "a"); got.Status != "lost" || *got.RecoveryReasonCode != "runner_inventory_missing" {
		t.Fatalf("missing: %+v", got)
	}
}
