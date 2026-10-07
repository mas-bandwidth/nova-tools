package member

import (
	"bytes"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// heldRunner is a fakeRunner whose Start waits for the test: it says which card it began,
// then waits for release (closed: every start passes at once).
type heldRunner struct {
	*fakeRunner
	began   chan string
	release chan struct{}
}

func newHeldRunner() *heldRunner {
	return &heldRunner{fakeRunner: newRunner(), began: make(chan string, 16), release: make(chan struct{})}
}

func (h *heldRunner) Start(p Packet) (Child, error) {
	h.began <- p.Card
	<-h.release
	return h.fakeRunner.Start(p)
}

// A Background start held in runner.Start while its card leaves the queue: the launch is busy,
// so it is not forgotten (no Ended, the lane held); once the start posts, the child is the
// launch's (never a process with no owner), and it is reaped when it ends. Rule 3.
func TestReviewABackgroundStartHeldWhileItsCardLeavesTheQueueIsNeitherForgottenNorOrphaned(t *testing.T) {
	t.Parallel()
	s, hr := newScript(), newHeldRunner()
	m := New(Config{As: "m", Width: 1, Background: true}, s, hr, &fakePusher{}, &bytes.Buffer{})
	p := pk("c1")
	s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	acted, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	require.Equal(t, 1, acted)
	require.Equal(t, "c1", <-hr.began)

	s.set("queue", 0, queueJSON(t, 7)) // the card left the queue while its start is held
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	assert.Equal(t, 1, m.Running(), "the starting launch holds its lane")
	assert.Empty(t, hr.endedLaunches(), "a busy launch is never forgotten")

	close(hr.release)
	<-m.Wake()
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	require.NotNil(t, hr.child("c1"))
	assert.Equal(t, 1, m.Running(), "the child that started is tracked, not orphaned")
	assert.Empty(t, hr.endedLaunches(), "its child still runs")

	hr.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "h"})
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	assert.Equal(t, []string{"c1:false"}, hr.endedLaunches(), "reaped once it ended")
	assert.Zero(t, m.Running())
	assert.Empty(t, s.lines("finish"), "a card no longer listed is never reported")
}

// A Background start held while its card is dealt again under the same id (gen 2): no second
// launch begins beside the busy one, the start's post goes to the gen-1 launch it was made for
// (posts are keyed by card id; safe because a busy launch is never replaced), that launch is
// reaped when its child ends, and only then the gen-2 claim is started.
func TestReviewAPostKeyedByCardGoesToTheLaunchItWasMadeForAcrossARedeal(t *testing.T) {
	t.Parallel()
	s, hr := newScript(), newHeldRunner()
	m := New(Config{As: "m", Width: 2, Background: true}, s, hr, &fakePusher{}, &bytes.Buffer{})
	p1 := pk("c1")
	s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p1)))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	<-hr.began

	p2 := pk("c1")
	p2.Gen = 2
	s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p2)))
	acted, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	assert.Zero(t, acted, "nothing is started beside a busy launch of the same card")
	assert.Equal(t, 1, m.Running())

	close(hr.release)
	<-m.Wake()
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	hr.mu.Lock()
	require.Len(t, hr.packets, 1)
	assert.Equal(t, 1, hr.packets[0].Gen, "the start that posted was gen 1's")
	hr.mu.Unlock()
	assert.Empty(t, hr.endedLaunches(), "the moved claim's child still runs: left alone")

	hr.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "h"})
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	assert.Equal(t, []string{"c1:false"}, hr.endedLaunches(), "gen 1 reaped")
	<-hr.began
	<-m.Wake()
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	hr.mu.Lock()
	require.Len(t, hr.packets, 2)
	assert.Equal(t, 2, hr.packets[1].Gen, "then gen 2 is started")
	hr.mu.Unlock()
	assert.Equal(t, 1, m.Running())
	assert.Empty(t, s.lines("finish"), "gen 1's result is never reported for gen 2")
}

// DEFECT: a draining reader still starts a child. Drain says "starts nothing (not a taken card,
// not a recovered one)", and memberLoop drains a replaced binary so it exits once Running is 0.
// A read whose stage failed is run again by the stage-retry path of the report loop
// (member.go, Tick, `m.start(*c.Packet)` in the `r.End == EndStaging && !l.retried` branch),
// which runs before the `if m.drain` return: the draining reader launches a new child (another
// harness run, up to the read's whole deadline) instead of handing the read back.
func TestReviewADrainingReaderRunsNoStageFailedReadAgain(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "r", Width: 1, Reader: true})
	p := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7, Head: "h1"}
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &p)))
	_, err := g.tickAt(t, 0)
	require.NoError(t, err)
	g.r.child("r1").end(Result{End: EndStaging, Staging: "git checkout failed", Report: "no child ran"})
	_, err = g.tickAt(t, 1)
	require.NoError(t, err)

	g.m.Drain() // the binary was replaced
	_, err = g.tickAt(t, int64(ReadStageRetry/time.Second)+2)
	require.NoError(t, err)
	assert.Equal(t, []string{"r1"}, g.r.started(), "a draining reader starts no child; the stage-failed read is handed back")
}

// heldForeverPusher is a push that does not return until the test ends.
type heldForeverPusher struct {
	began   chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *heldForeverPusher) Push(Packet, Result) Push {
	p.once.Do(func() { close(p.began) })
	<-p.release
	return Push{Refused: "released at the test's end"}
}

// DEFECT: a push that never returns no longer stops the beat. BeatStall's own words (member.go,
// the BEAT comment and BeatLoop): "A pass that has not advanced for BeatStall (a verb or a push
// that never returns) stops the beat, so a hung member still goes down and its cards are dealt
// elsewhere." With Background the push runs apart from the pass, and every Tick calls
// advanced() at its start (member.go, Tick's first line), so the pass "advances" every --every
// while the push hangs: Stalled stays 0 for ever, the member beats on, and the card holds its
// lane (busy, so never reported or reaped) until the sprint's own lateness rule, if any.
func TestReviewAHungBackgroundPushStillStopsTheBeat(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_000_000, 0)
	var at atomic.Int64
	at.Store(start.UnixNano())
	clock := func() time.Time { return time.Unix(0, at.Load()) }
	s := newScript()
	r := newRunner()
	pu := &heldForeverPusher{began: make(chan struct{}), release: make(chan struct{})}
	m := New(Config{As: "m", Width: 1, Background: true, Now: clock}, s, r, pu, &bytes.Buffer{})
	t.Cleanup(func() { close(pu.release) })
	p := pk("c1")
	s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	_, err := m.Tick(start)
	require.NoError(t, err)
	<-m.Wake() // the start posted
	_, err = m.Tick(start)
	require.NoError(t, err)
	require.NotNil(t, r.child("c1"))
	r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "abc"})
	_, err = m.Tick(start)
	require.NoError(t, err)
	<-pu.began // the push is in flight and never returns

	// the loop passes every --every; the push is still hung BeatStall later
	// a push inside its budgets never stops the beat: BeatStall later the member still beats
	at.Store(start.Add(BeatStall + time.Minute).UnixNano())
	_, err = m.Tick(start.Add(BeatStall + time.Minute))
	require.NoError(t, err)
	assert.Zero(t, m.Stalled(), "a slow push that may still be inside its budgets does not stop the beat")
	for _, d := range []time.Duration{LongStall / 2, LongStall + time.Minute} {
		at.Store(start.Add(d).UnixNano())
		_, err = m.Tick(start.Add(d))
		require.NoError(t, err)
	}
	assert.Equal(t, 1, m.Running(), "the hung push's card holds its lane")
	assert.Positive(t, m.Stalled(), "a push that never returns stops the beat (BeatStall)")
}

// liveSprint is a sprint with state: its cards (ready or working, at a generation) on one
// member; take moves ready cards to working; a finish of a working card at its generation
// removes it (exit 0), any other is refused (exit 1).
type liveSprint struct {
	mu       sync.Mutex
	t        *testing.T
	col      map[string]string
	finished []string
}

func (s *liveSprint) pkt(id string) Packet { return pk(id) }

func (s *liveSprint) Run(args ...string) (int, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch args[0] {
	case "queue":
		ids := make([]string, 0, len(s.col))
		for id := range s.col {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		var cs []queueCard
		for _, id := range ids {
			p := s.pkt(id)
			cs = append(cs, queueCard{ID: id, Col: s.col[id], Gen: 1, Packet: &p})
		}
		return 0, []byte(queueJSON(s.t, 7, cs...))
	case "take":
		n, _ := strconv.Atoi(args[slices.Index(args, "--limit")+1])
		ids := make([]string, 0, len(s.col))
		for id, c := range s.col {
			if c == "ready" {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		var ps []Packet
		for _, id := range ids {
			if len(ps) == n {
				break
			}
			s.col[id] = "working"
			ps = append(ps, s.pkt(id))
		}
		return 0, []byte(takeJSON(s.t, ps...))
	case "finish":
		id, _, _ := strings.Cut(args[3], "@")
		if s.col[id] != "working" {
			return 1, []byte("not working")
		}
		delete(s.col, id)
		s.finished = append(s.finished, id)
	}
	return 0, nil
}

// A Background member over a live sprint: twelve cards through width four, children ending,
// pushes side by side. Under -race: every card is finished exactly once, the lanes never pass
// the width, nothing is reaped. Rules 2, 4 and 7.
func TestReviewABackgroundMemberRunsEveryCardOnceWithinItsWidth(t *testing.T) {
	t.Parallel()
	s := &liveSprint{t: t, col: map[string]string{}}
	for i := range 12 {
		s.col[fmt.Sprintf("c%02d", i)] = "ready"
	}
	r := newRunner()
	pu := &fakePusher{def: Push{Sha: fullSha}, yield: 50}
	m := New(Config{As: "m", Width: 4, Background: true}, s, r, pu, &bytes.Buffer{})
	most := 0
	for i := 0; i < 200_000; i++ {
		_, err := m.Tick(time.Unix(0, 0))
		require.NoError(t, err)
		most = max(most, m.Running())
		r.mu.Lock()
		for _, c := range r.children {
			if !c.Done() {
				c.end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "abc"})
			}
		}
		r.mu.Unlock()
		s.mu.Lock()
		left := len(s.col)
		s.mu.Unlock()
		if left == 0 && m.Running() == 0 {
			break
		}
		select {
		case <-m.Wake():
		default:
			runtime.Gosched()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	assert.Empty(t, s.col, "every card finished")
	assert.Len(t, s.finished, 12)
	seen := map[string]bool{}
	for _, id := range s.finished {
		assert.False(t, seen[id], "finished twice: %s", id)
		seen[id] = true
	}
	assert.LessOrEqual(t, most, 4, "the lanes never pass the width")
	assert.Len(t, r.endedLaunches(), 12)
	for _, e := range r.endedLaunches() {
		assert.True(t, strings.HasSuffix(e, ":false"), "an ok finish is not failed: %s", e)
	}
}
