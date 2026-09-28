//go:build functional

package taskcard

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// Record and Records read task records for the tests' assertions: one HGETALL
// each, in one pipeline. No verb reaches them any more, so they live with the
// tests.
func Record(ctx context.Context, c redis.Cmdable, id string) (map[string]string, error) {
	recs, err := Records(ctx, c, id)
	if err != nil {
		return nil, err
	}
	return recs[0], nil
}
func Records(ctx context.Context, c redis.Cmdable, ids ...string) ([]map[string]string, error) {
	pipe := c.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGetAll(ctx, Key(id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	out := make([]map[string]string, len(ids))
	for i, cmd := range cmds {
		m := cmd.Val()
		if len(m) == 0 {
			return nil, &Refused{Why: "NOTASK " + Key(ids[i])}
		}
		out[i] = m
	}
	return out, nil
}
