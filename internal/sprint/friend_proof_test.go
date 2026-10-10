package sprint_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Moved from pkg/friend/proof_test.go (the nova-sprint split): the tests that read the
// sprint server's own rules (sprint.ProveBeat, sprint.FriendEvidence), over friend's exported API.
//
// The findings of 2026-10-06 on the new build, one test each: the daemon knew its
// session answered and never told the sprint server, so three friends read down for
// twenty minutes with their sessions answering; a daemon whose start check was not
// answered within five minutes exited, and launchd restarted it into the same wait for
// ever; a headless dsh daemon owed its check for twenty minutes with no turn running.

// proofT0 is friend's test clock origin (pkg/friend machine_test.go t0).
var proofT0 = time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)

// checkLine is a session check as the session reads it: its nonce.
var checkLine = regexp.MustCompile(`^` + friend.SessionCheckPrefix + `(\S+)`)

// proofHarness is bob's harness and the session in it. Answering, the session runs a
// check's pong line at once; in a long turn, a check waits queued in the session until
// the test lets the session answer it.
type proofHarness struct {
	r      *proofRig
	mu     sync.Mutex
	texts  []string
	answer bool
	delay  time.Duration // the session answers a check this long after it went in
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

// proofRig is bob's daemon with its SessionCheck wired as nova-friend run wires them
// (the beat through SessionCheck.BeatOr saying the check asked and the check answered,
// down with the check's reason; Proof gating the deliveries; Sent the proof the server
// took), over bus's Fake and a fake harness, and a sprint server that keeps his last
// beat, steps its proof by its own rule (sprint.ProveBeat: only an answer to a check
// asked) and reads his status by its own rule (sprint.FriendEvidence). The clock moves one BeatEvery per step, in a
// synctest bubble; script runs each step's look at its offset from proofT0.
type proofRig struct {
	t        *testing.T
	mu       sync.Mutex
	now      time.Time
	fake     *bustest.Fake
	session  *bus.Bus
	coord    *bus.Bus
	h        *proofHarness
	sc       *friend.SessionCheck
	d        *friend.Daemon
	beat     sprint.Beat // the server's record of bob's last beat
	asked    []sprint.AskedCheck
	proof    time.Time
	noProof  []string
	sent     time.Time
	records  []string
	status   friend.Status
	presence friend.PresenceStatus
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

func newProofRig(t *testing.T) *proofRig {
	t.Helper()
	r := &proofRig{t: t, now: proofT0, fake: bustest.NewFake(proofT0, "coord", "bob")}
	r.session, r.coord = &bus.Bus{Store: r.fake}, &bus.Bus{Store: r.fake}
	r.h = &proofHarness{r: r, answer: true}
	var adapter friend.Deliverer = r.h
	r.sc = &friend.SessionCheck{
		Friend: "bob", Store: r.fake, Now: r.clock, Record: r.record,
		Nonce: func() string { r.nonces++; return "n" + string(rune('0'+r.nonces)) },
		Text: func(nonce string) string {
			return friend.SessionCheckText(nonce, "nova-friend pong --as bob --nonce "+nonce, "coord")
		},
		Save: func(p friend.PresenceStatus) error { r.mu.Lock(); r.presence = p; r.mu.Unlock(); return nil },
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
	r.d = &friend.Daemon{
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
		Pong:   func() (friend.Pong, bool, error) { return friend.Pong{}, false, nil },
		Status: func(s friend.Status) error { r.mu.Lock(); r.status = s; r.mu.Unlock(); return nil },
	}
	return r
}

// step is the daemon's clock: one BeatEvery a step (the Fake never blocks a read, so
// the loop's own look at the clock is its step).
func (r *proofRig) step() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.now.Add(friend.BeatEvery)
	return r.now
}

// serve is the server taking one beat with its proof words (sprint.ProveBeat at its
// clock) and the report a down beat carries; it answers whether the beat proved.
func (r *proofRig) serve(w friend.BeatWords, rep *sprint.FriendReport) bool {
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

// at runs f at the first step at or after offset from proofT0.
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
	elapsed := r.clock().Sub(proofT0)
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
	_, err := r.session.Send(context.Background(), bus.Message{From: "bob", To: []string{"coord"}, Subject: friend.PongSubject, Body: friend.PongLine(nonce, 0, 0, 1) + "\n"})
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

// proofTwinRow is friend's twinRow (pkg/friend inbox_test.go): the sprint server's
// answer to friend cards <friend>, the cards on her row however they got there.
type proofTwinRow struct {
	mu    sync.Mutex
	cards []friend.HeldCard
	err   error
	asked int
}

func (w *proofTwinRow) held(context.Context) (friend.Row, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.asked++
	return friend.Row{Cards: append([]friend.HeldCard(nil), w.cards...), Reads: true, From: friend.FromCards}, w.err
}

func (w *proofTwinRow) set(cards ...friend.HeldCard) { w.mu.Lock(); w.cards = cards; w.mu.Unlock() }

// proofWorkCard is friend's workCard: a held work card as friend sync renders its brief
// (its STATUS line first).
func proofWorkCard(card, col string) friend.HeldCard {
	job := card + "~15"
	return friend.HeldCard{Card: card, Job: job, Col: col, Brief: fmt.Sprintf("STATUS: nova-sprint card %s, epoch 15, attempt 1; push your work to the branch sprint/%s.g1.e15; when done, write outbox/%s/REPORT.md with Verdict: LAND|HOLD|FAIL and Head: <sha>\n\n%s: the brief\n", card, card, job, card)}
}

// proofInboxJob is friend's inboxJob: a brief in her inbox.
func proofInboxJob(t *testing.T, dir, job, brief string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", job), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", job, "BRIEF.md"), []byte(brief), 0o644))
}

// proofOutboxReport is friend's outboxReport: a report in her outbox.
func proofOutboxReport(t *testing.T, dir, job, report string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", job), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox", job, "REPORT.md"), []byte(report), 0o644))
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
		r := newProofRig(t)
		var before, after, kept, fell, back [2]string
		var proof time.Time
		var presence friend.PresenceStatus
		var sent time.Time
		r.at(3*friend.BeatEvery, func() { before[0], before[1] = r.server() })
		r.at(5*friend.BeatEvery, func() {
			after[0], after[1] = r.server()
			r.mu.Lock()
			proof, presence, sent = r.proof, r.presence, r.status.ProofSent
			r.mu.Unlock()
		})
		// the session answers the check every ProveEvery: proved all along
		r.at(friend.ProveEvery+time.Minute, func() { kept[0], kept[1] = r.server(); r.h.set(false) })
		// quiet from here: the check at 2*ProveEvery goes unanswered past its bound
		r.at(2*friend.ProveEvery+friend.SessionBound+10*friend.BeatEvery, func() {
			fell[0], fell[1] = r.server()
			r.pong("n3") // its late answer to the latest check
		})
		r.at(2*friend.ProveEvery+friend.SessionBound+20*friend.BeatEvery, func() { back[0], back[1] = r.server() })
		r.run(2*friend.ProveEvery + friend.SessionBound + 21*friend.BeatEvery)

		assert.Equal(t, sprint.Down, before[0], "a daemon that started has proved nothing")
		assert.Contains(t, before[1], "push unproven: session check n1")
		assert.Equal(t, sprint.Up, after[0], "the session's answer is on the server within the step: %s", after[1])
		assert.Contains(t, after[1], "session proof")
		require.False(t, proof.IsZero(), "the server took the answer as its proof")
		assert.LessOrEqual(t, proof.Sub(presence.LastHeard), 2*friend.BeatEvery, "within a step of the presence file's last_heard: %s, %s", proof, presence.LastHeard)
		assert.True(t, sent.Equal(proof), "the status says the proof the server took")
		assert.Equal(t, sprint.Up, kept[0], "a session that answers every ProveEvery stays proved: %s", kept[1])
		assert.Equal(t, sprint.Down, fell[0])
		assert.Contains(t, fell[1], "her beat says down until")
		assert.Contains(t, fell[1], friend.NoSessionAnswer+" to session check n3 within 5m0s", "down with the reason and the nonce")
		assert.Equal(t, sprint.Up, back[0], "the next answer is proved again: %s", back[1])
		assert.Equal(t, []string{"n1", "n2", "n3"}, checks(r.h.got()))
		assert.Empty(t, r.noProof, "every answer the daemon said named a check it asked")

		// the server's rule against a forged answer
		asked, _, _ := sprint.ProveBeat(nil, sprint.BeatWords{Run: "run1", Check: "n9"}, proofT0)
		_, proved, why := sprint.ProveBeat(asked, sprint.BeatWords{Run: "run1", Pong: proofT0.Format(time.RFC3339)}, proofT0)
		assert.False(t, proved, "a time proves nothing")
		assert.Contains(t, why, sprint.NoProof)
		_, proved, _ = sprint.ProveBeat(asked, sprint.BeatWords{Run: "run1", Pong: "n8"}, proofT0)
		assert.False(t, proved, "a nonce never asked proves nothing")
		asked, proved, _ = sprint.ProveBeat(asked, sprint.BeatWords{Run: "run1", Pong: "n9"}, proofT0)
		assert.True(t, proved, "the asked nonce answered proves")
		_, proved, _ = sprint.ProveBeat(asked, sprint.BeatWords{Run: "run1", Pong: "n9"}, proofT0)
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
				r := newProofRig(t)
				r.d.StepBeatForTests = false
				watch := friend.WatchHarness(r.d, friend.Stub{Harness: "fake"}) // production wrapper used by the CLI: advisory status must not race the loop
				watch.Now = r.clock                                             // observe the rig's clock without advancing a step
				r.h.set(answers)
				row := &proofTwinRow{}
				r.d.Held = row.held
				for _, id := range []string{"first.w1", "second.w1"} {
					card := proofWorkCard(id, "working")
					proofInboxJob(t, r.d.Dir, card.Job, card.Brief)
					proofOutboxReport(t, r.d.Dir, card.Job, "Verdict: HOLD\n\nneeds repair\n")
					row.set(append(row.cards, card)...)
				}
				var finishes int
				r.d.Finish = func(ctx context.Context, _ []string) error {
					finishes++
					<-ctx.Done()
					return ctx.Err()
				}
				var during, later [2]string
				r.at(6*friend.BeatEvery, func() { during[0], during[1] = r.server() })
				r.at(17*friend.BeatEvery, func() { later[0], later[1] = r.server() })
				r.run(21 * friend.BeatEvery)
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
		r := newProofRig(t)
		r.sc.Keep = "k1" // the last run's check, queued in the session and never answered
		r.h.set(false)   // the session is in a long turn
		_, err := r.coord.Send(context.Background(), bus.Message{From: "coord", To: []string{"bob"}, Subject: "card dealt", Body: "a card is in your inbox"})
		require.NoError(t, err)
		var waiting friend.Status
		var server [2]string
		var once, twice []string
		r.at(friend.SessionQuiet+friend.SessionBound+time.Minute, func() {
			r.mu.Lock()
			waiting = r.status
			r.mu.Unlock()
			server[0], server[1] = r.server()
			once = r.h.got()
		})
		r.at(friend.ReaskAfter+time.Minute, func() {
			twice = r.h.got()
			r.pong("k1") // the long turn ends and the session answers the check queued in it
		})
		var live friend.Status
		var up string
		r.at(friend.ReaskAfter+time.Minute+5*friend.BeatEvery, func() {
			r.mu.Lock()
			live = r.status
			r.mu.Unlock()
			up, _ = r.server()
		})
		r.run(friend.ReaskAfter + time.Minute + 6*friend.BeatEvery)

		assert.Equal(t, []string{"k1"}, checks(once), "one copy while the session has not read it")
		assert.Len(t, once, 1, "nothing but the check went into the session while the push was unproven: %q", once)
		assert.Equal(t, []string{"k1", "k1"}, checks(twice), "asked again after ReaskAfter, with the queued check's nonce, never a new one")
		assert.Len(t, twice, 2)
		assert.Equal(t, friend.PushUnproven, waiting.Push)
		assert.Equal(t, "k1", waiting.PushNonce)
		assert.True(t, !waiting.PushSince.After(proofT0.Add(2*friend.BeatEvery)) && waiting.PushSince.After(proofT0), "unproven since the start: %s", waiting.PushSince)
		assert.Equal(t, sprint.Down, server[0])
		assert.Contains(t, server[1], "push unproven: session check k1", "her beat says why")
		assert.Len(t, r.lines("push proof: unproven: session check k1"), 1, "the refusal names the nonce once")

		assert.Equal(t, friend.PushProved, live.Push, "the session answered: live without a restart")
		assert.Equal(t, sprint.Up, up, "the re-ask was said again, so the server takes its answer")
		require.Len(t, r.lines("push proof: proved"), 1)
		got := r.h.got()
		require.Len(t, got, 3, "the present went in once the push was proved")
		assert.Contains(t, got[2], friend.PresentTextRule)
		assert.Contains(t, got[2], "Skipped: 1 deals")
		assert.NotContains(t, got[2], `subject="card dealt"`, "the old deal never reaches the newly proved session")
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
		r := newProofRig(t)
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
				gaps = append(gaps, now.Sub(proofT0).String()+": "+why)
			}
		}
		r.run(time.Hour)
		require.False(t, first.IsZero(), "proved once the first answer came")
		assert.LessOrEqual(t, first.Sub(proofT0), 3*time.Minute+5*friend.BeatEvery)
		assert.Empty(t, gaps, "up every step from the first answer on")
		assert.GreaterOrEqual(t, len(checks(r.h.got())), int(time.Hour/friend.ProveEvery), "a check every ProveEvery, timed from the ask")
	})
}

// TestTheProofCycleFitsTheEvidenceWindow: the slowest answer the daemon accepts comes
// SessionBound after the ask, the next ask ProveEvery after this one, so a session that
// answers is never proved longer ago than ProveEvery + SessionBound, which has to be
// under the server's window for her session's proof.
func TestTheProofCycleFitsTheEvidenceWindow(t *testing.T) {
	t.Parallel()
	assert.Less(t, friend.ProveEvery+friend.SessionBound, sprint.FriendProofLive)
	assert.Less(t, friend.SessionBound, sprint.CheckAnswerWithin, "the server takes every answer the daemon waits for")
}
