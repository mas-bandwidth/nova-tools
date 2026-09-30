//go:build functional

package sprintfn

import "github.com/redis/go-redis/v9"

// AddHookForTest puts h on the connection NewRedis owns, so a store-tier
// test counts the round trips of real steps and reads (testredis.RoundTrips,
// E8). It exists only in the functional build: a production build has no
// seam through which a caller's hook reaches the client (NewRedis).
func (r *Redis) AddHookForTest(h redis.Hook) {
	r.conn.AddHook(h)
}
