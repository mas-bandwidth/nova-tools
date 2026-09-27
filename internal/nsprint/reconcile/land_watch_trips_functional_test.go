//go:build functional

package reconcile_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestLandWatchBusyTickOneTrip (Stella's read of nova-tools #4449,
// 2026-09-27, her reproduction TestStellaBusyLandWatchOneTrip: "a stream
// with one merging member and an already-open merge card takes 3 trips on
// BOTH passes: snapshot, member/card pipeline, final brief/write
// pipeline"): the normal active tick is one round trip, on the first pass
// and on every pass after: the read, the stamps and the slow record are
// one call, and the brief is a view, so nothing is written every second.
func TestLandWatchBusyTickOneTrip(t *testing.T) {
	t.Parallel()
	mr := testutil.StartStore(t)
	c := mr.Client
	ctx := context.Background()
	const s = "active"
	now := time.UnixMilli(1700000000000)
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: s})
	c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: 10, Member: "t1"})
	c.HSet(ctx, "task:t1", "pr", "nova-tools#1", "merging_at", now.UnixMilli())
	c.HSet(ctx, reconcile.LandMergeKey(s), "task", "merge-active-1", "members", "t1", "seq", "1")
	c.HSet(ctx, "task:merge-active-1", "state", "open", "kind", "merge")
	w := &reconcile.LandWatch{Client: c, Now: func() time.Time { return now }, Repo: "mas-bandwidth/nova-tools"}
	trips := store.New(c).CountTrips()
	for pass := 0; pass < 3; pass++ {
		before := trips.N()
		if _, err := w.Run(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if got := trips.N() - before; got != 1 {
			t.Errorf("active land-watch pass %d took %d trips; the budget is 1", pass+1, got)
		}
		now = now.Add(time.Second)
	}
	if at, _ := c.HGet(ctx, reconcile.LandMergingKey(s), "t1").Result(); at != "1700000000000" {
		t.Fatalf("first sight stamped in the call: %q", at)
	}
	// A slow tick with a note due is the one call, the coordinator's name
	// (the friends' roles), the note (two XADDs in one pipeline) and the
	// noted mark: four trips once per member and word for its stay, then
	// one again. (The coordinator's name could ride the call; owed.)
	c.HSet(ctx, "cfg:land", "slow", "60", "wall", "120")
	now = now.Add(2 * time.Minute)
	before := trips.N()
	if _, err := w.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := trips.N() - before; got > 4 {
		t.Errorf("the noting pass took %d trips; at most 4 (the call, the coordinator, the note, the mark)", got)
	}
	before = trips.N()
	now = now.Add(time.Second)
	if _, err := w.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := trips.N() - before; got != 1 {
		t.Errorf("the pass after the note took %d trips; the budget is 1", got)
	}
}

// TestLandWatchNewStayGetsNewAlarm (Stella's retest of nova-tools #4449 at
// 6ddb3d8c0, 2026-09-27, her probe TestStellaLandWatchNewStayGetsNewAlarm;
// LandWatch.tla L3): a member that leaves merging and re-enters between
// two watch ticks is a new stay with a new deadline; its alarm reaches the
// coordinator again. The one task writer stamps merging_at on every entry
// (02_card_move.lua), and the watch reads a stamp newer than its first
// sight as the new stay: first sight and the noted words start over.
func TestLandWatchNewStayGetsNewAlarm(t *testing.T) {
	t.Parallel()
	mr := testutil.StartStore(t)
	c := mr.Client
	ctx := context.Background()
	const s = "reentry"
	start := time.UnixMilli(1700000000000)
	now := start
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: s})
	c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: 1, Member: "m1"})
	c.HSet(ctx, "task:m1", "merging_at", start.UnixMilli())
	c.HSet(ctx, reconcile.LandMergeKey(s), "task", "merge-reentry-1", "members", "m1", "seq", "1")
	c.HSet(ctx, "task:merge-reentry-1", "state", "open", "kind", "merge")
	var wakes []reconcile.LandWake
	w := &reconcile.LandWatch{Client: c, Repo: "mas-bandwidth/nova-tools", Now: func() time.Time { return now },
		Notify: func(_ context.Context, wake reconcile.LandWake) error { wakes = append(wakes, wake); return nil }}
	pass := func() {
		t.Helper()
		if _, err := w.Run(ctx, nil); err != nil {
			t.Fatal(err)
		}
	}
	pass()
	now = start.Add(11 * time.Minute)
	pass()
	if len(wakes) != 1 {
		t.Fatalf("first stay: wakes=%+v", wakes)
	}
	// The member leaves and re-enters between ticks; the move stamps the
	// new merging_at; the watch never sees an empty merging set.
	now = start.Add(12 * time.Minute)
	c.ZRem(ctx, "ws:"+s+":merging", "m1")
	c.HSet(ctx, "task:m1", "merging_at", now.UnixMilli())
	c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: 1, Member: "m1"})
	pass()
	if first, _ := c.HGet(ctx, reconcile.LandMergingKey(s), "m1").Result(); first != strconv.FormatInt(now.UnixMilli(), 10) {
		t.Fatalf("first sight after the re-entry = %q, want the pass's now", first)
	}
	if n, _ := c.HExists(ctx, reconcile.LandNotedKey(s), "m1").Result(); n {
		t.Fatal("the old stay's noted words survived the re-entry")
	}
	now = start.Add(23 * time.Minute)
	pass()
	if len(wakes) != 2 || wakes[1].Oldest != "m1" {
		t.Fatalf("the new stay got no new alarm: wakes=%+v", wakes)
	}
	// The L1 control: an older stamp on the same stay is not a new stay.
	c.HSet(ctx, "task:m1", "merging_at", start.Add(12*time.Minute).UnixMilli()-1)
	now = start.Add(24 * time.Minute)
	pass()
	if len(wakes) != 2 {
		t.Fatalf("an older stamp re-noted the same stay: wakes=%+v", wakes)
	}
}
