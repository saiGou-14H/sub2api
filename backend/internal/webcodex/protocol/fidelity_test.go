// SPDX-License-Identifier: Apache-2.0
// Wire-fidelity regressions for the WebCodex Apache-2.0 adaptation.
package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestOriginalRequestFieldInventory(t *testing.T) {
	// runner_protocol.rs:1827-1901 at the attributed fixed commit.
	want := strings.Fields("request_id client_id kind job_id cwd path content max_bytes expected_sha256 expected_prefix start_line end_line create_dirs command process script stdin timeout_secs requested_by created_at validation lsp job_context persistent_shell mcp_gateway plugin_gateway coding_agent")
	typ := reflect.TypeFor[RunnerRequest]()
	if typ.NumField() != len(want) {
		t.Fatalf("request inventory: got %d want %d", typ.NumField(), len(want))
	}
	for i, name := range want {
		if got := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]; got != name {
			t.Fatalf("field %d: got %s want %s", i, got, name)
		}
	}
	if reflect.TypeFor[FilePayload]().NumField() != 9 {
		t.Fatal("canonical file payload must have nine fields")
	}
}
func TestGenerationBaselineIsExplicit(t *testing.T) {
	bits := map[string]bool{"shell": false}
	for _, name := range GenerationV2BaselineCapabilityNames() {
		bits[name] = true
	}
	b, _ := json.Marshal(bits)
	var c RunnerCapabilities
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGenerationCapabilities(c); err != nil {
		t.Fatal(err)
	}
	for _, name := range GenerationV2BaselineCapabilityNames() {
		bits[name] = false
		b, _ = json.Marshal(bits)
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
		if err := ValidateGenerationCapabilities(c); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("missing %s accepted: %v", name, err)
		}
		bits[name] = true
	}
}
func TestCanonicalInvocationOwnsValidatedValues(t *testing.T) {
	r := request(t, "run_process")
	r.Cwd = ptr("/original")
	r.Stdin = ptr("original input")
	r.Process = &ShellProcessArgv{Executable: "tool", Args: []string{"original arg"}}
	inv, err := r.DecodeInvocation()
	if err != nil {
		t.Fatal(err)
	}
	*r.Cwd = "/changed"
	*r.Stdin = "changed"
	r.Process.Args[0] = "changed"
	op := inv.Operation.(ProcessOperation)
	if *op.Cwd != "/original" || *op.Stdin != "original input" || op.Process.Args[0] != "original arg" {
		t.Fatal("DTO mutation changed admitted invocation")
	}
}
func TestOutboundVectorsAreArrays(t *testing.T) {
	for _, v := range []any{ShellProcessArgv{Executable: "x"}, ShellScriptPayload{Language: ScriptSh, Script: "x"}, RunnerProjectSummary{}, RunnerView{}} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"args", "hooks", "projects"} {
			if strings.Contains(string(b), `"`+name+`":null`) {
				t.Fatalf("nil vector serialized as null: %s", b)
			}
		}
	}
}
func TestUnicodeBoundaryDoesNotReplaceData(t *testing.T) {
	for _, raw := range []string{`"\ud800"`, `"\udc00"`, `"\ud800\u0041"`} {
		b := mutate(t, shellJSON, "command", json.RawMessage(raw), false)
		if _, err := ReadRequest(b); err == nil {
			t.Fatalf("accepted unpaired surrogate %s", raw)
		}
	}
	for _, raw := range []string{`"\ud83d\ude3a"`, `"\\ud800"`, `"猫"`} {
		b := mutate(t, shellJSON, "command", json.RawMessage(raw), false)
		if _, err := ReadRequest(b); err != nil {
			t.Fatalf("rejected valid Unicode %s: %v", raw, err)
		}
	}
	b := []byte(strings.Replace(shellJSON, "echo ok", string([]byte{0xff}), 1))
	if _, err := ReadRequest(b); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
}
