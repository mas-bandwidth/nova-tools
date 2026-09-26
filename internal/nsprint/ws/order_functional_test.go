//go:build functional

package ws_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

const orStream = "land: order"

// orPush pushes one task onto orStream with its issue, paths and DEPENDS-ON.
func orPush(t *testing.T, c *redis.Client, id string, issue int, paths, dependsOn string) {
	t.Helper()
	r := taskcard.PushRequest{ID: id, Stream: orStream, Kind: "build", Title: id, By: "test",
		Ref: "mas-bandwidth/nova-tools#" + strconv.Itoa(issue), DependsOn: dependsOn, Fields: []string{"paths", paths}}
	if _, err := taskcard.Push(context.Background(), c, r); err != nil {
		t.Fatalf("push %s: %v", id, err)
	}
}

// orFive is the class fixture on a store: the diamond a <- {b, c} <- d and
// e sharing d's path.
func orFive(t *testing.T, c *redis.Client) {
	orPush(t, c, "a", 50, "internal/a", "")
	orPush(t, c, "b", 20, "internal/b", "a")
	orPush(t, c, "c", 30, "internal/c", "a")
	orPush(t, c, "d", 40, "internal/x", "b,c")
	orPush(t, c, "e", 45, "internal/x/e.go", "")
}

var orIDs = []string{"a", "b", "c", "d", "e", "land-order:sentinel"}

// TestReorderWritesTheOrderAndMovesCarryIt: five cards pushed age-ordered
// read another sequence than their order (the sentinel, oldest, first);
// Reorder writes the order in one FCALL, after which the stored sequence is
// the computed one and the invariants hold; a move into ready and merging
// carries the card's order_score; a hand ZADD is ORDER DRIFT with both
// sequences; a cycle is refused by name and writes nothing.
func TestReorderWritesTheOrderAndMovesCarryIt(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	orFive(t, c)

	so, err := ws.ReadOrder(ctx, c, orStream)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(so.Computed, ","); got != "a,b,c,d,e,land-order:sentinel" {
		t.Fatalf("computed %s", got)
	}
	// Pushed by the library (no door): no card has an order score yet, so
	// the stored order is stale whether or not the age order happens to read
	// the same sequence (pushes inside one millisecond tie on created_at).
	if !so.Stale() {
		t.Fatalf("unranked cards %v read as a written order", so.Stored)
	}

	r, err := ws.Reorder(ctx, c, orStream, "test")
	if err != nil {
		t.Fatal(err)
	}
	if r.Ranked != 6 || r.Skipped != 0 || r.RoundTrips != 3 {
		t.Fatalf("reorder %+v", r)
	}
	so, _ = ws.ReadOrder(ctx, c, orStream)
	if so.Drift() || strings.Join(so.Stored, ",") != "a,b,c,d,e,land-order:sentinel" {
		t.Fatalf("after reorder: stored %v computed %v", so.Stored, so.Computed)
	}
	if err := ws.Check(ctx, c, orIDs); err != nil {
		t.Fatalf("invariants after reorder: %v", err)
	}

	// b to ready then working and merging: it carries its score
	// into ready and merging, and the order holds.
	for _, to := range []string{"ready", "working", "merging"} {
		o := taskcard.Opts{By: "test", Why: "to " + to}
		if to == "working" {
			o.As, o.Friend, o.SetFriend = "f1", "f1", true
		}
		if _, err := taskcard.Move(ctx, c, "b", to, o); err != nil {
			t.Fatalf("b to %s: %v", to, err)
		}
		so, _ = ws.ReadOrder(ctx, c, orStream)
		if so.Drift() {
			t.Fatalf("b in %s: stored %v computed %v", to, so.Stored, so.Computed)
		}
		if err := ws.Check(ctx, c, orIDs); err != nil {
			t.Fatalf("b in %s: %v", to, err)
		}
		// the one writer's fsck reads the order scores as no drift
		if f, err := taskcard.Fsck(ctx, c, ""); err != nil || f.Drift != 0 {
			t.Fatalf("b in %s: task fsck %+v %v", to, f, err)
		}
	}
	bScore, _ := c.ZScore(ctx, ws.Key(orStream, "merging"), "b").Result()
	if want := so.Scores[1]; bScore != want {
		t.Fatalf("b scores %.0f in merging, want its order_score %.0f", bScore, want)
	}

	// A hand ZADD puts e before a: ORDER DRIFT, both sequences named.
	if err := c.ZAdd(ctx, ws.Key(orStream, "ready"), redis.Z{Score: 1, Member: "e"}).Err(); err != nil {
		t.Fatal(err)
	}
	so, _ = ws.ReadOrder(ctx, c, orStream)
	if !so.Drift() || strings.Join(so.Stored, ",") != "e,a,b,c,d,land-order:sentinel" ||
		strings.Join(so.Computed, ",") != "a,b,c,d,e,land-order:sentinel" {
		t.Fatalf("hand zadd: drift %v stored %v computed %v", so.Drift(), so.Stored, so.Computed)
	}
	if f, err := taskcard.Fsck(ctx, c, ""); err != nil || f.Drift != 1 || !strings.Contains(strings.Join(f.Lines, "\n"), "score ws:"+orStream+":ready e") {
		t.Fatalf("task fsck after the hand zadd: %+v %v", f, err)
	}
	if _, err := ws.Reorder(ctx, c, orStream, "test"); err != nil {
		t.Fatal(err)
	}
	if so, _ = ws.ReadOrder(ctx, c, orStream); so.Drift() {
		t.Fatalf("reorder did not repair: %v", so.Stored)
	}

	// A cycle: f waits on g, g on f. Refused by name; nothing written.
	orPush(t, c, "f", 60, "internal/f", "g")
	orPush(t, c, "g", 61, "internal/g", "f")
	before, _ := c.ZRangeWithScores(ctx, ws.Key(orStream, "waiting"), 0, -1).Result()
	_, err = ws.Reorder(ctx, c, orStream, "test")
	var ce *ws.CycleError
	if !errors.As(err, &ce) || err.Error() != "DEPENDS-ON cycle f -> g -> f" {
		t.Fatalf("cycle: %v", err)
	}
	after, _ := c.ZRangeWithScores(ctx, ws.Key(orStream, "waiting"), 0, -1).Result()
	if len(before) != len(after) {
		t.Fatalf("a refused reorder wrote: %v -> %v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("a refused reorder wrote: %v -> %v", before, after)
		}
	}
}
