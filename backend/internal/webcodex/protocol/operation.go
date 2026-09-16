// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// runner_operation.rs and runner_protocol.rs. See README.md.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	RawShellCommandMaxBytes                     = 16000
	RawShellWireMaxBytes                        = 64 * 1024
	ProcessExecutableMaxBytes                   = 1024
	ProcessArgMaxCount                          = 256
	ProcessArgMaxBytes                          = 8192
	ProcessArgvMaxBytes                         = 16000
	ProcessStdinMaxBytes                        = 64 * 1024
	ProcessCwdMaxBytes                          = 1024
	ScriptMaxBytes                              = 512 * 1024
	StructuredExecutionTimeoutMinSecs           = 1
	StructuredExecutionTimeoutMaxSecs           = 3600
	StructuredExecutionTimeoutDefaultSecs       = 60
	StructuredExecutionDirectSyncTimeoutMaxSecs = 120
)

var ErrUnsupported = errors.New("unsupported Runner operation")
var ErrUnknownKind = errors.New("unknown Runner V2 request kind")

type InvocationMetadata struct {
	RequestID   string
	ClientID    string
	RequestedBy string
	CreatedAt   int64
}
type Invocation struct {
	Metadata  InvocationMetadata
	Operation Operation
}

// Operation is closed to this package. Dispatch with a type switch; there is no
// generic command fallback for typed or unsupported operations.
type Operation interface {
	WireKind() string
	runnerOperation()
}
type ShellOperation struct {
	Cwd         *string
	Command     string
	Stdin       *string
	MaxBytes    *uint64
	TimeoutSecs uint64
}

func (ShellOperation) WireKind() string { return "run_shell" }
func (ShellOperation) runnerOperation() {}

type ProcessOperation struct {
	Cwd         *string
	Process     ShellProcessArgv
	Stdin       *string
	TimeoutSecs uint64
}

func (ProcessOperation) WireKind() string { return "run_process" }
func (ProcessOperation) runnerOperation() {}

type ScriptOperation struct {
	Cwd         *string
	Script      ShellScriptPayload
	Stdin       *string
	TimeoutSecs uint64
}

func (ScriptOperation) WireKind() string { return "run_script" }
func (ScriptOperation) runnerOperation() {}

type FilePayload struct {
	Cwd            *string
	Path           string
	Content        *string
	MaxBytes       *uint64
	ExpectedSHA256 *string
	ExpectedPrefix *string
	StartLine      *uint64
	EndLine        *uint64
	CreateDirs     bool
}
type FileOperation struct {
	kind    string
	Payload FilePayload
}

func (f FileOperation) WireKind() string { return f.kind }
func (FileOperation) runnerOperation()   {}

func present(r json.RawMessage) bool {
	return len(r) > 0 && !bytes.Equal(bytes.TrimSpace(r), []byte("null"))
}
func copyOptional[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func (r RunnerRequest) DecodeInvocation() (Invocation, error) {
	// Match Rust's owned canonical payload: later DTO edits cannot retarget it.
	r.Cwd = copyOptional(r.Cwd)
	r.Stdin = copyOptional(r.Stdin)
	r.Path = copyOptional(r.Path)
	r.Content = copyOptional(r.Content)
	r.MaxBytes = copyOptional(r.MaxBytes)
	r.ExpectedSHA256 = copyOptional(r.ExpectedSHA256)
	r.ExpectedPrefix = copyOptional(r.ExpectedPrefix)
	r.StartLine = copyOptional(r.StartLine)
	r.EndLine = copyOptional(r.EndLine)
	op, err := r.decodeOperation()
	if err != nil {
		return Invocation{}, err
	}
	return Invocation{Metadata: InvocationMetadata{r.RequestID, r.ClientID, r.RequestedBy, r.CreatedAt}, Operation: op}, nil
}
func (r RunnerRequest) decodeOperation() (Operation, error) {
	allowed := ""
	switch r.Kind {
	case "run_process", "start_process_job", "start_detached_process_job":
		allowed = "process"
	case "run_script", "run_internal_posix_script", "start_script_job":
		allowed = "script"
	case "validation", "lsp", "persistent_shell", "mcp_gateway", "plugin_gateway", "coding_agent":
		allowed = r.Kind
	}
	payloads := map[string]bool{"process": r.Process != nil, "script": r.Script != nil, "validation": present(r.Validation), "lsp": present(r.LSP), "persistent_shell": present(r.PersistentShell), "mcp_gateway": present(r.MCPGateway), "plugin_gateway": present(r.PluginGateway), "coding_agent": present(r.CodingAgent)}
	for name, has := range payloads {
		if has && name != allowed {
			return nil, fmt.Errorf("%s contains incompatible typed payload %s", r.Kind, name)
		}
	}
	noFile := r.Path == nil && r.Content == nil && r.ExpectedSHA256 == nil && r.ExpectedPrefix == nil && r.StartLine == nil && r.EndLine == nil && !r.CreateDirs
	switch r.Kind {
	case "run_shell":
		if !noFile || r.JobID != nil {
			return nil, fmt.Errorf("run_shell contains incompatible fields")
		}
		if err := ValidateRawShellWireCommand(r.Command); err != nil {
			return nil, err
		}
		// Session/SSH binding is deferred: its presence must never become local execution.
		if present(r.JobContext) {
			return nil, fmt.Errorf("%w: run_shell job_context", ErrUnsupported)
		}
		if strings.HasPrefix(r.Command, "# webcodex:search_project_text:v1") {
			return nil, fmt.Errorf("%w: external search", ErrUnsupported)
		}
		return ShellOperation{r.Cwd, r.Command, r.Stdin, r.MaxBytes, r.TimeoutSecs}, nil
	case "run_process", "run_script":
		if !noFile || r.MaxBytes != nil || r.JobID != nil || present(r.JobContext) || r.Command != "" {
			return nil, fmt.Errorf("%s contains incompatible execution fields", r.Kind)
		}
		if err := validateStructuredCommon(r.Cwd, r.Stdin, r.TimeoutSecs); err != nil {
			return nil, err
		}
		if r.Kind == "run_process" {
			if r.Process == nil {
				return nil, fmt.Errorf("run_process requires process payload")
			}
			if err := ValidateProcessArgv(*r.Process); err != nil {
				return nil, err
			}
			p := *r.Process
			p.Args = append([]string{}, p.Args...)
			return ProcessOperation{r.Cwd, p, r.Stdin, r.TimeoutSecs}, nil
		}
		if r.Script == nil {
			return nil, fmt.Errorf("run_script requires script payload")
		}
		if err := validateScript(*r.Script); err != nil {
			return nil, err
		}
		s := *r.Script
		s.Args = append([]string{}, s.Args...)
		return ScriptOperation{r.Cwd, s, r.Stdin, r.TimeoutSecs}, nil
	case "file_read", "file_write", "file_list", "file_skill_read_file", "file_skill_list_packages", "file_project_overview":
		if r.JobID != nil || r.Stdin != nil || present(r.JobContext) || r.Command != "" {
			return nil, fmt.Errorf("file operation contains incompatible execution fields")
		}
		if r.Path == nil || *r.Path == "" || strings.ContainsRune(*r.Path, 0) {
			return nil, fmt.Errorf("file operation path is required and cannot contain NUL")
		}
		if r.Cwd != nil && strings.ContainsRune(*r.Cwd, 0) {
			return nil, fmt.Errorf("file operation cwd cannot contain NUL")
		}
		if r.Kind != "file_write" && (r.ExpectedSHA256 != nil || r.ExpectedPrefix != nil || r.CreateDirs) {
			return nil, fmt.Errorf("write-only fields on non-write operation")
		}
		if r.Kind == "file_write" && r.Content == nil {
			return nil, fmt.Errorf("file_write requires content")
		}
		if r.Kind == "file_read" || r.Kind == "file_skill_read_file" {
			if (r.StartLine != nil || r.EndLine != nil) && (r.StartLine == nil || r.EndLine == nil || *r.StartLine == 0 || *r.EndLine < *r.StartLine) {
				return nil, fmt.Errorf("%s line range is invalid", r.Kind)
			}
		} else if r.StartLine != nil || r.EndLine != nil {
			return nil, fmt.Errorf("line range incompatible with file operation")
		}
		return FileOperation{r.Kind, FilePayload{r.Cwd, *r.Path, r.Content, r.MaxBytes, r.ExpectedSHA256, r.ExpectedPrefix, r.StartLine, r.EndLine, r.CreateDirs}}, nil
	}
	if knownDeferredKind(r.Kind) {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, r.Kind)
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownKind, r.Kind)
}
func knownDeferredKind(k string) bool {
	switch k {
	case "run_internal_posix_script", "start_job", "start_validation_job", "start_process_job", "start_detached_process_job", "start_script_job", "stop_job",
		"file_delete_project_files", "file_write_project_file", "file_apply_text_edits", "file_apply_patch", "file_save_project_artifact", "file_read_project_artifact_metadata", "file_read_project_artifact", "file_read_project_artifact_export_chunk", "file_artifact_upload_begin", "file_artifact_upload_chunk", "file_artifact_upload_finish", "file_artifact_upload_abort", "file_checkpoint_create", "file_checkpoint_restore",
		"register_project", "create_project", "resolve_or_register_project", "prepare_managed_worktree", "project_lifecycle_enable", "project_lifecycle_disable", "project_lifecycle_unregister",
		"computer_list_windows", "computer_list_applications", "computer_launch_application", "computer_list_displays", "computer_snapshot_display", "computer_read_clipboard", "computer_write_clipboard", "computer_pointer_move", "computer_pointer_click", "computer_snapshot", "computer_snapshot_region", "computer_accessibility_status", "computer_accessibility_tree", "computer_element_state", "computer_activate_window", "computer_control", "computer_scroll_to_element", "computer_key_input", "computer_input_text",
		"validation", "lsp", "persistent_shell", "mcp_gateway", "plugin_gateway", "coding_agent", "skill_store", "ssh_resource", "runner_config":
		return true
	}
	return false
}
func ValidateRawShellWireCommand(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("command cannot be empty")
	}
	if len(s) > RawShellWireMaxBytes {
		return fmt.Errorf("command exceeds Runner wire envelope of %d bytes", RawShellWireMaxBytes)
	}
	if strings.ContainsRune(s, 0) {
		return fmt.Errorf("command cannot contain NUL")
	}
	return nil
}

// ValidateAuthoredShellCommand is for caller input before any shell wrapper.
// The 64 KiB wire limit applies only to Command, never the full JSON body.
func ValidateAuthoredShellCommand(s string) error {
	if len(s) > RawShellCommandMaxBytes {
		return fmt.Errorf("authored command exceeds %d bytes", RawShellCommandMaxBytes)
	}
	return ValidateRawShellWireCommand(s)
}
func validateArgs(args []string, total int) error {
	if len(args) > ProcessArgMaxCount {
		return fmt.Errorf("too many args")
	}
	for _, a := range args {
		if len(a) > ProcessArgMaxBytes || strings.ContainsRune(a, 0) {
			return fmt.Errorf("invalid or oversized argument")
		}
		total += 1 + len(a)
	}
	if total > ProcessArgvMaxBytes {
		return fmt.Errorf("argv exceeds %d bytes", ProcessArgvMaxBytes)
	}
	return nil
}
func ValidateProcessArgv(p ShellProcessArgv) error {
	if strings.TrimSpace(p.Executable) == "" || len(p.Executable) > ProcessExecutableMaxBytes || strings.ContainsRune(p.Executable, 0) {
		return fmt.Errorf("invalid executable")
	}
	if err := validateArgs(p.Args, len(p.Executable)); err != nil {
		return err
	}
	base := strings.ToLower(p.Executable[strings.LastIndexAny(p.Executable, "/\\")+1:])
	for _, arg := range p.Args {
		a := strings.ToLower(arg)
		switch base {
		case "sh", "sh.exe", "bash", "bash.exe":
			if arg == "--" {
				return nil
			}
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a[1:], "c") {
				return fmt.Errorf("run_process does not accept shell command modes")
			}
		case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
			if a == "-command" || a == "-c" {
				return fmt.Errorf("run_process does not accept shell command modes")
			}
		case "cmd", "cmd.exe":
			if a == "/c" {
				return fmt.Errorf("run_process does not accept shell command modes")
			}
		}
	}
	return nil
}
func validateStructuredCommon(cwd, stdin *string, timeout uint64) error {
	if timeout < 1 || timeout > StructuredExecutionDirectSyncTimeoutMaxSecs {
		return fmt.Errorf("timeout_secs must be between 1 and 120")
	}
	if cwd != nil && (len(*cwd) > ProcessCwdMaxBytes || strings.ContainsRune(*cwd, 0)) {
		return fmt.Errorf("cwd invalid or oversized")
	}
	if stdin != nil && (len(*stdin) > ProcessStdinMaxBytes || strings.ContainsRune(*stdin, 0)) {
		return fmt.Errorf("stdin invalid or oversized")
	}
	return nil
}
func validateScript(s ShellScriptPayload) error {
	switch s.Language {
	case ScriptSh, ScriptBash, ScriptPowershell:
	default:
		return fmt.Errorf("unknown script language")
	}
	if len(s.Script) < 1 || len(s.Script) > ScriptMaxBytes || strings.ContainsRune(s.Script, 0) {
		return fmt.Errorf("script invalid or oversized")
	}
	return validateArgs(s.Args, 0)
}
