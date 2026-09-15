// SPDX-License-Identifier: Apache-2.0
package protocol

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestJobSerdeSequences(t *testing.T) {
	// Independent source-authored minimum positional forms: each unmarked Option
	// still occupies a position, even when the equivalent object omits it.
	cases := []struct {
		value            any
		sequence, object string
	}{
		{&RunnerJobUpdateRequest{}, `["c","i","j",null,null,"running"]`, `{"client_id":"c","agent_instance_id":"i","job_id":"j","status":"running"}`},
		{&RunnerJobUpdateResponse{}, `[true,null,null]`, `{"success":true}`},
		{&ShellJobCodexMetadata{}, `[]`, `{}`},
		{&ShellJobValidationStep{}, `["n","p"]`, `{"name":"n","program":"p"}`},
		{&ShellJobValidationProgress{}, `[0]`, `{"completed":0}`},
		{&ShellJobActivity{}, `[{"working":null},{"process_running":null},{"runner_execution":null}]`, `{"state":"working","phase":"process_running","source":"runner_execution"}`},
		{&ShellJobValidationMetadata{}, `["t","k",[["n","p"]],1,0,"a"]`, `{"tool":"t","kind":"k","steps":[{"name":"n","program":"p"}],"effective_timeout_secs":1,"sync_wait_secs":0,"adapter":"a"}`},
		{&ShellJobStructuredExecutionMetadata{}, `["run_script",{"bash":null},0,1,false]`, `{"execution_source":"run_script","language":"bash","script_bytes":0,"arg_count":1,"stdin_present":false}`},
		{&ShellJobContext{}, `[null,null,null,null,null,null,null,"p"]`, `{"command_preview":"p"}`},
		{&ShellJobStreamSnapshot{}, `[]`, `{}`},
		{&ShellJobLogSnapshot{}, `[[],[]]`, `{"stdout":{},"stderr":{}}`},
		{&ShellJobSnapshot{}, `["j","r","s",0,0,null,null,null,null,null,null,[null,null,null,null,null,null,null,"p"]]`, `{"job_id":"j","request_id":"r","status":"s","update_seq":0,"created_at":0,"context":{"command_preview":"p"}}`},
		{&ShellJobInventory{}, `[]`, `{}`},
		{&RunnerShellJobResult{}, `[null,"p"]`, `{"command_preview":"p"}`},
		{&RunnerJobResult{}, `[]`, `{}`},
		{&ShellJobInfo{}, `["j",null,"c","shell",null,null,null,null,null,null,null,"p","s",0,null,null,null,null,null,null]`, `{"job_id":"j","client_id":"c","command_preview":"p","status":"s","created_at":0}`},
		{&RunnerJobStatusRequest{}, `[null,"j"]`, `{"job_id":"j"}`},
		{&RunnerJobStopRequest{}, `[null,"j"]`, `{"job_id":"j"}`},
		{&RunnerJobLogRequest{}, `[null,"j"]`, `{"job_id":"j"}`},
		{&RunnerJobsListRequest{}, `["c"]`, `{"client_id":"c"}`},
		{&RunnerJobStatusResponse{}, `[true,null,null,null,null,null,null,null,null,null]`, `{"success":true}`},
		{&RunnerJobLogResponse{}, `[true,null,null,null,null,null,null,null,null]`, `{"success":true}`},
		{&RunnerJobStopResponse{}, `[true,null,null,null,null]`, `{"success":true}`},
		{&RunnerJobsListResponse{}, `[true,"c",[],null]`, `{"success":true,"client_id":"c","jobs":[]}`},
	}
	for _, c := range cases {
		t.Run(reflect.TypeOf(c.value).Elem().Name(), func(t *testing.T) {
			if err := json.Unmarshal([]byte(c.sequence), c.value); err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(c.value)
			if err != nil {
				t.Fatal(err)
			}
			wantValue := reflect.New(reflect.TypeOf(c.value).Elem()).Interface()
			if err := json.Unmarshal([]byte(c.object), wantValue); err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(wantValue)
			jobJSONEqual(t, got, want)
			// These are shortest accepted arrays: dropping their last supplied field
			// must fail, including response Options whose object keys may be omitted.
			var elems []json.RawMessage
			_ = json.Unmarshal([]byte(c.sequence), &elems)
			if len(elems) > 0 {
				shorter, _ := json.Marshal(elems[:len(elems)-1])
				if err := json.Unmarshal(shorter, c.value); err == nil {
					t.Fatalf("accepted truncated %s", shorter)
				}
			}
		})
	}
}

func TestJobSerdeOracleAndRawBoundaries(t *testing.T) {
	for _, wire := range []string{`[]`, `[false]`, `[false,[]]`} {
		v, err := ReadJobInventory([]byte(wire))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		jobJSONEqual(t, b, []byte(`{"active_complete":false,"jobs":[]}`))
	}
	wire := `{"client_id":"c","agent_instance_id":"i","job_id":"j","status":"running","activity":[{"working":null},{"process_running":null},"runner_execution"],"log_snapshot":[[],[]],"command_execution_state":{"completed":null}}`
	v, err := ReadJobUpdateRequest([]byte(wire))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	jobJSONEqual(t, b, []byte(`{"client_id":"c","agent_instance_id":"i","job_id":"j","request_id":null,"status":"running","stdout_chunk":null,"stderr_chunk":null,"stdout_tail":null,"stderr_tail":null,"log_snapshot":{"stdout":{"tail":"","first_retained_line":1,"next_line":1,"truncated":false},"stderr":{"tail":"","first_retained_line":1,"next_line":1,"truncated":false}},"exit_code":null,"duration_ms":null,"error":null,"command_execution_state":"completed","activity":{"state":"working","phase":"process_running","source":"runner_execution"},"finished":false}`))
	bad := []struct {
		value any
		wire  string
	}{
		{&ShellJobInventory{}, `[false,[],0]`},
		{&ShellJobStreamSnapshot{}, `["",1,1,false,0]`},
		{&ShellJobActivity{}, `["working","process_running","runner_execution",0]`},
		{&ShellJobActivity{}, `{"state":"working","phase":"process_running","source":"runner_execution","extra":0}`},
		{&RunnerJobUpdateRequest{}, `["c","i","j"]`},
		{&RunnerJobUpdateRequest{}, `["c","i","j","running"]`},
		{&ShellJobStructuredExecutionMetadata{}, `["run_process",0,false]`},
		{&ShellJobValidationStep{}, `["n","p",[null]]`},
		{&ShellJobValidationStep{}, `["n","p",[],[["a",null]]]`},
		{&ShellJobValidationStep{}, `["n","p",[],[["a","b","c"]]]`},
		{&ShellJobInventory{}, `[false,[{"job_id":"j","job_id":"j"}]]`},
		{&ShellJobStreamSnapshot{}, `["",18446744073709551616]`},
		{&ShellJobStreamSnapshot{}, `["",1e0]`},
		{&ShellJobStreamSnapshot{}, `["",-1]`},
		{&ShellJobStreamSnapshot{}, `[null]`},
		{&ShellJobStreamSnapshot{}, `["\ud800"]`},
		{&ShellJobCodexMetadata{}, `[null,null,null,null,null,null,null,null,9223372036854775808]`},
		{&RunnerShellJobResult{}, `[null,"p",2147483648]`},
		{&ShellJobInventory{}, `[false,null]`},
	}
	for _, c := range bad {
		if err := json.Unmarshal([]byte(c.wire), c.value); err == nil {
			t.Fatalf("%T accepted %s", c.value, c.wire)
		}
	}
	var stream ShellJobStreamSnapshot
	if err := json.Unmarshal([]byte(`["",18446744073709551615,0,false]`), &stream); err != nil || stream.FirstRetainedLine != ^uint64(0) {
		t.Fatal(stream, err)
	}
	var codex ShellJobCodexMetadata
	if err := json.Unmarshal([]byte(`[null,null,null,null,null,null,null,null,-9223372036854775808]`), &codex); err != nil || *codex.MaxRuntimeSecs != -1<<63 {
		t.Fatal(codex, err)
	}
}

func TestJobUnitObjects(t *testing.T) {
	for _, c := range []struct {
		value    any
		variants []string
	}{
		{new(ShellJobActivityState), []string{"working", "waiting"}},
		{new(ShellJobActivityPhase), []string{"process_running", "validation_format", "validation_check", "validation_test", "cargo_waiting_for_build_lock", "cargo_compiling", "cargo_checking"}},
		{new(ShellJobActivitySource), []string{"runner_execution", "validation_plan", "cargo_output"}},
	} {
		for _, s := range c.variants {
			if err := json.Unmarshal([]byte(`{"`+s+`":null}`), c.value); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(c.value)
			if string(b) != `"`+s+`"` {
				t.Fatal(string(b))
			}
			for _, bad := range []string{`{"` + s + `":0}`, `{"` + s + `":null,"` + s + `":null}`, `{"` + s + `":null,"other":null}`, `["` + s + `"]`, `{}`, `null`, `{"unknown":null}`} {
				if err := json.Unmarshal([]byte(bad), c.value); err == nil {
					t.Fatalf("accepted %s", bad)
				}
			}
		}
	}
	for _, s := range []string{"not_started", "outcome_unknown", "timed_out", "completed"} {
		_, err := ReadJobUpdateRequest([]byte(`{"client_id":"c","agent_instance_id":"i","job_id":"j","status":"s","command_execution_state":{"` + s + `":null}}`))
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []string{"sh", "bash", "powershell"} {
		var v ShellJobStructuredExecutionMetadata
		if err := json.Unmarshal([]byte(`["run_script",{"`+s+`":null},0,0,false]`), &v); err != nil || string(*v.Language) != s {
			t.Fatal(s, err)
		}
	}
	for _, bad := range []string{`{}`, `{"completed":0}`, `{"completed":null,"completed":null}`, `{"unknown":null}`, `["completed"]`} {
		if _, err := ReadJobUpdateRequest([]byte(`{"client_id":"c","agent_instance_id":"i","job_id":"j","status":"s","command_execution_state":` + bad + `}`)); err == nil {
			t.Fatal(bad)
		}
	}
}

func TestJobCarrierSequenceAndScope(t *testing.T) {
	context := `[null,null,null,null,null,null,null,"p",[],null,["run_process",null,null,0,false]]`
	wire := `{"request_id":"r","client_id":"c","kind":"start_process_job","job_id":"j","command":"","process":{"executable":"go"},"timeout_secs":1,"requested_by":"u","created_at":0,"job_context":` + context + `}`
	inv, err := DecodeJobRequest([]byte(wire))
	if err != nil {
		t.Fatal(err)
	}
	r, err := inv.IntoV2Request()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.JobContext) == 0 || r.JobContext[0] != '{' {
		t.Fatal("context not canonicalized")
	}
	parsed, err := ReadRequest([]byte(wire))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parsed.DecodeInvocation(); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	for _, field := range []string{"validation", "lsp", "persistent_shell", "mcp_gateway", "plugin_gateway", "coding_agent"} {
		nonJob := strings.Replace(wire, `"job_context":`+context, `"`+field+`":[]`, 1)
		if _, err := ReadRequest([]byte(nonJob)); err == nil {
			t.Fatal("widened opaque " + field)
		}
	}
	processSequence := strings.Replace(wire, `"process":{"executable":"go"}`, `"process":["go"]`, 1)
	if _, err := DecodeJobRequest([]byte(processSequence)); err != nil {
		t.Fatalf("Job process sequence rejected: %v", err)
	}
	if _, err := ReadRequest([]byte(processSequence)); err == nil {
		t.Fatal("generic request process widened")
	}
	duplicateProcess := strings.Replace(processSequence, `"process":["go"]`, `"process":["go"],"process":["go"]`, 1)
	if _, err := DecodeJobRequest([]byte(duplicateProcess)); err == nil {
		t.Fatal("Job process duplicate erased")
	}
	if _, err := DecodeJobRequest([]byte(`[]`)); err == nil {
		t.Fatal("Job outer request widened")
	}
	if _, err := ReadRequest([]byte(`[]`)); err == nil {
		t.Fatal("widened outer request")
	}
	var script ShellScriptPayload
	if err := json.Unmarshal([]byte(`{"language":{"bash":null},"script":"x"}`), &script); err == nil {
		t.Fatal("widened non-Job language")
	}
	register := `{"client_id":"c","agent_instance_id":"i","agent_protocol_generation":2,"capabilities":{"shell":true},"job_inventory":[]}`
	reg, err := ReadRegisterRequest([]byte(register))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadJobInventory(reg.JobInventory); err != nil {
		t.Fatal(err)
	}
	if reg.Capabilities.Jobs || reg.Capabilities.AsyncJobs || reg.Capabilities.StructuredExecutionJobs || reg.Capabilities.JobStateReconciliation {
		t.Fatal("Job bits changed")
	}
}
