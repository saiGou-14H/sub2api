// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// core runner_protocol.rs and runner-registry validation.rs/capabilities.rs.
package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidateRegistration validates initial registration identity and metadata.
// Call ReadRegisterRequest first at a JSON boundary to enforce explicit shell.
// It does not validate or authorize deferred inventories, policy, or providers.
// The original generation baseline has its own explicit validator below.
func ValidateRegistration(r RunnerRegisterRequest) error {
	if err := ValidateIdentity(r.ClientID, r.AgentInstanceID); err != nil {
		return err
	}
	if r.AgentProtocolGeneration != RunnerProtocolGenerationV2 {
		return fmt.Errorf("agent_protocol_generation is unsupported")
	}
	for name, p := range map[string]*string{"display_name": r.DisplayName, "owner": r.Owner, "hostname": r.Hostname} {
		if p != nil && (utf8.RuneCountInString(*p) > 200 || strings.ContainsRune(*p, 0)) {
			return fmt.Errorf("%s invalid or oversized", name)
		}
	}
	if r.JobConcurrencyLimit != nil && (*r.JobConcurrencyLimit < 1 || *r.JobConcurrencyLimit > 64) {
		return fmt.Errorf("job_concurrency_limit must be between 1 and 64")
	}
	if r.HostContext != nil {
		if _, err := r.HostContext.Normalized(); err != nil {
			return err
		}
	}
	return nil
}
func ValidateIdentity(clientID, instanceID string) error {
	for _, f := range []struct {
		name, value string
		max         int
		dot         bool
	}{{"client_id", clientID, 80, true}, {"agent_instance_id", instanceID, 128, false}} {
		if len(f.value) == 0 || len(f.value) > f.max {
			return fmt.Errorf("%s invalid length", f.name)
		}
		for _, c := range f.value {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || f.dot && c == '.') {
				return fmt.Errorf("%s contains invalid character", f.name)
			}
		}
	}
	return nil
}
func (h RunnerHostContext) Normalized() (RunnerHostContext, error) {
	total := 0
	count := 0
	fields := []struct {
		name string
		p    **string
		max  int
	}{{"role", &h.Role, 64}, {"runtime", &h.Runtime, 512}, {"service", &h.Service, 512}, {"network", &h.Network, 512}, {"architecture", &h.Architecture, 512}}
	for _, f := range fields {
		if *f.p == nil {
			continue
		}
		s := strings.TrimSpace(**f.p)
		if len(s) == 0 || len(s) > f.max {
			return RunnerHostContext{}, fmt.Errorf("host_context.%s invalid length", f.name)
		}
		for _, c := range s {
			if f.name == "role" {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
					return RunnerHostContext{}, fmt.Errorf("host_context.role invalid character")
				}
			} else if unicode.IsControl(c) {
				return RunnerHostContext{}, fmt.Errorf("host_context.%s contains control character", f.name)
			}
		}
		*f.p = &s
		total += len(s)
		count++
	}
	if count == 0 || total > 1536 {
		return RunnerHostContext{}, fmt.Errorf("host_context empty or oversized")
	}
	return h, nil
}

// GenerationV2BaselineCapabilityNames returns an owned copy of the exact
// upstream baseline. Presence of generation 2 never grants any capability.
func GenerationV2BaselineCapabilityNames() []string {
	return []string{
		"file_read", "file_write", "artifact_export_chunk_read", "artifact_export_streaming_metadata", "structured_file_delete", "apply_text_edit_occurrence", "jobs", "async_jobs", "async_shell_jobs", "structured_validation_argv", "structured_cargo_test_count_assertion", "structured_go_test_json", "structured_go_test_tool", "structured_go_test_packages", "structured_process_argv", "structured_script_payload", "internal_posix_script", "structured_execution_jobs", "lsp_read_only_navigation", "lsp_call_hierarchy", "project_lifecycle", "project_path_registration",
	}
}
func ValidateGenerationCapabilities(c RunnerCapabilities) error {
	data, _ := json.Marshal(c)
	var bits map[string]bool
	_ = json.Unmarshal(data, &bits)
	for _, name := range GenerationV2BaselineCapabilityNames() {
		if !bits[name] {
			return fmt.Errorf("runner generation baseline capability mismatch: %s", name)
		}
	}
	if c.ApplyPatchMatchMetadata && !c.ApplyPatch {
		return fmt.Errorf("apply_patch_match_metadata requires apply_patch")
	}
	if (c.ApplyPatchStrictMatching || c.ApplyPatchMatchingMode) && !c.ApplyPatchMatchMetadata {
		return fmt.Errorf("apply_patch matching capabilities require apply_patch_match_metadata")
	}
	return nil
}
