//go:build functional

package taskcard_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestDealPassFillsEveryConsumerInOnePass: the deal duty's pass deals and
// fills every live enrolled consumer, benches and friends alike, to its
// slots in one pass with no per-pass cap; a down or unenrolled consumer
// gets nothing; WHO decides who may take a card; the next pass after an
// end refills the freed slot.
func TestDealPassFillsEveryConsumerInOnePass(t *testing.T) {
	t.Parallel()

	c := start(t)
	ctx := context.Background()
	now := time.Now()
	at := strconv.FormatInt(now.UnixMilli(), 10)
	b, f, g, h := mustConsumer(t, "bench:b"), mustConsumer(t, "friend:f"), mustConsumer(t, "friend:g"), mustConsumer(t, "bench:h")
	for _, k := range []taskcard.Consumer{b, f, g} {
		if err := taskcard.Enroll(ctx, c, k, true); err != nil {
			t.Fatal(err)
		}
		c.HSet(ctx, k.BeatKey(), "at", at)
	}
	c.HSet(ctx, h.BeatKey(), "at", at) // live, but not enrolled
	c.HSet(ctx, h.DesiredKey(), "slots", "5")
	c.HSet(ctx, b.DesiredKey(), "slots", "40")
	c.HSet(ctx, f.DesiredKey(), "slots", "2")
	c.HSet(ctx, g.DesiredKey(), "slots", "2")
	c.Set(ctx, g.DownKey(), "out of credits", 0)
	ids := pushPrimaries(t, c, 50)
	c.HSet(ctx, taskcard.Key(ids[0]), "who", "only f") // the oldest, but only f may take it

	calls := countCalls(c)
	r, err := taskcard.DealPass(ctx, c, "reconciler", now)
	if err != nil {
		t.Fatal(err)
	}
	// expire + the review reads (#4094) + (deal + work) per consumer
	// with room: 1 + 1 + 2 + 2
	if n := countCalls(c) - calls; n != 4 {
		t.Fatalf("pass took %d calls, want 4: %v", n, r.Lines)
	}
	if r.Dealt != 42 {
		t.Fatalf("pass %+v", r)
	}
	// The pass deals only; each consumer takes its own copies (a bench's beat
	// session, a friend's serve), here by hand.
	for _, k := range []taskcard.Consumer{b, f} {
		if _, err := taskcard.Work(ctx, c, k, k.Name, 0, true); err != nil {
			t.Fatal(err)
		}
	}
	wantCells(t, cellsOf(t, c, b), "bench", 0, 40, 0, 0)
	wantCells(t, cellsOf(t, c, f), "friend", 0, 2, 0, 0)
	wantCells(t, cellsOf(t, c, g), "down", 0, 0, 0, 0)
	wantCells(t, cellsOf(t, c, h), "unenrolled", 0, 0, 0, 0)
	if cp := c.HGet(ctx, taskcard.Key(ids[0]), "copy").Val(); cp == "" || c.HGet(ctx, taskcard.Key(cp), "consumer").Val() != "friend:f" {
		t.Fatalf("WHO only f: copy %q", cp)
	}
	cleanMoves(t, c, "pass")

	// a full consumer is not dealt; an end frees a slot the next pass fills
	w, _ := c.ZRange(ctx, b.KeyAt(0, "working"), 0, 0).Result()
	if _, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: w, Why: "red", By: "b"}); err != nil {
		t.Fatal(err)
	}
	r, err = taskcard.DealPass(ctx, c, "reconciler", now)
	if err != nil || r.Dealt != 1 {
		t.Fatalf("refill %+v %v", r, err)
	}
	wantCells(t, cellsOf(t, c, b), "refill", 1, 39, 0, 1)
	cleanMoves(t, c, "refill")

	// a stale beat is not dealt
	c.HSet(ctx, f.BeatKey(), "at", strconv.FormatInt(now.Add(-time.Hour).UnixMilli(), 10))
	fw, _ := c.ZRange(ctx, f.KeyAt(0, "working"), 0, -1).Result()
	if _, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: fw, OK: true, By: "f"}); err != nil {
		t.Fatal(err)
	}
	if r, err = taskcard.DealPass(ctx, c, "reconciler", now); err != nil || r.Dealt != 0 {
		t.Fatalf("stale friend dealt: %+v %v", r, err)
	}
}
