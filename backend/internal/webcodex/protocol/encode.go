// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9.
package protocol

import "encoding/json"

// Rust Vec values always serialize as arrays. Normalize nil Go slices for
// programmatically constructed outbound DTOs as well as decoded values.
func (x ShellProcessArgv) MarshalJSON() ([]byte, error) {
	type plain ShellProcessArgv
	v := plain(x)
	if v.Args == nil {
		v.Args = []string{}
	}
	return json.Marshal(v)
}
func (x ShellScriptPayload) MarshalJSON() ([]byte, error) {
	type plain ShellScriptPayload
	v := plain(x)
	if v.Args == nil {
		v.Args = []string{}
	}
	return json.Marshal(v)
}
func (x RunnerProjectSummary) MarshalJSON() ([]byte, error) {
	type plain RunnerProjectSummary
	v := plain(x)
	if v.Hooks == nil {
		v.Hooks = []string{}
	}
	return json.Marshal(v)
}
func (x RunnerView) MarshalJSON() ([]byte, error) {
	type plain RunnerView
	v := plain(x)
	if v.Projects == nil {
		v.Projects = []RunnerProjectSummary{}
	}
	return json.Marshal(v)
}
