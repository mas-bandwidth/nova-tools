package friendrow

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Redis is the fleet store behind Store. MGet is one MGET on the client the
// caller already opened. This type starts no process and dials nothing: the
// table this read replaces asked a client binary once a second, and a
// friend-row must not.
type Redis struct {
	rdb *redis.Client
}

// NewRedis wraps a client. A nil client fails the read; it is not an
// anonymous dial and it is not an empty room.
func NewRedis(rdb *redis.Client) Redis {
	return Redis{rdb: rdb}
}

// MGet is one MGET. A missing key is "". A value that is not a string is
// not turned into one: only the bytes the beat wrote are a beat.
func (r Redis) MGet(ctx context.Context, keys ...string) ([]string, error) {
	if r.rdb == nil {
		return nil, fmt.Errorf("friend-row: no redis client")
	}
	if len(keys) == 0 {
		return nil, nil
	}
	vals, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		s, ok := v.(string)
		if ok {
			out[i] = s
		}
	}
	return out, nil
}
