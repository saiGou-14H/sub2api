# ChatGPT Web Tool Calling Compatibility

Status: Implemented compatibility bridge

Last updated: 2026-09-06

## 1. Purpose

Sub2API's ChatGPT Web transport supports access-token-backed text, dynamic model
discovery, direct SSE, `stream_handoff + WebSocket`, and attachments through the
existing `/v1/responses` and `/v1/chat/completions` endpoints. The private
ChatGPT Web conversation endpoint does not accept OpenAI API `tools` fields
directly, so the optional Prompt Tool bridge converts them into an internal
strict prompt protocol.

This document records the implementation baseline for adding OpenAI-compatible
tool calling without claiming that ChatGPT Web provides a native function-
calling API. The bridge is prompt-based: the public request is validated and
normalized, a private protocol instruction is sent to ChatGPT Web, and the
result is converted back to standard Responses/Chat tool events.

## 2. Current behavior

With Prompt Tool disabled, the Web transport rejects:

- non-empty `tools` and legacy `functions` declarations;
- non-no-op `tool_choice` and legacy `function_call` selections;
- Chat Completions messages containing `tool_calls`, `function_call`,
  `tool_call_id`, or `tool`/`function` roles;
- Responses input that requires a stateful native tool continuation.

These checks are implemented in
`backend/internal/service/openai_web_transport.go`. They prevent Sub2API from
silently accepting semantics that the current Web adapter would discard.

With `enable_openai_web_prompt_tools=true`, function/custom/namespace and
additional tool declarations are validated and converted instead of forwarded
as native Web fields. The setting is fail-closed and disabled by default.

## 3. WebCodex reference conclusion

[WebCodex](https://github.com/saiGou-14H/webcodex) is a tool execution runtime,
not an LLM tool-call generation adapter. Its Server exposes tool schemas over
MCP, REST, and OpenAPI, applies authentication and authorization, then sends
project operations to a Runner over WebSocket, QUIC, or polling.

WebCodex can execute a tool call after a model or client has selected a tool.
It does not make an access-token ChatGPT Web request emit OpenAI-compatible
`tool_calls` by itself. An OpenAI Secure MCP Tunnel only changes how ChatGPT
reaches a WebCodex Server; it does not replace the missing model-to-tool-call
conversion layer.

Consequently, WebCodex is suitable as an optional execution backend, but it is
not required for the first compatibility layer.

## 4. Proposed modes

| Mode | Tool selection | Tool execution | Required configuration |
|---|---|---|---|
| Client-executed compatibility bridge | ChatGPT Web through a constrained prompt protocol | The OpenAI API client | Existing Web account access token only |
| WebCodex server-executed mode | ChatGPT Web through the same constrained prompt protocol | Configured WebCodex Server and Runner | Access token plus WebCodex URL, credential, project, and policy |

The client-executed compatibility bridge is the recommended first phase. It
preserves normal OpenAI function-calling semantics: Sub2API returns a tool call,
the caller executes it, and the caller sends the result in a later request.

WebCodex execution should be a separate opt-in mode because it changes the
authority boundary. In that mode Sub2API, rather than the API caller, causes
tools to run.

## 5. Recommended phase-one flow

```text
OpenAI client request with function tools
    -> validate and normalize tool schemas
    -> add a transport-internal tool protocol instruction
    -> send the conversation to ChatGPT Web
    -> buffer and classify the assistant response
    -> validate the selected tool name and JSON arguments
    -> emit standard Responses function_call or Chat tool_calls
    -> client executes the function
    -> client sends function_call_output or a tool message
    -> encode the correlated result into the next Web conversation
    -> return the final assistant answer
```

Sub2API must not execute caller-provided functions in this mode.

## 6. Transport-internal tool protocol

The bridge should add an instruction that is not included in the public request
history. The instruction describes the available function names, descriptions,
and JSON Schemas and gives the model two valid outcomes:

1. Return normal assistant text when no tool is needed.
2. Return a strict tool-call envelope containing one or more calls.

The implemented envelope uses a random per-request nonce, a normalized schema
hash, an explicit event type, and start/end turn signals:

```json
{
  "protocol": "sub2api.prompt_tool.v1",
  "nonce": "request_nonce",
  "schema_hash": "sha256-prefix",
  "event": "tool_call",
  "start": "tool_call_start",
  "end": "tool_call_end",
  "calls": [
    {"name":"get_weather","type":"function","arguments":{"city":"Shanghai"}}
  ]
}
```

For `custom` tools, the call uses free-text `input`:

```json
{"name":"exec","type":"custom","input":"pwd"}
```

The parser also accepts the legacy `tools` array and the compatibility forms
`arguments: "text"`, `arguments: {"input":"text"}`, and raw text. All forms
are normalized to the same internal call before public Responses events are
emitted. Legacy envelopes may omit all three boundary fields; partial or
incorrect boundary fields are rejected so a malformed turn is never classified
as a valid tool call.
The parser must accept it only when all of the following are true:

- the nonce matches the current request;
- every tool name exists in the validated request tool set;
- every function `arguments` value is valid JSON and represents an object;
- every custom `input` value is a bounded string and is validated through the
  strict `{ "input": "..." }` wrapper Schema;
- the call count and serialized argument size are within configured limits;
- `tool_choice`, including a specifically named function, is satisfied;
- no unparsed non-whitespace data is mixed into a required tool-call envelope.

The nonce reduces accidental collisions but does not turn model output into a
trusted command. The result remains untrusted data until the API caller chooses
to execute it.

## 7. Public API mapping

### 7.1 Responses API

For a selected function, the non-streaming response should contain an output
item equivalent to:

```json
{
  "type": "function_call",
  "id": "fc_generated",
  "call_id": "call_generated",
  "name": "get_weather",
  "arguments": "{\"city\":\"Shanghai\"}",
  "status": "completed"
}
```

Streaming responses should emit the normal function-call item lifecycle,
including output-item added/done and function-call-arguments delta/done events,
followed by `response.completed`.

The next request may contain the corresponding `function_call` and
`function_call_output` items. Phase one should remain stateless and require the
caller to send the relevant history; native Web `previous_response_id`
continuation remains out of scope.

### 7.2 Chat Completions API

For the same selection, the response should populate
`choices[0].message.tool_calls` and use `finish_reason: "tool_calls"`.
Streaming chunks should expose tool-call deltas with stable indexes, IDs,
function names, and argument fragments.

The next request may contain the assistant tool-call message followed by one or
more `role: "tool"` messages correlated by `tool_call_id`.

The implementation should generate one internal Responses-style representation
and reuse Sub2API's existing Responses-to-Chat conversion where possible.

## 8. Strict schema and tool registry

The bridge uses a normalized internal representation rather than passing raw
OpenAI tool JSON into the Web endpoint. Function-like schemas are parsed as
JSON Schema Draft 2020-12 documents. The validator enforces valid JSON, object
roots, valid keyword types, bounded depth/bytes, consistent `required` and
`properties`, and safe `additionalProperties` handling. Tool arguments are
validated against the same normalized schema before a call is emitted. Invalid
schemas or arguments fail closed with an OpenAI-shaped 400/upstream protocol
error; they are never silently repaired into a different call.

The registry is type-driven and extensible. It currently recognizes these
OpenAI/ChatGPT tool families:

| Declaration type | Prompt representation | Public result | Execution boundary |
|---|---|---|---|
| `function` | function schema | `function_call` / `tool_calls` | API client |
| `custom` | strict `{input:string}` wrapper | function-compatible call | API client |
| `web_search`, `web_search_preview`, `x_search` | typed search wrapper | function-compatible call | API client or configured search service |
| `file_search` | typed retrieval wrapper | function-compatible call | API client or configured retrieval service |
| `code_interpreter`, `shell`, `local_shell` | typed execution wrapper | function-compatible call | external sandbox required |
| `computer_use` | typed action wrapper | function-compatible call | external computer-use runner required |
| `image_generation` | typed image wrapper | function-compatible call | image executor required |
| `remote_mcp`, `mcp`, `skills`, `tool_search`, `programmatic_tool_call` | typed namespace/search wrapper | function-compatible call | configured MCP/skills executor required |
| `namespace` | flattened, collision-checked function names | function-compatible call | API client or registered executor |

The registry accepts newly introduced declaration types through the generic
typed-wrapper path, so the public `/v1/models` catalog and Web model selectors
do not need a hard-coded five-model/tool ceiling. Generic wrappers preserve the
original declaration type in the private protocol and include an explicit
capability marker in the returned call metadata. They do not imply that the
gateway can execute the tool: only the API caller or an administrator-
configured executor may do so.

Every request receives a random nonce and schema hash. The parser requires the
exact protocol, nonce, hash, allowlisted name, valid argument object, bounded
call count/size, and no unparsed non-whitespace text in a tool envelope.

The bridge supports `auto`, `none`, `required`, named choices, parallel calls,
legacy `functions`/`function_call` normalization, `additional_tools`, namespace
tools, assistant tool-call history, `function_call_output`,
`custom_tool_call_output`, tool-result continuation, attachments, and both
streaming and non-streaming public endpoints. Tool-enabled Web streams are
buffered until the classifier can prove whether the output is text or a valid
envelope; private markers are never leaked to clients.

Legacy `functions` and `function_call` may be normalized to the modern function
tool representation after the primary flow is stable.

## 9. Streaming constraint

Tool-enabled Web requests cannot safely forward assistant text immediately.
Sub2API must first determine whether the response is normal text or a tool-call
envelope. The initial implementation should therefore buffer the complete
assistant output for tool-enabled requests, then synthesize a valid streaming
event sequence.

This preserves protocol correctness at the cost of first-token latency. The
same buffered classifier runs after direct SSE parsing or after WebSocket
handoff frames have been unwrapped, so the public event contract is identical
for both Web transport variants.

## 10. Failure behavior

The bridge must fail closed:

- invalid client tool schemas return OpenAI-shaped HTTP 400 errors;
- a model-selected unknown tool is an upstream protocol error and is never
  executed;
- malformed arguments are never repaired silently into a different call;
- an unsatisfied `required` or specifically named `tool_choice` is reported as
  an upstream tool-selection failure;
- incomplete Web streams cannot produce a completed tool call;
- plain-text requests without tools retain their current streaming behavior.

When a custom call is selected, Responses uses the official custom lifecycle:
`custom_tool_call`, `response.custom_tool_call_input.delta`,
`response.custom_tool_call_input.done`, and the paired
`custom_tool_call_output` on continuation. Function calls retain the parallel
`function_call` lifecycle and are never mixed with custom input events.

An optional bounded model repair attempt can be evaluated later. It should not
be part of the first implementation because it changes latency, cost, and retry
semantics.

## 11. Optional WebCodex execution mode

If server-side coding tools are required later, Sub2API may add a separately
configured WebCodex provider that:

1. reads a bounded tool catalog from MCP `tools/list` or the WebCodex OpenAPI
   projection;
2. exposes only an administrator-approved allowlist to the Web tool-selection
   bridge;
3. validates selected arguments against the registered schema;
4. calls WebCodex through its authenticated MCP or REST endpoint;
5. correlates the result with the generated call ID and continues the Web
   conversation;
6. records execution and approval outcomes without logging credentials or full
   sensitive payloads.

This mode requires explicit configuration for endpoint URL, authentication,
project selection, allowed tools, timeouts, output limits, and approval policy.
It cannot satisfy an "access token only" deployment contract.

The WebCodex endpoint must be administrator-configured. A client request must
never be allowed to provide an arbitrary execution URL or credential.

## 12. Security boundaries

- Treat tool descriptions, schemas, model output, arguments, and tool results
  as untrusted data.
- Limit tool count, schema depth, schema bytes, argument bytes, result bytes,
  and calls per turn.
- Validate function names and call IDs before conversion.
- Do not log access tokens, WebCodex credentials, raw authorization headers, or
  unbounded tool arguments/results.
- Keep client-executed and server-executed modes distinct in configuration,
  scheduling, observability, and billing.
- Never execute a tool merely because its name appears in assistant text.
- Preserve current account proxy, rate-limit, retry, and request-isolation
  behavior.

## 13. Verification matrix

Focused tests should cover both public endpoints in streaming and non-streaming
mode:

- no-tool requests remain byte-compatible with current behavior;
- `auto`, `none`, `required`, and named tool selection;
- one call and multiple parallel calls;
- tool result continuation and final assistant text;
- invalid schemas, unknown names, malformed JSON, duplicate call IDs, excessive
  call counts, and oversized arguments;
- model output that contains marker-like user text but no valid nonce;
- truncated Web streams before the envelope is complete;
- attachments combined with function tools;
- all existing Web sampling, reasoning, model discovery, and error mapping
  regressions.

A live acceptance test should use a harmless local function such as `echo` or
`get_test_value`. It should verify the complete client loop without granting the
gateway permission to execute shell commands.

WebCodex mode, if implemented, requires a separate acceptance suite with an
isolated test project, a read-only tool first, explicit mutation approval tests,
and Runner disconnect/retry coverage.

## 14. Implementation gates

- Prompt Tool is disabled by default and enabled only by the administrator
  setting `enable_openai_web_prompt_tools`.
- When disabled, Web requests with tools keep the existing explicit 400 error.
- When enabled, all declarations go through the strict registry and generic
  wrappers. Unsupported execution environments return a clear capability error
  after the client receives the call; the gateway never executes arbitrary
  caller-provided code.
- Focused tests must cover schema rejection, all registry families, nonce/hash
  validation, tool-choice semantics, parallel calls, history/result replay,
  stream buffering, malformed envelopes, and attachment coexistence.

This document is the contract for the first runtime implementation; any change
to the envelope or registry requires a versioned protocol update and regression
tests.

## 15. Streaming tool events

The Web upstream still returns assistant content as cumulative text patches.
When Prompt Tool is enabled, the adapter keeps the raw patch text and incrementally
parses a JSON prefix after the protocol marker is observed. Once `name`, `type`,
and the arguments/input prefix are available it emits the corresponding
`response.output_item.added` and argument/input `delta` events immediately. The
public call ID and item ID are generated once and remain stable for the turn.

Only the terminal Web frame is allowed to produce `arguments.done` or
`custom_tool_call_input.done`, `output_item.done`, and `response.completed`.
At that point the complete envelope is parsed again using the strict nonce,
schema hash, boundary-signal, tool-choice, size, and JSON-Schema checks. A
truncated stream, rewritten prefix, invalid end signal, or failed final schema
validation produces `response.failed` and exactly one `data: [DONE]`; no
provisional call is exposed as completed. Legacy envelopes that omit all three
boundary fields remain buffered and compatible, but are not speculatively
streamed.
