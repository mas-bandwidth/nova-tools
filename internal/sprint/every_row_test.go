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

// Two readers' oks of one primary in one read: the primary is ready to
// accept, its judgment written once, after both.
func TestTwoReadersOksInOneReadMakeOneJudgment(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 1, 64, "m1")
	w.s.Readers.SetRows(append(w.s.Readers.Rows(), "reader-b"))
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 1}}))
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 1}}))
	w.must(Finish(w.s, FinishReq{As: "m1,m2", Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Who: "coordinator"}))
	ids := []string{ReadCardID("s1-1", 1, "reader-a"), ReadCardID("s1-1", 1, "reader-b")}
	p := w.must(Read(w.s, ReadReq{As: "reader-a,reader-b", Verdict: "ok", Sel: Sel{IDs: ids}}))
	n := 0
	for _, u := range p.Units {
		for _, x := range u.Notes {
			if x.Type == NReadyToAccept {
				n++
			}
		}
	}
	require.Len(t, p.Units, 2, "two oks in one read: %d units, %d ready-to-accept judgments, want 2 and 1", len(p.Units), n)
	require.Equal(t, 1, n, "two oks in one read: %d units, %d ready-to-accept judgments, want 2 and 1", len(p.Units), n)
	q := Read(w.s, ReadReq{As: "reader-a,reader-b", Verdict: "ok"})
	require.Len(t, q.Refused, 1, "a read by selection of several readers: %+v", q)
}
