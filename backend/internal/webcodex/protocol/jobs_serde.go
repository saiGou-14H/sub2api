// SPDX-License-Identifier: Apache-2.0
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// jobSequenceFields records Rust declaration order at frozen WebCodex
// 97ad66949a859174911c2f6da2ff1063be98bfa9, runner_protocol.rs. A '+' marks an
// explicit #[serde(default)] (including named defaults); an unmarked Option is
// sequence-required even though its object member may be absent. Serialization
// skip annotations do not change sequence positions. jobs.go retains the source
// numeric types and skip-None/skip-empty rules. No struct here uses flatten.
var jobSequenceFields = map[string]string{
	// 1511–1515: scoped to canonical Job process decoding, not general requests.
	"jobProcess": "executable +args",
	// 1791–1824: optional request fields all explicitly default.
	"jobStatusRequest": "+client_id job_id",
	"jobStopRequest":   "+client_id job_id",
	"jobLogRequest":    "+client_id job_id +tail_lines +since_stdout_line +since_stderr_line",
	"jobListRequest":   "client_id +status +limit",
	// 2075–2128: update's middle defaults cannot shift status; response Options
	// have skip_serializing_if but no default.
	"jobUpdate":         "client_id agent_instance_id job_id +request_id +update_seq status +stdout_chunk +stderr_chunk +stdout_tail +stderr_tail +log_snapshot +exit_code +duration_ms +error +command_execution_state +validation_progress +activity +finished",
	"jobUpdateResponse": "success job error",
	// 2188–2207, 2235–2246: Codex all default; step args/env default.
	"jobCodex": "+project +goal_id +client_request_id +command +kind +suite +script_path +reason +max_runtime_secs",
	"jobStep":  "name program +args +env",
	// 2537–2581: progress's Options default; Activity has deny_unknown_fields
	// and three non-default enums (snake_case externally tagged unit variants).
	"jobProgress": "completed +current_step +failed_step",
	"jobActivity": "state phase source",
	// 2623–2647, 2702–2722, 2790–2814: all Options explicitly default.
	"jobValidation": "tool kind steps effective_timeout_secs sync_wait_secs adapter +validation_target_id +minimum_tests +require_tests +no_run",
	"jobStructured": "execution_source +language +script_bytes arg_count stdin_present +validation_identity +assertion_name +validation_tool",
	"jobContext":    "+runtime_project_id +workflow_session_id +ssh_resource +project_cwd +cwd +purpose +shell command_preview +validation_steps +validation +structured_execution",
	// 2821–2919: stream line defaults are 1 (also inside defaulted snapshot
	// streams); log snapshot's two streams have no default.
	"jobStream":      "+tail +first_retained_line +next_line +truncated",
	"jobLogSnapshot": "stdout stderr",
	"jobSnapshot":    "job_id request_id status update_seq created_at +started_at +ended_at +exit_code +duration_ms +error +command_execution_state context +stdout +stderr +validation_progress +activity",
	"jobInventory":   "+active_complete +jobs",
	"jobShellResult": "+cwd command_preview +exit_code +duration_ms +error",
	"jobResult":      "+shell",
	// 2922–2993: request_id/cwd/started_at/ended_at/exit_code/duration_ms/error
	// are non-default Options. kind has named default "shell".
	"jobInfo": "job_id request_id client_id +kind +project_id +session_id +ssh_resource cwd +project_cwd +purpose +shell command_preview status created_at started_at ended_at exit_code duration_ms +elapsed_secs error +command_execution_state +structured_execution +codex +result +validation_progress +activity +validation +recovery_state +recovered_after_server_restart +reconciled_at +recovery_reason_code +observation_token +last_update_seq +stdout_retained_from_line +stderr_retained_from_line +stdout_log_truncated +stderr_log_truncated",
	// 3016–3079: only result/job explicitly default; the remaining Options
	// require positions in a sequence, including the final error field.
	"jobStatusResponse": "success job_id client_id kind status elapsed_secs exit_code +result +job error",
	"jobLogResponse":    "success job_id client_id stdout_tail stderr_tail next_stdout_line next_stderr_line +job error",
	"jobStopResponse":   "success job_id status +job error",
	"jobListResponse":   "success client_id jobs error",
}

// jobSequenceObject maps positions to raw members, without decoding through any
// float/map intermediary. Nested duplicate keys and integer lexemes survive.
func jobSequenceObject(b []byte, name string) ([]byte, error) {
	if err := validateJSONUnicode(b); err != nil {
		return nil, err
	}
	fields, ok := jobSequenceFields[name]
	if !ok {
		return nil, fmt.Errorf("missing Job sequence schema %s", name)
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(b, &elems); err != nil {
		return nil, err
	}
	order := strings.Fields(fields)
	if len(elems) > len(order) {
		return nil, fmt.Errorf("too many %s sequence elements", name)
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for i, field := range order {
		if i >= len(elems) {
			if !strings.HasPrefix(field, "+") {
				return nil, fmt.Errorf("missing sequence field %s", field)
			}
			continue
		}
		if i > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(strings.TrimPrefix(field, "+"))
		out.Write(key)
		out.WriteByte(':')
		out.Write(elems[i])
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// These enums are shared with the synchronous DTOs, whose prior string-only
// decoder stays unchanged. Source runner_protocol.rs:1521–1527 uses lowercase
// for ScriptLanguage; 1939–1946 uses snake_case for CommandExecutionState.
// Both are externally tagged unit enums; Job accepts their variants locally.
func decodeJobField(b []byte, out any) error {
	switch x := out.(type) {
	case **ShellScriptLanguage:
		var value ShellScriptLanguage
		if err := jobEnum(b, &value, ScriptSh, ScriptBash, ScriptPowershell); err != nil {
			return err
		}
		*x = &value
		return nil
	case **ShellCommandExecutionState:
		var value ShellCommandExecutionState
		if err := jobEnum(b, &value, CommandNotStarted, CommandOutcomeUnknown, CommandTimedOut, CommandCompleted); err != nil {
			return err
		}
		*x = &value
		return nil
	}
	return json.Unmarshal(b, out)
}
