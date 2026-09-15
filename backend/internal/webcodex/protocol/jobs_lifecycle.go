// SPDX-License-Identifier: Apache-2.0
// Source: runner_job_lifecycle.rs at 97ad66949a859174911c2f6da2ff1063be98bfa9.
package protocol

import "fmt"

type RunnerJobLifecycle string

const (
	JobQueued        RunnerJobLifecycle = "queued"
	JobRunnerQueued  RunnerJobLifecycle = "agent_queued"
	JobStartedLegacy RunnerJobLifecycle = "started"
	JobRunning       RunnerJobLifecycle = "running"
	JobStopRequested RunnerJobLifecycle = "stop_requested"
	JobCompleted     RunnerJobLifecycle = "completed"
	JobFailed        RunnerJobLifecycle = "failed"
	JobStopped       RunnerJobLifecycle = "stopped"
	JobTimeout       RunnerJobLifecycle = "timeout"
	JobTimedOut      RunnerJobLifecycle = "timed_out"
	JobLost          RunnerJobLifecycle = "lost"
	JobCancelled     RunnerJobLifecycle = "cancelled"
)

// ParseRunnerJobLifecycle matches Rust from_wire exactly (no implicit trim).
func ParseRunnerJobLifecycle(s string) (RunnerJobLifecycle, error) {
	switch v := RunnerJobLifecycle(s); v {
	case JobQueued, JobRunnerQueued, JobStartedLegacy, JobRunning, JobStopRequested, JobCompleted, JobFailed, JobStopped, JobTimeout, JobTimedOut, JobLost, JobCancelled:
		return v, nil
	}
	return "", fmt.Errorf("unknown Runner Job lifecycle status: %s", s)
}

func (s RunnerJobLifecycle) AsWire() string { return string(s) }
func (s RunnerJobLifecycle) IsTerminal() bool {
	switch s {
	case JobCompleted, JobFailed, JobStopped, JobTimeout, JobTimedOut, JobLost, JobCancelled:
		return true
	}
	return false
}
func (s RunnerJobLifecycle) IsRunnerActive() bool {
	return s == JobRunnerQueued || s == JobRunning || s == JobStopRequested
}
func (s RunnerJobLifecycle) IsActive() bool {
	return s == JobQueued || s == JobStartedLegacy || s.IsRunnerActive()
}
func (s RunnerJobLifecycle) IsTimedOut() bool { return s == JobTimeout || s == JobTimedOut }
func (a ShellJobActivity) IsCanonical() bool {
	if a.State == ActivityWaiting {
		return a.Phase == ActivityCargoWaitingForBuildLock && a.Source == ActivityCargoOutput
	}
	if a.State != ActivityWorking {
		return false
	}
	switch a.Source {
	case ActivityRunnerExecution:
		return a.Phase == ActivityProcessRunning
	case ActivityValidationPlan:
		return a.Phase == ActivityValidationFormat || a.Phase == ActivityValidationCheck || a.Phase == ActivityValidationTest
	case ActivityCargoOutput:
		return a.Phase == ActivityCargoCompiling || a.Phase == ActivityCargoChecking
	}
	return false
}
