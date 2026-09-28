//go:build functional

package taskcard_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestDealPassHonoursWorkerPause (#4308): a worker paused through
// ns_worker_pause (worker pause) is dealt nothing by the deal pass, a
// bench's and a friend's alike, and keeps its working copies; resumed, the
// next pass fills it.
func TestDealPassHonoursWorkerPause(t *testing.T) {
	t.Parallel()

	c := start(t)
	ctx := context.Background()
	now := time.Now()
	at := strconv.FormatInt(now.UnixMilli(), 10)
	b, f := mustConsumer(t, "bench:b"), mustConsumer(t, "friend:f")
	for _, k := range []taskcard.Consumer{b, f} {
		if err := taskcard.Enroll(ctx, c, k, true); err != nil {
			t.Fatal(err)
		}
		c.HSet(ctx, k.BeatKey(), "at", at)
		c.HSet(ctx, k.DesiredKey(), "slots", "2")
	}
	c.SAdd(ctx, "benches", "b")
	c.SAdd(ctx, "friends", "f")
	pushPrimaries(t, c, 8)

	pause := func(k taskcard.Consumer, flag string) {
		t.Helper()
		got, err := c.FCall(ctx, "ns_worker_pause", nil, k.Kind, k.Name, flag, "rowan", "").StringSlice()
		if err != nil || len(got) < 2 || got[1] != k.String() {
			t.Fatalf("ns_worker_pause %s %s = %v %v", k, flag, got, err)
		}
	}
	pause(b, "1")
	r, err := taskcard.DealPass(ctx, c, "reconciler", now)
	if err != nil || r.Dealt != 2 {
		t.Fatalf("pass with the bench paused: %+v %v", r, err)
	}
	wantCells(t, cellsOf(t, c, b), "paused bench", 0, 0, 0, 0)
	wantCells(t, cellsOf(t, c, f), "friend", 2, 0, 0, 0)

	// the friend works its copies, then is paused: it keeps them
	if _, err := taskcard.Work(ctx, c, f, "f", 0, true); err != nil {
		t.Fatal(err)
	}
	pause(f, "1")
	pause(b, "0")
	r, err = taskcard.DealPass(ctx, c, "reconciler", now)
	if err != nil || r.Dealt != 2 {
		t.Fatalf("pass with the bench resumed and the friend paused: %+v %v", r, err)
	}
	wantCells(t, cellsOf(t, c, b), "resumed bench", 2, 0, 0, 0)
	wantCells(t, cellsOf(t, c, f), "paused friend keeps its work", 0, 2, 0, 0)
	cleanMoves(t, c, "pause")
}
