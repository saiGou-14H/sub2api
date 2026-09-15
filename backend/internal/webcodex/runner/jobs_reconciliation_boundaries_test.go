// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func TestReconciliationUpdateWireRejectionsAtomic(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*protocol.RunnerJobUpdateRequest)
	}{
		{"missing sequence", func(u *protocol.RunnerJobUpdateRequest) { u.UpdateSeq = nil }},
		{"zero sequence", func(u *protocol.RunnerJobUpdateRequest) { u.UpdateSeq = jobPtr(uint64(0)) }},
		{"whitespace", func(u *protocol.RunnerJobUpdateRequest) { u.Status = " running " }},
		{"server queued", func(u *protocol.RunnerJobUpdateRequest) { u.Status = "queued" }},
		{"legacy started", func(u *protocol.RunnerJobUpdateRequest) { u.Status = "started" }},
		{"active finished", func(u *protocol.RunnerJobUpdateRequest) { u.Finished = true }},
		{"active exit", func(u *protocol.RunnerJobUpdateRequest) { u.ExitCode = jobPtr(int32(0)) }},
		{"active duration", func(u *protocol.RunnerJobUpdateRequest) { u.DurationMS = jobPtr(uint64(1)) }},
		{"terminal unfinished", func(u *protocol.RunnerJobUpdateRequest) { u.Status = "completed"; u.ExitCode = jobPtr(int32(0)) }},
		{"completed missing exit", func(u *protocol.RunnerJobUpdateRequest) { u.Status = "completed"; u.Finished = true }},
		{"completed nonzero", func(u *protocol.RunnerJobUpdateRequest) {
			u.Status = "completed"
			u.Finished = true
			u.ExitCode = jobPtr(int32(1))
		}},
		{"snapshot chunk", func(u *protocol.RunnerJobUpdateRequest) { u.StdoutChunk = jobPtr("") }},
		{"snapshot tail", func(u *protocol.RunnerJobUpdateRequest) { u.StderrTail = jobPtr("") }},
		{"snapshot zero", func(u *protocol.RunnerJobUpdateRequest) { u.LogSnapshot.Stdout.FirstRetainedLine = 0 }},
		{"snapshot range", func(u *protocol.RunnerJobUpdateRequest) { u.LogSnapshot.Stdout.NextLine = 3 }},
		{"snapshot truncation", func(u *protocol.RunnerJobUpdateRequest) {
			u.LogSnapshot.Stdout.FirstRetainedLine = 2
			u.LogSnapshot.Stdout.NextLine = 2
		}},
		{"snapshot oversized", func(u *protocol.RunnerJobUpdateRequest) {
			u.LogSnapshot.Stdout.Tail = strings.Repeat("a", 65537)
			u.LogSnapshot.Stdout.NextLine = 2
		}},
		{"snapshot invalid UTF8", func(u *protocol.RunnerJobUpdateRequest) {
			u.LogSnapshot.Stdout.Tail = string([]byte{255})
			u.LogSnapshot.Stdout.NextLine = 2
		}},
		{"wrong request", func(u *protocol.RunnerJobUpdateRequest) { u.RequestID = jobPtr("request-other") }},
		{"wrong instance", func(u *protocol.RunnerJobUpdateRequest) { u.AgentInstanceID = "other" }},
		{"wrong client", func(u *protocol.RunnerJobUpdateRequest) { u.ClientID = "other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r, _ := reconciliationRegistry(t)
			s := reconciliationStart(t, r, "a")
			u := sequencedUpdate("a", "running", 1)
			u.LogSnapshot = &protocol.ShellJobLogSnapshot{Stdout: s.Stdout, Stderr: s.Stderr}
			test.mutate(&u)
			before, _ := copyJSON(r.jobs[s.JobID].info)
			seen := r.nodes["node-a"].lastSeen
			if _, err := r.UpdateJob(jobPrincipal(), u); err == nil {
				t.Fatal("accepted invalid update")
			}
			if !reflect.DeepEqual(before, r.jobs[s.JobID].info) || seen != r.nodes["node-a"].lastSeen {
				t.Fatal("rejection mutated job or liveness")
			}
		})
	}
}
func TestReconciliationBusinessViolationsLatchFailure(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*protocol.RunnerJobUpdateRequest)
	}{
		{"validation progress", func(u *protocol.RunnerJobUpdateRequest) {
			u.ValidationProgress = &protocol.ShellJobValidationProgress{}
		}},
		{"active command state", func(u *protocol.RunnerJobUpdateRequest) { u.CommandExecutionState = jobPtr(protocol.CommandCompleted) }},
		{"wrong activity owner", func(u *protocol.RunnerJobUpdateRequest) {
			u.Activity = &protocol.ShellJobActivity{State: protocol.ActivityWorking, Phase: protocol.ActivityCargoCompiling, Source: protocol.ActivityCargoOutput}
		}},
		{"noncanonical activity", func(u *protocol.RunnerJobUpdateRequest) {
			u.Activity = &protocol.ShellJobActivity{State: protocol.ActivityWaiting, Phase: protocol.ActivityProcessRunning, Source: protocol.ActivityRunnerExecution}
		}},
		{"missing terminal state", func(u *protocol.RunnerJobUpdateRequest) {
			u.Status = "completed"
			u.Finished = true
			u.ExitCode = jobPtr(int32(0))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, _ := reconciliationRegistry(t)
			reconciliationStart(t, r, "a")
			u := sequencedUpdate("a", "running", 1)
			test.mutate(&u)
			got := jobApply(t, r, u)
			if got.Status != "failed" || got.Error == nil || !strings.Contains(*got.Error, "executor protocol violation") {
				t.Fatalf("violation: %+v", got)
			}
		})
	}
}
func TestReconciliationInventoryAtomicFailures(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*protocol.ShellJobSnapshot)
	}{
		{"unknown", func(s *protocol.ShellJobSnapshot) { s.JobID = "unknown"; s.RequestID = "unknown-request" }},
		{"request binding", func(s *protocol.ShellJobSnapshot) { s.RequestID = "another-request" }},
		{"context", func(s *protocol.ShellJobSnapshot) { s.Context.Cwd = jobPtr("elsewhere") }},
		{"created", func(s *protocol.ShellJobSnapshot) { s.CreatedAt = 0 }},
		{"started", func(s *protocol.ShellJobSnapshot) { s.StartedAt = jobPtr(s.CreatedAt - 1) }},
		{"running missing started", func(s *protocol.ShellJobSnapshot) { s.StartedAt = nil }},
		{"active ended", func(s *protocol.ShellJobSnapshot) { s.EndedAt = jobPtr(s.CreatedAt) }},
		{"terminal no ended", func(s *protocol.ShellJobSnapshot) {
			s.Status = "failed"
			s.CommandExecutionState = jobPtr(protocol.CommandCompleted)
		}},
		{"terminal before started", func(s *protocol.ShellJobSnapshot) {
			s.Status = "failed"
			s.EndedAt = jobPtr(s.CreatedAt - 1)
			s.CommandExecutionState = jobPtr(protocol.CommandCompleted)
		}},
		{"seq zero", func(s *protocol.ShellJobSnapshot) { s.UpdateSeq = 0 }},
		{"activity", func(s *protocol.ShellJobSnapshot) {
			s.Activity = &protocol.ShellJobActivity{State: protocol.ActivityWorking, Source: protocol.ActivityCargoOutput, Phase: protocol.ActivityCargoCompiling}
		}},
		{"progress", func(s *protocol.ShellJobSnapshot) { s.ValidationProgress = &protocol.ShellJobValidationProgress{} }},
		{"identity", func(s *protocol.ShellJobSnapshot) {
			s.Context.StructuredExecution.ValidationIdentity = jobPtr("target:invalid")
		}},
		{"assertion", func(s *protocol.ShellJobSnapshot) { s.Context.StructuredExecution.AssertionName = jobPtr("name") }},
		{"tool", func(s *protocol.ShellJobSnapshot) {
			s.Context.StructuredExecution.ValidationTool = jobPtr("cargo_test")
		}},
		{"arg count", func(s *protocol.ShellJobSnapshot) { s.Context.StructuredExecution.ArgCount = 257 }},
		{"script bytes", func(s *protocol.ShellJobSnapshot) { s.Context.StructuredExecution.ScriptBytes = jobPtr(uint64(1)) }},
		{"detached", func(s *protocol.ShellJobSnapshot) {
			s.Context.StructuredExecution.ExecutionSource = "run_detached_process"
		}},
		{"workflow", func(s *protocol.ShellJobSnapshot) { s.Context.WorkflowSessionID = jobPtr("session") }},
		{"project owner", func(s *protocol.ShellJobSnapshot) { s.Context.RuntimeProjectID = jobPtr("agent:other:project") }},
		{"preview", func(s *protocol.ShellJobSnapshot) { s.Context.CommandPreview = " noncanonical " }},
		{"nul context", func(s *protocol.ShellJobSnapshot) { s.Context.Cwd = jobPtr("bad\x00") }},
		{"error limit", func(s *protocol.ShellJobSnapshot) { s.Error = jobPtr(strings.Repeat("x", 4097)) }},
		{"purpose", func(s *protocol.ShellJobSnapshot) { s.Context.Purpose = jobPtr("invalid") }},
		{"shell", func(s *protocol.ShellJobSnapshot) { s.Context.Shell = jobPtr("invalid") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r, reg := reconciliationRegistry(t)
			first := reconciliationStart(t, r, "a")
			bad := reconciliationStart(t, r, "b")
			test.mutate(&bad)
			if err := r.Offline(jobPrincipal(), protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); err != nil {
				t.Fatal(err)
			}
			before, _ := copyJSON(r.jobs[first.JobID].info)
			seen := r.nodes["node-a"].lastSeen
			since := *r.jobs[first.JobID].recoveringSince
			n := r.nodes["node-a"]
			setInventory(t, &reg, []protocol.ShellJobSnapshot{first, bad})
			reg.DisplayName = jobPtr("changed")
			if _, err := r.Register(jobPrincipal(), reg); err == nil {
				t.Fatal("accepted bad inventory")
			}
			if r.nodes["node-a"] != n || !reflect.DeepEqual(before, r.jobs[first.JobID].info) || seen != n.lastSeen || since != *r.jobs[first.JobID].recoveringSince || len(r.pending) != 0 {
				t.Fatal("inventory partially committed")
			}
		})
	}
}
func TestReconciliationStaleSequenceAndCursor(t *testing.T) {
	r, reg := reconciliationRegistry(t)
	s := reconciliationStart(t, r, "a")
	u := sequencedUpdate("a", "running", uint64(1)<<63)
	u.StdoutChunk = jobPtr("one\ntwo\n")
	jobApply(t, r, u)
	u = sequencedUpdate("a", "running", 1)
	u.StdoutChunk = jobPtr("stale\n")
	jobApply(t, r, u)
	if r.jobs[s.JobID].stdout.tail != "one\ntwo\n" {
		t.Fatal("stale update appended")
	}
	s.UpdateSeq = uint64(1)<<63 + 1
	setInventory(t, &reg, []protocol.ShellJobSnapshot{s})
	if _, err := r.Register(jobPrincipal(), reg); err == nil {
		t.Fatal("cursor regression accepted")
	}
	s.UpdateSeq = 1
	setInventory(t, &reg, []protocol.ShellJobSnapshot{s})
	if _, err := r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	u = sequencedUpdate("a", "running", ^uint64(0))
	u.LogSnapshot = &protocol.ShellJobLogSnapshot{Stdout: s.Stdout, Stderr: s.Stderr}
	if _, err := r.UpdateJob(jobPrincipal(), u); err == nil {
		t.Fatal("update cursor regression accepted")
	}
	u.LogSnapshot.Stdout = protocol.ShellJobStreamSnapshot{Tail: "last", FirstRetainedLine: ^uint64(0), NextLine: ^uint64(0), Truncated: true}
	jobApply(t, r, u)
	jobApply(t, r, u)
	if *r.jobs[s.JobID].info.LastUpdateSeq != ^uint64(0) {
		t.Fatal("uint64 sequence lost")
	}
}
func TestReconciliationInventoryBudgetsAndOrdering(t *testing.T) {
	r, reg := reconciliationRegistry(t)
	base := reconciliationStart(t, r, "a")
	makeSnapshots := func(n int, terminal bool) []protocol.ShellJobSnapshot {
		items := make([]protocol.ShellJobSnapshot, n)
		for i := range items {
			items[i], _ = copyJSON(base)
			items[i].JobID = fmt.Sprintf("j-%d", i)
			items[i].RequestID = fmt.Sprintf("r-%d", i)
			if terminal {
				items[i].Status = "completed"
				items[i].EndedAt = jobPtr(base.CreatedAt)
				items[i].ExitCode = jobPtr(int32(0))
				items[i].CommandExecutionState = jobPtr(protocol.CommandCompleted)
			}
		}
		return items
	}
	for _, test := range []struct {
		name  string
		items []protocol.ShellJobSnapshot
		ok    bool
	}{
		{"64 active", makeSnapshots(64, false), true}, {"65 active", makeSnapshots(65, false), false}, {"64 terminal", makeSnapshots(64, true), true}, {"65 terminal", makeSnapshots(65, true), false}, {"129 total", makeSnapshots(129, false), false},
		{"duplicate job", []protocol.ShellJobSnapshot{base, base}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			setInventory(t, &reg, test.items)
			_, err := r.registrationInventory(reg)
			if (err == nil) != test.ok {
				t.Fatalf("budget %v", err)
			}
		})
	}
	items := makeSnapshots(2, false)
	items[1].RequestID = items[0].RequestID
	setInventory(t, &reg, items)
	if _, err := r.registrationInventory(reg); err == nil {
		t.Fatal("duplicate request")
	}
	items = makeSnapshots(2, true)
	items[1].Status = "running"
	items[1].EndedAt = nil
	items[1].ExitCode = nil
	items[1].CommandExecutionState = nil
	setInventory(t, &reg, items)
	if _, err := r.registrationInventory(reg); err == nil {
		t.Fatal("terminal before active")
	}
	items = makeSnapshots(16, false)
	for i := range items {
		items[i].Stdout.Tail = strings.Repeat("x", 65536)
		items[i].Stdout.NextLine = 2
	}
	setInventory(t, &reg, items)
	if _, err := r.registrationInventory(reg); err == nil {
		t.Fatal("full object budget ignored")
	}
	items = makeSnapshots(3, false)
	for i := range items {
		items[i].Stdout.Tail = strings.Repeat("<", 60000)
		items[i].Stdout.NextLine = 2
	}
	setInventory(t, &reg, items)
	if _, err := r.registrationInventory(reg); err != nil {
		t.Fatalf("Go HTML encoding changed source byte limit: %v", err)
	}
	for _, str := range []string{"<>&\u2028\u2029", `literal\u003c`, "line\n\t\x00"} {
		b, _ := json.Marshal(str)
		want := len(b)
		if str == "<>&\u2028\u2029" {
			want = 11
		}
		if got := rustJSONSerializedBytes(b); got != want {
			t.Fatalf("source JSON count %q got %d want %d", str, got, want)
		}
	}
}
func TestReconciliationRegisterUpdateRace(t *testing.T) {
	r, reg := reconciliationRegistry(t)
	s := reconciliationStart(t, r, "a")
	setInventory(t, &reg, []protocol.ShellJobSnapshot{s})
	u := sequencedUpdate("a", "completed", 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := r.Register(jobPrincipal(), reg); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := r.UpdateJob(jobPrincipal(), u); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := getJob(t, r, "a"); got.Status != "completed" || *got.LastUpdateSeq != 20 {
		t.Fatalf("race rolled back: %+v", got)
	}
}
func TestReconciliationInstanceOwnerAndDowngrade(t *testing.T) {
	r, reg := reconciliationRegistry(t)
	s := reconciliationStart(t, r, "a")
	n := r.nodes["node-a"]
	changed, _ := copyJSON(reg)
	changed.AgentInstanceID = "process-b"
	setInventory(t, &changed, []protocol.ShellJobSnapshot{s})
	if _, err := r.Register(jobPrincipal(), changed); err == nil || r.nodes["node-a"] != n {
		t.Fatal("cross-instance inventory takeover")
	}
	bad := jobPrincipal()
	bad.Username = "mallory"
	if _, err := r.Register(bad, reg); err == nil {
		t.Fatal("owner takeover")
	}
	group := jobPrincipal()
	group.ProjectGrantID = "other"
	if _, err := r.Register(group, reg); err == nil {
		t.Fatal("group takeover")
	}
	changed, _ = copyJSON(reg)
	changed.Capabilities.JobStateReconciliation = false
	changed.JobInventory = nil
	if _, err := r.Register(jobPrincipal(), changed); err == nil {
		t.Fatal("same instance capability downgrade")
	}
	changed.AgentInstanceID = "process-b"
	if _, err := r.Register(jobPrincipal(), changed); err != nil {
		t.Fatal(err)
	}
	if got := getJob(t, r, "a"); got.Status != "lost" {
		t.Fatal("replacement failed to terminalize")
	}
	if _, err := r.UpdateJob(jobPrincipal(), sequencedUpdate("a", "running", 1)); err == nil {
		t.Fatal("old instance revived")
	}
}
func TestReconciliationRecoveryStopAndDeadline(t *testing.T) {
	r, reg := reconciliationRegistry(t)
	s := reconciliationStart(t, r, "a")
	if _, err := r.StopJob(Access{Username: "alice"}, s.JobID, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := r.Offline(jobPrincipal(), protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.StopJob(Access{Username: "alice"}, s.JobID, "alice"); err == nil {
		t.Fatal("stop accepted during recovery")
	}
	setInventory(t, &reg, []protocol.ShellJobSnapshot{s})
	if _, err := r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	if _, err := r.StopJob(Access{Username: "alice"}, s.JobID, "alice"); err != nil {
		t.Fatal(err)
	}
	if len(r.pending) != 1 {
		t.Fatal("dropped stop cannot be retried after authoritative running")
	}
	if err := r.Offline(jobPrincipal(), protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.jobs[s.JobID].recoveringSince = jobPtr(time.Now().Add(-2 * time.Minute))
	r.mu.Unlock()
	if _, err := r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	if got := getJob(t, r, "a"); got.Status != "lost" || *got.RecoveryReasonCode != "runner_recovery_deadline_exceeded" {
		t.Fatalf("deadline: %+v", got)
	}
}
func TestReconciliationTerminalRetentionObservedLocally(t *testing.T) {
	r, reg := reconciliationRegistry(t)
	s := reconciliationStart(t, r, "a")
	s.CreatedAt = 1
	s.StartedAt = jobPtr(int64(1))
	s.EndedAt = jobPtr(int64(2))
	s.Status = "completed"
	s.CommandExecutionState = jobPtr(protocol.CommandCompleted)
	s.ExitCode = jobPtr(int32(0))
	setInventory(t, &reg, []protocol.ShellJobSnapshot{s})
	if _, err := r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	if got := getJob(t, r, "a"); got.Status != "completed" || got.CreatedAt == 1 {
		t.Fatal("original creation or local TTL lost")
	}
	r.mu.Lock()
	r.jobs[s.JobID].observedTerminal = jobPtr(time.Now().Add(-jobTerminalRetention))
	r.mu.Unlock()
	if _, err := r.GetJob(Access{Username: "alice"}, s.JobID); err == nil {
		t.Fatal("terminal did not expire")
	}
	if _, err := r.Register(jobPrincipal(), reg); err == nil {
		t.Fatal("unverified expired inventory resurrected")
	}
}
