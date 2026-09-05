# Progress

## 2026-09-04 Plus HAR dynamic-catalog continuation

- Restored the active planning files and current dirty worktree without retrying the known-broken catch-up helper.
- Re-parsed the supplied Plus HAR through a bounded field inventory and confirmed the exact `ace_upload` / `process_upload_stream` ordinary-file flow without emitting credential headers.
- Traced the remaining static five-model assumptions through `Account.IsModelSupported`, gateway aggregation/composite ownership, the admin model endpoint, and frontend tests. The implementation is now being changed to one account-level persisted dynamic snapshot with `auto` as the only no-snapshot fallback.

## 2026-09-04 Web `auto` continuation

- Restored the active plan, findings, progress, dirty worktree, and prior
  release evidence without repeating the broken catch-up helper.
- Traced public Chat Completions and Responses requests from handlers through
  channel checks and account scheduling to the Web transport.
- Confirmed the transport itself always sends `model: auto`; the remaining
  work is making that capability survive scheduling, model discovery, and
  composite routing, followed by a fresh isolated 9999 release.
- Added the account/model invariants: Web accounts accept only `auto`, their
  upstream and billing model ignore legacy mappings, the public model catalog
  publishes only `auto` for those accounts, and composite ownership derives
  `auto` from an actual Web account rather than the global detector.
- Superseded before testing: new UI evidence shows Web supports a dedicated
  catalog (`auto`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`, `gpt-5.5`).
  The just-added auto-only logic is being revised in place to validate and
  forward this catalog while still ignoring stale Codex mappings.

## 2026-09-04 final protocol and packaging pass

- Applied the Web turn-trace fix: conversation prepare and final conversation
  now share one `X-Oai-Turn-Trace-Id`, and prepare sends
  `X-Conduit-Token: no-token`.
- Aligned the conversation body with the supplied HAR: final
  `client_prepare_state` is `success`; text prepare carries `partial_query`,
  while attachment prepare omits it.
- Split attachment serialization by protocol family. Inline images use
  `file-service://` plus `mimeType`; arbitrary uploaded files use
  `sediment://` plus `mime_type`; explicit existing pointer schemes remain
  unchanged.
- Fresh focused backend Web/gateway/account tests pass, as does the
  `internal/pkg/apicompat` package. Frontend production build, `vue-tsc`, and
  85 focused Vitest tests pass.
- A full `internal/service` run still reports only the four pre-existing
  plugin-package installer tests (`TestPluginPackageInstaller*`); no Web test
  fails. This is recorded as an unrelated environment/test-fixture gate.
- The previous deployment archive predates these source edits and is stale;
  a new context must be generated before any remote upload.

## 2026-09-04 resumed release pass

- Restored the current state from `task_plan.md`, `findings.md`, the worktree,
  and the prior handoff. The planning catch-up helper remains broken and was
  not retried.
- Re-read the supplied HAR through structured JSON inspection without printing
  credentials or header values. Confirmed the current `/f/conversation/prepare`
  plus `X-Conduit-Token` flow and the current finalize key names.
- Chose a staged release approach: implement and test the confirmed protocol
  contract first, then build and deploy only the isolated 9999 application.
  A Node Sentinel runtime will be added only if the resulting live-stage error
  proves it is necessary.

## 2026-09-03

- Created bounded shallow checkout at `D:\WebGpt\sub2api`.
- Created task plan and findings log.
- Confirmed that existing OpenAI-compatible routes and `setup-token` forwarding
  can be reused for access-token-only web conversations. Next: run focused
  backend/frontend tests and resolve any compatibility failures.
- Official OpenAI documentation retrieval was attempted for schema cross-check,
  but this host received Cloudflare 403 responses or a timeout; focused offline
  source-level tests are the verification basis.
- Reviewed the current dirty worktree and confirmed the UI selector/access-token
  import changes are already present. Backend web transport and route-level
  conversion remain to be implemented.
- Audited the Python reference client: authenticated web chat uses `/`,
  `/backend-api/sentinel/chat-requirements/{prepare,finalize}`, then
  `/backend-api/conversation`; the response is line-oriented SSE and must be
  adapted to the gateway's Responses event parser.
- Audited the Go integration seam: `UsesOpenAICodexProtocol` is the shared
  protocol gate, and both public forwarding methods need an early web branch;
  the existing `HTTPUpstream` already carries account proxy and body lifecycle.
- Completed the attachment audit: `file_data` data URIs and existing `file_id`
  references now cover arbitrary MIME types through metadata, signed PUT, and
  upload-completion calls; PDF/TXT/ZIP/PNG tests pass and remote URLs are
  rejected. Real upstream acceptance for non-image pointers remains unverified.
- The focused Go web-transport/service tests pass. Frontend `pnpm exec` and
  `pnpm build` were blocked before compilation because this checkout's pnpm
  policy attempted an install and rejected ignored `esbuild`/`vue-demi` build
  scripts; use the already-installed local binaries for an equivalent check.

## 2026-09-04

- Recorded the final endpoint architecture constraint: preserve Sub2API's two
  existing public OpenAI routes and confine `chatgpt2api` compatibility to the
  Web-account upstream adapter and bidirectional protocol conversion.
- Rechecked the current gateway branches and confirmed they reuse Sub2API's
  established Chat Completions and Responses output handlers. Scoped the next
  implementation work to Web request validation and SSE normalization tests.
- Compared the Go transport headers/session behavior with `chatgpt2api` and
  recorded the exact browser request headers and transport-local CookieJar
  strategy. Also corrected the diagnostic assumption: a successful bootstrap
  HTML body can exceed its current 4 MiB cap before conversation begins.

- Resumed the final compatibility phase and restored the three planning files
  plus current dirty-worktree state. The bundled catch-up helper repeated its
  known `NameError`, so it will not be retried.
- Read the current official Responses and Chat Completions create references.
  They confirm that unsupported non-default parameters require an explicit
  compatibility decision rather than silent loss in the Web adapter.
- Kept the existing payload audit agent running and restarted the transport
  failure audit. A third remote-audit agent could not be created because the
  local thread store is out of disk space; the primary task will own that probe.
- Traced the deployed limit error to the non-2xx conversation error path, not
  to successful SSE conversion, then corrected that initial inference after
  source review: the conversation path discards its bounded-read error, while
  bootstrap propagates a 4 MiB overflow. A remote stage/size probe is next.
- Compared reference session construction and conversation headers. The Go
  adapter lacks the reference browser client-hint/fetch headers and has no
  cookie jar; existing uTLS support is being evaluated for direct reuse.
- Confirmed that the current Web path cannot activate uTLS: its shared OpenAI
  upstream helper always uses plain `Do`, and the account TLS switch only
  recognizes Anthropic accounts. Split ownership so the payload agent owns the
  Web adapter/tests while the primary task owns upstream routing and deployment.
- Probed ChatGPT from the remote host. The reference curl-cffi session fetched
  a 200/337,590-byte bootstrap, while plain curl received Cloudflare 403. The
  normal page is not oversized; transport fingerprint/session differences are
  material. Cleared the temporary remote token variable and probe files.
- Inspected the current uTLS implementation. Its built-in behavior mimics
  Node.js/Claude and defaults to HTTP/1.1, so it is not sufficient evidence of
  Chrome compatibility. Kept a staged validation plan: session/headers/errors
  first, Chrome ClientHello only if remote acceptance still fails.
- Found the repository's existing `req/v3` Chrome impersonation client used
  for ChatGPT privacy/account calls. It is a stronger Web transport candidate;
  the implementation must create an account/request-isolated client instead
  of sharing the existing global client cache and cookies across accounts.
- Verified that an isolated req client can still satisfy the transport's
  native `*http.Response` contract and stream SSE while supplying a private
  CookieJar plus Chrome TLS/HTTP2/header ordering.
- The payload worker has added in-progress request validation, phase-specific
  browser headers, Cookie continuity, and a bootstrap body larger than 4 MiB
  regression test. The primary task is avoiding that file until the worker
  completes and is moving to the independent admin account-test path.
- Rechecked both public gateway entry points: Web selection occurs before all
  Codex transforms, preserving Sub2API's route ownership. Started auditing the
  Web helper error boundary and admin test routing for standard 400 behavior.

- Re-tested the user-supplied access token from the remote host without a
  proxy. The reference `chatgpt2api` flow completed bootstrap, sentinel, and a
  real `OK` conversation.
- Re-tested both OpenAI-compatible endpoints through `127.0.0.1:9999` with
  `gpt-5.6-luna`; both returned HTTP 502 after successful gateway authorization
  and account selection.
- Queried the matching error rows. Both record `ChatGPT web response body
  exceeds limit` at the Web conversation endpoint. No remote configuration or
  production code was changed, and all temporary shell secret variables were
  cleared before disconnecting.

- Restored the deployment context from the planning files after the bundled
  session-catchup script failed with a `NameError`.
- Audited the remote host read-only: port 9999 and the target directory are
  free, existing 10000/10001 instances are healthy, and capacity is adequate.
- Added a backend-only remote Docker build that embeds the already-generated
  frontend and inherits the pinned production runtime image.
- Confirmed the remote firewall must be updated for public port 9999 access;
  no firewall or service changes have been made yet.
- Uploaded an 8.4 MB SHA-256-verified context and built the amd64 image
  `local/sub2api:web-attachments-9999` successfully on the remote host.
- Created `/root/sub2api-deploy-9999` with independent application,
  PostgreSQL, and Redis storage plus rotated service secrets, then started all
  three containers healthy.
- Added the exact UFW rules for 9999/tcp. Public `/health` returns 200 and both
  OpenAI-compatible endpoints return 401 without a Sub2API API key rather than
  404.
- Rechecked 9999, 10000, and 10001 health as 200, verified isolated mounts and
  unchanged existing container IDs, and removed the remote build artifacts.
- Final release review found that manual access-token account creation did not
  explicitly persist `extra.openai_transport=web`. The source and focused test
  are fixed, so the first healthy 9999 image is treated as provisional and
  must be rebuilt before completion.
- Resumed from the completed gateway routing implementation. Focused Chat and
  Responses web-routing tests were reported passing; deployment packaging and
  runtime verification remain active.
- Confirmed that local Docker is unavailable. The existing backend-only
  Dockerfile has the required embed build and pinned runtime base; the remote
  host will be used for the actual image build.
- Focused frontend unit tests passed (80/80), `vue-tsc --noEmit` passed, and
  focused backend web-routing tests passed. Exact hashed-bundle inspection also
  confirmed the prepared tarball contains the new transport selector labels.
- The service package compile gate (`go test ... -run '^$'`) and focused
  `internal/pkg/apicompat` conversion tests also passed.
- Fixed direct OpenAI web access-token creation so the new setup-token account
  explicitly selects web transport. Its focused component test (28/28) and
  frontend typecheck pass; embedded assets and deployment context now need to
  be regenerated before final rollout.
- Resumed the task and reconciled the earlier deployment record with the later
  frontend default-mode fix. Started parallel frontend behavior review; next
  steps are source diff validation, fresh embedded build, remote image update,
  and end-to-end health/API verification.
- Current `git diff --check` passes at commit `b1748c4`. A concurrently prepared
  `.tmp-sub2api-9999-context-v2.tar.gz` is present and must be inspected for the
  latest frontend marker and all untracked transport sources before upload.
- Inspected the `v2` archive: its core web transport source and current embedded
  account UI are present. The remaining frontend acceptance item is ensuring
  that Web mode disables or removes incompatible Codex-only account settings.
- Confirmed the edit form currently renders Codex namespace, hosted-image,
  WebSocket, CLI-only, fingerprint, and compact controls in Web mode. The save
  path also writes the OAuth WebSocket selection, so the fix must force those
  transport keys to `off`/`false` when `openai_transport=web` and cover this in
  the focused edit-modal test.
- Located the backend defense point: both
  `IsOpenAIResponsesWebSocketV2Enabled` and
  `ResolveOpenAIResponsesWebSocketV2Mode` currently honor stale WS fields for a
  Web account. They should short-circuit to disabled/off before reading Extra.
- Rebuilt the embedded frontend after the web-default fix and audited the v2
  release context. Its SHA-256 is
  `256F208BAA1A76B538305EE2419F3A4420E372B3C0C66E382A4065D4E774278D`;
  required gateway files and the rebuilt account bundle are present, and
  credential/runtime paths are excluded.
- Added the backend web-transport invariant for Responses WebSocket capability
  and effective mode. Focused account and gateway tests pass. This makes the v2
  archive stale; a final archive must be generated after the concurrent UI
  audit finishes.
- Completed the frontend mode-isolation audit, rebuilt production assets, and
  created the frozen final deployment context. It has no `.env`, runtime data,
  Git metadata, vendor tree, or Go tests; required sources and UI markers were
  independently confirmed after packaging.
- Completed the UI transport isolation: Web mode hides Codex-only settings and
  clears their active Extra fields on save, while API-key behavior remains
  unchanged. Focused EditAccountModal tests pass (53/53), together with
  `vue-tsc --noEmit`, ESLint, and `git diff --check`.
- Built and deployed the frozen final context as
  `local/sub2api:web-attachments-9999-r3`. Only the 9999 application container
  was recreated; its PostgreSQL and Redis container IDs remained unchanged.
- Final internal and public acceptance passed: health/root return 200, both
  OpenAI routes return protected 401 responses without a key, final frontend
  assets are served, 10000/10001 remain healthy with unchanged container IDs,
  mounts are isolated, UFW permits 9999, and the startup error count is zero.
- Recorded the final context hash, image ID, and asset names in the remote
  deployment manifest. Kept r1/r2 images and Compose backups as rollback
  points, removed remote build/upload files, and moved local archives out of
  the workspace.
- Verified the 10001 administrator credentials, synchronized the same email and
  password to the 9999 `.env` and admin database row, and confirmed login
  status 200 on both ports. The secret itself is intentionally omitted from
  project records.
- Completed the Responses attachment bridge for top-level files and
  `input_image.file_id`; the full `internal/pkg/apicompat` test package and
  `git diff --check` pass using D-drive Go cache/temp directories.
- Added the first Web transport hardening slice: 32 MiB bootstrap allowance,
  reference browser headers, a transport-local CookieJar, and tests for cookie
  continuity plus a current-size HTML shell. Parameter validators now reject
  unsupported Chat/Responses semantics with field-specific request errors.
- Finished the assigned transport/bridge slice and froze code edits. The
  focused Web transport service tests, full `internal/pkg/apicompat` tests, and
  `git diff --check` pass. Production requests now prefer a per-transport
  req/v3 Chrome client after plugin opt-out; injected upstream tests remain
  unchanged. Gateway HTTP-400 mapping and live deployment remain owned by the
  primary task.
# 2026-09-04 continuation: r4 deployment acceptance

- Resumed from the deployed `local/sub2api:web-attachments-9999-r4` state.
- Confirmed the official OpenAI Responses and Chat Completions create-reference pages return HTTP 200; they remain the downstream contract for the deployment smoke matrix.
- Started parallel audits for the deployed no-proxy smoke path and the remaining Turnstile/Sentinel difference against the local `chatgpt2api` reference.
- No secret value is being written to source, planning files, or command output.
- Replaced the stale local reference baseline with a clean GitHub shallow clone at commit `dc105e51bd486bd75c8ef4f74be4bc4724bdfc33`; the historical customized checkout remains untouched.

## 2026-09-04 latest-release continuation

- Restored `task_plan.md`, `findings.md`, `progress.md`, the current diff, and
  the live agent state. The protocol patch worker remains active; three older
  review workers ended under provider rate limits and produced no trusted
  completion result.
- Kept the release scope on the isolated port-9999 instance and preserved the
  existing dirty worktree. The next gate is the current HAR prepare/conduit/
  finalize contract, followed by focused tests and remote no-proxy acceptance.
- Rechecked the current untracked Web transport and tests. They still encode
  the legacy finalize field names and do not yet contain the conversation
  prepare/conduit flow; no edit was made while the protocol worker owns them.
- The protocol worker began landing its patch after that observation: the
  transport now declares `/backend-api/f/conversation/prepare` and new helper
  entry points. Treat the earlier source snapshot as superseded and wait for
  the worker's test result before review.
- Confirmed `git diff --check` remains clean. The existing 6.0 MB release
  archive predates the active protocol patch and is explicitly stale; it must
  not be uploaded. The backend-only Dockerfile still rebuilds the embedded Go
  binary on the remote amd64 host from the supplied context.

## 2026-09-04 release resume

- Revalidated the HAR prepare/conduit/finalize field matrix and confirmed the
  current adapter and focused tests match it.
- Recovered state without the broken catch-up helper; no source changes were
  made during the read-only protocol audit. The next action is a fresh build
  context followed by an isolated `sub2api-9999` application restart.
- The direct installed-binary frontend build, focused Vitest suite (85/85),
  Web service tests, `apicompat`, typecheck, and `git diff --check` passed.
- A first staging archive was rejected by its own audit because one backend
  temporary directory was copied; it was not uploaded. The replacement package
  will exclude every dynamically discovered `.tmp*` directory and `bin`.

## 2026-09-04 remote matrix probe

- Added `tools/test_deployed_web_access_token_matrix.py` as a disposable,
  direct-to-host regression probe. It creates a temporary OpenAI Web group,
  account, and API key, covers both public endpoints in streaming and
  non-streaming modes plus PNG/PDF/TXT/ZIP data-URI attachments, and cleans up
  the temporary objects in `finally`.
- The outer script and embedded remote Python code both pass AST/bytecode
  syntax checks. The probe disables Python proxy discovery on the server.
- Read-only follow-up located the deployed `chatgpt2api` checkout and container
  runtime, summarized the attachment-stage logs, and confirmed all three
  service ports remain healthy. It also proved the probe's normal account
  DELETE is soft and leaves the temporary access token in the deleted row; no
  cleanup mutation was attempted under the frozen remote scope.
- Executed the disposable matrix against r8a with an access token supplied only
  through process input. All four text variants passed with `model: auto`, and
  all temporary remote objects were deleted successfully.
- Classified the attachment failures before further deployment: the PNG
  fixture used by the matrix was corrupt, while non-image files were being
  serialized as image pointers. Re-parsed the HAR to establish the correct
  metadata-only shape for ordinary files.

## 2026-09-04 multi-model Web fix

- Restored the plan, findings, progress, dirty-worktree status, and current
  subagent state. The bundled catch-up helper repeated its known `NameError`,
  so context recovery used the planning files and Git status directly.
- Inspected the supplied screenshot and confirmed five Web selector values:
  `auto`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`, and `gpt-5.5`.
- Stopped the stale auto-only audit direction. Split the remaining work into
  model routing/catalog, dedicated Web account testing, and transport payload
  verification while retaining primary ownership of the isolated deployment.
- Verified the latest reference implementation passes its `model` argument
  unchanged through `stream_conversation` into `_conversation_payload`.

## 2026-09-04 model and account-test resume

- Recovered the active plan and dirty-worktree state without running the known
  broken catch-up helper.
- Re-inspected the user screenshot and recorded its five visible Web model
  choices as evidence; no credential value was copied into project records.
- Confirmed the remaining release gates are the two active model/account-test
  patches, focused local verification, a fresh secret-free build context, and
  isolated replacement plus server-local acceptance of port 9999.
- The first model-transport patch completed a five-item static catalog, request
  passthrough, response metadata, and focused tests. User clarification
  superseded the static catalog: Web model availability must be discovered from
  the authenticated upstream list and remain open to future slugs.

## 2026-09-04 Plus HAR evidence

- Parsed the new Plus HAR using a field-whitelisted structured reader; no
  cookie, authorization header, upload URL, or token value was written to the
  project records.
- Confirmed a 16-entry live Web model catalog, exact `-wm` wire slugs, Plus
  subscription evidence, successful text and ordinary-file turns, and the
  three-stage file upload flow.
- Updated the implementation direction from fixed names to verbatim upstream
  slugs and from rejecting output-limit compatibility fields to omitting them
  from the private Web request.

## 2026-09-04 parameter compatibility patch

- Changed Web validation to accept valid Chat/Responses sampling controls and
  omit them from the private conversation payload.
- Added range checks and tests for `temperature`/`top_p`; mapped
  `reasoning_effort=minimal` to the HAR-observed Web value `thinking_effort=min`.
- Expanded both public-route tests to cover sampling, all three token-limit
  names, no-op tool controls, cache/include hints, and reasoning/text options.
  Focused Web gateway/account tests and the full `apicompat` package pass.

## 2026-09-04 release 7135952 deployment closeout

- Confirmed local `HEAD` and GitHub `main` are commit `7135952`; tracked files have no pending changes.
- Verified the already-deployed 9999 application uses image `local/sub2api:web-attachments-9999-7135952`, matching image ID `sha256:a22c4616cde21cd0d102116f671346c4d694cd4599721c630e8d7a1c2410347a` and reporting healthy/running.
- Atomically appended the release commit, context hash, image, and image ID to `/root/sub2api-deploy-9999/deployment-manifest.txt`.
- Removed only `/root/sub2api-build-7135952` and `/root/sub2api-9999-context-7135952.tar.gz` after exact resolved-path checks, then verified both are absent.
- Rechecked HTTP health on 9999, 10000, and 10001 as 200. Existing 10000/10001 application container IDs remain unchanged from the deployment manifest baselines.
- The first local helper launch used a nonexistent repository `.venv`; a second non-TTY launch lacked stdin. Neither connected to the server. The successful run used the verified system Python and hidden terminal input.

## 2026-09-04 Web tool-calling design record

- Reviewed Codex task `01a06b8a-c459-79c2-9c25-687f49d5987d` and the local WebCodex source. WebCodex is an MCP/REST/OpenAPI execution runtime and Runner architecture, not a model tool-call generation adapter.
- Confirmed the current Web transport explicitly rejects tool declarations, tool selections, tool-call history, and tool-result roles rather than silently discarding them.
- Added `docs/CHATGPT_WEB_TOOL_CALLING_DESIGN.md` as a draft. It separates a recommended client-executed compatibility bridge from optional server-executed WebCodex integration and records API mapping, buffering, failure, security, and test requirements.
- No runtime behavior changed in this documentation phase.
- Committed the design and its exact documentation allowlist entry as `f382b02`, then pushed GitHub `main`.
- Built the secret-free release context with SHA-256 `f8f3ed2ba85880f095bda7b538fad8d7329e44a160342d4bcfccfeca0f7bc99e` and deployed image `local/sub2api:web-attachments-9999-f382b02` to port 9999.
- The replacement application is healthy; ports 9999/10000/10001 return HTTP 200, both protected OpenAI endpoints return 401 without a key, startup fatal-log count is zero, and existing 10000/10001 plus 9999 database/cache containers were unchanged.
- Appended the release manifest and removed only this release's remote build directory and upload archive. Kept the previous Compose file and images as rollback points.
- Local recursive deletion was blocked before execution, so the exact release stage, raw archive, and compressed context were moved out of the repository into a recoverable system temporary directory. Other existing untracked artifacts were not changed.

## 2026-09-04 bulk Web transport editing

- Started tracing the existing single-account transport selector, bulk edit modal, bulk account API, and backend account update rules.
- Scope is limited to OpenAI accounts and the existing `extra.openai_transport` contract. The UI must allow explicit Web or Codex selection without changing accounts when the field is left unchanged.
- Added an explicit bulk transport field for OpenAI OAuth/Setup Token targets. The field is opt-in, supports `web` and `codex`, and works for both selected IDs and filtered targets.
- Web bulk switches write explicit off values for stale Codex-only passthrough, namespace, WebSocket, CLI-only, fingerprint, long-context, and compact settings because bulk `extra` updates are top-level JSONB merges.
- Added service-layer validation for `openai_transport`, including normalization, OpenAI OAuth/Setup Token target enforcement, and direct-API Web isolation.
- Focused frontend tests pass (56 bulk + 53 edit), backend bulk-update protocol tests pass, `vue-tsc --noEmit`, modified-file ESLint, and `git diff --check` pass.
- The focused admin bulk-update handler tests also pass with the unit build tag.
- Committed the seven source/test/locale files as `feb2f45 feat: support bulk OpenAI transport selection` and pushed `main` to GitHub.
- Built a secret-free backend-only release context from the committed tree plus regenerated `backend/internal/web/dist`; local SHA-256 is `913f6fa445f410e5d2375c8aaf13baf3b61b5eb1b71341c62657ac895dbd1342`.
- Remote Docker built `local/sub2api:web-attachments-9999-feb2f45` with image ID `sha256:c1fcecb2f5ef106e5af28aac373a467ed9329ab054313de4e3662aa8a41c5140`.
- Recreated only the `sub2api-9999` application container. PostgreSQL and Redis IDs remained `424b583e...` and `24a12bc7...`; all three public health checks returned 200 and protected OpenAI routes returned 401 without a gateway key.
- Verified the deployed binary contains the Web transport UI marker and startup logs have zero panic/fatal entries. The release manifest records the commit, context hash, image, and image ID; remote build/upload artifacts were removed while the Compose backup was retained for rollback.

## 2026-09-04 protocol-specific rate-limit work

- Audited the current OpenAI OAuth 429 path and scheduler veto order.
- Confirmed the implementation will use `model_rate_limits` transport scopes, add Web body-only 429 classification, bypass the Codex retry window for Web, and expose protocol-specific filter reasons.
- Source edits and focused tests are pending; no remote state has been changed in this phase.

# 2026-09-04 protocol-specific rate-limit implementation

- Added Web and Codex transport scope constants and body-based Web message-limit classification.
- Web 429 handling now uses an hourly dedicated scope, avoids the Codex same-account retry window, and does not write the global `RateLimitResetAt`.
- Codex 429 handling records the dedicated Codex scope while preserving legacy global behavior for backwards compatibility.
- Added protocol-specific scheduler veto reasons, counts, samples, and admin scope labels.
- Added transport-scope keys to `modelRateLimitKeysForRequest`, covering legacy GatewayService model/sticky paths that bypass the newer diagnostic helper.
- Focused Go tests passed for Web 429 persistence, account-test reconciliation, cross-protocol isolation, scheduler diagnostics, and sticky-session clearing.
- Service package compile passed with `go test -vet=off -p 1 ./internal/service -run '^$'`.
- No remote state has been changed yet; release, push, and port-9999 deployment remain next.

- 2026-09-04: Uploaded the SHA-256-verified `3e2718e` release tar to the server. The first remote build attempt used `tar -xzf` against an uncompressed tar and stopped before Docker build; no running container changed. Retrying with `tar -xf`.
- 2026-09-04: The first post-recreate health command was rejected locally when PowerShell expanded `$(seq ...)`; no SSH command ran. The check is being retried with a Base64-encoded remote shell script.

- 2026-09-04: Deployed `3e2718e` to the isolated `9999` application. Remote Docker built image `local/sub2api:web-attachments-9999-3e2718e` with image ID `sha256:b608d038416683c9d1dbb63ececae5369d72701dc70db335e2c9ab1309f6f5f1`; the tar hash matched `84d4dcbbc11def4f1731ab5bdd623dab2ef06889628a12e8ccef2fc9e97eeab8`.
- 2026-09-04: Recreated only `sub2api-9999`; PostgreSQL `424b583e...` and Redis `24a12bc7...` stayed unchanged. `9999`, `10000`, and `10001` health checks returned 200, protected Chat Completions/Responses routes returned 401, and startup logs reported zero fatal/panic entries.
- 2026-09-04: Appended the release manifest, retained the previous images and Compose backup, and removed only `/root/sub2api-build-3e2718e` and `/root/sub2api-9999-context-3e2718e.tar`. Protocol-specific Web/Codex rate-limit release is deployed and verified.

- 2026-09-04: Diagnosed the reported 404: `backend/Dockerfile` built without `-tags embed`, selecting `embed_off.go`; remote `/` and SPA paths returned 404 while `/health` and protected APIs worked. Added the embed tag. The full embed test suite has a pre-existing missing-PNG fixture mismatch; focused root/embed tests and `cmd/server` embed compilation pass.
- 2026-09-04: The first `3dc5dcd` embed rebuild stopped before image creation with exit 126 because the Windows-created tar lacked the executable bit on `resolve-version.sh`. The Dockerfile now calls `sh ./scripts/resolve-version.sh` to make the release portable.
- 2026-09-04: The first post-embed replacement command was aborted locally because PowerShell would have expanded `$path` inside the remote verification loop; no remote state changed. Retrying with Base64 script transport.

- 2026-09-04: Committed and pushed `d7f0f2d`, rebuilt the image with `-tags embed` and portable version-script invocation, and recreated only `sub2api-9999`. Remote `/`, `/admin/accounts`, `/logo.svg`, and `/health` now return 200; protected OpenAI routes remain 401 and startup fatal/panic count is zero. The previous image and Compose backups remain available, and all temporary build/upload files were removed.
## 2026-09-05 Prompt Tool continuation

- Read the current official OpenAI function-calling guide and confirmed the
  client-executed flow, strict schema requirements, tool-choice forms, and
  Responses streaming event lifecycle.
- Formatted the in-progress Web Prompt Tool implementation and ran focused
  service tests successfully.
- Fixed Responses namespace and native-type `tool_choice` mapping after the
  Web registry flattened names.
- Added fail-closed setting coverage and a unit-tagged persistence regression
  test for `enable_openai_web_prompt_tools`.
- Added regression coverage for required/具名 `tool_choice` plain-text failures
  and `parallel_tool_calls` metadata in the Web Responses stream. Local reruns
  were blocked by D: drive space during Go compile/link; remote Docker remains
  the authoritative build/test gate for the release.
- Committed and pushed `b4da2b0` (`fix: enforce web prompt tool choices`).
- Rebuilt the frontend and generated a secret-free release context with SHA-256
  `2efeb655ea93c5a7893dec8d3494b61faf50c31f5f71f380ed04d9dcccaef414`.
- The first archive omitted `backend/internal/web/dist` because it was created
  before the generated assets were copied; Docker rejected that context before
  any container replacement. The corrected archive was uploaded and verified
  remotely with the same SHA-256 and 179 embedded files.
- Remote Docker built `local/sub2api:web-attachments-9999-b4da2b0` with image
  ID `sha256:204626c350a83c2eddad94df5a6e22a68ceb647bffebe2a20a13538d19df33aa`.
  Only `sub2api-9999` was recreated; PostgreSQL/Redis and the existing 10000/
  10001 application containers were unchanged.
- Remote Prompt Tool/Web regression tests passed inside the built image. Final
  smoke checks returned 200 for root, health, admin shell, and logo; both
  protected OpenAI routes returned 401; 10000/10001 health returned 200; and
  startup panic/fatal count was zero.
- 2026-09-05: Diagnosed the Prompt Tool switch regression on the deployed 9999 instance. GET returned the persisted value correctly, but PUT returned `enable_openai_web_prompt_tools=false` because the update response DTO mapping omitted the field; the frontend then overwrote its reactive form with that false value. Source fix and regression tests are next.
- 2026-09-05: Added the missing PUT response mapping, a stable frontend test selector, and frontend/backend regression tests. The first Vitest invocation used unsupported `--runInBand`; it will be rerun with Vitest-native arguments.
- 2026-09-05: Targeted Vitest passed (38/38). The local Go handler test could not finish because D: ran out of space while compiling dependencies; remote Docker verification remains the backend release gate.
- 2026-09-05: Remote image smoke command initially used a login shell that hid `go` from PATH; no container changed. The image contains the expected embedded frontend marker, and the test is being rerun with an absolute Go binary path.
- 2026-09-05: Remote Docker test passed for `TestSettingHandler_UpdateSettings_ReturnsOpenAIWebPromptToolsValue`; the rebuilt image contains the Prompt Tool frontend marker.
- 2026-09-05: Committed and pushed `581feef` (`fix: preserve web prompt tool setting after save`). Rebuilt `local/sub2api:web-attachments-9999-581feef`, recreated only `sub2api-9999`, and kept PostgreSQL/Redis plus 10000/10001 unchanged.
- 2026-09-05: Remote verification passed: 9999/10000/10001 health=200, 9999 root/admin shell=200, protected Chat/Responses routes=401, startup fatal/panic count=0, and settings round-trip was `before=True, PUT response=True, after=True`. The 9999 database value is enabled.
- 2026-09-05: Reproduced the remaining Web tool failure in source: both OpenAI Web forwarding paths rebuilt normalized tools into `chatReq.Tools`, so the bridge could still be rejected as native Web tools. Removed those assignments and added transport-local sanitization of all native tool fields.
- 2026-09-05: Added backend tests for Prompt Tool payload exclusion and explicit setting disable persistence, plus a frontend test covering enable -> disable -> save -> reload. Direct Vitest passed 39/39; focused Go service/admin tests passed.
- 2026-09-05: `pnpm exec vitest` attempted an automatic dependency check and was blocked by ignored build scripts; reran the already-installed Vitest module directly. This is an environment/tooling warning, not a test failure.
- 2026-09-05: Re-ran the flat-name remote verification command without shell
  regex parsing issues. The release image is healthy and the deployed admin
  setting round-trip passed `true -> false -> true` with matching PUT/GET
  values.
- 2026-09-05: Verified ports 9999, 10000, and 10001 return health 200. The
  9999 image is `local/sub2api:web-attachments-9999-d9cd8e6`; 10000 and 10001
  containers were not recreated. Recent 9999 logs have no fatal/panic or
  native Web Prompt Tool rejection.
- 2026-09-05: The remote probe found no account configured with
  `extra.openai_transport=web`, so a live ChatGPT Web request was not attempted
  and no account data was modified. Local Go rerun remained blocked by D-drive
  compile space; the already-passed remote Docker regression is the release
  gate.
- 2026-09-05: Read the 9999 container and database logs over SSH without
  changing remote state. Confirmed the 413 is returned by ChatGPT Web with
  `你提交的消息过长，请修改后重新提交。`; Sub2API's 256 MiB body limits were
  not reached. Inbound bodies peaked at 162562 bytes, and the same oversized
  request was retried across Web accounts 31/32/33 before the final 413.
- 2026-09-05: Added Plus-HAR-compatible Web stream handoff support. The
  transport now detects the short `stream_handoff` SSE prelude, obtains the
  authenticated user WebSocket URL, sends the array-framed topic subscription,
  unwraps `conversation-turn-stream` frames, and feeds deduplicated
  `encoded_item` SSE back into the existing Responses converter. Direct legacy
  SSE remains buffered/replayed unchanged.
- 2026-09-05: Added regression coverage for handoff parsing, topic frame
  decoding, array subscription shape, end-to-end `Do` handoff switching, and
  empty Web responses. Focused Web service tests and `git diff --check` pass.
- 2026-09-05: Committed and pushed `0c6b8d1` (`fix: recover ChatGPT web handoff streams`). Built a secret-free 60 MB release context with SHA-256 `2dac15ad28c6f422d4ff67b109f96b3e7d83cc12dc058f3c7eb0b1246bdbdded`; remote Docker built image `local/sub2api:web-attachments-9999-0c6b8d1` with image ID `sha256:fa002c2b4c39cdb24a2e1ce8736224d1133a8075d274f9863963a4f120735969`.
- 2026-09-05: Recreated only `sub2api-9999`; PostgreSQL `424b583e...` and Redis `24a12bc7...` stayed unchanged. Ports 9999/10000/10001 root and health checks returned 200, protected 9999 Chat/Responses routes returned 401, and startup logs had no panic/fatal or empty-response errors. Remote build and upload artifacts were removed after exact-path verification.
- 2026-09-05: A live Web handoff turn was not run in this release because the isolated 9999 database currently has no account with `extra.openai_transport=web`; no account or token data was changed.
# 2026-09-05 Plus HAR follow-up

- Resumed from the deployed `469a482` handoff release without changing remote state.
- Re-read the current Web transport and existing handoff tests; the next gate is a field-level comparison against the supplied Plus-member HAR.
- Updated the model discovery query, conversation/prepare origin and response contract fields, and neutral message metadata defaults to match the captured Plus request shape.
- Added focused regression coverage; the selected Web transport/model/attachment/handoff tests pass locally.
- Committed and pushed `c1631d1` (`fix: align web transport with plus har`).
- Built a secret-free release context with SHA-256 `5ac286bd83128c437c9797af0f724125ee6d83bd55ade44d5ce75a5a4f65be51`; remote Docker built image `local/sub2api:web-attachments-9999-c1631d1` with image ID `sha256:ab5ddb1e2e47dfbd4368ba77337af5aadb27baab0e2824ccaa5c49a2e90f5fc1`.
- Recreated only `sub2api-9999`; PostgreSQL/Redis and 10000/10001 were unchanged. Final health checks for 9999/10000/10001 returned 200, protected POST Chat/Responses routes returned 401, and recent fatal/panic/Web-empty diagnostic matches were zero.
- The remote database still has no account marked `extra.openai_transport=web`, so a live authenticated Web conversation was not attempted in this release.
- 2026-09-05: Read the current 9999 logs over SSH. The deployed image was still
  `local/sub2api:web-attachments-9999-c1631d1`; Astra requests on Web account 36
  emitted no client-visible stream bytes and ended after the generic 183-second
  timeout, while `auto` completed. The Plus HAR shows the HTTP `[DONE]` is only a
  handoff prelude and WebSocket telemetry finishes with `reason:"done"`.
- 2026-09-05: Added a bounded WebSocket topic reader and tolerant terminal/event
  parsing in the working tree; focused tests are next, followed by an isolated
  9999 release.
- 2026-09-05: Re-ran the focused Web service/model-routing tests with the
  existing repository-local Go cache. The selected handoff, object/camelCase
  frame, idle-timeout, model-catalog, and composite-routing tests passed
  (`ok .../internal/service 0.301s`). A full local package link was not used
  because the D: drive has about 1.8 GB free; remote Docker compilation is the
  release gate. The first rerun attempt only failed from an invalid PowerShell
  directory argument and a locked temporary test executable, not source code.

## 2026-09-05 Web model/mode 422 follow-up

- Started a diagnosis for `/backend-api/f/conversation` HTTP 422 reporting that the selected model is unavailable for the conversation mode.
- The prior `8d1df27` WebSocket initialization release is deployed and healthy; this phase will correlate server logs with the exact model and payload mode before changing code.
- WebCodex continuation was unavailable in the current tool surface, so remote inspection will use the existing fingerprint-pinned SSH helper without recording credentials or authorization headers.
- Remote logs show account 37 returning the same upstream 422 for both public OpenAI routes with model `auto`; this is a pre-stream request/model compatibility failure, not a recurrence of `not_connected`.
- Read-only database inspection confirmed account 37 is active Web `setup-token`; its saved model snapshot has ten dynamically discovered selectors and `default_model_slug=auto`.
- Enumerated both test accounts without reading credentials: account 36 is the Plus/OAuth catalog and account 37 is the smaller Setup Token catalog. The account test endpoint can isolate each upstream behavior.
- Confirmed both test accounts are active members of group 2 at the same priority. Deterministic verification will use per-account tests before pooled gateway tests.
- Traced the account-test and scheduler model chains. Both currently preserve `auto` as the private Web model; no account-specific default resolution exists.
- The initial deterministic test script halted before network tests because the login JSON did not contain a top-level `access_token`; no account data or upstream request was touched.
- Corrected the in-memory login parser to `data.access_token` and completed four account-specific live tests. Account 37 failed `auto` and `gpt-5-6` with 422; account 36 passed `auto` and Astra.
- Completed a whitelist-only structured comparison of both supplied HARs. The ordinary account sends a minimal `auto` payload and receives direct SSE; the Plus work-model payload adds a small set of work-mode fields and receives WebSocket handoff.
- Added the reported Prompt Tool switch disable failure to this release scope. Earlier API round-trip evidence is not sufficient; the deployed click path and reload state must be verified end to end.
- Located the frontend switch, API types, backend update DTO/mapping, and existing enable/disable tests. No change has been made yet because the current static test contract already claims the failing behavior works.
- Confirmed the save payload carries the setting. Investigation is moving from DTO plumbing to the common Toggle click target and deployed end-to-end behavior.
- Reviewed the production Toggle and post-save response merge. No obvious raw click defect exists; the test suite currently stubs away the production component interaction.
- Opened the deployed settings page in an isolated browser session. Parsed the Plus HAR through a whitelist-only reader and confirmed work-mode models still use `primary_assistant`; no HAR credentials or headers were emitted.
- Browser login fields were filled using administrator values held only in process memory. The subsequent URL wait returned no matching output, so the next step is an explicit URL/snapshot check rather than repeating the wait.
- The explicit URL read also timed out without output. The current `agent-browser` session is considered unhealthy and will be restarted rather than polled again.

# 2026-09-05 HAR/profile and screenshot follow-up

- Compared both supplied HARs at field level. Ordinary `auto`/non-work
  selectors use a minimal direct-SSE contract; Plus work selectors are marked
  by `is_work_mode_model` and add only origin/contracts/thinking fields before
  WebSocket handoff.
- The screenshot exposes a second compatibility gap: the model emits the
  nonce/schema-bound legacy `tools[].arguments` Prompt Tool envelope, while
  the parser only accepts `calls[]`, so the UI renders it as assistant text.

# 2026-09-05 Responses protocol release

- Confirmed `603483b` is the current `origin/main` commit and generated a fresh
  release context containing the committed backend plus 179 embedded frontend
  files. The local archive SHA-256 is
  `894daeea278d6ffca2fa88a225cc7c5d02fdce5687e9de46efe89ce72743b0f1`.
- Remote Docker built `local/sub2api:web-attachments-9999-603483b` with image
  ID `sha256:36807a14e5bd7dc10ec4dfb6c16e782c4cad218799c20f9f664f4712a426efc8`;
  the image was built from the same SHA-256-verified archive and passed
  container-local `apicompat` and Web Prompt Tool service tests.
- Recreated only `sub2api-9999` and preserved its PostgreSQL/Redis containers;
  `10000` and `10001` were not recreated. Health checks for all three ports,
  9999 root/admin shell, and embedded frontend assets passed. Unauthenticated
  `/v1/chat/completions` and `/v1/responses` on 9999 returned 401 as expected.
- Recent 9999 logs contain zero panic/fatal, native Web `tools`/`temperature`/
  `max_tokens` rejection, WebSocket `not_connected`, model-mode 422, or empty
  response diagnostics. A live upstream Web turn was not run because the
  isolated database has no account currently marked `extra.openai_transport=web`.
# 2026-09-05 interruption fix continuation

- Read the current 9999 container logs over SSH and correlated the reported
  interruption with two concrete failure paths: deployed Prompt Tool strict
  schema rejection and Web `gpt-6-astra-wm` upstream 422 invalid bodies.
- Confirmed the server is still running the pre-fix `603483b` image; the
  working tree contains the uncommitted parser/schema remediation that must be
  tested and released next.

# 2026-09-05 Web conversation continuity

- Confirmed from the current source that Web requests still transmit complete converted message history; `conversation_id`/`parent_message_id` are not wired from the gateway into the transport or back into persistent state.
- Added the continuity implementation phases to `task_plan.md` and recorded the isolation and fallback contract in `findings.md`.
- No runtime source has been changed yet in this phase; next step is to inspect gateway request/session helpers and implement the state store against existing TTL primitives.
- The Plus HAR confirms the handoff prelude carries `conversation_id` and `turn_exchange_id`; WebSocket frames are not archived in that HAR, so parent-message capture will be best-effort and fail closed when absent.

# 2026-09-06 Web conversation continuity implementation

- Added an isolated `OpenAIWebConversationState` store with local TTL caching
  and an optional Redis namespace. Keys include API key, group, account,
  model, and stable caller session identity; no state is shared across account
  failover or model changes.
- Wired `/v1/responses` and `/v1/chat/completions` Web forwarding through the
  continuation planner. The first eligible request replays full history and
  commits the Web cursor; subsequent compatible turns send only the newest
  user message with the stored `conversation_id` and `parent_message_id`.
- Added fail-closed invalidation for upstream/stream errors, profile changes,
  attachment/tool turns, expired state, and missing Web parent IDs. Responses
  `previous_response_id` is consumed as a gateway alias and is not serialized
  into the private ChatGPT Web payload.
- Added direct SSE/WS cursor extraction and state-store/cursor/expiry tests.
  Focused service tests and GatewayCache tests pass; `git diff --check` passes.
- Added an end-to-end continuation unit test proving that the first stable
  session turn stores a Web cursor and the next compatible turn sends only the
  latest user message. The focused continuation/state tests pass.
- Built and deployed commit `99c0e94` as
  `local/sub2api:web-attachments-9999-99c0e94` on the isolated `9999` app.
  Only `sub2api-9999` was recreated; `10000` and `10001` remained healthy.
  Root/health checks passed, protected Chat Completions and Responses routes
  returned 401 without a key, and recent logs had no fatal/panic or known Web
  transport rejection signatures. Remote build/upload artifacts were removed.
- A local Go cache cleanup initially tried to move a 576 MB cache across drives
  into a full C: volume. The exact destination and source cache directories
  created by this run were then removed; no unrelated files were touched.
