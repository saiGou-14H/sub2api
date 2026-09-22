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
	require.Equal(t, service.CodexTicketCookiePinRequired, loaded.CookiePinMode)
	require.Equal(t, 240, loaded.TTLSeconds)
	require.Equal(t, 210, loaded.RefreshBeforeSeconds)
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
func TestCodexTicketSettingsRepositoryLoadsLegacyCookiePinMode(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	cfg := service.DefaultCodexTicketSettings()
	cfg.TTLSeconds = 3600
	cfg.RefreshBeforeSeconds = 600
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	delete(fields, "cookie_pin_mode")
	raw, err = json.Marshal(fields)
	require.NoError(t, err)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT value FROM settings WHERE key=$1")).WithArgs(service.CodexTicketSettingsKey).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(string(raw)))
	loaded, err := NewCodexTicketSettingsRepository(db).Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, service.CodexTicketCookiePinOptional, loaded.CookiePinMode)
	require.Equal(t, 3600, loaded.TTLSeconds)
	require.Equal(t, 600, loaded.RefreshBeforeSeconds)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCodexTicketStoredCookiePinModeRoundTrip(t *testing.T) {
	for _, mode := range []string{service.CodexTicketCookiePinRequired, service.CodexTicketCookiePinOptional, "unknown"} {
		t.Run(mode, func(t *testing.T) {
			cfg := service.DefaultCodexTicketSettings()
			cfg.CookiePinMode = mode
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			loaded, err := decodeCodexTicketSettings(string(raw))
			if mode == "unknown" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, cfg, loaded)
		})
	}
}

func TestCodexTicketStoredCorruptionRejected(t *testing.T) {
	for _, raw := range []string{`{`, `null`, `{}`, `{"schema_version":99}`} {
		_, err := decodeCodexTicketSettings(raw)
		require.Error(t, err)
	}
}
