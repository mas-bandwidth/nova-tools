package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHoldSentinelAndWaveAreOneWait(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Lawful(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true})))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"h"}, Held: true}))
	w.must(Add(w.s, AddReq{Stream: "s3", IDs: []string{"gate"}, Sentinel: true, Held: true}))
	w.must(Add(w.s, AddReq{Stream: "s3", IDs: []string{"m1", "m2"}}))

	held := WaitOf(w.s, w.s.Work.Card("h"))
	stop := WaitOf(w.s, w.s.Work.Card("stop"))
	gate := WaitOf(w.s, w.s.Work.Card("gate"))
	wave1 := WaitOf(w.s, w.s.Work.Card("m1"))
	wave2 := WaitOf(w.s, w.s.Work.Card("m2"))

	require.Equal(t, WaitOnRelease, held.Operand, "a held card waits for the release: %+v", held)
	require.Empty(t, held.On, "a held card names no other operand: %+v", held)

	require.Equal(t, WaitOnLine, stop.Operand, "a sentinel waits for its line: %+v", stop)
	require.Equal(t, []string{"s1-1", "s1-2"}, stop.On, "a sentinel waits for the line before it: %+v", stop)
	require.Equal(t, WaitOnRelease, gate.Operand, "a held sentinel waits for release: %+v", gate)
	require.Empty(t, gate.On, "the held sentinel waits only for release: %+v", gate)
	require.Equal(t, held.Operand, gate.Operand, "a held card and held sentinel share one release wait")

	require.Equal(t, WaitOnCards, wave1.Operand, "a wave card waits for the cards it names: %+v", wave1)
	require.Equal(t, []string{"gate"}, wave1.On, "the wave waits for the held sentinel: %+v", wave1)
	require.Equal(t, wave1.On, wave2.On, "the wave is many cards on one wait: %+v %+v", wave1, wave2)
	require.Equal(t, gate.Operand, WaitOf(w.s, w.s.Work.Card(wave1.On[0])).Operand, "the wave waits on the held sentinel's wait")
	for _, wait := range []CardWait{held, stop, gate, wave1, wave2} {
		require.NotEmpty(t, wait.Why, "every wait states why: %+v", wait)
		require.Equal(t, wait.Card, WaitOf(w.s, w.s.Work.Card(wait.Card)).Card, "WaitOf reads the card it names")
	}
}
