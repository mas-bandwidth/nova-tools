package taskcard_test

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestCardCancelEach (#4309): the batch form refuses the whole list when
// one id is bad and writes nothing; --each cancels every id on its own,
// names the refused id with its why, and the rest move (a copy given back
// to waiting, a primary to done); fsck is clean after.
func TestCardCancelEach(t *testing.T) {
	t.Parallel()

	c := start(t)
	ctx := context.Background()
	k := mustConsumer(t, "friend:f")
	c.SAdd(ctx, "friends", "f")
	c.HSet(ctx, k.DesiredKey(), "slots", "3")
	ids := pushPrimaries(t, c, 3)
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 3, By: "rowan"})
	if err != nil || len(d) != 3 {
		t.Fatal(d, err)
	}
	if _, err := taskcard.Work(ctx, c, k, "f", 0, true); err != nil {
		t.Fatal(err)
	}
	batch := []string{d[0].Copy, "nope", ids[1]}
	if _, err := taskcard.CancelCards(ctx, c, "rowan", "moved", batch...); !isRefusedWith(err, "NOTASK") {
		t.Fatalf("batch with a bad id: %v", err)
	}
	if wsCount(c, "working") != 3 {
		t.Fatal("a refused batch wrote")
	}
	if _, err := taskcard.CancelEach(ctx, c, "rowan", "", batch...); !isRefusedWith(err, "WHY") {
		t.Fatalf("each with no why: %v", err)
	}
	r, err := taskcard.CancelEach(ctx, c, "rowan", "moved", batch...)
	if err != nil || len(r) != 3 {
		t.Fatalf("each: %v %v", r, err)
	}
	if r[0] != (taskcard.Cancelled{ID: d[0].Copy, To: "waiting"}) || r[2] != (taskcard.Cancelled{ID: ids[1], To: "done"}) {
		t.Fatalf("cancelled receipts %+v", r)
	}
	if r[1].ID != "nope" || r[1].To != "" || r[1].Why != "NOTASK task:nope" {
		t.Fatalf("refused receipt %+v", r[1])
	}
	if h := c.HGetAll(ctx, taskcard.Key(ids[0])).Val(); h["where"] != "waiting" || h["attempts"] != "0" {
		t.Fatalf("given back %v", h)
	}
	if h := c.HGetAll(ctx, taskcard.Key(d[1].Copy)).Val(); h["where"] != "fail" {
		t.Fatalf("cancelled primary's copy %v", h)
	}
	if wsCount(c, "working") != 1 || wsCount(c, "waiting") != 1 || wsCount(c, "done") != 1 {
		t.Fatalf("ws sets after each: %s", wsSnapshot(t, c))
	}
	wantCells(t, cellsOf(t, c, k), "each", 0, 1, 0, 2)
	cleanMoves(t, c, "each")
}
