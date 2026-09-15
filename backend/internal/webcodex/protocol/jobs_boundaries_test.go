// SPDX-License-Identifier: Apache-2.0
package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestJobKindErrorClassification(t *testing.T) {
	for _, kind := range []string{"run_shell", "run_process", "run_script", "file_read", "file_write", "file_list", "start_script_job", "start_detached_process_job", "validation"} {
		_, err := (RunnerRequest{Kind: kind}).DecodeJobInvocation()
		if !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrUnknownKind) {
			t.Fatalf("known kind %q: %v", kind, err)
		}
	}
	for _, kind := range []string{"background_job", "", "start_process_job "} {
		_, err := (RunnerRequest{Kind: kind}).DecodeJobInvocation()
		if !errors.Is(err, ErrUnknownKind) || errors.Is(err, ErrUnsupported) {
			t.Fatalf("unknown kind %q: %v", kind, err)
		}
	}
}

func TestJobActivityEnumMatrix(t *testing.T) {
	states := []ShellJobActivityState{ActivityWorking, ActivityWaiting}
	phases := []ShellJobActivityPhase{ActivityProcessRunning, ActivityValidationFormat, ActivityValidationCheck, ActivityValidationTest, ActivityCargoWaitingForBuildLock, ActivityCargoCompiling, ActivityCargoChecking}
	sources := []ShellJobActivitySource{ActivityRunnerExecution, ActivityValidationPlan, ActivityCargoOutput}
	canonical := 0
	for _, state := range states {
		for _, phase := range phases {
			for _, source := range sources {
				want := ShellJobActivity{state, phase, source}
				b, _ := json.Marshal(want)
				var got ShellJobActivity
				if e := json.Unmarshal(b, &got); e != nil || got != want {
					t.Fatalf("%s: %v", b, e)
				}
				if got.IsCanonical() {
					canonical++
				}
			}
		}
	}
	if canonical != 7 {
		t.Fatalf("canonical activity combinations: %d", canonical)
	}
	for _, state := range []ShellCommandExecutionState{CommandNotStarted, CommandOutcomeUnknown, CommandTimedOut, CommandCompleted} {
		b, _ := json.Marshal(struct {
			State     ShellCommandExecutionState `json:"command_execution_state"`
			Completed uint64                     `json:"completed"`
		}{state, 0})
		// Exercise the enum through the actual Job update field.
		var obj map[string]json.RawMessage
		_ = json.Unmarshal(b, &obj)
		wire := `{"client_id":"c","agent_instance_id":"a","job_id":"j","status":"future","command_execution_state":` + string(obj["command_execution_state"]) + `}`
		var got RunnerJobUpdateRequest
		if e := json.Unmarshal([]byte(wire), &got); e != nil || got.CommandExecutionState == nil || *got.CommandExecutionState != state {
			t.Fatal(wire, e)
		}
	}
	for _, language := range []ShellScriptLanguage{ScriptSh, ScriptBash, ScriptPowershell} {
		b, _ := json.Marshal(ShellJobStructuredExecutionMetadata{Language: &language})
		var got ShellJobStructuredExecutionMetadata
		if e := json.Unmarshal(b, &got); e != nil || got.Language == nil || *got.Language != language {
			t.Fatal(language, e)
		}
	}
}

func TestJobOwnedDecodeAndWireDefaults(t *testing.T) {
	cwd := "/project"
	stdin := "input"
	p := JobProcessOperation{JobID: "j", Cwd: &cwd, Process: ShellProcessArgv{Executable: "go", Args: []string{"test"}}, Stdin: &stdin, TimeoutSecs: 3600, Context: ShellJobContext{CommandPreview: "p", Cwd: &cwd}}
	m := p.ExpectedStructuredExecution()
	p.Context.StructuredExecution = &m
	r, e := (JobInvocation{Operation: p}).IntoV2Request()
	if e != nil {
		t.Fatal(e)
	}
	inv, e := r.DecodeJobInvocation()
	if e != nil {
		t.Fatal(e)
	}
	*r.Cwd = "other"
	*r.Stdin = "other"
	r.Process.Args[0] = "other"
	got := inv.Operation.(JobProcessOperation)
	if *got.Cwd != "/project" || *got.Stdin != "input" || got.Process.Args[0] != "test" {
		t.Fatal("decoded operation aliased input DTO")
	}
	for _, v := range []any{ShellJobInventory{}, RunnerJobsListResponse{}, ShellJobValidationMetadata{}, ShellJobValidationStep{}} {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		var obj map[string]json.RawMessage
		_ = json.Unmarshal(b, &obj)
		for _, key := range []string{"jobs", "steps", "args"} {
			if raw, ok := obj[key]; ok && string(raw) != "[]" {
				t.Fatalf("%T.%s: %s", v, key, raw)
			}
		}
	}
}
