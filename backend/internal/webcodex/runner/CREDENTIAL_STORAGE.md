# Managed credential storage

This feature supplies native sub2api persistence and current host User resolution to the [existing managed verifier](README.md#managed-database-agent-token-authentication). It does not mount application routes or enable Runner transport.

## Storage decision and source map

All WebCodex source references use frozen revision `97ad66949a859174911c2f6da2ff1063be98bfa9`. The original `api_keys` definition is in `crates/webcodex-store/src/schema.rs:89-105`, its record in `models.rs:234-263`, and its lifecycle methods in `accounts.rs`.

The native [repository](../../repository/webcodex_api_key_repo.go) uses the existing `*sql.DB` pool, following host SQL repository practice. The [embedded migration](../../../migrations/238_webcodex_api_keys.sql) creates only `wc_api_keys`, a namespaced version of the original managed credential table. The normal host migration runner discovers it through `migrations.FS`; no alternate migration runner or Ent-generated schema is introduced. Host `api_keys` requires a plaintext `key` and integer credential ID and lacks the original kind/scope/client binding. Inventing model keys to carry hash-only credentials would change their authority and lifecycle.

| Original field | Storage and projection |
|---|---|
| `id` | Original TEXT credential ID, distinct from host model APIKey ID |
| `user_id` | BIGINT foreign key to existing `users(id)`, without cascade deletion; the sole original field whose identity representation changes |
| `name`, `key_hash`, `key_prefix` | Original TEXT values, hash UNIQUE; no plaintext token column |
| `created_at` | Signed BIGINT Unix seconds, required |
| `last_used_at`, `revoked_at`, `expires_at` | Nullable signed BIGINT Unix seconds; zero and negative values remain distinct from null |
| `scopes` | Original TEXT, default empty; order, duplicates and unknown values remain unchanged |
| `kind` | Original TEXT, default `user`; explicitly inserted empty/unknown values are preserved for verifier classification/rejection |
| `allowed_client_id` | Original nullable TEXT binding |

The unique hash index already supports lookup; the original user index is retained. No second user/login, model group, tenant, or authority table is introduced. Physical `user_id` is supplied only after trusted host selection or explicit legacy ID mapping. `GetByHash` converts it with `runner.HostUserOwner` to the verifier's existing string `sub2api_user_<positive int64>`; no verifier API changes are needed. Imported owner/subject/fingerprint references still require explicit consistent rewriting. No automatic import, username matching or account creation occurs.

## Lifecycle and host adapter

- `Insert` retains all twelve supplied fields (`accounts.rs:63-83`), uses a plain INSERT (no conflict revival), and requires a nonempty credential ID and positive host ID. It is a trusted persistence operation, not token issuance or public import authorization.
- `GetByID` includes revoked rows for management (`accounts.rs:290-302`).
- `GetByHash` queries the exact supplied hash and excludes every non-null revocation marker (`accounts.rs:49-60`). Expiry and current host status remain verifier checks. Each lookup creates an owned snapshot and performs a fresh database query.
- `Revoke` uses original `COALESCE(revoked_at, timestamp)` first-revocation semantics (`accounts.rs:306-313`). PostgreSQL `UPDATE ... RETURNING` makes the original update/read a single statement. Missing IDs return `(nil, nil)`.
- `UpdateLastUsed` updates only the original column, with missing IDs succeeding as a no-op (`accounts.rs:352-358`). The verifier treats failure as best effort; this never clears revocation or changes scopes.

[NewWebCodexHostUserResolver](../../repository/webcodex_host_user.go) calls existing `service.UserRepository.GetByID` for every authentication. It requires matching ID, active status and `DeletedAt == nil`, even if a context bypasses Ent soft deletion. Profile names, admin roles, balance and model groups grant no Runner rights. Missing users fail closed, dependency errors are sanitized, and cancellation yields no principal.

Construction is explicit: pass `NewWebCodexAPIKeyRepository(sqlDB)` and `NewWebCodexHostUserResolver(userRepository)` to `runner.NewAgentTokenAuthenticator` along with its required options. These are actual host types; no pool is opened by the constructor. Repository methods use individual SQL statements and do not join an outer Ent transaction. Future multi-record import/issuance must add explicit transaction composition before claiming atomic owner/grant rewrites. Auth lookup, user lookup and last-used touch are not one atomic transaction against concurrent revocation; already admitted in-flight work is not retroactively cancelled.

## Offline validation and remaining integration

`go test -race -count=1 ./internal/repository -run '^TestWebCodex'` uses SQLmock with fabricated records. Tests cover original column values and int64/null fidelity, exact query arguments, canonical projection, absent/revoked rows, first-revocation SQL, isolated last-used writes, scan/query errors, cancelled contexts, nil dependencies, current user disable/deletion/mismatch, sanitized errors, and full repository-to-verifier fixtures with exact UTF-8 token hashing and all stored scopes. The host adapter test runs real `NewUserRepository` and Ent queries over SQLmock. `go test ./migrations -run '^TestWebCodex'` checks the embedded DDL field/FK/index contract structurally.

No real PostgreSQL DDL, foreign-key enforcement or concurrent database transaction behavior is claimed by these tests. Test rows and token strings are fabricated. The migration has not been applied to a database. Public issuance/revocation/import endpoints, UI, other credential families, persistent registry/results and full G2 dispatch remain later features. The default-off application integration is provided by `server.ProvideWebCodexRunner`: when `webcodex_runner.enabled=true`, it receives the host SQL pool and User repository, mounts the four original polling routes, and closes the in-memory registry during HTTP shutdown. Applying the normal migration creates an empty credential table and seeds no credential.
