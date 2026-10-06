package friend

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rig is one daemon over bus's Fake, a fake harness and a clock that moves
// one second per read: no socket, no real time. The loop runs until
// stopAfter steps (a step is one beat) or the test cancels it. Pause, which
// the loop calls while a turn runs, waits for that turn to end (gate, sent
// once the turn's result is queued, so the loop's next look finds it), so
// every run is the same sequence of steps; with hold set the turn runs on
// until the releaseAt-th pause.
type rig struct {
	mu        sync.Mutex
	store     *bustest.Fake
	bus       *bus.Bus
	now       time.Time
	delivered []string
	hold      chan struct{} // when set, a delivery blocks until it is closed
	releaseAt int
	pauses    int
	gate      chan struct{}
	owed      int  // turns delivered whose gate token is not yet sent
	passive   bool // no worker: a pause returns at once
	exit      int
	beats     int
	actives   []time.Time // what each beat carried as the session's last activity
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
	r := &rig{store: bustest.NewFake(t0, "ada", "bob"), now: t0, stopAfter: 1 << 20, gate: make(chan struct{}, 1), at: map[int]func(){}}
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
		Beat: func(_ context.Context, active time.Time) error {
			r.mu.Lock()
			r.beats++
			r.actives = append(r.actives, active)
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
	r.d.turnEnded = func() {
		r.mu.Lock()
		owed := r.owed > 0
		if owed {
			r.owed--
		}
		r.mu.Unlock()
		if owed {
			r.gate <- struct{}{}
		}
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
	r.mu.Lock()
	r.owed++ // the token goes once the result is queued (turnEnded), never before
	r.mu.Unlock()
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

// A wake check is answered by the session, never by the daemon
// (docs/SPEC-FRIEND.md, session-pong.w1; tla/Friend.tla, WakeTurn). A PING
// carrying wake=1 is answered by the daemon at once as any ping is, and,
// the session being free, the exact pong line is pushed in as its own short
// turn; mid-turn it rides at the head of the next turn as today. The
// daemon's pong alone leaves the friend challenged, then deaf (the finding
// of 2026-10-04: every daemon ponged while a session sat idle from 2:40 to
// 4:34 PM).
func TestWakeCheckIsAnsweredOnlyByTheSession(t *testing.T) {
	t.Parallel()
	pongCommand := func(nonce string) string {
		return "/opt/nova/bin/nova-friend pong --as bob --nonce " + nonce + " --dir /w/bob --redis store:6379"
	}
	wakePing := func(nonce string) string {
		text := WakePingText("ada", t0, nonce)
		require.True(t, IsWake(text))
		got, _, _, ok := ParsePing(text)
		require.True(t, ok && got == nonce, "a wake ping is a ping")
		return text
	}

	t.Run("an idle session gets the pong line as its own turn, and a daemon pong alone is deaf", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		r.d.PongCommand = pongCommand
		r.send(t, "ada", "PING w1", wakePing("w1"))
		var afterWake Status
		r.at[4] = func() { afterWake = r.last() }
		r.run(t, int(Window/BeatEvery)+6)
		assert.Equal(t, []string{"daemon-pong: daemon-pong w1"}, r.adaGot(t), "the daemon answers at once, as for any ping")
		require.Len(t, r.delivered, 1, "one wake turn, pushed once: %v", r.delivered)
		assert.Equal(t, WakeTurnText(pongCommand("w1")), r.delivered[0], "the turn holds only the exact pong line")
		assert.NotContains(t, r.delivered[0], "PING", "the ping itself is never a turn")
		assert.Equal(t, Challenged, afterWake.Challenge, "the daemon's pong ends nothing")
		assert.False(t, afterWake.LastDaemonPong.IsZero(), "status keeps the daemon's pong apart")
		assert.True(t, afterWake.LastPong.IsZero(), "and the session's: none yet")
		s := r.last()
		assert.Equal(t, Deaf, s.Challenge, "no session pong within the window is deaf, whatever the daemon said")
		assert.Equal(t, 0, s.Pongs)
		pending, fresh := r.pending(t)
		assert.Empty(t, pending, "the ping was acked by the daemon")
		assert.Empty(t, fresh)
	})

	t.Run("the session's pong ends the wake challenge", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		r.d.PongCommand = pongCommand
		r.send(t, "ada", "PING w1", wakePing("w1"))
		r.at[4] = func() { // the session ran the line its wake turn held
			r.mu.Lock()
			r.pong, r.pongSet = Pong{Nonce: "w1", At: r.now, To: "ada"}, true
			r.mu.Unlock()
		}
		r.run(t, 8)
		require.Len(t, r.delivered, 1)
		s := r.last()
		assert.Equal(t, Quiet, s.Challenge)
		assert.Equal(t, 1, s.Pongs)
		assert.False(t, s.LastPong.IsZero())
		assert.False(t, s.LastDaemonPong.IsZero())
	})

	t.Run("mid-turn the line rides at the head of the next turn", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		r.d.PongCommand = pongCommand
		r.hold, r.releaseAt = make(chan struct{}), 5
		r.send(t, "ada", "work", "the first thing")
		var second bus.Message
		r.at[2] = func() {
			r.send(t, "ada", "PING w1", wakePing("w1"))
			second = r.send(t, "ada", "more", "the second thing")
		}
		r.run(t, 14)
		assert.Equal(t, []string{"daemon-pong: daemon-pong w1"}, r.adaGot(t), "answered at once by the daemon, mid-turn")
		require.Len(t, r.delivered, 2, "no wake turn of its own while the next turn carries the line: %v", r.delivered)
		assert.NotContains(t, r.delivered[0], "nova-friend pong", "the running turn began before the ping")
		assert.True(t, strings.HasPrefix(r.delivered[1], "Run this now, first, exactly as written: "+pongCommand("w1")+"\n"), r.delivered[1])
		assert.Contains(t, r.delivered[1], "[1/1] "+second.ID+" from=ada at="+second.At.Format(time.RFC3339)+" age=0m subject=more\nthe second thing\n")
		assert.Equal(t, Challenged, r.last().Challenge)
	})

	t.Run("a plain ping is never pushed in", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		r.d.PongCommand = pongCommand
		r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
		assert.False(t, IsWake(PingText("ada", t0, "n1")))
		r.run(t, 6)
		assert.Empty(t, r.delivered)
		assert.Equal(t, []string{"daemon-pong: daemon-pong n1"}, r.adaGot(t))
	})
}

// TestStatusSaysTheChallengeIsAnsweredTheSecondAfterAPongIsWritten verifies that when
// the session writes a pong file, the daemon reads it on the next tick and the
// status file shows the challenge as answered with pongs incremented.
func TestStatusSaysTheChallengeIsAnsweredTheSecondAfterAPongIsWritten(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	// Send a ping to start a challenge
	r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
	// On the next beat (step 2), set up the pong so it gets read
	r.at[2] = func() {
		r.mu.Lock()
		r.pong, r.pongSet = Pong{Nonce: "n1", At: r.now, To: "ada"}, true
		r.mu.Unlock()
	}
	r.run(t, 5)
	s := r.last()
	assert.Equal(t, Quiet, s.Challenge, "the pong for the current nonce should end the challenge")
	assert.Equal(t, 1, s.Pongs, "the pongs count should be incremented")
}

// head is a message's line in an envelope (docs/SPEC-FRIEND.md, the loop).
func head(i, n int, m bus.Message, age int) string {
	return fmt.Sprintf("[%d/%d] %s from=%s at=%s age=%dm subject=%s\n", i, n, m.ID, m.From, m.At.Format(time.RFC3339), age, m.Subject)
}

// Three messages that land during a six-minute turn go in as the next one
// turn, oldest first, each with its id, from, time, age and subject, then
// its body; that turn's exit 0 acks all three.
func TestATurnEndDeliversEveryPendingMessageAsOneTurnOldestFirst(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.hold, r.releaseAt = make(chan struct{}), 30
	r.send(t, "ada", "long", "a long task")
	var msgs []bus.Message
	for i, step := range []int{3, 5, 7} {
		r.at[step] = func() {
			r.store.Advance(2 * time.Minute)
			r.mu.Lock()
			r.now = r.now.Add(2 * time.Minute) // the turn runs six minutes in all
			r.mu.Unlock()
			msgs = append(msgs, r.send(t, "ada", fmt.Sprintf("news %d", i+1), fmt.Sprintf("body %d", i+1)))
		}
	}
	r.run(t, 40)
	require.Len(t, r.delivered, 2, "the long task, then one turn for the three that waited: %v", r.delivered)
	text := r.delivered[1]
	assert.Contains(t, text, "nova-friend: 3 message(s) for you, oldest first, in one turn; take each in order.\n")
	at := -1
	for i, m := range msgs {
		line := regexp.QuoteMeta(fmt.Sprintf("[%d/3] %s from=ada at=%s age=", i+1, m.ID, m.At.Format(time.RFC3339))) + `\d+m` + regexp.QuoteMeta(fmt.Sprintf(" subject=news %d\nbody %d\n", i+1, i+1))
		loc := regexp.MustCompile(line).FindStringIndex(text)
		require.NotNil(t, loc, "message %d whole, with its line: %q", i+1, text)
		assert.Greater(t, loc[0], at, "oldest first")
		at = loc[0]
	}
	assert.Contains(t, text, "age=4m subject=news 1\n", "the oldest waited four minutes when the turn ended")
	pending, fresh, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Empty(t, pending, "all three acked on exit 0")
	assert.Empty(t, fresh)
	assert.Equal(t, 4, r.last().Delivered)
	assert.Equal(t, 3, r.last().Envelope, "the status names the envelope's size")
	assert.Equal(t, len(text), r.last().EnvelopeBytes)
}

// A turn that fails acks none of the messages its envelope carried.
func TestAFailedEnvelopeAcksNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.exit = 1
	for i := 0; i < 3; i++ {
		r.send(t, "ada", "m", "x")
	}
	r.run(t, 4)
	require.Len(t, r.delivered, 1)
	pending, _, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Len(t, pending, 3, "a failed turn acks nothing")
	assert.Equal(t, 0, r.last().Delivered)
	require.NotEmpty(t, r.records)
	assert.Contains(t, r.records[0], "messages=3")
	assert.Contains(t, r.records[0], "exit=1 deliveries=1/3")
	assert.NotContains(t, r.records[0], "acked=true")
}

// Of the daemon's own notices about the coordinator not yet in a turn, only
// the newest goes in: an older one is dropped, its successor named on the
// record by id (docs/SPEC-FRIEND.md, the loop).
func TestASupersededNoticeIsDroppedAndAckedWithItsSuccessor(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	window := int(Window / BeatEvery)
	r.at[window+5] = func() { r.send(t, "ada", "PING n1", PingText("ada", t0, "n1")) } // back before the silent was said
	r.at[2*window+20] = func() { r.send(t, "ada", "news", "during the second outage") }
	r.run(t, 2*window+25)
	require.Len(t, r.delivered, 1, "the notices are never a turn alone: %v", r.delivered)
	text := r.delivered[0]
	assert.Equal(t, 1, strings.Count(text, "coordinator silent since "), "only the newest notice: %q", text)
	assert.NotContains(t, text, "coordinator back")
	all := strings.Join(r.records, "\n")
	drop := regexp.MustCompile(`notice=(notice-\d+-1) subject="coordinator silent" superseded=(notice-\d+-2) dropped=true`)
	assert.Regexp(t, drop, all, "the first silent is dropped for the back that followed it")
	assert.Equal(t, 1, strings.Count(all, "superseded="), "the back, never said, supersedes nothing more; the second silent goes in: %s", all)
	assert.Contains(t, all, `notice="coordinator silent"`, "the turn's line names the notice it carried")
}

// SupersededNotices is a function of the daemon's own notices, oldest first:
// every one but the newest maps to the newest.
func TestSupersededNoticesKeepsTheNewestOfTheDaemonsOwn(t *testing.T) {
	t.Parallel()
	owed := []Notice{
		{Push: Push{Subject: "coordinator silent"}, ID: "n1"},
		{Push: Push{Subject: "coordinator back"}, ID: "n2"},
		{Push: Push{Subject: "coordinator silent"}, ID: "n3"},
	}
	assert.Equal(t, map[string]string{"n1": "n3", "n2": "n3"}, SupersededNotices(owed))
	assert.Empty(t, SupersededNotices(owed[2:]))
	assert.Empty(t, SupersededNotices(nil))
}

// The supersede rule reads only the daemon's own notices: a message on the
// stream with a notice's subject, even from the friend's own name, is a
// message like any other, delivered and acked, never dropped.
func TestTheSupersedeRuleNeverDropsAMessageOnTheStream(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	s1 := r.send(t, "bob", "coordinator silent", "the session's own note")
	s2 := r.send(t, "bob", "coordinator silent", "and another")
	work := r.send(t, "ada", "work", "do the thing")
	r.run(t, 4)
	require.Len(t, r.delivered, 1)
	for i, m := range []bus.Message{s1, s2, work} {
		assert.Contains(t, r.delivered[0], head(i+1, 3, m, 0)+m.Body+"\n")
	}
	assert.NotContains(t, strings.Join(r.records, "\n"), "superseded=")
	pending, fresh, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Empty(t, fresh)
}

// A ping the daemon answers is acked and is never a turn: the session's
// turns are spent on work.
func TestAnAnsweredPingIsNeverATurn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
	r.run(t, 4)
	assert.Empty(t, r.delivered, "a ping is no turn")
	assert.Equal(t, []string{"daemon-pong: daemon-pong n1"}, r.adaGot(t))
	pending, fresh, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Empty(t, pending, "acked once answered")
	assert.Empty(t, fresh)
}

// Any bus line the session sends after a ping proves it alive: the challenge
// ends, and the next turn carries the work without the pong line. The
// daemon's own lines prove nothing.
func TestAnyBusLineFromTheSessionIsItsProofOfLife(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.PongCommand = func(nonce string) string { return "nova-friend pong --as bob --nonce " + nonce }
	r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
	var challenged Status
	r.at[3] = func() {
		challenged = r.last()
		r.store.Advance(time.Second)
		_, err := r.bus.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: "done", Body: "card done"})
		require.NoError(t, err)
	}
	r.at[6] = func() { r.send(t, "ada", "work", "do the thing") }
	r.run(t, 10)
	assert.Equal(t, Challenged, challenged.Challenge, "the ping opened a challenge; the daemon's own pong ended nothing")
	assert.Equal(t, Quiet, r.last().Challenge, "the session's line ended it")
	require.Len(t, r.delivered, 1)
	assert.NotContains(t, r.delivered[0], "nova-friend pong", "a session that spoke is not asked to pong")
}

// A Deliverer's text limit caps the envelope; what did not fit is named by
// id with the command that prints it, stays pending, and goes in the next
// turn.
func TestTheEnvelopeNamesWhatDidNotFit(t *testing.T) {
	t.Parallel()
	now := t0.Add(10 * time.Minute)
	msgs := []bus.Message{
		{ID: "m1", From: "ada", At: t0, Subject: "one", Body: strings.Repeat("x", 200)},
		{ID: "m2", From: "ada", At: t0.Add(time.Minute), Subject: "two", Body: "y"},
		{ID: "m3", From: "ada", At: t0.Add(2 * time.Minute), Subject: "three", Body: "z"},
	}
	text, shown := Envelope(msgs, now, "bob", 300, "", "")
	assert.Equal(t, 1, shown)
	assert.Contains(t, text, head(1, 3, msgs[0], 10)+strings.Repeat("x", 200)+"\n")
	assert.Contains(t, text, "\nand 2 more: nova-bus recv --as bob --all\n"+head(2, 3, msgs[1], 9)+head(3, 3, msgs[2], 8))
	assert.NotContains(t, text, "\ny\n")
	assert.LessOrEqual(t, len(text), 300)

	many := make([]bus.Message, 20)
	for i := range many {
		many[i] = bus.Message{ID: fmt.Sprintf("n%02d", i), From: "ada", At: t0.Add(time.Duration(i) * time.Second), Subject: "s", Body: "b"}
	}
	for _, limit := range []int{400, 500, 700, 1000} {
		text, shown = Envelope(many, now, "bob", limit, "", "")
		assert.LessOrEqual(t, len(text), limit, "limit %d, shown %d", limit, shown)
		assert.Less(t, shown, len(many))
		assert.Contains(t, text, fmt.Sprintf("and %d more: nova-bus recv --as bob --all\n", len(many)-shown))
	}

	text, shown = Envelope(msgs, now, "bob", 0, "", "")
	assert.Equal(t, 3, shown, "no limit: every message")
	assert.NotContains(t, text, "more:")

	r := newRig(t)
	r.d.Deliver = limited{r, 800}
	for _, m := range msgs {
		r.send(t, m.From, m.Subject, m.Body+strings.Repeat("w", 100))
	}
	r.run(t, 8)
	require.Len(t, r.delivered, 2, "what did not fit is the next turn: %v", r.delivered)
	assert.Contains(t, r.delivered[0], "and 1 more: nova-bus recv --as bob --all\n")
	pending, fresh, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Empty(t, fresh)
	assert.Equal(t, 3, r.last().Delivered)
}

type limited struct {
	*rig
	n int
}

func (l limited) TextLimit() int { return l.n }
