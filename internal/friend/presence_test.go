package friend

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// closedApp is a harness whose app is not running: every delivery "works"
// (the daemon is alive and the command exits 0) and nothing inside ever
// answers, the finding of 2026-10-04.
type closedApp struct {
	mu    sync.Mutex
	texts []string
}

func (c *closedApp) Deliver(_ context.Context, text string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, text)
	return 0, nil
}

func (c *closedApp) got() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.texts...)
}

// presenceRig is one SessionCheck over bus's Fake, the closed app and a
// clock the test moves by hand; the check's delivery runs inline.
type presenceRig struct {
	store          *bustest.Fake
	app            *closedApp
	sc             *SessionCheck
	daemon, direct *bus.Bus // what the daemon sends, and what anyone else (the session, a friend) sends
	now            time.Time
	nonces         int
	beats          int
	beat           func(context.Context) error
}

func newPresenceRig(t *testing.T) *presenceRig {
	t.Helper()
	r := &presenceRig{store: bustest.NewFake(t0, "ada", "bob"), app: &closedApp{}, now: t0}
	r.sc = &SessionCheck{
		Friend: "bob", Store: r.store, Now: func() time.Time { return r.now },
		Nonce: func() string { r.nonces++; return "n" + string(rune('0'+r.nonces)) },
		Text: func(nonce string) string {
			return SessionCheckPrefix + nonce + "\nanswer: nova-friend pong --as bob --nonce " + nonce
		},
		Go: func(f func()) { f() },
	}
	r.sc.Deliver = r.sc.Gate(r.app)
	r.daemon, r.direct = &bus.Bus{Store: r.sc.DaemonStore()}, &bus.Bus{Store: r.store}
	r.beat = r.sc.Beat(func(context.Context) error { r.beats++; return nil })
	return r
}

func (r *presenceRig) step(t *testing.T, d time.Duration) {
	t.Helper()
	r.now = r.now.Add(d)
	_ = r.beat(context.Background()) // ignored: the refusal is what Present says, asserted by the caller
}

func (r *presenceRig) send(t *testing.T, b *bus.Bus, from, subject, body string) {
	t.Helper()
	_, err := b.Send(context.Background(), bus.Message{From: from, To: []string{"ada"}, Subject: subject, Body: body})
	require.NoError(t, err)
}

func (r *presenceRig) present(t *testing.T) (bool, string) {
	t.Helper()
	return r.sc.Present()
}

// TestOnlyTheSessionCanAnswerTheNonce: a friend whose app is closed is
// down, however well its daemon answers. The daemon delivers a session check
// with a fresh nonce through the adapter; the daemon's own pong, a pong the
// daemon writes, another friend's pong and a wrong nonce all leave it down;
// five minutes without the session's answer is down "no session answer", and
// no beat goes to the sprint server; the session's answer brings it up; a check
// goes in ProveEvery after it whatever the session says, the old nonce no
// answer to it; silence with it unanswered is down; a check the session has not
// read is not asked again before ReaskAfter; a message the session writes
// brings it up as an answer does (docs/SPEC-FRIEND.md, presence).
func TestOnlyTheSessionCanAnswerTheNonce(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	ctx := context.Background()

	r.step(t, BeatEvery)
	require.Len(t, r.app.got(), 1, "the first step delivers a session check through the adapter")
	assert.True(t, strings.HasPrefix(r.app.got()[0], SessionCheckPrefix+"n1"), "the check carries its nonce: %q", r.app.got()[0])
	up, reason := r.present(t)
	assert.False(t, up, "a daemon that started proves nothing about the session")
	assert.Equal(t, NotYetAnswered, reason)
	assert.Equal(t, 0, r.beats, "no beat to the sprint server before the session answers")

	r.send(t, r.daemon, "bob", DaemonPongSubject, "daemon-pong n1\n")
	r.send(t, r.daemon, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.send(t, r.direct, "ada", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.send(t, r.direct, "bob", PongSubject, PongLine("n9", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	up, _ = r.present(t)
	assert.False(t, up, "the daemon's pong, a pong the daemon wrote, another friend's and a wrong nonce are no answer")

	r.step(t, SessionBound)
	up, reason = r.present(t)
	assert.False(t, up)
	assert.Equal(t, NoSessionAnswer, reason, "five minutes with no session answer")
	assert.Error(t, r.beat(ctx), "the beat is held back while the session is down")
	assert.Equal(t, 0, r.beats)
	assert.Len(t, r.app.got(), 1, "one check per nonce")

	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	up, reason = r.present(t)
	assert.True(t, up, "the session's answer to the nonce brings it back up")
	assert.Empty(t, reason)
	assert.Equal(t, 1, r.beats, "and the beat flows again")

	for range 5 { // a session that talks on the bus is still checked every ProveEvery: only an answer proves it to the server
		r.send(t, r.direct, "bob", "status", "working on it\n")
		r.step(t, 4*time.Minute)
	}
	require.Len(t, r.app.got(), 2, "one check ProveEvery after the answer, whatever the session says")
	assert.True(t, strings.HasPrefix(r.app.got()[1], SessionCheckPrefix+"n2"), "a new nonce: %q", r.app.got()[1])
	up, _ = r.present(t)
	assert.True(t, up, "her messages keep her up while the check waits for its answer")

	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.send(t, r.daemon, "bob", DaemonPongSubject, "daemon-pong n2\n")
	r.step(t, SessionQuiet+SessionBound)
	up, reason = r.present(t)
	assert.False(t, up, "silent, and the old nonce and the daemon's pong answer nothing")
	assert.Equal(t, NoSessionAnswer, reason)
	assert.Len(t, r.app.got(), 2, "the check the session has not read is not asked again before ReaskAfter")

	r.send(t, r.direct, "bob", "status", "hello\n")
	r.step(t, BeatEvery)
	up, _ = r.present(t)
	assert.True(t, up, "while down, any message the session writes brings it back up: the session is alive (the finding of 2026-10-05)")

	r.send(t, r.direct, "bob", PongSubject, PongLine("n2", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	require.Len(t, r.app.got(), 3, "a late answer: the next check is timed from the ask, so it goes in at once")
	r.step(t, ReaskAfter)
	require.Len(t, r.app.got(), 4, "and the one after on the cadence")
	r.step(t, SessionBound)
	up, _ = r.present(t)
	require.False(t, up, "quiet again, and the next check unanswered")
	r.send(t, r.direct, "bob", PongSubject, PongLine("n4", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	up, _ = r.present(t)
	assert.True(t, up, "the next answer brings it back up")
}

// TestASessionCheckWaitsForTheTurnUnderWay: the check is a turn of its own,
// never a second one in the same session; it waits for the daemon's turn to
// end, and its bound runs from when it went in.
func TestASessionCheckWaitsForTheTurnUnderWay(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	release, entered := make(chan struct{}), make(chan struct{})
	held := &heldApp{release: release, entered: entered}
	r.sc.Deliver = r.sc.Gate(held)
	go func() { _, _ = r.sc.Deliver.Deliver(context.Background(), "the daemon's turn") }()
	<-entered

	r.step(t, BeatEvery)
	r.step(t, SessionBound+time.Minute)
	assert.Equal(t, []string{"the daemon's turn"}, held.got(), "no check while the turn runs")
	_, reason := r.present(t)
	assert.Equal(t, NotYetAnswered, reason, "the bound has not started: nothing was asked")

	close(release)
	require.Eventually(t, func() bool { r.step(t, 0); return len(held.got()) == 2 }, 5*time.Second, time.Millisecond)
	assert.True(t, strings.HasPrefix(held.got()[1], SessionCheckPrefix), "the check goes in once the turn ends")
	r.step(t, SessionBound)
	_, reason = r.present(t)
	assert.Equal(t, NoSessionAnswer, reason)
}

// TestAPassiveSessionIsCheckedOnItsOwnStream: a harness with no deliver
// command reads its own stream, so the check goes there, as the daemon's.
func TestAPassiveSessionIsCheckedOnItsOwnStream(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	r.sc.Deliver = r.sc.Gate(Stub{Harness: "claude"})
	r.step(t, BeatEvery)
	got, err := r.store.Range(context.Background(), bus.StreamOf("bob"), "-", "+", 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, SessionCheckPrefix+"n1", got[0].Message().Subject)
	up, _ := r.present(t)
	assert.False(t, up, "the check on the stream is the daemon's, no answer")
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 0)+"\n")
	r.step(t, BeatEvery)
	up, _ = r.present(t)
	assert.True(t, up)
}

type heldApp struct {
	mu      sync.Mutex
	texts   []string
	release chan struct{}
	entered chan struct{}
}

func (h *heldApp) Deliver(_ context.Context, text string) (int, error) {
	h.mu.Lock()
	h.texts = append(h.texts, text)
	first := len(h.texts) == 1
	h.mu.Unlock()
	if first {
		close(h.entered)
		<-h.release
	}
	return 0, nil
}

func (h *heldApp) got() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.texts...)
}

// upWithOpenCheck is a presence proved and up at t0, its next check n2 asked at
// t0 plus ProveEvery and unanswered: the state a batch friend is in when her
// daemon starts the turn that takes every waiting card.
func upWithOpenCheck(t *testing.T) (*Presence, time.Time) {
	t.Helper()
	p := StartPresence()
	p.Ask(t0, "n1")
	require.True(t, p.Answer(t0.Add(time.Minute), "n1"))
	asked := t0.Add(ProveEvery)
	p.Tick(asked, time.Time{})
	require.True(t, p.Owed, "the next check is due ProveEvery after the last ask")
	p.Ask(asked, "n2")
	require.True(t, p.Up)
	return p, asked
}

// TestACheckWaitsForTheBatchTurnUnderWay: the finding of 2026-10-10 on two batch
// friends (dsh and opencode, mini-m5): her session took every waiting card in one
// turn of 20 to 40 minutes, the check could not be answered until it ended, and the
// bound called her down mid-turn. Deaf is a message to her not seen: while the
// daemon's own batch turn runs, no bound runs against the check; it starts when the
// turn ends.
func TestACheckWaitsForTheBatchTurnUnderWay(t *testing.T) {
	t.Parallel()

	t.Run("a turn running 20m with no answer: still up", func(t *testing.T) {
		t.Parallel()
		p, asked := upWithOpenCheck(t)
		turn := asked.Add(time.Second)
		behind, _, _ := p.Tick(turn, turn)
		assert.True(t, behind, "the open check goes behind the turn, said once")
		for at := turn; at.Sub(turn) <= 20*time.Minute; at = at.Add(time.Minute) {
			behind, restarted, _ := p.Tick(at, turn)
			assert.False(t, behind || restarted, "said once while the turn runs")
			require.True(t, p.Up, "%s into the turn: no bound runs while she cannot answer", at.Sub(turn))
		}
		assert.True(t, p.Open, "the check stays open, never asked again under the turn")
	})

	t.Run("the turn ends, no answer for the bound: down", func(t *testing.T) {
		t.Parallel()
		p, asked := upWithOpenCheck(t)
		turn, end := asked.Add(time.Second), asked.Add(30*time.Minute)
		p.Tick(turn, turn)
		_, restarted, _ := p.Tick(end, time.Time{})
		assert.True(t, restarted, "the bound starts again at the turn's end")
		assert.True(t, p.Open, "unread, it waits in her queue: not asked again before ReaskAfter")
		assert.False(t, p.Owed)
		p.Tick(end.Add(SessionBound-time.Second), time.Time{})
		assert.True(t, p.Up, "within the bound from the turn's end")
		p.Tick(end.Add(SessionBound), time.Time{})
		assert.False(t, p.Up, "the bound from the turn's end, unanswered")
		assert.Equal(t, NoSessionAnswer, p.Reason)
	})

	t.Run("the turn ends, a read check is asked again and its bound runs from the ask", func(t *testing.T) {
		t.Parallel()
		p, asked := upWithOpenCheck(t)
		p.Read = true // a headless turn of its own that the session took (ReadOnReturn) and did not answer
		turn, end := asked.Add(time.Second), asked.Add(40*time.Minute)
		p.Tick(turn, turn)
		_, restarted, _ := p.Tick(end, time.Time{})
		assert.True(t, restarted)
		assert.True(t, p.Owed, "asked again at the turn's end")
		assert.False(t, p.Open)
		again := end.Add(2 * time.Second) // the ask goes in on a later step
		p.Ask(again, "n3")
		p.Tick(again.Add(SessionBound-time.Second), time.Time{})
		assert.True(t, p.Up)
		p.Tick(again.Add(SessionBound), time.Time{})
		assert.False(t, p.Up, "unanswered for the bound from the ask")
	})

	t.Run("the turn ends, answered within the bound: up", func(t *testing.T) {
		t.Parallel()
		p, asked := upWithOpenCheck(t)
		turn, end := asked.Add(time.Second), asked.Add(25*time.Minute)
		p.Tick(turn, turn)
		p.Tick(end, time.Time{})
		assert.True(t, p.Answer(end.Add(SessionBound-time.Minute), "n2"))
		p.Tick(end.Add(SessionBound+time.Minute), time.Time{})
		assert.True(t, p.Up)
		assert.False(t, p.Behind)
	})

	t.Run("an answer during the turn clears it: nothing restarts", func(t *testing.T) {
		t.Parallel()
		p, asked := upWithOpenCheck(t)
		turn := asked.Add(time.Second)
		p.Tick(turn, turn)
		assert.True(t, p.Answer(turn.Add(10*time.Minute), "n2"))
		_, restarted, _ := p.Tick(turn.Add(30*time.Minute), time.Time{})
		assert.False(t, restarted)
		assert.True(t, p.Up)
	})

	t.Run("no turn running, no answer for the bound: down, as before", func(t *testing.T) {
		t.Parallel()
		p, asked := upWithOpenCheck(t)
		p.Tick(asked.Add(SessionBound-time.Second), time.Time{})
		assert.True(t, p.Up)
		behind, restarted, _ := p.Tick(asked.Add(SessionBound), time.Time{})
		assert.False(t, behind || restarted)
		assert.False(t, p.Up)
		assert.Equal(t, NoSessionAnswer, p.Reason)
	})

	t.Run("a turn never stretches silence after it: the quiet rule from the turn's end", func(t *testing.T) {
		t.Parallel()
		p, asked := upWithOpenCheck(t)
		p.Heard(asked.Add(time.Minute)) // she spoke, so the expiry leaves her up
		p.Tick(asked.Add(SessionBound), time.Time{})
		require.True(t, p.Up)
		require.False(t, p.Open)
		turn := asked.Add(SessionBound + time.Minute)
		behind, _, _ := p.Tick(turn, turn)
		assert.True(t, behind, "an unanswered check goes behind the turn even once its bound passed")
		p.Tick(turn.Add(SessionQuiet+SessionBound+time.Minute), turn)
		assert.True(t, p.Up, "no quiet rule while the turn runs")
		end := turn.Add(30 * time.Minute)
		p.Tick(end, time.Time{})
		p.Tick(end.Add(SessionBound), time.Time{})
		assert.False(t, p.Up, "unanswered for the bound after the turn, the session silent since")
	})
}

// TestOneShotModeTellsNoBatchTurn: one-shot mode is unchanged, its lanes are its
// turns and its primary session answers the check, so the daemon tells no batch
// turn; in batch mode it tells the turn whose Deliver is under way, once per change.
func TestOneShotModeTellsNoBatchTurn(t *testing.T) {
	t.Parallel()
	running := &turn{started: t0, running: true}
	assert.True(t, batchTurnSince(ModeOneShot, running).IsZero(), "one-shot: no batch turn")
	assert.True(t, batchTurnSince(ModeBatch, nil).IsZero())
	assert.True(t, batchTurnSince(ModeBatch, &turn{started: t0}).IsZero(), "a deferred turn in hand runs nothing")
	assert.Equal(t, t0, batchTurnSince(ModeBatch, running))

	var told []time.Time
	l := &loop{d: &Daemon{BatchTurn: func(since time.Time) { told = append(told, since) }}, mode: ModeOneShot, busy: running}
	l.tellBatchTurn()
	assert.Empty(t, told, "one-shot: nothing told")
	l.mode = ModeBatch
	l.tellBatchTurn()
	l.tellBatchTurn()
	l.busy = nil
	l.tellBatchTurn()
	assert.Equal(t, []time.Time{t0, {}}, told, "the start once, then the end")

	p := StartPresence() // and a presence that hears no turn keeps the rule it had
	p.Ask(t0, "n1")
	p.Tick(t0.Add(SessionBound), time.Time{})
	assert.Equal(t, NoSessionAnswer, p.Reason)
}

// turnApp is a headless harness whose delivery returns once the session took the
// text (ReadOnReturn) and which never answers: the daemon's batch turn (batchText)
// stays in the session until release closes or its ctx ends, and with hang every
// check after the first stays in it until its ctx ends (a deaf session, the probe of
// the cold read of 7077ed131).
type turnApp struct {
	mu        sync.Mutex
	texts     []string
	hang      bool
	release   chan struct{}
	inBatch   chan struct{}
	inCheck   chan struct{}
	cancelled int
}

const batchText = "the daemon's batch turn"

func newTurnApp(hang bool) *turnApp {
	return &turnApp{hang: hang, release: make(chan struct{}), inBatch: make(chan struct{}), inCheck: make(chan struct{})}
}

func (*turnApp) ReadOnReturn() {}

func (a *turnApp) Deliver(ctx context.Context, text string) (int, error) {
	a.mu.Lock()
	a.texts = append(a.texts, text)
	checks := 0
	for _, t := range a.texts {
		if strings.HasPrefix(t, SessionCheckPrefix) {
			checks++
		}
	}
	a.mu.Unlock()
	switch {
	case text == batchText:
		close(a.inBatch)
		select {
		case <-a.release:
		case <-ctx.Done():
		}
		return 0, nil
	case a.hang && checks >= 2:
		close(a.inCheck)
		<-ctx.Done()
		a.mu.Lock()
		a.cancelled++
		a.mu.Unlock()
		return 1, ctx.Err()
	}
	return 0, nil
}

func (a *turnApp) got() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.texts...)
}

// saidLines records the check's lines and counts those holding part.
func saidLines(sc *SessionCheck) func(part string) int {
	var mu sync.Mutex
	var lines []string
	sc.Record = func(line string) { mu.Lock(); lines = append(lines, line); mu.Unlock() }
	return func(part string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, l := range lines {
			if strings.Contains(l, part) {
				n++
			}
		}
		return n
	}
}

// TestTheSessionCheckWaitsForTheDaemonsBatchTurn: through the SessionCheck, a batch
// turn the daemon says runs and that holds the gate holds the bound; the deferral and
// the restart are said once each, the read check goes in again at the turn's end,
// and its bound then runs.
func TestTheSessionCheckWaitsForTheDaemonsBatchTurn(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	app := newTurnApp(false)
	r.sc.Deliver = r.sc.Gate(app)
	said := saidLines(r.sc)

	r.step(t, BeatEvery)
	require.Len(t, app.got(), 1)
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	up, _ := r.present(t)
	require.True(t, up)

	r.step(t, ProveEvery)
	require.Len(t, app.got(), 2, "the next check goes in")
	r.sc.BatchTurn(r.now) // the daemon's batch turn takes every waiting card
	done := make(chan struct{})
	go func() { _, _ = r.sc.Deliver.Deliver(context.Background(), batchText); close(done) }()
	<-app.inBatch // past the gate, in the session
	for range 30 {
		r.step(t, time.Minute)
		up, _ = r.present(t)
		require.True(t, up, "no bound runs while her batch turn runs")
	}
	assert.Equal(t, 1, said("waits behind the batch turn under way since"))
	assert.Len(t, app.got(), 3, "never asked again under the turn")

	close(app.release)
	<-done
	r.sc.BatchTurn(time.Time{})
	r.step(t, BeatEvery)
	assert.Equal(t, 1, said("the batch turn ended; session check n2 goes in again"))
	require.Len(t, app.got(), 4, "the read check goes in again at the turn's end")
	r.step(t, SessionBound)
	up, reason := r.present(t)
	assert.False(t, up, "unanswered for the bound after the turn")
	assert.Equal(t, NoSessionAnswer, reason)
}

// TestAHungCheckIsNotHeldBehindATurnWaitingAtTheGate: the cold read of 7077ed131
// (reader B's probe): a deaf session's check hangs in it holding the gate alone, and
// the daemon's batch turn, told running, waits at the gate behind it. That turn is
// not in the session, so it holds no bound off: down after exactly the bound, the
// down cancels the hung check, and the turn gets through the gate.
func TestAHungCheckIsNotHeldBehindATurnWaitingAtTheGate(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	app := newTurnApp(true)
	close(app.release)
	r.sc.Deliver = r.sc.Gate(app)
	r.sc.Go = func(f func()) { go f() }

	r.step(t, BeatEvery)
	require.Eventually(t, func() bool { return len(app.got()) == 1 }, 5*time.Second, time.Millisecond)
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	require.Eventually(t, func() bool { r.step(t, 0); up, _ := r.present(t); return up }, 5*time.Second, time.Millisecond)

	r.step(t, ProveEvery) // n2 goes in and hangs, holding the gate alone
	require.Eventually(t, func() bool {
		select {
		case <-app.inCheck:
			return true
		default:
			r.step(t, 0) // n1's delivery may still be leaving the gate
			return false
		}
	}, 5*time.Second, time.Millisecond)
	r.sc.BatchTurn(r.now) // the daemon starts its batch turn: told running, it waits at the gate
	done := make(chan struct{})
	go func() { _, _ = r.sc.Deliver.Deliver(context.Background(), batchText); close(done) }()
	require.Eventually(t, func() bool { r.sc.mu.Lock(); defer r.sc.mu.Unlock(); return r.sc.gated == 1 }, 5*time.Second, time.Millisecond)

	r.step(t, SessionBound-time.Second)
	up, _ := r.present(t)
	assert.True(t, up, "within the bound")
	r.step(t, time.Second)
	up, reason := r.present(t)
	assert.False(t, up, "a turn waiting at the gate holds no bound off: down after exactly the bound")
	assert.Equal(t, NoSessionAnswer, reason)
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 5*time.Second, time.Millisecond, "the down cancelled no hung check: the batch turn never got through the gate")
	app.mu.Lock()
	assert.Equal(t, 1, app.cancelled, "the hung check was cancelled by its bound")
	app.mu.Unlock()
	assert.Contains(t, app.got(), batchText)
}

// TestBackToBackTurnsHoldTheBoundOffOnlyToTheCap: turns that follow each other with
// no step between them, or with a gap and the check asked again, never hold an
// unanswered check off past BehindCap from the first time it went behind one.
func TestBackToBackTurnsHoldTheBoundOffOnlyToTheCap(t *testing.T) {
	t.Parallel()

	t.Run("no gap", func(t *testing.T) {
		t.Parallel()
		p, asked := upWithOpenCheck(t)
		first := asked.Add(time.Second)
		p.Tick(first, first)
		require.Equal(t, first, p.BehindSince)
		second := first.Add(30 * time.Minute) // the next turn begins in the step the first ended
		var capped bool
		for at := first; at.Before(first.Add(BehindCap)); at = at.Add(time.Minute) {
			turn := first
			if !at.Before(second) {
				turn = second
			}
			_, _, c := p.Tick(at, turn)
			capped = capped || c
			require.True(t, p.Up, "%s behind turns: held", at.Sub(first))
		}
		assert.False(t, capped)
		_, _, capped = p.Tick(first.Add(BehindCap), second)
		assert.True(t, capped, "the cap is said once")
		assert.False(t, p.Up, "down at the cap, the turn still running")
		assert.Equal(t, NoSessionAnswer, p.Reason)
	})

	t.Run("a gap, the check asked again, then another turn", func(t *testing.T) {
		t.Parallel()
		p, asked := upWithOpenCheck(t)
		p.Read = true
		first := asked.Add(time.Second)
		p.Tick(first, first)
		end := first.Add(30 * time.Minute)
		_, restarted, _ := p.Tick(end, time.Time{})
		require.True(t, restarted)
		require.True(t, p.Owed)
		p.Ask(end.Add(time.Second), "n3") // asked again, taken, not answered
		p.Read = true
		next := end.Add(2 * time.Second) // the next turn takes the gate at once
		behind, _, _ := p.Tick(next, next)
		assert.True(t, behind)
		assert.Equal(t, first, p.BehindSince, "a new turn or a check asked again never resets the cap")
		p.Tick(first.Add(BehindCap-time.Second), next)
		assert.True(t, p.Up)
		_, _, capped := p.Tick(first.Add(BehindCap), next)
		assert.True(t, capped)
		assert.False(t, p.Up, "down at the cap from the first time behind")
	})

	t.Run("an answer clears the cap", func(t *testing.T) {
		t.Parallel()
		p, asked := upWithOpenCheck(t)
		first := asked.Add(time.Second)
		p.Tick(first, first)
		require.True(t, p.Answer(first.Add(40*time.Minute), "n2"))
		assert.True(t, p.BehindSince.IsZero())
		assert.False(t, p.Capped)
	})
}
