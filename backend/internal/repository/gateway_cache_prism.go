package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const prismProjectPrefix = "prism:project:"
const prismProjectLockPrefix = "prism:project-lock:"

var _ service.PrismProjectStore = (*gatewayCache)(nil)

var refreshPrismProjectLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0
`)

var savePrismProjectWithLeaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] or redis.call('EXISTS', KEYS[2]) == 0 then
  return 0
end
redis.call('SET', KEYS[2], ARGV[2], 'PX', ARGV[3])
return 1
`)

func validPrismCacheHandle(handle string) bool {
	if !strings.HasPrefix(handle, "prism_proj_") {
		return false
	}
	_, err := uuid.Parse(strings.TrimPrefix(handle, "prism_proj_"))
	return err == nil
}

func (c *gatewayCache) GetPrismProject(ctx context.Context, handle string) ([]byte, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("Prism project storage unavailable")
	}
	if !validPrismCacheHandle(handle) {
		return nil, nil
	}
	value, err := c.rdb.Get(ctx, prismProjectPrefix+handle).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return value, err
}

func (c *gatewayCache) SetPrismProject(ctx context.Context, handle string, payload []byte, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return errors.New("Prism project storage unavailable")
	}
	if !validPrismCacheHandle(handle) || len(payload) == 0 || ttl <= 0 {
		return errors.New("invalid Prism project record")
	}
	created, err := c.rdb.SetNX(ctx, prismProjectPrefix+handle, payload, ttl).Result()
	if err != nil {
		return err
	}
	if !created {
		return errors.New("Prism project already exists")
	}
	return nil
}

// SavePrismProjectWithLease fences stale writers after expiration, failover or
// a Redis restart. Checking and writing in one script closes the GET/SET race.
func (c *gatewayCache) SavePrismProjectWithLease(ctx context.Context, handle, owner string, payload []byte, ttl time.Duration) (bool, error) {
	if c == nil || c.rdb == nil || !validPrismCacheHandle(handle) || strings.TrimSpace(owner) == "" || len(payload) == 0 || ttl <= 0 {
		return false, errors.New("invalid Prism project lease update")
	}
	result, err := savePrismProjectWithLeaseScript.Run(ctx, c.rdb, []string{prismProjectLockPrefix + handle, prismProjectPrefix + handle}, owner, payload, ttl.Milliseconds()).Int()
	return result == 1, err
}

func (c *gatewayCache) TryAcquirePrismProjectLock(ctx context.Context, handle, owner string, ttl time.Duration) (bool, error) {
	if c == nil || c.rdb == nil || !validPrismCacheHandle(handle) || strings.TrimSpace(owner) == "" || ttl <= 0 {
		return false, errors.New("invalid Prism project lock")
	}
	return c.rdb.SetNX(ctx, prismProjectLockPrefix+handle, owner, ttl).Result()
}

func (c *gatewayCache) RefreshPrismProjectLock(ctx context.Context, handle, owner string, ttl time.Duration) (bool, error) {
	if c == nil || c.rdb == nil || !validPrismCacheHandle(handle) || strings.TrimSpace(owner) == "" || ttl <= 0 {
		return false, errors.New("invalid Prism project lock")
	}
	result, err := refreshPrismProjectLockScript.Run(ctx, c.rdb, []string{prismProjectLockPrefix + handle}, owner, ttl.Milliseconds()).Int()
	return result == 1, err
}

func (c *gatewayCache) ReleasePrismProjectLock(ctx context.Context, handle, owner string) error {
	if c == nil || c.rdb == nil || !validPrismCacheHandle(handle) || strings.TrimSpace(owner) == "" {
		return errors.New("invalid Prism project lock")
	}
	return releaseOpenAIWebConversationLockScript.Run(ctx, c.rdb, []string{prismProjectLockPrefix + handle}, owner).Err()
}
