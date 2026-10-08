package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A held sentinel (add --held --sentinel) is released as a sentinel: it goes
// waiting -> landed and the wave behind it goes to ready.
func TestHeldSentinelWaveReleases(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"gate"}, Sentinel: true, Held: true}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"m1", "m2"}}))
	require.Equal(t, Waiting, w.state("m1"))
	require.Equal(t, Waiting, w.state("m2"))

	p := Release(w.s, ReleaseReq{IDs: []string{"gate"}, Reason: "the wave is loaded", Coordinator: "coordinator", Who: "coordinator"})
	require.Empty(t, p.Refused)
	w.must(p)
	require.Equal(t, Landed, w.state("gate"))
	require.Equal(t, Ready, w.state("m1"))
	require.Equal(t, Ready, w.state("m2"))
}

// A held card with needs not landed follows its chain: its wait names the
// needs, and the no-stall rule holds it through them, not as stalled.
func TestHeldCardFollowsItsChain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"n"}}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"h"}, Needs: []string{"n"}, Held: true}))
	require.Equal(t, Waiting, w.state("h"))

	h := w.s.Work.Card("h")
	wait := WaitOf(w.s, h)
	require.Equal(t, WaitOnRelease, wait.Operand)
	require.Equal(t, []string{"n"}, wait.On, "a held card's wait names the chain: %+v", wait)

	why, root, ok := newHeld(HeldState{Snap: w.s}, w.s.Now).waits(h)
	require.True(t, ok, "a held card with a need not landed is held through its chain, not stalled: %q (root %q)", why, root)
	require.Contains(t, why, "needs n")
	require.False(t, newHeld(HeldState{Snap: w.s}, w.s.Now).hold("h").Stalled())
}

// heldWave offers only a held sentinel with no line before it.
func TestHeldWaveSkipsASentinelWithALine(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"a"}}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"gate"}, Sentinel: true, Held: true}))
	require.Equal(t, []string{"a"}, WaitsFor(w.s, w.s.Work.Card("gate"), nil))
	require.Nil(t, heldWave(w.s), "a held sentinel with a line before it is not offered")

	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"free"}, Sentinel: true, Held: true}))
	got := heldWave(w.s)
	require.NotNil(t, got)
	require.Equal(t, "free", got.ID)
}
