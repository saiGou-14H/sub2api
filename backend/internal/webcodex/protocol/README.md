<!-- SPDX-License-Identifier: Apache-2.0 -->
# WebCodex Runner protocol — first V6 milestone

This standard-library-only Go package adapts the original wire DTOs and a bounded initial subset of the canonical operation decoder. It does not execute commands, contact runners, grant authority, implement a server executable, or claim completion of the all-family migration.

Source: WebCodex commit `97ad66949a859174911c2f6da2ff1063be98bfa9`, Apache-2.0. The original license is reproduced in [LICENSE](LICENSE); port attribution and modification notices are in [NOTICE](NOTICE). This package is a modified Go translation and staged subset of the Rust protocol, not an unchanged upstream distribution. The adaptation follows the local fixed source at `source-sync/webcodex/`. Go source and tests retain SPDX attribution. Primary references:

- `crates/webcodex-core/src/runner_protocol.rs`: capabilities/defaults (503–728, 902–963), registration/view/offline (1219–1491), process/script and bounds (1511–1736), poll/request/result (1769–1988).
- `crates/webcodex-core/src/runner_operation.rs`: invocation metadata and file payload (28–69, 218–228), canonical decoding (873–1128), direct/file validation (1270–1413), conflicting payload rules (1469–1559).
- `crates/webcodex-runner-registry/src/{protocol,capabilities,validation,runners}.rs`: generation 2 admission, frozen baseline, bounded identities and registration metadata.
- `WEBCODEX_RUNNER_MIGRATION_MAP_V6.zh-CN.md`, sections 3–5: original `kind` discriminator and staged migration scope.

## Entry points

```go
registration, err := protocol.ReadRegisterRequest(body) // required JSON shape
if err == nil {
    err = protocol.ValidateRegistration(registration) // IDs, exact generation, hints
}
// Mandatory at Registry.Register before installing any runner state:
if err == nil {
    err = protocol.ValidateGenerationCapabilities(registration.Capabilities)
}

wire, err := protocol.ReadRequest(body) // wire-only: preserves deferred payloads
invocation, err := wire.DecodeInvocation() // six initial operations, fail closed
// Or in one call:
invocation, err = protocol.DecodeRequest(body)
```

`ReadPollRequest`, `ReadPollPayload`, `ReadResultRequest`, `ReadResultPayload`, and `ReadOfflineRequest` likewise return a value plus error. All exported DTOs implement strict `UnmarshalJSON`; ordinary `json.Unmarshal` therefore enforces the same shape requirements for requests and responses. Decode into fresh values; failed decoding does not publish a partially decoded struct.

Canonical `Invocation` contains only `Metadata` (`RequestID`, `ClientID`, `RequestedBy`, `CreatedAt`) and a closed `Operation`. Dispatch by its concrete type: `ShellOperation`, `ProcessOperation`, `ScriptOperation`, or `FileOperation`. `Operation.WireKind()` returns the original kind; `FileOperation.Payload` contains exactly the nine original file fields. These Go semantic structs are not a replacement network schema.

Implemented kinds: `run_shell`, `run_process`, `run_script`, `file_read`, `file_write`, `file_list`. Typed process argv and script content remain typed data; quoting, argument splitting, and shell-string construction never occur here. File decoding follows upstream compatibility validation, including allowing otherwise unused `content` on list/read. File observation, expectations/SHA enforcement, root containment, max-byte policy, process launch and result attribution belong to the executor/admission layer.

Every other known original kind returns an error wrapping `ErrUnsupported`; unknown kinds wrap `ErrUnknownKind`. Conflicting typed payloads are rejected first. Deferred families are not semantically validated or converted into executable operations. `run_shell` carrying `job_context` (including Session/SSH metadata) or the original external-search prefix is also unsupported in this direct-local milestone. It never silently falls back to local shell.

## Wire fidelity and limits

- All 27 original `RunnerRequest` top-level fields are present. JSON uses `kind`, never a fabricated `request_kind` alias. Missing `kind` alone defaults to `run_shell`. Explicit null is invalid for this non-optional field.
- `request_id`, `client_id`, `command`, `timeout_secs`, `requested_by`, and `created_at` are required, including `command: ""` for typed/file requests. Missing numeric fields are rejected separately from zero. Shell wire timeout is a required u64 without the typed direct 1–120 semantic constraint.
- Original JSON `agent_instance_id` and `agent_protocol_generation` map to Go `AgentInstanceID` and `AgentProtocolGeneration`. The raw generation type is uint16, preserving future values for explicit admission rejection. `ValidateRegistration` accepts exactly generation 2.
- Registration requires `capabilities` and explicit boolean `shell`. False is valid wire data. General/view capability decoding retains the historical shell=true default; every other missing bit defaults false. `RunnerCapabilities{}` as a Go literal has ordinary Go zero values; it is not a current-runner advertisement. No capability is inferred from generation or transport.
- `ValidateGenerationCapabilities` separately checks the exact original 22 generation-2 baseline fields and apply-patch capability dependencies. `Registry.Register` must call it in addition to `ValidateRegistration`, before installing any state; there is no partial-feature registration bypass. The current partial DSH implementation cannot register until it actually implements the full baseline. Transport/server lifecycle tests may use original-valid registration fixtures, but those fixtures do not advertise real execution support or prove migration completion. Never turn unimplemented capability bits on merely to pass registration.
- Option fields use pointers or optional `json.RawMessage`. Absent and explicit null become None/nil; explicit 0/false/empty strings survive. Raw optional null normalizes to nil and is omitted where upstream uses `skip_serializing_if`. Result request optional fields intentionally serialize as null, matching upstream. Missing arrays default empty where upstream uses `Vec::default`; explicit null arrays and null string elements are rejected.
- Integers use uint16/u64/i64/i32 rather than floating point. `usize` wire fields use uint64 to match the fixed deployment's 64-bit Rust target independently of Go host word size. Fractions, exponent representations for integer fields, negatives for unsigned fields, and overflows are rejected.
- Unknown DTO keys are ignored where upstream serde permits them; known duplicate keys are rejected. `RunnerHostContext` alone uses upstream `deny_unknown_fields`. There is no blanket unknown-field prohibition and no invented tenant, claims, NodeProtocol, or execution-epoch fields.
- `RawShellCommandMaxBytes = 16_000` is the authored command bound; `RawShellWireMaxBytes = 64*1024` is the already-wrapped wire command bound. The latter caps **only the command string**, not the JSON request body. A caller should use `ValidateAuthoredShellCommand` before constructing a shell wrapper. Script text may be up to 512 KiB. All text bounds count UTF-8 bytes. Process/script argv bounds, shell-command-mode rejection, stdin/cwd limits, and direct 1–120 second timeouts follow the source.
- `RunnerView` defaults omitted transport to `polling`, instance ID to empty, lifecycle timestamps to 0, and projects to an empty vector, while generation remains required. These stale-view defaults never authorize registration.

## Explicit deferred nested domains

Registration `policy`, `job_inventory`, and `coding_agent_inventory` are optional RawMessage objects; `coding_agent_providers` is an optional RawMessage array. `host_context` and `build` are typed. The host context is closed and provides `Normalized()` with the source field/content bounds; metadata is descriptive, never authority. `ValidateRegistration` checks identities, display metadata, exact generation, concurrency (when present, 1–64), and host-context validation, but does not mutate the original wire values.

View policy/project-inventory/provider metadata, polling tool-provider/project-page metadata, and result MCP/Plugin/CodingAgent payloads are also deferred RawMessage values. The parser checks their object/array outer shape and preserves valid nested JSON without float conversion. It does **not** implement domain validation, inventory completeness/reconciliation, provider validation, policy sanitization, or durable acknowledgement. Consumers must not use these deferred records as execution authority or publish unvalidated policy/provider internals as a trusted safe projection. If the initial registry cannot implement those checks, it should explicitly reject relevant nonempty metadata or keep it private and non-authoritative.

Request `validation`, `lsp`, `job_context`, `persistent_shell`, `mcp_gateway`, `plugin_gateway`, and `coding_agent` preserve their original top-level slots as RawMessage objects. `process` and `script` are fully typed. Raw deferred requests are only useful for storage/inspection/round-trip; `ReadRequest` alone does not authorize dispatch. No full all-family migration is asserted.

## Shared external fixtures

[testdata/fixtures.json](testdata/fixtures.json) is the language-neutral fixture table, exercised through the public Go API by `fixtures_test.go` in package `protocol_test`. It is test data only. The schema is:

| Member | Meaning |
|---|---|
| `schema_version` | Fixture schema version, currently integer 1; not a Runner wire field. |
| `source_commit`, `license` | Fixed source attribution. |
| `requests[]` | Each entry has unique `name`, original wire `request`, `wire`, `invocation`, and optional expected `kind`. |
| `wire` | `accept` or `reject`, judged by `ReadRequest` / `ReadRegisterRequest`. |
| `invocation` | `accept`, `invalid`, `unknown`, `unsupported`, or `not_checked` after a wire rejection. |
| `kind` | Expected `Operation.WireKind()` for accepted invocations. |
| `registrations[]` | Each entry has `name`, original wire `request`, `wire`, and `admission`. |
| `admission` | `accept` or `reject` after BOTH `ValidateRegistration` and `ValidateGenerationCapabilities`; `not_checked` after wire rejection. |

There are six positive initial-kind cases plus missing/null/zero/integer-boundary/conflict/deferred cases. `original_gen2_baseline` is a registration fixture with the exact 22 required bits plus explicit shell. It is suitable for staged server lifecycle tests only; copying it into a partially implemented DSH capability advertisement would be false advertising. JSON consumers must retain request bodies as raw JSON or use lossless integer parsing, especially the uint64/i64 extremes.

## Focused verification

Run from `integration/sub2api/backend`:

```sh
GOTOOLCHAIN=local \
GOMODCACHE=/root/project-development/A2AMesh/source-sync/toolchains/gomodcache \
/root/project-development/A2AMesh/source-sync/toolchains/go1.27.1/go/bin/go test ./internal/webcodex/protocol
```

Tests cover original required fields and defaults, explicit shell, future/out-of-range generation, missing/null versus zero, integer extremes, typed argv quoting and shell-mode rejection, script/command bounds, file compatibility fields, conflicting and unsupported families, and flattened poll/result payloads. These are focused Go tests, not a claim to have run the upstream Rust suite, transport integration, or production workloads.
