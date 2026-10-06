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

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		h.r.pong(m[1])
	}
	return 0, nil
}

func (h *proofHarness) got() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.texts)
}

func (h *proofHarness) set(answer bool) {
	h.mu.Lock()
	h.answer = answer
	h.mu.Unlock()
}

// headlessHarness is the proofHarness as a headless adapter: its own turn record.
type headlessHarness struct{ *proofHarness }

func (h headlessHarness) TurnUnderWay() (bool, time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running, h.since
}

// proofRig is bob's daemon with its SessionCheck wired as nova-friend run wires them
// (the beat through SessionCheck.BeatOr carrying Proved, down with the check's reason;
// Proof gating the deliveries; Sent the proof the server took), over bus's Fake and a
// fake harness, and a sprint server that keeps his last beat and reads his status by
// its own rule (sprint.FriendEvidence). The clock moves one BeatEvery per step, in a
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
	beat     sprint.Beat // the server's record of bob's last beat
	sent     time.Time
	records  []string
	status   Status
	presence PresenceStatus
	script   []scripted
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
	up := func(context.Context) error {
		proof := r.sc.Proved()
		r.mu.Lock()
		defer r.mu.Unlock()
		r.beat = sprint.Beat{At: r.now, Proof: proof}
		if !proof.IsZero() {
			r.sent = proof
		}
		return nil
	}
	down := func(_ context.Context, until time.Time, reason string) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.beat = sprint.Beat{At: r.now, Friend: &sprint.FriendReport{Until: until, Reason: reason}}
		return nil
	}
	beat := r.sc.BeatOr(up, down)
	r.d = &Daemon{
		Friend: "bob", Harness: "fake", Dir: t.TempDir(), Width: 1, Store: r.sc.DaemonStore(), Deliver: r.sc.Deliver,
		Now: r.step, Record: r.record, Proof: r.sc.Proof,
		Sent:  func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.sent },
		Pause: func(context.Context, time.Duration) { synctest.Wait() },
		Beat: func(ctx context.Context, _ time.Time) error {
			synctest.Wait() // a turn under way settles before the step looks
			r.look()
			return beat(ctx)
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

// look runs what is due, and ends the run at stop.
func (r *proofRig) look() {
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

// server is bob's status as the sprint server reads it now, and why.
func (r *proofRig) server() (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sprint.FriendEvidence(sprint.FriendPresence{Beat: r.beat}, r.now)
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

// TestAnAnsweredCheckIsProvedToTheServerByTheDaemon: the daemon is the one that proves
// its session. The second the session answers its check, the daemon's next beat carries
// the answer's time (the presence file's last_heard) and the sprint server reads her up
// on it, by its own rule; the status says the proof the server took. When the session
// stops answering past the bound, her beat says down with the reason and the check's
// nonce, and the server reads her down with it; her next answer is proved again.
func TestAnAnsweredCheckIsProvedToTheServerByTheDaemon(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newProofRig(t, false)
		var before, after, quiet, fell, back [2]string
		var proof time.Time
		var presence PresenceStatus
		var sent time.Time
		r.at(3*BeatEvery, func() { before[0], before[1] = r.server() })
		r.at(5*BeatEvery, func() {
			after[0], after[1] = r.server()
			r.mu.Lock()
			proof, presence, sent = r.beat.Proof, r.presence, r.status.ProofSent
			r.mu.Unlock()
			r.h.set(false) // the session goes quiet and answers nothing more
		})
		r.at(SessionQuiet+SessionBound-time.Minute, func() { quiet[0], quiet[1] = r.server() })
		r.at(SessionQuiet+SessionBound+10*BeatEvery, func() {
			fell[0], fell[1] = r.server()
			r.pong("n2") // its late answer to the latest check
		})
		r.at(SessionQuiet+SessionBound+20*BeatEvery, func() { back[0], back[1] = r.server() })
		r.run(SessionQuiet + SessionBound + 21*BeatEvery)

		assert.Equal(t, sprint.Down, before[0], "a daemon that started has proved nothing")
		assert.Contains(t, before[1], "push unproven: session check n1")
		assert.Equal(t, sprint.Up, after[0], "the session's answer is on the server within the step: %s", after[1])
		assert.Contains(t, after[1], "session proof")
		require.False(t, proof.IsZero(), "the beat carries the session's proof")
		assert.True(t, proof.Equal(presence.LastHeard), "the proof is the presence file's last_heard: %s, %s", proof, presence.LastHeard)
		assert.True(t, sent.Equal(proof), "the status says the proof the server took")
		assert.Equal(t, sprint.Up, quiet[0], "a quiet session is proved until its check's bound: %s", quiet[1])
		assert.Equal(t, sprint.Down, fell[0])
		assert.Contains(t, fell[1], "her beat says down until")
		assert.Contains(t, fell[1], NoSessionAnswer+" to session check n2 within 5m0s", "down with the reason and the nonce")
		assert.Equal(t, sprint.Up, back[0], "the next answer is proved again: %s", back[1])
		assert.Equal(t, []string{"n1", "n2"}, checks(r.h.got()))
	})
}

// TestADaemonWaitsForItsProofInsteadOfExiting: a session in a long turn answers no
// check inside five minutes. The daemon starts all the same with its push unproven, in
// status.json and in its beat, keeps the check the last run queued (its nonce, never a
// new one per try), asks again on the check cadence, delivers nothing until the pong
// arrives, says the refusal naming the nonce once, and turns live without a restart.
func TestADaemonWaitsForItsProofInsteadOfExiting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newProofRig(t, false)
		r.sc.Keep = "k1" // the last run's check, queued in the session and never answered
		r.h.set(false)   // the session is in a long turn
		_, err := r.coord.Send(context.Background(), bus.Message{From: "coord", To: []string{"bob"}, Subject: "card dealt", Body: "a card is in your inbox"})
		require.NoError(t, err)
		var waiting Status
		var server [2]string
		var delivered []string
		r.at(SessionQuiet+SessionBound+time.Minute, func() {
			r.mu.Lock()
			waiting = r.status
			r.mu.Unlock()
			server[0], server[1] = r.server()
			delivered = r.h.got()
			r.pong("k1") // the long turn ends and the session answers the check queued in it
		})
		var live Status
		var up string
		r.at(SessionQuiet+SessionBound+time.Minute+5*BeatEvery, func() {
			r.mu.Lock()
			live = r.status
			r.mu.Unlock()
			up, _ = r.server()
		})
		r.run(SessionQuiet + SessionBound + time.Minute + 6*BeatEvery)

		assert.Equal(t, []string{"k1", "k1"}, checks(delivered), "the queued check's nonce, asked again on the cadence, never a new one")
		assert.Len(t, delivered, 2, "nothing but the checks went into the session while the push was unproven: %q", delivered)
		assert.Equal(t, PushUnproven, waiting.Push)
		assert.Equal(t, "k1", waiting.PushNonce)
		assert.True(t, !waiting.PushSince.After(t0.Add(2*BeatEvery)) && waiting.PushSince.After(t0), "unproven since the start: %s", waiting.PushSince)
		assert.Equal(t, sprint.Down, server[0])
		assert.Contains(t, server[1], "push unproven: session check k1", "her beat says why")
		assert.Len(t, r.lines("push proof: unproven: session check k1"), 1, "the refusal names the nonce once")

		assert.Equal(t, PushProved, live.Push, "the session answered: live without a restart")
		assert.Equal(t, sprint.Up, up)
		require.Len(t, r.lines("push proof: proved"), 1)
		got := r.h.got()
		require.Len(t, got, 3, "the waiting message went in once the push was proved")
		assert.Contains(t, got[2], `subject="card dealt"`)
	})
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
		assert.Contains(t, texts[1], `subject="next"`)
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
