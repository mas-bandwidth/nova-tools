//go:build functional

package reconcile_test

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// TestProgressOldestFollowsTheOrderWithinOnePass is probe (2) of the #4322
// fix round, clock injected: cards a (3 h old), b (2 h), c (1 h) and the
// sentinel (5 h, the stream's age) on one stream, the order written; PROGRESS
// reads oldest=3h0m0s (a's age, never the sentinel's 5 h). a lands through
// the library (no door writes the order), so the stored base is a's; one
// waiting-resolve pass rewrites the order (one ORDER line) and PROGRESS
// reads oldest=2h0m0s, b's age.
func TestProgressOldestFollowsTheOrderWithinOnePass(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)
	const S, stream = "sprint-order", "order: progress"
	stop := ws.SentinelID(stream)
	ages := map[string]time.Duration{"a": 3 * time.Hour, "b": 2 * time.Hour, "c": time.Hour, stop: 5 * time.Hour}
	for i, id := range []string{"a", "b", "c"} {
		r := taskcard.PushRequest{ID: id, Stream: stream, Kind: "build", Title: id, By: "test",
			Ref: "mas-bandwidth/nova-tools#" + strconv.Itoa(10+i), Fields: []string{"paths", "internal/" + id}}
		if _, err := taskcard.Push(ctx, c, r); err != nil {
			t.Fatal(err)
		}
	}
	pipe := c.Pipeline()
	for id, age := range ages {
		at := float64(now.Add(-age).UnixMilli())
		pipe.HSet(ctx, "task:"+id, "created_at", strconv.FormatFloat(at, 'f', 0, 64))
		w := "ready"
		if id == stop {
			w = "waiting"
		}
		pipe.ZAdd(ctx, ws.Key(stream, w), redis.Z{Score: at, Member: id})
	}
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: S})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Reorder(ctx, c, stream, "test"); err != nil {
		t.Fatal(err)
	}
	lease, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	// A fresh duty per read (the duty gates on its cadence and prints a
	// line only on change): its PROGRESS line's oldest= field.
	oldest := func() string {
		t.Helper()
		var out bytes.Buffer
		p := &reconcile.Progress{Client: c, Out: &out, Now: func() time.Time { return now }}
		if _, err := p.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		for _, s := range p.Last.Samples {
			if s.Stream == stream {
				line := reconcile.ProgressLine(s, p.Last.States[stream], now, time.Hour)
				for _, f := range strings.Fields(line) {
					if strings.HasPrefix(f, "oldest=") {
						return f
					}
				}
			}
		}
		t.Fatalf("no sample for %s: %+v", stream, p.Last.Samples)
		return ""
	}
	if got := oldest(); got != "oldest=3h0m0s" {
		t.Fatalf("before the land: %s, want a's 3h0m0s (not the sentinel's 5 h)", got)
	}

	if _, err := taskcard.Move(ctx, c, "a", "working", taskcard.Opts{By: "test", As: "f1", Friend: "f1", SetFriend: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Land(ctx, c, "a", "test", "0123abcd", "merged"); err != nil {
		t.Fatal(err)
	}
	if got := oldest(); got != "oldest=2h59m59s" {
		t.Fatalf("after the land, before a pass: %s, want 2h59m59s (the stored base is still a's: b scores base+1 ms)", got)
	}
	var rout bytes.Buffer
	duty := &reconcile.WaitingResolve{Client: c, Out: &rout}
	if _, err := duty.Run(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rout.String(), `ORDER stream="order: progress" order=3 why=stale`) {
		t.Fatalf("resolver pass: %q", rout.String())
	}
	if got := oldest(); got != "oldest=2h0m0s" {
		t.Fatalf("after one resolver pass: %s, want b's 2h0m0s", got)
	}
	t.Logf("oldest before=3h0m0s after the land=stale after one pass=2h0m0s; %s", strings.TrimSpace(rout.String()))
}
