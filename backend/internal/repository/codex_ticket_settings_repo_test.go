package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func expectCodexTicketConfigLock(mock sqlmock.Sqlmock, cfg *service.CodexTicketSettings) {
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock($1)")).WithArgs(codexTicketConfigLock).WillReturnResult(sqlmock.NewResult(0, 1))
	rows := sqlmock.NewRows([]string{"value"})
	if cfg != nil {
		raw, _ := json.Marshal(cfg)
		rows.AddRow(string(raw))
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT value FROM settings WHERE key=$1 FOR UPDATE")).WithArgs(service.CodexTicketSettingsKey).WillReturnRows(rows)
}
func TestCodexTicketSettingsRepositoryCAS(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewCodexTicketSettingsRepository(db)
	ctx := context.Background()
	cfg := service.DefaultCodexTicketSettings()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT value FROM settings WHERE key=$1")).WithArgs(service.CodexTicketSettingsKey).WillReturnRows(sqlmock.NewRows([]string{"value"}))
	loaded, err := repo.Load(ctx)
	require.NoError(t, err)
	require.Zero(t, loaded.Revision)
	mock.ExpectBegin()
	expectCodexTicketConfigLock(mock, nil)
	mock.ExpectExec(`INSERT INTO settings`).WithArgs(service.CodexTicketSettingsKey, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	saved, err := repo.Save(ctx, 0, cfg)
	require.NoError(t, err)
	require.Equal(t, uint64(1), saved.Revision)
	mock.ExpectBegin()
	expectCodexTicketConfigLock(mock, &saved)
	mock.ExpectRollback()
	_, err = repo.Save(ctx, 0, cfg)
	require.ErrorIs(t, err, service.ErrCodexTicketRevisionConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestCodexTicketSettingsRepositoryUnavailableProxyRollback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	cfg := service.DefaultCodexTicketSettings()
	cfg.Enabled = true
	id := int64(8)
	cfg.HarvestProxyID = &id
	mock.ExpectBegin()
	expectCodexTicketConfigLock(mock, nil)
	mock.ExpectQuery(`SELECT protocol,status,expires_at FROM proxies`).WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"protocol", "status", "expires_at"}).AddRow("http", "inactive", nil))
	mock.ExpectRollback()
	_, err = NewCodexTicketSettingsRepository(db).Save(context.Background(), 0, cfg)
	require.ErrorIs(t, err, service.ErrCodexTicketProxyUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestCodexTicketStoredCorruptionRejected(t *testing.T) {
	for _, raw := range []string{`{`, `null`, `{}`, `{"schema_version":99}`} {
		_, err := decodeCodexTicketSettings(raw)
		require.Error(t, err)
	}
}
