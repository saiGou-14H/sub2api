// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9 runner_protocol.rs.
package protocol

// Status fields intentionally remain strings: lifecycle parsing is opt-in.
type RunnerJobUpdateRequest struct {
	ClientID              string                      `json:"client_id" wire:"required"`
	AgentInstanceID       string                      `json:"agent_instance_id" wire:"required"`
	JobID                 string                      `json:"job_id" wire:"required"`
	RequestID             *string                     `json:"request_id"`
	UpdateSeq             *uint64                     `json:"update_seq,omitempty"`
	Status                string                      `json:"status" wire:"required"`
	StdoutChunk           *string                     `json:"stdout_chunk"`
	StderrChunk           *string                     `json:"stderr_chunk"`
	StdoutTail            *string                     `json:"stdout_tail"`
	StderrTail            *string                     `json:"stderr_tail"`
	LogSnapshot           *ShellJobLogSnapshot        `json:"log_snapshot,omitempty"`
	ExitCode              *int32                      `json:"exit_code"`
	DurationMS            *uint64                     `json:"duration_ms"`
	Error                 *string                     `json:"error"`
	CommandExecutionState *ShellCommandExecutionState `json:"command_execution_state,omitempty"`
	ValidationProgress    *ShellJobValidationProgress `json:"validation_progress,omitempty"`
	Activity              *ShellJobActivity           `json:"activity,omitempty"`
	Finished              bool                        `json:"finished"`
}
type RunnerJobUpdateResponse struct {
	Success bool          `json:"success" wire:"required"`
	Job     *ShellJobInfo `json:"job,omitempty"`
	Error   *string       `json:"error,omitempty"`
}
type ShellJobCodexMetadata struct {
	Project         *string `json:"project,omitempty"`
	GoalID          *string `json:"goal_id,omitempty"`
	ClientRequestID *string `json:"client_request_id,omitempty"`
	Command         *string `json:"command,omitempty"`
	Kind            *string `json:"kind,omitempty"`
	Suite           *string `json:"suite,omitempty"`
	ScriptPath      *string `json:"script_path,omitempty"`
	Reason          *string `json:"reason,omitempty"`
	MaxRuntimeSecs  *int64  `json:"max_runtime_secs,omitempty"`
}
type ShellJobValidationStep struct {
	Name    string            `json:"name" wire:"required"`
	Program string            `json:"program" wire:"required"`
	Args    []string          `json:"args"`
	Env     []ShellJobEnvPair `json:"env,omitempty"`
}

// ShellJobEnvPair is the exact two-string tuple used by Vec<(String,String)>.
type ShellJobEnvPair [2]string
type ShellJobValidationProgress struct {
	Completed   uint64  `json:"completed" wire:"required"`
	CurrentStep *string `json:"current_step,omitempty"`
	FailedStep  *string `json:"failed_step,omitempty"`
}
type ShellJobActivityState string
type ShellJobActivityPhase string
type ShellJobActivitySource string

const (
	ActivityWorking                  ShellJobActivityState  = "working"
	ActivityWaiting                  ShellJobActivityState  = "waiting"
	ActivityProcessRunning           ShellJobActivityPhase  = "process_running"
	ActivityValidationFormat         ShellJobActivityPhase  = "validation_format"
	ActivityValidationCheck          ShellJobActivityPhase  = "validation_check"
	ActivityValidationTest           ShellJobActivityPhase  = "validation_test"
	ActivityCargoWaitingForBuildLock ShellJobActivityPhase  = "cargo_waiting_for_build_lock"
	ActivityCargoCompiling           ShellJobActivityPhase  = "cargo_compiling"
	ActivityCargoChecking            ShellJobActivityPhase  = "cargo_checking"
	ActivityRunnerExecution          ShellJobActivitySource = "runner_execution"
	ActivityValidationPlan           ShellJobActivitySource = "validation_plan"
	ActivityCargoOutput              ShellJobActivitySource = "cargo_output"
)

type ShellJobActivity struct {
	State  ShellJobActivityState  `json:"state" wire:"required"`
	Phase  ShellJobActivityPhase  `json:"phase" wire:"required"`
	Source ShellJobActivitySource `json:"source" wire:"required"`
}
type ShellJobValidationMetadata struct {
	Tool                 string                   `json:"tool" wire:"required"`
	Kind                 string                   `json:"kind" wire:"required"`
	Steps                []ShellJobValidationStep `json:"steps" wire:"required"`
	EffectiveTimeoutSecs uint64                   `json:"effective_timeout_secs" wire:"required"`
	SyncWaitSecs         uint64                   `json:"sync_wait_secs" wire:"required"`
	Adapter              string                   `json:"adapter" wire:"required"`
	ValidationTargetID   *string                  `json:"validation_target_id,omitempty"`
	MinimumTests         *uint64                  `json:"minimum_tests,omitempty"`
	RequireTests         *bool                    `json:"require_tests,omitempty"`
	NoRun                *bool                    `json:"no_run,omitempty"`
}
type ShellJobStructuredExecutionMetadata struct {
	ExecutionSource    string               `json:"execution_source" wire:"required"`
	Language           *ShellScriptLanguage `json:"language,omitempty"`
	ScriptBytes        *uint64              `json:"script_bytes,omitempty"`
	ArgCount           uint64               `json:"arg_count" wire:"required"`
	StdinPresent       bool                 `json:"stdin_present" wire:"required"`
	ValidationIdentity *string              `json:"validation_identity,omitempty"`
	AssertionName      *string              `json:"assertion_name,omitempty"`
	ValidationTool     *string              `json:"validation_tool,omitempty"`
}
type ShellJobContext struct {
	RuntimeProjectID    *string                              `json:"runtime_project_id,omitempty"`
	WorkflowSessionID   *string                              `json:"workflow_session_id,omitempty"`
	SSHResource         *string                              `json:"ssh_resource,omitempty"`
	ProjectCwd          *string                              `json:"project_cwd,omitempty"`
	Cwd                 *string                              `json:"cwd,omitempty"`
	Purpose             *string                              `json:"purpose,omitempty"`
	Shell               *string                              `json:"shell,omitempty"`
	CommandPreview      string                               `json:"command_preview" wire:"required"`
	ValidationSteps     []string                             `json:"validation_steps,omitempty"`
	Validation          *ShellJobValidationMetadata          `json:"validation,omitempty"`
	StructuredExecution *ShellJobStructuredExecutionMetadata `json:"structured_execution,omitempty"`
}
type ShellJobStreamSnapshot struct {
	Tail              string `json:"tail"`
	FirstRetainedLine uint64 `json:"first_retained_line"`
	NextLine          uint64 `json:"next_line"`
	Truncated         bool   `json:"truncated"`
}
type ShellJobLogSnapshot struct {
	Stdout ShellJobStreamSnapshot `json:"stdout" wire:"required"`
	Stderr ShellJobStreamSnapshot `json:"stderr" wire:"required"`
}
type ShellJobSnapshot struct {
	JobID                 string                      `json:"job_id" wire:"required"`
	RequestID             string                      `json:"request_id" wire:"required"`
	Status                string                      `json:"status" wire:"required"`
	UpdateSeq             uint64                      `json:"update_seq" wire:"required"`
	CreatedAt             int64                       `json:"created_at" wire:"required"`
	StartedAt             *int64                      `json:"started_at,omitempty"`
	EndedAt               *int64                      `json:"ended_at,omitempty"`
	ExitCode              *int32                      `json:"exit_code,omitempty"`
	DurationMS            *uint64                     `json:"duration_ms,omitempty"`
	Error                 *string                     `json:"error,omitempty"`
	CommandExecutionState *ShellCommandExecutionState `json:"command_execution_state,omitempty"`
	Context               ShellJobContext             `json:"context" wire:"required"`
	Stdout                ShellJobStreamSnapshot      `json:"stdout"`
	Stderr                ShellJobStreamSnapshot      `json:"stderr"`
	ValidationProgress    *ShellJobValidationProgress `json:"validation_progress,omitempty"`
	Activity              *ShellJobActivity           `json:"activity,omitempty"`
}
type ShellJobInventory struct {
	ActiveComplete bool               `json:"active_complete"`
	Jobs           []ShellJobSnapshot `json:"jobs"`
}
type RunnerShellJobResult struct {
	Cwd            *string `json:"cwd"`
	CommandPreview string  `json:"command_preview" wire:"required"`
	ExitCode       *int32  `json:"exit_code"`
	DurationMS     *uint64 `json:"duration_ms"`
	Error          *string `json:"error"`
}
type RunnerJobResult struct {
	Shell *RunnerShellJobResult `json:"shell,omitempty"`
}
type ShellJobInfo struct {
	JobID                       string                               `json:"job_id" wire:"required"`
	RequestID                   *string                              `json:"request_id,omitempty"`
	ClientID                    string                               `json:"client_id" wire:"required"`
	Kind                        string                               `json:"kind"`
	ProjectID                   *string                              `json:"project_id,omitempty"`
	SessionID                   *string                              `json:"session_id,omitempty"`
	SSHResource                 *string                              `json:"ssh_resource,omitempty"`
	Cwd                         *string                              `json:"cwd,omitempty"`
	ProjectCwd                  *string                              `json:"project_cwd,omitempty"`
	Purpose                     *string                              `json:"purpose,omitempty"`
	Shell                       *string                              `json:"shell,omitempty"`
	CommandPreview              string                               `json:"command_preview" wire:"required"`
	Status                      string                               `json:"status" wire:"required"`
	CreatedAt                   int64                                `json:"created_at" wire:"required"`
	StartedAt                   *int64                               `json:"started_at,omitempty"`
	EndedAt                     *int64                               `json:"ended_at,omitempty"`
	ExitCode                    *int32                               `json:"exit_code,omitempty"`
	DurationMS                  *uint64                              `json:"duration_ms,omitempty"`
	ElapsedSecs                 *uint64                              `json:"elapsed_secs,omitempty"`
	Error                       *string                              `json:"error,omitempty"`
	CommandExecutionState       *ShellCommandExecutionState          `json:"command_execution_state,omitempty"`
	StructuredExecution         *ShellJobStructuredExecutionMetadata `json:"structured_execution,omitempty"`
	Codex                       *ShellJobCodexMetadata               `json:"codex,omitempty"`
	Result                      *RunnerJobResult                     `json:"result,omitempty"`
	ValidationProgress          *ShellJobValidationProgress          `json:"validation_progress,omitempty"`
	Activity                    *ShellJobActivity                    `json:"activity,omitempty"`
	Validation                  *ShellJobValidationMetadata          `json:"validation,omitempty"`
	RecoveryState               *string                              `json:"recovery_state,omitempty"`
	RecoveredAfterServerRestart bool                                 `json:"recovered_after_server_restart"`
	ReconciledAt                *int64                               `json:"reconciled_at,omitempty"`
	RecoveryReasonCode          *string                              `json:"recovery_reason_code,omitempty"`
	ObservationToken            *string                              `json:"observation_token,omitempty"`
	LastUpdateSeq               *uint64                              `json:"last_update_seq,omitempty"`
	StdoutRetainedFromLine      *uint64                              `json:"stdout_retained_from_line,omitempty"`
	StderrRetainedFromLine      *uint64                              `json:"stderr_retained_from_line,omitempty"`
	StdoutLogTruncated          bool                                 `json:"stdout_log_truncated"`
	StderrLogTruncated          bool                                 `json:"stderr_log_truncated"`
}
type RunnerJobStatusRequest struct {
	ClientID *string `json:"client_id"`
	JobID    string  `json:"job_id" wire:"required"`
}
type RunnerJobStopRequest RunnerJobStatusRequest
type RunnerJobLogRequest struct {
	ClientID        *string `json:"client_id"`
	JobID           string  `json:"job_id" wire:"required"`
	TailLines       *uint64 `json:"tail_lines"`
	SinceStdoutLine *uint64 `json:"since_stdout_line"`
	SinceStderrLine *uint64 `json:"since_stderr_line"`
}
type RunnerJobsListRequest struct {
	ClientID string  `json:"client_id" wire:"required"`
	Status   *string `json:"status"`
	Limit    *uint64 `json:"limit"`
}
type RunnerJobStatusResponse struct {
	Success     bool             `json:"success" wire:"required"`
	JobID       *string          `json:"job_id,omitempty"`
	ClientID    *string          `json:"client_id,omitempty"`
	Kind        *string          `json:"kind,omitempty"`
	Status      *string          `json:"status,omitempty"`
	ElapsedSecs *uint64          `json:"elapsed_secs,omitempty"`
	ExitCode    *int32           `json:"exit_code,omitempty"`
	Result      *RunnerJobResult `json:"result,omitempty"`
	Job         *ShellJobInfo    `json:"job,omitempty"`
	Error       *string          `json:"error,omitempty"`
}
type RunnerJobLogResponse struct {
	Success        bool          `json:"success" wire:"required"`
	JobID          *string       `json:"job_id,omitempty"`
	ClientID       *string       `json:"client_id,omitempty"`
	StdoutTail     *string       `json:"stdout_tail,omitempty"`
	StderrTail     *string       `json:"stderr_tail,omitempty"`
	NextStdoutLine *uint64       `json:"next_stdout_line,omitempty"`
	NextStderrLine *uint64       `json:"next_stderr_line,omitempty"`
	Job            *ShellJobInfo `json:"job,omitempty"`
	Error          *string       `json:"error,omitempty"`
}
type RunnerJobStopResponse struct {
	Success bool          `json:"success" wire:"required"`
	JobID   *string       `json:"job_id,omitempty"`
	Status  *string       `json:"status,omitempty"`
	Job     *ShellJobInfo `json:"job,omitempty"`
	Error   *string       `json:"error,omitempty"`
}
type RunnerJobsListResponse struct {
	Success  bool           `json:"success" wire:"required"`
	ClientID string         `json:"client_id" wire:"required"`
	Jobs     []ShellJobInfo `json:"jobs" wire:"required"`
	Error    *string        `json:"error,omitempty"`
}
