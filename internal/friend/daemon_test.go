package friend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
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
		Deliver:    r,
		SilentStop: DefaultSilentStop,
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

// deferrer is a harness whose session cannot take a turn now and nothing is
// wrong (the Codex chat open in the app): every delivery is Deferred. It
// ends the run at the thousandth.
type deferrer struct {
	now  func() time.Time
	at   []time.Time
	mu   sync.Mutex
	n    int
	stop func()
}

func (d *deferrer) Deliver(context.Context, string) (int, error) {
	d.mu.Lock()
	d.n++
	d.at = append(d.at, d.now())
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
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		def := &deferrer{stop: func() { r.cancel() }, now: func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }}
		r.d.Deliver, r.passive = def, true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.send(t, "ada", "hello", "x")
		r.run(t, 1000*int(RecheckEvery/BeatEvery)*4) // the ceiling, never reached: the thousandth deferral ends the run
		assert.Equal(t, 1000, def.n, "handed in a thousand times")
		for i := 1; i < len(def.at); i++ {
			assert.GreaterOrEqual(t, def.at[i].Sub(def.at[i-1]), RecheckEvery, "retry gap %d", i)
		}
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
	})
}

// The DSH adapter over a session under an agent preset, through the daemon:
// every delivery is refused by the one-shot runner, the message stays in
// hand and is never given up (the finding of 2026-10-04: Zhi's session runs
// preset "minimal", and the third refusal would have acked her message).
func TestADSHSessionUnderAPresetKeepsTheMessagePending(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		var mu sync.Mutex
		calls := 0
		refuse := func(context.Context, string, string, []string, string) (string, int, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 10 {
				r.cancel()
			}
			return `dsh: session "session-zhi" runs under agent preset "minimal", which the one-shot runner does not compose` + "\n", 1, nil
		}
		r.d.Deliver, r.passive = &DSH{Dir: "/w/zhi", Session: "session-zhi", Run: refuse, Program: "dsh"}, true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.send(t, "ada", "hello", "x")
		r.run(t, 10*int(RecheckEvery/BeatEvery)*4) // the ceiling, never reached: the tenth refusal ends the run
		assert.Equal(t, 10, calls, "handed in again after each refusal, beyond the three a failure gets")
		pending, fresh, err := r.bus.Peek(context.Background(), "bob")
		require.NoError(t, err)
		assert.Len(t, pending, 1, "still in hand: never acked, never given up")
		assert.Empty(t, fresh)
		for _, line := range r.records {
			assert.NotContains(t, line, "given_up")
			assert.NotContains(t, line, "acked")
		}
		require.NotEmpty(t, r.records)
		assert.Contains(t, r.records[0], `subject="hello" deferred=1: session session-zhi runs under agent preset "minimal"`)
	})
}

// printer is a harness whose first turn prints when the test says and ends
// when the test releases it (or the daemon stops it); later turns end at
// once, exit 0.
type printer struct {
	mu    sync.Mutex
	calls int
	seen  func()
	done  chan struct{}
}

func (p *printer) Deliver(ctx context.Context, _ string) (int, error) {
	p.mu.Lock()
	p.calls++
	first := p.calls == 1
	if first {
		p.seen, _ = ctx.Value(outputKey{}).(func())
	}
	p.mu.Unlock()
	if !first {
		return 0, nil
	}
	select {
	case <-p.done:
	case <-ctx.Done():
	}
	return 0, nil
}

func (p *printer) print() {
	p.mu.Lock()
	seen := p.seen
	p.mu.Unlock()
	if seen != nil {
		seen()
	}
}

func (p *printer) callsN() int { p.mu.Lock(); defer p.mu.Unlock(); return p.calls }

// A turn that keeps working is never stopped: output every minute for
// forty-five minutes runs on past any window (SPEC-FRIEND.md, the loop: no
// clock bounds a turn that prints), and the message is acked at exit 0.
func TestATurnThatKeepsWorkingIsNeverStopped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		p := &printer{done: make(chan struct{})}
		r.d.Deliver, r.passive = p, true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.send(t, "ada", "long work", "keep going")
		minute := int(time.Minute / BeatEvery)
		for m := 1; m <= 45; m++ {
			r.at[m*minute] = func() { p.print() }
		}
		r.at[46*minute] = func() { close(p.done) }
		r.run(t, 50*minute)
		assert.Equal(t, 1, p.callsN(), "one turn, never stopped and never restarted")
		var ended string
		for _, line := range r.records {
			assert.NotContains(t, line, "stopping:", "a turn that prints is never stopped: %v", r.records)
			assert.NotContains(t, line, "stopped=")
			if strings.Contains(line, `subject="long work"`) && strings.Contains(line, "exit=") {
				ended = line
			}
		}
		require.NotEmpty(t, ended, "%v", r.records)
		assert.Contains(t, ended, "exit=0")
		assert.Contains(t, ended, "acked=true")
		assert.Equal(t, 1, r.last().Delivered)
	})
}

// silentTurn is a harness whose every turn prints nothing and ends only when
// the daemon stops it.
type silentTurn struct {
	mu        sync.Mutex
	calls     int
	stoppedAt time.Time
	now       func() time.Time
}

func (s *silentTurn) Deliver(ctx context.Context, _ string) (int, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	<-ctx.Done()
	s.mu.Lock()
	if s.stoppedAt.IsZero() {
		s.stoppedAt = s.now()
	}
	s.mu.Unlock()
	return -1, errors.New("the delivery was stopped with its process group")
}

func (s *silentTurn) callsN() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls }

// A turn silent for the window is stopped once: the record says no output
// for the window, the group is signalled once, and the message stays pending
// (SPEC-FRIEND.md, the loop: a turn that has printed nothing for
// --silent-stop is stopped, its process group signalled).
func TestATurnSilentForTheWindowIsStopped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		s := &silentTurn{now: func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }}
		r.d.Deliver, r.passive = s, true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.send(t, "ada", "silent", "no output at all")
		minute := int(time.Minute / BeatEvery)
		r.run(t, 23*minute)
		require.False(t, s.stoppedAt.IsZero(), "the silent turn was stopped")
		assert.False(t, s.stoppedAt.Before(t0.Add(DefaultSilentStop)), "never before %s of silence: stopped at %s", DefaultSilentStop, s.stoppedAt.Sub(t0))
		assert.True(t, s.stoppedAt.Before(t0.Add(DefaultSilentStop+5*BeatEvery)), "and at once after: stopped at %s", s.stoppedAt.Sub(t0))
		stops := 0
		var ended string
		for _, line := range r.records {
			if strings.Contains(line, "stopping: no output for 20m0s") {
				stops++
			}
			if strings.Contains(line, `subject="silent"`) && strings.Contains(line, "exit=") {
				ended = line
			}
		}
		assert.Equal(t, 1, stops, "the group is signalled once: %v", r.records)
		require.NotEmpty(t, ended, "%v", r.records)
		assert.Contains(t, ended, `stopped="no output for 20m0s"`)
		assert.Contains(t, ended, "deliveries=1/3", "a stopped turn is a failed delivery")
		assert.Equal(t, 0, r.last().Delivered, "nothing acked")
		pending, fresh, err := r.bus.Peek(context.Background(), "bob")
		require.NoError(t, err)
		assert.Len(t, pending, 1, "the message stays pending")
		assert.Empty(t, fresh)
	})
}

// One turn at a time per session: a message that lands while a delivery runs
// is not delivered until that delivery ends (SPEC-FRIEND.md, the loop: while
// a turn runs, never a second turn).
func TestNoSecondTurnWhileOneRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		p := &printer{done: make(chan struct{})}
		r.d.Deliver, r.passive = p, true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.send(t, "ada", "first", "one")
		r.at[10] = func() { r.send(t, "ada", "second", "two") }
		var mid int
		r.at[50] = func() { mid = p.callsN() }
		r.at[80] = func() { close(p.done) }
		r.run(t, 100)
		assert.Equal(t, 1, mid, "the second message waits while the first delivery runs")
		assert.Equal(t, 2, p.callsN(), "it goes in once the first ends")
		pending, fresh, err := r.bus.Peek(context.Background(), "bob")
		require.NoError(t, err)
		assert.Empty(t, pending)
		assert.Empty(t, fresh)
	})
}

// busyTurn is a harness that can tell whether its session is busy (Busier):
// its first turn runs until the daemon stops it; the test holds whether the
// session is busy and counts the asks.
type busyTurn struct {
	mu    sync.Mutex
	calls int
	asks  int
	busy  bool
	texts []string
}

func (b *busyTurn) Deliver(ctx context.Context, text string) (int, error) {
	b.mu.Lock()
	b.calls++
	b.texts = append(b.texts, text)
	first := b.calls == 1
	b.mu.Unlock()
	if !first {
		return 0, nil
	}
	<-ctx.Done()
	b.mu.Lock()
	defer b.mu.Unlock()
	return -1, errors.New("the delivery was stopped with its process group")
}

func (b *busyTurn) Busy(context.Context) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.asks++
	return b.busy, nil
}

func (b *busyTurn) setBusy(v bool) { b.mu.Lock(); b.busy = v; b.mu.Unlock() }
func (b *busyTurn) callsN() int    { b.mu.Lock(); defer b.mu.Unlock(); return b.calls }
func (b *busyTurn) asksN() int     { b.mu.Lock(); defer b.mu.Unlock(); return b.asks }

// After a stopped delivery the daemon delivers into the session again only
// once it is free: while the adapter answers Busy the next delivery defers,
// said on the record and counted toward nothing, and once it answers free
// the waiting messages go in as one turn (SPEC-FRIEND.md, the loop: one turn
// at a time; a stopped process whose harness keeps the turn running never
// gets a second turn beside it).
func TestNoRedeliveryWhileTheSessionIsBusy(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := &busyTurn{busy: true}
		r.d.Deliver, r.passive = b, true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.send(t, "ada", "first", "stopped mid-turn")
		r.at[5] = func() { r.send(t, "ada", "second", "waiting") }
		minute := int(time.Minute / BeatEvery)
		r.at[16*minute] = func() { r.store.Advance(bus.ClaimAfter) } // the stopped message's claim opens before the stop
		var midCalls, midAsks int
		r.at[24*minute] = func() { midCalls, midAsks = b.callsN(), b.asksN() }
		r.at[25*minute] = func() { b.setBusy(false) }
		r.run(t, 27*minute)
		assert.Equal(t, 1, midCalls, "no redelivery while the session is busy")
		assert.GreaterOrEqual(t, midAsks, 2, "the daemon asked, and asked again while the answer stayed busy")
		b.mu.Lock()
		texts := append([]string(nil), b.texts...)
		b.mu.Unlock()
		require.Len(t, texts, 2, "once free, the waiting messages go in: %v", texts)
		assert.Contains(t, texts[1], "2 message(s) for you")
		assert.Contains(t, texts[1], `subject="first"`)
		assert.Contains(t, texts[1], `subject="second"`)
		assert.Equal(t, 2, b.callsN())
		said := 0
		for _, line := range r.records {
			if strings.Contains(line, "the session is busy") {
				said++
			}
		}
		assert.GreaterOrEqual(t, said, 2, "the deferral is said on the record")
		pending, fresh, err := r.bus.Peek(context.Background(), "bob")
		require.NoError(t, err)
		assert.Empty(t, pending, "both messages acked at exit 0")
		assert.Empty(t, fresh)
	})
}

// Zero never stops: a turn that never prints runs on past any window when
// the window is zero (SPEC-FRIEND.md, the loop: --silent-stop 0 never stops).
func TestNoProgressZeroNeverStops(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		r.d.SilentStop = 0
		p := &printer{done: make(chan struct{})}
		r.d.Deliver, r.passive = p, true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.send(t, "ada", "silent with zero", "no output, no stop")
		minute := int(time.Minute / BeatEvery)
		r.at[30*minute] = func() { close(p.done) }
		r.run(t, 32*minute)
		assert.Equal(t, 1, p.callsN(), "the turn ran on and ended only when its work ended")
		for _, line := range r.records {
			assert.NotContains(t, line, "stopping:", "zero never stops: %v", r.records)
			assert.NotContains(t, line, "stopped=")
		}
		var ended string
		for _, line := range r.records {
			if strings.Contains(line, `subject="silent with zero"`) && strings.Contains(line, "exit=") {
				ended = line
			}
		}
		require.NotEmpty(t, ended, "%v", r.records)
		assert.Contains(t, ended, "exit=0")
		assert.Contains(t, ended, "acked=true")
		assert.Equal(t, 1, r.last().Delivered)
	})
}
