package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ready is kept at twice the fleet's width: while a wave is held and ready is under that,
// the tick raises "the fleet is starving" once, updates it in place, carrying working and width;
// "release a wave" appears only with the numbers.
func TestNStarvingCarriesWorkingAndWidth(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 2}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2", Width: 2}))
	// Add 3 ready cards in stream s1
	w.must(Add(w.s, AddReq{Brief: proBrief, Stream: "s1", Count: 3}))
	// The wave: a held sentinel with nothing before it, 4 cards loaded behind it
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"s2-gate"}, Sentinel: true, Held: true}))
	w.must(Add(w.s, AddReq{Brief: proBrief, Stream: "s2", Count: 4}))

	// Run TickDeal before any card has been taken into working.
	// Ready has 3 cards, width is 4 (2+2), twice the width is 8. Working is 0.
	p, _ := TickDeal(w.s, TickReq{})
	var starvingNotes []Note
	for _, n := range p.Notes {
		if n.Type == NStarving {
			starvingNotes = append(starvingNotes, n)
		}
	}
	require.Len(t, starvingNotes, 1, "the starving judgment is raised")
	n := starvingNotes[0]

	// Verify notification text names working and width
	assert.Contains(t, n.What, "working")
	assert.Contains(t, n.What, "width")
	assert.Contains(t, n.What, "ready 3 is under twice the width 8, working 0 of width 4")

	// "release a wave" appears only with the numbers (in n.What, never in decisions or elsewhere)
	assert.Contains(t, n.What, "release a wave: nova-sprint release s2-gate --reason '<why>'")
	assert.Equal(t, []string{"release", "wait"}, n.Decisions)
	for _, d := range n.Decisions {
		assert.NotContains(t, d, "release a wave", "decision must be release, not 'release a wave'")
	}
	assert.Equal(t, []string{"s2-gate"}, n.Primaries)

	// Apply the plan so the judgment is open in w.s.Open, and the 3 ready cards are dealt
	w.must(p)

	// Take one card into working on m1: working becomes 1
	require.NotEmpty(t, w.s.Fleet.Cell("m1", Ready))
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 1}}))
	workingCount := w.s.Fleet.Count("m1", Working) + w.s.Fleet.Count("m2", Working)
	require.Equal(t, 1, workingCount)

	// Run TickDeal again: ready is 0 (the three were dealt into fleet).
	// The open judgment should be updated in place in p2.Updates.
	p2, _ := TickDeal(w.s, TickReq{})
	var updatedNotes []Note
	for _, u := range p2.Updates {
		if u.Type == NStarving {
			updatedNotes = append(updatedNotes, u)
		}
	}
	require.Len(t, updatedNotes, 1, "judgment updated in place")
	u := updatedNotes[0]
	assert.Contains(t, u.What, "working")
	assert.Contains(t, u.What, "width")
	assert.Contains(t, u.What, "ready 0 is under twice the width 8, working 1 of width 4")
	assert.Contains(t, u.What, "release a wave")
}

// The all-dealt case has its own test: when all ready cards have been dealt,
// ready is 0 while a wave is held, and the starving judgment names ready 0,
// the working count, and the fleet width.
func TestNStarvingAllDealt(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 2}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2", Width: 2}))
	// Deal 2 cards directly to working on m1 and m2
	w.must(Add(w.s, AddReq{Brief: proBrief, Stream: "s1", Count: 2}))
	pDeal, _ := TickDeal(w.s, TickReq{})
	w.must(pDeal)
	if len(w.s.Fleet.Cell("m1", Ready)) > 0 {
		w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 1}}))
	}
	if len(w.s.Fleet.Cell("m2", Ready)) > 0 {
		w.must(Take(w.s, TakeReq{As: "m2", Sel: Sel{Limit: 1}}))
	}

	// The wave: held sentinel waiting with cards loaded behind it
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"s2-gate"}, Sentinel: true, Held: true}))
	w.must(Add(w.s, AddReq{Brief: proBrief, Stream: "s2", Count: 4}))

	// Now ready (non-sentinel) in s.Work is 0: all dealt!
	readyCount := 0
	for _, c := range w.s.Work.Column(Ready) {
		if !IsSentinel(c) {
			readyCount++
		}
	}
	require.Equal(t, 0, readyCount, "all cards dealt")

	// TickDeal raises NStarving with ready 0, working 2 of width 4
	p, _ := TickDeal(w.s, TickReq{})
	var starvingNotes []Note
	for _, n := range p.Notes {
		if n.Type == NStarving {
			starvingNotes = append(starvingNotes, n)
		}
	}
	require.Len(t, starvingNotes, 1, "starving raised in all-dealt case")
	n := starvingNotes[0]
	assert.Contains(t, n.What, "working")
	assert.Contains(t, n.What, "width")
	assert.Contains(t, n.What, "ready 0 is under twice the width 8, working 2 of width 4")
	assert.Contains(t, n.What, "release a wave: nova-sprint release s2-gate --reason '<why>'")
	assert.Equal(t, []string{"release", "wait"}, n.Decisions)
}
