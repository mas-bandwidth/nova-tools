package friend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
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
		s := r.last()
		assert.Equal(t, SessionBroken, s.Session, "the first refusal marks the session broken, not the third")
		assert.Equal(t, "dsh session session-zhi: agent preset minimal", s.SessionReason)
		said := 0
		for _, line := range r.records {
			if strings.Contains(line, `presence=down reason="dsh session session-zhi: agent preset minimal"`) {
				said++
			}
		}
		assert.Equal(t, 1, said, "recorded once, not once a refusal: %v", r.records)
	})
}

// TestDSHFakeChild is the fake dsh, a re-exec of this test binary. The parent
// sets NOVA_FRIEND_DSH_FAKE; a direct run skips. It prints the refusal or a
// clean turn and exits 0. It prints no credential.
func TestDSHFakeChild(t *testing.T) {
	mode := os.Getenv("NOVA_FRIEND_DSH_FAKE")
	if mode == "" {
		t.Skip("spawned by TestDSHRefusalOnExitZeroMarksTheFriendDownWithTheReason")
	}
	session := os.Getenv("NOVA_FRIEND_DSH_SESSION")
	switch mode {
	case "preset":
		fmt.Printf("dsh: session %q runs under agent preset \"minimal\", which the one-shot runner does not compose\n", session)
	case "missing":
		fmt.Println("MISSING_CREDENTIAL")
	default:
		fmt.Println("ok")
	}
	_ = os.Stdout.Sync()
	os.Exit(0)
}

// fakeDSH is this test binary standing in for dsh. mode is read on each
// turn, so a later turn can succeed. The process exits 0; the refusal is
// in what it prints.
func fakeDSH(t *testing.T, mode *atomic.Value) Exec {
	t.Helper()
	bin, err := os.Executable()
	require.NoError(t, err)
	return func(ctx context.Context, dir, _ string, args []string, stdin string) (string, int, error) {
		session := ""
		for i, a := range args {
			if a == "--session-id" && i+1 < len(args) {
				session = args[i+1]
			}
		}
		m, _ := mode.Load().(string)
		cmd := exec.CommandContext(ctx, bin, "-test.run=^TestDSHFakeChild$", "-test.count=1", "-test.timeout=20s")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "NOVA_FRIEND_DSH_FAKE="+m, "NOVA_FRIEND_DSH_SESSION="+session)
		cmd.Stdin = strings.NewReader(stdin)
		cmd.WaitDelay = 5 * time.Second
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		out := stdout.String() + stderr.String()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out, exitErr.ExitCode(), nil
		}
		return out, 0, err
	}
}

// A dsh headless turn that prints the agent-preset refusal or
// MISSING_CREDENTIAL and exits 0 is a failed delivery: after one turn the
// session is broken, the friend's row reads down with the reason, and every
// message is still pending. The down line is recorded once. A later turn
// that succeeds clears it (docs/SPEC-FRIEND.md, a dsh turn the session
// cannot take).
func TestDSHRefusalOnExitZeroMarksTheFriendDownWithTheReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, mode, reason string
	}{
		{"agent preset", "preset", "dsh session session-zhi: agent preset minimal"},
		{"missing credential", "missing", "dsh: missing credential"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			var mode atomic.Value
			mode.Store(tc.mode)
			var calls atomic.Int32
			run := fakeDSH(t, &mode)
			dsh := &DSH{Dir: r.d.Dir, Session: "session-zhi", Program: "dsh", Run: func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
				calls.Add(1)
				return run(ctx, dir, name, args, stdin)
			}}
			// The rig's pause waits on a gate token. This deliverer counts one,
			// or the loop waits forever. A deaf turn leaves the batch in
			// hand, so later pauses have no token until the recheck: wait
			// only while a delivery has counted itself. The short grace is
			// the turn goroutine entering Deliver; a recheck sends nothing.
			r.d.Deliver = deliverFunc(func(ctx context.Context, text string) (int, error) {
				r.mu.Lock()
				r.owed++
				r.mu.Unlock()
				return dsh.Deliver(ctx, text)
			})
			r.d.Pause = func(ctx context.Context, _ time.Duration) {
				r.mu.Lock()
				live := r.owed > 0
				r.mu.Unlock()
				if live {
					select {
					case <-r.gate:
					case <-ctx.Done():
					}
					return
				}
				wait := time.NewTimer(100 * time.Millisecond)
				defer wait.Stop()
				select {
				case <-r.gate:
				case <-ctx.Done():
				case <-wait.C:
				}
			}
			r.send(t, "ada", "hello", "one")
			r.send(t, "ada", "again", "two")
			var down Status
			sawDown := false
			status := r.d.Status
			r.d.Status = func(s Status) error {
				err := status(s)
				if !sawDown && s.Session == SessionBroken {
					sawDown = true
					down = s
					pending, fresh, perr := r.bus.Peek(context.Background(), "bob")
					assert.NoError(t, perr)
					assert.Len(t, pending, 2, "both messages stay pending")
					assert.Empty(t, fresh)
					v := FriendStatus(Evidence{LastAnswer: s.At, BusBlocked: "session broken", Undelivered: len(pending)}, s.At, AnswerBound, time.UTC)
					assert.Equal(t, "down", v.Status, "the row reads down while the session cannot take a turn")
					assert.NotEqual(t, "up", v.Status)
					mode.Store("ok")
				}
				return err
			}
			r.run(t, 40)
			require.True(t, sawDown, "one refusal was enough to mark the session broken: %v", r.records)
			assert.Equal(t, SessionBroken, down.Session)
			assert.Equal(t, "session-zhi", down.SessionID)
			assert.Equal(t, tc.reason, down.SessionReason)
			assert.GreaterOrEqual(t, calls.Load(), int32(2), "a later turn still runs, so a success can clear it")
			s := r.last()
			assert.Equal(t, SessionOK, s.Session, "a turn that succeeds clears the broken mark")
			assert.Empty(t, s.SessionReason)
			pending, _, err := r.bus.Peek(context.Background(), "bob")
			require.NoError(t, err)
			assert.Empty(t, pending, "the successful turn acks the messages")
			said := 0
			for _, line := range r.records {
				if strings.Contains(line, "presence=down reason="+fmt.Sprintf("%q", tc.reason)) {
					said++
				}
				assert.NotContains(t, line, "given_up")
			}
			assert.Equal(t, 1, said, "recorded once across the retries: %v", r.records)
			assert.Contains(t, strings.Join(r.records, "\n"), "presence=up")
		})
	}
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
		assert.Contains(t, r.delivered[1], Text(second))
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
