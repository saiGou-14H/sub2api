// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9
// runner-registry/{reconciliation,job_updates,jobs}.rs. Known process Jobs only.
package runner

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

// Frozen protocol limits, not the Server's retained Job capacity.
const (
	jobInventoryMaxActive     = 64
	jobInventoryMaxTerminal   = 64
	jobInventoryMaxBytes      = 1024 * 1024
	jobSnapshotStreamMaxBytes = 64 * 1024
)

func validateStreamSnapshot(s protocol.ShellJobStreamSnapshot) error {
	if !utf8.ValidString(s.Tail) || len(s.Tail) > jobSnapshotStreamMaxBytes {
		return fmt.Errorf("job snapshot stream exceeds UTF-8 byte budget")
	}
	if s.FirstRetainedLine == 0 || s.NextLine != jobLogSaturatingAdd(s.FirstRetainedLine, jobLogLineCount(s.Tail)) || (s.FirstRetainedLine > 1 && !s.Truncated) {
		return fmt.Errorf("job snapshot stream line range is inconsistent")
	}
	return nil
}
func logFromSnapshot(s protocol.ShellJobStreamSnapshot) jobLogState {
	return jobLogState{tail: s.Tail, firstRetainedLine: s.FirstRetainedLine, nextLine: s.NextLine, truncated: s.Truncated}
}
func validateSequencedUpdate(u protocol.RunnerJobUpdateRequest, state protocol.RunnerJobLifecycle, sequenced bool) error {
	if u.LogSnapshot != nil {
		if !sequenced {
			return fmt.Errorf("%w: log_snapshot requires job_state_reconciliation", protocol.ErrUnsupported)
		}
		if u.StdoutChunk != nil || u.StderrChunk != nil || u.StdoutTail != nil || u.StderrTail != nil {
			return fmt.Errorf("log_snapshot cannot be combined with chunk or tail fields")
		}
		if err := validateStreamSnapshot(u.LogSnapshot.Stdout); err != nil {
			return err
		}
		if err := validateStreamSnapshot(u.LogSnapshot.Stderr); err != nil {
			return err
		}
	}
	if !sequenced {
		return nil
	}
	if u.UpdateSeq == nil || *u.UpdateSeq == 0 {
		return fmt.Errorf("job_state_reconciliation requires positive update_seq")
	}
	if u.Status != strings.TrimSpace(u.Status) || (!state.IsRunnerActive() && !state.IsTerminal()) {
		return fmt.Errorf("job_state_reconciliation requires canonical Runner-owned status")
	}
	if u.Finished != state.IsTerminal() {
		return fmt.Errorf("job update finished/status is inconsistent")
	}
	if !state.IsTerminal() && (u.ExitCode != nil || u.DurationMS != nil) {
		return fmt.Errorf("active job update contains terminal result fields")
	}
	if state == protocol.JobCompleted && (u.ExitCode == nil || *u.ExitCode != 0) {
		return fmt.Errorf("completed job update requires exit_code=0")
	}
	return nil
}

func (r *Registry) registrationInventory(body protocol.RunnerRegisterRequest) (*protocol.ShellJobInventory, error) {
	if !body.Capabilities.JobStateReconciliation {
		if present(body.JobInventory) {
			return nil, fmt.Errorf("%w: inventory requires job_state_reconciliation", protocol.ErrUnsupported)
		}
		return nil, nil
	}
	if r.options.JobRecoveryGrace <= 0 || r.options.MaxJobsPerRunner <= 0 {
		return nil, fmt.Errorf("%w: reconciliation recovery grace and Job capacity must be configured", protocol.ErrUnsupported)
	}
	if !present(body.JobInventory) {
		return nil, fmt.Errorf("job_state_reconciliation requires active_complete job inventory")
	}
	inv, err := protocol.ReadJobInventory(body.JobInventory)
	if err != nil {
		return nil, err
	}
	if !inv.ActiveComplete || len(inv.Jobs) > jobInventoryMaxActive+jobInventoryMaxTerminal {
		return nil, fmt.Errorf("job inventory requires active_complete and at most 128 records")
	}
	wire, err := json.Marshal(inv)
	if err != nil {
		return nil, err
	}
	if rustJSONSerializedBytes(wire) > jobInventoryMaxBytes {
		return nil, fmt.Errorf("job inventory exceeds serialized byte budget")
	}
	ids, requests := make(map[string]bool, len(inv.Jobs)), make(map[string]bool, len(inv.Jobs))
	active, terminal := 0, 0
	for _, s := range inv.Jobs {
		if ids[s.JobID] || requests[s.RequestID] {
			return nil, fmt.Errorf("job inventory duplicate job_id or request_id")
		}
		ids[s.JobID], requests[s.RequestID] = true, true
		if err := validateInventorySnapshot(body.ClientID, s); err != nil {
			return nil, err
		}
		state, _ := protocol.ParseRunnerJobLifecycle(s.Status)
		if state.IsRunnerActive() {
			if terminal > 0 {
				return nil, fmt.Errorf("job inventory active records must precede terminal history")
			}
			active++
		} else {
			terminal++
		}
	}
	if active > jobInventoryMaxActive || terminal > jobInventoryMaxTerminal {
		return nil, fmt.Errorf("job inventory exceeds active or terminal record budget")
	}
	return &inv, nil
}

// serde_json writes HTML characters and U+2028/U+2029 literally. Go's owned
// typed DTO MarshalJSON escapes them even inside a SetEscapeHTML(false) encoder.
// Count the source encoding without changing transport JSON or interpreting a
// literal backslash-u string as an escape. Other JSON escapes have equal sizes.
func rustJSONSerializedBytes(wire []byte) int {
	size := len(wire)
	for i := 0; i < len(wire); i++ {
		if wire[i] != '\\' {
			continue
		}
		if i+5 < len(wire) && wire[i+1] == 'u' {
			switch string(wire[i+2 : i+6]) {
			case "003c", "003e", "0026":
				size -= 5
			case "2028", "2029":
				size -= 3
			}
		}
		i++ // skip the escaped character, including a literal escaped backslash
	}
	return size
}
func boundedSnapshotText(s string, n int) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0) && utf8.RuneCountInString(s) <= n
}
func validateInventorySnapshot(client string, s protocol.ShellJobSnapshot) error {
	for _, id := range []string{s.JobID, s.RequestID} {
		if err := validateID(id, 80, true); err != nil {
			return err
		}
	}
	state, err := protocol.ParseRunnerJobLifecycle(s.Status)
	if err != nil || s.UpdateSeq == 0 || (!state.IsRunnerActive() && !state.IsTerminal()) {
		return fmt.Errorf("job inventory invalid lifecycle or sequence")
	}
	if s.CreatedAt <= 0 || (s.StartedAt != nil && *s.StartedAt < s.CreatedAt) || (s.EndedAt != nil && (*s.EndedAt < s.CreatedAt || (s.StartedAt != nil && *s.EndedAt < *s.StartedAt))) {
		return fmt.Errorf("job inventory timestamps are inconsistent")
	}
	if state.IsTerminal() != (s.EndedAt != nil) || ((state == protocol.JobRunning || state == protocol.JobStopRequested) && s.StartedAt == nil) {
		return fmt.Errorf("job inventory lifecycle timestamps are inconsistent")
	}
	u := protocol.RunnerJobUpdateRequest{Status: s.Status, UpdateSeq: &s.UpdateSeq, Finished: state.IsTerminal(), ExitCode: s.ExitCode, DurationMS: s.DurationMS, CommandExecutionState: s.CommandExecutionState, Activity: s.Activity, ValidationProgress: s.ValidationProgress, LogSnapshot: &protocol.ShellJobLogSnapshot{Stdout: s.Stdout, Stderr: s.Stderr}}
	if err := validateSequencedUpdate(u, state, true); err != nil {
		return err
	}
	c := s.Context
	if c.Validation != nil || len(c.ValidationSteps) != 0 || c.SSHResource != nil {
		return fmt.Errorf("%w: inventory validation or SSH Job", protocol.ErrUnsupported)
	}
	if c.StructuredExecution == nil || c.StructuredExecution.ExecutionSource != "run_process" {
		return fmt.Errorf("%w: inventory must describe a structured process Job", protocol.ErrUnsupported)
	}
	if !validProcessSnapshotMetadata(*c.StructuredExecution) {
		return fmt.Errorf("job inventory structured execution metadata is invalid")
	}
	if c.CommandPreview != jobCommandPreview(c.CommandPreview) {
		return fmt.Errorf("job inventory command_preview is not canonical")
	}
	for _, text := range []*string{c.Cwd, c.ProjectCwd, c.Purpose, c.Shell} {
		if text != nil && !boundedSnapshotText(*text, 1024) {
			return fmt.Errorf("job inventory context is invalid or oversized")
		}
	}
	if c.Purpose != nil && !oneOf(*c.Purpose, "validation", "test", "build", "format", "release", "diagnostic", "operation", "other") {
		return fmt.Errorf("job inventory purpose is invalid")
	}
	if c.Shell != nil && !oneOf(*c.Shell, "sh", "bash", "powershell", "direct_argv", "configured", "custom", "remote") {
		return fmt.Errorf("job inventory shell is invalid")
	}
	if c.WorkflowSessionID != nil {
		return fmt.Errorf("%w: workflow inventory recovery", protocol.ErrUnsupported)
	}
	if c.RuntimeProjectID != nil {
		prefix := "agent:" + client + ":"
		if !strings.HasPrefix(*c.RuntimeProjectID, prefix) {
			return fmt.Errorf("job inventory project does not belong to client")
		}
		if err := validateID(strings.TrimPrefix(*c.RuntimeProjectID, prefix), 80, true); err != nil {
			return err
		}
	}
	if s.Error != nil && !boundedSnapshotText(*s.Error, 4096) {
		return fmt.Errorf("job inventory error is invalid or oversized")
	}
	fake := &jobRecord{info: protocol.ShellJobInfo{StartedAt: s.StartedAt}}
	if violation := jobUpdateViolation(fake, u, state); violation != "" {
		return fmt.Errorf("job inventory %s", violation)
	}
	return nil
}
func jobCommandPreview(value string) string {
	lines := jobLogLines(value)
	first := ""
	if len(lines) > 0 {
		first = strings.TrimSpace(lines[0])
	}
	if jobPreviewSecretLike(first) {
		return "[redacted]"
	}
	chars := []rune(first)
	if len(chars) > 120 {
		return string(chars[:120]) + "…"
	}
	return first
}
func validProcessSnapshotMetadata(m protocol.ShellJobStructuredExecutionMetadata) bool {
	if m.ExecutionSource != "run_process" || m.Language != nil || m.ScriptBytes != nil || m.ArgCount > protocol.ProcessArgMaxCount {
		return false
	}
	if m.ValidationIdentity != nil {
		identity := *m.ValidationIdentity
		suffix := ""
		for _, prefix := range []string{"target:", "command:", "assertion:"} {
			if strings.HasPrefix(identity, prefix) {
				suffix = strings.TrimPrefix(identity, prefix)
				break
			}
		}
		if len(suffix) != 24 {
			return false
		}
		for _, c := range suffix {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	if m.ValidationTool != nil && (!oneOf(*m.ValidationTool, "cargo_fmt", "cargo_check", "cargo_test") || m.ValidationIdentity == nil || (!strings.HasPrefix(*m.ValidationIdentity, "target:") && !strings.HasPrefix(*m.ValidationIdentity, "assertion:"))) {
		return false
	}
	if m.AssertionName != nil {
		s := *m.AssertionName
		if strings.TrimSpace(s) != s || s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > 120 || strings.IndexFunc(s, unicode.IsControl) >= 0 || m.ValidationIdentity == nil || !strings.HasPrefix(*m.ValidationIdentity, "assertion:") {
			return false
		}
	}
	return true
}
func oneOf(v string, values ...string) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}

func sameSnapshotContext(j *jobRecord, s protocol.ShellJobSnapshot) bool {
	c := s.Context
	return j.info.RequestID != nil && *j.info.RequestID == s.RequestID && reflect.DeepEqual(j.info.ProjectID, c.RuntimeProjectID) && reflect.DeepEqual(j.info.SessionID, c.WorkflowSessionID) && reflect.DeepEqual(j.info.SSHResource, c.SSHResource) && reflect.DeepEqual(j.info.Cwd, c.Cwd) && reflect.DeepEqual(j.info.ProjectCwd, c.ProjectCwd) && reflect.DeepEqual(j.info.Purpose, c.Purpose) && reflect.DeepEqual(j.info.Shell, c.Shell) && j.info.CommandPreview == c.CommandPreview && reflect.DeepEqual(j.info.StructuredExecution, c.StructuredExecution)
}

// Pure preflight: no liveness, registration, sweep, pending or Job mutation.
func (r *Registry) preflightInventoryLocked(access Access, body protocol.RunnerRegisterRequest, inv protocol.ShellJobInventory) error {
	for _, s := range inv.Jobs {
		j := r.jobs[s.JobID]
		if j == nil {
			return fmt.Errorf("%w: unknown inventory Job has no trusted Server projection", protocol.ErrUnsupported)
		}
		owner := ""
		if body.Owner != nil {
			owner = *body.Owner
		}
		if j.info.ClientID != body.ClientID || j.instanceID != body.AgentInstanceID || j.owner != owner || j.groupKind != access.GroupKind || j.groupID != access.GroupID {
			return ErrForbidden
		}
		if !sameSnapshotContext(j, s) {
			return fmt.Errorf("job inventory has inconsistent ownership context")
		}
		if id := r.requestToJob[s.RequestID]; id != "" && id != s.JobID {
			return fmt.Errorf("job inventory request belongs to another Job")
		}
		if !j.dispatched {
			return fmt.Errorf("job inventory Job has not been dispatched")
		}
		if !jobLifecycle(j).IsTerminal() && (j.info.LastUpdateSeq == nil || s.UpdateSeq > *j.info.LastUpdateSeq || (s.UpdateSeq == *j.info.LastUpdateSeq && j.recoveringSince != nil)) {
			if s.Stdout.NextLine < j.stdout.nextLine || s.Stderr.NextLine < j.stderr.nextLine {
				return fmt.Errorf("job inventory regresses an absolute log cursor")
			}
		}
	}
	return nil
}
func (r *Registry) reconcileInventoryLocked(body protocol.RunnerRegisterRequest, inv protocol.ShellJobInventory, now time.Time) {
	ids := make(map[string]bool, len(inv.Jobs))
	for _, s := range inv.Jobs {
		ids[s.JobID] = true
	}
	for _, j := range r.jobs {
		if j.info.ClientID != body.ClientID || j.instanceID != body.AgentInstanceID {
			continue
		}
		if j.recoveringSince != nil && now.Sub(*j.recoveringSince) >= r.options.JobRecoveryGrace {
			r.loseJobLocked(j, "runner_recovery_deadline_exceeded", "runner did not reconcile before recovery deadline", now)
		}
		if jobLifecycle(j).IsRunnerActive() && !ids[j.info.JobID] {
			r.loseJobLocked(j, "runner_inventory_missing", "runner complete active inventory did not contain this job", now)
		}
	}
	for _, s := range inv.Jobs {
		j := r.jobs[s.JobID]
		if jobLifecycle(j).IsTerminal() || (j.info.LastUpdateSeq != nil && (s.UpdateSeq < *j.info.LastUpdateSeq || (s.UpdateSeq == *j.info.LastUpdateSeq && j.recoveringSince == nil))) {
			continue
		}
		state, _ := protocol.ParseRunnerJobLifecycle(s.Status)
		if state.IsRunnerActive() {
			j.stopRegistered = state == protocol.JobStopRequested
		}
		if state.IsTerminal() {
			r.finishJobLocked(j, state, "", now)
		} else {
			j.info.Status = s.Status
		}
		j.info.StartedAt, j.info.EndedAt = s.StartedAt, s.EndedAt
		j.info.ExitCode, j.info.DurationMS, j.info.Error = s.ExitCode, s.DurationMS, s.Error
		j.info.CommandExecutionState, j.info.Activity = s.CommandExecutionState, s.Activity
		j.stdout, j.stderr = logFromSnapshot(s.Stdout), logFromSnapshot(s.Stderr)
		j.info.LastUpdateSeq = jobPtr(s.UpdateSeq)
		j.reconciled(now, "same_instance_reconciliation")
		j.changed()
	}
}
func (j *jobRecord) reconciled(now time.Time, reason string) {
	j.stopRecoveryTimer()
	j.recoveringSince = nil
	j.info.RecoveryState = jobPtr("reconciled")
	j.info.ReconciledAt = jobPtr(now.Unix())
	j.info.RecoveryReasonCode = &reason
}
func (j *jobRecord) stopRecoveryTimer() {
	if j.recoveryTimer != nil {
		j.recoveryTimer.Stop()
		j.recoveryTimer = nil
	}
}

// One bounded deadline timer per recovering Job, owned and cancelled by Registry.
// It never resets on heartbeat/repeated disconnect and needs no caller to poll.
func (r *Registry) beginRecoveryLocked(j *jobRecord, now time.Time, reason string) {
	if jobLifecycle(j).IsTerminal() || !j.dispatched {
		return
	}
	if j.recoveringSince != nil {
		return
	}
	j.recoveringSince = jobPtr(now)
	j.recoveryTimer = time.AfterFunc(r.options.JobRecoveryGrace, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.closed || r.jobs[j.info.JobID] != j || j.recoveringSince == nil || jobLifecycle(j).IsTerminal() {
			return
		}
		if time.Since(*j.recoveringSince) >= r.options.JobRecoveryGrace {
			r.loseJobLocked(j, "runner_recovery_deadline_exceeded", "runner did not reconcile before recovery deadline", time.Now())
		}
	})
	j.info.RecoveryState = jobPtr("recovering")
	j.info.RecoveryReasonCode = &reason
	j.info.Activity = nil
	j.changed()
}
