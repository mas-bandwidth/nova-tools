package sprint

import (
	"testing"
)

// The world's workers and readers move in one batch a tick (errata 3
// amendment 10): a take, a finish and a read may name several members
// (readers), each acting on its own cards, in the one plan.

func TestATakeOfSeveralMembersTakesEachOnesQueue(t *testing.T) {
	t.Parallel()
	f := newFleetW(t, 12, 64, "m1", "m2", "m3")
	f.w.must(Deal(f.snap(), DealReq{Sel: Sel{Limit: 12}}))
	p := f.w.must(Take(f.snap(), TakeReq{As: "m1,m2,m3", Sel: Sel{Limit: 3}, Who: "m1,m2,m3"}))
	by := map[string]int{}
	for _, u := range p.Units {
		by[f.snap().Fleet.Card(u.Key).Row]++
	}
	if by["m1"] != 3 || by["m2"] != 3 || by["m3"] != 3 {
		t.Fatalf("taken by member %v, want three of each", by)
	}
	if q := Take(f.snap(), TakeReq{As: "m1,m2", Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}}); len(q.Refused) != 1 || len(q.Units) != 0 {
		t.Fatalf("a take by id of several members: %+v", q)
	}
}

// Two readers' oks of one primary in one read: the primary is ready to
// accept, its judgment written once, after both.
func TestTwoReadersOksInOneReadMakeOneJudgment(t *testing.T) {
	t.Parallel()
	f := newFleetW(t, 1, 64, "m1")
	f.snap().Readers.SetRows(append(f.snap().Readers.Rows(), "reader-b"))
	f.w.must(Deal(f.snap(), DealReq{Sel: Sel{Limit: 1}}))
	f.w.must(Take(f.snap(), TakeReq{As: "m1", Sel: Sel{Limit: 1}}))
	f.w.must(Finish(f.snap(), FinishReq{As: "m1,m2", Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}}))
	f.w.must(Ask(f.snap(), AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Who: "coordinator"}))
	ids := []string{ReadCardID("s1-1", 1, "reader-a"), ReadCardID("s1-1", 1, "reader-b")}
	p := f.w.must(Read(f.snap(), ReadReq{As: "reader-a,reader-b", Verdict: "ok", Sel: Sel{IDs: ids}}))
	n := 0
	for _, u := range p.Units {
		for _, x := range u.Notes {
			if x.Type == NReadyToAccept {
				n++
			}
		}
	}
	if len(p.Units) != 2 || n != 1 {
		t.Fatalf("two oks in one read: %d units, %d ready-to-accept judgments, want 2 and 1", len(p.Units), n)
	}
	if q := Read(f.snap(), ReadReq{As: "reader-a,reader-b", Verdict: "ok"}); len(q.Refused) != 1 {
		t.Fatalf("a read by selection of several readers: %+v", q)
	}
}
