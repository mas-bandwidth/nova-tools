package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The world's workers and readers move in one batch a tick (errata 3
// amendment 10): a take, a finish and a read may name several members
// (readers), each acting on its own cards, in the one plan.

func TestATakeOfSeveralMembersTakesEachOnesQueue(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 12, 64, "m1", "m2", "m3")
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 12}}))
	p := w.must(Take(w.s, TakeReq{As: "m1,m2,m3", Sel: Sel{Limit: 3}, Who: "m1,m2,m3"}))
	by := map[string]int{}
	for _, u := range p.Units {
		by[w.s.Fleet.Card(u.Key).Row]++
	}
	require.Equal(t, 3, by["m1"], "taken by member %v, want three of each", by)
	require.Equal(t, 3, by["m2"], "taken by member %v, want three of each", by)
	require.Equal(t, 3, by["m3"], "taken by member %v, want three of each", by)
	q := Take(w.s, TakeReq{As: "m1,m2", Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}})
	require.Len(t, q.Refused, 1, "a take by id of several members: %+v", q)
	require.Empty(t, q.Units, "a take by id of several members: %+v", q)
}
