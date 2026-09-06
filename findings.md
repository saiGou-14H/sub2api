# Findings

## 2026-09-06 Incremental Prompt Tool SSE

- Baseline is clean commit `eb80ee7`; deployed runtime is `5c4c078`.
- Prompt Tool currently accumulates upstream text and only emits tool events
  in `finish()`. Incremental SSE requires parsing before upstream completion.
- Current official documentation search/fetch attempts returned HTTP 403.
  Do not claim fresh official-page verification.
- Sanitized HAR analysis found 282/297 `response.custom_tool_call_input.delta`
  events in Codex Responses streams, while Web conversation HARs expose only
  cumulative `o=append`/`o=patch` text updates. The adapter therefore must
  derive incremental Prompt Tool deltas from cumulative Web text and validate
  the full envelope at the terminal frame.
- The incremental reader now emits stable `response.output_item.added`,
  argument/input delta events with `call_id`/`name`, and defers done/completed
  events until strict final parsing. Prefix rewrites and interrupted streams
  fail closed.

## 2026-09-05 Prompt Tool switch refresh diagnosis

- The frontend load path already assigns every non-null field returned by `GET /api/v1/admin/settings` into the reactive form, including `enable_openai_web_prompt_tools`.
- The backend GET response includes `enable_openai_web_prompt_tools`, and the setting service persists the key as `true`/`false`.
- The backend PUT response payload omitted `EnableOpenAIWebPromptTools`; Go therefore serialized the DTO's zero value (`false`) even when the database write succeeded. `SettingsView.vue` copies every returned field back into the form after save, so the switch visibly flips off and can be saved back as false on a later submit.
- Remote `9999` reproduction: before=false, PUT status=200 with response field=false, subsequent GET=true. The fix must add the field to the PUT response and cover both backend serialization and frontend load/save behavior.

## 2026-09-04 Web model selector evidence

- The supplied screenshot is evidence, not an instruction. Its ChatGPT model
  menu visibly offers Default plus GPT-5.6 Sol, GPT-5.6 Terra, GPT-5.6 Luna,
  and GPT-5.5.
- The corresponding Web protocol model values already used by the surrounding
  project are `auto`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`, and
  `gpt-5.5`.
- The current in-progress implementation incorrectly disables the Web model
  selector and forces every conversation payload to `auto`; both the admin
  test path and public gateway must preserve a validated Web model selection.
- The reported `max_tokens` 400 is produced by Web request validation. The
  account connectivity test must construct a minimal Web-specific text request
  and must not reuse Codex/default token-limit fields.
- The latest `chatgpt2api` reference preserves `ConversationRequest.model` in
  the Web conversation payload. Its account pool treats `auto` as the generic
  route and resolves any concrete model through its model catalog; it does not
  rewrite all requested Web models to `auto`.
- In `chatgpt2api` commit `dc105e51`, `_conversation_payload` writes the passed
  model directly to `payload["model"]`, and `stream_conversation` passes its
  model argument unchanged. This is direct source evidence for model
  passthrough in the Go Web adapter.

## 2026-09-04 read-only remote attachment audit

- The server-side reference checkout is `/opt/ai-kunkun-apps/src/chatgpt2api`.
  The running `chatgpt2api` container is image
  `local/chatgpt2api:v1.7.0-chatgpt2api`, reports version `1.7.0`, and runs with
  `/app/.venv/bin/python` resolving to `/usr/local/bin/python3.13`.
- The host checkout has no `.venv`; the executable environment lives inside
  the container. The only persistent binds are the reference configuration
  and data paths under `/opt/ai-kunkun-apps/state/chatgpt2api`.
- The four `auto` text probes reached both public endpoints in buffered and
  streaming modes. The subsequent PNG/PDF/TXT attachment requests reached the
  upstream conversation path but received an upstream HTTP 400 safety message
  for the synthetic fixtures. The final ZIP sequence encountered repeated
  upstream 429s and one client-disconnect 499; this does not establish a
  protocol conversion failure.
- The temporary API key, account, and group DELETE requests returned HTTP 200,
  but account deletion is soft. Read-only PostgreSQL verification found the
  deleted temporary account row still contains an access-token credential
  (`length=1840`). The probe therefore must not claim credential cleanup.
- Ports 9999, 10000, and 10001 remained healthy (HTTP 200) after the audit. No
  container, service configuration, or database row was changed by the audit.

## 2026-09-04 Web `auto` public-path audit

- The Web transport already forces the private ChatGPT conversation payload to
  `model: auto`, but public handlers pass the caller's raw model through account
  scheduling first.
- A Web account with a non-empty legacy `model_mapping` can therefore reject a
  public `model: auto` request in `Account.IsModelSupported` before the Web
  transport is reached.
- Existing Web routing tests call the forwarding methods directly and do not
  cover handler authorization, channel restriction, scheduler selection, or
  composite platform resolution.
- The public model catalog is derived from account mappings/default OpenAI
  models and does not explicitly publish the Web-only `auto` capability.
- Bare `auto` has no natural platform in composite groups, so it needs a narrow
  OpenAI/Web resolution rule or an explicit composite route before account
  selection.

## 2026-09-04 current HAR conversation contract

- The supplied HAR is treated as protocol evidence only. A structured,
  credential-free inspection confirms that both ordinary text and attachment
  turns call `POST /backend-api/f/conversation/prepare` before
  `POST /backend-api/f/conversation`.
- The prepare body does not contain the user message body. It contains the
  action, parent/conversation identifiers, `model`, client prepare metadata,
  timezone/context fields, and `attachment_mime_types` when files are present.
- The prepare response supplies `conduit_token`; the following conversation
  request carries it as `X-Conduit-Token` together with
  `OpenAI-Sentinel-Chat-Requirements-Token`.
- Current finalize request field names observed in the HAR are
  `prepare_token`, `proofofwork`, and `turnstile`. The existing Go adapter still
  uses the legacy `proof_token` and `turnstile_token` names.
- The full Node Sentinel runner remains a contingency rather than an immediate
  runtime dependency: the latest Python reference already completed a real
  conversation on the target host, so first deploy the confirmed request-shape
  fixes and use stage-aware remote evidence to decide whether Node/SO support
  is actually required for the live challenge.
- The resumed source inspection at 11:58 local time still found no
  `conversation/prepare` or `X-Conduit-Token` implementation. The focused
  transport test still asserts the legacy `proof_token` and
  `turnstile_token` finalize keys, so this remains an active release gate.

## 2026-09-04 endpoint ownership decision

- The user explicitly requires the existing Sub2API `/v1/responses` and
  `/v1/chat/completions` handlers to remain the only public interfaces.
- For an account marked `extra.openai_transport=web`, Sub2API must normalize the
  standard OpenAI request, convert it to the private ChatGPT Web conversation
  request used by `chatgpt2api`, and convert the returned Web SSE back through
  Sub2API's existing OpenAI-compatible response pipeline.
- Authentication, API-key lookup, account scheduling, usage accounting, and
  public error envelopes remain Sub2API responsibilities.
- Both gateway entry points already branch only after Sub2API account selection
  and feed Web transport output into the existing streaming/non-streaming
  response handlers. The public handlers do not need a parallel implementation;
  fixes belong in request validation and Web SSE conversion.

## 2026-09-04 public OpenAI contract refresh

- The rendered official OpenAI API reference now loads from
  `developers.openai.com` when opened directly, even though its search route is
  blocked in this environment.
- `POST /responses` documents `input`, `instructions`, `max_output_tokens`,
  `reasoning`, `stream`, sampling controls, structured `text.format`, tools,
  `tool_choice`, `previous_response_id`, `include`, `store`, and service/cache
  controls as meaningful request fields.
- `POST /chat/completions` documents `messages`, model, token limits,
  `reasoning_effort`, sampling controls, `response_format`, tools/tool choice,
  streaming, modalities, and related compatibility fields.
- Therefore the Web-account adapter may support a documented field, translate
  it with equivalent semantics, or reject a non-default unsupported value with
  an OpenAI-shaped 400. It must not silently accept and discard such values.
- These public pages define the downstream contract only. The private ChatGPT
  Web request remains grounded in the locally tested `chatgpt2api` behavior.

## 2026-09-04 Web transport failure mechanics

- `OpenAIWebTransport.Do` streams a successful conversation response directly.
  For a non-2xx conversation it deliberately ignores the bounded-read error
  before building `OpenAIWebHTTPError`, so that path cannot return the raw
  `ChatGPT web response body exceeds limit` string currently seen in ops logs.
- Bootstrap reads a successful `/` response through a 4 MiB cap and propagates
  an overflow directly. Prepare/finalize also propagate their bounded-read
  errors. The most likely current failure is therefore an oversized bootstrap
  HTML document, pending a remote stage/size probe.
- Gateway diagnostics prefill `ActualEndpoint` as
  `/backend-api/conversation` before the Web adapter runs bootstrap. The stored
  endpoint does not identify which step of the multi-request Web flow failed.
- A live remote probe through the already deployed `chatgpt2api` curl-cffi
  session returned the authenticated ChatGPT home page as HTTP 200 with
  337,590 decoded bytes (`text/html`, Brotli on the wire). A normal browser-
  compatible bootstrap is therefore far below the Go adapter's 4 MiB cap.
- Plain remote `curl`, even with the same high-level user-agent and bearer
  token, returned a small Cloudflare 403. This isolates browser TLS/session
  behavior as material and rules out ordinary server reachability.
- The shared cached Go clients reuse transports/connections but are created
  without a CookieJar. The Python reference uses a persistent Chrome-
  impersonating session for bootstrap, sentinel, uploads, and conversation.
- The Python reference's ordinary API requests send `Priority: u=1, i`, the
  complete `Sec-Ch-Ua*` fingerprint family, and `Sec-Fetch-Dest: empty`,
  `Sec-Fetch-Mode: cors`, `Sec-Fetch-Site: same-origin`. Bootstrap overrides
  those fetch headers to `document`, `navigate`, `none`, adds
  `Sec-Fetch-User: ?1`, and sends `Upgrade-Insecure-Requests: 1`.
- A transport-local CookieJar can preserve Set-Cookie state across bootstrap,
  sentinel, upload, and conversation without modifying the shared
  `HTTPUpstream` implementation. The jar must be applied before every request
  and updated immediately after every response.
- The repository already depends on `github.com/imroc/req/v3`, whose
  per-client `ImpersonateChrome` transport and default CookieJar match the
  reference browser session more closely than the shared plain HTTP client.
  Web transport can first offer each request to `PluginManager`; only an
  unhandled production request should fall through to a transport-local req
  client. Explicitly injected `HTTPUpstream` remains the deterministic test
  seam.
- The reference session also sends a coherent Edge 143 header set: `Priority`,
  `Sec-Ch-Ua*`, and `Sec-Fetch-*` in addition to the headers already present in
  the Go adapter. The current Go `commonHeaders` omits those browser client
  hints entirely.
- Sub2API already exposes `HTTPUpstream.DoWithTLS` and account-bound/default
  TLS fingerprint profiles. The Web adapter should reuse that extension point
  instead of introducing an unrelated HTTP stack, but the exact
  `doOpenAIUpstream` behavior must be confirmed before editing.
- `doOpenAIUpstream` falls back to `HTTPUpstream.Do`, not `DoWithTLS`.
  `Account.IsTLSFingerprintEnabled` is hard-limited to Anthropic accounts, so
  an OpenAI Web account can never activate the existing TLS path through its
  current account settings.
- A Web-specific route therefore needs an explicit browser TLS profile or a
  separate compatible transport decision. Broadening the generic OpenAI path
  would risk changing Codex/OAuth behavior and is out of scope.
- The existing uTLS package is not a generic Chrome impersonator: it builds a
  custom ClientHello around Node.js 24 defaults and defaults ALPN to
  `http/1.1`. Reusing an empty/default profile would not match the reference
  curl-cffi Chrome/Edge handshake.
- Cookie continuity, phase-specific browser headers, and stage-aware errors can
  be implemented and tested independently first. A real remote retry must
  decide whether extending uTLS to a built-in Chrome ClientHello is also
  required; it cannot be claimed from unit tests alone.
- The repository already depends on `github.com/imroc/req/v3` and exposes
  `CreatePrivacyReqClient`, which enables `ImpersonateChrome()` specifically
  for ChatGPT Cloudflare compatibility and supports configured proxies.
- Its current factory returns globally cached clients keyed only by proxy and
  options. Reusing that shared instance for Web conversations would risk
  cross-account CookieJar state. A Web transaction needs an isolated req
  client that persists cookies only across its own bootstrap, sentinel,
  attachment, and conversation requests.
- `req.Client.Do(*http.Request)` is compatible with the native Go client and
  returns `*http.Response`, so a request with auto-read disabled at the native
  layer can preserve the existing streaming SSE adapter.
- A fresh `req.C()` initializes an in-memory CookieJar. `ImpersonateChrome()`
  configures Chrome 120 ClientHello, HTTP/2 SETTINGS, pseudo/header ordering,
  flow control, and common browser headers. This is substantially closer to
  curl-cffi than the repository's custom Node.js uTLS dialer.
- Both Sub2API public handlers branch to Web before Codex normalization,
  identity, client restrictions, passthrough, and WebSocket routing. The public
  routes remain unchanged as requested; only selected-account upstream
  behavior is replaced.
- Those branches currently prefill one generic conversation endpoint before
  the multi-stage transport runs. Web request-validation errors still need to
  be verified at the helper boundary so they become stable OpenAI-shaped HTTP
  400 responses rather than scheduler/failover 502 errors.

## 2026-09-04 remote direct access-token re-test

- The test ran on `66.92.18.39` itself with no `HTTP_PROXY`, `HTTPS_PROXY`, or
  `ALL_PROXY` configured.
- The deployed reference `chatgpt2api` browser-fingerprint client completed
  bootstrap, sentinel prepare/finalize, and a real conversation with the
  supplied access token. The assistant returned `OK` with a completed event and
  a conversation ID.
- The same host then called the port-9999 gateway using account 1 in explicit
  Web mode. Both `/v1/chat/completions` and `/v1/responses` selected the account
  and returned HTTP 502.
- `ops_error_logs` records `ChatGPT web response body exceeds limit` for both
  requests, with the upstream endpoint shown as `/backend-api/conversation`.
- Account 1 remains `active`, `schedulable=true`, `openai_transport=web`, and
  has no proxy. The failure is therefore inside Sub2API's Go Web transport, not
  server reachability or validity of this access token.
- The current admin account-test implementation still probes the Codex
  Responses endpoint for Web accounts. A Codex-path 401 can incorrectly mark a
  valid Web account as unavailable; this is separate from the reproduced 502.

## Repository acquisition

- Remote: `https://github.com/saiGou-14H/sub2api`
- Checkout: `D:\WebGpt\sub2api`
- Current ref: `main` at the fetched upstream commit.
- The checkout uses a local Git HTTP proxy only for repository transport; this is not an application credential or runtime configuration.

## Investigation log

- The backend module is `backend/go.mod` and requires Go 1.27.0.
- `POST /api/v1/admin/accounts` already accepts `type=setup-token` and arbitrary credential maps; the service stores `credentials.access_token` for OpenAI setup-token accounts.
- `OpenAIGatewayService.GetAccessToken` reads OpenAI setup-token credentials directly and returns OAuth forwarding mode without invoking the OAuth token provider or a refresh-token flow.
- OpenAI OAuth-like accounts are forwarded to `https://chatgpt.com/backend-api/codex/responses`; the existing public handlers expose `/v1/chat/completions` and `/v1/responses` in OpenAI-compatible shapes.
- Existing uncommitted changes add JWT `chatgpt_account_id` fallback and an OpenAI access-token input in the admin modal. These changes must be tested for type safety and must not log or echo token values.
- Official OpenAI documentation endpoints returned HTTP 403 from this environment, so no current external protocol claim is being inferred from an official page.
- Gateway callers must still present a Sub2API API key. The ChatGPT web access
  token is an upstream account credential, not a replacement for downstream
  gateway authentication.
- The local official-docs fetch failed consistently: `platform.openai.com` and
  `developers.openai.com` returned Cloudflare 403, while `learn.chatgpt.com`
  timed out. No search snippet is used as compatibility evidence.

## Verification boundary

- No real access token has been read, stored, logged, or used in a network
  request during this task. Validation remains offline unless the user later
  explicitly asks for a secure local runtime test.

## Attachment compatibility audit

- Chat Completions and Responses file parts are represented by `file_data` data
  URIs or an existing `file_id`; the web transport accepts either form.
- The data URI MIME type is authoritative for uploaded bytes, so PDF, text,
  ZIP, office, archive, and other `application/*` or `text/*` payloads are not
  restricted to image MIME types. Image dimensions are populated only when the
  bytes decode as an image.
- Uploads use ChatGPT's three-step metadata/signed PUT/completion flow. The
  signed PUT intentionally has no bearer token. Existing file IDs are reused
  without re-uploading.
- The conversation pointer remains `image_asset_pointer` with
  `file-service://<id>` for all uploaded attachment types, matching the local
  Python reference client's browser protocol. This is covered by offline tests
  but still needs a real authenticated web-account check before claiming
  upstream acceptance for every non-image MIME.
- The Responses bridge now preserves top-level `input_file`/`file` items and
  both nested and top-level `input_image.file_id`. A file-backed image without
  an `image_url` becomes the same Chat `file` part consumed by the Web
  transport, while URL-backed images retain their existing mapping.

## Current worktree review

- The account editor now exposes `extra.openai_transport` with `web` and
  `codex` values for OpenAI OAuth-like accounts; legacy records default to
  `codex`.
- The create-account modal accepts an access-token-only OpenAI setup-token
  account. The token value is passed through the existing credential form and
  is not intended for tests or planning artifacts.
- Backend changes currently present outside the transport implementation
  include JWT account-id fallback and broader OAuth-like usage accounting.
  They must remain compatible with the web transport and be checked for
  unrelated regressions.
- A release audit found that the dedicated web access-token import path was
  reusing Codex defaults and did not persist the transport marker. It now sends
  `extra.openai_transport=web`; legacy and Codex import paths remain unchanged.
- The resumed audit confirms the access-token default fix was made after the
  first port-9999 deployment, so the running image must be fingerprinted and,
  if stale, rebuilt before final acceptance.
- Prior local deployment guidance identifies Paramiko as the reliable
  noninteractive SSH path on this workstation. Remote changes must stay scoped
  to the isolated port-9999 Compose project and preserve the existing
  port-10000 and port-10001 instances.

## Web transport design

- The existing `HTTPUpstream` interface supports account proxy routing and can
  be called for the multi-step web flow (bootstrap, sentinel prepare/finalize,
  conversation).
- The web conversation endpoint always returns SSE. Converting its event stream
  to the existing Responses SSE shape lets both `/v1/responses` and
  `/v1/chat/completions` reuse the gateway's established response, usage, and
  failover handling.
- Web mode must bypass Codex-only identity/WS/plugin paths. It should use the
  access token directly as an upstream Bearer credential and never expose it in
  logs or error bodies.
- The current edit submit path still persists OAuth Responses WebSocket mode,
  passthrough, Codex CLI-only, and fingerprint values while selecting Web mode.
  These mutually exclusive Codex settings need to be hidden and cleared (or
  forced off) so scheduler behavior cannot contradict `openai_transport=web`.
- Both HTTP gateway entry points branch to the Web transport before Codex
  identity, restriction, passthrough, and WebSocket logic. For defense in depth,
  `Account.GetOpenAIResponsesWebSocketV2Mode` should also return `off` for an
  explicit Web account so stale Extra fields cannot make it WS-eligible.
- The reference client requires `OpenAI-Sentinel-Chat-Requirements-Token`,
  optional proof/Turnstile headers, and browser-style target-path headers.

## Integration audit

- `Account.UsesOpenAICodexProtocol` is currently the shared gate for identity,
  payload, and upstream endpoint selection. A web transport override must make
  this method false only for OpenAI OAuth-like accounts explicitly marked
  `extra.openai_transport=web`; legacy and invalid values remain Codex.
- The two public service entry points are `ForwardAsChatCompletions` and
  `Forward`. The web branch must run before Codex restriction/identity setup so
  an access-token-only account does not require OAuth metadata.
- `HTTPUpstream.Do` owns proxy selection and response-body lifecycle. The web
  adapter should pass the account proxy URL and leave downstream response
  accounting to the existing response handlers.

## Remote 9999 deployment audit

- The remote host runs Docker 29.6.1 and Compose 5.3.1 with about 59 GB free
  disk and 13 GB available memory at the pre-deployment check.
- Port 9999 is unused and `/root/sub2api-deploy-9999` does not exist.
- Existing `sub2api` on port 10000 and `sub2api-10001` on port 10001 are
  healthy; their application, PostgreSQL, and Redis containers must remain
  unchanged.
- The new instance will copy the existing `.env` server-side but initialize
  separate `data`, `postgres_data`, and `redis_data` directories.
- The runtime base is pinned to the exact image used by the port 10000
  instance; only the locally adapted embedded Go binary is replaced.

## Deployment packaging

- The local workstation has no `docker` executable, so the isolated image must
  be built on the remote Docker host.
- `deploy/Dockerfile.backend-dist` builds with Go 1.27, `CGO_ENABLED=0`, and the
  required `embed` build tag, then replaces only `/app/sub2api` in the pinned
  production runtime image.
- `backend/internal/web/dist/index.html` exists locally and must be included in
  the upload context because the generated frontend directory is not tracked.
- `backend/resources` has no local diff, so the pinned runtime image copy can
  remain authoritative for runtime resources.
- The prepared build context contains the new Go web transport, gateway
  routing, Dockerfile, and `backend/internal/web/dist`. It intentionally omits
  Vue source files because only the already-built embedded frontend is needed
  by the backend-only image build; the embedded assets still need a marker
  check before accepting that package.
- Exact bundle verification confirms both the local embedded frontend and the
  prepared tarball contain the new `Upstream protocol` and `Web conversation
  (web)` labels. The frontend source is therefore represented in the uploaded
  backend-only build context.
- The resumed `v2` archive has SHA-256
  `256F208BAA1A76B538305EE2419F3A4420E372B3C0C66E382A4065D4E774278D` and
  includes `openai_web_transport.go`, `Dockerfile.backend-dist`, and the latest
  `AccountsView-cFsJoLsr.js` bundle with the direct access-token import flow.
- The refreshed post-fix release context is
  `.tmp-sub2api-9999-context-v2.tar.gz` (5,995,704 bytes, SHA-256
  `256F208BAA1A76B538305EE2419F3A4420E372B3C0C66E382A4065D4E774278D`).
  It contains the rebuilt UI and all current gateway sources, while excluding
  Git metadata, vendor, Go tests, runtime data, `.env`, and `config.yaml`.
- A web-transport account can retain historical Codex WebSocket fields. The
  gateway's early web branch already bypasses WebSocket forwarding, and the
  account capability methods now also force web accounts to `off` so scheduling
  and feature reporting cannot contradict the selected transport.
- The frozen release context is
  `.tmp-sub2api-9999-context-final.tar.gz` (5,994,742 bytes, SHA-256
  `A46E18C20C0831E66755C35E035882ECEE1E91813A2A50CC035ED7A133E63D3B`).
  Its account bundle contains the direct web default and transport selector,
  and its backend contains the WS capability invariant. This supersedes all
  earlier deployment archives.
- UFW is active with a default inbound drop policy. Ports 10000 and 10001 are
  allowed, but 9999 is not; the deployment needs one exact 9999/tcp rule and
  both local and public health checks.
- The remote host is x86_64. The new Compose project, container names, network,
  and data paths have no conflicts with existing Docker objects.
- The existing administrator settings can be retained while database, Redis,
  JWT, and TOTP secrets are rotated inside the new instance's private `.env`.

## Final remote deployment

- The final Compose project is `sub2api-9999` in
  `/root/sub2api-deploy-9999`. It runs immutable local image
  `local/sub2api:web-attachments-9999-r3` with image ID
  `sha256:3ade04df8a8742822996738baeb4185caf3f84700f006bf2a66a340189826efd`.
- Public port 9999 health returns 200. Both `/v1/chat/completions` and
  `/v1/responses` return 401 without a Sub2API API key, confirming that the
  routes are present and protected.
- The deployed frontend exposes the Web/Codex selector. Web mode hides and
  clears Codex-only passthrough, WebSocket, CLI, fingerprint, compact, and
  image-bridge settings. Backend account methods also force Web accounts to
  the non-WebSocket path if stale fields remain.
- Real ChatGPT upstream acceptance still requires a fresh access token and a
  configured Sub2API account/API key. No token from prior console output was
  reused during deployment verification.
# 2026-09-04 r4 continuation boundary

- The currently deployed candidate is `local/sub2api:web-attachments-9999-r4` on the isolated port-9999 Compose project; ports 10000 and 10001 remain outside the change scope.
- The remaining acceptance matrix covers non-streaming and streaming requests for `/v1/chat/completions` and `/v1/responses`, plus representative PNG, PDF, TXT, and ZIP attachments.
- Official downstream references used for this pass are `https://developers.openai.com/api/reference/resources/responses/methods/create/` and `https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create/`; both were fetched successfully on 2026-09-04.
- Real upstream credentials must be supplied ephemerally and must not appear in saved command output, application logs, archives, or planning files.

## 2026-09-04 latest chatgpt2api reference baseline

- The historical customized checkout under `D:\新建文件夹\gpt-new\infinite-canvas-deploy\src\chatgpt2api` is no longer the reference baseline.
- A clean shallow clone of `https://github.com/basketikun/chatgpt2api.git` was fetched into `D:\WebGpt\chatgpt2api-latest` through the local SOCKS proxy.
- The fetched `main` HEAD is `dc105e51bd486bd75c8ef4f74be4bc4724bdfc33`, authored 2026-07-29 with subject `feat: add Atlas Cloud sponsorship section and logo to README`.
- All remaining Sentinel, challenge, attachment, and event-stream comparisons must use that clean commit.

## 2026-09-04 final protocol reconciliation

- The supplied HAR uses one `X-Oai-Turn-Trace-Id` for each paired
  `/f/conversation/prepare` and `/f/conversation` request. The prepare request
  also carries `X-Conduit-Token: no-token`; the final request carries the
  returned conduit token.
- In the captured text turn, prepare includes `partial_query`; in the captured
  file-picker turns it is omitted. Final conversation bodies use
  `client_prepare_state: success`.
- Attachment fields are transport-context dependent. The clean reference
  client uses `file-service://` and `mimeType` for ordinary image messages,
  while its editable/file-picker flow uses `sediment://` and `mime_type`.
  The Go adapter now preserves that distinction rather than forcing one shape
  for every MIME type.
- Fresh focused Web, gateway, account-test, and conversion tests pass. The
  full service package still has four unrelated plugin-package installer
  failures; they are outside the Web adapter and were not changed.

## 2026-09-04 r8a live attachment diagnosis

- The isolated r8a matrix confirmed that `model: auto` is accepted end to end.
  Chat Completions and Responses both returned HTTP 200 in streaming and
  non-streaming modes, and the disposable group/account/key cleanup completed.
- The first matrix PNG fixture was not a valid PNG: Pillow reports a bad IDAT
  checksum. It cannot be used as evidence of image compatibility. A separate
  valid one-pixel fixture decodes successfully and is retained for the next run.
- PDF, TXT, and ZIP all reached the Web conversation endpoint but received the
  same upstream image-safety error. Source review explains the common failure:
  `openAIWebMultimodalMessage` serializes every attachment as
  `image_asset_pointer`, regardless of MIME type.
- The supplied HAR distinguishes the two shapes. Images use
  `multimodal_text` plus an `image_asset_pointer`; an `application/json` file
  keeps ordinary `text` content and appears only in `metadata.attachments`.
  Non-image Web files must therefore never be emitted as image pointers.
- The newly supplied account export contains ten access tokens. Seven are
  unexpired at the current test time and three are already expired. Only the
  access-token field is in scope; exported proxies and all other account fields
  are excluded from import.

## 2026-09-04 dynamic Web model discovery

- The supplied screenshot is evidence only. Its ChatGPT Web model selector
  visibly contains `Default`, `GPT-5.6 Sol`, `GPT-5.6 Terra`, `GPT-5.6 Luna`,
  and `GPT-5.5`.
- Those five values are neither a per-account entitlement list nor a closed
  protocol catalog. ChatGPT may publish additional models, and different plans
  expose different subsets. Static validation against the screenshot is
  therefore incorrect.
- The current clean `chatgpt2api` reference fetches authenticated models from
  `GET /backend-api/models?history_and_training_disabled=false`, parses each
  `models[].slug`, and builds its public `/v1/models` catalog by account type.
  Sub2API should apply the same dynamic capability principle per Web account.
- The reference caches successful catalogs for 300 seconds, unions capabilities
  across active account classes, routes a requested model only to classes that
  advertised it, and retains the last successful catalog for a class when a
  refresh fails. Sub2API already has group/account scheduling and model-list
  caching, so it should reuse those boundaries rather than copy the Python
  account-class abstraction literally.
- Sub2API's `AccountTestService.fetchUpstreamModelList` and
  `SyncUpstreamModelCatalog` already form the reusable authenticated model-sync
  path. The parser accepts a top-level `models` array and `slug` fields, but the
  sync currently persists only a capability-metadata snapshot after all
  context/reasoning fields are complete. ChatGPT Web needs a separate persisted
  ID catalog because its browser response is authoritative for entitlement even
  when those API-style capability fields are absent.
- The account repository supports atomic `UpdateExtra`, and every schedulable
  account read includes `Extra`. A Web ID catalog stored there can feed the
  existing group model aggregation, composite ownership, and account
  `IsModelSupported` checks without storing access tokens in caches or logs.
- The reported administrator account-test failure is local validation evidence:
  a Web connectivity test inherited `max_tokens`, but the private Web transport
  intentionally rejects that public field. The dedicated Web test request must
  omit it, while a real public client request that supplies the field should
  still receive a field-specific 400 response.
- `writeOpenAIModelsList` already preserves arbitrary model ids and synthesizes
  metadata for new slugs, so the public serializer can expose future Web models
  without a source update. The missing piece is dynamic per-account discovery,
  caching, and scheduling capability rather than a larger static list.
- The completed dedicated Web account-test constructor is still valid: it emits
  only model, stream, and one user text message and therefore fixes the reported
  accidental `max_tokens` rejection. Its fixed five-item selector source must
  be replaced by live account capabilities.

## 2026-09-04 Plus-plan HAR comparison

- The Plus HAR contains a successful authenticated
  `GET /backend-api/models?iim=false&is_gizmo=false&supports_model_picker_upgrade_presets=true`
  response with 16 model entries and `default_model_slug=gpt-5-6`. This is
  stronger evidence than the earlier screenshot or a static catalog.
- Display labels and wire slugs differ. The selector label `GPT-5.6 Sol` is
  represented by multiple slugs, and captured conversations use
  `gpt-5.6-sol-wm`; Terra uses `gpt-5.6-terra-wm`. Other returned slugs include
  instant, thinking, mini, and research variants. The adapter must expose and
  pass the upstream `slug` verbatim and remain open to future entries.
- The Plus catalog exposes `max_tokens` as per-model metadata, but successful
  prepare/final conversation payloads do not send a `max_tokens` request field.
  OpenAI-compatible output-limit fields therefore cannot be forwarded into the
  Web payload. For compatibility they should be consumed/ignored on the Web
  path rather than causing the reported account test to fail.
- The Plus text turn and file turn both use `thinking_effort=min` and a `-wm`
  model slug. Prepare state/dispatch varies by UI event (`none`, `sent`, or
  `success`; `debounced` or `immediate`), while the final conversation request
  remains `client_prepare_state=success`.
- The Plus attachment flow is three-stage: `POST /backend-api/files`, direct
  `PUT` to an `oaiusercontent.com` upload URL, then
  `POST /backend-api/files/process_upload_stream`. The final user message keeps
  ordinary `text` content and puts the uploaded file only in
  `metadata.attachments`, with id, library id, name, size, source, and MIME
  fields. This confirms non-image files must not become image pointers.
- The Plus conversation SSE in this capture hands off through resume/topic
  events instead of carrying the full assistant text in the initial HTTP body.
  The existing transport must retain its resume-stream handling for these
  accounts.

## 2026-09-04 release resume verification

- Re-parsed the supplied HAR with a credential-free field inventory. Text
  prepare requests use `client_prepare_state=none`, `debounced` dispatch, and
  `partial_query`; file-picker prepare requests use `success`, `immediate`
  dispatch, `attachment_mime_types`, and omit `partial_query`.
- The final conversation request in every captured turn uses
  `client_prepare_state=success`; Sentinel finalize uses `prepare_token`,
  `proofofwork`, and `turnstile`. The current adapter matches these shapes.
- The existing release archive is older than the latest source edits and is
  excluded from deployment. A new context must be built and hash-checked
  before upload.
- The planning catch-up helper remains unusable because its installed script
  raises `NameError: bi is not defined`; state was recovered from the plan,
  findings, progress, and current worktree instead.

## 2026-09-04 Plus HAR implementation evidence

- The authenticated Web catalog is an open-ended upstream capability list. Preserve every valid non-empty `models[].slug` exactly as returned, persist it per account, and synthesize only `auto` as the conservative default/fallback.
- The captured ordinary-file create request uses `use_case=ace_upload`, `supports_direct_azure_multipart=true`, `entry_surface=chat_composer`, `selection_method=file_picker`, and `mime_resolution_source=none`, together with library persistence fields.
- After the signed blob PUT, the captured completion request is `POST /backend-api/files/process_upload_stream` with `file_id`, `use_case=ace_upload`, `index_for_retrieval=false`, the original `file_name`, and library metadata. Its response is newline-delimited JSON ending in `file.processing.completed` with `extra.metadata_object_id`.
- Current message serialization already has the required MIME boundary: only `image/*` produces `image_asset_pointer`; non-image files remain ordinary `text` and are represented only in `metadata.attachments`.

## 2026-09-05 Plus HAR follow-up

- The captured Plus model-picker request is `GET /backend-api/models?iim=false&is_gizmo=false&supports_model_picker_upgrade_presets=true`; this is the query used for the Web entitlement catalog and is now the discovery path.
- Captured prepare and conversation requests use `conversation_origin:"tpp"` and `model_response_contracts:[{"id":"photo_upload_action.v1","protocol_version":1,"presets":["cap:image","cap:file","placement:end"]}]`.
- Captured user message metadata always includes `selected_sources:[]` alongside `serialization_metadata`; the adapter now emits that neutral field without copying HAR-specific experiment identifiers.
- Focused regression tests now assert the exact model-discovery query and the Plus contract fields while retaining dynamic verbatim model slugs.

## 2026-09-04 Web parameter compatibility

- The Web adapter never serializes Chat/Responses sampling fields into the private conversation payload. The reported `temperature` 400 came from the adapter rejecting non-default values before that omission could happen.
- Valid `temperature` (0..2) and `top_p` (0..1) values are now accepted for OpenAI-client compatibility and dropped at the Web boundary; invalid ranges still produce a field-specific `invalid_request_error`.
- `parallel_tool_calls` is harmless when no tools are bridged and is accepted; tool definitions, non-text structured output, stop sequences, stateful Responses fields, and non-default service tiers remain explicitly rejected because they cannot be represented equivalently by the classic Web API.
- The Plus HAR records the lowest private Web reasoning selector as `thinking_effort=min`; `reasoning_effort=minimal` is normalized to that value rather than rejected.
- Safe compatibility fields now accepted and omitted at the Web boundary are token limits, valid sampling values, stream options, no-op tool choices when no tools are declared, `parallel_tool_calls`, `include`, `prompt_cache_key`, valid reasoning-summary preferences, and valid text verbosity preferences.
- Behavior-changing fields still rejected are non-empty stop sequences, actual tools/functions and their call history, non-text structured output, `store=true`, `previous_response_id`, and non-default service tiers. Invalid model slugs, sampling ranges, token limits, reasoning values, summaries, and verbosity values remain field-specific 400 errors.

## 2026-09-04 release 7135952 final state

- GitHub `main` and local `HEAD` both resolve to commit `71359527417b066b7d6808394baecdb47b020795`.
- Port 9999 runs `local/sub2api:web-attachments-9999-7135952` with image ID `sha256:a22c4616cde21cd0d102116f671346c4d694cd4599721c630e8d7a1c2410347a`; Docker reports it healthy and running.
- The remote deployment manifest records the commit, image, image ID, and context SHA-256 `0f64553fe7397c42f6e5896de365131dc1f35e5cbd6a3d7f3ec2530639245908`.
- Health checks for ports 9999, 10000, and 10001 return HTTP 200. The 10000 and 10001 application container IDs still equal their recorded baselines.
- The exact remote build directory and uploaded archive for release 7135952 were removed after verifying their resolved paths.
- The live TXT attachment attempt reached ChatGPT Web but received an upstream 429. This is quota/rate-limit evidence rather than a parameter or attachment-shape rejection; PNG/PDF/TXT/ZIP protocol tests remain the non-live format coverage.

## 2026-09-04 Web tool-calling design outcome

- WebCodex can provide schema discovery, authorization, execution, Runner dispatch, and result transport after a tool call has been selected. It does not provide the missing model-to-OpenAI-tool-call conversion for a ChatGPT Web access-token request.
- The recommended first phase is a client-executed compatibility bridge: inject a constrained private tool protocol, buffer and validate the Web response, emit standard Responses/Chat tool calls, and accept correlated client-executed results in the next request.
- Server-side WebCodex execution remains a separate opt-in mode because it requires an endpoint, credential, project, tool allowlist, and execution/approval policy in addition to the Web access token.
- The initial design is tracked in `docs/CHATGPT_WEB_TOOL_CALLING_DESIGN.md`; it does not claim runtime tool support before implementation and live verification.

## 2026-09-04 bulk transport selector gap

- `frontend/src/components/account/BulkEditAccountModal.vue` is the existing bulk account editor and already contains OpenAI-specific batch controls, but it has no control for `extra.openai_transport`.
- `frontend/src/components/account/EditAccountModal.vue` already provides the canonical single-account Web/Codex selector and is the source for field values and visible wording.
- The backend entry contract is `BulkUpdateAccountsRequest` in `backend/internal/handler/admin/account_handler.go`; the implementation must merge the transport key without replacing unrelated per-account Extra fields.

## 2026-09-05 WebSocket handoff evidence

- The Plus HAR's `/backend-api/f/conversation` response is a short SSE handoff, not the assistant stream. It contains `resume_conversation_token` and `stream_handoff` with matching `resume_sse_endpoint` and `subscribe_ws_topic` topic IDs.
- The user WebSocket URL is obtained from `GET /backend-api/celsius/ws/user` and points at `wss://ws.chatgpt.com/...`.
- An independent protocol implementation confirms the subscription command is an array frame: `[{"id":1,"command":{"type":"subscribe","topic_id":"conversation-turn-..."}}]`.
- Topic frames are arrays. Relevant messages have `type:"message"`, matching `topic_id`, and a payload envelope `type:"conversation-turn-stream"`; its payload contains `type:"stream-item"` with an SSE-encoded `encoded_item`, or `type:"done"`.
- The Web transport must consume the initial handoff `[DONE]`, subscribe to the topic, and feed each `encoded_item` back through the existing SSE-to-Responses converter. Treating the handoff as terminal is the direct cause of HTTP 200 empty responses for newer Web model slugs.
- Existing focused coverage lives in `frontend/src/components/account/__tests__/BulkEditAccountModal.spec.ts` plus account service and handler tests.

## 2026-09-04 protocol-specific rate-limit audit

- `accounts.extra.model_rate_limits` is already persisted and cached as a scope map; adding transport scopes avoids a migration and preserves existing model scopes.
- `RateLimitService.handle429` currently sends every OpenAI OAuth-like account through Codex header/snapshot logic and the same-account OAuth retry window.
- ChatGPT Web's observed 429 body (`messages per hour`) has no Codex headers or reset timestamp, so it needs body classification plus a Web-specific fallback window.
- The scheduler's `isAccountRequestCompatibleReason` is the single filter-stat veto point for both initial and recheck paths; adding transport-scope checks there will expose `web_rate_limited` and `codex_rate_limited` counts.
- Existing repository `SetModelRateLimit`/`ClearModelRateLimits` updates JSONB and refreshes scheduler cache, so transport scopes can use the same path. Success recovery must clear only the matching transport scope.

## 2026-09-04 protocol-specific rate-limit completion

- Added `openai_transport_web` and `openai_transport_codex` to the model-rate-limit key set used by `IsSchedulableForModelWithContext`.
- This closes the legacy `GatewayService` sticky/routing path that did not call the newer protocol diagnostic helper; dedicated Web/Codex limits now veto every scheduler entry point.
- Cross-protocol tests confirm a Web account ignores a Codex scope and a Codex account ignores a Web scope, while the active protocol is reported as `web_rate_limited` or `codex_rate_limited`.
- Focused service tests and the service package compile pass with the existing repository cache. The first uncached compile exhausted the D: drive and was not treated as a code failure.

## 2026-09-05 Prompt Tool switch and request-body regression

- The current Web forwarding functions construct `OpenAIWebPromptTools`, then
  populate `chatReq.Tools` with the normalized definitions before calling the
  Web transport. This defeats the prompt bridge because the private Web body
  validator still sees native tools. The normalized definitions must remain
  only in `PromptTools`; the Web request object must have tools/functions and
  related choice fields cleared before serialization.
- `BuildConversationPayloadWithOptions` validates the request but the generated
  private payload has no native `tools` field. A regression test should assert
  that a prompt-enabled request's serialized body contains the injected prompt
  instruction and does not contain native tool keys or tool definitions.
- The admin setting is represented as `*bool` on PUT and `bool` on GET. The
  update handler now echoes the persisted value, so a false request should be
  preserved through PUT and GET; an explicit false round-trip test is still
  required to catch frontend toggle regressions.

## 2026-09-05 final Prompt Tool verification

- `d9cd8e6` removes the last two forwarding assignments that copied normalized
  Prompt Tool definitions back into `chatReq.Tools`.
- `BuildConversationPayloadWithOptions` now works on a request-local copy and,
  when Prompt Tools are active, clears all native Web-rejected tool fields
  before serialization. The original public request remains unchanged for
  response conversion and billing.
- The service regression asserts the serialized Web body contains the injected
  protocol instruction but none of `tools`, `functions`, `tool_choice`,
  `function_call`, or `parallel_tool_calls`. The frontend regression covers
  enable, disable, save, and reload; the focused suite passed 39/39 before
  deployment.
- The deployed admin API was exercised on the server itself. The setting
  persisted and echoed both boolean states correctly, which rules out the
  earlier UI symptom where a successful save was overwritten by a false PUT
  response.
- A live Web account was not available in the isolated 9999 database during
  this pass. Therefore no claim is made about an upstream ChatGPT response;
  the remaining live-test prerequisite is importing or enabling a Web account.

## 2026-09-05 server-side 413 diagnosis

- Read-only SSH inspection of `/root/sub2api-deploy-9999` confirms the running
  container is healthy and uses `local/sub2api:web-attachments-9999-d9cd8e6`.
- The deployed `.env` sets both `SERVER_MAX_REQUEST_BODY_SIZE` and
  `GATEWAY_MAX_BODY_SIZE` to `268435456` (256 MiB). The reported requests are
  therefore not rejected by Sub2API's HTTP body limit.
- In the last six hours the container recorded 130 gateway audit requests;
  59 had an inbound body of at least 50 KiB and the maximum was 162562 bytes.
  The failing requests entered `/v1/responses` with body sizes such as
  154376, 155972, and 162562 bytes, then received upstream HTTP 413.
- Upstream 413 failover events were distributed across Web account IDs 31, 32,
  and 33 (9, 10, and 10 events). The final client-side 413 is emitted only
  after the account failover loop exhausts the pool; `no available OpenAI
  accounts` is a secondary routing message, not the root cause.
- `ops_error_logs` preserves the upstream body for these events:
  `{"error":{"message":"你提交的消息过长，请修改后重新提交。","type":"upstream_error"}}`.
  This identifies the ChatGPT Web conversation/message-length limit rather
  than a raw HTTP request-size limit.
- The source path matches the log behavior: `buildConversationPayload()` in
  `backend/internal/service/openai_web_transport.go` reserializes the full
  request history, Prompt Tool instruction, and attachment metadata into the
  private Web conversation body; `openai_gateway_upstream_errors.go` classifies
  a non-context-window 413 as `openai_request_body_too_large` with account
  scope and `NextAccountRetry`.
- Small requests continue to succeed on the same deployment (for example,
  299/300-byte Responses requests returned upstream 200), so this is size
  dependent and not a blanket access-token or model-authentication failure.

## 2026-09-05 Astra WebSocket stall diagnosis

- The live 9999 container was still `local/sub2api:web-attachments-9999-c1631d1`.
- Requests for `gpt-6-astra-wm` selected Web account 36 and produced no client-visible
  bytes until the generic 183-second stream interval timeout; `auto` on the same
  account completed normally.
- The Plus HAR confirms the Web conversation HTTP response is only a prelude:
  `resume_conversation_token`, `stream_handoff`, and `data: [DONE]`. The answer is
  delivered on the authenticated `/backend-api/celsius/ws/user` topic.
- HAR telemetry records the WebSocket completion as `ws stream summary` with
  `reason:"done"`, `stream_item_count`, followed by `Turn exchange complete`,
  `chatgpt_convo_stream_completed`, and `chatgpt_turn_finish`. The HAR export does
  not include raw WebSocket frames, so the exact frame envelope must remain tolerant.
- The local fix adds a 60-second per-read WebSocket deadline, supports object and
  array envelopes/camelCase fields/nested payloads, deduplicates anonymous stream
  items by SHA-256, and recognizes done/finished/completed terminal states. Timeout
  errors expose only frame counters and the last frame type.
- Focused local verification passed after reusing the existing Go build cache:
  `TestOpenAIWebTopicBody*`, `TestOpenAIWebTransportDoSwitchesHandoffToUserTopic`,
  `TestAccount_OpenAIWebSupportsCurrentModelCatalogIgnoringLegacyMapping`,
  `TestResolveCompositeModelOwnershipDerivesWebCatalogFromWebAccount`, and
  `TestAccountOpenAIWebModelCatalogSnapshotControlsSupport` all passed. The
  remaining full-build check is intentionally delegated to the remote Docker
  host because the local D: volume is nearly full.

## 2026-09-05 Web model/mode 422 diagnosis

- The reported failure is an authenticated upstream validation response from ChatGPT Web's `/backend-api/f/conversation`, distinct from the previously fixed WebSocket `not_connected` failure.
- Required transport compatibility includes both legacy direct SSE and the newer `stream_handoff + WebSocket` path. Since the 422 is returned by the conversation POST before either stream reader begins, the fix belongs in request/model resolution and must not remove either reader.
- The test group contains two usable Web accounts intended to cover the two upstream stream variants. Validation should target each account deterministically rather than rely only on scheduler choice.
- Account 36 is an OAuth Web account with an 18-selector Plus catalog, `default_model_slug=gpt-5-6`, and all observed `-wm`/Astra selectors. Account 37 is a setup-token Web account with a smaller ten-selector catalog and `default_model_slug=auto`.
- This account split makes the public `auto` selector ambiguous across the scheduler pool. Account-specific admin tests can target `/api/v1/admin/accounts/:id/test` without changing account status or priorities.
- Accounts 36 and 37 both belong to test group 2 with equal priority. Ordinary `auto` requests are therefore nondeterministic at the account level, while the administrator test endpoint accepts an explicit account ID and model.
- The dedicated Web account test defaults an empty model to `auto` and sends it unchanged. Scheduler and forwarding helpers likewise return Web `auto` unchanged, so all three paths share the same virtual-selector behavior.
- A targeted live matrix of account 37 `auto` versus `gpt-5-6`, followed by account 36 equivalents, can distinguish an `auto` selector defect from a broader setup-token transport defect.
- Deterministic live tests establish that account 37 fails with the same 422 for both `auto` and concrete `gpt-5-6`. Account 36 succeeds for both `auto` and `gpt-6-astra-wm`, emitting content and `test_complete`.
- The defect is therefore account/mode-specific, not limited to the virtual `auto` selector and not caused by WebSocket handoff. Account 37 rejects the conversation POST before stream negotiation for every tested model.
- Direct raw model metadata retrieval cannot be used as evidence because ChatGPT returned 403 without the transport's browser fingerprint/bootstrap session. The safe path is to inspect the existing transport or extend its authenticated parser under tests.

### Two-HAR comparison

- The ordinary Web HAR has three successful `/backend-api/f/conversation` calls using `model=auto`, `conversation_mode.kind=primary_assistant`, a compact common payload, direct `text/event-stream`, and terminal `[DONE]`. It covers text, PNG, and JSON attachment turns.
- The Plus HAR has successful explicit `gpt-5.6-sol-wm` calls with the same `primary_assistant` mode, plus `conversation_origin=tpp`, `model_response_contracts`, and `thinking_effort=min`; the HTTP response is a `stream_handoff` prelude with `[DONE]`, followed by the authenticated WebSocket stream.
- Current Sub2API unconditionally emits the Plus-only fields for every Web request and additionally emits `force_paragen`, `force_paragen_model_slug`, `force_rate_limit`, `force_use_sse`, `history_and_training_disabled`, `reset_rate_limits`, `suggestions`, `variant_purpose`, and `websocket_request_id`, none of which appear in either captured conversation request.
- Compatibility must use a common minimal payload for ordinary models and add only HAR-observed work-model extensions for dynamically identified work-mode models. Both response readers remain independent of this request-profile decision.
- The Plus model manifest explicitly marks selectors with `is_work_mode_model`;
  observed work selectors include `gpt-5.5-wm`, `gpt-5.6-sol-wm`,
  `gpt-5.6-terra-wm`, and `gpt-5.6-luna-wm`. Ordinary selectors such as
  `gpt-5-6` and `auto` are marked false.
- Ordinary `conversation` and `conversation/prepare` requests omit
  `conversation_origin`, `model_response_contracts`, and `thinking_effort`.
  The current transport adds them unconditionally, which can make a Setup
  Token account reject an otherwise valid `auto` request with HTTP 422.

### 2026-09-05 screenshot follow-up

- The screenshot shows a nonce/schema-bound Prompt Tool object using
  `tools:[{name,arguments,...}]` instead of the current `calls:[...]` shape.
  Its arguments are structured JSON and reference a declared tool, so it can
  be normalized safely after the existing nonce, schema, name, and argument
  validation rather than rendered as ordinary assistant text.
- The administrator login response exposes `access_token` at the response root; server-local account tests can authenticate and parse it entirely in shell memory without printing credentials or the token.
- The first server-local login parser assumed a top-level token and stopped with `KeyError` before any account test request. The response shape is wrapped differently in this deployment; inspect keys only and adapt the parser.
- Root cause is not yet established; required evidence is the requested public model, resolved wire model, selected account capabilities, and the private conversation payload's mode-related fields.
- Live 9999 logs correlate repeated failures with account `37` (`setup-token`, Web transport). Both `/v1/responses` and `/v1/chat/completions` fail for public model `auto`; the upstream rejects within roughly one second, so the failure precedes WebSocket streaming.
- The scheduler repeatedly chooses the same single Web account. An explicit `gpt-5.6-sol-wm` request was separately rejected by scheduling as unsupported for the current account snapshot, confirming that per-account catalog state participates in selection.
- `buildConversationPayload` always sends `conversation_mode.kind=primary_assistant` and sends the normalized public model verbatim. Consequently public `auto` becomes private wire model `auto`.
- Model discovery persists `default_model_slug`, but the conversation builder does not consult the account snapshot. This is a concrete candidate for account-specific `auto` failures and must be checked against account 37's safe catalog fields before editing.
- Account 37's persisted snapshot contains ten selectors and reports `default_model_slug=auto`; replacing `auto` with the stored default would therefore be a no-op. The catalog contains `gpt-5-6-t-mini-mini`, demonstrating why the implementation must remain dynamically driven rather than use a closed allowlist.
- No current service code derives `conversation_mode` from model metadata. The builder hard-codes `primary_assistant`, and the prepare payload copies that same value.
- Plus HAR comparison shows a successful `gpt-5.6-sol-wm` request also uses `conversation_mode.kind=primary_assistant`. The catalog's `is_work_mode_model=true` flag must not be translated into a different conversation mode.
- The Plus HAR raw model list exposes concrete model objects and a separate `default_model_slug`; `auto` may be a selector/default rather than a concrete model object. Account 37's stored list includes `auto` because Sub2API injects it during normalization, not necessarily because upstream advertised it as a model entry.
- Account 37 credentials contain only `access_token` and `model_mapping`; there is no account-side conversation-mode override to explain the 422.
- The isolated browser reached the deployed login page normally. Administrator credentials will be used only in process memory for the UI reproduction and will not be printed or stored in the repository.
- Prompt Tool UI currently binds the setting directly with `v-model=form.enable_openai_web_prompt_tools`; backend DTO/update mappings and existing frontend tests for a true-to-false reload cycle are present. The reported click failure therefore requires an actual deployed interaction check rather than another DTO-only fix.
- `saveSettings` explicitly includes `enable_openai_web_prompt_tools` in its update payload, and the current test toggles the rendered input from true to false before submit. No omission has been found in the page payload construction.
- The next frontend evidence needed is the shared `Toggle` component's actual click target and a deployed browser interaction. A unit-level `setValue` may bypass a real pointer/label issue.
- The real shared Toggle is a `type=button` with a direct click handler that emits the inverse boolean. It has no disabled state or overlay. The Settings test replaces it with a checkbox stub, so the existing test does not exercise the production component.
- After saving, `SettingsView` assigns all non-null fields from the update response back into the form. A stale/incorrect response can therefore visibly revert the switch even when the click itself worked; browser network evidence is needed to distinguish this from a raw click failure.

## 2026-09-05 interruption follow-up

- The current 9999 service is healthy and recent Web `/v1/responses` requests complete with upstream `200` and `response.completed`; the visible interruption is not a container crash.
- The server recorded repeated `/v1/chat/completions` requests for `gpt-6-astra-wm` that failed locally before the Web request with `tool "create_workflow" schema: $.additionalProperties=true is not allowed in strict mode`.
- A separate stream request ended with `context canceled` after an upstream `200`/`[DONE]`, which indicates the downstream client closed the response after the tool/schema failure or an incompatible stream result.
- The Web Prompt Tool bridge currently validates every tool as strict and rejects root `additionalProperties:true`, even though OpenAI Chat Completions tools are non-strict by default and dynamic schemas rely on that field.
- The parser also only attempts an envelope when the entire assistant text starts with `{`; Web models may prepend a short explanation or markdown fence before a valid nonce/schema-bound envelope.
## 2026-09-05 interruption diagnosis

- The live `sub2api-9999` container is healthy and still runs image
  `local/sub2api:web-attachments-9999-603483b`.
- Recent request logs show Prompt Tool requests failing before upstream with
  `tool "automation_update" schema: $.additionalProperties=true is not allowed in strict mode`
  and the same error for `test_tool`. These are request-level 400s from the
  deployed pre-fix validator.
- Web requests for `gpt-6-astra-wm` also received upstream HTTP 422
  `Invalid conversation body`; the later `context canceled` entries are
  downstream cancellation after the failed/slow request, not a process crash.
- Ordinary `auto` requests on account 36 completed with upstream 200 and
  `response.completed`, confirming the service itself remains alive.

## 2026-09-05 Web conversation continuity design

- `OpenAIWebConversationOptions` already accepts `ConversationID` and `ParentMessageID`, but both Web forwarding paths currently call `Do` without either value.
- The Web transport creates a random parent message ID when no parent is supplied and includes the conversation ID only when explicitly provided. Its response reader can parse `conversation_id`, but the forwarding layer does not persist it for the next request.
- The Web adapter serializes the complete converted `chatReq.Messages` on every call. The existing `session_hash` is an account-affinity key and does not reduce request history.
- Codex-compatible response/turn state is not a safe substitute: Web sessions are account-bound and the Web transport explicitly rejects `previous_response_id`.
- The continuity key must be caller-scoped and stable. Preferred sources are an explicit session header or request session metadata, then `prompt_cache_key`; a hash of changing full history is only a fallback for one request and must not become a long-lived Web conversation identity.
- A stored Web conversation is reusable only when account ID, group ID, protocol, model profile, and the caller's last-turn fingerprint all match. Any account failover, model/mode change, TTL expiry, attachment/tool turn, or mismatch must clear the state and send a complete history.
- The Plus HAR's HTTP handoff response exposes `conversation_id` and `turn_exchange_id`, while the captured HAR does not include WebSocket message frames. The implementation must therefore persist the conversation ID from the handoff/stream and accept a missing assistant message ID as a safe non-reuse result rather than inventing a parent ID.

## 2026-09-06 Web conversation continuity implementation

- The gateway now persists private Web cursor state only after a stable caller
  identity is present. Supported identities are the existing session/conversation
  headers, `prompt_cache_key`, metadata session IDs, or a Responses
  `previous_response_id` alias when a prior state exists.
- State is scoped by API key, group, account, and upstream model. A scheduler
  account switch cannot reuse the prior account's Web conversation because the
  account ID is part of the storage key and validation contract.
- A compatible text-only follow-up is reduced to the newest user message;
  initial turns, profile changes, attachments, tool history, and uncertain
  message ordering use full replay. Tool/attachment turns set a one-turn
  full-replay guard for the next request.
- Direct SSE and WebSocket handoff responses are parsed through the same
  Responses reader. `conversation_id` is captured from handoff/stream frames;
  the latest assistant Web message ID is captured only when an assistant
  author object proves it, otherwise state is not committed.
- The public OpenAI response ID remains a gateway identifier. It is stored only
  as an internal alias for `previous_response_id`; it is never sent upstream to
  ChatGPT Web.

## 2026-09-06 deployment verification

- Remote Docker compiled the committed backend successfully and produced image
  `local/sub2api:web-attachments-9999-99c0e94`.
- Only the isolated `sub2api-9999` application container was recreated. The
  existing `10000` and `10001` application containers were left running and
  returned healthy responses alongside `9999`.
- `9999` root and health endpoints returned 200; unauthenticated
  `/v1/chat/completions` and `/v1/responses` returned 401 as expected. Recent
  container logs contained no panic/fatal or known Web parameter/stream error
  signatures.

## 2026-09-06 context-drift audit

- The first continuity implementation only compared the newest user-message
  fingerprint. A caller could send a modified assistant answer or reorder an
  older turn while retaining the same latest user message; the gateway could
  then send only the new user message against the old Web cursor.
- The state must retain a digest of the exact prior transcript plus the Web
  assistant output. A full-history follow-up may be reduced only when the
  messages before its newest user message match that stored digest exactly.
  A one-message follow-up remains valid because it explicitly delegates prior
  context to the server-side Web cursor.
- Concurrent requests sharing one Web session must be serialized across the
  upstream request and state commit. Otherwise two turns can reuse the same
  parent message ID and fork or overwrite the upstream conversation. The
  local state store will provide a keyed lock; callers that cannot acquire it
  will fail closed instead of sending an unsafe continuation.

## 2026-09-06 context-drift hardening result

- The stored digest now covers the exact prior request transcript plus the
  assistant text emitted by the Web response. Edited assistant history and
  reordered turns therefore invalidate the private Web cursor and force a
  complete replay.
- A request carrying both a stable session identity and `previous_response_id`
  now locks and writes through the canonical session key while retaining the
  response alias. This keeps both continuation forms on the same cursor.
- Same-key turns use a keyed semaphore spanning upstream I/O through commit or
  invalidation. Cancellation before acquisition fails closed; release is
  idempotent and removes idle lock entries.

## 2026-09-06 HAR continuation alignment

- The ordinary HAR includes `partial_query` only on the initial
  attachment-free prepare. Continuation prepares use the Web conversation
  cursor; Plus work-mode prepares omit `partial_query` even on the first turn.
- Attachment continuation requests carry `conversation_id` and
  `parent_message_id` and contain only the newest attachment-bearing message.
  The gateway therefore no longer treats ordinary file/image content as an
  automatic full-replay boundary; tool calls and tool results remain one.
- When a Redis-backed state cache is configured, local hot state is never used
  as a fallback after a Redis miss or error. This prevents an instance from
  resurrecting a cursor another instance invalidated.
- The Redis lease uses a random owner token and compare-and-delete release, so
  a late release after lease expiry cannot delete a newer request's lease.
- The current lease is 15 minutes, matching the default WebSocket read budget;
  long-running turns beyond that budget are outside the gateway's normal
  request lifetime and must be retried as a complete replay after expiry.

## 2026-09-06 Cross-instance test-account import

- The live `10000` application is `/root/sub2api-deploy` (`sub2api`, port
  10000); the isolated target is `/root/sub2api-deploy-9999` (`sub2api-9999`,
  port 9999).
- The source group resolved unambiguously to `0707-phone-free`, ID 40,
  platform `openai`, with 3,827 accounts. The target group resolved to `测试`,
  ID 2, platform `openai`, with 8 accounts before the operation.
- The source selection contained 100 accounts matching platform `openai`,
  type `oauth`, status `active`, `schedulable=true`, and
  `credentials_status.has_access_token=true`. The export returned exactly 100
  account records and no proxies.
- The target import returned `account_created=100` and
  `account_failed=0`. The target account total increased from 8 to 108; the
  100 new account IDs were then bound with a bulk update to group 2, which
  returned 100 successes and 0 failures. A second group-filtered listing
  confirmed all 100 new IDs are members of the target group.
- Two target spot checks returned `test_start` followed by an error. A source
  check of the corresponding first selected account returned the same
  upstream `401` with code `token_revoked` (`Encountered invalidated oauth
  token for user, failing request`). This is an upstream credential-state
  issue in the source data, not a cross-instance serialization or binding
  failure.
- No credentials, access tokens, administrator secrets, or full export
  payloads were written to disk, logs, or repository files.

## 2026-09-06 Windows caller versus tool execution environment

- Calling `9999` from a Windows client only identifies the network caller; it
  does not transfer the Windows filesystem, process table, shell, or current
  working directory to the upstream model.
- The repository contains no backend/frontend hard-coded `/root` or
  `/home/oai` environment banner. Those paths therefore come from the
  upstream model's runtime/system context or from a client-provided agent
  prompt, not from the Windows caller's local filesystem.
- The Web Prompt Tool bridge is intentionally client-executed. It prepends an
  internal schema/nonce instruction, strips native Web `tools` fields, parses
  a valid model envelope, and emits standard Responses `function_call` or
  `custom_tool_call` events. It never runs caller-provided functions.
- A Windows-aware tool turn requires the client to receive the tool event,
  execute an approved local PowerShell/Windows tool, and send the result back
  as `function_call_output` or `custom_tool_call_output`. If the client sees
  only `output_text`/`message.content`, no local tool was executed and a
  Linux path in the prose is not evidence of Windows access.

## 2026-09-07 Web text.format compatibility

- Remote 9999 `ops_error_logs` showed HTTP 400 `parameter "text.format" is not
  supported by ChatGPT web transport` on `/v1/responses`; this is the gateway's
  Web validator, not a ChatGPT upstream rejection.
- Responses conversion copies `ResponsesText.Format` into Chat
  `response_format`, so validating only the Responses object still allowed the
  incompatible field to reach the Web transport.
- The Web adapter now accepts client-only fields alongside `{"type":"text"}`
  and removes the whole format object from the private payload.
- `json_schema` and `json_object` remain rejected without Prompt Tool. With a
  request-scoped Prompt Tool declaration they are accepted as a public
  contract, then removed before Web dispatch because the generated
  nonce/schema envelope is the private contract.
- The same normalization is applied to direct Chat Completions and
  Responses-shaped Chat Completions paths, preventing regressions in either
  public endpoint.
