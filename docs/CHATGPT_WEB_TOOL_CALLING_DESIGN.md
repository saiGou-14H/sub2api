# ChatGPT Web Tool Calling Compatibility

Status: Draft

Last updated: 2026-09-04

## 1. Purpose

Sub2API's ChatGPT Web transport currently supports access-token-backed text,
model discovery, streaming, and attachments through the existing
`/v1/responses` and `/v1/chat/completions` endpoints. It intentionally rejects
function tools because the private ChatGPT Web conversation protocol does not
accept OpenAI API `tools` definitions directly.

This document records the initial design for adding OpenAI-compatible function
calling without claiming that ChatGPT Web provides a native function-calling
API.

## 2. Current behavior

The Web transport currently rejects:

- non-empty `tools` and legacy `functions` declarations;
- non-no-op `tool_choice` and legacy `function_call` selections;
- Chat Completions messages containing `tool_calls`, `function_call`,
  `tool_call_id`, or `tool`/`function` roles;
- Responses input that requires a stateful native tool continuation.

These checks are implemented in
`backend/internal/service/openai_web_transport.go`. They prevent Sub2API from
silently accepting semantics that the current Web adapter would discard.

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

The envelope should use a random per-request nonce and an unambiguous marker.
The parser must accept it only when all of the following are true:

- the nonce matches the current request;
- every tool name exists in the validated request tool set;
- every `arguments` value is valid JSON and represents an object;
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

## 8. Initial supported surface

Phase one should support:

- `type: "function"` tools;
- `tool_choice` values `auto`, `none`, `required`, and a specifically named
  function;
- `parallel_tool_calls` with a bounded maximum call count;
- Chat Completions assistant tool calls and tool-result messages;
- Responses `function_call` and `function_call_output` input items;
- streaming and non-streaming output;
- tool-enabled requests that also contain supported attachments.

Phase one should not claim support for built-in web search, code execution,
computer use, custom/freeform tools, remote MCP discovery, or server-side tool
execution.

Legacy `functions` and `function_call` may be normalized to the modern function
tool representation after the primary flow is stable.

## 9. Streaming constraint

Tool-enabled Web requests cannot safely forward assistant text immediately.
Sub2API must first determine whether the response is normal text or a tool-call
envelope. The initial implementation should therefore buffer the complete
assistant output for tool-enabled requests, then synthesize a valid streaming
event sequence.

This preserves protocol correctness at the cost of first-token latency. A later
incremental classifier may reduce latency, but it must never leak the private
tool envelope as assistant text.

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

## 14. Open decisions

- Whether phase one should support only modern function tools or also normalize
  legacy `functions` immediately.
- Maximum schema size, call count, and argument/result size.
- Whether tool-enabled streaming always buffers or supports an incremental
  classifier after the initial release.
- Whether invalid model envelopes fail immediately or permit one bounded repair
  attempt in a later release.
- Whether WebCodex execution belongs in Sub2API core or a separately versioned
  provider/plugin.

No tool-calling behavior described here is implemented until the corresponding
code, tests, live verification, and deployment are completed.
