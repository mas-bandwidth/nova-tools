// Package beat is the Go half of the beat liveness rule of nova-tools #3878,
// "keys do not expire": a beat hash carries no TTL. Its writer stamps `at`
// (the store's TIME in ms) and `stale_ms`, the window it promises to beat
// again within, and a reader judges it live while TIME - at < stale_ms. A
// friend or bench that stops beating keeps its hash and reads down, dated by
// its at, instead of vanishing from the store.
//
// The keys are friend:<f>:beat and bench:<b>:beat, written only by the
// nova_sprint functions (internal/nsprint/fn/lua/presence.lua and
// friend_serve.lua), whose own reads use the Lua half of this rule
// (internal/nsprint/fn/lua/00_beat.lua, NS.beat). A hash with no stale_ms (one
// written before #3878, still bounded by its TTL) is judged by existence, as
// every reader did before.
//
// A reader queues Read on the pipeline it already sends, with one TIME on the
// same pipeline, and asks Live after Exec: still one round trip.
package beat

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// The fields of a beat hash this rule reads.
const (
	FieldAt    = "at"
	FieldStale = "stale_ms"
)

// FriendKey and BenchKey are the two beat keys.
func FriendKey(friend string) string { return "friend:" + friend + ":beat" }
func BenchKey(bench string) string   { return "bench:" + bench + ":beat" }

// Cmd is one beat's reads queued on a pipeline: EXISTS and HMGET at stale_ms.
type Cmd struct {
	exists *redis.IntCmd
	fields *redis.SliceCmd
}

// Read queues the reads for the beat at key on p.
func Read(ctx context.Context, p redis.Pipeliner, key string) *Cmd {
	return &Cmd{exists: p.Exists(ctx, key), fields: p.HMGet(ctx, key, FieldAt, FieldStale)}
}

// Live reports whether the beat keeps its promise at now, the store's TIME
// read on the same pipeline.
func (c *Cmd) Live(now time.Time) bool {
	var at, stale string
	if vals, err := c.fields.Result(); err == nil && len(vals) == 2 {
		at, _ = vals[0].(string)
		stale, _ = vals[1].(string)
	}
	return Judge(c.exists.Val() == 1, at, stale, now.UnixMilli())
}

// Judge is the rule itself: an absent beat is down; a beat whose at and
// stale_ms both parse is live while nowMs - at < stale_ms; any other beat is
// judged by its existence.
func Judge(exists bool, at, stale string, nowMs int64) bool {
	if !exists {
		return false
	}
	a, errA := strconv.ParseInt(at, 10, 64)
	s, errS := strconv.ParseInt(stale, 10, 64)
	if errA != nil || errS != nil {
		return true
	}
	return nowMs-a < s
}

// LiveNow reads one beat and the store's clock in one pipeline.
func LiveNow(ctx context.Context, c redis.Cmdable, key string) (bool, error) {
	p := c.Pipeline()
	clock := p.Time(ctx)
	b := Read(ctx, p, key)
	if _, err := p.Exec(ctx); err != nil && err != redis.Nil {
		return false, err
	}
	return b.Live(clock.Val()), nil
}
