package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type sharedWebConversationCache struct {
	stubGatewayCache
	mu         sync.Mutex
	values     map[string][]byte
	lockOwner  string
	lockSerial int
}

func (c *sharedWebConversationCache) SetOpenAIWebConversationState(_ context.Context, groupID int64, stateKey string, payload []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil {
		c.values = make(map[string][]byte)
	}
	c.values[fmt.Sprintf("%d:%s", groupID, stateKey)] = append([]byte(nil), payload...)
	return nil
}

func (c *sharedWebConversationCache) GetOpenAIWebConversationState(_ context.Context, groupID int64, stateKey string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	payload := c.values[fmt.Sprintf("%d:%s", groupID, stateKey)]
	return append([]byte(nil), payload...), nil
}

func (c *sharedWebConversationCache) DeleteOpenAIWebConversationState(_ context.Context, groupID int64, stateKey string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.values, fmt.Sprintf("%d:%s", groupID, stateKey))
	return nil
}

func (c *sharedWebConversationCache) TryAcquireOpenAIWebConversationLock(_ context.Context, _ int64, _ string, _ time.Duration) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lockOwner != "" {
		return "", false, nil
	}
	c.lockSerial++
	c.lockOwner = fmt.Sprintf("owner-%d", c.lockSerial)
	return c.lockOwner, true, nil
}

func (c *sharedWebConversationCache) ReleaseOpenAIWebConversationLock(_ context.Context, _ int64, _ string, owner string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lockOwner == owner {
		c.lockOwner = ""
	}
	return nil
}

func TestOpenAIWSStateStore_BindGetDeleteResponseAccount(t *testing.T) {
	cache := &stubGatewayCache{}
	store := NewOpenAIWSStateStore(cache)
	ctx := context.Background()
	groupID := int64(7)

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_abc", 101, time.Minute))

	accountID, err := store.GetResponseAccount(ctx, groupID, "resp_abc")
	require.NoError(t, err)
	require.Equal(t, int64(101), accountID)

	require.NoError(t, store.DeleteResponseAccount(ctx, groupID, "resp_abc"))
	accountID, err = store.GetResponseAccount(ctx, groupID, "resp_abc")
	require.NoError(t, err)
	require.Zero(t, accountID)
}

func TestOpenAIWSStateStore_BindGetDeleteWebConversationState(t *testing.T) {
	store := NewOpenAIWSStateStore(&stubGatewayCache{})
	ctx := context.Background()
	state := OpenAIWebConversationState{
		ConversationID:  "conv-web",
		ParentMessageID: "msg-web",
		AccountID:       101,
		GroupID:         7,
		Model:           "auto",
	}
	require.NoError(t, store.BindWebConversationState(ctx, 7, "state-key", state, time.Minute))
	loaded, found, err := store.GetWebConversationState(ctx, 7, "state-key")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, state, *loaded)

	require.NoError(t, store.DeleteWebConversationState(ctx, 7, "state-key"))
	loaded, found, err = store.GetWebConversationState(ctx, 7, "state-key")
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, loaded)
}

func TestOpenAIWSStateStore_RedisStateIsAuthoritativeAcrossInstances(t *testing.T) {
	cache := &sharedWebConversationCache{}
	writer := NewOpenAIWSStateStore(cache)
	reader := NewOpenAIWSStateStore(cache)
	state := OpenAIWebConversationState{
		ConversationID:  "conv-shared",
		ParentMessageID: "msg-shared",
		AccountID:       101,
		GroupID:         7,
		Model:           "auto",
	}
	require.NoError(t, writer.BindWebConversationState(context.Background(), 7, "state-key", state, time.Minute))
	loaded, found, err := reader.GetWebConversationState(context.Background(), 7, "state-key")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, state, *loaded)

	require.NoError(t, writer.DeleteWebConversationState(context.Background(), 7, "state-key"))
	loaded, found, err = reader.GetWebConversationState(context.Background(), 7, "state-key")
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, loaded)
}

func TestOpenAIWSStateStore_DistributedConversationLockSerializesInstances(t *testing.T) {
	cache := &sharedWebConversationCache{}
	first := NewOpenAIWSStateStore(cache)
	second := NewOpenAIWSStateStore(cache)
	releaseFirst, acquired := first.(*defaultOpenAIWSStateStore).AcquireOpenAIWebConversationLock(context.Background(), 7, "lock-key")
	require.True(t, acquired)

	result := make(chan struct {
		release  func()
		acquired bool
	}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		release, ok := second.(*defaultOpenAIWSStateStore).AcquireOpenAIWebConversationLock(ctx, 7, "lock-key")
		result <- struct {
			release  func()
			acquired bool
		}{release: release, acquired: ok}
	}()

	time.Sleep(150 * time.Millisecond)
	releaseFirst()
	acquiredResult := <-result
	require.True(t, acquiredResult.acquired)
	acquiredResult.release()
}

func TestOpenAIWSStateStore_WebConversationStateExpires(t *testing.T) {
	store := NewOpenAIWSStateStore(&stubGatewayCache{})
	state := OpenAIWebConversationState{ConversationID: "conv-expiring", AccountID: 1}
	require.NoError(t, store.BindWebConversationState(context.Background(), 1, "expiring", state, time.Nanosecond))
	time.Sleep(2 * time.Millisecond)
	_, found, err := store.GetWebConversationState(context.Background(), 1, "expiring")
	require.NoError(t, err)
	require.False(t, found)
}

func TestOpenAIWSStateStore_HTTPResponseOwnerPersistsAcrossStoreInstances(t *testing.T) {
	cache := &stubGatewayCache{}
	ctx := context.Background()
	groupID := int64(8)
	writer := NewOpenAIWSStateStore(cache)

	require.NoError(t, writer.BindHTTPResponseOwner(ctx, groupID, "resp_owned", 201, 301, time.Minute))
	userID, apiKeyID, found, err := writer.GetHTTPResponseOwner(ctx, groupID, "resp_owned")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(201), userID)
	require.Equal(t, int64(301), apiKeyID)

	reader := NewOpenAIWSStateStore(cache)
	userID, apiKeyID, found, err = reader.GetHTTPResponseOwner(ctx, groupID, "resp_owned")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(201), userID)
	require.Equal(t, int64(301), apiKeyID)
}

func TestOpenAIWSStateStore_ResponseConnTTL(t *testing.T) {
	store := NewOpenAIWSStateStore(nil)
	store.BindResponseConn("resp_conn", "conn_1", 30*time.Millisecond)

	connID, ok := store.GetResponseConn("resp_conn")
	require.True(t, ok)
	require.Equal(t, "conn_1", connID)

	time.Sleep(60 * time.Millisecond)
	_, ok = store.GetResponseConn("resp_conn")
	require.False(t, ok)
}

func TestOpenAIWSStateStore_SessionTurnStateTTL(t *testing.T) {
	store := NewOpenAIWSStateStore(nil)
	store.BindSessionTurnState(9, "session_hash_1", "turn_state_1", 30*time.Millisecond)

	state, ok := store.GetSessionTurnState(9, "session_hash_1")
	require.True(t, ok)
	require.Equal(t, "turn_state_1", state)

	// group 隔离
	_, ok = store.GetSessionTurnState(10, "session_hash_1")
	require.False(t, ok)

	time.Sleep(60 * time.Millisecond)
	_, ok = store.GetSessionTurnState(9, "session_hash_1")
	require.False(t, ok)
}

func TestOpenAIWSStateStore_SessionConnTTL(t *testing.T) {
	store := NewOpenAIWSStateStore(nil)
	store.BindSessionConn(9, "session_hash_conn_1", "conn_1", 30*time.Millisecond)

	connID, ok := store.GetSessionConn(9, "session_hash_conn_1")
	require.True(t, ok)
	require.Equal(t, "conn_1", connID)

	// group 隔离
	_, ok = store.GetSessionConn(10, "session_hash_conn_1")
	require.False(t, ok)

	time.Sleep(60 * time.Millisecond)
	_, ok = store.GetSessionConn(9, "session_hash_conn_1")
	require.False(t, ok)
}

func TestOpenAIWSStateStore_GetResponseAccount_NoStaleAfterCacheMiss(t *testing.T) {
	cache := &stubGatewayCache{sessionBindings: map[string]int64{}}
	store := NewOpenAIWSStateStore(cache)
	ctx := context.Background()
	groupID := int64(17)
	responseID := "resp_cache_stale"
	cacheKey := openAIWSResponseAccountCacheKey(responseID)

	cache.sessionBindings[cacheKey] = 501
	accountID, err := store.GetResponseAccount(ctx, groupID, responseID)
	require.NoError(t, err)
	require.Equal(t, int64(501), accountID)

	delete(cache.sessionBindings, cacheKey)
	accountID, err = store.GetResponseAccount(ctx, groupID, responseID)
	require.NoError(t, err)
	require.Zero(t, accountID, "上游缓存失效后不应继续命中本地陈旧映射")
}

func TestOpenAIWSStateStore_MaybeCleanupRemovesExpiredIncrementally(t *testing.T) {
	raw := NewOpenAIWSStateStore(nil)
	store, ok := raw.(*defaultOpenAIWSStateStore)
	require.True(t, ok)

	expiredAt := time.Now().Add(-time.Minute)
	total := 2048
	store.responseToConnMu.Lock()
	for i := 0; i < total; i++ {
		store.responseToConn[fmt.Sprintf("resp_%d", i)] = openAIWSConnBinding{
			connID:    "conn_incremental",
			expiresAt: expiredAt,
		}
	}
	store.responseToConnMu.Unlock()

	store.lastCleanupUnixNano.Store(time.Now().Add(-2 * openAIWSStateStoreCleanupInterval).UnixNano())
	store.maybeCleanup()

	store.responseToConnMu.RLock()
	remainingAfterFirst := len(store.responseToConn)
	store.responseToConnMu.RUnlock()
	require.Less(t, remainingAfterFirst, total, "单轮 cleanup 应至少有进展")
	require.Greater(t, remainingAfterFirst, 0, "增量清理不要求单轮清空全部键")

	for i := 0; i < 8; i++ {
		store.lastCleanupUnixNano.Store(time.Now().Add(-2 * openAIWSStateStoreCleanupInterval).UnixNano())
		store.maybeCleanup()
	}

	store.responseToConnMu.RLock()
	remaining := len(store.responseToConn)
	store.responseToConnMu.RUnlock()
	require.Zero(t, remaining, "多轮 cleanup 后应逐步清空全部过期键")
}

func TestEnsureBindingCapacity_EvictsOneWhenMapIsFull(t *testing.T) {
	bindings := map[string]int{
		"a": 1,
		"b": 2,
	}

	ensureBindingCapacity(bindings, "c", 2)
	bindings["c"] = 3

	require.Len(t, bindings, 2)
	require.Equal(t, 3, bindings["c"])
}

func TestEnsureBindingCapacity_DoesNotEvictWhenUpdatingExistingKey(t *testing.T) {
	bindings := map[string]int{
		"a": 1,
		"b": 2,
	}

	ensureBindingCapacity(bindings, "a", 2)
	bindings["a"] = 9

	require.Len(t, bindings, 2)
	require.Equal(t, 9, bindings["a"])
}

type openAIWSStateStoreTimeoutProbeCache struct {
	setHasDeadline    bool
	getHasDeadline    bool
	deleteHasDeadline bool
	setDeadlineDelta  time.Duration
	getDeadlineDelta  time.Duration
	delDeadlineDelta  time.Duration
}

func (c *openAIWSStateStoreTimeoutProbeCache) GetSessionAccountID(ctx context.Context, _ int64, _ string) (int64, error) {
	if deadline, ok := ctx.Deadline(); ok {
		c.getHasDeadline = true
		c.getDeadlineDelta = time.Until(deadline)
	}
	return 123, nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) SetSessionAccountID(ctx context.Context, _ int64, _ string, _ int64, _ time.Duration) error {
	if deadline, ok := ctx.Deadline(); ok {
		c.setHasDeadline = true
		c.setDeadlineDelta = time.Until(deadline)
	}
	return errors.New("set failed")
}

func (c *openAIWSStateStoreTimeoutProbeCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) DeleteSessionAccountID(ctx context.Context, _ int64, _ string) error {
	if deadline, ok := ctx.Deadline(); ok {
		c.deleteHasDeadline = true
		c.delDeadlineDelta = time.Until(deadline)
	}
	return nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) SetGrokVideoPendingBilling(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}
func (c *openAIWSStateStoreTimeoutProbeCache) GetGrokVideoPendingBilling(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}
func (c *openAIWSStateStoreTimeoutProbeCache) ClaimGrokVideoBilled(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) ReleaseGrokVideoBilled(_ context.Context, _ string) error {
	return nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) SetReasoningContent(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}
func (c *openAIWSStateStoreTimeoutProbeCache) GetReasoningContent(_ context.Context, _ string) (string, error) {
	return "", ErrReasoningContentNotFound
}

func TestOpenAIWSStateStore_RedisOpsUseShortTimeout(t *testing.T) {
	probe := &openAIWSStateStoreTimeoutProbeCache{}
	store := NewOpenAIWSStateStore(probe)
	ctx := context.Background()
	groupID := int64(5)

	err := store.BindResponseAccount(ctx, groupID, "resp_timeout_probe", 11, time.Minute)
	require.Error(t, err)

	accountID, getErr := store.GetResponseAccount(ctx, groupID, "resp_timeout_probe")
	require.NoError(t, getErr)
	require.Equal(t, int64(11), accountID, "本地缓存命中应优先返回已绑定账号")

	require.NoError(t, store.DeleteResponseAccount(ctx, groupID, "resp_timeout_probe"))

	require.True(t, probe.setHasDeadline, "SetSessionAccountID 应携带独立超时上下文")
	require.True(t, probe.deleteHasDeadline, "DeleteSessionAccountID 应携带独立超时上下文")
	require.False(t, probe.getHasDeadline, "GetSessionAccountID 本用例应由本地缓存命中，不触发 Redis 读取")
	require.Greater(t, probe.setDeadlineDelta, 2*time.Second)
	require.LessOrEqual(t, probe.setDeadlineDelta, 3*time.Second)
	require.Greater(t, probe.delDeadlineDelta, 2*time.Second)
	require.LessOrEqual(t, probe.delDeadlineDelta, 3*time.Second)

	probe2 := &openAIWSStateStoreTimeoutProbeCache{}
	store2 := NewOpenAIWSStateStore(probe2)
	accountID2, err2 := store2.GetResponseAccount(ctx, groupID, "resp_cache_only")
	require.NoError(t, err2)
	require.Equal(t, int64(123), accountID2)
	require.True(t, probe2.getHasDeadline, "GetSessionAccountID 在缓存未命中时应携带独立超时上下文")
	require.Greater(t, probe2.getDeadlineDelta, 2*time.Second)
	require.LessOrEqual(t, probe2.getDeadlineDelta, 3*time.Second)
}

func TestWithOpenAIWSStateStoreRedisTimeout_WithParentContext(t *testing.T) {
	ctx, cancel := withOpenAIWSStateStoreRedisTimeout(context.Background())
	defer cancel()
	require.NotNil(t, ctx)
	_, ok := ctx.Deadline()
	require.True(t, ok, "应附加短超时")
}
