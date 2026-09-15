// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func jobsRegistry(t *testing.T, maximum int) *Registry {
	t.Helper()
	r, err := NewRegistry(Options{MaxRunners: 8, MaxPendingPerRunner: 128, MaxJobsPerRunner: maximum, OnlineWindow: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	register(t, r, "node-a", "process-a")
	return r
}
func jobPrincipal() Principal {
	p := testPrincipal("node-a")
	p.Scopes = append(p.Scopes, ScopeJobUpdate)
	return p
}
func jobInvocation(id string) protocol.JobInvocation {
	process := protocol.JobProcessOperation{JobID: "job-" + id, Process: protocol.ShellProcessArgv{Executable: "printf", Args: []string{"hello", "two words"}}, TimeoutSecs: 30, Context: protocol.ShellJobContext{CommandPreview: "caller provided", RuntimeProjectID: jobPtr("project"), WorkflowSessionID: jobPtr("session")}}
	expected := process.ExpectedStructuredExecution()
	process.Context.StructuredExecution = &expected
	return protocol.JobInvocation{Metadata: protocol.InvocationMetadata{ClientID: "node-a", RequestID: "request-" + id, RequestedBy: "alice", CreatedAt: 1}, Operation: process}
}
func startJob(t *testing.T, r *Registry, id string) protocol.ShellJobInfo {
	t.Helper()
	v, err := r.StartProcessJob(Access{Username: "alice"}, jobInvocation(id))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func jobUpdate(id, status string) protocol.RunnerJobUpdateRequest {
	return protocol.RunnerJobUpdateRequest{ClientID: "node-a", AgentInstanceID: "process-a", JobID: "job-" + id, RequestID: jobPtr("request-" + id), Status: status}
}
func getJob(t *testing.T, r *Registry, id string) protocol.ShellJobInfo {
	t.Helper()
	v, err := r.GetJob(Access{Username: "alice"}, "job-"+id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func jobPoll(t *testing.T, r *Registry) *protocol.RunnerRequest {
	t.Helper()
	v, err := poll(r, "node-a", "process-a")
	if err != nil || v == nil {
		t.Fatalf("poll: %v, %v", v, err)
	}
	return v
}
func jobApply(t *testing.T, r *Registry, u protocol.RunnerJobUpdateRequest) protocol.ShellJobInfo {
	t.Helper()
	v, err := r.UpdateJob(jobPrincipal(), u)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func jobLog(t *testing.T, r *Registry, id string, out, errCursor *uint64) protocol.RunnerJobLogResponse {
	t.Helper()
	v, err := r.JobLog(Access{Username: "alice"}, protocol.RunnerJobLogRequest{JobID: "job-" + id, SinceStdoutLine: out, SinceStderrLine: errCursor})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestJobsLegacyProcessLifecycleAndOwnership(t *testing.T) {
	r := jobsRegistry(t, 8)
	in := jobInvocation("a")
	v, err := r.StartProcessJob(Access{Username: "alice"}, in)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "queued" || v.StartedAt != nil || v.CommandPreview != `printf hello "two words"` || v.CreatedAt == 1 {
		t.Fatalf("new Job: %+v", v)
	}
	// Caller and returned DTOs cannot mutate queue or registry state.
	op := in.Operation.(protocol.JobProcessOperation)
	op.Process.Args[0] = "changed"
	*v.StructuredExecution = protocol.ShellJobStructuredExecutionMetadata{}
	p := jobPoll(t, r)
	if p.Kind != "start_process_job" || p.Process.Args[0] != "hello" || p.Command != "" {
		t.Fatalf("wire: %+v", p)
	}
	if r.pending[p.RequestID] != nil || r.requestToJob[p.RequestID] != "job-a" {
		t.Fatal("Job acquired synchronous waiter")
	}
	if !errors.Is(r.Complete(testPrincipal("node-a"), result("node-a", "process-a", p.RequestID)), protocol.ErrUnsupported) {
		t.Fatal("Job admitted synchronous result")
	}
	v = getJob(t, r, "a")
	if v.Status != "agent_queued" || v.StartedAt != nil {
		t.Fatalf("dispatch claimed spawn: %+v", v)
	}
	u := jobUpdate("a", "running")
	u.StdoutChunk = jobPtr("one\ntwo\n")
	u.StderrChunk = jobPtr("warning\n")
	u.Activity = &protocol.ShellJobActivity{State: protocol.ActivityWorking, Phase: protocol.ActivityProcessRunning, Source: protocol.ActivityRunnerExecution}
	u.UpdateSeq = jobPtr(uint64(9))
	v = jobApply(t, r, u)
	if v.StartedAt == nil || v.Activity == nil || *v.LastUpdateSeq != 9 {
		t.Fatalf("running: %+v", v)
	}
	// Legacy optional sequences are annotations, not deduplication authority.
	u.UpdateSeq = jobPtr(uint64(2))
	u.StdoutChunk = jobPtr("three\n")
	u.StderrChunk = nil
	u.Activity = nil
	v = jobApply(t, r, u)
	if *v.LastUpdateSeq != 2 || v.Activity == nil {
		t.Fatal("legacy sequence/activity changed semantics")
	}
	logs := jobLog(t, r, "a", jobPtr(uint64(2)), jobPtr(uint64(1)))
	if *logs.StdoutTail != "two\nthree\n" || *logs.StderrTail != "warning\n" || *logs.NextStdoutLine != 4 || *logs.NextStderrLine != 2 {
		t.Fatalf("cursor logs: %+v", logs)
	}
	logs = jobLog(t, r, "a", logs.NextStdoutLine, logs.NextStderrLine)
	if *logs.StdoutTail != "" || *logs.StderrTail != "" {
		t.Fatal("cursor repeated logs")
	}
	u = jobUpdate("a", "completed")
	u.Finished = true
	u.ExitCode = jobPtr(int32(0))
	u.DurationMS = jobPtr(uint64(2200))
	u.CommandExecutionState = jobPtr(protocol.CommandCompleted)
	terminal := jobApply(t, r, u)
	if terminal.Activity != nil || terminal.Result == nil || terminal.EndedAt == nil || *terminal.ElapsedSecs != 2 {
		t.Fatalf("terminal: %+v", terminal)
	}
	if len(r.pending) != 0 || len(r.requestToJob) != 0 {
		t.Fatal("terminal mappings retained")
	}
	before := jobLog(t, r, "a", nil, nil)
	u.Status = "running"
	u.StdoutChunk = jobPtr("forged\n")
	u.UpdateSeq = jobPtr(uint64(100))
	v = jobApply(t, r, u)
	after := jobLog(t, r, "a", nil, nil)
	if !reflect.DeepEqual(terminal, v) || !reflect.DeepEqual(before, after) {
		t.Fatal("first terminal latch mutated")
	}
}

func TestJobsAuthorizationAndAtomicRejections(t *testing.T) {
	r := jobsRegistry(t, 8)
	startJob(t, r, "a")
	if _, err := r.UpdateJob(jobPrincipal(), jobUpdate("a", "running")); err == nil {
		t.Fatal("unpolled update admitted")
	}
	jobPoll(t, r)
	for _, test := range []struct {
		name   string
		mutate func(*Principal, *protocol.RunnerJobUpdateRequest)
	}{
		{"scope", func(p *Principal, _ *protocol.RunnerJobUpdateRequest) { p.Scopes = []string{ScopePoll} }},
		{"owner", func(p *Principal, _ *protocol.RunnerJobUpdateRequest) { p.Username = "bob" }},
		{"client binding", func(p *Principal, _ *protocol.RunnerJobUpdateRequest) { p.AllowedClientID = "node-b" }},
		{"stale", func(_ *Principal, u *protocol.RunnerJobUpdateRequest) { u.AgentInstanceID = "process-old" }},
		{"request", func(_ *Principal, u *protocol.RunnerJobUpdateRequest) { u.RequestID = jobPtr("different") }},
		{"unknown", func(_ *Principal, u *protocol.RunnerJobUpdateRequest) { u.JobID = "job-other" }},
		{"snapshot", func(_ *Principal, u *protocol.RunnerJobUpdateRequest) {
			u.LogSnapshot = &protocol.ShellJobLogSnapshot{}
		}},
		{"status", func(_ *Principal, u *protocol.RunnerJobUpdateRequest) { u.Status = "recovering" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := getJob(t, r, "a")
			seen := r.nodes["node-a"].lastSeen
			p := jobPrincipal()
			u := jobUpdate("a", "running")
			test.mutate(&p, &u)
			if _, err := r.UpdateJob(p, u); err == nil {
				t.Fatal("invalid update admitted")
			}
			if !reflect.DeepEqual(before, getJob(t, r, "a")) || seen != r.nodes["node-a"].lastSeen {
				t.Fatal("rejection mutated state/liveness")
			}
		})
	}
	for _, a := range []Access{{Username: "bob"}, {GroupKind: "shared_key", GroupID: strings.Repeat("a", 64)}, {}} {
		if _, err := r.GetJob(a, "job-a"); !errors.Is(err, ErrUnknownJob) {
			t.Fatal("foreign view", err)
		}
		if _, err := r.JobLog(a, protocol.RunnerJobLogRequest{JobID: "job-a"}); !errors.Is(err, ErrUnknownJob) {
			t.Fatal("foreign log", err)
		}
		values, err := r.ListJobs(a, nil, nil, nil)
		if err != nil || len(values) != 0 {
			t.Fatal("foreign list", values, err)
		}
	}
	observer := Access{GlobalVisibility: true, Username: "bob"}
	if _, err := r.GetJob(observer, "job-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.StopJob(observer, "job-a", "bob"); !errors.Is(err, ErrForbidden) {
		t.Fatal("observation became stop authority", err)
	}
	if _, err := r.StartProcessJob(observer, jobInvocation("b")); !errors.Is(err, ErrForbidden) {
		t.Fatal("observation became execution authority", err)
	}
	// request_id omission is original optional wire semantics, with exact job binding.
	u := jobUpdate("a", " running ")
	u.RequestID = nil
	v := jobApply(t, r, u)
	if v.Status != "running" {
		t.Fatal(v.Status)
	}
}

func TestJobsProtocolViolationsAreFailedNotSuccessful(t *testing.T) {
	for _, test := range []struct {
		name, status string
		state        *protocol.ShellCommandExecutionState
		activity     *protocol.ShellJobActivity
		progress     *protocol.ShellJobValidationProgress
		code         string
	}{
		{name: "missing lifecycle", status: "completed", code: "structured_job_lifecycle_missing"},
		{name: "active lifecycle", status: "running", state: jobPtr(protocol.CommandCompleted), code: "command_execution_state_on_active_job"},
		{name: "invalid lifecycle", status: "completed", state: jobPtr(protocol.CommandTimedOut), code: "structured_job_lifecycle_invalid"},
		{name: "invalid activity", status: "running", activity: &protocol.ShellJobActivity{State: protocol.ActivityWaiting, Phase: protocol.ActivityProcessRunning, Source: protocol.ActivityRunnerExecution}, code: "job_activity_invalid"},
		{name: "validation", status: "running", progress: &protocol.ShellJobValidationProgress{}, code: "validation_progress_unexpected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := jobsRegistry(t, 2)
			startJob(t, r, "a")
			jobPoll(t, r)
			u := jobUpdate("a", test.status)
			u.CommandExecutionState = test.state
			u.Activity = test.activity
			u.ValidationProgress = test.progress
			u.StdoutChunk = jobPtr("must not apply\n")
			v := jobApply(t, r, u)
			if v.Status != "failed" || v.Error == nil || !strings.Contains(*v.Error, test.code) || v.CommandExecutionState == nil || *v.CommandExecutionState != protocol.CommandNotStarted {
				t.Fatalf("violation: %+v", v)
			}
			if *jobLog(t, r, "a", nil, nil).StdoutTail != "" || len(r.pending) != 0 {
				t.Fatal("violation retained mutable output/pending")
			}
		})
	}
}

func TestJobsLegacyFinishedAndTerminalStateMatrix(t *testing.T) {
	for _, test := range []struct {
		status  string
		command protocol.ShellCommandExecutionState
	}{
		{"completed", protocol.CommandCompleted}, {"failed", protocol.CommandOutcomeUnknown}, {"stopped", protocol.CommandNotStarted}, {"cancelled", protocol.CommandNotStarted}, {"lost", protocol.CommandOutcomeUnknown}, {"timeout", protocol.CommandTimedOut}, {"timed_out", protocol.CommandTimedOut},
	} {
		t.Run(test.status, func(t *testing.T) {
			r := jobsRegistry(t, 2)
			startJob(t, r, "a")
			jobPoll(t, r)
			u := jobUpdate("a", test.status)
			u.CommandExecutionState = &test.command
			u.ExitCode = jobPtr(int32(17))
			v := jobApply(t, r, u)
			if v.Status != test.status || v.Error != nil || *v.ExitCode != 17 {
				t.Fatalf("legacy terminal was incorrectly sequenced: %+v", v)
			}
		})
	}
	r := jobsRegistry(t, 2)
	startJob(t, r, "a")
	jobPoll(t, r)
	u := jobUpdate("a", "running")
	u.Finished = true
	u.ExitCode = jobPtr(int32(0))
	v := jobApply(t, r, u)
	if v.Status != "failed" || v.ExitCode != nil || v.CommandExecutionState != nil {
		t.Fatalf("legacy finished fallback: %+v", v)
	}
}

func TestJobsStopRegistrationNeverWaitsOrClaimsRemoteTermination(t *testing.T) {
	r := jobsRegistry(t, 5)
	startJob(t, r, "before")
	v, err := r.StopJob(Access{Username: "alice"}, "job-before", "alice")
	if err != nil || v.Status != "stopped" || *v.CommandExecutionState != protocol.CommandNotStarted {
		t.Fatal(v, err)
	}
	if p, err := poll(r, "node-a", "process-a"); err != nil || p != nil {
		t.Fatal("cancelled queue delivery", p, err)
	}
	startJob(t, r, "active")
	jobPoll(t, r)
	for i := 0; i < 3; i++ {
		v, err = r.StopJob(Access{Username: "alice"}, "job-active", "alice")
		if err != nil || v.Status != "stop_requested" || v.EndedAt != nil {
			t.Fatal(v, err)
		}
	}
	if len(r.nodes["node-a"].queue) != 1 {
		t.Fatal("duplicate stop queued")
	}
	p := jobPoll(t, r)
	if p.Kind != "stop_job" || p.JobID == nil || *p.JobID != "job-active" || r.pending[p.RequestID] != nil {
		t.Fatal("stop wire/control retained", p)
	}
	if p, err := poll(r, "node-a", "process-a"); err != nil || p != nil {
		t.Fatal("stop replay", p, err)
	}
	u := jobUpdate("active", "stopped")
	u.CommandExecutionState = jobPtr(protocol.CommandNotStarted)
	jobApply(t, r, u)
	if len(r.pending) != 0 || len(r.requestToJob) != 0 {
		t.Fatal("stop final cleanup")
	}
	startJob(t, r, "race")
	jobPoll(t, r)
	_, err = r.StopJob(Access{Username: "alice"}, "job-race", "alice")
	if err != nil {
		t.Fatal(err)
	}
	u = jobUpdate("race", "completed")
	u.CommandExecutionState = jobPtr(protocol.CommandCompleted)
	u.ExitCode = jobPtr(int32(0))
	jobApply(t, r, u)
	if p, err := poll(r, "node-a", "process-a"); err != nil || p != nil {
		t.Fatal("terminal left stop control queued", p, err)
	}
}

func TestJobsRetentionCapacityLeasesAndClose(t *testing.T) {
	r := jobsRegistry(t, 1)
	startJob(t, r, "a")
	if _, err := r.StartProcessJob(Access{Username: "alice"}, jobInvocation("b")); !errors.Is(err, ErrCapacity) {
		t.Fatal("retention capacity", err)
	}
	_, err := r.StopJob(Access{Username: "alice"}, "job-a", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.StartProcessJob(Access{Username: "alice"}, jobInvocation("b")); !errors.Is(err, ErrCapacity) {
		t.Fatal("terminal excluded from capacity", err)
	}
	observed := r.jobs["job-a"].observedTerminal
	r.jobs["job-a"].info.EndedAt = jobPtr(int64(1))
	if _, err := r.GetJob(Access{Username: "alice"}, "job-a"); err != nil {
		t.Fatal("Runner timestamp controls retention")
	}
	r.jobs["job-a"].observedTerminal = jobPtr(observed.Add(-jobTerminalRetention))
	startJob(t, r, "b")
	if _, err := r.GetJob(Access{Username: "alice"}, "job-a"); !errors.Is(err, ErrUnknownJob) {
		t.Fatal("terminal not pruned")
	}
	jobPoll(t, r)
	jobApply(t, r, jobUpdate("b", "running"))
	r.nodes["node-a"].lastSeen = time.Now().Add(-2 * time.Minute)
	lost := getJob(t, r, "b")
	if lost.Status != "lost" || *lost.CommandExecutionState != protocol.CommandOutcomeUnknown || lost.Activity != nil {
		t.Fatal(lost)
	}
	register(t, r, "node-a", "process-a")
	late := jobApply(t, r, jobUpdate("b", "running"))
	if late.Status != "lost" {
		t.Fatal("lost revived")
	}
	r.Close()
	r.Close()
	if len(r.jobs) != 0 || len(r.requestToJob) != 0 || len(r.pending) != 0 || len(r.nodes) != 0 {
		t.Fatal("Close retained local records")
	}
	if _, err := r.GetJob(Access{Username: "alice"}, "job-b"); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestJobsReplacementOfflineAndConcurrentAccess(t *testing.T) {
	r := jobsRegistry(t, 16)
	startJob(t, r, "a")
	jobPoll(t, r)
	register(t, r, "node-a", "process-a")
	if getJob(t, r, "a").Status != "agent_queued" {
		t.Fatal("same instance invalidated job")
	}
	register(t, r, "node-a", "process-b")
	if getJob(t, r, "a").Status != "lost" {
		t.Fatal("replacement retained active job")
	}
	if _, err := r.UpdateJob(jobPrincipal(), jobUpdate("a", "running")); !errors.Is(err, ErrStale) {
		t.Fatal("old lease accepted", err)
	}
	// Fresh registry avoids mixing lease fixtures with the concurrent update test.
	concurrent := jobsRegistry(t, 2)
	startJob(t, concurrent, "c")
	jobPoll(t, concurrent)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := jobUpdate("c", "running")
			u.StdoutChunk = jobPtr(fmt.Sprint(i, "\n"))
			if _, err := concurrent.UpdateJob(jobPrincipal(), u); err != nil {
				t.Error(err)
			}
			_, err := concurrent.GetJob(Access{Username: "alice"}, "job-c")
			if err != nil {
				t.Error(err)
			}
			_, err = concurrent.ListJobs(Access{Username: "alice"}, nil, nil, nil)
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	logs := jobLog(t, concurrent, "c", nil, nil)
	if *logs.NextStdoutLine != 21 {
		t.Fatal("lost concurrent chunks", *logs.NextStdoutLine)
	}
	if err := concurrent.Offline(jobPrincipal(), protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); err != nil {
		t.Fatal(err)
	}
	if v := getJob(t, concurrent, "c"); v.Status != "lost" {
		t.Fatal(v)
	}
}

func TestJobsListFiltersOwnershipAndInputValidation(t *testing.T) {
	r := jobsRegistry(t, 128)
	for _, mutate := range []func(*protocol.JobInvocation){
		func(i *protocol.JobInvocation) { i.Metadata.RequestID = "bad/id" },
		func(i *protocol.JobInvocation) {
			p := i.Operation.(protocol.JobProcessOperation)
			p.Context.StructuredExecution = nil
			i.Operation = p
		},
		func(i *protocol.JobInvocation) {
			p := i.Operation.(protocol.JobProcessOperation)
			p.Context.ValidationSteps = []string{"check"}
			i.Operation = p
		},
		func(i *protocol.JobInvocation) {
			p := i.Operation.(protocol.JobProcessOperation)
			p.Process.Executable = ""
			i.Operation = p
		},
		func(i *protocol.JobInvocation) { i.Operation = protocol.JobStopOperation{JobID: "a"} },
	} {
		i := jobInvocation("invalid")
		mutate(&i)
		if _, err := r.StartProcessJob(Access{Username: "alice"}, i); err == nil {
			t.Fatal("invalid dispatch admitted")
		}
	}
	if len(r.jobs) != 0 || len(r.pending) != 0 {
		t.Fatal("invalid dispatch mutated state")
	}
	for i := 0; i < 105; i++ {
		startJob(t, r, fmt.Sprint(i))
	}
	for _, test := range []struct {
		limit *uint64
		want  int
	}{{nil, 20}, {jobPtr(uint64(0)), 1}, {jobPtr(uint64(999)), 100}} {
		jobs, err := r.ListJobs(Access{Username: "alice"}, nil, jobPtr("queued"), test.limit)
		if err != nil || len(jobs) != test.want {
			t.Fatalf("list: %d %v", len(jobs), err)
		}
	}
	jobs, err := r.ListJobs(Access{Username: "alice"}, jobPtr("other"), nil, nil)
	if err != nil || len(jobs) != 0 {
		t.Fatal(jobs, err)
	}
	// Outputs serialize as original Job DTOs, with no private owner/lease/argv/stdin.
	data, err := json.Marshal(getJob(t, r, "0"))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"agent_instance_id", "process-a", "owner", "requested_by", "args", "stdin\""} {
		if strings.Contains(string(data), private) {
			t.Fatalf("private %q in Job view", private)
		}
	}
}
