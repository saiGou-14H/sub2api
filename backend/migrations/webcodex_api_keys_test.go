package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Structural regression only: this does not execute PostgreSQL DDL.
func TestWebCodexAPIKeysMigration(t *testing.T) {
	content, err := FS.ReadFile("238_webcodex_api_keys.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	for _, column := range []string{
		"id TEXT PRIMARY KEY", "user_id BIGINT NOT NULL REFERENCES users(id)",
		"name TEXT NOT NULL", "key_hash TEXT NOT NULL UNIQUE", "key_prefix TEXT NOT NULL",
		"created_at BIGINT NOT NULL", "last_used_at BIGINT", "revoked_at BIGINT",
		"scopes TEXT NOT NULL DEFAULT ''", "expires_at BIGINT", "kind TEXT NOT NULL DEFAULT 'user'", "allowed_client_id TEXT",
	} {
		require.Contains(t, sql, column)
	}
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS wc_api_keys")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_wc_api_keys_user_id ON wc_api_keys(user_id)")
	require.Equal(t, 1, strings.Count(sql, "CREATE TABLE"))
	require.NotContains(t, sql, "ALTER TABLE")
	require.NotContains(t, sql, "ON DELETE CASCADE")
	require.NotContains(t, sql, "INSERT INTO")
}
