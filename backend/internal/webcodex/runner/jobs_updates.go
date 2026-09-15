// SPDX-License-Identifier: Apache-2.0
// WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9 job_updates.rs:1885-2162.
package runner

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

// UpdateJob accepts only original legacy updates for an admitted process Job.
// Capability-gated sequencing and authoritative inventories/snapshots remain
// unsupported; optional legacy update_seq is recorded, not used as a replay key.
func (r *Registry) UpdateJob(principal Principal, input protocol.RunnerJobUpdateRequest) (protocol.ShellJobInfo, error) {
	var empty protocol.ShellJobInfo
	access, err := principal.authorize(input.ClientID, ScopeJobUpdate)
	if err != nil {
		return empty, err
	}
	body, err := copyJSON(input)
	if err != nil {
		return empty, err
	}
	if err := validateID(body.JobID, 80, true); err != nil {
		return empty, err
	}
	if body.RequestID != nil {
		if err := validateID(*body.RequestID, 80, true); err != nil {
			return empty, err
		}
	}
	if body.LogSnapshot != nil {
		return empty, fmt.Errorf("%w: log_snapshot requires job_state_reconciliation", protocol.ErrUnsupported)
	}
	lifecycle, err := protocol.ParseRunnerJobLifecycle(strings.TrimSpace(body.Status))
	if err != nil {
		return empty, fmt.Errorf("job update status is invalid")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.activeLocked(access, body.ClientID, body.AgentInstanceID)
	if err != nil {
		return empty, err
	}
	j := r.jobs[body.JobID]
	if j == nil {
		return empty, ErrUnknownJob
	}
	if j.info.ClientID != body.ClientID || j.instanceID != body.AgentInstanceID || !r.jobVisibleLocked(access, j) {
		return empty, ErrForbidden
	}
	if body.RequestID != nil && (j.info.RequestID == nil || *body.RequestID != *j.info.RequestID) {
		return empty, fmt.Errorf("job update request_id does not match job_id")
	}
	now := time.Now()
	// Original terminal latch precedes per-field lifecycle validation and mutation.
	if jobLifecycle(j).IsTerminal() {
		n.lastSeen = now
		n.disconnectedAt = nil
		return j.view(now), nil
	}
	if !j.dispatched || r.requestToJob[*j.info.RequestID] != body.JobID {
		return empty, fmt.Errorf("Runner Job request has not been dispatched")
	}
	before, _ := copyJSON(j.info)
	oldOut, oldErr := j.stdout, j.stderr
	if code := jobUpdateViolation(j, body, lifecycle); code != "" {
		r.finishJobLocked(j, protocol.JobFailed, "executor protocol violation: "+code, now)
		j.info.ExitCode = body.ExitCode
		j.info.DurationMS = body.DurationMS
		state := protocol.CommandNotStarted
		if j.info.StartedAt != nil {
			state = protocol.CommandOutcomeUnknown
		}
		j.info.CommandExecutionState = &state
	} else {
		j.stdout.replace(body.StdoutTail)
		j.stderr.replace(body.StderrTail)
		j.stdout.append(body.StdoutChunk)
		j.stderr.append(body.StderrChunk)
		if body.Activity != nil {
			j.info.Activity = body.Activity
		}
		if j.info.StartedAt == nil && (body.CommandExecutionState == nil || *body.CommandExecutionState != protocol.CommandNotStarted) {
			switch lifecycle {
			case protocol.JobRunning, protocol.JobCompleted, protocol.JobFailed, protocol.JobStopped, protocol.JobTimeout:
				j.info.StartedAt = jobPtr(now.Unix())
			}
		}
		status := lifecycle
		if status == protocol.JobQueued && j.info.StartedAt != nil {
			status = protocol.JobRunnerQueued
		}
		j.info.Status = status.AsWire()
		if lifecycle.IsTerminal() {
			// finishJobLocked's guard expects the previous nonterminal lifecycle.
			j.info.Status = before.Status
			r.finishJobLocked(j, lifecycle, "", now)
			j.info.ExitCode = body.ExitCode
			j.info.DurationMS = body.DurationMS
			j.info.Error = body.Error
			j.info.CommandExecutionState = body.CommandExecutionState
		} else if body.Error != nil {
			j.info.Error = body.Error
		}
		// Legacy updates do not copy active exit_code/duration. finished is kept with
		// the source fallback rather than upgrading it to sequenced completed=0 rules.
		if body.Finished && !jobLifecycle(j).IsTerminal() {
			terminal := protocol.JobFailed
			if j.info.Error == nil && j.info.ExitCode != nil && *j.info.ExitCode == 0 {
				terminal = protocol.JobCompleted
			}
			r.finishJobLocked(j, terminal, "", now)
		}
	}
	if body.UpdateSeq != nil {
		j.info.LastUpdateSeq = jobPtr(*body.UpdateSeq)
	}
	if !reflect.DeepEqual(before, j.info) || oldOut != j.stdout || oldErr != j.stderr {
		j.changed()
	}
	n.lastSeen = now
	n.disconnectedAt = nil
	return j.view(now), nil
}

func jobUpdateViolation(j *jobRecord, u protocol.RunnerJobUpdateRequest, state protocol.RunnerJobLifecycle) string {
	if u.ValidationProgress != nil {
		return "validation_progress_unexpected"
	}
	terminal := state.IsTerminal()
	if !terminal && u.CommandExecutionState != nil {
		return "command_execution_state_on_active_job"
	}
	if terminal && u.CommandExecutionState == nil {
		return "structured_job_lifecycle_missing"
	}
	if u.CommandExecutionState != nil {
		valid := false
		switch *u.CommandExecutionState {
		case protocol.CommandNotStarted:
			valid = (state == protocol.JobFailed || state == protocol.JobStopped || state == protocol.JobCancelled || state == protocol.JobLost) && j.info.StartedAt == nil
		case protocol.CommandOutcomeUnknown:
			valid = state == protocol.JobFailed || state == protocol.JobLost
		case protocol.CommandTimedOut:
			valid = state.IsTimedOut()
		case protocol.CommandCompleted:
			valid = state == protocol.JobCompleted || state == protocol.JobFailed || state == protocol.JobStopped || state == protocol.JobCancelled
		}
		if !valid {
			return "structured_job_lifecycle_invalid"
		}
	}
	if activity := u.Activity; activity != nil {
		if !activity.IsCanonical() || (state != protocol.JobRunning && state != protocol.JobStopRequested) || activity.Source != protocol.ActivityRunnerExecution || activity.Phase != protocol.ActivityProcessRunning {
			return "job_activity_invalid"
		}
	}
	return ""
}
