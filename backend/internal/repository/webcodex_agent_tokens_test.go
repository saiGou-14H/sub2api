package repository

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/runner"
	"github.com/stretchr/testify/require"
)

func agentManagementRows(values ...driver.Value) *sqlmock.Rows {
	rows := sqlmock.NewRows(strings.Split(strings.ReplaceAll(webCodexSafeKeyColumns, " ", ""), ","))
	if values != nil {
		rows.AddRow(values...)
	}
	return rows
}
func TestWebCodexAgentManagementScopedMetadata(t *testing.T) {
	repo, mock := webCodexMock(t)
	listSQL := `SELECT ` + webCodexSafeKeyColumns + ` FROM wc_api_keys WHERE user_id = $1 AND kind = 'agent' ORDER BY created_at DESC LIMIT 200`
	mock.ExpectQuery(listSQL).WithArgs(int64(math.MaxInt64)).WillReturnRows(agentManagementRows("id", int64(math.MaxInt64), "name", "wc_agent_public", int64(math.MinInt64), int64(0), int64(-1), "agent:poll future:scope agent:poll", int64(math.MaxInt64), "agent", nil)).RowsWillBeClosed()
	keys, err := repo.ListAgentByUser(context.Background(), math.MaxInt64)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Empty(t, keys[0].KeyHash)
	require.Equal(t, int64(-1), *keys[0].RevokedAt)
	require.Equal(t, int64(math.MaxInt64), *keys[0].ExpiresAt)
	require.Equal(t, "agent:poll future:scope agent:poll", keys[0].Scopes)
	require.Nil(t, keys[0].AllowedClientID)
	query := `UPDATE wc_api_keys SET revoked_at = COALESCE(revoked_at, $3) WHERE id = $1 AND user_id = $2 AND kind = 'agent' RETURNING ` + webCodexSafeKeyColumns
	for _, at := range []int64{10, 20} {
		mock.ExpectQuery(query).WithArgs("id", int64(42), at).WillReturnRows(agentManagementRows("id", int64(42), "name", "prefix", int64(1), nil, int64(10), "", nil, "agent", "node"))
		key, err := repo.RevokeAgentByUser(context.Background(), 42, "id", at)
		require.NoError(t, err)
		require.Empty(t, key.KeyHash)
		require.Equal(t, int64(10), *key.RevokedAt)
	}
}
func TestWebCodexAgentManagementErrorsAndCancellation(t *testing.T) {
	repo, mock := webCodexMock(t)
	ctx := context.Background()
	query := `SELECT ` + webCodexSafeKeyColumns + ` FROM wc_api_keys WHERE user_id = $1 AND kind = 'agent' ORDER BY created_at DESC LIMIT 200`
	mock.ExpectQuery(query).WithArgs(int64(42)).WillReturnRows(agentManagementRows("id", "bad bigint", "name", "prefix", int64(1), nil, nil, "", nil, "agent", nil)).RowsWillBeClosed()
	keys, err := repo.ListAgentByUser(ctx, 42)
	require.Error(t, err)
	require.Nil(t, keys)
	rows := agentManagementRows("id", int64(42), "name", "prefix", int64(1), nil, nil, "", nil, "agent", nil).RowError(0, errors.New("fixture row error"))
	mock.ExpectQuery(query).WithArgs(int64(42)).WillReturnRows(rows).RowsWillBeClosed()
	_, err = repo.ListAgentByUser(ctx, 42)
	require.Error(t, err)
	mock.ExpectQuery(`UPDATE wc_api_keys SET revoked_at = COALESCE(revoked_at, $3) WHERE id = $1 AND user_id = $2 AND kind = 'agent' RETURNING `+webCodexSafeKeyColumns).WithArgs("id", int64(42), int64(1)).WillReturnError(errors.New("fixture update error"))
	_, err = repo.RevokeAgentByUser(ctx, 42, "id", 1)
	require.Error(t, err)
	mock.ExpectQuery(`SELECT user_id, kind FROM wc_api_keys WHERE id = $1`).WithArgs("missing").WillReturnRows(sqlmock.NewRows([]string{"user_id", "kind"}))
	_, _, found, err := repo.AgentTokenIdentity(ctx, "missing")
	require.NoError(t, err)
	require.False(t, found)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = repo.ListAgentByUser(cancelled, 42)
	require.ErrorIs(t, err, context.Canceled)
	_, err = repo.RevokeAgentByUser(cancelled, 42, "id", 1)
	require.ErrorIs(t, err, context.Canceled)
	_, _, _, err = repo.AgentTokenIdentity(cancelled, "id")
	require.ErrorIs(t, err, context.Canceled)
	var absent *WebCodexAPIKeyRepository
	_, err = absent.ListAgentByUser(ctx, 42)
	require.ErrorIs(t, err, runner.ErrAgentCredentialUnavailable)
	_, err = absent.RevokeAgentByUser(ctx, 42, "id", 1)
	require.ErrorIs(t, err, runner.ErrAgentCredentialUnavailable)
	_, _, _, err = absent.AgentTokenIdentity(ctx, "id")
	require.ErrorIs(t, err, runner.ErrAgentCredentialUnavailable)
}
