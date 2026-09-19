package redisq_test

// The red tests for slice 1 of docs/SPEC-STATE.md (Part 2, "The work queue", "Slot
// leases", "In-flight caps"): the Redis Streams pull queue with one consumer group per
// stream, the fenced lease whose renew/release are a Lua compare-and-release, and the
// atomic cap admission script. A fake Redis (miniredis) stands in for the instance; no
// test reaches the network, and every path is a flag or a temporary directory.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/redisq"
)

func newQueue(t *testing.T) (*miniredis.Miniredis, *redisq.Queue) {
	t.Helper()
	mr := miniredis.RunT(t)
	q, err := redisq.Open(mr.Addr())
	if err != nil {
		t.Fatalf("open redisq over miniredis: %s", err)
	}
	t.Cleanup(func() { _ = q.Close() })
	return mr, q
}

func mustAdd(t *testing.T, q *redisq.Queue, stream, card string) string {
	t.Helper()
	ctx := context.Background()
	if err := q.EnsureGroup(ctx, stream); err != nil {
		t.Fatalf("ensure group on %s: %s", stream, err)
	}
	id, err := q.Add(ctx, stream, map[string]string{"card": card, "repo": "mas-bandwidth/nova-tools"})
	if err != nil {
		t.Fatalf("add card %s to %s: %s", card, stream, err)
	}
	return id
}

// one-card-is-delivered-to-exactly-one-consumer: with the workers group shared by two
// benches, XREADGROUP hands a card to one bench and not the other; a second group over the
// same stream would hand both a copy, so the test fails unless the group is per stream,
// never per bench.
func TestOneCardIsDeliveredToExactlyOneConsumer(t *testing.T) {
	ctx := context.Background()
	stream := "nova:queue:red:green"
	_, q := newQueue(t)
	id := mustAdd(t, q, stream, "9014")

	a, err := q.Pull(ctx, stream, "bench-a", 0)
	if err != nil {
		t.Fatalf("bench-a pull: %s", err)
	}
	if a == nil || a.ID != id || a.Fields["card"] != "9014" {
		t.Fatalf("bench-a got %+v, want the one card %s", a, id)
	}
	b, err := q.Pull(ctx, stream, "bench-b", 0)
	if err != nil {
		t.Fatalf("bench-b pull: %s", err)
	}
	if b != nil {
		t.Fatalf("bench-b also got %+v; the workers group is shared per stream, not per bench", b)
	}
}

// a-stream-consumer-that-dies-mid-card-has-its-card-reclaimed: kill a puller mid-card;
// XAUTOCLAIM hands the card to the next puller, its partial RESULT.md kept as evidence.
func TestAStreamConsumerThatDiesMidCardHasItsCardReclaimed(t *testing.T) {
	ctx := context.Background()
	stream := "nova:queue:red:small"
	mr, q := newQueue(t)
	id := mustAdd(t, q, stream, "9014")

	// The first bench takes the card and then dies: it never XACKs, and what it had
	// written before dying is left where it is.
	partial := filepath.Join(t.TempDir(), "RESULT.md")
	if err := os.WriteFile(partial, []byte("partial evidence\n"), 0o644); err != nil {
		t.Fatalf("write partial: %s", err)
	}
	first, err := q.Pull(ctx, stream, "bench-a", 0)
	if err != nil || first == nil {
		t.Fatalf("bench-a pull: card=%+v err=%v", first, err)
	}

	// The lease lapses and the next puller reclaims the card with XAUTOCLAIM.
	mr.SetTime(time.Now().UTC().Add(redisq.ConsumerLease + time.Minute))
	second, err := q.Claim(ctx, stream, "bench-b", redisq.ConsumerLease)
	if err != nil {
		t.Fatalf("bench-b claim: %s", err)
	}
	if second == nil || second.ID != id {
		t.Fatalf("bench-b claimed %+v, want the dead bench's card %s", second, id)
	}
	if _, err := os.Stat(partial); err != nil {
		t.Fatalf("the dead bench's partial RESULT.md is kept as evidence: %s", err)
	}
	if err := q.Ack(ctx, stream, second.ID); err != nil {
		t.Fatalf("ack on clip: %s", err)
	}
	pending, err := q.PendingIDs(ctx, stream, "bench-b")
	if err != nil {
		t.Fatalf("pending after ack: %s", err)
	}
	if len(pending) != 0 {
		t.Fatalf("an acked card is a landed card and safe to forget, still pending: %v", pending)
	}
}

// a-cap-counter-refuses-the-41st-in-flight-muse-call: the 40th Muse call admits, the 41st
// is refused, and a lapsed call frees its seat with no reap pass.
func TestACapCounterRefusesThe41stInFlightMuseCall(t *testing.T) {
	ctx := context.Background()
	_, q := newQueue(t)
	base := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	q.SetClock(func() time.Time { return base })

	const cap40 = 40
	deadline := base.Add(time.Hour)
	for i := 0; i < cap40; i++ {
		ok, err := q.Admit(ctx, "muse", "muse-1", cap40, deadline, "call-"+itoa(i))
		if err != nil {
			t.Fatalf("admit %d: %s", i, err)
		}
		if !ok {
			t.Fatalf("the %d-th Muse call was refused before the cap was reached", i+1)
		}
	}
	ok, err := q.Admit(ctx, "muse", "muse-1", cap40, deadline, "call-41")
	if err != nil {
		t.Fatalf("admit 41: %s", err)
	}
	if ok {
		t.Fatalf("the 41st in-flight Muse call was admitted; the cap is a script that refuses")
	}
	if n, err := q.Inflight(ctx, "muse", "muse-1"); err != nil || n != cap40 {
		t.Fatalf("inflight after refusal = %d err=%v, want %d", n, err, cap40)
	}

	// A lapsed call frees its seat on the next admission, with no reap pass.
	q.SetClock(func() time.Time { return base.Add(2 * time.Hour) })
	ok, err = q.Admit(ctx, "muse", "muse-1", cap40, base.Add(3*time.Hour), "call-42")
	if err != nil {
		t.Fatalf("admit after lapse: %s", err)
	}
	if !ok {
		t.Fatalf("a call past its deadline must free its seat on the next admission")
	}
}

// a-cap-admission-is-one-atomic-script-that-refuses-the-41st: concurrent admissions run
// through the single script and exactly 40 hold the key; no ZCARD-then-ZADD interleaving
// lets a 41st in.
func TestACapAdmissionIsOneAtomicScriptThatRefusesThe41st(t *testing.T) {
	ctx := context.Background()
	_, q := newQueue(t)
	base := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	q.SetClock(func() time.Time { return base })

	const cap40 = 40
	const callers = 200
	var admitted int64
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := q.Admit(ctx, "muse", "muse-1", cap40, base.Add(time.Hour), "call-"+itoa(i))
			if err != nil {
				t.Errorf("admit %d: %s", i, err)
				return
			}
			if ok {
				atomic.AddInt64(&admitted, 1)
			}
		}(i)
	}
	wg.Wait()
	if admitted != cap40 {
		t.Fatalf("exactly %d concurrent admissions hold the key, got %d", cap40, admitted)
	}
	if n, err := q.Inflight(ctx, "muse", "muse-1"); err != nil || n != cap40 {
		t.Fatalf("inflight = %d err=%v, want %d", n, err, cap40)
	}
}

// a-stale-lease-token-cannot-renew-or-release-a-slot: a token that lost the SET NX renews
// and releases as a no-op through the Lua script, no bare EXPIRE moves the key, and a bench
// that chose the file mode never touches the Redis key (nor the reverse), so one slot never
// has two modes.
func TestAStaleLeaseTokenCannotRenewOrReleaseASlot(t *testing.T) {
	ctx := context.Background()
	_, q := newQueue(t)
	q.SetClock(func() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) })

	held, ok, err := q.TakeLease(ctx, "space", "slot-3", time.Minute)
	if err != nil || !ok || held == nil {
		t.Fatalf("take lease: lease=%+v ok=%v err=%v", held, ok, err)
	}
	stale := &redisq.Lease{Key: q.LeaseKey("space", "slot-3"), Token: "not-the-holder"}
	if renewed, err := q.RenewLease(ctx, stale, 10*time.Minute); err != nil || renewed {
		t.Fatalf("a stale token renewed the lease: renewed=%v err=%v", renewed, err)
	}
	if released, err := q.ReleaseLease(ctx, stale); err != nil || released {
		t.Fatalf("a stale token released the lease: released=%v err=%v", released, err)
	}
	if token, err := q.LeaseToken(ctx, held.Key); err != nil || token != held.Token {
		t.Fatalf("the holder's lease moved: token=%q err=%v, want %q", token, err, held.Token)
	}
	if renewed, err := q.RenewLease(ctx, held, 10*time.Minute); err != nil || !renewed {
		t.Fatalf("the holder must still renew: renewed=%v err=%v", renewed, err)
	}

	// One mode per bench: the file fallback and the Redis lease never touch each other.
	dir := t.TempDir()
	file := redisq.NewSlotLeases(redisq.ModeDirectory, nil, dir)
	if _, ok, err := file.Take(ctx, "space", "slot-4", time.Minute); err != nil || !ok {
		t.Fatalf("directory take: ok=%v err=%v", ok, err)
	}
	if n, err := q.LeaseCount(ctx); err != nil || n != 1 {
		t.Fatalf("the file-mode bench wrote a Redis lease: count=%d err=%v", n, err)
	}
	redisMode := redisq.NewSlotLeases(redisq.ModeRedis, q, dir)
	funch, ok, err := redisMode.Take(ctx, "space", "slot-5", time.Minute)
	if err != nil || !ok || funch == nil {
		t.Fatalf("redis-mode take: lease=%+v ok=%v err=%v", funch, ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "space", "slot-5")); err == nil {
		t.Fatalf("the redis-mode bench wrote a file lease")
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
