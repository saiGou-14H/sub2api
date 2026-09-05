# Access-token web chat compatibility

## Goal

Adapt Sub2API so a caller can configure a ChatGPT web account with only an access token, send web conversations through the configured upstream, and expose the result through the existing OpenAI-compatible API.

## Phases

- [completed] Inspect repository contracts, account model, upstream adapter, and OpenAI routes.
- [completed] Implement access-token-only account validation and ChatGPT web transport compatibility.
- [completed] Verify OpenAI-compatible request/stream/error behavior with focused tests.
- [completed] Audit and package an isolated build for remote port 9999 without changing the existing 10000/10001 instances.
- [completed] Rebuild and redeploy the isolated image after the final account-creation transport fix.
- [completed] Reconcile web transport with Codex-only UI and WebSocket settings, then freeze a final release archive.
- [completed] Re-verify remote health, protected OpenAI routes, deployed frontend labels, and unchanged existing instances.
- [completed] Run final repository checks and document secure configuration and startup commands.
- [completed] Synchronize the 9999 administrator account with the verified 10001 credentials.
- [completed] Re-test the supplied access token entirely on the remote host without a proxy and isolate the remaining 9999 failure.
- [completed] Audit and adapt model, message, tool, reasoning, attachment, and stream parameters for the ChatGPT Web transport while preserving Sub2API's existing public endpoints.
- [completed] Discover ChatGPT Web models dynamically from each account's authenticated `/backend-api/models` response, aggregate those capabilities into scheduling and public `/v1/models`, and use `auto` only as the conservative fallback when discovery is unavailable.
- [completed] Give Web accounts a dedicated connectivity-test request that sends only supported text fields and never injects Codex `max_tokens` defaults.
- [completed] Rebuild and deploy the corrected Web transport to the isolated 9999 instance, then verify both public endpoints against a real Web account.
- [completed] Record the initial ChatGPT Web tool-calling compatibility and optional WebCodex execution design without changing runtime behavior.
- [completed] Add the OpenAI account transport selector to bulk account editing so selected accounts can switch between Codex OAuth and ChatGPT Web, with backend validation and focused tests.
- [completed] Build, commit, push, and deploy the bulk transport selector release to the isolated 9999 instance, then verify the deployed UI and account update behavior.
- [completed] Add protocol-specific Web/Codex rate-limit scopes, body-based Web 429 detection, scheduler veto reasons, and regression coverage.
- [completed] Commit and deploy the protocol-specific rate-limit release to the isolated 9999 instance, then verify remote health and logs.
- [completed] Diagnose and fix the isolated 9999 frontend 404 by embedding the SPA in backend image builds, then redeploy and verify page routes.
- [completed] Implement the administrator-gated ChatGPT Web prompt-based tool bridge with strict schemas and native-tool wrappers.
- [completed] Verify Chat and Responses tool-call/history conversion, frontend settings, and release integrity.
- [completed] Commit, push, package, deploy to isolated port 9999, and run remote smoke tests.
- [completed] Fix Prompt Tool setting response mapping and add frontend/backend regression coverage.
- [completed] Commit and push the switch fix, redeploy only port 9999, and verify PUT/GET plus UI asset behavior.
- [completed] Remove native Web tool fields from Prompt Tool requests and add explicit on/off regression coverage.
- [completed] Commit, push, redeploy only port 9999, and verify the deployed request-body and switch behavior.
- [completed] Reconcile the latest Plus-member HAR with Web model, attachment, SSE, and WebSocket handoff payloads; add focused regression coverage for any uncovered shape.

## 2026-09-05 Plus HAR follow-up release

- [completed] Align Web model discovery and conversation/prepare payloads with the Plus HAR.
- [completed] Run local and remote Web transport regression tests.
- [completed] Commit, push, rebuild, and deploy only port 9999; verify health and protected routes.

## Scope decisions

- Keep the change inside `D:\WebGpt\sub2api`.
- Never store or print a real access token in source, tests, logs, or planning files.
- Reuse existing account, proxy, routing, and OpenAI response abstractions.
- Preserve unrelated repository changes and do not push or rewrite history.
- Keep the existing downstream API-key authorization model: a ChatGPT web access
  token configures the upstream account, while callers use a Sub2API API key to
  access the OpenAI-compatible gateway.
- Keep `/v1/responses` and `/v1/chat/completions` owned by Sub2API. Web accounts
  only replace the selected upstream transport and use `chatgpt2api` as the
  private protocol reference; no additional public Web-chat route is added.

## Errors Encountered

| Error | Attempt | Resolution |
|---|---:|---|
| Initial full clone stalled during pack indexing | 1 | Reused the initialized repository and fetched `main` shallowly through the local SOCKS proxy. |
| Blob-filter checkout could not fetch a promisor blob without proxy | 1 | Persisted the bounded repository proxy and completed the lazy blob fetch. |
| planning-with-files session-catchup.py raised `NameError: bi is not defined` | 1 | Restored context directly from the three planning files and current git diff. |
| Official OpenAI documentation pages returned Cloudflare 403 or timed out from this host | 1 | Do not treat snippets as evidence; verify compatibility from repository contracts and offline tests. |
| PowerShell and cmd removal of the local deployment archive were blocked by execution policy | 2 | Moved the exact files out of the workspace into the system temporary directory. |
| Initial deployed-bundle label checks used array-level PowerShell `Contains` semantics | 2 | Joined curl output before matching and also verified exact deployed assets server-side. |
| Local `docker` executable is unavailable | 1 | Build and inspect the image on the authorized remote Docker host after validating the upload context. |
| First embedded frontend marker command returned exit code 1 | 1 | Checked the exact hashed bundle inside both local `dist` and the tarball; it contains `Upstream protocol` and `Web conversation (web)`. The first result reflected one absent optional search term, not a stale build. |
| The v2 archive was generated before the final WS capability invariant | 1 | Rebuilt assets and deployed the frozen final archive with SHA-256 `a46e18c20c0831e66755c35e035882ecee1e91813a2a50cc035ed7a133e63d3b`. |
| PowerShell parsed a double-quoted `rg` pattern as a module expression | 1 | Re-run with a single-quoted literal pattern. |
| Windows `rg` rejected explicit `*.go` path arguments | 1 | Search the fixed service directory with `-g '*.go'` filters instead. |
| Called the exec-session wait helper without a live cell | 1 | Use the collaboration agent wait mechanism for delegated work instead. |
| A temporary `r2` deployment archive disappeared during hash inspection | 1 | Another parallel worker removed its transient package; use the final archive generated after all source edits instead. |
| A planning-file patch raced with a parallel audit update | 1 | Re-read the planning files and applied a narrow patch against the current content. |
| Initial release-stage secret scan matched generic source `password=` strings | 1 | Re-ran an exact JWT/API-key-shaped scan; no credential-like values were found. |
| Moving the concurrently used Go cache produced transient file-not-found messages | 1 | Left the existing cache untouched and moved only the completed run's isolated cache artifacts to a recoverable temp directory. |
| The 9999 admin password was auto-generated and absent from its environment file | 1 | Copied the verified 10001 admin email/password into 9999's `.env` and database, then validated login on both instances. Secret values are not recorded here. |
| The official developers.openai.com search route was blocked by the in-app browser | 1 | Opened the exact Responses and Chat Completions API reference pages directly and verified their rendered parameter documentation. |
| planning-with-files session-catchup.py still raises `NameError: bi is not defined` | 2 | Recovered current state from `task_plan.md`, `findings.md`, `progress.md`, and `git diff --stat`; do not repeat the broken helper. |
| A new remote-audit subagent could not start because the local thread store reported insufficient disk space | 1 | Continue with the two existing audit agents and perform the bounded remote probe from the primary task. |
| An exec-session wait helper was called twice with a nonexistent placeholder cell | 2 | Stopped using the exec wait helper; use collaboration agent status/wait calls only. |
| Planning-file patches used stale or wrong-file anchors | 2 | Re-read each exact section and split the updates by target file. |
| Go service test could not write its argument response file to the full system-drive temp directory | 1 | Re-ran once with `TEMP`, `TMP`, `GOCACHE`, and `GOTMPDIR` scoped to the repository's D-drive temp directories. |
| The exec-session wait helper was called again without a live cell during resume | 4 | Do not use `functions.wait` for agents; use `collaboration.wait_agent` only. |
| `request_user_input` was accidentally invoked in Default mode | 1 | No user input is needed; continue autonomously with direct tools. |
| pnpm 11 production build invoked a dependency install and blocked esbuild/vue-demi build scripts | 1 | Ran the already-installed `vue-tsc` and Vite binaries directly; typecheck and production build passed. |
| First release-context staging pass copied one backend `.tmp*` directory | 1 | Rebuilt from a new staging directory with all dynamically discovered `.tmp*` paths excluded; the first archive was not uploaded. |
| The r8a attachment matrix used a PNG with a bad IDAT checksum | 1 | Verified both candidate fixtures with Pillow; replace the corrupt fixture with the known-valid PNG before the next live matrix. |
| PDF/TXT/ZIP were serialized as `image_asset_pointer` and rejected by the Web endpoint | 1 | Use HAR-backed metadata-only content for non-image attachments and keep pointers exclusive to image MIME types. |
| An obsolete long-running attachment test lost its backing account during execution | 1 | Stopped treating that mixed result as acceptance evidence and switched to the isolated create/test/finally-cleanup matrix. |
| Focused Go verification used a new `GOTMPDIR` before creating it | 1 | Create the exact repository-local temp directory, then rerun the focused test with the same isolated cache settings. |
| Focused Go verification exhausted D-drive space while linking `service.test.exe` | 1 | Treat this as an environment-only gate failure, remove only this run's exact caches, and require the remote Docker compile to succeed before replacing the running container. |
| Matrix probe's empty-input branch raised `NameError` before connecting | 1 | Import `json`, verify the branch returns structured input failure, then rerun with hidden interactive secrets. No remote request was made. |
| Exec-session wait was invoked without a live cell during this continuation | 3 | Do not use the exec wait helper; use `collaboration.wait_agent` for subagent completion. |
| First multi-file `auto` patch had an invalid hunk boundary | 1 | Split the edit into narrow per-file patches with complete context. |
| A PowerShell `rg` pattern containing double-quoted alternatives was parsed as an unterminated regex | 1 | Re-ran with a single-quoted literal pattern and avoided embedded PowerShell quote interpolation. |
| A repeat direct PowerShell fetch of the official OpenAI create-reference pages returned edge `Forbidden` responses | 1 | Do not retry this blocked route; retain the already successful 2026-09-04 official-page verification and validate the implementation through repository contracts and focused tests. |
| `rg` was passed a Windows path wildcard (`backend/internal/handler/gateway*.go`) and rejected it | 1 | Use a fixed directory plus `-g 'gateway*.go'`, or enumerate matching files first. |
| An exec-session wait helper was accidentally invoked without a live cell during this resume | 1 | Use only `collaboration.wait_agent` for agent completion and do not call the exec wait helper. |
| A reference-repository read included a Sub2API-only path that does not exist there | 1 | Keep reference reads scoped to its Python implementation (`services/openai_backend_api.py` and `services/model_service.py`). |
| Remote helper was first launched through a nonexistent repository `.venv` Python | 1 | Use the verified system Python with Paramiko 4.0.0. No SSH connection or remote change occurred. |
| Non-TTY remote helper invocation closed stdin before password input | 1 | Use `getpass` through a PTY so the password is hidden and passed only to the process. No SSH connection or remote change occurred. |
| Official OpenAI function-calling documentation was blocked by the current network edge | 1 | Base this initial draft on the checked-in OpenAI-compatible types/converters and current repository behavior; do not claim unavailable official-page verification. |
| The first referenced-task read requested more than the supported 10-turn limit | 1 | Re-read the task with the supported limit and bounded output settings. |
| The new design document was ignored by the repository's `docs/*` allowlist | 1 | Add one exact `.gitignore` exception for the requested document, matching the existing documentation policy. |
| Recursive removal of this run's local release staging was blocked before execution | 1 | Moved the three exact workspace-owned targets into a new system temporary directory after validating both source and destination roots. |
| Go test from the repository root could not find a module | 1 | Re-ran from `backend`, the Go module directory. |
| Full service test compile exhausted D: drive space | 1 | Moved only the newly-created rate-limit cache to the system temp directory and used the existing compiled cache with `-p 1 -vet=off`. |
| Remote release archive extraction used gzip mode for an uncompressed tar | 1 | Verified the local tar header and will re-run extraction with `tar -xf`; the running container was not changed. |
| Remote post-deploy test command parsed a grouped `-run` regex as shell syntax | 1 | Container health and image replacement completed; rerun tests with a flat regex and keep settings verification as a separate command. |
| Prompt Tool service regression rerun exhausted local D: space during compile/link | 2 | First run failed writing Go packages; a second run with the existing compiled cache reached the linker but faulted under the same low-space condition. Remote Docker compilation remains the release gate. |
| First b4da2b0 archive was generated before copying embedded frontend assets | 1 | Docker correctly rejected the missing `internal/web/dist`; regenerated the archive from the completed staging tree, re-uploaded it, and rebuilt successfully. |
| PowerShell expanded a remote shell `$(seq ...)` expression before SSH execution | 1 | No remote command ran; passed the verification script as Base64 to avoid local interpolation. |
| The first manifest cleanup orchestration contained PowerShell syntax inside the JavaScript tool wrapper | 1 | No remote command ran; reran with a valid JavaScript string and completed cleanup. |
| Full `internal/web` embed test run expected an absent PNG fixture and failed MIME/cache assertions | 1 | Kept the existing fixture untouched; focused embed/root tests and `cmd/server` embed compilation passed. |
| Remote embed-image rebuild hit exit 126 because Windows tar omitted `resolve-version.sh` executable mode | 1 | Changed `backend/Dockerfile` to invoke the script via `sh`; rebuilding from the new commit. |
| PowerShell expanded `$path` in the first post-embed deployment command | 1 | Aborted before password submission; no remote command ran. Retrying with a Base64-encoded script. |
| Vitest rejected the Jest-only `--runInBand` flag | 1 | Re-run the targeted Vitest file without that option; no source failure was involved. |
| Local Go handler test exhausted D: drive space while compiling dependencies | 1 | Treat the local run as inconclusive; use the remote Docker build/test gate and remove only the temporary directory created by this run. |
| Remote test shell used `sh -lc`, whose login PATH hid the Go toolchain | 1 | Re-run with `sh -c` and the absolute `/usr/local/go/bin/go` path; the image and source are intact. |
| Local focused Prompt Tool rerun exhausted the D: drive while compiling uncached Go dependencies | 1 | Removed only the exact `.tmp-go-cache-check` and `.tmp-go-tmp-check` directories; use the successful remote Docker regression as the release gate. |
| Moving the 576 MB repository-local Go cache to C: exhausted the destination drive | 1 | Removed only the exact source and partial destination directories created by this run; no unrelated files were touched. |

## 2026-09-05 final verification

- Commit `d9cd8e6` is pushed to `origin/main`; the 9999 container runs image
  `local/sub2api:web-attachments-9999-d9cd8e6`.
- The Web Prompt Tool bridge now keeps normalized definitions only in the
  request-scoped prompt protocol and clears `tools`, `functions`,
  `tool_choice`, `function_call`, and `parallel_tool_calls` from the private
  ChatGPT Web payload.
- Remote settings round-trip passed on `127.0.0.1:9999`: login 200, GET true,
  PUT false/GET false, PUT true/GET true. No secret values were recorded.
- Remote health passed for ports 9999, 10000, and 10001. Existing 10000/10001
  application containers were not recreated.
- Recent 9999 logs contain no fatal/panic or native Web Prompt Tool parameter
  rejection. The diagnostic database currently has no account marked with
  `extra.openai_transport=web`, so a live Web upstream conversation could not
  be exercised without changing account data.

## 2026-09-05 request payload 413 diagnosis

- [completed] Read the live 9999 container logs and `ops_error_logs` over SSH.
- [completed] Cross-check the server evidence against the Web transport and
  upstream-error classification in the Sub2API source.
- [completed] Record the root cause and bounded remediation direction without
  changing runtime code or remote state.

## 2026-09-05 Plus HAR stream handoff

- [completed] Implement the authenticated user WebSocket handoff and preserve direct SSE compatibility.
- [completed] Commit and push the handoff implementation, rebuild and deploy only port 9999, then run remote health and regression checks.

## 2026-09-05 Astra stall fix

- [completed] Validate the WebSocket reader timeout and tolerant terminal-frame parser.
- [completed] Run focused and remote Docker compile/tests.
- [completed] Commit and push the fix without staging credentials or temporary release files.
- [completed] Rebuild and replace only the 9999 application container.
- [completed] Run server-local health/API/log regression checks; verify 10000/10001 are unchanged. A live Astra turn remains unavailable while no Web account is configured in the isolated database.

## 2026-09-05 Web model/mode 422 fix

- [completed] Correlate the live 9999 HTTP 422 with the selected account, requested model, and private Web payload.
- [completed] Correct Web model/mode resolution without hard-coding a closed model list, and add focused regression tests.
- [completed] Preserve and regress both Web response modes: legacy direct SSE and `stream_handoff + WebSocket`.
- [completed] Run local/remote verification, commit and push the fix, then redeploy only the 9999 application container.
- [completed] Verify public health and protected route behavior plus unchanged 10000/10001 health. Live explicit-model verification remains gated by the absence of a Web account in the isolated database.

## 2026-09-05 HAR/profile and screenshot compatibility

- [completed] Split ordinary and work-mode Web payload fields using the authenticated `is_work_mode_model` catalog flag.
- [completed] Accept the screenshot-compatible nonce/schema-bound `tools[].arguments` Prompt Tool envelope and convert it to standard tool events.
- [completed] Add regression tests for ordinary/Plus prepare and conversation payloads and both Prompt Tool envelope shapes.
- [completed] Run focused verification and record the remote deployment separately.
- [in_progress] Reproduce the Prompt Tool switch click failure in the deployed UI and distinguish frontend interaction state from backend persistence.
- [pending] Fix the switch so both enable and disable actions persist and remain correct after reload; add frontend/backend regression coverage as needed.

### Current errors

- An existing WebCodex continuation tool was unavailable in the current tool surface; continue with the verified fingerprint-pinned SSH helper and do not repeat that MCP call.
- Two multi-file planning patches used stale end anchors and made no changes; use separate end-of-file patches against current content.
- The first read-only remote SQL command was rejected by local PowerShell quote parsing before SSH execution; use Base64-encoded script transport for SQL commands.
- `agent-browser batch --json` received empty stdin from the Windows PowerShell pipeline and made no UI changes; use individual browser commands inside one credential-scoped process instead.
- The first post-login browser URL wait produced no match/output; inspect current URL and DOM before deciding whether login failed or SPA loading stalled.
- A subsequent `agent-browser get url` also exhausted the default timeout with no output, indicating the isolated browser daemon/session is blocked; restart that session once, then use the available browser CUA if it repeats.
- A source search repeated the known Windows wildcard-path mistake for `backend/internal/handler/auth*`; use a fixed directory plus glob filter. The needed login response contract was still found in the fixed frontend/backend paths.
- A raw `curl` to account model metadata returned 403 because it lacked the Web transport's browser fingerprint/challenge session; no token was printed and temporary output was removed. Do not repeat direct curl; use the project transport or reference implementation.

## 2026-09-05 Web conversation continuity

- [completed] Define an isolated Web conversation state contract keyed by caller, group, account, model, and stable session identity.
- [completed] Capture and persist Web `conversation_id` plus the latest `parent_message_id` from direct SSE and WebSocket handoff responses.
- [completed] Reuse the Web conversation only when the supplied history matches the stored turn fingerprint; otherwise replay the complete history and start a new Web conversation.
- [completed] Add account/model/protocol/TTL invalidation and failover safeguards so a Web conversation ID is never sent to a different account or incompatible model.
- [completed] Add focused tests for state storage, expiry, cursor capture, previous-response bridging, and both public forwarding paths.
- [completed] Run the remote Docker release gate and deploy only port 9999; the implementation and regression tests are committed and pushed.

## 2026-09-06 Web conversation context-drift hardening

- [completed] Bind continuation reuse to the exact prior transcript prefix and captured Web assistant text.
- [completed] Serialize same-session Web turns with a keyed lock held through upstream completion and state commit/invalidation.
- [completed] Preserve the canonical session state key when a request also uses `previous_response_id` as an alias.
- [completed] Add regression coverage for edited/reordered history, alias write-back, lock cancellation, and lock release.
- [completed] Commit, push, package, deploy only port 9999, and run remote health/log verification.

## 2026-09-06 HAR continuation alignment

- [completed] Match ordinary Web `partial_query` behavior: only an initial,
  attachment-free, non-work-mode prepare carries it; continuations and Plus
  work-mode prepares omit it.
- [completed] Allow attachment follow-up turns to reuse a valid Web cursor and
  send only the latest attachment-bearing user message; tool turns remain a
  full-replay boundary.
- [completed] Make Redis the authoritative cursor source when configured and
  fail closed on Redis read/write errors instead of using a stale local cursor.
- [completed] Add compare-and-delete Redis leases and cross-instance
  serialization for Web parent cursors, with cancellation-aware bounded retry.
- [completed] Run focused service, repository, and Redis integration tests;
  publish and deploy verification completed for this release.
