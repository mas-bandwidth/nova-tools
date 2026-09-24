package redisq_test

// The red tests nova-tools #2196 names, for behaviours 10, 11 and 12 of the
// "Tests this spec demands" section docs/SPEC-STATE.md carries (PR #2195):
// the fence that reads the lease token before every guarded write, the
// mode-restart drain gate, and the three-set reservation over provider, model
// and key. A miniredis fake stands in for the instance, every path is a
// temporary directory, and no test reaches the network.
//
// Each subtest first asserts the behaviour EXISTS -- on the base sha the
// fence, the gate and the reservation are absent, and the test fails there
// and fails the same way when the production change is reverted -- through a
// runtime interface assertion, so the file compiles against a tree without
// the behaviours and the red is a failure that names them, never a build
// error. The behavioural half then runs against miniredis, which skips where
// the sandbox forbids listening sockets (the way internal/swarm's routeladder
// suite asks) and runs fully on a bench; the mode-drain half is all files and
// runs everywhere.

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/redisq"
	"github.com/redis/go-redis/v9"
)

// fencedWrites is the fence of SPEC-STATE ("Every write a lease guards is
// fenced"): the clip, the XACK, the RESULT publish and the harvest push, each
// carrying the caller's fencing token and comparing it before the write.
type fencedWrites interface {
	Fenced(ctx context.Context, l *redisq.Lease) (bool, error)
	FencedAck(ctx context.Context, l *redisq.Lease, stream, id string) (bool, error)
	FencedPublish(ctx context.Context, l *redisq.Lease, channel, payload string) (bool, error)
	FencedPush(ctx context.Context, l *redisq.Lease, card, row string) (bool, error)
}

// capReservation is the three-set reservation of SPEC-STATE ("In-flight caps
// -- one atomic reservation over provider, model and key"): the all-or-nothing
// Reserve, its mirror Release, and the count of each scope set.
type capReservation interface {
	Reserve(ctx context.Context, provider, model, key string, capProvider, capModel, capKey int, bound time.Time, callID string) (bool, error)
	Release(ctx context.Context, provider, model, key, callID string) (bool, error)
	InflightProvider(ctx context.Context, provider string) (int64, error)
	InflightModel(ctx context.Context, model string) (int64, error)
	InflightKey(ctx context.Context, key string) (int64, error)
}

// modeDrainGate is the drain gate of SPEC-STATE ("One mode per bench"): the
// old store's live cards, and the restart in the other mode it refuses to let
// start until they are drained.
type modeDrainGate interface {
	LiveCards() ([]string, error)
	ModeRestart(log io.Writer, reclaimed, completed int, newMode redisq.Mode) (bool, error)
}

// testWait is the allowed poll bound for one expected event: NOVA_TEST_WAIT
// when set, thirty seconds otherwise (the shape internal/ci's events suite
// keeps). No test asserts a bound under it.
func testWait(t *testing.T) time.Duration {
	t.Helper()
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 30 * time.Second
}

// TestIssue2196 is the anchor the card names: it runs the three tests the
// issue lists, each under the spec's own name, so the one command the card
// gates on exercises every behaviour.
func TestIssue2196(t *testing.T) {
	t.Run("APausedOldWorkerCannotWriteAfterItsLeaseLapsed", testAPausedOldWorkerCannotWriteAfterItsLeaseLapsed)
	t.Run("AModeRestartRefusesToStartUntilTheOldModesCardsAreDrained", testAModeRestartRefusesToStartUntilTheOldModesCardsAreDrained)
	t.Run("ACapReservationOverProviderModelAndKeyIsOneAtomicScript", testACapReservationOverProviderModelAndKeyIsOneAtomicScript)
}

// a-paused-old-worker-cannot-write-after-its-lease-lapsed: take a lease, let
// it lapse, re-lease to a new token, then run the old worker's clip, XACK,
// RESULT publish and harvest push through the fenced Lua; every one returns 0
// and nothing lands, while the current holder's identical writes each return 1.
func testAPausedOldWorkerCannotWriteAfterItsLeaseLapsed(t *testing.T) {
	// The red gate: on a tree without the fence the test fails here, naming
	// the behaviour, before any Redis is needed.
	if _, ok := any((*redisq.Queue)(nil)).(fencedWrites); !ok {
		t.Fatalf(`docs/SPEC-STATE.md "Every write a lease guards is fenced": the clip, XACK, RESULT publish and harvest push carry no fencing token, so a worker that lost its lease to XAUTOCLAIM could still write (nova-tools#2196)`)
	}
	ctx := context.Background()
	stream := "nova:queue:red:green"
	mr, q := newQueue(t)
	id := mustAdd(t, q, stream, "9014")
	fq, _ := any(q).(fencedWrites)

	// The old worker holds the card and the lease; the lease lapses and the
	// slot is re-leased to a new token, which is XAUTOCLAIM's moment.
	if _, err := q.Pull(ctx, stream, "bench-a", 0); err != nil {
		t.Fatalf("bench-a pull: %s", err)
	}
	old, ok, err := q.TakeLease(ctx, "space", "slot-9", time.Minute)
	if err != nil || !ok || old == nil {
		t.Fatalf("the old worker takes the lease: lease=%+v ok=%v err=%v", old, ok, err)
	}
	mr.FastForward(2 * time.Minute)
	fresh, ok, err := q.TakeLease(ctx, "space", "slot-9", time.Minute)
	if err != nil || !ok || fresh == nil {
		t.Fatalf("the lapsed lease is not re-leased to a new token: lease=%+v ok=%v err=%v", fresh, ok, err)
	}

	// A subscriber stands at the events channel, so a publish that lands is a
	// publish it receives.
	events := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = events.Close() })
	sub := events.Subscribe(ctx, "nova:events:job")
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe to the job events channel: %s", err)
	}

	// The old worker's four guarded writes: each returns 0 -- the clip's gate
	// refuses, so no clip is made; the XACK, the RESULT publish and the
	// harvest push are refused inside their one script with the write.
	if fenced, err := fq.Fenced(ctx, old); err != nil || fenced {
		t.Fatalf("the old worker's clip passed the fence: fenced=%v err=%v", fenced, err)
	}
	if acked, err := fq.FencedAck(ctx, old, stream, id); err != nil || acked {
		t.Fatalf("the old worker's XACK passed the fence: acked=%v err=%v", acked, err)
	}
	if published, err := fq.FencedPublish(ctx, old, "nova:events:job", "RESULT 9014 from the old worker"); err != nil || published {
		t.Fatalf("the old worker's RESULT publish passed the fence: published=%v err=%v", published, err)
	}
	if pushed, err := fq.FencedPush(ctx, old, "9014", "timeline row from the old worker"); err != nil || pushed {
		t.Fatalf("the old worker's harvest push passed the fence: pushed=%v err=%v", pushed, err)
	}
	// And nothing lands: the card is still pending where the old worker left it.
	pending, err := q.PendingIDs(ctx, stream, "bench-a")
	if err != nil {
		t.Fatalf("pending after the old worker's writes: %s", err)
	}
	if len(pending) != 1 || pending[0] != id {
		t.Fatalf("the old worker's XACK landed anyway: pending=%v, want [%s]", pending, id)
	}

	// The current holder's identical writes each return 1 and each lands.
	if fenced, err := fq.Fenced(ctx, fresh); err != nil || !fenced {
		t.Fatalf("the current holder's clip is refused by the fence: fenced=%v err=%v", fenced, err)
	}
	if acked, err := fq.FencedAck(ctx, fresh, stream, id); err != nil || !acked {
		t.Fatalf("the current holder's XACK is refused by the fence: acked=%v err=%v", acked, err)
	}
	pending, err = q.PendingIDs(ctx, stream, "bench-a")
	if err != nil || len(pending) != 0 {
		t.Fatalf("the current holder's XACK did not land: pending=%v err=%v", pending, err)
	}
	if published, err := fq.FencedPublish(ctx, fresh, "nova:events:job", "RESULT 9014 from the current holder"); err != nil || !published {
		t.Fatalf("the current holder's RESULT publish is refused by the fence: published=%v err=%v", published, err)
	}
	if pushed, err := fq.FencedPush(ctx, fresh, "9014", "timeline row from the current holder"); err != nil || !pushed {
		t.Fatalf("the current holder's harvest push is refused by the fence: pushed=%v err=%v", pushed, err)
	}

	// The event that landed is the holder's: the old worker's publish never
	// landed, or its payload would have arrived first.
	wait, cancel := context.WithTimeout(ctx, testWait(t))
	defer cancel()
	msg, err := sub.ReceiveMessage(wait)
	if err != nil {
		t.Fatalf("the current holder's RESULT publish never landed: %s", err)
	}
	if msg.Payload != "RESULT 9014 from the current holder" {
		t.Fatalf("the landed event is %q, want the current holder's; the old worker's publish landed too", msg.Payload)
	}
	// And the pushed timeline row is the holder's, one row for the card.
	rows, err := events.XRange(ctx, "cards:done", "-", "+").Result()
	if err != nil {
		t.Fatalf("read the done stream: %s", err)
	}
	if len(rows) != 1 || rows[0].Values["card"] != "9014" || rows[0].Values["row"] != "timeline row from the current holder" {
		t.Fatalf("the pushed rows are %+v, want the current holder's one row for card 9014", rows)
	}
}

// a-mode-restart-refuses-to-start-until-the-old-modes-cards-are-drained: a
// bench with a live directory-mode card refuses to start Redis mode; the card
// is reclaimed or completed, the drain is written to the bench's log, and only
// then does the new mode start.
func testAModeRestartRefusesToStartUntilTheOldModesCardsAreDrained(t *testing.T) {
	// The red gate: on a tree without the drain gate the test fails here.
	if _, ok := any((*redisq.DirQueue)(nil)).(modeDrainGate); !ok {
		t.Fatalf(`docs/SPEC-STATE.md "A restart in the other mode drains survivors first": the mode restart carries no drain gate, so a live old-mode card would be silently dropped on the switch (nova-tools#2196)`)
	}
	stream := "nova:queue:cut:red"
	dq := &redisq.DirQueue{Root: t.TempDir()}
	gate, _ := any(dq).(modeDrainGate)

	// A live directory-mode card: taken by a worker, never acked.
	if _, err := dq.Add(stream, "card-1", map[string]string{"card": "9014"}); err != nil {
		t.Fatalf("add a directory-mode card: %s", err)
	}
	if card, err := dq.Pull(stream); err != nil || card == nil {
		t.Fatalf("the card is not taken by its worker: card=%+v err=%v", card, err)
	}
	live, err := gate.LiveCards()
	if err != nil {
		t.Fatalf("the old mode's live cards: %s", err)
	}
	if len(live) != 1 || live[0] != stream+"/card-1" {
		t.Fatalf("the old mode's live cards are %v, want [%s/card-1]", live, stream)
	}

	// The restart in Redis mode is refused while the old mode holds the card,
	// and nothing is written to the bench's log for a restart that did not start.
	var benchLog strings.Builder
	started, err := gate.ModeRestart(&benchLog, 0, 0, redisq.ModeRedis)
	if err != nil {
		t.Fatalf("the mode restart gate: %s", err)
	}
	if started {
		t.Fatal("the restart started Redis mode while a live directory-mode card was still held")
	}
	if strings.Contains(benchLog.String(), "mode restart") {
		t.Fatalf("an undrained restart wrote a drain line: %q", benchLog.String())
	}

	// The card is completed: its result landed, so it is safe to forget.
	if err := dq.Ack(stream, "card-1"); err != nil {
		t.Fatalf("complete the live card: %s", err)
	}
	live, err = gate.LiveCards()
	if err != nil || len(live) != 0 {
		t.Fatalf("the drained store still holds live cards: %v err=%v", live, err)
	}

	// Only now does the new mode start, and the drain is in the bench's log.
	started, err = gate.ModeRestart(&benchLog, 0, 1, redisq.ModeRedis)
	if err != nil {
		t.Fatalf("the mode restart gate after the drain: %s", err)
	}
	if !started {
		t.Fatal("the restart is still refused after every old-mode card was drained")
	}
	if got, want := benchLog.String(), "mode restart: directory drained (0 reclaimed, 1 completed) -> redis\n"; got != want {
		t.Fatalf("the drain line is %q, want %q", got, want)
	}
}

// a-cap-reservation-over-provider-model-and-key-is-one-atomic-script: with the
// key or model scope full, a call that would fit the provider scope alone is
// refused with none of the three sets written; an admitted call reserves all
// three together and releases all three together.
func testACapReservationOverProviderModelAndKeyIsOneAtomicScript(t *testing.T) {
	// The red gate: on a tree without the three-set reservation the test
	// fails here.
	if _, ok := any((*redisq.Queue)(nil)).(capReservation); !ok {
		t.Fatalf(`docs/SPEC-STATE.md "one atomic reservation over provider, model and key": the three-set Reserve/Release is absent, so two scopes could be reserved and the third refused (nova-tools#2196)`)
	}
	ctx := context.Background()
	_, q := newQueue(t)
	cq, _ := any(q).(capReservation)
	base := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	q.SetClock(func() time.Time { return base })
	bound := base.Add(time.Hour)

	// The key scope is full: a call that fits the provider scope alone is
	// refused, with none of the three sets written.
	if ok, err := cq.Reserve(ctx, "muse", "muse-1", "seat-1", 40, 40, 1, bound, "call-1"); err != nil || !ok {
		t.Fatalf("the first call reserves the provider, model and key scopes together: ok=%v err=%v", ok, err)
	}
	if ok, err := cq.Reserve(ctx, "muse", "muse-1", "seat-1", 40, 40, 1, bound, "call-2"); err != nil || ok {
		t.Fatalf("a call that fits the provider scope alone was admitted against a full key scope: ok=%v err=%v", ok, err)
	}
	if n, err := cq.InflightProvider(ctx, "muse"); err != nil || n != 1 {
		t.Fatalf("the refused call wrote the provider set: count=%d err=%v, want 1", n, err)
	}
	if n, err := cq.InflightModel(ctx, "muse-1"); err != nil || n != 1 {
		t.Fatalf("the refused call wrote the model set: count=%d err=%v, want 1", n, err)
	}
	if n, err := cq.InflightKey(ctx, "seat-1"); err != nil || n != 1 {
		t.Fatalf("the refused call wrote the key set: count=%d err=%v, want 1", n, err)
	}

	// The model scope full refuses the same way.
	if ok, err := cq.Reserve(ctx, "grok", "grok-4", "seat-7", 40, 1, 40, bound, "call-3"); err != nil || !ok {
		t.Fatalf("the first Grok call reserves all three scopes together: ok=%v err=%v", ok, err)
	}
	if ok, err := cq.Reserve(ctx, "grok", "grok-4", "seat-8", 40, 1, 40, bound, "call-4"); err != nil || ok {
		t.Fatalf("a call that fits the provider and key scopes alone was admitted against a full model scope: ok=%v err=%v", ok, err)
	}
	if n, err := cq.InflightProvider(ctx, "grok"); err != nil || n != 1 {
		t.Fatalf("the refused call wrote the provider set: count=%d err=%v, want 1", n, err)
	}
	if n, err := cq.InflightModel(ctx, "grok-4"); err != nil || n != 1 {
		t.Fatalf("the refused call wrote the model set: count=%d err=%v, want 1", n, err)
	}
	if n, err := cq.InflightKey(ctx, "seat-8"); err != nil || n != 0 {
		t.Fatalf("the refused call wrote the key set: count=%d err=%v, want 0", n, err)
	}

	// The reservation is its own three sets, distinct from the single-set
	// admission counter the package already keeps.
	if n, err := q.Inflight(ctx, "muse", "muse-1"); err != nil || n != 0 {
		t.Fatalf("the reservation wrote the single-set admission counter: count=%d err=%v, want 0", n, err)
	}

	// A released seat frees all three scopes together, and the freed scopes
	// admit the next call.
	if ok, err := cq.Release(ctx, "muse", "muse-1", "seat-1", "call-1"); err != nil || !ok {
		t.Fatalf("release the reserved seats: ok=%v err=%v", ok, err)
	}
	if n, err := cq.InflightProvider(ctx, "muse"); err != nil || n != 0 {
		t.Fatalf("the released seat lingers in the provider set: count=%d err=%v, want 0", n, err)
	}
	if n, err := cq.InflightModel(ctx, "muse-1"); err != nil || n != 0 {
		t.Fatalf("the released seat lingers in the model set: count=%d err=%v, want 0", n, err)
	}
	if n, err := cq.InflightKey(ctx, "seat-1"); err != nil || n != 0 {
		t.Fatalf("the released seat lingers in the key set: count=%d err=%v, want 0", n, err)
	}
	if ok, err := cq.Reserve(ctx, "muse", "muse-1", "seat-1", 40, 40, 1, bound, "call-5"); err != nil || !ok {
		t.Fatalf("the released scopes did not admit the next call: ok=%v err=%v", ok, err)
	}
}
