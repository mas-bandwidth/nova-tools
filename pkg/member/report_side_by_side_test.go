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
	select {
	case <-b.release:
	case <-time.After(2 * time.Second):
	}
	b.mu.Lock()
	b.in--
	b.mu.Unlock()
	return b.scriptSprint.Run(args...)
}

// TestReportsRunSideBySideInOnePass pins that a pass reports every ended card side by
// side, bounded by pushWidth: eight finishes that each wait for all eight to be in
// flight all run in one pass. Red at BASE: reportOne ran each finish one after another,
// so only one was ever in flight and the barrier timed out.
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

// orderSprint records whether the take ran before any finish returned: a finish sleeps the
// delay given, and the take notes how many finishes had returned when it was called.
type orderSprint struct {
	*scriptSprint
	delay time.Duration
	mu    sync.Mutex
	done  int
	saw   bool
	at    int
}

func (o *orderSprint) Run(args ...string) (int, []byte) {
	switch verbOf(args) {
	case "finish":
		time.Sleep(o.delay)
		o.mu.Lock()
		o.done++
		o.mu.Unlock()
		return o.scriptSprint.Run(args...)
	case "take":
		o.mu.Lock()
		o.saw, o.at = true, o.done
		o.mu.Unlock()
		return o.scriptSprint.Run(args...)
	}
	return o.scriptSprint.Run(args...)
}

// TestTakeRunsBeforeTheReportsReturn pins step 5: the take for the lanes free now runs in
// the same pass, before any report returns, so a lane freed by a report is filled by the
// next pass and never by waiting for every report. Four slow finishes and four free lanes:
// the take is called while none of the finishes has returned. Red at BASE: the reports ran
// to completion before the take.
func TestTakeRunsBeforeTheReportsReturn(t *testing.T) {
	t.Parallel()
	s := &orderSprint{scriptSprint: newScript(), delay: 500 * time.Millisecond}
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
