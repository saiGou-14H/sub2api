# Runner polling registry

This reference describes the synchronous polling portion of the WebCodex Server migration. Its original wire DTOs are owned by [protocol](../protocol/README.md). The package is not mounted by sub2api's router and does not execute commands.

## Admission and ownership

`NewHTTPHandler` requires a host authenticator and a positive complete-body byte limit. The authenticator returns the verified credential kind, owner, bound `client_id`, scopes and original shared-key/project-grant access projection. It must check expiry, revocation and credential authenticity. A model API key or an unverified JSON identity does not grant Runner access.

| Original route (POST) | Original required scope |
|---|---|
| `/api/shell/agent/register` | `agent:register` |
| `/api/shell/agent/poll` | `agent:poll` |
| `/api/shell/agent/result` | `agent:result` |
| `/api/shell/agent/offline` | `agent:register` |

Agent tokens are bound to `client_id` and owner. Shared keys are isolated by the verified SHA-256 group; their requested owner is not an authorization input. Bootstrap is an explicit host-auth kind. Global visibility alone does not bypass managed-Runner ownership for dispatch. Query credentials and duplicate Authorization headers are rejected. Credential-verifier errors are never reflected in responses.

Registration requires generation 2 and all 22 original baseline capability bits. These bits are facts reported by a real executor, not defaults inferred from the generation. A partial DSH executor cannot register merely by supporting the six migrated direct request codecs. Same-instance reconnect preserves queued and dispatched work. A new instance retires the prior instance, settles prior requests with their dispatch evidence, and never transfers those requests to the replacement. The original bounded retirement history is 16 instances.

## Synchronous requests and lifecycle

Trusted host callers use `Enqueue` only after establishing the original project and operation scopes. The HTTP adapter has no dispatch endpoint. The caller supplies the durable unique `request_id`; this memory registry does not replace the Connector execution record or idempotency transaction.

The current dispatch decoder accepts `run_shell`, `run_process`, `run_script`, `file_read`, `file_write` and `file_list`. Each request must match the target's advertised capability. Job-backed operations and deferred families fail explicitly. Structured argv/script payloads remain structured and are not concatenated into shell commands. A request is delivered at most once while pending, including after a polling response is lost. Consuming a result requires its exact dispatched request, client and active instance. A duplicate consumed/expired result receives the original HTTP 400 failure behavior; success means an in-memory waiter accepted it, not a durable acknowledgement.

Each returned `Pending` must be awaited with a bounded context or explicitly cancelled. Cancellation before dispatch removes the queue item. After dispatch, `Outcome.Dispatched` stays true even when the waiter fails, so timeout, disconnect and replacement cannot prove that execution never started. Cancellation does not stop a remote process and cannot authorize automatic retry. `Close` synchronously settles all waiters; the registry owns no subprocesses, sockets or timer goroutines.

Runner views preserve `online`/`stale`, second-based timestamps, and `pending_requests` as the undelivered queue length. Result stdout/stderr preserve the original last-256-KiB UTF-8 tail and truncation notice. The notice is additional to the per-stream limit; the complete HTTP body has a separate configured bound. Registry options bound retained nodes and pending requests. Original shared-key per-group/global ceilings remain 16/1024. Cancelled queue items are removed immediately. Offline records are retained until registry disposal; TTL collection is deferred.

## Validation

Run from the backend directory:

```sh
go test -race ./internal/webcodex/...
DSH_RUNNER_CHECKOUT=/absolute/path/to/deepseek-harness go test -run TestDSH -v ./internal/webcodex/runner
```

The opt-in cross-language tests launch the test-only DSH stdin driver through `node --import tsx/esm`. They need DSH's workspace dependencies. They round-trip direct request JSON, including signed/unsigned 64-bit extrema, and prove that the actual partial DSH client is rejected by the Go generation check before executor invocation. Go loopback tests use a fixture credential verifier and fabricated results; they do not prove local execution or end-to-end ChatGPT Connector operation.

## Migration limitations

The host credential/store adapter, router/composition mounting, WebSocket/QUIC, provider policy sanitization, paged project inventories, Jobs/recovery, Generic ToolRuntime, ProjectConnector/OAuth/MCP and UI are not implemented here. Registration carrying policy/provider/recovery inventory is explicitly rejected until those owned semantics are migrated. A database, transaction and outbox are required before claiming persistent result receipt, restart recovery or durable idempotency. A full generation-2 DSH execution provider with its real non-Agent guard/approval/filesystem/sandbox owner remains required. No package option disables that generation check.

## Source and licensing

Ported behavior comes from WebCodex commit `97ad66949a859174911c2f6da2ff1063be98bfa9`, `src/runner_http/{auth,handlers}.rs` and `crates/webcodex-runner-registry/src/{access,access_control,capabilities,runners,polling,jobs,registry,validation}.rs`, under Apache-2.0; the license text and modification attribution are bundled as [LICENSE](../LICENSE) and [NOTICE](../NOTICE). Source and test files carry SPDX attribution; [protocol](../protocol/README.md) owns DTO provenance. These files implement the scoped native Go migration; no WebCodex or Rust executable is called at runtime.
