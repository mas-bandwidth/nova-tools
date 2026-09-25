package reconcile_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// A friend status change rebalances that friend's ready tasks in the same
// reconciler tick (nova-tools #4145). Found 2026-09-25: after `friend down
// emma` her 15 ready tasks sat on friend:emma:cards:ready until a hand move.

// queued writes one ready task on friend's queue in stream: the record points
// to ready with friend and owner set, and both views hold it.
func (f *dealFixture) queued(t *testing.T, id, stream, friend string, age int64, extra ...any) {
	t.Helper()
	age += cardEpoch
	pipe := f.c.TxPipeline()
	pipe.SAdd(f.ctx, "ws:names", stream)
	pipe.HSet(f.ctx, "task:"+id, append([]any{"stream", stream, "where", "ready", "where_ok", "-", "state", "open",
		"friend", friend, "owner", friend, "created_at", age, "kind", "build"}, extra...)...)
	pipe.ZAdd(f.ctx, "ws:"+stream+":ready", redis.Z{Score: float64(age), Member: id})
	pipe.ZAdd(f.ctx, "friend:"+friend+":cards:ready", redis.Z{Score: float64(age), Member: id})
	if _, err := pipe.Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
}

// TestFriendDownRebalancesInOneTick is #4145's DONE-WHEN: two friends with
// slots, a ready task on emma's queue; a pass records both up, emma goes
// down, and the next pass moves her task onto rowan's queue and prints one
// REBALANCE line naming it. A third pass prints nothing.
func TestFriendDownRebalancesInOneTick(t *testing.T) {
	t.Parallel()

	f := newDealFixture(t)
	const s = "nova-sprint"
	must(t, f.c.ZAdd(f.ctx, "ws:order", redis.Z{Score: 1, Member: s}).Err())
	f.friend(t, "emma", 2, true, "pre-emma-1", "pre-emma-2")
	f.friend(t, "rowan", 2, true)
	f.queued(t, "build-x", s, "emma", 10)

	var out bytes.Buffer
	duty := &reconcile.FriendDeal{Client: f.c, Out: &out}
	lp := &reconcile.Loop{Lease: f.l, Duties: []reconcile.Duty{duty.Run}}
	mustPass(t, lp)
	if got := f.members(t, "friend:emma:cards:ready"); strings.Join(got, ",") != "build-x" {
		t.Fatalf("emma up: ready %v, want build-x kept on her queue", got)
	}
	if strings.Contains(out.String(), "REBALANCE") {
		t.Fatalf("first pass with every friend up printed %q, want no REBALANCE", out.String())
	}

	must(t, f.c.HSet(f.ctx, "friend:emma:down", "reason", "out of credits", "actor", "rowan").Err())
	out.Reset()
	mustPass(t, lp)
	if got := f.members(t, "friend:emma:cards:ready"); len(got) != 0 {
		t.Fatalf("emma down: ready %v after one pass, want empty", got)
	}
	if got := f.members(t, "friend:rowan:cards:ready"); strings.Join(got, ",") != "build-x" {
		t.Fatalf("rowan ready %v, want build-x moved onto his queue in the same pass; receipts:\n%s", got, out.String())
	}
	if h, _ := f.c.HMGet(f.ctx, "task:build-x", "where", "owner").Result(); h[0] != "ready" || h[1] != "rowan" {
		t.Fatalf("build-x where/owner %v, want ready/rowan", h)
	}
	want := "REBALANCE friend=emma from=up to=down moved=1 build-x:rowan\n"
	if n := strings.Count(out.String(), "REBALANCE"); n != 1 || !strings.Contains(out.String(), want) {
		t.Fatalf("receipts %q, want the one line %q", out.String(), want)
	}

	out.Reset()
	mustPass(t, lp)
	if out.Len() != 0 {
		t.Fatalf("third pass printed %q, want nothing", out.String())
	}
}

// TestFriendRebalanceNeverSilentZero: the Lua move on a down friend whose
// ready set is not empty never answers moved=0 without a reason. With no
// other friend and a task with no stream there is nowhere to put it, so the
// reply is REFUSED with the reason and the id; with a stream it goes back to
// the stream's ready set, unowned, why=no-consumer.
func TestFriendRebalanceNeverSilentZero(t *testing.T) {
	t.Parallel()

	f := newDealFixture(t)
	f.friend(t, "emma", 2, false)
	must(t, f.c.HSet(f.ctx, "friend:emma:down", "reason", "down", "actor", "rowan").Err())
	// stream-less: the record names no stream, so ready means emma's queue only
	must(t, f.c.HSet(f.ctx, "task:lost", "where", "ready", "where_ok", "-", "state", "open",
		"friend", "emma", "owner", "emma", "created_at", cardEpoch+5, "kind", "build").Err())
	must(t, f.c.ZAdd(f.ctx, "friend:emma:cards:ready", redis.Z{Score: float64(cardEpoch + 5), Member: "lost"}).Err())

	reply, err := f.c.FCall(f.ctx, "ns_friend_rebalance", nil, f.l.Token(), "emma", "rowan", "0").Slice()
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(reply...)
	if len(reply) < 2 || fmt.Sprint(reply[0]) != "REFUSED" || strings.TrimSpace(fmt.Sprint(reply[1])) == "" ||
		!strings.Contains(got, "lost") {
		t.Fatalf("reply %v, want REFUSED with a reason naming lost", reply)
	}

	f.queued(t, "streamed", "nova-sprint", "emma", 6)
	reply, err = f.c.FCall(f.ctx, "ns_friend_rebalance", nil, f.l.Token(), "emma", "rowan", "0").Slice()
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(reply[0]) != "REBALANCED" || fmt.Sprint(reply[1]) != "1" {
		t.Fatalf("reply %v, want REBALANCED 1 (streamed back to the stream's ready set)", reply)
	}
	if h, _ := f.c.HMGet(f.ctx, "task:streamed", "where", "owner", "why").Result(); h[0] != "ready" || h[1] != "" || h[2] != reconcile.NoConsumer {
		t.Fatalf("streamed where/owner/why %v, want ready, unowned, %s", h, reconcile.NoConsumer)
	}
	if sc, err := f.c.ZScore(f.ctx, "ws:nova-sprint:ready", "streamed").Result(); err != nil || sc != float64(cardEpoch+6) {
		t.Fatalf("streamed in ws ready: %v %v", sc, err)
	}
	if n := f.zcard(t, "friend:emma:cards:ready"); n != 1 {
		t.Fatalf("emma ready %d, want only the refused lost", n)
	}
}

// TestRedistributeFromMovesDownFriendsQueue: the hand verb's function,
// ns_redistribute_from, on a down friend moves every ready task on its
// queue (the 2026-09-25 evidence: state=- moved=0 with 15 on the queue).
func TestRedistributeFromMovesDownFriendsQueue(t *testing.T) {
	t.Parallel()

	f := newDealFixture(t)
	const s = "nova-sprint"
	f.friend(t, "emma", 2, false)
	f.friend(t, "rowan", 3, true)
	must(t, f.c.HSet(f.ctx, "friend:rowan:roles", "roles", "builder,coordinator,may-hold").Err())
	must(t, f.c.HSet(f.ctx, "friend:rowan:beat", "at", "1").Err())
	must(t, f.c.HSet(f.ctx, "friend:rowan:desired", "slots", "3").Err())
	must(t, f.c.HSet(f.ctx, "friend:emma:down", "reason", "out of credits", "actor", "rowan").Err())
	f.queued(t, "q1", s, "emma", 1)
	f.queued(t, "q2", s, "emma", 2)

	reply, err := f.c.FCall(f.ctx, "ns_redistribute_from", nil, "emma", "down", "", "", "rowan", "i1").Slice()
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(reply[0]) != "OK" {
		t.Fatalf("reply %v, want OK", reply)
	}
	sum := reply[1].([]any)
	if fmt.Sprint(sum[2]) != "2" {
		t.Fatalf("summary %v, want moved=2", sum)
	}
	if got := f.members(t, "friend:rowan:cards:ready"); strings.Join(got, ",") != "q1,q2" {
		t.Fatalf("rowan ready %v, want q1,q2", got)
	}
}

// TestFriendStaleBeatRebalances: a beat older than FriendLive is down too;
// the pass that sees it go stale moves the friend's ready task.
func TestFriendStaleBeatRebalances(t *testing.T) {
	t.Parallel()

	f := newDealFixture(t)
	const s = "nova-sprint"
	f.friend(t, "emma", 1, true, "pre-emma")
	f.friend(t, "rowan", 2, true)
	f.queued(t, "build-y", s, "emma", 10)
	var out bytes.Buffer
	duty := &reconcile.FriendDeal{Client: f.c, Out: &out}
	lp := &reconcile.Loop{Lease: f.l, Duties: []reconcile.Duty{duty.Run}}
	mustPass(t, lp)
	old := time.Now().Add(-2 * reconcile.FriendLive).UTC().Format(time.RFC3339)
	must(t, f.c.HSet(f.ctx, "friend:emma", "at", old).Err())
	out.Reset()
	mustPass(t, lp)
	if got := f.members(t, "friend:rowan:cards:ready"); strings.Join(got, ",") != "build-y" {
		t.Fatalf("rowan ready %v, want build-y; receipts:\n%s", got, out.String())
	}
	if want := "REBALANCE friend=emma from=up to=down moved=1 build-y:rowan\n"; !strings.Contains(out.String(), want) {
		t.Fatalf("receipts %q, want %q", out.String(), want)
	}
}
