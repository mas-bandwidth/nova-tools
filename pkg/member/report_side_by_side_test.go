package member

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// barrierSprint is the package's scriptSprint with a finish that holds every call until
// all n are in flight at once (a barrier), so a serial stretch of finishes cannot reach
// n and a side-by-side one reaches it at once. The other verbs answer as the script.
type barrierSprint struct {
	*scriptSprint
	n       int
	mu      sync.Mutex
	in      int
	most    int
	release chan struct{}
	hit     bool
}

func (b *barrierSprint) Run(args ...string) (int, []byte) {
	if verbOf(args) != "finish" {
		return b.scriptSprint.Run(args...)
	}
	b.mu.Lock()
	b.in++
	if b.in > b.most {
		b.most = b.in
	}
	if b.in == b.n && !b.hit {
		b.hit = true
		close(b.release)
	}
	b.mu.Unlock()
	<-b.release
	b.mu.Lock()
	b.in--
	b.mu.Unlock()
	return b.scriptSprint.Run(args...)
}

// TestReportsRunSideBySideInOnePass pins that a pass reports every ended card side by
// side, bounded by pushWidth: eight finishes that each wait for all eight to be in
// flight all run in one pass. Red at BASE: reportOne ran each finish one after another,
// so only one was ever in flight and the barrier was never reached.
func TestReportsRunSideBySideInOnePass(t *testing.T) {
	t.Parallel()
	const n = 8
	s := &barrierSprint{scriptSprint: newScript(), n: n, release: make(chan struct{})}
	rn, out := newRunner(), &bytes.Buffer{}
	m := New(Config{As: "m", Width: 2 * n}, s, rn, &fakePusher{def: Push{Sha: fullSha}}, out)

	cards := make([]queueCard, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("c%d", i)
		p := pk(id)
		cards[i] = working(id, 1, &p)
	}
	s.set("queue", 0, queueJSON(t, 7, cards...))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)

	for i := 0; i < n; i++ {
		rn.child(fmt.Sprintf("c%d", i)).end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "done"})
	}
	s.reset()
	acted, err := m.Tick(time.Unix(0, 0).Add(time.Second))
	require.NoError(t, err)

	s.mu.Lock()
	most, hit := s.most, s.hit
	s.mu.Unlock()
	assert.Equal(t, n, most, "the most finishes in flight at once, want %d", n)
	assert.True(t, hit, "the barrier was reached: all %d finishes were in flight together", n)
	assert.Len(t, s.lines("finish"), n, "one finish per ended card")
	assert.Equal(t, n, acted, "acted=%d, want %d", acted, n)
	assert.Equal(t, 0, m.Running(), "every reported launch is forgotten")
}

// orderSprint records whether the take ran before any finish returned: a finish holds on
// release until the take closes it, and the take notes how many finishes had returned when
// it was called. No clock is read.
type orderSprint struct {
	*scriptSprint
	mu      sync.Mutex
	done    int
	saw     bool
	at      int
	release chan struct{}
	once    sync.Once
}

func (o *orderSprint) Run(args ...string) (int, []byte) {
	switch verbOf(args) {
	case "finish":
		<-o.release
		o.mu.Lock()
		o.done++
		o.mu.Unlock()
		return o.scriptSprint.Run(args...)
	case "take":
		o.mu.Lock()
		o.saw, o.at = true, o.done
		o.mu.Unlock()
		o.once.Do(func() { close(o.release) })
		return o.scriptSprint.Run(args...)
	}
	return o.scriptSprint.Run(args...)
}

// TestTakeRunsBeforeTheReportsReturn pins step 5: the take for the lanes free now runs in
// the same pass, before any report returns, so a lane freed by a report is filled by the
// next pass and never by waiting for every report. Four finishes held until the take and
// four free lanes:
// the take is called while none of the finishes has returned. Red at BASE: the reports ran
// to completion before the take.
func TestTakeRunsBeforeTheReportsReturn(t *testing.T) {
	t.Parallel()
	s := &orderSprint{scriptSprint: newScript(), release: make(chan struct{})}
	rn, out := newRunner(), &bytes.Buffer{}
	m := New(Config{As: "m", Width: 8}, s, rn, &fakePusher{def: Push{Sha: fullSha}}, out)

	var cards []queueCard
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("w%d", i)
		p := pk(id)
		cards = append(cards, working(id, 1, &p))
	}
	s.set("queue", 0, queueJSON(t, 7, cards...))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)

	for i := 0; i < 4; i++ {
		rn.child(fmt.Sprintf("w%d", i)).end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "done"})
	}
	// four more lanes are free: four ready cards the take begins this pass
	readyCards := make([]queueCard, 0, 8)
	readyCards = append(readyCards, cards...)
	var free []Packet
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("r%d", i)
		q := ready(id)
		readyCards = append(readyCards, q)
		free = append(free, pk(id))
	}
	s.set("queue", 0, queueJSON(t, 7, readyCards...))
	s.set("take", 0, takeJSON(t, free...))
	s.reset()

	_, err = m.Tick(time.Unix(0, 0).Add(time.Second))
	require.NoError(t, err)

	s.mu.Lock()
	saw, at, done := s.saw, s.at, s.done
	s.mu.Unlock()
	require.True(t, saw, "the take ran")
	assert.Equal(t, 0, at, "the take ran before any of the %d finishes returned (done=%d)", done, done)
}

// TestARefusedReadVerdictIsForgottenNotSpent pins STEP (1)'s "the state changes reportOne
// makes today ... applied by collect from the posted result", exactly as today: a read whose
// verdict verb (read --ok / read --broken) exits 1 is refused, and reportOne today reached
// m.forget(id, false) -- the launch is dropped and the reading card is recovered on a later
// pass. The side-by-side applyReport must not keep it spent: spent is "a read whose child
// ended with no verdict" (member.go), only the read --return refusal. Red at HEAD: applyReport
// retained the refused read-verdict launch in m.running with l.spent set, so this member never
// reports or recovers it again.
func TestARefusedReadVerdictIsForgottenNotSpent(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1})
	a := Packet{Card: "p1.r1.m", Kind: "read", As: "m", Primary: "p1", Attempt: 1, Gen: 1, Epoch: 7, Head: fullSha}
	g.s.set("queue", 0, queueJSON(t, 7, ready(a.Card)))
	g.s.set("take", 0, takeJSON(t, a))
	_, err := g.tick(t)
	require.NoError(t, err)
	require.Equal(t, []string{a.Card}, g.r.started(), "the read runs")

	g.r.child(a.Card).end(Result{Ran: true, OK: true, Verdict: "ok", Report: "clean"})
	g.s.set("queue", 0, queueJSON(t, 7, working(a.Card, 1, &a)))
	g.s.set("report", 1, "REFUSED read: no such card")
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	require.Len(t, g.s.lines("report"), 1, "the read verdict is reported")
	// the launch itself, not Running() (which skips a spent one): a spent launch is still
	// held and never retried by this member
	assert.Empty(t, g.m.running, "a refused read verdict is forgotten, not kept spent")
}

// TestAReadReturnRefusalIsQuietAndSpent pins STEP (1)'s "the state changes reportOne makes
// today ... applied by collect from the posted result", exactly as today: a read that reached
// no verdict and whose `read --return` verb exits 1 is refused; reportOne's return path set
// the launch spent (the read stays for the lateness rule) and printed NO refusal NOTE, unlike
// a refused read verdict. Red at HEAD: applyReport printed the NOTE for a refused read
// --return.
func TestAReadReturnRefusalIsQuietAndSpent(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "r", Width: 1, Reader: true})
	a := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &a)))
	_, err := g.tick(t)
	require.NoError(t, err)
	require.Equal(t, []string{"r1"}, g.r.started(), "the read runs")

	g.r.child("r1").end(Result{Ran: false, Report: "STAGE FAIL no bench mirror"})
	g.s.set("queue", 0, queueJSON(t, 7, working("r1", 1, &a)))
	g.s.set("return", 1, "REFUSED read --return: no such card")
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)

	require.Len(t, g.s.lines("return"), 1, "the read is returned")
	require.Len(t, g.m.running, 1, "a refused read --return leaves the launch spent")
	assert.True(t, g.m.running["r1"].spent, "the launch is spent")
	assert.NotContains(t, g.out.String(), "refused:",
		"reportOne printed no refusal note for a read --return")
}

// TestAReadReturnNoAnswerNamesTheReturnVerb pins STEP (1)'s noAnswer note, exactly as today:
// the store not answering a `read --return` is noted as `read --return <id>`, the label
// reportOne passed, never the bare `read <id>`.
func TestAReadReturnNoAnswerNamesTheReturnVerb(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "r", Width: 1, Reader: true})
	a := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &a)))
	_, err := g.tick(t)
	require.NoError(t, err)
	require.Equal(t, []string{"r1"}, g.r.started(), "the read runs")

	g.r.child("r1").end(Result{Ran: false, Report: "STAGE FAIL no bench mirror"})
	g.s.set("queue", 0, queueJSON(t, 7, working("r1", 1, &a)))
	g.s.set("return", 2, "the store did not answer")
	g.s.reset()
	_, err = g.tick(t)
	require.Error(t, err, "a store that did not answer is reported")

	assert.Contains(t, g.out.String(), "NOTE read --return r1: the store did not answer",
		"the no-answer note names the read --return verb")
}

// TestARefusedFinishIsRetriedByteForByteNextPass pins that a refused finish of a pushed
// generation is sent again, byte for byte, by a later pass, and the run is never repeated
// (retryFinishes; launch.push).
func TestARefusedFinishIsRetriedByteForByteNextPass(t *testing.T) {
	t.Parallel()
	g, _ := pushRig(t, &fakePusher{def: Push{Sha: fullSha}}, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "landed"})
	g.s.set("finish", 1, "REFUSED finish: no such column")
	_, err := g.m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	require.Len(t, g.s.lines("finish"), 1)

	g.s.set("finish", 0, "")
	_, err = g.m.Tick(time.Unix(0, 0).Add(FinishRefusedRetry + time.Second))
	require.NoError(t, err)
	fin := g.s.lines("finish")
	require.Len(t, fin, 2, "the refused finish is sent again")
	assert.Equal(t, fin[0], fin[1], "the second finish is the first, byte for byte")
	assert.Equal(t, []string{"c1"}, g.r.started(), "the run is never repeated")
}

// TestAStopWordStartsNoChildWhileAReportRuns pins the stop's reach: a stop word set while
// reports are in flight still lets the report run (each report is its own card's words)
// and starts no child (NoLaunchAfterStop, GateBeforeStart; tla/StopCancels.tla).
func TestAStopWordStartsNoChildWhileAReportRuns(t *testing.T) {
	t.Parallel()
	g, p := pushRig(t, &fakePusher{def: Push{Sha: fullSha}}, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "landed"})
	before := len(g.r.started())

	g.m.stoppedWord.Store(true)
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p), ready("c9")))
	g.s.set("take", 0, takeJSON(t, pk("c9")))
	g.s.reset()

	_, err := g.m.Tick(time.Unix(0, 0).Add(time.Second))
	require.NoError(t, err)
	assert.Len(t, g.s.lines("finish"), 1, "the report still runs its own card's words")
	assert.Equal(t, before, len(g.r.started()), "no child is started under the stop word")
}

// gateSprint is the package's scriptSprint with a finish that says it is in flight
// (began) and holds until release: a report held while the queue shows the claim moved.
type gateSprint struct {
	*scriptSprint
	began   chan string
	release chan struct{}
}

func (g *gateSprint) Run(args ...string) (int, []byte) {
	if verbOf(args) == "finish" {
		g.began <- args[0]
		<-g.release
	}
	return g.scriptSprint.Run(args...)
}

// TestAMovedClaimWhileReportingIsReapedNotApplied pins STEP (4): a report whose claim moved
// while it ran is collected as reaped, not applied (m.forget + the FinishReaped line), exactly
// as the in-pass moved-claim reap does. In a Background member the report is launched by the
// pass that still showed the old claim; the claim then moves and the report posts during the
// inter-tick sleep, before any pass reads the moved queue. The pass that collects it must reap
// it then, against the queue it just read, never apply a finish for the old generation/epoch.
// Red at HEAD: the collecting pass's first collect (before its queue read) applied the posted
// report against the previous pass's queue, printing `finish c1 ok=true` instead of reaping.
func TestAMovedClaimWhileReportingIsReapedNotApplied(t *testing.T) {
	t.Parallel()
	gs := &gateSprint{scriptSprint: newScript(), began: make(chan string, 4), release: make(chan struct{})}
	rn, out := newRunner(), &bytes.Buffer{}
	m := New(Config{As: "m", Width: 1, Background: true}, gs, rn, &fakePusher{def: Push{Sha: fullSha}}, out)

	old := pk("c1") // gen 1, epoch 7
	gs.set("queue", 0, queueJSON(t, 7, working("c1", 1, &old)))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	<-m.Wake() // the background start posted
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	require.NotNil(t, rn.child("c1"), "the child started")

	rn.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "done"})
	_, err = m.Tick(time.Unix(0, 0)) // the end begins (its push runs)
	require.NoError(t, err)
	<-m.Wake() // the end posted

	// the pass that collects the end launches the finish report, held on the gate
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	require.Equal(t, "finish", <-gs.began, "the finish report is in flight")

	// the claim moves while the report runs: the queue shows gen 2, and the report
	// posts before any pass has read that queue (no Tick in between). The store refuses
	// the finish for gen 1 as its live-generation check does (nova-sprint
	// internal/sprint/steps_work.go finishPlan, liveGen: a finish whose <card>@<gen> is
	// not the live generation is refused)
	moved := pk("c1")
	moved.Gen = 2
	gs.set("queue", 0, queueJSON(t, 7, working("c1", 2, &moved)))
	gs.set("finish", 1, "REFUSED finish: c1@1 is not the live generation (2)")

	close(gs.release)
	<-m.Wake()                       // the report posted
	_, err = m.Tick(time.Unix(0, 0)) // collect: reap, never apply
	require.NoError(t, err)

	assert.Contains(t, out.String(), "reaped c1: the claim moved", "the moved report is reaped")
	assert.NotContains(t, out.String(), "finish c1 ok=true", "a moved claim's report is never applied")
	assert.NotContains(t, out.String(), "refused:", "a reaped report's refusal is neither said nor remembered for a retry")
	assert.Empty(t, m.running, "the reaped launch is forgotten")
	assert.Equal(t, []string{"c1:false"}, rn.endedLaunches(), "reaped: nobody's result")
}

// TestAnUnansweredReportWhoseClaimMovedKeepsTheRunAsFailed pins STEP (4)'s third case
// (Stella's follow-up on #5625): a report the store did not answer (exit 2) may or may not
// have been committed; the store may have taken a failed finish and redealt the card at
// gen+1, every reply lost on the way back. The collecting pass then sees the claim moved,
// and the outcome is unknown: the run is kept as failed (its directory stays for a person)
// and never reaped as "the claim moved", which would forget it as clean. Red before the
// fix: any non-zero exit with a moved claim was reaped, Ended(c1, failed=false).
func TestAnUnansweredReportWhoseClaimMovedKeepsTheRunAsFailed(t *testing.T) {
	t.Parallel()
	gs := &gateSprint{scriptSprint: newScript(), began: make(chan string, 4), release: make(chan struct{})}
	rn, out := newRunner(), &bytes.Buffer{}
	m := New(Config{As: "m", Width: 1, Background: true}, gs, rn, &fakePusher{def: Push{Sha: fullSha}}, out)

	old := pk("c1") // gen 1, epoch 7
	gs.set("queue", 0, queueJSON(t, 7, working("c1", 1, &old)))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	<-m.Wake() // the background start posted
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	require.NotNil(t, rn.child("c1"), "the child started")

	rn.child("c1").end(Result{Ran: true, OK: false, Shaped: true, Verdict: "notdone", Head: "0123456", Report: "not done"})
	_, err = m.Tick(time.Unix(0, 0)) // the end begins (its push runs)
	require.NoError(t, err)
	<-m.Wake() // the end posted

	_, err = m.Tick(time.Unix(0, 0)) // the finish report, held on the gate
	require.NoError(t, err)
	require.Equal(t, "finish", <-gs.began, "the finish report is in flight")

	// the store commits the failed finish and redeals the card at gen 2, and every reply
	// is lost: the member sees no answer (exit 2) and then the moved claim
	redealt := pk("c1")
	redealt.Gen = 2
	gs.set("queue", 0, queueJSON(t, 7, working("c1", 2, &redealt)))
	gs.set("finish", 2, "the store did not answer")

	close(gs.release)
	<-m.Wake() // the report posted
	_, err = m.Tick(time.Unix(0, 0))
	assert.Error(t, err, "the collecting pass says the store did not answer")

	assert.Contains(t, out.String(), "finish c1 ok=false exit=2", "the unanswered report's own line")
	assert.Contains(t, out.String(), "the outcome is unknown, so the run is kept as failed", "the uncertainty is said")
	assert.NotContains(t, out.String(), "reaped c1", "an unanswered report is never reaped as a moved claim")
	assert.Contains(t, rn.endedLaunches(), "c1:true", "the run is kept as failed")
	assert.NotContains(t, rn.endedLaunches(), "c1:false", "the run is never forgotten as clean")
	assert.Len(t, gs.lines("finish"), 1, "the finish is not sent again to a claim that is gone")
}

// TestAnAcceptedReportIsAppliedThoughTheQueueThenMoved pins the other side of STEP (4): a
// report the store ACCEPTED is applied, never reaped, though the queue the collecting pass
// reads lists the card at a new generation. The store's finish refuses a stale claim
// (nova-sprint finishPlan, liveGen), so an accepted finish was the card's claim when it
// landed, and a move seen after it is the store acting on that very report: a failed finish
// is redealt at gen+1 (nova-sprint redeal) before the member's next queue read. Red before
// the fix: the reap ran on every report whatever its exit code, so the accepted failed
// finish was reaped as "the claim moved" and its run forgotten as clean (Ended failed=false),
// losing the failed run's directory a person may want to inspect.
func TestAnAcceptedReportIsAppliedThoughTheQueueThenMoved(t *testing.T) {
	t.Parallel()
	gs := &gateSprint{scriptSprint: newScript(), began: make(chan string, 4), release: make(chan struct{})}
	rn, out := newRunner(), &bytes.Buffer{}
	m := New(Config{As: "m", Width: 1, Background: true}, gs, rn, &fakePusher{def: Push{Sha: fullSha}}, out)

	old := pk("c1") // gen 1, epoch 7
	gs.set("queue", 0, queueJSON(t, 7, working("c1", 1, &old)))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	<-m.Wake() // the background start posted
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	require.NotNil(t, rn.child("c1"), "the child started")

	// the run fails: a notdone verdict is finished failed
	rn.child("c1").end(Result{Ran: true, OK: false, Shaped: true, Verdict: "notdone", Head: "0123456", Report: "not done"})
	_, err = m.Tick(time.Unix(0, 0)) // the end begins (its push runs)
	require.NoError(t, err)
	<-m.Wake() // the end posted

	_, err = m.Tick(time.Unix(0, 0)) // the finish report, held on the gate
	require.NoError(t, err)
	require.Equal(t, "finish", <-gs.began, "the finish report is in flight")

	// the store accepts the failed finish (exit 0) and redeals the card to this member at
	// gen 2 before the member's next queue read
	redealt := pk("c1")
	redealt.Gen = 2
	gs.set("queue", 0, queueJSON(t, 7, working("c1", 2, &redealt)))

	close(gs.release)
	<-m.Wake()                       // the report posted
	_, err = m.Tick(time.Unix(0, 0)) // collect: apply, never reap
	require.NoError(t, err)

	assert.Contains(t, out.String(), "finish c1 ok=false exit=0", "the accepted finish is applied")
	assert.NotContains(t, out.String(), "reaped c1", "an accepted report is never reaped")
	assert.Contains(t, rn.endedLaunches(), "c1:true", "the failed run is kept as failed")
	assert.NotContains(t, rn.endedLaunches(), "c1:false", "the failed run is never forgotten as clean")
}

// TestABackgroundReportNoAnswerIsTheCollectingPassesError pins STEP (4)/finding (2): a
// Background member's report is collected by a later pass than the one that launched it, and
// the store-did-not-answer note is that collecting pass's, so the collecting pass returns the
// error (cmd/nova-swarm logs the "store did not answer" line for the verb). Red at HEAD: the
// note was applied to the launching (already returned) pass's unanswered slice, so the
// collecting pass returned no error.
func TestABackgroundReportNoAnswerIsTheCollectingPassesError(t *testing.T) {
	t.Parallel()
	gs := &gateSprint{scriptSprint: newScript(), began: make(chan string, 4), release: make(chan struct{})}
	rn, out := newRunner(), &bytes.Buffer{}
	m := New(Config{As: "m", Width: 1, Background: true}, gs, rn, &fakePusher{def: Push{Sha: fullSha}}, out)

	p := pk("c1")
	gs.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	<-m.Wake()
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	require.NotNil(t, rn.child("c1"))

	rn.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "done"})
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	<-m.Wake()

	gs.set("finish", 2, "the store did not answer")
	_, err = m.Tick(time.Unix(0, 0)) // collect the end, launch the finish (held)
	require.NoError(t, err)
	require.Equal(t, "finish", <-gs.began, "the finish report is in flight")

	close(gs.release)
	<-m.Wake() // the report posted
	_, err = m.Tick(time.Unix(0, 0))
	require.Error(t, err, "the collecting pass returns the store-did-not-answer error")
	assert.Contains(t, out.String(), "NOTE finish c1: the store did not answer",
		"the no-answer note names the verb the store did not answer")
}

// answerGate is gateSprint as the member's sprintwire.Worker is: an AnswerSprint that says
// what became of the finish (finishAnswer) apart from its exit code.
type answerGate struct {
	*gateSprint
	finishAnswer sprintwire.Answer
}

func (g *answerGate) RunAnswer(args ...string) (int, []byte, sprintwire.Answer) {
	code, out := g.gateSprint.Run(args...)
	if verbOf(args) == "finish" {
		return code, out, g.finishAnswer
	}
	return code, out, sprintwire.Classify(code, out, nil, true)
}

// reportWithMovedClaim runs a Background member's failed finish through the gate, moves the
// claim to gen 2 while it is in flight, has the finish exit code with out, and collects it
// in the next pass: it returns what the member printed, its ended launches and that pass's
// error. sp is the Sprint the member is given (gs itself, or gs wrapped).
func reportWithMovedClaim(t *testing.T, gs *gateSprint, sp Sprint, code int, out string) (string, []string, error) {
	t.Helper()
	rn, buf := newRunner(), &bytes.Buffer{}
	m := New(Config{As: "m", Width: 1, Background: true}, sp, rn, &fakePusher{def: Push{Sha: fullSha}}, buf)
	old := pk("c1") // gen 1, epoch 7
	gs.set("queue", 0, queueJSON(t, 7, working("c1", 1, &old)))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	<-m.Wake() // the background start posted
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	require.NotNil(t, rn.child("c1"), "the child started")
	rn.child("c1").end(Result{Ran: true, OK: false, Shaped: true, Verdict: "notdone", Head: "0123456", Report: "not done"})
	_, err = m.Tick(time.Unix(0, 0)) // the end begins (its push runs)
	require.NoError(t, err)
	<-m.Wake() // the end posted
	_, err = m.Tick(time.Unix(0, 0)) // the finish report, held on the gate
	require.NoError(t, err)
	require.Equal(t, "finish", <-gs.began, "the finish report is in flight")
	moved := pk("c1")
	moved.Gen = 2
	gs.set("queue", 0, queueJSON(t, 7, working("c1", 2, &moved)))
	gs.set("finish", code, out)
	close(gs.release)
	<-m.Wake() // the report posted
	_, err = m.Tick(time.Unix(0, 0))
	return buf.String(), rn.endedLaunches(), err
}

// TestAMovedReportIsJudgedByWhatBecameOfItsWriteNotItsExitCode pins collect's three classes
// on a moved claim, each from nova-sprint's own answers (sprintwire.Classify), never the exit
// code alone (Stella's read of #5625 at a0d0429e; the cold reader's CutError):
//   - a definite refusal is reaped, even when it exits 2 (cmd/nova-sprint/main.go refuse(),
//     "nova-sprint finish REFUSED: ...; nothing was changed");
//   - an unknown outcome keeps the run as failed: no answer (the worker's own 2), the store
//     not confirming the write (exit 2, changed=unknown), an operation cut after an earlier
//     manifest applied (exit 1, "cut at table", fenced for nova-sprint repair);
//   - an accepted finish is applied (TestAnAcceptedReportIsAppliedThoughTheQueueThenMoved).
//
// The typed answer (an AnswerSprint, the member's sprintwire.Worker) decides when the Sprint
// gives one; the printed words decide for one that does not. Red at a0d0429e: the exit-2
// refusal was kept as uncertain ("the outcome is unknown"), and the cut, exit 1, was reaped.
func TestAMovedReportIsJudgedByWhatBecameOfItsWriteNotItsExitCode(t *testing.T) {
	t.Parallel()
	const (
		refused2 = "nova-sprint finish REFUSED: a report names the epoch its cards were handed at: --epoch <n>; nothing was changed"
		unknown2 = "FINISH FAILED moved=0 refused=0 notes=0 changed=unknown; nova-sprint finish: the store did not confirm the write (changed=unknown)"
		cut1     = "FINISH FAILED moved=0 refused=0 notes=0 changed=no; nova-sprint finish: operation o1 cut at table cards: write failed; run: nova-sprint repair"
	)
	for _, tc := range []struct {
		name   string
		code   int
		out    string
		typed  bool
		answer sprintwire.Answer
		reaped bool
	}{
		{"a refusal that exits 2, read from its words", 2, refused2, false, 0, true},
		{"a refusal that exits 2, the typed answer", 2, refused2, true, sprintwire.Refused, true},
		{"no answer, the worker's own 2, typed", 2, "the sprint server did not answer", true, sprintwire.Unknown, false},
		{"the store did not confirm, exit 2", 2, unknown2, false, 0, false},
		{"an operation cut, exit 1", 1, cut1, false, 0, false},
		{"an operation cut, exit 1, typed", 1, cut1, true, sprintwire.Unknown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gs := &gateSprint{scriptSprint: newScript(), began: make(chan string, 4), release: make(chan struct{})}
			var sp Sprint = gs
			if tc.typed {
				sp = &answerGate{gateSprint: gs, finishAnswer: tc.answer}
			}
			out, ended, err := reportWithMovedClaim(t, gs, sp, tc.code, tc.out)
			if tc.reaped {
				assert.NoError(t, err, "a refusal is no unanswered verb")
				assert.Contains(t, out, "reaped c1: the claim moved", "a definite refusal is reaped")
				assert.NotContains(t, out, "the outcome is unknown", "a definite refusal is no unknown outcome")
				assert.Equal(t, []string{"c1:false"}, ended, "reaped: nobody's result")
				return
			}
			assert.NotContains(t, out, "reaped c1", "an unknown outcome is never reaped")
			assert.Contains(t, out, "the outcome is unknown, so the run is kept as failed", "the uncertainty is said")
			assert.Equal(t, []string{"c1:true"}, ended, "the run is kept as failed")
			if tc.code == 2 {
				assert.Error(t, err, "the collecting pass says the store did not answer or confirm")
			}
		})
	}
}
