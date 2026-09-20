//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexTicketSettingsIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := integrationDB.ExecContext(ctx, `DELETE FROM settings WHERE key=$1`, service.CodexTicketSettingsKey)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM settings WHERE key=$1`, service.CodexTicketSettingsKey) })
	repo := NewCodexTicketSettingsRepository(integrationDB)
	proxyRepo := NewProxyRepository(integrationEntClient, integrationDB)
	p := &service.Proxy{Name: "codex-test", Protocol: "http", Host: "127.0.0.1", Port: 8080, Status: service.StatusActive, FallbackMode: service.FallbackModeNone}
	require.NoError(t, proxyRepo.Create(ctx, p))
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM proxies WHERE id=$1`, p.ID) })
	cfg := service.DefaultCodexTicketSettings()
	cfg.Enabled = true
	cfg.HarvestProxyID = &p.ID
	// First-create CAS: exactly one of two independent transactions may win.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := repo.Save(ctx, 0, cfg); errs <- e }()
	}
	wg.Wait()
	close(errs)
	wins, conflicts := 0, 0
	for e := range errs {
		if e == nil {
			wins++
		} else {
			require.ErrorIs(t, e, service.ErrCodexTicketRevisionConflict)
			conflicts++
		}
	}
	require.Equal(t, 1, wins)
	require.Equal(t, 1, conflicts)
	cfg, err = repo.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cfg.Revision)
	require.ErrorIs(t, proxyRepo.Delete(ctx, p.ID), service.ErrCodexTicketProxyInUse)
	p.Name = "rename-only"
	require.NoError(t, proxyRepo.Update(ctx, p))
	got, err := repo.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, cfg.Revision, got.Revision)
	p.Password = "test-secret"
	require.NoError(t, proxyRepo.Update(ctx, p))
	got, err = repo.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, cfg.Revision+1, got.Revision)
	// Context Ent transaction must include both proxy and revision changes, and rollback both.
	tx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err)
	p.Host = "changed.invalid"
	require.NoError(t, proxyRepo.Update(dbent.NewTxContext(ctx, tx), p))
	require.NoError(t, tx.Rollback())
	after, err := repo.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, got.Revision, after.Revision)
	original, err := proxyRepo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", original.Host)
	past := time.Now().Add(-time.Minute)
	original.ExpiresAt = &past
	require.NoError(t, proxyRepo.Update(ctx, original))
	beforeSweep, err := repo.Load(ctx)
	require.NoError(t, err)
	_, err = proxyRepo.SweepExpiredProxies(ctx, time.Now())
	require.NoError(t, err)
	after, err = repo.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeSweep.Revision+1, after.Revision)
	// Disabling preserves an expired selection; clearing it permits deletion.
	after.Enabled = false
	after, err = repo.Save(ctx, after.Revision, after)
	require.NoError(t, err)
	require.ErrorIs(t, proxyRepo.Delete(ctx, p.ID), service.ErrCodexTicketProxyInUse)
	after.HarvestProxyID = nil
	_, err = repo.Save(ctx, after.Revision, after)
	require.NoError(t, err)
	require.NoError(t, proxyRepo.Delete(ctx, p.ID))
}
