package friend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rig is one daemon over bus's Fake, a fake harness and a clock that moves
// one second per read: no socket, no real time. The loop runs until
// stopAfter steps (a step is one beat) or the test cancels it. Pause, which
// the loop calls while a turn runs, waits for that turn to end (gate), so
// every run is the same sequence of steps; with hold set the turn runs on
// until the releaseAt-th pause.
type rig struct {
	mu        sync.Mutex
	store     *bus.Fake
	bus       *bus.Bus
	now       time.Time
	delivered []string
	hold      chan struct{} // when set, a delivery blocks until it is closed
	releaseAt int
	pauses    int
	gate      chan struct{}
	passive   bool // no worker: a pause returns at once
	exit      int
	beats     int
	beatErr   error
	pong      Pong
	pongSet   bool
	status    []Status
	records   []string
	cancel    context.CancelFunc
	stopAfter int
	at        map[int]func() // what happens at a step, from the beat
	d         *Daemon
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{store: bus.NewFake(t0, "ada", "bob"), now: t0, stopAfter: 1 << 20, gate: make(chan struct{}, 1), at: map[int]func(){}}
	r.bus = &bus.Bus{Store: r.store}
	r.d = &Daemon{
		Friend: "bob", Harness: "fake", Dir: t.TempDir(), Width: 4, Store: r.store,
		Deliver: r,
		Now: func() time.Time {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.now = r.now.Add(BeatEvery)
			return r.now
		},
		Pause: func(context.Context, time.Duration) {
			r.mu.Lock()
			r.pauses++
			hold, release, passive := r.hold, r.pauses == r.releaseAt, r.passive
			if release {
				close(hold)
				r.hold = nil
			}
			r.mu.Unlock()
			if passive || (hold != nil && !release) {
				return // no turn to wait for, or one still running on purpose
			}
			<-r.gate
		},
		Beat: func(context.Context) error {
			r.mu.Lock()
			r.beats++
			f, stop := r.at[r.beats], r.beats >= r.stopAfter
			r.mu.Unlock()
			if f != nil {
				f()
			}
			if stop {
				r.cancel()
			}
			return r.beatErr
		},
		Record: func(line string) { r.mu.Lock(); r.records = append(r.records, line); r.mu.Unlock() },
		Pong: func() (Pong, bool, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.pong, r.pongSet, nil
		},
		Status: func(s Status) error { r.mu.Lock(); r.status = append(r.status, s); r.mu.Unlock(); return nil },
	}
	return r
}

func (r *rig) Deliver(ctx context.Context, text string) (int, error) {
	r.mu.Lock()
	r.delivered = append(r.delivered, text)
	hold := r.hold
	r.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
		}
	}
	r.gate <- struct{}{}
	return r.exit, nil
}

func (r *rig) run(t *testing.T, steps int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.stopAfter = steps
	require.NoError(t, r.d.Run(ctx))
}

func (r *rig) send(t *testing.T, from, subject, body string) bus.Message {
	t.Helper()
	m, err := r.bus.Send(context.Background(), bus.Message{From: from, To: []string{"bob"}, Subject: subject, Body: body})
	require.NoError(t, err)
	return m
}

func (r *rig) adaGot(t *testing.T) []string {
	t.Helper()
	got, err := r.store.Range(context.Background(), bus.StreamOf("ada"), "-", "+", 100)
	require.NoError(t, err)
	var out []string
	for _, e := range got {
		m := e.Message()
		out = append(out, m.Subject+": "+strings.TrimSpace(m.Body))
	}
	return out
}

func (r *rig) last() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status[len(r.status)-1]
}

func TestAMessageIsPushedIntoTheSessionAndAckedWhenTheTurnEndsAtZero(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	m := r.send(t, "ada", "hello", "are you there?")
	r.run(t, 4)
	require.Len(t, r.delivered, 1)
	assert.Equal(t, Text(m), r.delivered[0], "the session reads what nova-bus recv prints; a plain message carries no pong line")
	pending, fresh, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Empty(t, fresh)
	assert.Equal(t, 4, r.beats, "one beat per step while the store answers")
	s := r.last()
	assert.Equal(t, 1, s.Delivered)
	assert.Equal(t, Connected, s.Connection)
	assert.Equal(t, Quiet, s.Challenge)
	assert.Equal(t, "bob", s.Friend)
	assert.Equal(t, 4, s.Width)
	require.NotEmpty(t, r.records)
	assert.Contains(t, r.records[0], `subject="hello"`)
	assert.Contains(t, r.records[0], "acked=true")
}

func TestATurnThatExitsNonZeroLeavesTheMessagePending(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.exit = 3
	r.send(t, "ada", "hello", "x")
	r.run(t, 4)
	pending, _, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Len(t, pending, 1, "exit 3 acks nothing")
	assert.Contains(t, r.records[0], "deliveries=1/3")
	assert.Equal(t, 0, r.last().Delivered)
	assert.Contains(t, r.records[0], "exit=3")
}

func TestAPingIsAnsweredAtOnceByTheDaemonAndPushedInAndThePongEndsTheChallenge(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ping := r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
	r.d.PongCommand = func(nonce string) string {
		return "/opt/nova/bin/nova-friend pong --as bob --nonce " + nonce + " --dir /w/bob --redis store:6379"
	}
	var afterPing, afterPong Status
	r.at[3] = func() { afterPing = r.last() }
	r.at[4] = func() { // the session answers: the pong verb wrote the pong file
		r.mu.Lock()
		r.pong, r.pongSet = Pong{Nonce: "n1", At: r.now, To: "ada"}, true
		r.mu.Unlock()
	}
	r.at[6] = func() {
		afterPong = r.last()
		r.send(t, "ada", "PING n2", PingText("ada", t0, "n2"))
	}
	r.run(t, 9)
	assert.Equal(t, []string{"daemon-pong: daemon-pong n1", "daemon-pong: daemon-pong n2"}, r.adaGot(t), "the daemon pong goes to the coordinator's stream, before the turn")
	require.Len(t, r.delivered, 2)
	assert.Contains(t, r.delivered[0], ping.ID)
	assert.True(t, strings.HasPrefix(r.delivered[0], "Run this now, first, exactly as written: /opt/nova/bin/nova-friend pong --as bob --nonce n1 --dir /w/bob --redis store:6379\nThen read on.\n\nRECV OK id="), r.delivered[0])
	assert.Contains(t, r.delivered[1], "--nonce n2 --dir /w/bob", "the second ping carries its own line")
	assert.Equal(t, Challenged, afterPing.Challenge)
	assert.Equal(t, "ada", afterPing.Seat)
	assert.Equal(t, "n1", afterPing.Nonce)
	assert.Equal(t, Quiet, afterPong.Challenge)
	assert.Equal(t, 1, afterPong.Pongs)
	// a stale pong file (an older nonce) does not answer the next
	assert.Equal(t, Challenged, r.last().Challenge, "the file still says n1")
	assert.Equal(t, "n2", r.last().Nonce)
}

func TestNoPingForAWindowTellsTheSessionOnceAndAPingTellsItBack(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	steps := int(Window / BeatEvery)
	var silent Status
	r.at[steps+5] = func() {
		silent = r.last()
		r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
	}
	r.run(t, steps+10)
	require.Len(t, r.delivered, 3, "one push for the outage however long it lasts, then back, then the ping")
	assert.Contains(t, r.delivered[0], "coordinator silent since "+t0.Add(BeatEvery).Format(time.RFC3339))
	assert.Equal(t, Silent, silent.Connection)
	assert.Contains(t, r.delivered[1], "coordinator back: ada has the seat")
	assert.Contains(t, r.delivered[2], "PING n1")
	assert.Equal(t, Connected, r.last().Connection)
}

func TestAPingDuringALongTurnIsStillAnsweredAtOnceByTheDaemon(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	// the turn runs longer than a window; the coordinator pings twice while it does
	window := int(Window / BeatEvery)
	r.hold, r.releaseAt = make(chan struct{}), window+20
	r.send(t, "ada", "long", "a long task")
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.at[2] = func() { r.send(t, "ada", "PING n1", PingText("ada", t0, "n1")) } // the turn is under way; a ping lands
	var midTurn Status
	r.at[100] = func() {
		midTurn = r.last()
		r.send(t, "ada", "PING n2", PingText("ada", t0, "n2"))
	}
	r.stopAfter = window + 40
	require.NoError(t, r.d.Run(ctx))
	assert.Equal(t, []string{"daemon-pong: daemon-pong n1", "daemon-pong: daemon-pong n2"}, r.adaGot(t), "answered from a peek while the turn ran, and once only")
	assert.True(t, midTurn.LastPing.After(t0.Add(BeatEvery)) && midTurn.LastPing.Before(t0.Add(10*BeatEvery)), "the machine saw the ping when the daemon did, mid-turn: %s", midTurn.LastPing)
	assert.Equal(t, Challenged, midTurn.Challenge)
	require.Len(t, r.delivered, 3, "the long task, then the pings as turns once the session was free: %v", r.delivered)
	assert.Contains(t, r.delivered[1], "PING n1")
	assert.Contains(t, r.delivered[2], "PING n2")
	assert.Equal(t, Connected, r.last().Connection, "pings peeked during a long turn keep the connection: no false silence")
	pending, fresh, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Empty(t, fresh)
}

func TestAStoreThatDoesNotAnswerStopsTheBeat(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.store.Fail = errors.New("connection refused")
	paused := 0
	r.d.Pause = func(context.Context, time.Duration) {
		paused++
		if paused == 3 {
			r.cancel()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	require.NoError(t, r.d.Run(ctx))
	assert.Equal(t, 0, r.beats, "no beat while the store is down: presence is the loop")
	assert.Contains(t, r.last().StoreError, "connection refused")
}

func TestAPassiveHarnessTakesNothingOffTheStreamAndStillAnswersTheDaemonPong(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Deliver, r.d.Harness, r.passive = Stub{Harness: "claude"}, "claude", true
	r.send(t, "ada", "hello", "for the session's own read")
	r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
	r.run(t, 4)
	assert.Equal(t, []string{"daemon-pong: daemon-pong n1"}, r.adaGot(t))
	assert.Empty(t, r.delivered)
	pending, fresh, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Empty(t, pending, "nothing was taken")
	assert.Len(t, fresh, 2, "both messages wait for the session's own nova-bus recv")
	s := r.last()
	assert.Equal(t, Challenged, s.Challenge, "the machine saw the ping all the same")
	assert.Equal(t, "ada", s.Seat)
	assert.Equal(t, 4, r.beats, "the beat is real")
	assert.GreaterOrEqual(t, s.Beats, 1, "the count in the file lags up to StatusEvery")
}

func TestAPassiveHarnessRecordsAPushItCannotDeliver(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Deliver, r.d.Harness, r.passive = Stub{Harness: "codex"}, "codex", true
	r.run(t, int(Window/BeatEvery)+3)
	assert.Empty(t, r.delivered)
	require.NotEmpty(t, r.records)
	assert.Contains(t, r.records[0], "not delivered: codex has no deliver command: coordinator silent")
	assert.Equal(t, Silent, r.last().Connection)
}

func TestAStatusFileThatCannotBeWrittenIsSaidOnceAMinuteAndTheBeatGoesOn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Status = func(Status) error { return errors.New("operation not permitted") }
	r.run(t, 130)
	assert.Equal(t, 130, r.beats)
	said := 0
	for _, line := range r.records {
		if strings.Contains(line, "status: operation not permitted") {
			said++
		}
	}
	assert.Equal(t, 3, said, "at the start and once a minute: %v", r.records)
}

// The pong file's at is the store's time, to the second (bus.Send truncates
// it); the ask is the daemon's own clock. A pong recorded in the same second
// as the ask, or on a store clock a little behind, carries an at before the
// ask and is still the answer: the nonce says which challenge it answers,
// never the clock (the finding of 2026-10-04: such a pong was dropped for
// ever, and the friend stayed challenged).
func TestAPongStampedBeforeTheAskByTheStoresClockStillAnswers(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
	r.at[3] = func() {
		r.mu.Lock()
		r.pong, r.pongSet = Pong{Nonce: "n1", At: r.d.m.Asked.Add(-1500 * time.Millisecond), To: "ada"}, true
		r.mu.Unlock()
	}
	r.run(t, 6)
	s := r.last()
	assert.Equal(t, Quiet, s.Challenge, "the pong for the current nonce ends the challenge whatever its stamp")
	assert.Equal(t, 1, s.Pongs)
}

// A message whose turn fails is handed in again once its claim opens, and
// not for ever: after MaxDeliveries it is acked with the failure on the
// record (the finding of 2026-10-04: a poison message came back every
// minute for ever).
func TestAMessageThatFailsThreeTimesIsAckedAndTheRecordSaysSo(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.exit = 3
	r.send(t, "ada", "poison", "x")
	for _, step := range []int{4, 8} { // the claim opens between the failures
		r.at[step] = func() { r.store.Advance(bus.ClaimAfter) }
	}
	r.run(t, 14)
	assert.Len(t, r.delivered, MaxDeliveries, "handed in three times, then given up")
	pending, _, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Empty(t, pending, "the third failure acks it")
	failed := 0
	for _, line := range r.records {
		if strings.Contains(line, "exit=3") {
			failed++
		}
	}
	assert.Equal(t, MaxDeliveries, failed, "%v", r.records)
	assert.Contains(t, r.records[len(r.records)-1], "deliveries=3/3 given_up=true acked=true")
	assert.Equal(t, 0, r.last().Delivered, "a message given up on was never delivered")
}

// A live reader is never handed a message a second time: the bus keeps a
// delivered message with its reader for longer than the longest turn and
// the kill that ends it (the finding of 2026-10-04: a one-minute claim
// against a ten-minute turn, delivered twice).
func TestTheClaimOpensOnlyAfterTheLongestTurnIsOver(t *testing.T) {
	t.Parallel()
	assert.Less(t, DeliverBudget+KillDelay, bus.ClaimAfter)
}

// deferrer is a harness whose session cannot take a turn now and nothing is
// wrong (the Codex chat open in the app): every delivery is Deferred. It
// ends the run at the thousandth.
type deferrer struct {
	mu   sync.Mutex
	n    int
	stop func()
}

func (d *deferrer) Deliver(context.Context, string) (int, error) {
	d.mu.Lock()
	d.n++
	n := d.n
	d.mu.Unlock()
	if n == 1000 {
		d.stop()
	}
	return 0, Deferred{Reason: "the chat is open in the app"}
}

// A delivery the adapter defers is neither a failure nor an ack: the
// message stays in the daemon's hand, tried again every RecheckEvery and
// counted toward nothing, however long the session stays unable (the
// finding of 2026-10-04: a thread open in the Codex app refused three
// deliveries in a row, and the give-up rule acked the message given_up=true
// while the chat was open, which is its normal state).
func TestADeferredDeliveryIsTriedAgainAndNeverGivenUpOrAcked(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	def := &deferrer{stop: func() { r.cancel() }}
	r.d.Deliver, r.passive = def, true
	r.send(t, "ada", "hello", "x")
	r.run(t, 1000*int(RecheckEvery/BeatEvery)*4) // the ceiling, never reached: the thousandth deferral ends the run
	assert.Equal(t, 1000, def.n, "handed in a thousand times")
	pending, fresh, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Len(t, pending, 1, "still in hand: never acked, never given up")
	assert.Empty(t, fresh)
	assert.Equal(t, 0, r.last().Delivered)
	said := 0
	for _, line := range r.records {
		assert.NotContains(t, line, "given_up")
		assert.NotContains(t, line, "acked")
		assert.NotContains(t, line, "deliveries=")
		if strings.Contains(line, "deferred=") {
			said++
		}
	}
	require.NotEmpty(t, r.records)
	assert.Contains(t, r.records[0], `subject="hello" deferred=1: the chat is open in the app; tried again every 10s, counted toward nothing (said once per 1m0s)`)
	elapsed := r.now.Sub(t0)
	assert.LessOrEqual(t, said, int(elapsed/DeferredSaidEvery)+2, "said once a minute, not once a deferral: %d lines over %s", said, elapsed)
	assert.GreaterOrEqual(t, said, int(elapsed/(DeferredSaidEvery+2*RecheckEvery))-1, "and not less often than once a minute plus a recheck")
}
