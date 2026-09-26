//go:build functional

package ws_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// orLand moves a ready card through working to landed (the lander's move).
func orLand(t *testing.T, c *redis.Client, id string) {
	t.Helper()
	ctx := context.Background()
	if _, err := taskcard.Move(ctx, c, id, "working", taskcard.Opts{By: "test", As: "f1", Friend: "f1", SetFriend: true}); err != nil {
		t.Fatalf("%s to working: %v", id, err)
	}
	if _, err := taskcard.Land(ctx, c, id, "test", "0123abcd", "merged"); err != nil {
		t.Fatalf("land %s: %v", id, err)
	}
}

func createdAt(t *testing.T, c *redis.Client, id string) float64 {
	t.Helper()
	v, err := c.HGet(context.Background(), ws.RecordKey(id), "created_at").Result()
	if err != nil {
		t.Fatalf("created_at %s: %v", id, err)
	}
	n, _ := strconv.ParseFloat(v, 64)
	return n
}

// TestOrderBaseIsTheOldestLiveCard is the cold read's probe (#4322 fix round
// item 1): the sentinel is made 8.4 s older than every card (the probe's
// gap); after a and e land and a reorder, the least score in the stream's
// ordered sets (what PROGRESS oldest= reads) is the created_at of the
// oldest live card that is not the sentinel, never the sentinel's.
func TestOrderBaseIsTheOldestLiveCard(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	orFive(t, c)
	const stop = "land-order:sentinel"
	old := createdAt(t, c, "a") - 8400
	pipe := c.Pipeline()
	pipe.HSet(ctx, "task:"+stop, "created_at", strconv.FormatFloat(old, 'f', 0, 64))
	pipe.ZAdd(ctx, ws.Key(orStream, "waiting"), redis.Z{Score: old, Member: stop})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Reorder(ctx, c, orStream, "test"); err != nil {
		t.Fatal(err)
	}
	orLand(t, c, "a")
	orLand(t, c, "e")
	r, err := ws.Reorder(ctx, c, orStream, "test")
	if err != nil {
		t.Fatal(err)
	}
	want := createdAt(t, c, "b")
	for _, id := range []string{"c", "d"} {
		if cr := createdAt(t, c, id); cr < want {
			want = cr
		}
	}
	least := 0.0
	for _, w := range ws.OrderedSets {
		zs, _ := c.ZRangeWithScores(ctx, ws.Key(orStream, w), 0, 0).Result()
		if len(zs) > 0 && (least == 0 || zs[0].Score < least) {
			least = zs[0].Score
		}
	}
	if least != want || least == old {
		t.Fatalf("least score %.0f, want the oldest live card's created_at %.0f (the sentinel's is %.0f); order %v", least, want, old, r.Computed)
	}
	if so, _ := ws.ReadOrder(ctx, c, orStream); so.Stale() || so.Drift() {
		t.Fatalf("after the reorder: stale %v drift %v", so.Stale(), so.Drift())
	}
	t.Logf("oldest live card b..d created_at=%.0f least score=%.0f sentinel created_at=%.0f ranked=%d", want, least, old, r.Ranked)
}

// TestOrderSentinelOnlyAndMidMove: probes (4) and (5) of the #4322 fix
// round. A stream whose one live card is its sentinel orders to
// [sentinel] with no error; `ws reorder` with a card in working leaves the
// working set's score untouched (its created_at), writes the card's
// order_score, and reorders the rest (a hand score in ready is rescored).
func TestOrderSentinelOnlyAndMidMove(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()

	orPush(t, c, "solo", 1, "internal/solo", "")
	if _, err := taskcard.Cancel(ctx, c, "solo", "test", "gone"); err != nil {
		t.Fatal(err)
	}
	r, err := ws.Reorder(ctx, c, orStream, "test")
	if err != nil || len(r.Order) != 1 || r.Order[0].ID != "land-order:sentinel" || r.Ranked != 1 {
		t.Fatalf("sentinel only: %+v %v", r.Order, err)
	}

	orFive(t, c)
	if _, err := ws.Reorder(ctx, c, orStream, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, c, "a", "working", taskcard.Opts{By: "test", As: "f1", Friend: "f1", SetFriend: true}); err != nil {
		t.Fatal(err)
	}
	working, _ := c.ZScore(ctx, ws.Key(orStream, "working"), "a").Result()
	if err := c.ZAdd(ctx, ws.Key(orStream, "ready"), redis.Z{Score: 1.5, Member: "e"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := ws.Check(ctx, c, append(orIDs, "solo")); err == nil || !strings.Contains(err.Error(), "e scores 1.5 in ") {
		t.Fatalf("INVARIANTS on a hand score of 1.5: %v", err)
	}
	r, err = ws.Reorder(ctx, c, orStream, "test")
	if err != nil || r.Rescored != 1 || r.Ranked != 6 {
		t.Fatalf("mid-move reorder: %+v %v", r, err)
	}
	if got, _ := c.ZScore(ctx, ws.Key(orStream, "working"), "a").Result(); got != working || got != createdAt(t, c, "a") {
		t.Fatalf("a's working score %.0f moved (was %.0f)", got, working)
	}
	if got, _ := c.HGet(ctx, "task:a", "order_score").Result(); got != strconv.FormatFloat(r.Scores[0], 'f', 0, 64) {
		t.Fatalf("a's order_score %q, want rank 1's %.0f", got, r.Scores[0])
	}
	if so, _ := ws.ReadOrder(ctx, c, orStream); so.Stale() {
		t.Fatalf("after the mid-move reorder: stored %v", so.Stored)
	}
	if err := ws.Check(ctx, c, append(orIDs, "solo")); err != nil {
		t.Fatal(err)
	}
	t.Logf("working a=%.0f untouched; ranked=%d rescored=%d", working, r.Ranked, r.Rescored)
}

// TestReorderWriteIsConditional (#4322 fix round item 4): an order read
// before a push is refused ORDER STALE by ns_ws_reorder and writes nothing
// (keys and every ordered set unchanged); Reorder reads again and writes.
func TestReorderWriteIsConditional(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	orFive(t, c)
	so, err := ws.ReadOrder(ctx, c, orStream)
	if err != nil {
		t.Fatal(err)
	}
	orPush(t, c, "late", 99, "internal/late", "")
	before := snapshot(t, c)
	_, err = ws.WriteOrder(ctx, c, so, "test")
	if !ws.IsStale(err) || !strings.Contains(err.Error(), "the stream has 7 live cards, the order read 6") {
		t.Fatalf("stale write: %v", err)
	}
	if after := snapshot(t, c); after != before {
		t.Fatalf("a refused write wrote:\n%s\n%s", before, after)
	}
	r, err := ws.Reorder(ctx, c, orStream, "test")
	if err != nil || r.Ranked != 7 || r.Stale != 0 {
		t.Fatalf("reorder: %+v %v", r, err)
	}
}

// snapshot is the store as the refusal tests compare it: DBSIZE, ws:log's
// length and every sorted set with its scores (a throwaway store: SCAN is
// fine).
func snapshot(t *testing.T, c *redis.Client) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder
	fmt.Fprintf(&b, "keys=%d ws:log=%d\n", c.DBSize(ctx).Val(), c.XLen(ctx, "ws:log").Val())
	keys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range sortStrings(keys) {
		if c.Type(ctx, k).Val() != "zset" {
			continue
		}
		fmt.Fprintf(&b, "%s %v\n", k, c.ZRangeWithScores(ctx, k, 0, -1).Val())
	}
	return b.String()
}

func sortStrings(xs []string) []string {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
	return xs
}

// TestTwoPushesAtOnceRace50 is probe (3) of the #4322 fix round: 50 rounds
// of two pushes at once onto one stream, each push followed by its
// ws.Reorder (the push door); every card ends with a distinct score in the
// ordered sets, the stored order is the computed one (not stale, no drift)
// and ws.Check holds.
func TestTwoPushesAtOnceRace50(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	stale := 0
	var mu sync.Mutex
	var ids []string
	for round := 0; round < 50; round++ {
		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{}) // both pushes leave together
		for k := 0; k < 2; k++ {
			id := fmt.Sprintf("r%02d-%d", round, k)
			ids = append(ids, id)
			wg.Add(1)
			go func(k int, id string) {
				defer wg.Done()
				<-start
				r := taskcard.PushRequest{ID: id, Stream: orStream, Kind: "build", Title: id, By: "test",
					Ref: "mas-bandwidth/nova-tools#" + strconv.Itoa(1000+len(id)+k), Fields: []string{"paths", "internal/" + id}}
				if _, err := taskcard.Push(ctx, c, r); err != nil {
					errs[k] = err
					return
				}
				rr, err := ws.Reorder(ctx, c, orStream, "test")
				mu.Lock()
				stale += rr.Stale
				mu.Unlock()
				errs[k] = err
			}(k, id)
		}
		close(start)
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
	}
	so, err := ws.ReadOrder(ctx, c, orStream)
	if err != nil || so.Stale() || so.Drift() || len(so.Order) != 101 {
		t.Fatalf("after the race: stale %v drift %v cards %d err %v", so.Stale(), so.Drift(), len(so.Order), err)
	}
	seen := map[float64]string{}
	for _, w := range ws.OrderedSets {
		for _, z := range c.ZRangeWithScores(ctx, ws.Key(orStream, w), 0, -1).Val() {
			if other, dup := seen[z.Score]; dup {
				t.Fatalf("%v and %s share the score %.0f", z.Member, other, z.Score)
			}
			seen[z.Score] = fmt.Sprint(z.Member)
		}
	}
	if err := ws.Check(ctx, c, append(ids, "land-order:sentinel")); err != nil {
		t.Fatal(err)
	}
	t.Logf("100 cards + sentinel, %d distinct scores, %d ORDER STALE re-reads", len(seen), stale)
}
