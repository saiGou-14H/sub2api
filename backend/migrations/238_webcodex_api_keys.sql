-- SPDX-License-Identifier: Apache-2.0
-- WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9:
-- crates/webcodex-store/src/schema.rs api_keys (89-105).
-- Original managed hash credentials, separate from model gateway api_keys.
-- user_id is the existing host users.id; canonical subjects are projected at
-- the repository boundary. Legacy IDs require an explicit import mapping.
CREATE TABLE IF NOT EXISTS wc_api_keys (
    id TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    name TEXT NOT NULL,
    key_hash TEXT NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    last_used_at BIGINT,
    revoked_at BIGINT,
    scopes TEXT NOT NULL DEFAULT '',
    expires_at BIGINT,
    kind TEXT NOT NULL DEFAULT 'user',
    allowed_client_id TEXT
);
CREATE INDEX IF NOT EXISTS idx_wc_api_keys_user_id ON wc_api_keys(user_id);
-- UNIQUE(key_hash) already provides the original hash lookup index.
-- All times retain original signed Unix seconds, not host TIMESTAMPTZ values.
