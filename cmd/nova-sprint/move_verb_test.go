package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// move end to end on the twin (the owner, 2026-10-01: "What other things
// should you be able to do to mutate a stopped sprint" / "I don't want you
// manually hopping in and working around it and doing manual stuff."):
// refused on a RUNNING machine, all or none with a started card among those
// named, the cards moved to a stream made as add makes one, and a need naming
// a moved card holding.
func TestMoveTakesUnstartedPrimariesToAnotherStreamOnAStoppedSprint(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream a --count 2")
	ta.ok("add --stream b b-1 --needs a-2")

	ta.ok("start")
	code, _, errs := ta.do("move a-2 --stream c")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "the machine is RUNNING")
	assert.Contains(t, errs, "run: nova-sprint stop")
	assert.Equal(t, "a", ta.primary("a-2").Row)
	ta.ok("stop")

	ta.deal(1)
	require.Equal(t, sprint.Working, ta.primary("a-1").Col)
	applies := ta.applies()
	code, out, errs := ta.do("move a-1 a-2 --stream c")
	assert.Equal(t, 1, code)
	assert.NotContains(t, out, "MOVED")
	assert.Contains(t, errs, "a-1 is working: a card dealt, working, in review, merging or landed keeps its stream")
	assert.Contains(t, errs, "all or none")
	assert.Equal(t, applies, ta.applies(), "all or none: nothing written")
	assert.Equal(t, "a", ta.primary("a-2").Row)

	assert.Contains(t, ta.ok("move a-2 b-1 --stream c"), "moved from stream a ready")
	a2, b1 := ta.primary("a-2"), ta.primary("b-1")
	assert.Equal(t, "c", a2.Row)
	assert.Equal(t, sprint.Ready, a2.Col)
	assert.Equal(t, "c", b1.Row)
	assert.Equal(t, sprint.Waiting, b1.Col, "b-1 still waits on a-2")
	assert.Equal(t, "a-2", b1.F("needs"))
	assert.Less(t, a2.Score, b1.Score, "in the order named, at the end of c's line")
	assert.Equal(t, map[string][]string{sprint.Work: {"a", "b", "c"}, sprint.Merge: {"a", "b", "c"}}, ta.streamRows(), "c made as add makes a stream")
	ta.clean()
}
