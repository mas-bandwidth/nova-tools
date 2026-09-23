package state

// The Redis heartbeat reader: the one adapter between the fleet store and the
// decision in state.go. It is what `nova-sprint fleet-state` and
// `nova-sprint fleet-live` call (the production callers PR #2752 lacked).
//
// What it reads is what the store's own writers put there, and nothing else:
//
//	benches              set of registered bench names (ns_bench_register)
//	bench:<b>:beat       hash (at ms, load1, ...), PEXPIRE 5 s (ns_bench_beat)
//	bench:<b>:desired    hash (slots, paused, ...)          (capacity)
//
// The key and its remaining TTL are the state; `at` only dates the write, and
// load1 rides beside it as a fact. The store's own TIME is the clock, so a
// reader's skew can never turn a live key into an expired one. Two pipelined
// round trips, whatever the fleet's size: TIME + SMEMBERS, then HGETALL, PTTL
// and HGET per bench. No SCAN, no KEYS.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Registry is the set every registered bench is a member of.
const Registry = "benches"

// BeatKey is a bench's heartbeat key; DesiredKey carries its pause (the hold).
func BeatKey(bench string) string    { return "bench:" + bench + ":beat" }
func DesiredKey(bench string) string { return "bench:" + bench + ":desired" }

// ReadRedis reads every registered bench from the fleet store, sorted by name,
// and the store's clock to decide them at.
func ReadRedis(ctx context.Context, c *redis.Client) ([]Bench, time.Time, error) {
	if c == nil {
		return nil, time.Time{}, fmt.Errorf("fleet state: nil redis client")
	}
	pipe := c.Pipeline()
	clock := pipe.Time(ctx)
	members := pipe.SMembers(ctx, Registry)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, time.Time{}, fmt.Errorf("fleet state: %w", err)
	}
	now := clock.Val()
	names := members.Val()
	sort.Strings(names)
	if len(names) == 0 {
		return nil, now, nil
	}

	type cmds struct {
		beat   *redis.MapStringStringCmd
		ttl    *redis.DurationCmd
		paused *redis.StringCmd
	}
	cs := make([]cmds, len(names))
	pipe = c.Pipeline()
	for i, b := range names {
		cs[i] = cmds{
			beat:   pipe.HGetAll(ctx, BeatKey(b)),
			ttl:    pipe.PTTL(ctx, BeatKey(b)),
			paused: pipe.HGet(ctx, DesiredKey(b), "paused"),
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, time.Time{}, fmt.Errorf("fleet state: %w", err)
	}

	out := make([]Bench, 0, len(names))
	for i, name := range names {
		b := Bench{Name: name}
		beat := cs[i].beat.Val()
		if load, err := strconv.ParseFloat(beat["load1"], 64); err == nil {
			b.Load = load
		}
		if len(beat) > 0 {
			b.Key = keyFrom(beat["at"], cs[i].ttl.Val(), now)
			p := cs[i].paused.Val()
			b.Key.Held = p == "1" || p == "true"
		}
		out = append(out, b)
	}
	return out, now, nil
}

// keyFrom rebuilds one heartbeat write from what the store says of it: the key
// expires at now+remaining. Written is the beat's own `at` when it parses and
// is not after now; otherwise the write is dated now, which changes nothing in
// the decision (that is the expiry alone). A key with no expiry (PTTL -1) is a
// beat some writer forgot to PEXPIRE: it exists, so it counts at this read and
// no longer -- one millisecond, never forever.
func keyFrom(at string, remaining time.Duration, now time.Time) *Key {
	if remaining <= 0 {
		// -1: no expiry. (-2, the key vanished between the two reads, only
		// reaches here with an empty hash, which is no key at all.)
		remaining = time.Millisecond
	}
	expires := now.Add(remaining)
	written := now
	if ms, err := strconv.ParseInt(at, 10, 64); err == nil {
		if w := time.UnixMilli(ms); !w.After(now) {
			written = w
		}
	}
	return &Key{Written: written, TTL: expires.Sub(written)}
}
