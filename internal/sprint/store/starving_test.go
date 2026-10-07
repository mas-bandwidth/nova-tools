package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
)

// Ready is kept at twice the fleet's width (the owner, 2026-10-02: "Ready always full";
// 2026-10-03: "BATCH EVERYTHING"): while a wave is held and ready is under that, the tick
// raises "the fleet is starving" once, updates it in place, and offers the first held wave's
// sentinel for release, never a single card; the judgment closes when no wave is held.
func TestTheTickRaisesStarvingWhileReadyIsUnderTwiceTheWidth(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 2}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2", Width: 2}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 3}))
	// the wave: a held sentinel with nothing before it, four cards loaded behind it
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"s2-gate"}, Sentinel: true, Held: true}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 4}))
	h.startMachine()
	h.machine()
	open := h.openOf(sprint.NStarving)
	require.Len(t, open, 1, "the starving judgment")
	n := open[0].Note
	assert.Equal(t, "the fleet is starving: ready 3 is under twice the width 8; release a wave: nova-sprint release s2-gate --reason '<why>'", n.What)
	assert.Equal(t, []string{"release", "wait"}, n.Decisions)
	assert.Equal(t, []string{"s2-gate"}, n.Primaries, "the first held wave's sentinel")
	cmds := h.commandsOf(sprint.NStarving)
	require.NotEmpty(t, cmds)
	assert.Equal(t, "release", cmds[0].Decision)
	assert.Contains(t, cmds[0].Lines[0], "nova-sprint release s2-gate --reason")
	h.machine()
	h.machine()
	assert.Len(t, h.openOf(sprint.NStarving), 1, "raised once while it holds")
	assert.Equal(t, 1, h.written(sprint.NStarving))
	assert.Contains(t, h.openOf(sprint.NStarving)[0].Note.What, "ready 0 is under", "the count updates in place: the three were dealt")
	h.must(ReleaseStep(sprint.ReleaseReq{IDs: []string{"s2-gate"}, Reason: "the wave", Coordinator: "tester", Who: "tester"}))
	h.machine()
	assert.Empty(t, h.openOf(sprint.NStarving), "no wave is held: nothing to offer")
	assert.Equal(t, sprint.Landed, h.state("s2-gate"))
	h.clean("the wave released")
}
