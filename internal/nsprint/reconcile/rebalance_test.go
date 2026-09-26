package reconcile_test

import (
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
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

// TestFriendRebalanceNeverSilentZero: the Lua move on a down friend whose
// ready set is not empty never answers moved=0 without a reason. With no
// other friend and a task with no stream there is nowhere to put it, so the
// reply is REFUSED with the reason and the id; with a stream it goes back to
// the stream's ready set, unowned, why=no-consumer.
func TestFriendRebalanceNeverSilentZero(t *testing.T) {
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
