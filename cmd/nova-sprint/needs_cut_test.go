package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// needs --cut end to end on the twin: the blocked judgment offers the cut as
// its third decision with the exact command; the command cuts the dead edge,
// closes the judgment, writes the timeline line with the actor and reason, and
// the next tick moves the card to ready. A live need and a card past waiting
// are refused, nothing written; a held sentinel's edge is cut and its hold kept.
func TestNeedsCutMendsAChainBehindADroppedCard(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s2 b --needs s1-1")
	ta.ok("add --stream s3 c --needs b,s1-2")
	ta.ok("add --stream s4 --sentinel gate --needs s1-1 --held")
	ta.ok("drop s1-1 --reason obsolete")

	g := ta.group(sprint.NBlocked, "s2")
	assert.Contains(t, ta.ok("inbox"), sprint.CutDecision+":\n    nova-sprint needs b --cut s1-1 --reason '<why>' --answers "+g.ID, "the blocked judgment offers the cut")

	applies := ta.applies()
	code, _, errs := ta.do("needs c --cut s1-2 --reason r")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "s1-2 is s1:ready, not dropped: only a need dropped off the table is cut")
	code, _, errs = ta.do("needs s1-2 --cut s1-1 --reason r")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "s1-2 is ready, not waiting")
	assert.Equal(t, applies, ta.applies(), "a refused cut writes nothing")

	ta.ok("start")
	ta.ok("needs b --cut s1-1 --reason 'b stands without s1-1' --answers " + g.ID)
	ta.ok("needs gate --cut s1-1 --reason 'the wave does not wait for s1-1'")
	ta.ok("tick")
	b := ta.primary("b")
	assert.NotEqual(t, sprint.Waiting, b.Col, "b still waits after the cut and a tick")
	assert.Empty(t, b.F("needs"))
	assert.Equal(t, "s1-1", b.F("cut"))
	assert.Equal(t, sprint.Waiting, ta.primary("gate").Col, "the cut released a held sentinel")
	for _, gr := range ta.inboxGroups() {
		assert.False(t, gr.Kind == sprint.Judgment && gr.Type == sprint.NBlocked, "a blocked judgment is still open: %+v", gr)
	}
	story := ta.ok("card b")
	assert.Contains(t, story, "b: needs on dropped cards cut by coordinator (cut: s1-1; needs now: -)")
	assert.Contains(t, story, "reason by coordinator:\n    b stands without s1-1")
	ta.clean()
}
