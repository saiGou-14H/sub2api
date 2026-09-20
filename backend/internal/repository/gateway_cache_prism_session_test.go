package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCachePrismSessionCrossInstancePreservesOpaqueStateAndTTL(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	first := NewGatewayCache(client).(*gatewayCache)
	second := NewGatewayCache(client).(*gatewayCache)
	ctx := context.Background()
	// These bytes are an opaque envelope, including large integers, whitespace,
	// nested snapshots and cookies. The repository must not re-encode them.
	payload := []byte("{\n  \"State\":{\"ProjectID\":\"synthetic-project\",\"ResponseID\":\"resp_synthetic\",\"CodexListenSnapshot\":{\"cursor\":9007199254740993,\"nested\":[{\"value\":18446744073709551615}]},\"Cookies\":[{\"Name\":\"session\",\"Value\":\"synthetic-cookie\"}]},\"ProjectHandle\":\"prism_proj_00000000-0000-4000-8000-000000000001\"\n}")
	require.NoError(t, first.SetPrismSession(ctx, "synthetic-chain", payload, time.Minute))
	got, err := second.GetPrismSession(ctx, "synthetic-chain")
	require.NoError(t, err)
	require.Equal(t, payload, got)

	server.FastForward(59 * time.Second)
	got, err = second.GetPrismSession(ctx, "synthetic-chain")
	require.NoError(t, err)
	require.Equal(t, payload, got)
	// Reading a continuation must not silently renew its lifetime.
	server.FastForward(2 * time.Second)
	got, err = first.GetPrismSession(ctx, "synthetic-chain")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestGatewayCachePrismSessionNamespaceIsolation(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewGatewayCache(client).(*gatewayCache)
	ctx := context.Background()
	const key = "shared-suffix"
	foreignKeys := []string{
		buildSessionKey(7, key),
		buildOpenAIResponsesSessionWindowKey(7, key),
		buildOpenAIWebConversationStateKey(7, key),
		buildOpenAIWebConversationLockKey(7, key),
		"prism:project:" + key,
		"prism:project-lock:" + key,
	}
	for _, foreignKey := range foreignKeys {
		require.NoError(t, client.Set(ctx, foreignKey, "foreign-state", time.Hour).Err())
	}
	got, err := cache.GetPrismSession(ctx, key)
	require.NoError(t, err)
	require.Nil(t, got)

	require.NoError(t, cache.SetPrismSession(ctx, key, []byte("first-session"), time.Minute))
	require.NoError(t, cache.SetPrismSession(ctx, key+"-other", []byte("second-session"), time.Hour))
	got, err = cache.GetPrismSession(ctx, key)
	require.NoError(t, err)
	require.Equal(t, []byte("first-session"), got)
	got, err = cache.GetPrismSession(ctx, key+"-other")
	require.NoError(t, err)
	require.Equal(t, []byte("second-session"), got)
	server.FastForward(2 * time.Minute)
	got, err = cache.GetPrismSession(ctx, key)
	require.NoError(t, err)
	require.Nil(t, got)
	got, err = cache.GetPrismSession(ctx, key+"-other")
	require.NoError(t, err)
	require.Equal(t, []byte("second-session"), got)
	for _, foreignKey := range foreignKeys {
		value, err := client.Get(ctx, foreignKey).Result()
		require.NoError(t, err)
		require.Equal(t, "foreign-state", value, "foreign namespace %s", foreignKey)
	}
}

func TestGatewayCachePrismSessionMissAndRedisFailure(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewGatewayCache(client).(*gatewayCache)
	ctx := context.Background()
	got, err := cache.GetPrismSession(ctx, "missing")
	require.NoError(t, err)
	require.Nil(t, got)
	require.NoError(t, cache.SetPrismSession(ctx, "existing", []byte("saved-state"), time.Hour))

	server.SetError("ERR synthetic session store failure")
	got, err = cache.GetPrismSession(ctx, "existing")
	require.Error(t, err)
	require.Empty(t, got, "a failed read must not return a stale session")
	require.Error(t, cache.SetPrismSession(ctx, "existing", []byte("replacement-state"), time.Hour))
	server.SetError("")
	got, err = cache.GetPrismSession(ctx, "existing")
	require.NoError(t, err)
	require.Equal(t, []byte("saved-state"), got, "a failed write must leave the existing state intact")
}

func TestGatewayCachePrismSessionRejectsInvalidWritesAndUnavailableStore(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewGatewayCache(client).(*gatewayCache)
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		key     string
		payload []byte
		ttl     time.Duration
	}{
		{name: "empty key", payload: []byte("state"), ttl: time.Hour},
		{name: "nil payload", key: "key", ttl: time.Hour},
		{name: "empty payload", key: "key", payload: []byte{}, ttl: time.Hour},
		{name: "zero TTL", key: "key", payload: []byte("state")},
		{name: "negative TTL", key: "key", payload: []byte("state"), ttl: -time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, cache.SetPrismSession(ctx, tc.key, tc.payload, tc.ttl))
		})
	}
	require.Empty(t, server.Keys())
	for name, unavailable := range map[string]*gatewayCache{"nil receiver": nil, "nil client": {}} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, unavailable.SetPrismSession(ctx, "key", []byte("state"), time.Hour))
			got, err := unavailable.GetPrismSession(ctx, "key")
			require.Error(t, err)
			require.Nil(t, got)
		})
	}
}
