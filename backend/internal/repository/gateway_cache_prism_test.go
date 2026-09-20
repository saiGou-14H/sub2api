package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCachePrismProjectCrossInstanceTTLAndLeaseOwnership(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	first := NewGatewayCache(client).(service.PrismProjectStore)
	second := NewGatewayCache(client).(service.PrismProjectStore)
	ctx := context.Background()
	handle := "prism_proj_00000000-0000-4000-8000-000000000001"
	payload := []byte(`{"AccountID":7,"APIKeyID":19,"State":{"ProjectID":"private-project"}}`)
	require.NoError(t, first.SetPrismProject(ctx, handle, payload, 7*24*time.Hour))
	got, err := second.GetPrismProject(ctx, handle)
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(got))
	claimed, err := first.TryAcquirePrismProjectLock(ctx, handle, "first", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	saved, err := first.SavePrismProjectWithLease(ctx, handle, "first", []byte(`{"state":"first"}`), time.Hour)
	require.NoError(t, err)
	require.True(t, saved)
	claimed, err = second.TryAcquirePrismProjectLock(ctx, handle, "second", time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, second.ReleasePrismProjectLock(ctx, handle, "second"))
	refreshed, err := second.RefreshPrismProjectLock(ctx, handle, "second", time.Hour)
	require.NoError(t, err)
	require.False(t, refreshed)
	server.FastForward(time.Minute + time.Second)
	claimed, err = second.TryAcquirePrismProjectLock(ctx, handle, "second", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	saved, err = second.SavePrismProjectWithLease(ctx, handle, "second", []byte(`{"state":"second"}`), time.Hour)
	require.NoError(t, err)
	require.True(t, saved)
	saved, err = first.SavePrismProjectWithLease(ctx, handle, "first", []byte(`{"state":"stale"}`), time.Hour)
	require.NoError(t, err)
	require.False(t, saved)
	got, err = first.GetPrismProject(ctx, handle)
	require.NoError(t, err)
	require.JSONEq(t, `{"state":"second"}`, string(got))
	// A delayed release from the expired lease must not delete its successor.
	require.NoError(t, first.ReleasePrismProjectLock(ctx, handle, "first"))
	refreshed, err = second.RefreshPrismProjectLock(ctx, handle, "second", time.Hour)
	require.NoError(t, err)
	require.True(t, refreshed)
	require.NoError(t, second.ReleasePrismProjectLock(ctx, handle, "second"))
	server.FastForward(7 * 24 * time.Hour)
	got, err = first.GetPrismProject(ctx, handle)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestGatewayCachePrismProjectCreationCannotOverwriteAndLeaseCannotResurrect(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewGatewayCache(client).(service.PrismProjectStore)
	ctx := context.Background()
	handle := "prism_proj_00000000-0000-4000-8000-000000000002"
	require.NoError(t, cache.SetPrismProject(ctx, handle, []byte(`{"owner":1}`), time.Minute))
	require.Error(t, cache.SetPrismProject(ctx, handle, []byte(`{"owner":2}`), time.Hour))
	claimed, err := cache.TryAcquirePrismProjectLock(ctx, handle, "owner", time.Hour)
	require.NoError(t, err)
	require.True(t, claimed)
	server.FastForward(2 * time.Minute)
	saved, err := cache.SavePrismProjectWithLease(ctx, handle, "owner", []byte(`{"owner":1}`), time.Hour)
	require.NoError(t, err)
	require.False(t, saved)
}

func TestGatewayCachePrismRejectsNonPublicHandles(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewGatewayCache(client).(service.PrismProjectStore)
	require.Error(t, cache.SetPrismProject(context.Background(), "upstream-project", []byte(`{}`), time.Hour))
	claimed, err := cache.TryAcquirePrismProjectLock(context.Background(), "prism_proj_../escape", "owner", time.Hour)
	require.Error(t, err)
	require.False(t, claimed)
}
