package friend

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
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
// (the beat through SessionCheck.BeatOr saying the check asked and the check answered,
// down with the check's reason; Proof gating the deliveries; Sent the proof the server
// took), over bus's Fake and a fake harness, and a sprint server that keeps his last
// beat, steps its proof by its own rule (sprint.ProveBeat: only an answer to a check
// asked) and reads his status by its own rule (sprint.FriendEvidence). The clock moves one BeatEvery per step, in a
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
	asked    []sprint.AskedCheck
	proof    time.Time
	noProof  []string
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
		if r.serve(r.sc.Words(), nil) {
			r.mu.Lock()
			r.sent = r.now
			r.mu.Unlock()
		}
		return nil
	}
	down := func(_ context.Context, until time.Time, reason string) error {
		r.serve(r.sc.Words(), &sprint.FriendReport{Until: until, Reason: reason})
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

// serve is the server taking one beat with its proof words (sprint.ProveBeat at its
// clock) and the report a down beat carries; it answers whether the beat proved.
func (r *proofRig) serve(w BeatWords, rep *sprint.FriendReport) bool {
	r.mu.Lock()
	asked, proved, why := sprint.ProveBeat(r.asked, sprint.BeatWords{Run: w.Run, Check: w.Check, Pong: w.Pong}, r.now)
	r.asked = asked
	if proved {
		r.proof = r.now
	}
	if why != "" {
		r.noProof = append(r.noProof, why)
	}
	r.beat = sprint.Beat{At: r.now, Proof: r.proof, Friend: rep}
	r.mu.Unlock()
	r.sc.Said(w)
	return proved
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
// its session. Its beat says each check it asks (--check) and, the second the session
// answers, the check answered (--pong), and the sprint server, by its own rule
// (sprint.ProveBeat), reads her up on that answer, once; the status says the proof the
// server took. While she is up a check goes in every ProveEvery, so the server's
// ten-minute window never lapses while she answers. When the session stops answering
// past the bound, her beat says down with the reason and the check's nonce; her next
// answer is proved again. A forged answer (a time, a nonce never asked, one answered
// twice) proves nothing.
func TestAnAnsweredCheckIsProvedToTheServerByTheDaemon(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newProofRig(t, false)
		var before, after, kept, fell, back [2]string
		var proof time.Time
		var presence PresenceStatus
		var sent time.Time
		r.at(3*BeatEvery, func() { before[0], before[1] = r.server() })
		r.at(5*BeatEvery, func() {
			after[0], after[1] = r.server()
			r.mu.Lock()
			proof, presence, sent = r.proof, r.presence, r.status.ProofSent
			r.mu.Unlock()
		})
		// the session answers the check every ProveEvery: proved all along
		r.at(ProveEvery+time.Minute, func() { kept[0], kept[1] = r.server(); r.h.set(false) })
		// quiet from here: the check at 2*ProveEvery goes unanswered past its bound
		r.at(2*ProveEvery+SessionBound+10*BeatEvery, func() {
			fell[0], fell[1] = r.server()
			r.pong("n3") // its late answer to the latest check
		})
		r.at(2*ProveEvery+SessionBound+20*BeatEvery, func() { back[0], back[1] = r.server() })
		r.run(2*ProveEvery + SessionBound + 21*BeatEvery)

		assert.Equal(t, sprint.Down, before[0], "a daemon that started has proved nothing")
		assert.Contains(t, before[1], "push unproven: session check n1")
		assert.Equal(t, sprint.Up, after[0], "the session's answer is on the server within the step: %s", after[1])
		assert.Contains(t, after[1], "session proof")
		require.False(t, proof.IsZero(), "the server took the answer as its proof")
		assert.LessOrEqual(t, proof.Sub(presence.LastHeard), 2*BeatEvery, "within a step of the presence file's last_heard: %s, %s", proof, presence.LastHeard)
		assert.True(t, sent.Equal(proof), "the status says the proof the server took")
		assert.Equal(t, sprint.Up, kept[0], "a session that answers every ProveEvery stays proved: %s", kept[1])
		assert.Equal(t, sprint.Down, fell[0])
		assert.Contains(t, fell[1], "her beat says down until")
		assert.Contains(t, fell[1], NoSessionAnswer+" to session check n3 within 5m0s", "down with the reason and the nonce")
		assert.Equal(t, sprint.Up, back[0], "the next answer is proved again: %s", back[1])
		assert.Equal(t, []string{"n1", "n2", "n3"}, checks(r.h.got()))
		assert.Empty(t, r.noProof, "every answer the daemon said named a check it asked")

		// the server's rule against a forged answer
		asked, _, _ := sprint.ProveBeat(nil, sprint.BeatWords{Run: "run1", Check: "n9"}, t0)
		_, proved, why := sprint.ProveBeat(asked, sprint.BeatWords{Run: "run1", Pong: t0.Format(time.RFC3339)}, t0)
		assert.False(t, proved, "a time proves nothing")
		assert.Contains(t, why, sprint.NoProof)
		_, proved, _ = sprint.ProveBeat(asked, sprint.BeatWords{Run: "run1", Pong: "n8"}, t0)
		assert.False(t, proved, "a nonce never asked proves nothing")
		asked, proved, _ = sprint.ProveBeat(asked, sprint.BeatWords{Run: "run1", Pong: "n9"}, t0)
		assert.True(t, proved, "the asked nonce answered proves")
		_, proved, _ = sprint.ProveBeat(asked, sprint.BeatWords{Run: "run1", Pong: "n9"}, t0)
		assert.False(t, proved, "the same nonce twice proves once")
	})
}

// A slow finish may occupy the reconcile loop for more than the server's beat
// freshness bound. The one cadence caller still carries the session's actual
// nonce answer; without that answer, the same cadence says down.
func TestAStalledFinishDoesNotHoldTheNativeBeat(t *testing.T) {
	t.Parallel()
	for _, answers := range []bool{true, false} {
		t.Run(fmt.Sprintf("session-answers-%t", answers), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r := newProofRig(t, false)
				r.d.StepBeatForTests = false
				watch := WatchHarness(r.d, Stub{Harness: "fake"}) // production wrapper used by the CLI: advisory status must not race the loop
				watch.Now = r.clock                               // observe the rig's clock without advancing a step
				r.h.set(answers)
				row := &twinRow{}
				r.d.Held = row.held
				for _, id := range []string{"first.w1", "second.w1"} {
					card := workCard(id, "working")
					inboxJob(t, r.d.Dir, card.Job, card.Brief)
					outboxReport(t, r.d.Dir, card.Job, "Verdict: HOLD\n\nneeds repair\n")
					row.set(append(row.cards, card)...)
				}
				var finishes int
				r.d.Finish = func(ctx context.Context, _ []string) error {
					finishes++
					<-ctx.Done()
					return ctx.Err()
				}
				var during, later [2]string
				r.at(6*BeatEvery, func() { during[0], during[1] = r.server() })
				r.at(17*BeatEvery, func() { later[0], later[1] = r.server() })
				r.run(21 * BeatEvery)
				assert.Equal(t, 2, finishes, "each blocked report is attempted once; no concurrent duplicate finish")
				want := sprint.Down
				if answers {
					want = sprint.Up
				}
				assert.Equal(t, want, during[0], "the native beat stays fresh during the first blocked finish: %s", during[1])
				assert.Equal(t, want, later[0], "the native beat stays fresh during the second blocked finish: %s", later[1])
				if !answers {
					assert.True(t, r.proof.IsZero(), "an unanswered check never manufactures session proof")
				}
			})
		})
	}
}

// TestADaemonWaitsForItsProofInsteadOfExiting: a session in a long turn answers no
// check inside five minutes. The daemon starts all the same with its push unproven, in
// status.json and in its beat, keeps the check the last run queued (its nonce, never a
// new one per try), asks again only after ReaskAfter while the session has not read the
// last (a queueing harness keeps every copy), delivers nothing until the pong arrives,
// says the refusal naming the nonce once, and turns live without a restart.
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
		var once, twice []string
		r.at(SessionQuiet+SessionBound+time.Minute, func() {
			r.mu.Lock()
			waiting = r.status
			r.mu.Unlock()
			server[0], server[1] = r.server()
			once = r.h.got()
		})
		r.at(ReaskAfter+time.Minute, func() {
			twice = r.h.got()
			r.pong("k1") // the long turn ends and the session answers the check queued in it
		})
		var live Status
		var up string
		r.at(ReaskAfter+time.Minute+5*BeatEvery, func() {
			r.mu.Lock()
			live = r.status
			r.mu.Unlock()
			up, _ = r.server()
		})
		r.run(ReaskAfter + time.Minute + 6*BeatEvery)

		assert.Equal(t, []string{"k1"}, checks(once), "one copy while the session has not read it")
		assert.Len(t, once, 1, "nothing but the check went into the session while the push was unproven: %q", once)
		assert.Equal(t, []string{"k1", "k1"}, checks(twice), "asked again after ReaskAfter, with the queued check's nonce, never a new one")
		assert.Len(t, twice, 2)
		assert.Equal(t, PushUnproven, waiting.Push)
		assert.Equal(t, "k1", waiting.PushNonce)
		assert.True(t, !waiting.PushSince.After(t0.Add(2*BeatEvery)) && waiting.PushSince.After(t0), "unproven since the start: %s", waiting.PushSince)
		assert.Equal(t, sprint.Down, server[0])
		assert.Contains(t, server[1], "push unproven: session check k1", "her beat says why")
		assert.Len(t, r.lines("push proof: unproven: session check k1"), 1, "the refusal names the nonce once")

		assert.Equal(t, PushProved, live.Push, "the session answered: live without a restart")
		assert.Equal(t, sprint.Up, up, "the re-ask was said again, so the server takes its answer")
		require.Len(t, r.lines("push proof: proved"), 1)
		got := r.h.got()
		require.Len(t, got, 3, "the present went in once the push was proved")
		assert.Contains(t, got[2], PresentTextRule)
		assert.Contains(t, got[2], "Skipped: 1 deals")
		assert.NotContains(t, got[2], `subject="card dealt"`, "the old deal never reaches the newly proved session")
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

// TestASlowAnswerNeverLeavesAGap: a session that takes three minutes to answer each
// check is proved without a gap. The next check is timed from the ask, not the answer,
// so the proof is renewed every ProveEvery whatever the answer's delay, and the
// server's window (sprint.FriendProofLive) outlasts the cycle (the second cold read of
// 2026-10-06: timed from the answer, a three-minute answer read down five minutes an
// hour).
func TestASlowAnswerNeverLeavesAGap(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newProofRig(t, false)
		r.h.delay = 3 * time.Minute
		var first time.Time
		var gaps []string
		r.after = func() {
			word, why := r.server()
			now := r.clock()
			switch {
			case first.IsZero() && word == sprint.Up:
				first = now
			case !first.IsZero() && word != sprint.Up:
				gaps = append(gaps, now.Sub(t0).String()+": "+why)
			}
		}
		r.run(time.Hour)
		require.False(t, first.IsZero(), "proved once the first answer came")
		assert.LessOrEqual(t, first.Sub(t0), 3*time.Minute+5*BeatEvery)
		assert.Empty(t, gaps, "up every step from the first answer on")
		assert.GreaterOrEqual(t, len(checks(r.h.got())), int(time.Hour/ProveEvery), "a check every ProveEvery, timed from the ask")
	})
}

// TestTheProofCycleFitsTheEvidenceWindow: the slowest answer the daemon accepts comes
// SessionBound after the ask, the next ask ProveEvery after this one, so a session that
// answers is never proved longer ago than ProveEvery + SessionBound, which has to be
// under the server's window for her session's proof.
func TestTheProofCycleFitsTheEvidenceWindow(t *testing.T) {
	t.Parallel()
	assert.Less(t, ProveEvery+SessionBound, sprint.FriendProofLive)
	assert.Less(t, SessionBound, sprint.CheckAnswerWithin, "the server takes every answer the daemon waits for")
}
