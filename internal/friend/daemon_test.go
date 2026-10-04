package friend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rig is one daemon over bus2's Fake, a fake harness and a clock that moves
// one second per read: no socket, no real time. The loop runs until
// stopAfter steps (a step is one beat) or the test cancels it. Pause, which
// the loop calls while a turn runs, waits for that turn to end (gate), so
// every run is the same sequence of steps; with hold set the turn runs on
// until the releaseAt-th pause.
type rig struct {
	mu        sync.Mutex
	store     *bus2.Fake
	bus       *bus2.Bus
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
	r := &rig{store: bus2.NewFake(t0, "ada", "bob"), now: t0, stopAfter: 1 << 20, gate: make(chan struct{}, 1), at: map[int]func(){}}
	r.bus = &bus2.Bus{Store: r.store}
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

func (r *rig) send(t *testing.T, from, subject, body string) bus2.Message {
	t.Helper()
	m, err := r.bus.Send(context.Background(), bus2.Message{From: from, To: []string{"bob"}, Subject: subject, Body: body})
	require.NoError(t, err)
	return m
}

func (r *rig) adaGot(t *testing.T) []string {
	t.Helper()
	got, err := r.store.Range(context.Background(), bus2.StreamOf("ada"), "-", "+", 100)
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
	assert.Equal(t, Text(m), r.delivered[0], "the session reads what nova-bus2 recv prints; a plain message carries no pong line")
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
	// a stale pong file (an older nonce, or one from before the ask) does not answer the next
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
	r.hold, r.releaseAt = make(chan struct{}), 3
	r.send(t, "ada", "long", "a long task")
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.at[2] = func() { r.send(t, "ada", "PING n1", PingText("ada", t0, "n1")) } // the turn is under way; a ping lands
	r.stopAfter = 9
	require.NoError(t, r.d.Run(ctx))
	assert.Equal(t, []string{"daemon-pong: daemon-pong n1"}, r.adaGot(t), "answered from a peek while the turn ran, and once only")
	require.Len(t, r.delivered, 2, "the long task, then the ping as a turn once the session was free")
	assert.Contains(t, r.delivered[1], "PING n1")
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
	assert.Len(t, fresh, 2, "both messages wait for the session's own nova-bus2 recv")
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
