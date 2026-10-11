package member

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
