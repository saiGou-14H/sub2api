// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// runner-registry/{job_updates,jobs,state,reconciliation}.rs. Legacy process Jobs only.
package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
	"github.com/google/uuid"
)

// Original protocol retention, measured from this Server's first terminal observation.
const jobTerminalRetention = 15 * time.Minute

var ErrUnknownJob = errors.New("unknown shell job")

type jobRecord struct {
	info             protocol.ShellJobInfo
	dispatched       bool
	stopRegistered   bool
	instanceID       string
	owner            string
	groupKind        string
	groupID          string
	stdout, stderr   jobLogState
	observedTerminal *time.Time
	epoch            string
	revision         uint64
}

// StartProcessJob queues the typed original start_process_job operation. The host
// must first establish project/scope authority, a unique durable request/job ID,
// and truthful recovery context. Access alone is not project authorization.
// This API has no synchronous Pending.Wait and never infers a process was spawned.
func (r *Registry) StartProcessJob(access Access, input protocol.JobInvocation) (protocol.ShellJobInfo, error) {
	var empty protocol.ShellJobInfo
	wire, err := input.IntoV2Request()
	if err != nil {
		return empty, err
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return empty, err
	}
	decoded, err := protocol.DecodeJobRequest(raw)
	if err != nil {
		return empty, err
	}
	process, ok := decoded.Operation.(protocol.JobProcessOperation)
	if !ok {
		return empty, fmt.Errorf("%w: process Job required", protocol.ErrUnsupported)
	}
	for _, id := range []string{wire.RequestID, wire.ClientID, process.JobID} {
		if err := validateID(id, 80, true); err != nil {
			return empty, err
		}
	}
	if process.Context.Validation != nil || len(process.Context.ValidationSteps) > 0 || process.Context.SSHResource != nil {
		return empty, fmt.Errorf("%w: validation or SSH Job", protocol.ErrUnsupported)
	}
	epoch, err := uuid.NewRandom()
	if err != nil {
		return empty, errors.New("Job observation identity unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return empty, ErrClosed
	}
	if r.options.MaxJobsPerRunner <= 0 {
		return empty, fmt.Errorf("%w: Job capacity is not configured", protocol.ErrUnsupported)
	}
	n := r.nodes[wire.ClientID]
	if n == nil || !permitted(access, n) {
		return empty, ErrForbidden
	}
	now := time.Now()
	r.sweepJobsLocked(now)
	if n.disconnectedAt != nil || now.Sub(n.lastSeen) > r.options.OnlineWindow {
		return empty, errors.New("Runner is offline")
	}
	if !supports(n.registration.Capabilities, wire.Kind) {
		return empty, errors.New("Runner capability not advertised")
	}
	if _, exists := r.jobs[process.JobID]; exists {
		return empty, errors.New("job_id already exists")
	}
	if r.pending[wire.RequestID] != nil || r.requestToJob[wire.RequestID] != "" {
		return empty, errors.New("request_id is already pending")
	}
	count, pending := 0, 0
	for _, job := range r.jobs {
		if job.info.ClientID == wire.ClientID {
			count++
		}
	}
	for _, p := range r.pending {
		if p.clientID == wire.ClientID {
			pending++
		}
	}
	if count >= r.options.MaxJobsPerRunner || pending >= r.options.MaxPendingPerRunner {
		return empty, ErrCapacity
	}
	context := process.Context
	context.CommandPreview = jobProcessPreview(process.Process.Executable, process.Process.Args)
	contextBytes, err := json.Marshal(context)
	if err != nil {
		return empty, err
	}
	wire.JobContext = contextBytes
	// Creation timestamps, like the original Server start path, are Server-owned.
	wire.CreatedAt = now.Unix()
	raw, err = json.Marshal(wire)
	if err != nil {
		return empty, err
	}
	owner := ""
	if n.registration.Owner != nil {
		owner = *n.registration.Owner
	}
	job := &jobRecord{info: protocol.ShellJobInfo{
		JobID: process.JobID, RequestID: jobPtr(wire.RequestID), ClientID: wire.ClientID, Kind: "shell",
		ProjectID: context.RuntimeProjectID, SessionID: context.WorkflowSessionID, Cwd: context.Cwd,
		ProjectCwd: context.ProjectCwd, Purpose: context.Purpose, Shell: context.Shell,
		CommandPreview: context.CommandPreview, Status: protocol.JobQueued.AsWire(), CreatedAt: now.Unix(),
		StructuredExecution: context.StructuredExecution, LastUpdateSeq: jobPtr(uint64(0)),
	}, instanceID: n.registration.AgentInstanceID, owner: owner, groupKind: n.access.GroupKind, groupID: n.access.GroupID,
		stdout: newJobLogState(), stderr: newJobLogState(), epoch: epoch.String()}
	p := &request{id: wire.RequestID, clientID: wire.ClientID, instanceID: job.instanceID, kind: wire.Kind, wire: raw, jobID: process.JobID}
	r.jobs[process.JobID] = job
	r.requestToJob[p.id] = process.JobID
	r.pending[p.id] = p
	n.queue = append(n.queue, p)
	return job.view(now), nil
}

func jobPtr[T any](value T) *T { return &value }
func jobLifecycle(j *jobRecord) protocol.RunnerJobLifecycle {
	v, _ := protocol.ParseRunnerJobLifecycle(j.info.Status)
	return v
}
func (j *jobRecord) view(now time.Time) protocol.ShellJobInfo {
	result, _ := copyJSON(j.info) // only validated owned DTOs, never live Host references
	if result.DurationMS != nil {
		result.ElapsedSecs = jobPtr(*result.DurationMS / 1000)
	} else if result.StartedAt != nil {
		end := now.Unix()
		if result.EndedAt != nil {
			end = *result.EndedAt
		}
		elapsed := uint64(0)
		if end > *result.StartedAt {
			elapsed = uint64(end) - uint64(*result.StartedAt)
		}
		result.ElapsedSecs = &elapsed
	}
	if jobLifecycle(j).IsTerminal() {
		result.Result = &protocol.RunnerJobResult{Shell: &protocol.RunnerShellJobResult{Cwd: result.Cwd, CommandPreview: result.CommandPreview, ExitCode: result.ExitCode, DurationMS: result.DurationMS, Error: result.Error}}
	}
	result.StdoutRetainedFromLine = jobPtr(j.stdout.firstRetainedLine)
	result.StderrRetainedFromLine = jobPtr(j.stderr.firstRetainedLine)
	result.StdoutLogTruncated = j.stdout.truncated
	result.StderrLogTruncated = j.stderr.truncated
	result.ObservationToken = jobPtr(fmt.Sprintf("wjob1:a:%s:%s:%d", result.JobID, j.epoch, j.revision))
	return result
}
func (j *jobRecord) changed() {
	if j.revision != ^uint64(0) {
		j.revision++
	}
}

// GetJob returns an owned current snapshot. Inaccessible IDs are indistinguishable
// from missing IDs. Terminal records remain observable after the runner goes offline.
func (r *Registry) GetJob(access Access, jobID string) (protocol.ShellJobInfo, error) {
	if err := validateID(jobID, 80, true); err != nil {
		return protocol.ShellJobInfo{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return protocol.ShellJobInfo{}, ErrClosed
	}
	now := time.Now()
	r.sweepJobsLocked(now)
	j := r.jobs[jobID]
	if j == nil || !r.jobVisibleLocked(access, j) {
		return protocol.ShellJobInfo{}, ErrUnknownJob
	}
	return j.view(now), nil
}

// ListJobs retains original newest-first ordering and display limit 20, clamped
// to 1..100 when supplied. A client filter never bypasses captured Job ownership.
func (r *Registry) ListJobs(access Access, clientID *string, status *string, limit *uint64) ([]protocol.ShellJobInfo, error) {
	if clientID != nil {
		if err := validateID(*clientID, 80, true); err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClosed
	}
	now := time.Now()
	r.sweepJobsLocked(now)
	jobs := make([]protocol.ShellJobInfo, 0)
	for _, j := range r.jobs {
		if r.jobVisibleLocked(access, j) && (clientID == nil || *clientID == j.info.ClientID) && (status == nil || *status == j.info.Status) {
			jobs = append(jobs, j.view(now))
		}
	}
	sort.SliceStable(jobs, func(a, b int) bool { return jobs[a].CreatedAt > jobs[b].CreatedAt })
	maximum := uint64(20)
	if limit != nil {
		maximum = *limit
		if maximum < 1 {
			maximum = 1
		}
		if maximum > 100 {
			maximum = 100
		}
	}
	if uint64(len(jobs)) > maximum {
		jobs = jobs[:int(maximum)]
	}
	return jobs, nil
}

// JobLog is the original immediate cursor read; it does not wait or claim an
// observation-token delta. Each stream has its own absolute, one-based cursor.
func (r *Registry) JobLog(access Access, request protocol.RunnerJobLogRequest) (protocol.RunnerJobLogResponse, error) {
	var empty protocol.RunnerJobLogResponse
	if err := validateID(request.JobID, 80, true); err != nil {
		return empty, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return empty, ErrClosed
	}
	now := time.Now()
	r.sweepJobsLocked(now)
	j := r.jobs[request.JobID]
	if j == nil || !r.jobVisibleLocked(access, j) || (request.ClientID != nil && *request.ClientID != j.info.ClientID) {
		return empty, ErrUnknownJob
	}
	stdout, nextOut, _ := j.stdout.selectLines(request.SinceStdoutLine, request.TailLines)
	stderr, nextErr, _ := j.stderr.selectLines(request.SinceStderrLine, request.TailLines)
	view := j.view(now)
	return protocol.RunnerJobLogResponse{Success: true, JobID: jobPtr(j.info.JobID), ClientID: jobPtr(j.info.ClientID), StdoutTail: &stdout, StderrTail: &stderr, NextStdoutLine: &nextOut, NextStderrLine: &nextErr, Job: &view}, nil
}

// StopJob removes an undispatched Job locally or registers one stop_job control
// request for a dispatched Job. stop_requested is not proof of remote termination.
func (r *Registry) StopJob(access Access, jobID, requestedBy string) (protocol.ShellJobInfo, error) {
	var empty protocol.ShellJobInfo
	if err := validateID(jobID, 80, true); err != nil {
		return empty, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return empty, ErrClosed
	}
	now := time.Now()
	r.sweepJobsLocked(now)
	j := r.jobs[jobID]
	if j == nil || !r.jobVisibleLocked(access, j) {
		return empty, ErrUnknownJob
	}
	// Observation-wide visibility alone is not managed-owner execution authority.
	n := r.nodes[j.info.ClientID]
	if n == nil || !permitted(access, n) {
		return empty, ErrForbidden
	}
	if j.stopRegistered {
		return j.view(now), nil
	}
	state := jobLifecycle(j)
	switch state {
	case protocol.JobQueued:
		if !j.dispatched {
			r.finishJobLocked(j, protocol.JobStopped, "job stopped before Runner picked it up", now)
			j.info.CommandExecutionState = jobPtr(protocol.CommandNotStarted)
			j.changed()
			break
		}
		// A legacy queued update cannot undo dispatch evidence.
		fallthrough
	case protocol.JobRunnerQueued, protocol.JobRunning, protocol.JobStartedLegacy:
		if n.disconnectedAt != nil || now.Sub(n.lastSeen) > r.options.OnlineWindow || n.registration.AgentInstanceID != j.instanceID {
			return empty, ErrStale
		}
		count := 0
		for _, p := range r.pending {
			if p.clientID == j.info.ClientID {
				count++
			}
		}
		if count >= r.options.MaxPendingPerRunner {
			return empty, ErrCapacity
		}
		id, err := uuid.NewRandom()
		if err != nil {
			return empty, errors.New("Job control identity unavailable")
		}
		request, err := (protocol.JobInvocation{Metadata: protocol.InvocationMetadata{RequestID: id.String(), ClientID: j.info.ClientID, RequestedBy: requestedBy, CreatedAt: now.Unix()}, Operation: protocol.JobStopOperation{JobID: jobID}}).IntoV2Request()
		if err != nil {
			return empty, err
		}
		raw, err := json.Marshal(request)
		if err != nil {
			return empty, err
		}
		j.stopRegistered = true
		r.queueJobStopLocked(n, j, id.String(), raw)
		j.info.Status = protocol.JobStopRequested.AsWire()
		j.info.Error = jobPtr("stop requested")
		j.changed()
	}
	return j.view(now), nil
}

func (r *Registry) queueJobStopLocked(n *node, j *jobRecord, id string, raw []byte) {
	p := &request{id: id, clientID: j.info.ClientID, instanceID: j.instanceID, kind: "stop_job", wire: raw, jobID: j.info.JobID}
	r.pending[id] = p
	n.queue = append(n.queue, p)
}
func (r *Registry) jobVisibleLocked(access Access, j *jobRecord) bool {
	if access.GlobalVisibility {
		return true
	}
	if j.groupKind != "" {
		return access.GroupKind == j.groupKind && access.GroupID == j.groupID
	}
	n := r.nodes[j.info.ClientID]
	return n != nil && permitted(access, n) && access.GroupKind == "" && (access.OwnerBypass || (access.Username != "" && access.Username == j.owner))
}
func (r *Registry) removeJobRequestsLocked(jobID string) {
	for _, p := range r.pending {
		if p.jobID == jobID {
			r.settleLocked(p, Outcome{})
		}
	}
	for id, job := range r.requestToJob {
		if job == jobID {
			delete(r.requestToJob, id)
		}
	}
}
func (r *Registry) finishJobLocked(j *jobRecord, state protocol.RunnerJobLifecycle, message string, now time.Time) {
	if jobLifecycle(j).IsTerminal() {
		return
	}
	j.info.Status = state.AsWire()
	j.info.EndedAt = jobPtr(now.Unix())
	j.observedTerminal = jobPtr(now)
	j.info.Activity = nil
	if message != "" {
		j.info.Error = &message
	}
	r.removeJobRequestsLocked(j.info.JobID)
}
func (r *Registry) loseJobLocked(j *jobRecord, reason, message string, now time.Time) {
	if jobLifecycle(j).IsTerminal() {
		return
	}
	r.finishJobLocked(j, protocol.JobLost, message, now)
	j.info.CommandExecutionState = jobPtr(protocol.CommandNotStarted)
	if j.info.StartedAt != nil {
		j.info.CommandExecutionState = jobPtr(protocol.CommandOutcomeUnknown)
	}
	j.info.RecoveryReasonCode = &reason
	j.changed()
}
func (r *Registry) loseClientJobsLocked(clientID, reason, message string, now time.Time) {
	for _, j := range r.jobs {
		if j.info.ClientID == clientID {
			r.loseJobLocked(j, reason, message, now)
		}
	}
}
func (r *Registry) sweepJobsLocked(now time.Time) {
	for id, j := range r.jobs {
		state := jobLifecycle(j)
		if state.IsActive() && j.dispatched {
			n := r.nodes[j.info.ClientID]
			if n == nil || n.disconnectedAt != nil || now.Sub(n.lastSeen) > r.options.OnlineWindow {
				r.loseJobLocked(j, "runner_disconnected_without_reconciliation", "runner went offline before the job completed", now)
			}
		}
		if j.observedTerminal != nil && now.Sub(*j.observedTerminal) >= jobTerminalRetention {
			r.removeJobRequestsLocked(id)
			delete(r.jobs, id)
		}
	}
}
