package ci

// events_test.go is the red-test contract of the pub/sub bridge in docs/SPEC-JOBS.md
// "Events, not ticks". The producer turns the cards:done stream and the gh fallback poll
// into card-done, pr-checks-done and dev-moved messages; the reactor turns those into an
// enqueue and a rebase-wanted. Every test runs against miniredis and a fake forge, so no
// test here opens a socket to GitHub or starts a redis server of its own.

import (
	"context"
	"encoding/json"
	"os"
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

// quietNoEvent is how long a test waits to be sure a message did NOT arrive.
func quietNoEvent() time.Duration { return 200 * time.Millisecond }

// reactorDeadline is the bound for a test that only proves the loop returns at one.
func reactorDeadline() time.Duration { return 500 * time.Millisecond }

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

// noMessage fails if any message arrives on the channel within the quiet window.
func noMessage(t *testing.T, ch <-chan *redis.Message, channel string) {
	t.Helper()
	select {
	case msg := <-ch:
		t.Fatalf("unexpected %s message: %s", msg.Channel, msg.Payload)
	case <-time.After(quietNoEvent()):
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

// 2. A completed check suite is published once and only once: a second poll with the same
// conclusion publishes nothing.
func TestProducerPublishesPRChecksDoneOnlyOnChange(t *testing.T) {
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
	noMessage(t, sub.Channel(), ChannelPRChecksDone)
}

// 3. dev-moved is published on the first poll and again only when the base head moves.
func TestProducerPublishesDevMovedOnlyOnChange(t *testing.T) {
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
	noMessage(t, sub.Channel(), ChannelDevMoved)

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
	_, rdb, ctx := newBus(t)
	var enqueued []int
	r := NewReactor(rdb, &fakeForge{}, func(_ context.Context, pr int, _ string) error {
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

// 5. A PR in the skip set is not enqueued.
func TestReactorSkipsTheSkipSet(t *testing.T) {
	_, rdb, ctx := newBus(t)
	if err := rdb.SAdd(ctx, SetEnqueueSkip, "42").Err(); err != nil {
		t.Fatal(err)
	}
	var enqueued []int
	r := NewReactor(rdb, &fakeForge{}, func(_ context.Context, pr int, _ string) error {
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

// 6. While enqueue:hold exists every green PR waits.
func TestReactorHoldsOnTheHoldKey(t *testing.T) {
	_, rdb, ctx := newBus(t)
	if err := rdb.Set(ctx, KeyEnqueueHold, "1", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	var enqueued []int
	r := NewReactor(rdb, &fakeForge{}, func(_ context.Context, pr int, _ string) error {
		enqueued = append(enqueued, pr)
		return nil
	}, nil)
	if err := r.Handle(ctx, ChannelPRChecksDone, `{"number":42,"head":"a1b2","conclusion":"SUCCESS"}`); err != nil {
		t.Fatal(err)
	}
	if len(enqueued) != 0 {
		t.Fatalf("a held PR was enqueued: %v", enqueued)
	}
}

// 7. A red conclusion does not enqueue.
func TestReactorDoesNotEnqueueARed(t *testing.T) {
	_, rdb, ctx := newBus(t)
	var enqueued []int
	r := NewReactor(rdb, &fakeForge{}, func(_ context.Context, pr int, _ string) error {
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
	_, rdb, ctx := newBus(t)
	sub := subscribe(t, rdb, ctx, ChannelRebaseWanted)
	forge := &fakeForge{snap: Snapshot{PRs: []PRState{
		{Number: 7, Branch: "rowan/dirty", Head: "d7", Mergeable: "DIRTY"},
		{Number: 8, Branch: "rowan/clean", Head: "c8", Mergeable: "CLEAN"},
	}}}
	r := NewReactor(rdb, forge, func(context.Context, int, string) error { return nil }, nil)
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
	noMessage(t, sub.Channel(), ChannelRebaseWanted)

	if err := r.Handle(ctx, ChannelDevMoved, `{"sha":"base-2"}`); err != nil {
		t.Fatal(err)
	}
	noMessage(t, sub.Channel(), ChannelRebaseWanted)
}

// 9. card-done publishes nothing: the recorder and the harvester read the stream directly.
func TestReactorCardDonePublishesNothing(t *testing.T) {
	_, rdb, ctx := newBus(t)
	sub := subscribe(t, rdb, ctx, ChannelRebaseWanted, ChannelPRChecksDone, ChannelCardDone)
	var enqueued []int
	r := NewReactor(rdb, &fakeForge{}, func(_ context.Context, pr int, _ string) error {
		enqueued = append(enqueued, pr)
		return nil
	}, nil)
	if err := r.Handle(ctx, ChannelCardDone, `{"card":"card-9348"}`); err != nil {
		t.Fatal(err)
	}
	if len(enqueued) != 0 {
		t.Fatalf("card-done enqueued: %v", enqueued)
	}
	noMessage(t, sub.Channel(), ChannelPRChecksDone)
}

// 10. The loop form returns at its deadline rather than blocking forever.
func TestReactorRunReturnsAtItsDeadline(t *testing.T) {
	_, rdb, ctx := newBus(t)
	deadline, cancel := context.WithTimeout(ctx, reactorDeadline())
	defer cancel()
	r := NewReactor(rdb, &fakeForge{}, func(context.Context, int, string) error { return nil }, nil)
	if err := r.Run(deadline); err != context.DeadlineExceeded {
		t.Fatalf("Run returned %v, want the deadline", err)
	}
}
