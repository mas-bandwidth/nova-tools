package ws

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Check verifies the ws invariants without a scan of task:*: every member of
// every stream's six sets is in exactly one of them, its hash names that
// stream and state, and its score is the hash's created_at in ms (the one
// score every set uses: the task's age); ws:order and ws:names hold the same streams; and each id
// in ids (the task side, which a set walk cannot see) is in the one set its
// hash names, or in none when closed or when it names no stream (a task
// outside the index). Three pipelined round trips. It returns
// the first violations it finds (at most 20), nil when there are none.
func Check(ctx context.Context, c redis.Cmdable, ids []string) error {
	pipe := c.Pipeline()
	order := pipe.ZRange(ctx, "ws:order", 0, -1)
	names := pipe.SMembers(ctx, "ws:names")
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return err
	}
	var bad []string
	add := func(format string, a ...any) {
		if len(bad) < 20 {
			bad = append(bad, fmt.Sprintf(format, a...))
		}
	}
	inNames := map[string]bool{}
	for _, s := range names.Val() {
		inNames[s] = true
	}
	for _, s := range order.Val() {
		if !inNames[s] {
			add("ws:order holds %q, not in ws:names", s)
		}
	}
	if len(order.Val()) != len(names.Val()) {
		add("ws:order has %d streams, ws:names %d", len(order.Val()), len(names.Val()))
	}
	type where struct {
		stream, state string
		score         float64
	}
	at := map[string]where{}
	pipe = c.Pipeline()
	type set struct {
		w   where
		cmd *redis.ZSliceCmd
	}
	var sets []set
	for _, s := range names.Val() {
		for _, st := range States {
			sets = append(sets, set{where{s, st, 0}, pipe.ZRangeWithScores(ctx, Key(s, st), 0, -1)})
		}
	}
	if len(sets) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return err
		}
	}
	for _, s := range sets {
		for _, z := range s.cmd.Val() {
			id := fmt.Sprint(z.Member)
			if prev, ok := at[id]; ok {
				add("%s in %s and %s", id, Key(prev.stream, prev.state), Key(s.w.stream, s.w.state))
				continue
			}
			at[id] = where{s.w.stream, s.w.state, z.Score}
		}
	}
	all := make([]string, 0, len(at)+len(ids))
	for id := range at {
		all = append(all, id)
	}
	for _, id := range ids {
		if _, ok := at[id]; !ok {
			all = append(all, id)
		}
	}
	pipe = c.Pipeline()
	hms := make([]*redis.SliceCmd, len(all))
	for i, id := range all {
		hms[i] = pipe.HMGet(ctx, "task:"+id, "stream", "state", "created_at")
	}
	if len(all) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return err
		}
	}
	for i, id := range all {
		v := hms[i].Val()
		stream, _ := v[0].(string)
		state, _ := v[1].(string)
		created, _ := v[2].(string)
		w, inSet := at[id]
		switch {
		case state == Closed && inSet:
			add("%s is closed but in %s", id, Key(w.stream, w.state))
		case state == Closed, stream == "" && !inSet:
			// closed, or a task with no stream: outside every set, rightly
		case !inSet:
			add("%s (stream %q state %q) is in no set", id, stream, state)
		case w.stream != stream || w.state != state:
			add("%s is in %s but its hash says stream %q state %q", id, Key(w.stream, w.state), stream, state)
		default:
			if ms, ok := CreatedMS(created); !ok || ms != w.score {
				add("%s scores %.0f in %s, not its created_at %q", id, w.score, Key(w.stream, w.state), created)
			}
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("ws invariants: %v", bad)
	}
	return nil
}

// CreatedMS reads a task's created_at as epoch ms, the score of every ws set:
// a number is ms already; friend-queue wrote RFC 3339 UTC seconds.
func CreatedMS(v string) (float64, bool) {
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		return n, true
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return float64(t.UnixMilli()), true
	}
	return 0, false
}
