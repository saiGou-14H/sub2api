// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// crates/webcodex-core/src/runner_protocol.rs. See README.md.
package protocol

import "encoding/json"

type RunnerProtocolGenerationNumber uint16

const RunnerProtocolGenerationV2 RunnerProtocolGenerationNumber = 2

// RunnerCapabilities follows the original wire defaults, not advertised support.
// Only Shell defaults true outside registration; registration requires it explicitly.
type RunnerCapabilities struct {
	Shell                              bool `json:"shell"`
	FileRead                           bool `json:"file_read"`
	FileWrite                          bool `json:"file_write"`
	ArtifactExportChunkRead            bool `json:"artifact_export_chunk_read,omitempty"`
	ArtifactExportStreamingMetadata    bool `json:"artifact_export_streaming_metadata,omitempty"`
	StructuredFileDelete               bool `json:"structured_file_delete,omitempty"`
	ApplyTextEditOccurrence            bool `json:"apply_text_edit_occurrence,omitempty"`
	ApplyTextEditLineScope             bool `json:"apply_text_edit_line_scope,omitempty"`
	ApplyPatch                         bool `json:"apply_patch,omitempty"`
	ApplyPatchMatchMetadata            bool `json:"apply_patch_match_metadata,omitempty"`
	ApplyPatchMatchingMode             bool `json:"apply_patch_matching_mode,omitempty"`
	ApplyPatchStrictMatching           bool `json:"apply_patch_strict_matching,omitempty"`
	Git                                bool `json:"git"`
	Jobs                               bool `json:"jobs"`
	AsyncJobs                          bool `json:"async_jobs"`
	AsyncShellJobs                     bool `json:"async_shell_jobs"`
	SSHShell                           bool `json:"ssh_shell"`
	PersistentShell                    bool `json:"persistent_shell"`
	SSHPersistentShell                 bool `json:"ssh_persistent_shell"`
	StructuredValidationArgv           bool `json:"structured_validation_argv"`
	StructuredCargoTestCountAssertion  bool `json:"structured_cargo_test_count_assertion,omitempty"`
	StructuredCargoTestExecutionPolicy bool `json:"structured_cargo_test_execution_policy,omitempty"`
	StructuredGoTestJSON               bool `json:"structured_go_test_json,omitempty"`
	StructuredGoTestTool               bool `json:"structured_go_test_tool,omitempty"`
	StructuredGoTestPackages           bool `json:"structured_go_test_packages,omitempty"`
	StructuredProcessArgv              bool `json:"structured_process_argv"`
	StructuredScriptPayload            bool `json:"structured_script_payload"`
	InternalPosixScript                bool `json:"internal_posix_script,omitempty"`
	StructuredExecutionJobs            bool `json:"structured_execution_jobs"`
	DetachedProcessJobs                bool `json:"detached_process_jobs,omitempty"`
	LSPReadOnlyNavigation              bool `json:"lsp_read_only_navigation"`
	LSPCallHierarchy                   bool `json:"lsp_call_hierarchy"`
	ProjectLifecycle                   bool `json:"project_lifecycle"`
	ProjectPathRegistration            bool `json:"project_path_registration"`
	ManagedWorktree                    bool `json:"managed_worktree,omitempty"`
	SkillStoreRead                     bool `json:"skill_store_read,omitempty"`
	SkillStoreManage                   bool `json:"skill_store_manage,omitempty"`
	ComputerObserve                    bool `json:"computer_observe,omitempty"`
	ComputerApplicationDiscovery       bool `json:"computer_application_discovery,omitempty"`
	ComputerApplicationLaunch          bool `json:"computer_application_launch,omitempty"`
	ComputerDisplayObserve             bool `json:"computer_display_observe,omitempty"`
	ComputerPointerControl             bool `json:"computer_pointer_control,omitempty"`
	ComputerClipboardRead              bool `json:"computer_clipboard_read,omitempty"`
	ComputerClipboardWrite             bool `json:"computer_clipboard_write,omitempty"`
	ComputerSnapshotRegion             bool `json:"computer_snapshot_region,omitempty"`
	ComputerAccessibilityObserve       bool `json:"computer_accessibility_observe,omitempty"`
	ComputerElementState               bool `json:"computer_element_state,omitempty"`
	ComputerControl                    bool `json:"computer_control,omitempty"`
	ComputerScrollToElement            bool `json:"computer_scroll_to_element,omitempty"`
	ComputerKeyInput                   bool `json:"computer_key_input,omitempty"`
	ComputerWindowActivate             bool `json:"computer_window_activate,omitempty"`
	ComputerTextInput                  bool `json:"computer_text_input,omitempty"`
	JobStateReconciliation             bool `json:"job_state_reconciliation,omitempty"`
	CodingAgentRuns                    bool `json:"coding_agent_runs,omitempty"`
	NativeToolPlugins                  bool `json:"native_tool_plugins,omitempty"`
	ManagedSSHResources                bool `json:"managed_ssh_resources,omitempty"`
	RunnerConfigControl                bool `json:"runner_config_control,omitempty"`
}

type RunnerBuildInfo struct {
	Version   *string `json:"version,omitempty"`
	GitCommit *string `json:"git_commit,omitempty"`
	GitDirty  *bool   `json:"git_dirty,omitempty"`
}
type RunnerHostContext struct {
	Role         *string `json:"role,omitempty"`
	Runtime      *string `json:"runtime,omitempty"`
	Service      *string `json:"service,omitempty"`
	Network      *string `json:"network,omitempty"`
	Architecture *string `json:"architecture,omitempty"`
}

type RunnerRegisterRequest struct {
	ClientID                string                         `json:"client_id" wire:"required"`
	AgentInstanceID         string                         `json:"agent_instance_id" wire:"required"`
	AgentProtocolGeneration RunnerProtocolGenerationNumber `json:"agent_protocol_generation" wire:"required"`
	DisplayName             *string                        `json:"display_name"`
	Owner                   *string                        `json:"owner"`
	Hostname                *string                        `json:"hostname"`
	Capabilities            RunnerCapabilities             `json:"capabilities" wire:"required"`
	HostContext             *RunnerHostContext             `json:"host_context,omitempty"`
	Policy                  json.RawMessage                `json:"policy,omitempty" shape:"object"`
	ProcessStartedAt        *int64                         `json:"process_started_at,omitempty"`
	Build                   *RunnerBuildInfo               `json:"build,omitempty"`
	JobConcurrencyLimit     *uint64                        `json:"job_concurrency_limit,omitempty"`
	JobInventory            json.RawMessage                `json:"job_inventory,omitempty" shape:"job"`
	CodingAgentProviders    json.RawMessage                `json:"coding_agent_providers,omitempty" shape:"array"`
	CodingAgentInventory    json.RawMessage                `json:"coding_agent_inventory,omitempty" shape:"object"`
}

type RunnerView struct {
	ClientID                string                         `json:"client_id" wire:"required"`
	AgentInstanceID         string                         `json:"agent_instance_id"`
	DisplayName             *string                        `json:"display_name"`
	Owner                   *string                        `json:"owner"`
	Hostname                *string                        `json:"hostname"`
	Status                  string                         `json:"status" wire:"required"`
	HostContext             *RunnerHostContext             `json:"host_context,omitempty"`
	Connected               bool                           `json:"connected" wire:"required"`
	LastSeen                int64                          `json:"last_seen" wire:"required"`
	Capabilities            RunnerCapabilities             `json:"capabilities" wire:"required"`
	CodingAgentProviders    json.RawMessage                `json:"coding_agent_providers,omitempty" shape:"array"`
	PendingRequests         uint64                         `json:"pending_requests" wire:"required"`
	Projects                []RunnerProjectSummary         `json:"projects"`
	ProjectInventory        json.RawMessage                `json:"project_inventory,omitempty" shape:"object"`
	AgentProtocolGeneration RunnerProtocolGenerationNumber `json:"agent_protocol_generation" wire:"required"`
	Transport               string                         `json:"transport"`
	Policy                  json.RawMessage                `json:"policy,omitempty" shape:"object"`
	RegisteredAt            int64                          `json:"registered_at"`
	ConnectedAt             int64                          `json:"connected_at"`
	DisconnectedAt          *int64                         `json:"disconnected_at,omitempty"`
	ProcessStartedAt        *int64                         `json:"process_started_at,omitempty"`
	Build                   *RunnerBuildInfo               `json:"build,omitempty"`
	JobConcurrencyLimit     *uint64                        `json:"job_concurrency_limit,omitempty"`
}

type RunnerProjectSummary struct {
	ID                 string   `json:"id" wire:"required"`
	Name               *string  `json:"name"`
	Path               string   `json:"path" wire:"required"`
	AllowPatch         bool     `json:"allow_patch"`
	Kind               *string  `json:"kind"`
	RegistrationSource *string  `json:"registration_source,omitempty"`
	Description        *string  `json:"description"`
	Hooks              []string `json:"hooks"`
	Disabled           bool     `json:"disabled"`
	Revision           *string  `json:"revision,omitempty"`
	GitBranch          *string  `json:"git_branch"`
	GitHead            *string  `json:"git_head"`
	GitDirty           *bool    `json:"git_dirty"`
	UpdatedAt          int64    `json:"updated_at" wire:"required"`
	ShellProfile       *string  `json:"shell_profile,omitempty"`
}

type RunnerRegisterResponse struct {
	Success bool        `json:"success" wire:"required"`
	Client  *RunnerView `json:"client,omitempty"`
	Error   *string     `json:"error,omitempty"`
}
type RunnerPollRequest struct {
	ClientID        string `json:"client_id" wire:"required"`
	AgentInstanceID string `json:"agent_instance_id" wire:"required"`
}
type RunnerPollPayload struct {
	RunnerPollRequest
	ToolProviders        json.RawMessage `json:"tool_providers,omitempty" shape:"object"`
	ProjectInventoryPage json.RawMessage `json:"project_inventory_page,omitempty" shape:"object"`
}
type RunnerPollResponse struct {
	Success          bool            `json:"success" wire:"required"`
	Request          *RunnerRequest  `json:"request,omitempty"`
	Error            *string         `json:"error,omitempty"`
	ProjectInventory json.RawMessage `json:"project_inventory,omitempty" shape:"object"`
}
type RunnerOfflineRequest RunnerPollRequest
type RunnerOfflineResponse struct {
	Success bool    `json:"success" wire:"required"`
	Error   *string `json:"error,omitempty"`
}
type RunnerResultResponse RunnerOfflineResponse

type RunnerResultRequest struct {
	ClientID        string  `json:"client_id" wire:"required"`
	AgentInstanceID string  `json:"agent_instance_id" wire:"required"`
	RequestID       string  `json:"request_id" wire:"required"`
	ExitCode        *int32  `json:"exit_code"`
	Stdout          *string `json:"stdout"`
	Stderr          *string `json:"stderr"`
	DurationMS      *uint64 `json:"duration_ms"`
	Error           *string `json:"error"`
}
type ShellCommandExecutionState string

const (
	CommandNotStarted     ShellCommandExecutionState = "not_started"
	CommandOutcomeUnknown ShellCommandExecutionState = "outcome_unknown"
	CommandTimedOut       ShellCommandExecutionState = "timed_out"
	CommandCompleted      ShellCommandExecutionState = "completed"
)

type RunnerResultPayload struct {
	RunnerResultRequest
	CommandExecutionState *ShellCommandExecutionState `json:"command_execution_state,omitempty"`
	MCPGateway            json.RawMessage             `json:"mcp_gateway,omitempty" shape:"object"`
	PluginGateway         json.RawMessage             `json:"plugin_gateway,omitempty" shape:"object"`
	CodingAgent           json.RawMessage             `json:"coding_agent,omitempty" shape:"object"`
}

type ShellProcessArgv struct {
	Executable string   `json:"executable" wire:"required"`
	Args       []string `json:"args"`
}
type ShellScriptLanguage string

const (
	ScriptSh         ShellScriptLanguage = "sh"
	ScriptBash       ShellScriptLanguage = "bash"
	ScriptPowershell ShellScriptLanguage = "powershell"
)

type ShellScriptPayload struct {
	Language ShellScriptLanguage `json:"language" wire:"required"`
	Script   string              `json:"script" wire:"required"`
	Args     []string            `json:"args"`
}

// RunnerRequest preserves every original top-level member. Deferred family
// payloads are data only; DecodeInvocation never dispatches them.
type RunnerRequest struct {
	RequestID       string              `json:"request_id" wire:"required"`
	ClientID        string              `json:"client_id" wire:"required"`
	Kind            string              `json:"kind"`
	JobID           *string             `json:"job_id,omitempty"`
	Cwd             *string             `json:"cwd,omitempty"`
	Path            *string             `json:"path,omitempty"`
	Content         *string             `json:"content,omitempty"`
	MaxBytes        *uint64             `json:"max_bytes,omitempty"`
	ExpectedSHA256  *string             `json:"expected_sha256,omitempty"`
	ExpectedPrefix  *string             `json:"expected_prefix,omitempty"`
	StartLine       *uint64             `json:"start_line,omitempty"`
	EndLine         *uint64             `json:"end_line,omitempty"`
	CreateDirs      bool                `json:"create_dirs"`
	Command         string              `json:"command" wire:"required"`
	Process         *ShellProcessArgv   `json:"process,omitempty"`
	Script          *ShellScriptPayload `json:"script,omitempty"`
	Stdin           *string             `json:"stdin,omitempty"`
	TimeoutSecs     uint64              `json:"timeout_secs" wire:"required"`
	RequestedBy     string              `json:"requested_by" wire:"required"`
	CreatedAt       int64               `json:"created_at" wire:"required"`
	Validation      json.RawMessage     `json:"validation,omitempty" shape:"object"`
	LSP             json.RawMessage     `json:"lsp,omitempty" shape:"object"`
	JobContext      json.RawMessage     `json:"job_context,omitempty" shape:"job"`
	PersistentShell json.RawMessage     `json:"persistent_shell,omitempty" shape:"object"`
	MCPGateway      json.RawMessage     `json:"mcp_gateway,omitempty" shape:"object"`
	PluginGateway   json.RawMessage     `json:"plugin_gateway,omitempty" shape:"object"`
	CodingAgent     json.RawMessage     `json:"coding_agent,omitempty" shape:"object"`
}
