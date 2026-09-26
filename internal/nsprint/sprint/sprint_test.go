//go:build functional

package sprint

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// countHook counts every command the client sends, pipelined or not.
type countHook struct{ n atomic.Int64 }

func (h *countHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *countHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmd)
	}
}
func (h *countHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.n.Add(int64(len(cmds)))
		return next(ctx, cmds)
	}
}

// TestControl21 is #2939 control 21: 4 tasks (1 closed, 1 cancelled) and 2
// cards (1 landed), opened 1 h before --now, print exactly one status line
// from exactly one Redis command.
func TestControl21(t *testing.T) {
	addr, c := planRedis(t)
	ctx := context.Background()
	const s = "control-21"
	now := time.Unix(1790179200, 0) // 2026-09-23 16:00 UTC
	opened := now.Add(-time.Hour).UnixMilli()
	for _, err := range []error{
		c.HSet(ctx, "s:"+s, "status", "open", "opened_at", opened).Err(),
		c.SAdd(ctx, "s:"+s+":idx:task:open", "t1").Err(),
		c.SAdd(ctx, "s:"+s+":idx:task:claimed", "t2").Err(),
		c.SAdd(ctx, "s:"+s+":idx:task:closed", "t3").Err(),
		c.SAdd(ctx, "s:"+s+":idx:task:cancelled", "t4").Err(),
		c.SAdd(ctx, "s:"+s+":idx:card:running", "c1").Err(),
		c.SAdd(ctx, "s:"+s+":idx:card:landed", "c2").Err(),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TZ", "America/New_York")

	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	hook := &countHook{}
	client.AddHook(hook)
	lines, err := StatusLines(ctx, store.New(client), s, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := s + " open 2/5 40% -> eta 13:30 EDT"; len(lines) != 1 || lines[0] != want {
		t.Fatalf("status = %q; want exactly %q", lines, want)
	}
	if got := hook.n.Load(); got != 1 {
		t.Fatalf("status sent %d Redis commands; want exactly 1", got)
	}
}
