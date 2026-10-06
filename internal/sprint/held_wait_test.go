package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// One wait (docs/SPEC-ISA.md, "the one wait kind"): hold (add --held), a
// sentinel (add --sentinel) and a wave are read by one function, WaitOf. A held
// card waits for the coordinator's release; a sentinel waits for the line
// before it and is itself released; a wave is the cards whose needs name that
// release. The three are one path, not three mechanisms.
func TestHoldSentinelAndWaveAreOneWait(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Lawful(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true})))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"h"}, Held: true}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"m1", "m2"}, Needs: []string{"h"}}))

	held := WaitOf(w.s, w.s.Work.Card("h"))
	stop := WaitOf(w.s, w.s.Work.Card("stop"))
	wave1 := WaitOf(w.s, w.s.Work.Card("m1"))
	wave2 := WaitOf(w.s, w.s.Work.Card("m2"))

	require.Equal(t, WaitOnRelease, held.Operand, "a held card waits for the release: %+v", held)
	require.Empty(t, held.On, "a held card names no other operand: %+v", held)

	require.Equal(t, WaitOnLine, stop.Operand, "a sentinel waits for its line: %+v", stop)
	require.Equal(t, []string{"s1-1", "s1-2"}, stop.On, "a sentinel waits for the line before it: %+v", stop)

	require.Equal(t, WaitOnCards, wave1.Operand, "a wave card waits for the cards it names: %+v", wave1)
	require.Equal(t, []string{"h"}, wave1.On, "the wave waits for the held card: %+v", wave1)
	require.Equal(t, wave1.On, wave2.On, "the wave is many cards on one wait: %+v %+v", wave1, wave2)

	// The wave's wait is the held card's: one operand, the release, behind both.
	require.Equal(t, held.Operand, WaitOf(w.s, w.s.Work.Card(wave1.On[0])).Operand, "the wave waits on the held wait")
	for _, wt := range []CardWait{held, stop, wave1, wave2} {
		require.NotEmpty(t, wt.Why, "every wait states why: %+v", wt)
		require.Equal(t, wt.Card, WaitOf(w.s, w.s.Work.Card(wt.Card)).Card, "WaitOf reads the card it names")
	}
}
