//go:build functional

package sprint

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// tripHook counts round trips: one per command or pipeline.
type tripHook struct{ n atomic.Int64 }

func (h *tripHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *tripHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmd)
	}
}
func (h *tripHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmds)
	}
}

// TestControl21 is #2939 control 21 on the one count (#one-count): the
// sprint's four cards in the ws index (one waiting, one working, two
// landed, one of them in the hour before --now) and its sentinel waiting,
// opened 1 h before --now, print exactly one status line: the ws index's
// numbers, sentinel counted, the eta from the last hour's landings, read in
// two round trips (the memberships, then every count in one pipeline). The
// legacy s:<S>:idx:task sets the old line counted are not read.
func TestControl21(t *testing.T) {
	addr, c := planRedis(t)
	ctx := context.Background()
	const s = "control-21"
	now := time.Unix(1790179200, 0) // 2026-09-23 16:00 UTC, 12:00 EDT
	opened := now.Add(-time.Hour).UnixMilli()
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) + "-0" }
	for _, err := range []error{
		c.HSet(ctx, "s:"+s, "status", "open", "opened_at", opened).Err(),
		c.ZAdd(ctx, "sprint:order", redis.Z{Score: float64(opened), Member: s}).Err(),
		c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: "st"}).Err(),
		c.ZAdd(ctx, "ws:st:waiting", redis.Z{Score: 1, Member: "t1"}, redis.Z{Score: 2, Member: "st:sentinel"}).Err(),
		c.ZAdd(ctx, "ws:st:working", redis.Z{Score: 3, Member: "t2"}).Err(),
		c.ZAdd(ctx, "ws:st:landed", redis.Z{Score: 4, Member: "t3"}, redis.Z{Score: 5, Member: "t4"}).Err(),
		c.XAdd(ctx, &redis.XAddArgs{Stream: "ws:log", ID: ms(-2 * time.Hour), Values: []any{"id", "t3", "to", "landed"}}).Err(),
		c.XAdd(ctx, &redis.XAddArgs{Stream: "ws:log", ID: ms(-30 * time.Minute), Values: []any{"id", "t4", "to", "landed"}}).Err(),
		// the legacy index the old line counted: never read now
		c.SAdd(ctx, "s:"+s+":idx:task:closed", "x1", "x2", "x3").Err(),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	hook := &tripHook{}
	client.AddHook(hook)
	for _, name := range []string{s, ""} {
		hook.n.Store(0)
		lines, err := StatusLines(ctx, store.New(client), name, now)
		if err != nil {
			t.Fatal(err)
		}
		// 5 cards (the sentinel one of them), 2 landed, 3 left at 1 an hour.
		if want := s + " open 2/5 done 40%, left 3, eta 15:00 ET"; len(lines) != 1 || lines[0] != want {
			t.Fatalf("status %q = %q; want exactly %q", name, lines, want)
		}
		if got := hook.n.Load(); got != 2 {
			t.Fatalf("status %q took %d round trips; want 2", name, got)
		}
	}
}
