// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// crates/webcodex-store/src/{accounts,models,schema}.rs.
package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/runner"
)

// WebCodexAPIKey is the original managed api_keys row. Only UserID changes
// storage type: it references the existing host users.id, never a username.
// Times remain nullable signed Unix seconds. KeyHash is not a plaintext key.
// This persistence DTO is not an HTTP input and does not grant import authority.
type WebCodexAPIKey struct {
	ID              string
	UserID          int64
	Name            string
	KeyHash         string
	KeyPrefix       string
	CreatedAt       int64
	LastUsedAt      *int64
	RevokedAt       *int64
	Scopes          string
	ExpiresAt       *int64
	Kind            string
	AllowedClientID *string
}

// WebCodexAPIKeyRepository uses the host SQL pool and migration runner. It owns
// no connection, worker, cache, or route. Unlike model API keys these records
// cannot authorize model requests. Each call observes current persisted state.
type WebCodexAPIKeyRepository struct{ db *sql.DB }

var _ runner.AgentCredentialRepository = (*WebCodexAPIKeyRepository)(nil)

func NewWebCodexAPIKeyRepository(db *sql.DB) *WebCodexAPIKeyRepository {
	return &WebCodexAPIKeyRepository{db: db}
}

const webCodexKeyColumns = `id, user_id, name, key_hash, key_prefix, created_at, last_used_at, revoked_at, scopes, expires_at, kind, allowed_client_id`

func (r *WebCodexAPIKeyRepository) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.db == nil {
		return runner.ErrAgentCredentialUnavailable
	}
	return nil
}

// Insert preserves supplied original fields; it does not issue a token, map
// legacy users, normalize scopes/kind, or revive a conflicting credential.
// Callers must authorize management/import before using this low-level method.
func (r *WebCodexAPIKeyRepository) Insert(ctx context.Context, key *WebCodexAPIKey) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if key == nil || key.ID == "" || key.UserID <= 0 {
		return runner.ErrAgentCredentialInvalid
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO wc_api_keys (`+webCodexKeyColumns+`)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, key.ID, key.UserID, key.Name, key.KeyHash, key.KeyPrefix, key.CreatedAt, key.LastUsedAt, key.RevokedAt, key.Scopes, key.ExpiresAt, key.Kind, key.AllowedClientID)
	return err
}

func scanWebCodexAPIKey(row *sql.Row) (*WebCodexAPIKey, error) {
	var key WebCodexAPIKey
	err := row.Scan(&key.ID, &key.UserID, &key.Name, &key.KeyHash, &key.KeyPrefix, &key.CreatedAt, &key.LastUsedAt, &key.RevokedAt, &key.Scopes, &key.ExpiresAt, &key.Kind, &key.AllowedClientID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &key, nil
}

// GetByID includes revoked rows for lifecycle management, as in the source.
func (r *WebCodexAPIKeyRepository) GetByID(ctx context.Context, id string) (*WebCodexAPIKey, error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	return scanWebCodexAPIKey(r.db.QueryRowContext(ctx, `SELECT `+webCodexKeyColumns+` FROM wc_api_keys WHERE id = $1`, id))
}

func (r *WebCodexAPIKeyRepository) GetByHash(ctx context.Context, hash string) (*runner.AgentCredentialRecord, error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	key, err := scanWebCodexAPIKey(r.db.QueryRowContext(ctx, `SELECT `+webCodexKeyColumns+` FROM wc_api_keys WHERE key_hash = $1 AND revoked_at IS NULL`, hash))
	if err != nil || key == nil {
		return nil, err
	}
	subject, err := runner.HostUserOwner(key.UserID)
	if err != nil {
		return nil, err
	}
	return &runner.AgentCredentialRecord{ID: key.ID, UserID: subject, KeyHash: key.KeyHash, Kind: key.Kind, Scopes: key.Scopes, AllowedClientID: key.AllowedClientID, ExpiresAt: key.ExpiresAt, RevokedAt: key.RevokedAt}, nil
}

// Revoke keeps the first revocation timestamp. UPDATE RETURNING makes the
// original revoke-and-read one atomic statement; unknown IDs return nil, nil.
func (r *WebCodexAPIKeyRepository) Revoke(ctx context.Context, id string, unixSeconds int64) (*WebCodexAPIKey, error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	return scanWebCodexAPIKey(r.db.QueryRowContext(ctx, `UPDATE wc_api_keys SET revoked_at = COALESCE(revoked_at, $2) WHERE id = $1 RETURNING `+webCodexKeyColumns, id, unixSeconds))
}

// UpdateLastUsed changes no authority fields and retains the source's no-op
// success for unknown IDs. The authenticator intentionally treats it as best effort.
func (r *WebCodexAPIKeyRepository) UpdateLastUsed(ctx context.Context, id string, unixSeconds int64) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `UPDATE wc_api_keys SET last_used_at = $2 WHERE id = $1`, id, unixSeconds)
	return err
}
