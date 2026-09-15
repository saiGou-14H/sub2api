// SPDX-License-Identifier: Apache-2.0
package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func jobFixtureValue(kind string) any {
	switch kind {
	case "update":
		return &RunnerJobUpdateRequest{}
	case "update_response":
		return &RunnerJobUpdateResponse{}
	case "context":
		return &ShellJobContext{}
	case "info":
		return &ShellJobInfo{}
	case "stream":
		return &ShellJobStreamSnapshot{}
	case "log_snapshot":
		return &ShellJobLogSnapshot{}
	case "snapshot":
		return &ShellJobSnapshot{}
	case "inventory":
		return &ShellJobInventory{}
	case "status_request":
		return &RunnerJobStatusRequest{}
	case "status_response":
		return &RunnerJobStatusResponse{}
	case "log_request":
		return &RunnerJobLogRequest{}
	case "log_response":
		return &RunnerJobLogResponse{}
	case "stop_request":
		return &RunnerJobStopRequest{}
	case "stop_response":
		return &RunnerJobStopResponse{}
	case "list_request":
		return &RunnerJobsListRequest{}
	case "list_response":
		return &RunnerJobsListResponse{}
	}
	panic(kind)
}
func jobJSONEqual(t *testing.T, got []byte, want []byte) {
	t.Helper()
	decode := func(b []byte) any {
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		var v any
		if e := d.Decode(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	if !reflect.DeepEqual(decode(got), decode(want)) {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}
func TestSharedJobFixtures(t *testing.T) {
	var f struct {
		SchemaVersion int    `json:"schema_version"`
		SourceCommit  string `json:"source_commit"`
		Cases         []struct {
			Name, Type, Wire string
			Expected         json.RawMessage
			Error            bool
		}
		Operations []struct {
			Name, Wire string
			Expected   json.RawMessage
			Error      bool
		}
	}
	b, e := os.ReadFile("testdata/jobs.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	if f.SchemaVersion != 1 || f.SourceCommit != "97ad66949a859174911c2f6da2ff1063be98bfa9" {
		t.Fatal("fixture identity")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			v := jobFixtureValue(c.Type)
			e := json.Unmarshal([]byte(c.Wire), v)
			if c.Error {
				if e == nil {
					t.Fatal("accepted invalid fixture")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			b, e := json.Marshal(v)
			if e != nil {
				t.Fatal(e)
			}
			jobJSONEqual(t, b, c.Expected)
		})
	}
	for _, c := range f.Operations {
		t.Run(c.Name, func(t *testing.T) {
			i, e := DecodeJobRequest([]byte(c.Wire))
			if c.Error {
				if e == nil {
					t.Fatal("accepted invalid operation")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			r, e := i.IntoV2Request()
			if e != nil {
				t.Fatal(e)
			}
			b, e := json.Marshal(r)
			if e != nil {
				t.Fatal(e)
			}
			jobJSONEqual(t, b, c.Expected)
			if _, e = r.DecodeInvocation(); e == nil {
				t.Fatal("sync decoder accepted job")
			}
		})
	}
}
func TestJobReachableFields(t *testing.T) {
	wire := `{"job_id":"j","request_id":"r","client_id":"c","kind":"shell","project_id":"p","session_id":"s","ssh_resource":"ssh","cwd":"/p","project_cwd":"/","purpose":"purpose","shell":"sh","command_preview":"go","status":"anything","created_at":-9223372036854775808,"started_at":9223372036854775807,"ended_at":0,"exit_code":2147483647,"duration_ms":18446744073709551615,"elapsed_secs":0,"error":"","command_execution_state":"completed","structured_execution":{"execution_source":"run_process","language":"bash","script_bytes":0,"arg_count":18446744073709551615,"stdin_present":true,"validation_identity":"arbitrary","assertion_name":"label","validation_tool":"tool"},"codex":{"project":"p","goal_id":"g","client_request_id":"r","command":"c","kind":"k","suite":"s","script_path":"x","reason":"r","max_runtime_secs":-9223372036854775808},"result":{"shell":{"cwd":null,"command_preview":"p","exit_code":null,"duration_ms":null,"error":null}},"validation_progress":{"completed":18446744073709551615,"current_step":"c","failed_step":"f"},"activity":{"state":"waiting","phase":"process_running","source":"cargo_output"},"validation":{"tool":"custom","kind":"k","steps":[{"name":"n","program":"p","args":["a"],"env":[["KEY","VALUE"]]}],"effective_timeout_secs":18446744073709551615,"sync_wait_secs":0,"adapter":"a","validation_target_id":"v","minimum_tests":0,"require_tests":false,"no_run":false},"recovery_state":"r","recovered_after_server_restart":true,"reconciled_at":-1,"recovery_reason_code":"r","observation_token":"t","last_update_seq":18446744073709551615,"stdout_retained_from_line":0,"stderr_retained_from_line":0,"stdout_log_truncated":true,"stderr_log_truncated":true}`
	var v ShellJobInfo
	if e := json.Unmarshal([]byte(wire), &v); e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	jobJSONEqual(t, b, []byte(wire))
	// Serde accepts advisory combinations and unvalidated metadata; semantic
	// validators must never silently narrow DTO deserialization.
	if v.Activity.IsCanonical() {
		t.Fatal("invalid advisory combination")
	}
	ctx := `{"runtime_project_id":"p","workflow_session_id":"s","ssh_resource":"ssh","project_cwd":"/","cwd":"/p","purpose":"p","shell":"sh","command_preview":"c","validation_steps":["a"]}`
	var c ShellJobContext
	if e := json.Unmarshal([]byte(ctx), &c); e != nil {
		t.Fatal(e)
	}
	b, _ = json.Marshal(c)
	jobJSONEqual(t, b, []byte(ctx))
}
func TestJobNestedInvalid(t *testing.T) {
	for _, wire := range []string{
		`{"name":"n","program":"p","args":[null]}`,
		`{"name":"n","program":"p","env":[["x"]]}`,
		`{"name":"n","program":"p","env":[["x","y","z"]]}`,
		`{"name":"n","program":"p","env":[[null,"y"]]}`,
		`{"name":"n","name":"n","program":"p"}`,
	} {
		var v ShellJobValidationStep
		if e := json.Unmarshal([]byte(wire), &v); e == nil {
			t.Fatalf("accepted %s", wire)
		}
	}
	for _, field := range []string{"state", "phase", "source"} {
		wire := `{"state":"working","phase":"process_running","source":"runner_execution"}`
		var obj map[string]string
		_ = json.Unmarshal([]byte(wire), &obj)
		obj[field] = "future"
		b, _ := json.Marshal(obj)
		var a ShellJobActivity
		if e := json.Unmarshal(b, &a); e == nil {
			t.Fatal(field)
		}
	}
}
func TestJobLifecycle(t *testing.T) {
	all := []RunnerJobLifecycle{JobQueued, JobRunnerQueued, JobStartedLegacy, JobRunning, JobStopRequested, JobCompleted, JobFailed, JobStopped, JobTimeout, JobTimedOut, JobLost, JobCancelled}
	for i, v := range all {
		p, e := ParseRunnerJobLifecycle(v.AsWire())
		if e != nil || p != v {
			t.Fatal(v, e)
		}
		if v.IsTerminal() != (i >= 5) || v.IsActive() != (i < 5) || v.IsRunnerActive() != (i == 1 || i == 3 || i == 4) || v.IsTimedOut() != (i == 8 || i == 9) {
			t.Fatal(v)
		}
	}
	for _, s := range []string{"recovering", "running ", "RUNNING", ""} {
		if _, e := ParseRunnerJobLifecycle(s); e == nil {
			t.Fatal(s)
		}
	}
	if _, e := ParseRunnerJobLifecycle("\u2003running\n"); e == nil {
		t.Fatal("accepted whitespace-padded lifecycle")
	}
}
func TestJobOperationRulesAndEncoderAsymmetry(t *testing.T) {
	p := JobProcessOperation{JobID: "", Process: ShellProcessArgv{Executable: "go"}, TimeoutSecs: 1, Context: ShellJobContext{CommandPreview: "p"}}
	m := p.ExpectedStructuredExecution()
	p.Context.StructuredExecution = &m
	valid := func(p JobProcessOperation) RunnerRequest {
		r, e := (JobInvocation{Operation: p}).IntoV2Request()
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	r := valid(p)
	if _, e := r.DecodeJobInvocation(); e != nil {
		t.Fatal(e)
	}
	for _, n := range []uint64{0, 3601, ^uint64(0)} {
		q := p
		q.TimeoutSecs = n
		if _, e := (JobInvocation{Operation: q}).IntoV2Request(); e == nil {
			t.Fatal(n)
		}
	}
	for _, mutate := range []func(*RunnerRequest){func(r *RunnerRequest) { r.Command = "x" }, func(r *RunnerRequest) { r.JobID = nil }, func(r *RunnerRequest) { r.MaxBytes = new(uint64) }, func(r *RunnerRequest) { r.Validation = json.RawMessage(`{}`) }, func(r *RunnerRequest) { r.Script = &ShellScriptPayload{} }, func(r *RunnerRequest) { r.Cwd = new(string) }, func(r *RunnerRequest) { r.Stdin = new(string) }} {
		q := r
		mutate(&q)
		if _, e := q.DecodeJobInvocation(); e == nil {
			t.Fatal("accepted incompatible job")
		}
	}
	q := p
	q.Context.StructuredExecution = nil
	encoded := valid(q)
	if _, e := encoded.DecodeJobInvocation(); e == nil {
		t.Fatal("metadata mismatch")
	}
	ssh := "ssh"
	session := "s"
	q = p
	q.Context.SSHResource = &ssh
	q.Context.WorkflowSessionID = &session
	encoded = valid(q)
	if _, e := encoded.DecodeJobInvocation(); e == nil {
		t.Fatal("SSH accepted")
	}
	q = p
	q.Process = ShellProcessArgv{Executable: "sh", Args: []string{"-c", "echo hi"}}
	if _, e := (JobInvocation{Operation: q}).IntoV2Request(); e == nil {
		t.Fatal("shell smuggling")
	}
	q = p
	stdin := strings.Repeat("x", ProcessStdinMaxBytes+1)
	q.Stdin = &stdin
	if _, e := (JobInvocation{Operation: q}).IntoV2Request(); e == nil {
		t.Fatal("stdin bound")
	}
}
