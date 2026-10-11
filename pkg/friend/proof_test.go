package friend

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests that read the sprint server's own rules (sprint.ProveBeat,
// sprint.FriendEvidence) moved to internal/sprint/friend_proof_test.go (the nova-sprint
// split); the ones here need no sprint server.
//
// The findings of 2026-10-06 on the new build, one test each: the daemon knew its
// session answered and never told the sprint server, so three friends read down for
// twenty minutes with their sessions answering; a daemon whose start check was not
// answered within five minutes exited, and launchd restarted it into the same wait for
// ever; a headless dsh daemon owed its check for twenty minutes with no turn running.

// checkLine is a session check as the session reads it: its nonce.
var checkLine = regexp.MustCompile(`^` + SessionCheckPrefix + `(\S+)`)

// proofHarness is bob's harness and the session in it. Answering, the session runs a
// check's pong line at once; in a long turn, a check waits queued in the session until
// the test lets the session answer it. Headless, it keeps a turn record the test sets
// (TurnRecord): a turn running or not, whatever lock anything holds.
type proofHarness struct {
	r        *proofRig
	mu       sync.Mutex
	texts    []string
	answer   bool
	delay    time.Duration // the session answers a check this long after it went in
	headless bool
	running  bool
	since    time.Time
}

func (h *proofHarness) Deliver(_ context.Context, text string) (int, error) {
	h.mu.Lock()
	h.texts = append(h.texts, text)
	answer := h.answer
	h.mu.Unlock()
	if m := checkLine.FindStringSubmatch(text); m != nil && answer {
		if h.delay > 0 {
			h.r.later(m[1], h.delay)
		} else {
			h.r.pong(m[1])
		}
	}
	return 0, nil
}

func (h *proofHarness) got() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.texts)
}

// headlessHarness is the proofHarness as a headless adapter: its own turn record.
type headlessHarness struct{ *proofHarness }

func (h headlessHarness) TurnUnderWay() (bool, time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running, h.since
}

// proofRig is bob's daemon with its SessionCheck wired as nova-friend run wires them
// (the beat through SessionCheck.BeatOr saying the check asked and the check answered,
// down with the check's reason; Proof gating the deliveries), over bus's Fake and a fake
// harness. Its server is a stub that takes every beat's words (SessionCheck.Said) and
// keeps no record: the tests here assert only the daemon's side (the presence file, the
// log, what went into the session); the server's own rules are tested in
// internal/sprint/friend_proof_test.go. The clock moves one BeatEvery per step, in a
// synctest bubble; script runs each step's look at its offset from t0.
type proofRig struct {
	t        *testing.T
	mu       sync.Mutex
	now      time.Time
	fake     *bustest.Fake
	session  *bus.Bus
	coord    *bus.Bus
	h        *proofHarness
	sc       *SessionCheck
	d        *Daemon
	sent     time.Time
	records  []string
	status   Status
	presence PresenceStatus
	script   []scripted
	due      map[string]time.Time // answers the session will send, by nonce, and when
	after    func()               // run after each step's beat
	stop     time.Duration
	cancel   context.CancelFunc
	nonces   int
}

type scripted struct {
	at   time.Duration
	f    func()
	done bool
}

func newProofRig(t *testing.T, headless bool) *proofRig {
	t.Helper()
	r := &proofRig{t: t, now: t0, fake: bustest.NewFake(t0, "coord", "bob")}
	r.session, r.coord = &bus.Bus{Store: r.fake}, &bus.Bus{Store: r.fake}
	r.h = &proofHarness{r: r, answer: true, headless: headless}
	var adapter Deliverer = r.h
	if headless {
		adapter = headlessHarness{r.h}
	}
	r.sc = &SessionCheck{
		Friend: "bob", Store: r.fake, Now: r.clock, Record: r.record,
		Nonce: func() string { r.nonces++; return "n" + string(rune('0'+r.nonces)) },
		Text: func(nonce string) string {
			return SessionCheckText(nonce, "nova-friend pong --as bob --nonce "+nonce, "coord")
		},
		Save: func(p PresenceStatus) error { r.mu.Lock(); r.presence = p; r.mu.Unlock(); return nil },
		Go:   func(f func()) { f() },
	}
	r.sc.Deliver = r.sc.Gate(adapter)
	r.sc.Run = "run1"
	up := func(context.Context) error {
		if r.serve(r.sc.Words()) {
			r.mu.Lock()
			r.sent = r.now
			r.mu.Unlock()
		}
		return nil
	}
	down := func(context.Context, time.Time, string) error {
		r.serve(r.sc.Words())
		return nil
	}
	beat := r.sc.BeatOr(up, down)
	r.d = &Daemon{
		Friend: "bob", Harness: "fake", Dir: t.TempDir(), Width: 1, Store: r.sc.DaemonStore(), Deliver: r.sc.Deliver, StepBeatForTests: true,
		Now: r.step, Record: r.record, Proof: r.sc.Proof,
		Sent:  func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.sent },
		Pause: func(context.Context, time.Duration) { synctest.Wait() },
		Beat: func(ctx context.Context, _ time.Time) error {
			synctest.Wait() // a turn under way settles before the step looks
			r.look()
			err := beat(ctx)
			if r.after != nil {
				r.after()
			}
			return err
		},
		Pong:   func() (Pong, bool, error) { return Pong{}, false, nil },
		Status: func(s Status) error { r.mu.Lock(); r.status = s; r.mu.Unlock(); return nil },
	}
	return r
}

// step is the daemon's clock: one BeatEvery a step (the Fake never blocks a read, so
// the loop's own look at the clock is its step).
func (r *proofRig) step() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.now.Add(BeatEvery)
	return r.now
}

// serve is the stub server taking one beat with its proof words: each word said is
// taken, and a beat carrying an answer proves.
func (r *proofRig) serve(w BeatWords) bool {
	r.sc.Said(w)
	return w.Pong != ""
}

func (r *proofRig) clock() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

func (r *proofRig) record(line string) {
	r.mu.Lock()
	r.records = append(r.records, line)
	r.mu.Unlock()
}

// at runs f at the first step at or after offset from t0.
func (r *proofRig) at(offset time.Duration, f func()) {
	r.script = append(r.script, scripted{at: offset, f: f})
}

// later is the session answering nonce after d.
func (r *proofRig) later(nonce string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.due == nil {
		r.due = map[string]time.Time{}
	}
	r.due[nonce] = r.now.Add(d)
}

// look sends the answers due, runs what is due, and ends the run at stop.
func (r *proofRig) look() {
	r.mu.Lock()
	var send []string
	for nonce, at := range r.due {
		if !r.now.Before(at) {
			send = append(send, nonce)
			delete(r.due, nonce)
		}
	}
	r.mu.Unlock()
	for _, nonce := range send {
		r.pong(nonce)
	}
	elapsed := r.clock().Sub(t0)
	for i := range r.script {
		if s := &r.script[i]; !s.done && elapsed >= s.at {
			s.done = true
			s.f()
		}
	}
	if elapsed >= r.stop {
		r.cancel()
	}
}

func (r *proofRig) run(stop time.Duration) {
	r.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.stop = cancel, stop
	require.NoError(r.t, r.d.Run(ctx), "the daemon runs until it is stopped, never exiting on its own")
}

// pong is the session running its pong line: the pong on the bus as bob.
func (r *proofRig) pong(nonce string) {
	_, err := r.session.Send(context.Background(), bus.Message{From: "bob", To: []string{"coord"}, Subject: PongSubject, Body: PongLine(nonce, 0, 0, 1) + "\n"})
	require.NoError(r.t, err)
}

func (r *proofRig) lines(substr string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, l := range r.records {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return out
}

func checks(texts []string) []string {
	var out []string
	for _, t := range texts {
		if m := checkLine.FindStringSubmatch(t); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

// TestAHeadlessDaemonSendsItsCheckWhenNoTurnRuns: on a headless harness (dsh: each turn
// a one-shot process) the check goes in as a turn of its own as soon as the adapter's
// own record says no turn runs, never held by a turn lock that nothing running holds;
// the log says when it went in and when it was answered. While the record says a turn
// runs, it waits, and a check owed a whole check period is a refusal line saying why.
func TestAHeadlessDaemonSendsItsCheckWhenNoTurnRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newProofRig(t, true)
		r.sc.turn.RLock() // a holder no turn stands behind: the adapter's last turn ended
		var first PresenceStatus
		r.at(StaleTurnLock+5*BeatEvery, func() {
			r.mu.Lock()
			first = r.presence
			r.mu.Unlock()
			r.h.mu.Lock()
			r.h.running, r.h.since = true, r.clock() // now a turn does run, a long one
			r.h.mu.Unlock()
		})
		var waited PresenceStatus
		r.at(StaleTurnLock+2*SessionQuiet+time.Minute, func() {
			r.mu.Lock()
			waited = r.presence
			r.mu.Unlock()
			r.h.mu.Lock()
			r.h.running = false // the turn ended, and the lock with it
			r.h.mu.Unlock()
			r.sc.turn.RUnlock()
		})
		r.run(StaleTurnLock + 2*SessionQuiet + time.Minute + 5*BeatEvery)

		assert.Equal(t, 1, first.Checks, "the check went in by the adapter's record")
		assert.Equal(t, 1, first.Answers)
		assert.Equal(t, PresenceUp, first.Presence)
		require.Len(t, r.lines("the turn lock is held and the adapter's record says no turn runs"), 1)
		went := r.lines("presence: session check n1 into the session")
		require.Len(t, went, 1)
		assert.Contains(t, went[0], "owed since 2026-10-04T03:00:0", "owed since the start")
		assert.Contains(t, went[0], "as a turn of its own")
		assert.Len(t, r.lines("presence: up: the session answered n1"), 1, "and when it was answered")

		assert.Equal(t, 1, waited.Checks, "no check while the record says a turn runs")
		refused := r.lines("presence: REFUSED: a session check owed since")
		require.Len(t, refused, 1, "owed a whole check period: one refusal line")
		assert.Contains(t, refused[0], "it waits for the turn under way since")
		assert.Equal(t, []string{"n1", "n2"}, checks(r.h.got()), "the next check goes in once the turn ends")
	})
}

// TestAQuietDshSessionStillGetsTheNextDelivery: a dsh session is seen only by its
// turns, so after a quiet spell past AliveWithin no turn has ended recently. That is no
// harness gone: the next message goes in as the next headless turn at once, with no
// deferral, the harness check says the session is quiet (never "not seen"), and while
// a turn does run the check says so with its start (the finding of 2026-10-06: Zhi's
// row read the quiet dsh session as not seen while her cards sat ready).
func TestAQuietDshSessionStillGetsTheNextDelivery(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		var mu sync.Mutex
		var texts []string
		var ranAt []int
		dsh := &DSH{Dir: r.d.Dir, Session: "session-1", Program: "dsh", Run: func(_ context.Context, _, _ string, _ []string, stdin string) (string, int, error) {
			r.mu.Lock()
			beats := r.beats
			r.mu.Unlock()
			mu.Lock()
			texts, ranAt = append(texts, stdin), append(ranAt, beats)
			mu.Unlock()
			return "done\n", 0, nil
		}}
		r.d.Deliver, r.d.Harness, r.passive = dsh, "dsh", true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		WatchHarness(r.d, dsh)
		r.send(t, "ada", "first", "the first card")
		quiet := int((AliveWithin + 5*time.Minute) / BeatEvery)
		var sentAt int
		r.at[quiet] = func() {
			sentAt = quiet
			r.send(t, "ada", "next", "the next card")
		}
		r.run(t, quiet+10)

		mu.Lock()
		defer mu.Unlock()
		require.Len(t, texts, 2, "both messages went in as headless turns: %q", texts)
		assert.Contains(t, texts[1], "subject=next\n")
		assert.LessOrEqual(t, ranAt[1]-sentAt, 2, "the next message goes in the step after it arrives, quiet or not")
		all := strings.Join(r.records, "\n")
		assert.NotContains(t, all, "deferred", "nothing was deferred")
		assert.NotContains(t, all, "harness: not seen", "a quiet one-shot session is never not seen")
		assert.Contains(t, all, "quiet: the last turn into the dsh session session-1 ended exit 0")

		var s SessionTurns
		s.clock(func() time.Time { return t0 })
		s.begin()
		l := s.alive("dsh")
		assert.True(t, l.Running)
		assert.Equal(t, "a turn is running in the dsh session since "+t0.Format(time.RFC3339), l.Why)
	})
}

// gateWait is a turn held between the gate and the adapter, as the limit gate holds
// one through its wait: the adapter's own record has not begun it.
type gateWait struct {
	headlessHarness
	entered, release chan struct{}
}

func (g gateWait) Deliver(ctx context.Context, text string) (int, error) {
	if !strings.HasPrefix(text, SessionCheckPrefix) { // the daemon's turn waits; the check, which holds the gate itself, does not
		close(g.entered)
		<-g.release
	}
	return g.headlessHarness.Deliver(ctx, text)
}

// TestACheckNeverGoesInBesideATurnHeldAtTheGate: a turn at the gate is a turn from
// before any wait under it (the limit gate's) to its end, so a check never runs into a
// dsh session beside one: the adapter's record saying no turn runs is believed only
// while nothing is between the gate and the turn's end, however long the lock is held.
func TestACheckNeverGoesInBesideATurnHeldAtTheGate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		now := t0
		var records []string
		h := &proofHarness{}
		g := gateWait{headlessHarness{h}, make(chan struct{}), make(chan struct{})}
		sc := &SessionCheck{Friend: "bob", Store: bustest.NewFake(t0, "coord", "bob"), Now: func() time.Time { return now },
			Nonce: func() string { return "n1" }, Text: func(nonce string) string { return SessionCheckPrefix + nonce },
			Record: func(l string) { records = append(records, l) }, Go: func(f func()) { f() }}
		sc.Deliver = sc.Gate(g)
		go func() { _, _ = sc.Deliver.Deliver(context.Background(), "the daemon's turn") }()
		<-g.entered
		for range int((StaleTurnLock + 2*time.Minute) / BeatEvery) {
			sc.Step(context.Background())
			now = now.Add(BeatEvery)
		}
		assert.Empty(t, checks(h.got()), "no check beside a turn held at the gate, however long")
		all := strings.Join(records, "\n")
		assert.Contains(t, all, "presence: a session check is owed; it waits for the turn under way since "+t0.Format(time.RFC3339))
		assert.NotContains(t, all, "goes in by the record")

		close(g.release)
		synctest.Wait()
		sc.Step(context.Background())
		assert.Equal(t, []string{"n1"}, checks(h.got()), "the check goes in once the turn ends")
	})
}
