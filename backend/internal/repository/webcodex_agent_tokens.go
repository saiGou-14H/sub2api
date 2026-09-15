// SPDX-License-Identifier: Apache-2.0
// Managed lifecycle adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// crates/webcodex-store/src/accounts.rs; writes retain user and kind predicates.
package repository

import (
	"context"
	"database/sql"
	"errors"
)

// The HTTP metadata path never selects key_hash, even for revocation.
const webCodexSafeKeyColumns = `id, user_id, name, key_prefix, created_at, last_used_at, revoked_at, scopes, expires_at, kind, allowed_client_id`

func scanWebCodexSafeKey(row interface{ Scan(...any) error }) (*WebCodexAPIKey, error) {
	var key WebCodexAPIKey
	err := row.Scan(&key.ID, &key.UserID, &key.Name, &key.KeyPrefix, &key.CreatedAt, &key.LastUsedAt, &key.RevokedAt, &key.Scopes, &key.ExpiresAt, &key.Kind, &key.AllowedClientID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &key, nil
}

// ListAgentByUser includes expired/revoked agent rows, newest first, bounded to
// the original management maximum. No token hash is loaded into the response path.
func (r *WebCodexAPIKeyRepository) ListAgentByUser(ctx context.Context, userID int64) ([]*WebCodexAPIKey, error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+webCodexSafeKeyColumns+` FROM wc_api_keys WHERE user_id = $1 AND kind = 'agent' ORDER BY created_at DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]*WebCodexAPIKey, 0)
	for rows.Next() {
		key, err := scanWebCodexSafeKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// RevokeAgentByUser mutates only the authorized user's agent row. Repeating it
// preserves the first revocation timestamp. No preceding read grants mutation.
func (r *WebCodexAPIKeyRepository) RevokeAgentByUser(ctx context.Context, userID int64, id string, now int64) (*WebCodexAPIKey, error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	return scanWebCodexSafeKey(r.db.QueryRowContext(ctx, `UPDATE wc_api_keys SET revoked_at = COALESCE(revoked_at, $3) WHERE id = $1 AND user_id = $2 AND kind = 'agent' RETURNING `+webCodexSafeKeyColumns, id, userID, now))
}

// AgentTokenIdentity reports only owner/kind after a scoped revoke found no row,
// preserving the original foreign-owner/non-agent/missing HTTP diagnostics.
// This read cannot authorize or trigger a later unscoped write.
func (r *WebCodexAPIKeyRepository) AgentTokenIdentity(ctx context.Context, id string) (userID int64, kind string, found bool, err error) {
	if err = r.ready(ctx); err != nil {
		return
	}
	err = r.db.QueryRowContext(ctx, `SELECT user_id, kind FROM wc_api_keys WHERE id = $1`, id).Scan(&userID, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
		return
	}
	found = err == nil
	return
}
