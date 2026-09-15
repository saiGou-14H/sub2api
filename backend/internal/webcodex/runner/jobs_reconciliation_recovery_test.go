// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func TestReconciliationDeadlineTimerAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := reconciliationRegistry(t)
		s := reconciliationStart(t, r, "a")
		if err := r.Offline(jobPrincipal(), protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); err != nil {
			t.Fatal(err)
		}
		r.mu.Lock()
		since := *r.jobs[s.JobID].recoveringSince
		r.mu.Unlock()
		time.Sleep(30 * time.Second)
		if err := r.Offline(jobPrincipal(), protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); err != nil {
			t.Fatal(err)
		}
		r.mu.Lock()
		sameDeadline := since == *r.jobs[s.JobID].recoveringSince
		r.mu.Unlock()
		if !sameDeadline {
			t.Fatal("repeated offline extended deadline")
		}
		time.Sleep(31 * time.Second)
		synctest.Wait()
		// Read the record directly: no request-triggered sweep can satisfy this test.
		r.mu.Lock()
		j := r.jobs[s.JobID]
		status := j.info.Status
		timer := j.recoveryTimer
		r.mu.Unlock()
		if status != "lost" || timer != nil {
			t.Fatal("deadline did not independently finalize Job")
		}
	})
	synctest.Test(t, func(t *testing.T) {
		r, _ := reconciliationRegistry(t)
		s := reconciliationStart(t, r, "a")
		if err := r.Offline(jobPrincipal(), protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); err != nil {
			t.Fatal(err)
		}
		j := r.jobs[s.JobID]
		r.Close()
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if j.recoveryTimer != nil || j.info.Status == "lost" {
			t.Fatal("Close leaked timer mutation")
		}
	})
}
func TestReconciliationSameSequenceRecoveryAndStaleObservation(t *testing.T) {
	r, reg := reconciliationRegistry(t)
	s := reconciliationStart(t, r, "a")
	setInventory(t, &reg, []protocol.ShellJobSnapshot{s})
	if _, err := r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.nodes["node-a"].lastSeen = time.Now().Add(-2 * time.Minute)
	r.mu.Unlock()
	if got := getJob(t, r, "a"); got.RecoveryState == nil || *got.RecoveryState != "recovering" {
		t.Fatal("stale not recoverable")
	}
	s.Stdout = protocol.ShellJobStreamSnapshot{Tail: "recovery\n", FirstRetainedLine: 1, NextLine: 2}
	setInventory(t, &reg, []protocol.ShellJobSnapshot{s})
	if _, err := r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	j := r.jobs[s.JobID]
	if j.recoveringSince != nil || j.recoveryTimer != nil || j.stdout.tail != "recovery\n" {
		t.Fatal("same seq recovery not applied")
	}
}
func TestReconciliationPublicStatusAcrossObservations(t *testing.T) {
	for _, restore := range []string{"inventory", "snapshot"} {
		t.Run(restore, func(t *testing.T) {
			r, reg := reconciliationRegistry(t)
			s := reconciliationStart(t, r, "a")
			setInventory(t, &reg, []protocol.ShellJobSnapshot{s})
			if _, err := r.Register(jobPrincipal(), reg); err != nil {
				t.Fatal(err)
			}
			access := Access{Username: "alice"}
			assertStatus := func(want, internal string) {
				t.Helper()
				got, err := r.GetJob(access, s.JobID)
				if err != nil || got.Status != want {
					t.Fatalf("GetJob status=%q, err=%v; want %q", got.Status, err, want)
				}
				list, err := r.ListJobs(access, nil, nil, nil)
				if err != nil || len(list) != 1 || list[0].Status != want {
					t.Fatalf("ListJobs=%+v, err=%v; want status %q", list, err, want)
				}
				for _, filter := range []string{"running", "recovering", "completed"} {
					filtered, err := r.ListJobs(access, nil, &filter, nil)
					count := 0
					if filter == want {
						count = 1
					}
					if err != nil || len(filtered) != count {
						t.Fatalf("ListJobs filter=%q count=%d, err=%v; want %d", filter, len(filtered), err, count)
					}
				}
				log, err := r.JobLog(access, protocol.RunnerJobLogRequest{JobID: s.JobID})
				if err != nil || log.Job == nil || log.Job.Status != want {
					t.Fatalf("JobLog job=%+v, err=%v; want status %q", log.Job, err, want)
				}
				r.mu.Lock()
				status := r.jobs[s.JobID].info.Status
				r.mu.Unlock()
				if status != internal {
					t.Fatalf("observation changed internal status to %q; want %q", status, internal)
				}
			}
			assertStatus("running", "running")
			if err := r.Offline(jobPrincipal(), protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); err != nil {
				t.Fatal(err)
			}
			assertStatus("recovering", "running")
			if restore == "inventory" {
				if _, err := r.Register(jobPrincipal(), reg); err != nil {
					t.Fatal(err)
				}
			} else {
				u := sequencedUpdate("a", "running", 2)
				u.LogSnapshot = &protocol.ShellJobLogSnapshot{Stdout: s.Stdout, Stderr: s.Stderr}
				jobApply(t, r, u)
			}
			assertStatus("running", "running")
			jobApply(t, r, sequencedUpdate("a", "completed", 3))
			// Retained recovery provenance cannot override an authoritative terminal lifecycle.
			r.mu.Lock()
			r.jobs[s.JobID].info.RecoveryState = jobPtr("recovering")
			r.mu.Unlock()
			assertStatus("completed", "completed")
		})
	}
}

func TestReconciliationAdmissionGatesAndPendingAtomicity(t *testing.T) {
	r, reg := reconciliationRegistry(t)
	in := jobInvocation("a")
	if _, err := r.StartProcessJob(Access{Username: "alice"}, in); err == nil {
		t.Fatal("unrecoverable workflow context dispatched")
	}
	if len(r.jobs) != 0 || len(r.pending) != 0 {
		t.Fatal("bad dispatch mutated registry")
	}
	in.Operation = protocol.JobProcessOperation{JobID: "job-a", Process: protocol.ShellProcessArgv{Executable: "printf", Args: []string{}}, TimeoutSecs: 30, Context: protocol.ShellJobContext{StructuredExecution: &protocol.ShellJobStructuredExecutionMetadata{ExecutionSource: "run_process"}}}
	if _, err := r.StartProcessJob(Access{Username: "alice"}, in); err != nil {
		t.Fatal(err)
	}
	before := r.nodes["node-a"]
	invalid, _ := copyJSON(reg)
	invalid.JobInventory = []byte(`{"active_complete":false,"jobs":[]}`)
	if _, err := r.Register(jobPrincipal(), invalid); err == nil {
		t.Fatal("incomplete accepted")
	}
	if r.nodes["node-a"] != before || len(before.queue) != 1 || len(r.pending) != 1 {
		t.Fatal("failed registration changed pending counts")
	}
	if _, err := r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	if r.jobs["job-a"].info.Status != "queued" || len(r.pending) != 1 {
		t.Fatal("complete inventory lost unpolled work")
	}
}
