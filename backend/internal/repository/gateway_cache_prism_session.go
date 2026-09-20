package repository

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

func (c *gatewayCache) SetPrismSession(ctx context.Context, key string, payload []byte, ttl time.Duration) error {
	if c == nil || c.rdb == nil || key == "" || len(payload) == 0 || ttl <= 0 {
		return errors.New("invalid Prism session store request")
	}
	return c.rdb.Set(ctx, "prism:session:"+key, payload, ttl).Err()
}

func (c *gatewayCache) GetPrismSession(ctx context.Context, key string) ([]byte, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("Prism session store unavailable")
	}
	data, err := c.rdb.Get(ctx, "prism:session:"+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return data, err
}
