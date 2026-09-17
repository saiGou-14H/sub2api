// SPDX-License-Identifier: Apache-2.0
// Source: runner_operation.rs at 97ad66949a859174911c2f6da2ff1063be98bfa9.
package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type JobInvocation struct {
	Metadata  InvocationMetadata
	Operation JobOperation
}
type JobOperation interface {
	WireKind() string
	runnerJobOperation()
}
type JobProcessOperation struct {
	JobID       string
	Cwd         *string
	Process     ShellProcessArgv
	Stdin       *string
	TimeoutSecs uint64
	Context     ShellJobContext
}
type JobStopOperation struct{ JobID string }

func (JobProcessOperation) WireKind() string    { return "start_process_job" }
func (JobProcessOperation) runnerJobOperation() {}
func (JobStopOperation) WireKind() string       { return "stop_job" }
func (JobStopOperation) runnerJobOperation()    {}
func (p JobProcessOperation) ExpectedStructuredExecution() ShellJobStructuredExecutionMetadata {
	m := ShellJobStructuredExecutionMetadata{ExecutionSource: "run_process", ArgCount: uint64(len(p.Process.Args)), StdinPresent: p.Stdin != nil}
	if c := p.Context.StructuredExecution; c != nil {
		m.ValidationIdentity = copyOptional(c.ValidationIdentity)
		m.ValidationTool = copyOptional(c.ValidationTool)
		m.AssertionName = copyOptional(c.AssertionName)
	}
	return m
}
func validateJobProcess(p JobProcessOperation) error {
	if err := ValidateProcessArgv(p.Process); err != nil {
		return err
	}
	if p.TimeoutSecs < 1 || p.TimeoutSecs > 3600 {
		return fmt.Errorf("timeout_secs must be between 1 and 3600")
	}
	if p.Cwd != nil && (len(*p.Cwd) > ProcessCwdMaxBytes || strings.ContainsRune(*p.Cwd, 0)) {
		return fmt.Errorf("cwd invalid or oversized")
	}
	if p.Stdin != nil && (len(*p.Stdin) > ProcessStdinMaxBytes || strings.ContainsRune(*p.Stdin, 0)) {
		return fmt.Errorf("stdin invalid or oversized")
	}
	if !reflect.DeepEqual(p.Cwd, p.Context.Cwd) {
		return fmt.Errorf("job recovery context cwd does not match operation cwd")
	}
	if p.Context.SSHResource != nil && p.Context.WorkflowSessionID == nil {
		return fmt.Errorf("job recovery SSH resource requires Workflow Session")
	}
	return nil
}
func (r RunnerRequest) DecodeJobInvocation() (JobInvocation, error) {
	empty := JobInvocation{}
	if r.Kind != "start_process_job" && r.Kind != "stop_job" {
		switch r.Kind {
		case "run_shell", "run_process", "run_script", "file_read", "file_write", "file_list", "file_skill_read_file", "file_skill_list_packages", "file_project_overview", "file_write_project_file", "file_delete_project_files", "file_apply_text_edits", "file_apply_patch":
			return empty, fmt.Errorf("%w: %s", ErrUnsupported, r.Kind)
		}
		if knownDeferredKind(r.Kind) {
			return empty, fmt.Errorf("%w: %s", ErrUnsupported, r.Kind)
		}
		return empty, fmt.Errorf("%w: %s", ErrUnknownKind, r.Kind)
	}
	if r.Path != nil || r.Content != nil || r.MaxBytes != nil || r.ExpectedSHA256 != nil || r.ExpectedPrefix != nil || r.StartLine != nil || r.EndLine != nil || r.CreateDirs {
		return empty, fmt.Errorf("job contains incompatible file fields")
	}
	if r.JobID == nil {
		return empty, fmt.Errorf("job_id required")
	}
	if r.Script != nil || present(r.Validation) || present(r.LSP) || present(r.PersistentShell) || present(r.MCPGateway) || present(r.PluginGateway) || present(r.CodingAgent) {
		return empty, fmt.Errorf("job contains incompatible typed payload")
	}
	meta := InvocationMetadata{r.RequestID, r.ClientID, r.RequestedBy, r.CreatedAt}
	if r.Kind == "stop_job" {
		if r.Process != nil || r.Cwd != nil || r.Stdin != nil || present(r.JobContext) || r.Command != "" {
			return empty, fmt.Errorf("stop_job contains incompatible execution fields")
		}
		return JobInvocation{meta, JobStopOperation{*r.JobID}}, nil
	}
	if r.Process == nil || !present(r.JobContext) || r.Command != "" {
		return empty, fmt.Errorf("process job requires process/context and empty command")
	}
	context, err := ReadJobContext(r.JobContext)
	if err != nil {
		return empty, err
	}
	if context.SSHResource != nil {
		return empty, fmt.Errorf("typed process Job contains SSH resource")
	}
	p := JobProcessOperation{*r.JobID, copyOptional(r.Cwd), *r.Process, copyOptional(r.Stdin), r.TimeoutSecs, context}
	p.Process.Args = append([]string{}, p.Process.Args...)
	if err := validateJobProcess(p); err != nil {
		return empty, err
	}
	expected := p.ExpectedStructuredExecution()
	if !reflect.DeepEqual(context.StructuredExecution, &expected) {
		return empty, fmt.Errorf("process Job recovery metadata does not match typed operation")
	}
	return JobInvocation{meta, p}, nil
}

// IntoV2Request preserves the original encoder's validation asymmetry: encoding
// checks bounds/cwd coherence, while decoding additionally checks SSH absence and
// exact structured metadata equality. It does not invent a decode roundtrip.
func (i JobInvocation) IntoV2Request() (RunnerRequest, error) {
	r := RunnerRequest{RequestID: i.Metadata.RequestID, ClientID: i.Metadata.ClientID, RequestedBy: i.Metadata.RequestedBy, CreatedAt: i.Metadata.CreatedAt}
	switch p := i.Operation.(type) {
	case JobStopOperation:
		r.Kind = p.WireKind()
		r.JobID = copyOptional(&p.JobID)
		r.TimeoutSecs = 1
	case JobProcessOperation:
		if err := validateJobProcess(p); err != nil {
			return RunnerRequest{}, err
		}
		b, err := json.Marshal(p.Context)
		if err != nil {
			return RunnerRequest{}, err
		}
		p.Process.Args = append([]string{}, p.Process.Args...)
		r.Kind = p.WireKind()
		r.JobID = copyOptional(&p.JobID)
		r.Cwd = copyOptional(p.Cwd)
		r.Process = &p.Process
		r.Stdin = copyOptional(p.Stdin)
		r.TimeoutSecs = p.TimeoutSecs
		r.JobContext = b
	default:
		return RunnerRequest{}, fmt.Errorf("%w: Job operation", ErrUnsupported)
	}
	return r, nil
}
func EncodeJobInvocation(i JobInvocation) (RunnerRequest, error) { return i.IntoV2Request() }
func DecodeJobRequest(b []byte) (JobInvocation, error) {
	// Keep alternate process struct forms inside the canonical Job boundary.
	// Generic ReadRequest and synchronous ShellProcessArgv decoding stay unchanged.
	type request RunnerRequest
	type jobProcess ShellProcessArgv
	r := request{Kind: "run_shell"}
	e := decodeObjectFields(b, &r, false, func(raw []byte, out any) error {
		if process, ok := out.(**ShellProcessArgv); ok {
			var value jobProcess
			if err := decodeJob(raw, &value); err != nil {
				return err
			}
			decoded := ShellProcessArgv(value)
			*process = &decoded
			return nil
		}
		return json.Unmarshal(raw, out)
	})
	if e != nil {
		return JobInvocation{}, e
	}
	return RunnerRequest(r).DecodeJobInvocation()
}
