package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The first real sprint's start: other streams' work is admitted, then a round-2
// sentinel at the head of an empty stream. With nothing placed before it and no need of
// its own it is not reached while other work moves: it is simply next
// (docs/SPEC-SPRINT.md section 16). No "sentinel reached" judgment is written for it, by
// the add or by any tick, and release lands it all the same. One with cards placed
// before it is reached when they have landed, as before.
func TestASentinelWithNothingBeforeItIsNextNotReached(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	ta.ok("add --stream s1 --count 2 --actor lead")
	out := ta.ok("add --stream docs --sentinel docs-round2 --actor lead")
	assert.NotContains(t, out, "reached", "the add marks it reached")
	ta.ok("add --stream docs d1 --actor lead")
	ta.ok("start --actor lead")
	ta.ok("tick")
	ta.ok("tick")
	for _, g := range ta.inboxGroups() {
		assert.NotEqual(t, sprint.NSentinelReached, g.Type, "reached with nothing before it: %+v", g)
	}
	assert.Empty(t, ta.primary("docs-round2").F("reached"))
	out = ta.ok("release docs-round2 --reason 'round 1 is read' --actor lead")
	assert.Contains(t, out, "sentinel docs-round2 waiting -> landed (released by lead); 1 cards are now ready")
	assert.Contains(t, out, "d1 waiting -> ready")
	ta.ok("tick")
	assert.NotEqual(t, sprint.Waiting, ta.primary("d1").Col, "the machine's pump applies the release")
}

// The sequence of the first real sprint: a round-2 sentinel added at the head of an empty
// stream, then the round-1 cards placed before it. It is reached when they have landed,
// and not before.
func TestASentinelIsReachedWhenTheCardsPlacedBeforeItLand(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	ta.ok("add --stream s1 --sentinel stop --actor lead")
	ta.ok("add --stream s1 s1-1 --before stop --actor lead")
	assert.False(t, hasGroup(ta.inboxGroups(), sprint.NSentinelReached), "reached before the card placed before it landed")
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1")
	ta.ok("ask --actor lead")
	ta.ok("read --as reader-a --ok s1-1.r1.reader-a")
	ta.ok("read --as reader-b --ok s1-1.r1.reader-b")
	ta.ok("accept s1-1 --actor lead")
	ta.ok("merge --stream s1")
	require.True(t, hasGroup(ta.inboxGroups(), sprint.NSentinelReached), "reached once the card before it landed")
	assert.Contains(t, ta.group(sprint.NSentinelReached, "s1").What, "sentinel stop reached: 1 cards of s1 have landed")
}

// wait <judgment> --for quiets it for the whole period: it is not overdue, and it is
// listed after every judgment that is not waiting, marked quiet until its review time,
// so it does not sit at the top of every inbox read (docs/SPEC-SPRINT.md section 8).
func TestAWaitQuietsAJudgmentForTheWholePeriod(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	ta.ok("add --stream s1 --count 1 --actor lead")
	ta.ok("add --stream s2 --count 1 --actor lead")
	ta.deal(2)
	ta.ok("take --as m1 s1-1.w1@1 s2-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'verdict not-done'")
	ta.mu.Lock()
	ta.now = ta.now.Add(time.Minute)
	ta.mu.Unlock()
	ta.ok("finish --as m1 s2-1.w1@1 --failed --report 'verdict not-done'")
	g := ta.group(sprint.NWorkFailed, "s1")
	ta.ok("wait " + g.ID + " --for 2h --actor lead")
	ta.ok("start --actor lead")
	for range 4 {
		ta.mu.Lock()
		ta.now = ta.now.Add(10 * time.Minute)
		ta.mu.Unlock()
		ta.ok("tick")
	}
	var judg []sprint.Group
	for _, x := range ta.inboxGroups() {
		if x.Kind == sprint.Judgment {
			judg = append(judg, x)
		}
	}
	require.GreaterOrEqual(t, len(judg), 2, "%+v", judg)
	last := judg[len(judg)-1]
	assert.Equal(t, g.ID, last.ID, "the waited judgment is listed after the others: %+v", judg)
	assert.True(t, last.Quiet, "marked quiet")
	assert.False(t, last.Overdue, "not overdue within its period")
	assert.Contains(t, ta.ok("inbox"), "quiet until=")
}

// hasGroup says the inbox holds an open judgment of the type.
func hasGroup(gs []sprint.Group, typ string) bool {
	for _, g := range gs {
		if g.Kind == sprint.Judgment && g.Type == typ {
			return true
		}
	}
	return false
}
