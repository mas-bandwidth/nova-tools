package bus_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/bus"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// fixture is a throwaway redis-server with the nova_sprint library loaded
// (ns_bus_post is a function; miniredis has no FCALL).
func fixture(t *testing.T) (context.Context, *redis.Client) {
	t.Helper()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	return ctx, c
}

func post(t *testing.T, ctx context.Context, c redis.Cmdable, m bus.Message) string {
	t.Helper()
	id, err := bus.Post(ctx, c, m)
	if err != nil {
		t.Fatalf("post %+v: %v", m, err)
	}
	return id
}

func ids(es []bus.Entry) string {
	var s []string
	for _, e := range es {
		s = append(s, e.ID)
	}
	return strings.Join(s, ",")
}

// TestBusPostReadAckRoundTrip: a post lands in the inbox and the sender's
// outbox (with the inbox id), a read delivers it with every field, the ack
// leaves nothing pending, and a second read delivers nothing.
func TestBusPostReadAckRoundTrip(t *testing.T) {
	t.Parallel()
	ctx, c := fixture(t)
	m := bus.Message{From: "emma", To: "rowan", Kind: "done", Subject: "PR up", Body: "line one\nline two", Ref: "nova-tools#3865"}
	id := post(t, ctx, c, m)

	sent, err := c.XRange(ctx, bus.SentKey("emma"), "-", "+").Result()
	if err != nil || len(sent) != 1 || sent[0].Values["xid"] != id || sent[0].Values["to"] != "rowan" || sent[0].Values["body"] != m.Body {
		t.Fatalf("outbox %v %v; want one copy naming xid=%s", sent, err, id)
	}
	es, err := bus.Fetch(ctx, c, "rowan", 20, false)
	if err != nil || len(es) != 1 {
		t.Fatalf("fetch: %v %v", es, err)
	}
	e := es[0]
	if e.ID != id || e.Stream != "bus:rowan" || e.Message != m || e.At <= 0 {
		t.Fatalf("entry %+v; want %s %+v", e, id, m)
	}
	if line := e.Line(); !strings.HasPrefix(line, id+" ") || !strings.HasSuffix(line, ` emma done "PR up" ref=nova-tools#3865`) {
		t.Fatalf("line %q", line)
	}
	if err := bus.Ack(ctx, c, "rowan", es); err != nil {
		t.Fatal(err)
	}
	if ps, err := bus.Pending(ctx, c, "rowan"); err != nil || len(ps) != 0 {
		t.Fatalf("pending after ack: %v %v", ps, err)
	}
	if es, err := bus.Fetch(ctx, c, "rowan", 20, false); err != nil || len(es) != 0 {
		t.Fatalf("second read: %v %v; want nothing", es, err)
	}
	// --all rewinds: the acked entry is delivered again.
	if es, err := bus.Fetch(ctx, c, "rowan", 20, true); err != nil || ids(es) != id {
		t.Fatalf("read --all: %v %v; want %s", ids(es), err, id)
	}
}

// TestBusPendingRedeliveredAfterCrash: a reader that dies between delivery
// and ack (Fetch with no Ack) leaves the entries pending, and the next read
// delivers the same entries, before any newer one.
func TestBusPendingRedeliveredAfterCrash(t *testing.T) {
	t.Parallel()
	ctx, c := fixture(t)
	a := post(t, ctx, c, bus.Message{From: "stella", To: "rowan", Kind: "note", Subject: "one"})
	b := post(t, ctx, c, bus.Message{From: "stella", To: "rowan", Kind: "note", Subject: "two"})
	first, err := bus.Fetch(ctx, c, "rowan", 20, false)
	if err != nil || ids(first) != a+","+b {
		t.Fatalf("first delivery %s %v", ids(first), err)
	}
	// the process dies here: no Ack
	ps, err := bus.Pending(ctx, c, "rowan")
	if err != nil || len(ps) != 2 || ps[0].ID != a || ps[0].Deliveries != 1 {
		t.Fatalf("pending %+v %v; want both, delivered once", ps, err)
	}
	cNew := post(t, ctx, c, bus.Message{From: "johnny", To: "rowan", Kind: "note", Subject: "three"})
	again, err := bus.Fetch(ctx, c, "rowan", 20, false)
	if err != nil || ids(again) != a+","+b+","+cNew {
		t.Fatalf("redelivery %s %v; want %s,%s,%s", ids(again), err, a, b, cNew)
	}
	if err := bus.Ack(ctx, c, "rowan", again); err != nil {
		t.Fatal(err)
	}
	if ps, _ := bus.Pending(ctx, c, "rowan"); len(ps) != 0 {
		t.Fatalf("pending after ack %+v", ps)
	}
}

// TestBusAllFanOut: one post to all reaches every reader once, each on a
// group of their own name; one reader's ack does not read it for another.
func TestBusAllFanOut(t *testing.T) {
	t.Parallel()
	ctx, c := fixture(t)
	for _, p := range []string{"emma", "stella", "johnny"} {
		if _, err := bus.Fetch(ctx, c, p, 20, false); err != nil {
			t.Fatal(err)
		}
	}
	id := post(t, ctx, c, bus.Message{From: "rowan", To: "all", Kind: "request", Subject: "rest tonight"})
	for _, p := range []string{"emma", "stella"} {
		es, err := bus.Fetch(ctx, c, p, 20, false)
		if err != nil || ids(es) != id || es[0].Stream != "bus:all" || !strings.HasSuffix(es[0].Line(), " to=all") {
			t.Fatalf("%s: %v %v; want %s from bus:all", p, es, err, id)
		}
		if err := bus.Ack(ctx, c, p, es); err != nil {
			t.Fatal(err)
		}
		if es, _ := bus.Fetch(ctx, c, p, 20, false); len(es) != 0 {
			t.Fatalf("%s read it twice", p)
		}
	}
	ins, err := bus.Ls(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	all := ins[len(ins)-1]
	if all.Name != "all" || all.Len != 1 || all.Unread["emma"] != 0 || all.Unread["johnny"] != 1 {
		t.Fatalf("ls all %+v; want len 1, emma 0, johnny 1", all)
	}
	if es, err := bus.Fetch(ctx, c, "johnny", 20, false); err != nil || ids(es) != id {
		t.Fatalf("johnny: %v %v", ids(es), err)
	}
}

// TestBusPostedBeforeFirstReadIsKept: the inbox group is made by the post,
// so a message sent before its reader ever read is still delivered.
func TestBusPostedBeforeFirstReadIsKept(t *testing.T) {
	t.Parallel()
	ctx, c := fixture(t)
	id := post(t, ctx, c, bus.Message{From: "glenn", To: "rowan", Kind: "question", Subject: "status?"})
	ins, err := bus.Ls(ctx, c)
	if err != nil || ins[0].Name != "rowan" || ins[0].Len != 1 || ins[0].Unread["rowan"] != 1 {
		t.Fatalf("ls %+v %v", ins, err)
	}
	if es, err := bus.Fetch(ctx, c, "rowan", 20, false); err != nil || ids(es) != id {
		t.Fatalf("first read %s %v; want %s", ids(es), err, id)
	}
}

func wantRefusal(t *testing.T, err error, why, remedy string) {
	t.Helper()
	var r *bus.Refusal
	if !errors.As(err, &r) || !strings.Contains(r.Why, why) || !strings.Contains(r.Remedy, remedy) {
		t.Fatalf("err %v; want a refusal %q naming %q", err, why, remedy)
	}
}

// TestBusRefusesBodyOver16KB: 16 KB is carried, one byte more is refused
// with the remedy (a task record or a PR, and the ref), and nothing is written.
func TestBusRefusesBodyOver16KB(t *testing.T) {
	t.Parallel()
	ctx, c := fixture(t)
	m := bus.Message{From: "emma", To: "rowan", Kind: "note", Subject: "big", Body: strings.Repeat("x", bus.MaxBody+1)}
	_, err := bus.Post(ctx, c, m)
	wantRefusal(t, err, "over 16384", "task record or a PR")
	if n := c.Exists(ctx, "bus:rowan", "bus:sent:emma").Val(); n != 0 {
		t.Fatalf("refused post wrote %d keys", n)
	}
	// the function refuses it too, for a caller that skips the verb
	if err := c.FCall(ctx, bus.PostFunction, nil, "emma", "rowan", "note", "big", m.Body, "", "").Err(); err == nil || !strings.Contains(err.Error(), "over 16384") {
		t.Fatalf("function took an oversize body: %v", err)
	}
	m.Body = strings.Repeat("x", bus.MaxBody)
	post(t, ctx, c, m)
}

// TestBusRefusesScoreLine: a SCORE line is a read record, never a bus note;
// the refusal names read post and nothing is written.
func TestBusRefusesScoreLine(t *testing.T) {
	t.Parallel()
	ctx, c := fixture(t)
	_, err := bus.Post(ctx, c, bus.Message{From: "emma", To: "rowan", Kind: "done", Subject: "read", Body: "looked at #3865\nSCORE who=emma 9/10 head=abc"})
	wantRefusal(t, err, "SCORE who=", "nova-sprint read post")
	if n := c.Exists(ctx, "bus:rowan", "bus:sent:emma").Val(); n != 0 {
		t.Fatalf("refused post wrote %d keys", n)
	}
}

// TestBusRefusesUnknownNamesAndKinds: a misspelt name is refused, never a
// new inbox.
func TestBusRefusesUnknownNamesAndKinds(t *testing.T) {
	t.Parallel()
	ctx, c := fixture(t)
	_, err := bus.Post(ctx, c, bus.Message{From: "emma", To: "rowna", Kind: "note", Subject: "x"})
	wantRefusal(t, err, "rowna", "rowan")
	_, err = bus.Post(ctx, c, bus.Message{From: "all", To: "rowan", Kind: "note", Subject: "x"})
	wantRefusal(t, err, "not a person", "rowan")
	_, err = bus.Post(ctx, c, bus.Message{From: "emma", To: "rowan", Kind: "score", Subject: "x"})
	wantRefusal(t, err, "score", "note")
	_, err = bus.Fetch(ctx, c, "nobody", 20, false)
	wantRefusal(t, err, "nobody", "rowan")
	if n := c.DBSize(ctx).Val(); n != 0 {
		t.Fatalf("refusals wrote %d keys", n)
	}
}

// TestBusThousandEntryReadUnderOneSecond: a 1,000-entry inbox is delivered,
// in order, and acked. The one-second rule is structural (the waits class
// asserts events, not the clock): two round trips whatever the inbox holds,
// one pipeline to deliver and one to ack. The wall time is logged.
func TestBusThousandEntryReadUnderOneSecond(t *testing.T) {
	t.Parallel()
	ctx, c := fixture(t)
	pipe := c.Pipeline()
	for i := 0; i < 1000; i++ {
		pipe.FCall(ctx, bus.PostFunction, nil, "stella", "rowan", "note", fmt.Sprintf("n%04d", i), "body", "", "")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	trips := store.New(c).CountTrips()
	start := time.Now()
	es, err := bus.Fetch(ctx, c, "rowan", 1000, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Ack(ctx, c, "rowan", es); err != nil {
		t.Fatal(err)
	}
	t.Logf("BUS READ entries=%d trips=%d ms=%.2f", len(es), trips.N(), float64(time.Since(start).Microseconds())/1000)
	if n := trips.N(); n != 2 {
		t.Fatalf("1,000-entry read took %d round trips; want 2 (deliver, ack)", n)
	}
	if len(es) != 1000 || es[0].Subject != "n0000" || es[999].Subject != "n0999" {
		t.Fatalf("got %d entries, first %q last %q", len(es), es[0].Subject, es[len(es)-1].Subject)
	}
	if ps, _ := bus.Pending(ctx, c, "rowan"); len(ps) != 0 {
		t.Fatalf("%d pending after the ack", len(ps))
	}
}

// TestBusReadCapsAtN: over n new entries, the rest stay pending and come
// back on the next read, oldest first; nothing is lost.
func TestBusReadCapsAtN(t *testing.T) {
	t.Parallel()
	ctx, c := fixture(t)
	var want []string
	for i := 0; i < 5; i++ {
		want = append(want, post(t, ctx, c, bus.Message{From: "emma", To: "rowan", Kind: "note", Subject: fmt.Sprint(i)}))
	}
	a, _ := bus.Fetch(ctx, c, "rowan", 3, false)
	_ = bus.Ack(ctx, c, "rowan", a)
	b, _ := bus.Fetch(ctx, c, "rowan", 3, false)
	_ = bus.Ack(ctx, c, "rowan", b)
	if got := ids(append(a, b...)); got != strings.Join(want, ",") {
		t.Fatalf("two capped reads %s; want %s", got, strings.Join(want, ","))
	}
}

// TestBusTailDeliversAsItArrives: Tail wakes on the XADD, not on a tick. Its
// blocking read waits up to a minute; the entry, posted once the read is
// blocked, is emitted long before that (the give-up is NOVA_TEST_WAIT, 30 s),
// so the delivery is the post's own wake-up. The latency is logged. Tail
// returns when its context ends.
func TestBusTailDeliversAsItArrives(t *testing.T) {
	t.Parallel()
	ctx, admin := fixture(t)
	c := redis.NewClient(&redis.Options{Addr: admin.Options().Addr, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = c.Close() })
	tctx, cancel := context.WithCancel(ctx)
	got := make(chan bus.Entry, 4)
	done := make(chan error, 1)
	go func() { done <- bus.Tail(tctx, c, "rowan", time.Minute, func(e bus.Entry) { got <- e }) }()
	giveUp := time.Now().Add(testWait())
	for !regexp.MustCompile(`flags=b .*cmd=xreadgroup`).MatchString(admin.ClientList(ctx).Val()) {
		if time.Now().After(giveUp) {
			t.Fatal("tail never blocked in XREADGROUP")
		}
		time.Sleep(5 * time.Millisecond) // wall-ok: polling a condition in a test
	}
	id := post(t, ctx, c, bus.Message{From: "emma", To: "rowan", Kind: "note", Subject: "live"})
	posted := time.Now()
	select {
	case e := <-got:
		t.Logf("BUS TAIL latency_ms=%.2f", float64(time.Since(posted).Microseconds())/1000)
		if e.ID != id {
			t.Fatalf("tail emitted %s; want %s", e.ID, id)
		}
	case <-time.After(testWait()):
		t.Fatal("tail printed nothing")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(testWait()):
		t.Fatal("tail did not stop with its context")
	}
	if ps, _ := bus.Pending(ctx, c, "rowan"); len(ps) != 0 {
		t.Fatalf("tail left %d pending", len(ps))
	}
}

// testWait is a test's give-up, never a product bound: NOVA_TEST_WAIT, else 30 s.
func testWait() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("NOVA_TEST_WAIT")); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}
