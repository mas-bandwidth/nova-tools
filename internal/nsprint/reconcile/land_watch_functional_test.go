//go:build functional

package reconcile_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestLandWatchCutsANewIdEachEpisode (nova-tools #4324): through the real
// task push, a stream's first episode cuts merge-<slug>-1 for the frontier
// friend; when merging empties the card fields go but seq stays; the next
// episode cuts merge-<slug>-2 (a repeated id would be CONFLICT or CLOSED
// against the old card, one id per pass forever).
func TestLandWatchCutsANewIdEachEpisode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const s, sprint = "quack", "lw-sprint"
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: s})
	c.SAdd(ctx, "ws:names", s)
	c.HSet(ctx, "s:"+sprint, "status", "open")
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	for _, f := range []string{"stella", "rowan"} {
		c.SAdd(ctx, "friends", f)
		c.HSet(ctx, "friend:"+f+":desired", "slots", 4, "paused", "0", "tiers", "frontier")
		c.HSet(ctx, "friend:"+f+":beat", "host", "fixture")
	}
	c.HSet(ctx, "friend:rowan:roles", "roles", "coordinator")
	now := time.UnixMilli(1700000000000)
	w := &reconcile.LandWatch{Client: c, Now: func() time.Time { return now }, Repo: "mas-bandwidth/quack",
		Notify: func(context.Context, reconcile.LandWake) error { return nil }}
	pass := func() {
		t.Helper()
		now = now.Add(time.Second)
		if _, err := w.Run(ctx, nil); err != nil {
			t.Fatalf("pass: %v", err)
		}
	}
	c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: 1, Member: "q1"})
	pass()
	rec, _ := c.HGetAll(ctx, reconcile.LandMergeKey(s)).Result()
	if rec["task"] != "merge-quack-1" || rec["to"] != "rowan" && rec["to"] != "stella" || rec["seq"] != "1" {
		t.Fatalf("first episode: %v", rec)
	}
	card, _ := c.HGetAll(ctx, "task:merge-quack-1").Result()
	if card["kind"] != "merge" || card["state"] != "open" || card["dest"] != rec["to"] {
		t.Fatalf("merge card record: %v", card)
	}
	// The landing moved q1: the fields go, seq stays.
	c.ZRem(ctx, "ws:"+s+":merging", "q1")
	pass()
	if rec, _ := c.HGetAll(ctx, reconcile.LandMergeKey(s)).Result(); len(rec) != 1 || rec["seq"] != "1" {
		t.Fatalf("after the episode: %v", rec)
	}
	// The next episode gets the next id through the real push: no CONFLICT.
	c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: 2, Member: "q2"})
	pass()
	if v, _ := c.HGet(ctx, reconcile.LandMergeKey(s), "task").Result(); v != "merge-quack-2" {
		t.Fatalf("second episode card %q", v)
	}
	if st, _ := c.HGet(ctx, "task:merge-quack-2", "state").Result(); st != "open" {
		t.Fatalf("second card state %q", st)
	}
	// A third pass with the card open cuts nothing more.
	pass()
	if v, _ := c.HGet(ctx, reconcile.LandMergeKey(s), "seq").Result(); v != "2" {
		t.Fatalf("seq after a steady pass %q", v)
	}
}
