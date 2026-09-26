//go:build functional

package reconcile_test

import (
	"bytes"
	"context"
	"errors"
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

const wrStream = "nova-sprint + merge + bus"

// wrTask writes one task in the ws shape: its hash and the one set its state
// names, scored by created_at (ms after cardEpoch).
func wrTask(t *testing.T, c *redis.Client, id, state string, created int64, kv ...string) {
	t.Helper()
	created += cardEpoch
	ctx := context.Background()
	fields := []any{"stream", wrStream, "state", state, "created_at", created}
	for _, f := range kv {
		fields = append(fields, f)
	}
	pipe := c.Pipeline()
	pipe.SAdd(ctx, "ws:names", wrStream)
	pipe.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: wrStream})
	pipe.HSet(ctx, "task:"+id, fields...)
	if state != "closed" {
		pipe.ZAdd(ctx, ws.Key(wrStream, state), redis.Z{Score: float64(created), Member: id})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func wrMembers(t *testing.T, c *redis.Client, state string) map[string]float64 {
	t.Helper()
	zs, err := c.ZRangeWithScores(context.Background(), ws.Key(wrStream, state), 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, z := range zs {
		out[z.Member.(string)] = z.Score
	}
	return out
}

// TestWaitingResolvesWhenDepsLand is nova-tools #3872's DONE-WHEN: with card
// A landed, B waiting on task:A and C waiting on task:A and task:D (D still
// working), one reconciler pass moves B to ws:<s>:ready and leaves C waiting
// with the receipt naming D; a card waiting on an id with no record stays
// waiting and is named in unknown=. The repo#n form is met by the landed
// task whose pr names it, and not by one still merging.
func TestWaitingResolvesWhenDepsLand(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()
	wrTask(t, c, "A", "landed", 1000)
	wrTask(t, c, "D", "working", 1001)
	wrTask(t, c, "G", "landed", 1002, "pr", "77", "repo", "mas-bandwidth/nova-tools")
	wrTask(t, c, "I", "merging", 1003, "ref", "mas-bandwidth/nova-tools#78")
	wrTask(t, c, "B", "waiting", 2000, "blocked_on", "task:A")
	wrTask(t, c, "C", "waiting", 2001, "blocked_on", "task:A,task:D")
	wrTask(t, c, "E", "waiting", 2002, "blocked_on", "task:ghost")
	wrTask(t, c, "F", "waiting", 2003, "blocked_on", "nova-tools#77")
	wrTask(t, c, "H", "waiting", 2004, "blocked_on", "mas-bandwidth/nova-tools#78")
	wrTask(t, c, "J", "waiting", 2005, "blocked_on", "o/r#99")
	wrTask(t, c, "K", "waiting", 2006) // no blocked_on: no evidence, stays

	st := store.New(c)
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	duty := &reconcile.WaitingResolve{Client: c, Out: &out}
	counts, err := duty.Run(ctx, lease)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if counts.Routed != 2 {
		t.Fatalf("routed %d, want 2 (B and F)", counts.Routed)
	}

	ready, waiting := wrMembers(t, c, "ready"), wrMembers(t, c, "waiting")
	if len(ready) != 2 || ready["B"] != float64(cardEpoch+2000) || ready["F"] != float64(cardEpoch+2003) {
		t.Fatalf("ready %v, want B and F at their created_at scores", ready)
	}
	for _, id := range []string{"C", "E", "H", "J", "K"} {
		if _, ok := waiting[id]; !ok {
			t.Errorf("%s left waiting: waiting is %v", id, waiting)
		}
	}
	if s, _ := c.HGet(ctx, "task:B", "where").Result(); s != "ready" {
		t.Fatalf("task:B where %q, want ready (the pointer, #3778)", s)
	}
	want := `RESOLVE stream="nova-sprint + merge + bus" ready=2 still=5 on=mas-bandwidth/nova-tools#78,task:D unknown=o/r#99,task:ghost` + "\n"
	if out.String() != want {
		t.Fatalf("receipt\n%q\nwant\n%q", out.String(), want)
	}

	// The ws:log entry names the dependency that released the card.
	entries, err := c.XRange(ctx, "ws:log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	whys := map[string]string{}
	for _, e := range entries {
		if e.Values["to"] == "ready" {
			whys[e.Values["id"].(string)] = e.Values["why"].(string)
		}
	}
	if whys["B"] != "depends-on met: task:A" || whys["F"] != "depends-on met: nova-tools#77" || len(whys) != 2 {
		t.Fatalf("ws:log whys %v", whys)
	}
	if err := ws.Check(ctx, c, []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K"}); err != nil {
		t.Fatalf("invariant: %v", err)
	}

	// A second pass moves nothing and prints nothing (the line is unchanged).
	out.Reset()
	counts, err = duty.Run(ctx, lease)
	if err != nil || counts.Routed != 0 || out.Len() != 0 {
		t.Fatalf("second pass: routed %d err %v out %q", counts.Routed, err, out.String())
	}

	// D lands: C is released on the next pass. landed needs the merge sha
	// (#3778): the one move with the lander's sha, working -> landed (a
	// primary with no copy never enters merging by hand, #3929)
	if _, err := taskcard.Land(ctx, c, "D", "test", "0123abcd", "merged"); err != nil {
		t.Fatal(err)
	}
	counts, err = duty.Run(ctx, lease)
	if err != nil || counts.Routed != 1 {
		t.Fatalf("after D landed: routed %d err %v", counts.Routed, err)
	}
	if _, ok := wrMembers(t, c, "ready")["C"]; !ok {
		t.Fatalf("C not ready after D landed")
	}
}

// TestWaitingResolveStopsAtLeaseMargin: with less than the write margin of
// the lease left, the duty moves nothing and says why; after a renewal the
// same pass moves the card.
func TestWaitingResolveStopsAtLeaseMargin(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()
	wrTask(t, c, "A", "landed", 1000)
	wrTask(t, c, "B", "waiting", 2000, "blocked_on", "task:A")

	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	lease, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "test", TTL: 6 * time.Second, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(5500 * time.Millisecond)
	duty := &reconcile.WaitingResolve{Client: c}
	counts, err := duty.Run(ctx, lease)
	if err == nil || !strings.Contains(err.Error(), "LEASE-MARGIN") || counts.Routed != 0 {
		t.Fatalf("at 500ms left: routed %d err %v; want no move and LEASE-MARGIN", counts.Routed, err)
	}
	if _, ok := wrMembers(t, c, "waiting")["B"]; !ok {
		t.Fatal("B moved with the lease inside the write margin")
	}
	if err := lease.Renew(ctx); err != nil {
		t.Fatal(err)
	}
	if counts, err = duty.Run(ctx, lease); err != nil || counts.Routed != 1 {
		t.Fatalf("after renew: routed %d err %v", counts.Routed, err)
	}

	// A fenced lease stops the duty with ErrFenced and moves nothing.
	wrTask(t, c, "B2", "waiting", 2001, "blocked_on", "task:A")
	lease.Fence(reconcile.ErrFenced)
	if _, err := duty.Run(ctx, lease); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("fenced: err %v, want ErrFenced", err)
	}
	if _, ok := wrMembers(t, c, "waiting")["B2"]; !ok {
		t.Fatal("B2 moved under a fenced lease")
	}
}

// TestWaitingResolveCountsRefusedMoves (the cold read of #4361, item 3): a
// move ns_ws_move_many refuses reaches Counts.Refused once, so the DUTY
// line and proc:progress carry it, and the duty's error names it.
func TestWaitingResolveCountsRefusedMoves(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()
	wrTask(t, c, "A", "landed", 1000)
	wrTask(t, c, "B", "waiting", 2000, "blocked_on", "task:A")
	// R is met but its record names no stream: the one move refuses it.
	if err := c.HSet(ctx, "task:R", "blocked_on", "task:A").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.ZAdd(ctx, ws.Key(wrStream, "waiting"), redis.Z{Score: float64(cardEpoch + 2001), Member: "R"}).Err(); err != nil {
		t.Fatal(err)
	}
	lease, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	counts, err := (&reconcile.WaitingResolve{Client: c}).Run(ctx, lease)
	if counts.Routed != 1 || counts.Refused != 1 {
		t.Fatalf("routed %d refused %d, want 1 and 1", counts.Routed, counts.Refused)
	}
	if err == nil || !strings.Contains(err.Error(), "R refused: task R has no stream") {
		t.Fatalf("err %v, want the refusal named", err)
	}
}

// TestSentinelEdgeNeverReachesReadyUnmet is #4318's class test: a card whose
// DEPENDS-ON names another stream's sentinel (bare or task:<id>) stays
// waiting, the receipt naming the sentinel, until that sentinel is landed;
// the sentinel itself is never the duty's to move (it is not counted), and
// task land refuses it while a card of its stream is live.
func TestSentinelEdgeNeverReachesReadyUnmet(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	const a, b = "a: one", "b: two"
	// Stream a: one working card and its sentinel waiting, written in the ws
	// shape; stream b: two cards waiting on a's sentinel in both spellings.
	sid := ws.SentinelID(a)
	for _, s := range []string{a, b} {
		must(t, c.SAdd(ctx, "ws:names", s).Err())
	}
	must(t, c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: a}, redis.Z{Score: 2, Member: b}).Err())
	put := func(id, stream, state string, created int64, kv ...string) {
		fields := []any{"stream", stream, "state", state, "created_at", cardEpoch + created}
		for _, f := range kv {
			fields = append(fields, f)
		}
		must(t, c.HSet(ctx, "task:"+id, fields...).Err())
		must(t, c.ZAdd(ctx, ws.Key(stream, state), redis.Z{Score: float64(cardEpoch + created), Member: id}).Err())
	}
	put("A1", a, "working", 1000)
	put(sid, a, "waiting", 999, "kind", "sentinel")
	put("B1", b, "waiting", 2000, "blocked_on", sid)
	put("B2", b, "waiting", 2001, "blocked_on", "task:"+sid)

	st := store.New(c)
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	duty := &reconcile.WaitingResolve{Client: c, Out: &out}
	if counts, err := duty.Run(ctx, lease); err != nil || counts.Routed != 0 {
		t.Fatalf("routed %d err %v; nothing is landed", counts.Routed, err)
	}
	want := `RESOLVE stream="b: two" ready=0 still=2 on=` + sid + `,task:` + sid + ` unknown=-` + "\n"
	if out.String() != want {
		t.Fatalf("receipt\n%q\nwant\n%q (stream a has only its sentinel waiting: no line)", out.String(), want)
	}
	if n, _ := c.ZCard(ctx, ws.Key(b, "ready")).Result(); n != 0 {
		t.Fatalf("ready %d, want 0: an unmet sentinel edge never reaches ready", n)
	}

	// The sentinel cannot land around A1; A1 lands, then it does.
	if _, err := taskcard.Land(ctx, c, sid, "test", "0123abcd", ""); err == nil || !strings.Contains(err.Error(), "SENTINEL task:"+sid+" lands after the 1 live card(s) of stream a: one (first A1 working)") {
		t.Fatalf("sentinel land with A1 live: %v", err)
	}
	if _, err := taskcard.Land(ctx, c, "A1", "test", "0123abcd", "merged"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Land(ctx, c, sid, "test", "0123abcd", ""); err != nil {
		t.Fatalf("sentinel land: %v", err)
	}
	out.Reset()
	if counts, err := duty.Run(ctx, lease); err != nil || counts.Routed != 2 {
		t.Fatalf("after the sentinel landed: routed %d err %v", counts.Routed, err)
	}
	ready, _ := c.ZRange(ctx, ws.Key(b, "ready"), 0, -1).Result()
	if strings.Join(ready, " ") != "B1 B2" {
		t.Fatalf("ready %v", ready)
	}
	if out.String() != `RESOLVE stream="b: two" ready=2 still=0 on=- unknown=-`+"\n" {
		t.Fatalf("receipt %q", out.String())
	}
	if err := ws.Check(ctx, c, []string{"A1", sid, "B1", "B2"}); err != nil {
		t.Fatalf("invariant: %v", err)
	}
}
