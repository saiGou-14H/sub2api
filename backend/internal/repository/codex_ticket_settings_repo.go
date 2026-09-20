package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const codexTicketConfigLock int64 = 0x434f44585449434b

type codexTicketSettingsRepository struct{ db *sql.DB }

func NewCodexTicketSettingsRepository(db *sql.DB) service.CodexTicketSettingsRepository {
	return &codexTicketSettingsRepository{db: db}
}

func decodeCodexTicketSettings(raw string) (service.CodexTicketSettings, error) {
	var cfg service.CodexTicketSettings
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, errors.New("stored Codex ticket settings are invalid")
	}
	cfg, err := service.NormalizeCodexTicketSettings(cfg)
	if err != nil {
		return cfg, errors.New("stored Codex ticket settings are invalid")
	}
	return cfg, nil
}
func (r *codexTicketSettingsRepository) Load(ctx context.Context) (service.CodexTicketSettings, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, service.CodexTicketSettingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return service.DefaultCodexTicketSettings(), nil
	}
	if err != nil {
		return service.CodexTicketSettings{}, err
	}
	return decodeCodexTicketSettings(raw)
}

// Every configuration/proxy writer takes this lock before any proxy or account locks.
// sqlExecutor is implemented by both SQL transactions and Ent transaction clients.
func lockCodexTicketSettings(ctx context.Context, exec sqlExecutor) (service.CodexTicketSettings, error) {
	if _, err := exec.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, codexTicketConfigLock); err != nil {
		return service.CodexTicketSettings{}, err
	}
	rows, err := exec.QueryContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, service.CodexTicketSettingsKey)
	if err != nil {
		return service.CodexTicketSettings{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return service.DefaultCodexTicketSettings(), rows.Err()
	}
	var raw string
	if err := rows.Scan(&raw); err != nil {
		return service.CodexTicketSettings{}, err
	}
	return decodeCodexTicketSettings(raw)
}
func writeCodexTicketSettings(ctx context.Context, exec sqlExecutor, cfg service.CodexTicketSettings) error {
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = exec.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`, service.CodexTicketSettingsKey, string(encoded))
	return err
}
func bumpCodexTicketProxyRevision(ctx context.Context, exec sqlExecutor, cfg service.CodexTicketSettings, id int64) error {
	if cfg.HarvestProxyID == nil || *cfg.HarvestProxyID != id {
		return nil
	}
	if cfg.Revision >= service.CodexTicketMaxRevision {
		return service.ErrCodexTicketRevisionConflict
	}
	cfg.Revision++
	return writeCodexTicketSettings(ctx, exec, cfg)
}
func (r *codexTicketSettingsRepository) Save(ctx context.Context, expected uint64, next service.CodexTicketSettings) (service.CodexTicketSettings, error) {
	next, err := service.NormalizeCodexTicketSettings(next)
	if err != nil {
		return next, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return next, err
	}
	defer tx.Rollback()
	current, err := lockCodexTicketSettings(ctx, tx)
	if err != nil {
		return next, err
	}
	if current.Revision != expected || current.Revision >= service.CodexTicketMaxRevision {
		return next, service.ErrCodexTicketRevisionConflict
	}
	if next.HarvestProxyID != nil {
		var p service.Proxy
		var expires sql.NullTime
		err = tx.QueryRowContext(ctx, `SELECT protocol,status,expires_at FROM proxies WHERE id=$1 AND deleted_at IS NULL FOR NO KEY UPDATE`, *next.HarvestProxyID).Scan(&p.Protocol, &p.Status, &expires)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return next, err
		}
		state := "missing"
		if err == nil {
			if expires.Valid {
				p.ExpiresAt = &expires.Time
			}
			state = service.CodexTicketProxyState(&p, time.Now())
		}
		if next.Enabled && state != "active" {
			return next, service.ErrCodexTicketProxyUnavailable.WithMetadata(map[string]string{"proxy_state": state})
		}
	}
	next.Revision = current.Revision + 1
	if err = writeCodexTicketSettings(ctx, tx, next); err != nil {
		return next, err
	}
	if err = tx.Commit(); err != nil {
		return next, err
	}
	return next, nil
}
