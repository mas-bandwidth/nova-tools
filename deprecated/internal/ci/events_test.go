package ci

// events_test.go is the red-test contract of the pub/sub bridge in docs/SPEC-JOBS.md
// "Events, not ticks". The producer turns the cards:done stream and the gh fallback poll
// into card-done, pr-checks-done and dev-moved messages; the reactor turns those into an
// enqueue and a rebase-wanted. Every test runs against miniredis and a fake forge, so no
// test here opens a socket to GitHub or starts a redis server of its own.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// testWait is the allowed poll bound: NOVA_TEST_WAIT when set, thirty seconds otherwise.
// It is read, never written as a constant, so a loaded machine lengthens the wait rather
// than flaking the test.
func testWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// newBus starts a miniredis and returns a client and a cancel for it.
func newBus(t *testing.T) (*miniredis.Miniredis, *redis.Client, context.Context) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb, context.Background()
}

// subscribe confirms a subscription before the caller publishes, so a publish that races
// the subscribe is never read as an absent event.
func subscribe(t *testing.T, rdb *redis.Client, ctx context.Context, channels ...string) *redis.PubSub {
	t.Helper()
	sub := rdb.Subscribe(ctx, channels...)
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	return sub
}

// recvBy reads the next message on the named channel, up to the poll bound.
func recvBy(t *testing.T, ch <-chan *redis.Message, channel string) string {
	t.Helper()
	wait := testWait()
	deadline := time.After(wait)
	for {
		select {
		case <-deadline:
			t.Fatalf("no %s message within %s", channel, wait)
			return ""
		case msg := <-ch:
			if msg.Channel == channel {
				return msg.Payload
			}
		}
	}
}

// noMessage proves nothing was published to the subscription before now, and waits
// out no quiet window to do it (nova-tools#4328: unit tests never wait on the wall
// clock). It publishes a sentinel on channel and requires the sentinel to be the next
// message on ch. Whatever the code under test published had its reply before this
// call, so Redis delivered it to the subscription ahead of the sentinel, in order;
// the bound below is only how long a broken bus is given to deliver the sentinel.
func noMessage(t *testing.T, rdb *redis.Client, ctx context.Context, ch <-chan *redis.Message, channel string) {
	t.Helper()
	const sentinel = "noMessage-sentinel"
	if err := rdb.Publish(ctx, channel, sentinel).Err(); err != nil {
		t.Fatalf("publishing the %s sentinel: %v", channel, err)
	}
	wait := testWait()
	select {
	case msg := <-ch:
		if msg.Channel != channel || msg.Payload != sentinel {
			t.Fatalf("unexpected %s message: %s", msg.Channel, msg.Payload)
		}
	case <-time.After(wait):
		t.Fatalf("the %s sentinel did not arrive within %s", channel, wait)
	}
}

// fakeForge is the gh edge with no gh: a snapshot a test sets, and an error it can raise.
type fakeForge struct {
	snap Snapshot
	err  error
}

func (f *fakeForge) Snapshot() (Snapshot, error) { return f.snap, f.err }

// 1. A card written to the cards:done stream is republished as a card-done message and
// acked in the events consumer group.
func TestProducerPublishesCardDoneFromTheStream(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	sub := subscribe(t, rdb, ctx, ChannelCardDone)

	p := NewProducer(rdb, &fakeForge{}, "events", nil)
	// The group is created at the stream's end; a card written before the group exists
	// is history the bridge does not replay. A real caller starts the bridge first.
	if _, err := p.PublishCardsDone(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamCardsDone,
		Values: map[string]interface{}{"card": "card-9348", "label": "9348"},
	}).Err(); err != nil {
		t.Fatal(err)
	}

	n, err := p.PublishCardsDone(ctx)
	if err != nil {
		t.Fatalf("PublishCardsDone: %v", err)
	}
	if n != 1 {
		t.Fatalf("published %d card-done messages, want 1", n)
	}
	payload := recvBy(t, sub.Channel(), ChannelCardDone)
	var got CardDone
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("card-done payload %q is not JSON: %v", payload, err)
	}
	if got.Card != "card-9348" {
		t.Errorf("card-done card = %q, want card-9348", got.Card)
	}
}

// 1b. Only a card's END is re-announced. The stream carries every transition (#2563) and
// the decide events (#2623); a queued entry and a decide entry are acked and publish
// nothing, an ok entry publishes one card-done, and the group owes no ack afterwards.
func TestProducerAnnouncesOnlyACardsEnd(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	sub := subscribe(t, rdb, ctx, ChannelCardDone)

	p := NewProducer(rdb, &fakeForge{}, "events", nil)
	if _, err := p.PublishCardsDone(ctx); err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string]interface{}{
		{"label": "card-41", "event": "queued"},
		{"label": "card-41", "event": "decide", "unit_id": "card-41", "kind": "rebase", "rung_tried": "flash"},
		{"label": "card-41", "event": "ok", "card": "card-41"},
	} {
		if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: StreamCardsDone, Values: values}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	n, err := p.PublishCardsDone(ctx)
	if err != nil {
		t.Fatalf("PublishCardsDone: %v", err)
	}
	if n != 1 {
		t.Fatalf("published %d card-done messages for one card end, want 1", n)
	}
	var got CardDone
	if err := json.Unmarshal([]byte(recvBy(t, sub.Channel(), ChannelCardDone)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Card != "card-41" {
		t.Errorf("card-done card = %q, want card-41", got.Card)
	}
	noMessage(t, rdb, ctx, sub.Channel(), ChannelCardDone)
	pending, err := rdb.XPending(ctx, StreamCardsDone, GroupEvents).Result()
	if err != nil {
		t.Fatal(err)
	}
	if pending.Count != 0 {
		t.Fatalf("the events group still owes %d acks; every entry read is acked", pending.Count)
	}
}

// 2. A completed check suite is published once and only once: a second poll with the same
// conclusion publishes nothing.
func TestProducerPublishesPRChecksDoneOnlyOnChange(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	sub := subscribe(t, rdb, ctx, ChannelPRChecksDone)
	p := NewProducer(rdb, &fakeForge{snap: Snapshot{PRs: []PRState{
		{Number: 42, Branch: "rowan/x", Head: "a1b2", Conclusion: ConclusionSuccess},
	}}}, "events", nil)

	n, err := p.PollOnce(ctx)
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if n != 1 {
		t.Fatalf("first poll published %d, want 1", n)
	}
	payload := recvBy(t, sub.Channel(), ChannelPRChecksDone)
	var got PRChecksDone
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("pr-checks-done payload %q is not JSON: %v", payload, err)
	}
	if got.Number != 42 || got.Head != "a1b2" || got.Conclusion != ConclusionSuccess {
		t.Errorf("pr-checks-done = %+v, want number 42 head a1b2 conclusion SUCCESS", got)
	}

	if n, err = p.PollOnce(ctx); err != nil || n != 0 {
		t.Fatalf("a second poll of an unchanged suite published %d (err %v), want 0", n, err)
	}
	noMessage(t, rdb, ctx, sub.Channel(), ChannelPRChecksDone)
}

// 3. dev-moved is published on the first poll and again only when the base head moves.
func TestProducerPublishesDevMovedOnlyOnChange(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	sub := subscribe(t, rdb, ctx, ChannelDevMoved)
	forge := &fakeForge{snap: Snapshot{Base: "base-1"}}
	p := NewProducer(rdb, forge, "events", nil)

	if n, err := p.PollOnce(ctx); err != nil || n != 1 {
		t.Fatalf("first poll published %d (err %v), want 1", n, err)
	}
	var got DevMoved
	if err := json.Unmarshal([]byte(recvBy(t, sub.Channel(), ChannelDevMoved)), &got); err != nil {
		t.Fatal(err)
	}
	if got.SHA != "base-1" {
		t.Errorf("dev-moved sha = %q, want base-1", got.SHA)
	}

	if n, err := p.PollOnce(ctx); err != nil || n != 0 {
		t.Fatalf("an unchanged base published %d (err %v), want 0", n, err)
	}
	noMessage(t, rdb, ctx, sub.Channel(), ChannelDevMoved)

	forge.snap.Base = "base-2"
	if n, err := p.PollOnce(ctx); err != nil || n != 1 {
		t.Fatalf("a moved base published %d (err %v), want 1", n, err)
	}
	if err := json.Unmarshal([]byte(recvBy(t, sub.Channel(), ChannelDevMoved)), &got); err != nil {
		t.Fatal(err)
	}
	if got.SHA != "base-2" {
		t.Errorf("dev-moved sha = %q, want base-2", got.SHA)
	}
}

// 4. A green PR that is neither skipped nor held is enqueued, once.
func TestReactorEnqueuesGreenPR(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	var enqueued []int
	r := NewReactor(rdb, &fakeForge{}, openGate{}, func(_ context.Context, pr int, _ string) error {
		enqueued = append(enqueued, pr)
		return nil
	}, nil)

	payload := `{"number":42,"head":"a1b2","conclusion":"SUCCESS"}`
	if err := r.Handle(ctx, ChannelPRChecksDone, payload); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(enqueued) != 1 || enqueued[0] != 42 {
		t.Fatalf("enqueued = %v, want [42]", enqueued)
	}
}

// openGate is a gate that stops nothing, for the tests that are about something else.
type openGate struct{}

func (openGate) Skipped(int) (bool, error)   { return false, nil }
func (openGate) Held() (bool, string, error) { return false, "", nil }

// oneGate is THE ONE HOLD AND THE ONE SKIP SET (edge 18), as a test can set them: the
// reactor reads whatever the lane's queue.json and hold say, and nothing else.
type oneGate struct {
	skip   map[int]bool
	held   bool
	reason string
}

func (g oneGate) Skipped(pr int) (bool, error) { return g.skip[pr], nil }
func (g oneGate) Held() (bool, string, error)  { return g.held, g.reason, nil }

// 5. A PR the QUEUE skips is not enqueued -- the queue's own skip set, not a second one
// in redis (edge 18).
func TestReactorSkipsTheQueuesSkipSet(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	var enqueued []int
	r := NewReactor(rdb, &fakeForge{}, oneGate{skip: map[int]bool{42: true}}, func(_ context.Context, pr int, _ string) error {
		enqueued = append(enqueued, pr)
		return nil
	}, nil)
	if err := r.Handle(ctx, ChannelPRChecksDone, `{"number":42,"head":"a1b2","conclusion":"SUCCESS"}`); err != nil {
		t.Fatal(err)
	}
	if len(enqueued) != 0 {
		t.Fatalf("a skipped PR was enqueued: %v", enqueued)
	}
}

// 6. While the LANE'S hold stands every green PR waits, and the line carries the reason
// the person wrote -- not the name of a redis key (edge 18).
func TestReactorHoldsOnTheLanesHold(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	var enqueued []int
	var log bytes.Buffer
	r := NewReactor(rdb, &fakeForge{}, oneGate{held: true, reason: "the base is frozen"}, func(_ context.Context, pr int, _ string) error {
		enqueued = append(enqueued, pr)
		return nil
	}, &log)
	if err := r.Handle(ctx, ChannelPRChecksDone, `{"number":42,"head":"a1b2","conclusion":"SUCCESS"}`); err != nil {
		t.Fatal(err)
	}
	if len(enqueued) != 0 {
		t.Fatalf("a held PR was enqueued: %v", enqueued)
	}
	if !strings.Contains(log.String(), `the\x20base\x20is\x20frozen`) {
		t.Errorf("the hold line does not carry the reason: %q", log.String())
	}
}

// EDGE 17: a reactor built with no door says so on the first green pull request rather
// than reporting an enqueue into a set nothing reads.
func TestReactorWithNoDoorRefusesRatherThanReportingAnEnqueue(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	r := NewReactor(rdb, &fakeForge{}, openGate{}, nil, nil)
	err := r.Handle(ctx, ChannelPRChecksDone, `{"number":42,"head":"a1b2","conclusion":"SUCCESS"}`)
	if err == nil {
		t.Fatal("a reactor with nowhere to enqueue reported success")
	}
	if !strings.Contains(err.Error(), "--lane") {
		t.Errorf("the failure does not name the door: %v", err)
	}
}

// EDGE 20: one malformed payload is one message dropped, said out loud and counted --
// never the death of the reactor, which already ignores a channel it has never heard of.
func TestReactorDropsAMalformedPayloadAndCarriesOn(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	var enqueued []int
	var log bytes.Buffer
	r := NewReactor(rdb, &fakeForge{}, openGate{}, func(_ context.Context, pr int, _ string) error {
		enqueued = append(enqueued, pr)
		return nil
	}, &log)
	if err := r.act(ctx, ChannelPRChecksDone, "{not json"); err != nil {
		t.Fatalf("a malformed payload killed the reactor: %v", err)
	}
	if r.Dropped != 1 {
		t.Errorf("Dropped = %d, want 1", r.Dropped)
	}
	if !strings.Contains(log.String(), "REACT DROP") {
		t.Errorf("the drop was not said out loud: %q", log.String())
	}
	if err := r.act(ctx, ChannelPRChecksDone, `{"number":42,"head":"a1b2","conclusion":"SUCCESS"}`); err != nil {
		t.Fatalf("the message after the bad one: %v", err)
	}
	if len(enqueued) != 1 || enqueued[0] != 42 {
		t.Fatalf("enqueued = %v, want [42] after the dropped message", enqueued)
	}
}

// 7. A red conclusion does not enqueue.
func TestReactorDoesNotEnqueueARed(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	var enqueued []int
	r := NewReactor(rdb, &fakeForge{}, openGate{}, func(_ context.Context, pr int, _ string) error {
		enqueued = append(enqueued, pr)
		return nil
	}, nil)
	if err := r.Handle(ctx, ChannelPRChecksDone, `{"number":42,"head":"a1b2","conclusion":"FAILURE"}`); err != nil {
		t.Fatal(err)
	}
	if len(enqueued) != 0 {
		t.Fatalf("a red PR was enqueued: %v", enqueued)
	}
}

// 8. A dev-moved event publishes rebase-wanted for every PR the move made DIRTY, once per
// head, and not for a clean one.
func TestReactorPublishesRebaseWantedForDirtyPRs(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	sub := subscribe(t, rdb, ctx, ChannelRebaseWanted)
	forge := &fakeForge{snap: Snapshot{PRs: []PRState{
		{Number: 7, Branch: "rowan/dirty", Head: "d7", Mergeable: "DIRTY"},
		{Number: 8, Branch: "rowan/clean", Head: "c8", Mergeable: "CLEAN"},
	}}}
	r := NewReactor(rdb, forge, openGate{}, func(context.Context, int, string) error { return nil }, nil)
	if err := r.Handle(ctx, ChannelDevMoved, `{"sha":"base-2"}`); err != nil {
		t.Fatal(err)
	}
	var got RebaseWanted
	if err := json.Unmarshal([]byte(recvBy(t, sub.Channel(), ChannelRebaseWanted)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Number != 7 || got.Head != "d7" {
		t.Errorf("rebase-wanted = %+v, want number 7 head d7", got)
	}
	noMessage(t, rdb, ctx, sub.Channel(), ChannelRebaseWanted)

	if err := r.Handle(ctx, ChannelDevMoved, `{"sha":"base-2"}`); err != nil {
		t.Fatal(err)
	}
	noMessage(t, rdb, ctx, sub.Channel(), ChannelRebaseWanted)
}

// 9. card-done publishes nothing: the recorder and the harvester read the stream directly.
func TestReactorCardDonePublishesNothing(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	sub := subscribe(t, rdb, ctx, ChannelRebaseWanted, ChannelPRChecksDone, ChannelCardDone)
	var enqueued []int
	r := NewReactor(rdb, &fakeForge{}, openGate{}, func(_ context.Context, pr int, _ string) error {
		enqueued = append(enqueued, pr)
		return nil
	}, nil)
	if err := r.Handle(ctx, ChannelCardDone, `{"card":"card-9348"}`); err != nil {
		t.Fatal(err)
	}
	if len(enqueued) != 0 {
		t.Fatalf("card-done enqueued: %v", enqueued)
	}
	noMessage(t, rdb, ctx, sub.Channel(), ChannelPRChecksDone)
}

// 10. The loop form returns at its deadline rather than blocking forever. The deadline
// is the test's to reach: it passes once the reactor has subscribed, never after a
// timer (nova-tools#4328).
func TestReactorRunReturnsAtItsDeadline(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	deadline := newTestDeadline(ctx)
	subscribed := make(chan struct{})
	r := NewReactor(rdb, &fakeForge{}, openGate{}, func(context.Context, int, string) error { return nil }, nil)
	r.subscribed = func() { close(subscribed) }
	done := make(chan error, 1)
	go func() { done <- r.Run(deadline) }()
	wait := testWait()
	select {
	case <-subscribed:
	case err := <-done:
		t.Fatalf("Run returned %v before it subscribed", err)
	case <-time.After(wait):
		t.Fatalf("Run did not subscribe within %s", wait)
	}
	deadline.pass()
	select {
	case err := <-done:
		if err != context.DeadlineExceeded {
			t.Fatalf("Run returned %v, want the deadline", err)
		}
	case <-time.After(wait):
		t.Fatalf("Run did not return within %s of its deadline passing", wait)
	}
}

// testDeadline is a context whose deadline passes when the test says so: Done closes
// and Err is context.DeadlineExceeded, the way a real deadline ends a context.
type testDeadline struct {
	context.Context
	done chan struct{}
	once sync.Once
}

func newTestDeadline(parent context.Context) *testDeadline {
	return &testDeadline{Context: parent, done: make(chan struct{})}
}

func (d *testDeadline) pass() { d.once.Do(func() { close(d.done) }) }

func (d *testDeadline) Done() <-chan struct{} { return d.done }

func (d *testDeadline) Err() error {
	select {
	case <-d.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// tripCounter is a go-redis hook that counts round trips: one per single command,
// one per pipeline Exec regardless of how many commands it carries.
type tripCounter struct{ n int }

func (t *tripCounter) DialHook(next redis.DialHook) redis.DialHook { return next }
func (t *tripCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		t.n++
		return next(ctx, cmd)
	}
}
func (t *tripCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		t.n++
		return next(ctx, cmds)
	}
}

// 11. PublishCardsDone with 100 pending entries uses at most 2 round trips:
// one XREADGROUP and one pipeline of all PUBLISH + one XACK. This is the
// DONE-WHEN test for #3269.
func TestPublishCardsDoneBatchesToOnePipeline(t *testing.T) {
	t.Parallel()

	_, rdb, ctx := newBus(t)
	tc := &tripCounter{}
	rdb.AddHook(tc)

	p := NewProducer(rdb, &fakeForge{}, "events", nil)
	// First call creates the group (1 round trip) and reads nothing.
	if _, err := p.PublishCardsDone(ctx); err != nil {
		t.Fatal(err)
	}

	// Seed 100 entries that are all card ends.
	for i := 0; i < 100; i++ {
		if err := rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: StreamCardsDone,
			Values: map[string]interface{}{
				"card":  fmt.Sprintf("card-%d", i),
				"label": fmt.Sprintf("%d", i),
				"event": "ok",
			},
		}).Err(); err != nil {
			t.Fatal(err)
		}
	}

	// One tick: read then act.
	tc.n = 0
	if _, err := p.PublishCardsDone(ctx); err != nil {
		t.Fatal(err)
	}
	tickTrips := tc.n
	if tickTrips > 2 {
		t.Fatalf("one tick used %d round trips, want <= 2", tickTrips)
	}
	if tickTrips < 1 {
		t.Fatalf("one tick used %d round trips, want >= 1", tickTrips)
	}
}
