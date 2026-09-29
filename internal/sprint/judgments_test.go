package sprint

import "testing"

// H2: the read that completes two different readers' ok writes one judgment,
// ready to accept; rework and drop close it as accept does.
func TestReadyToAcceptIsClosedByReworkAndDrop(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Start(w.s, StartReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}}))
	for _, id := range []string{"s1-1", "s1-2"} {
		c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
		w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	}
	w.must(Ask(w.s, AskReq{}))
	for _, id := range []string{"s1-1", "s1-2"} {
		for _, rc := range readsAt(w.s, w.s.Work.Card(id), 1) {
			w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
		}
	}
	notes := w.notesOf(NReadyToAccept)
	if len(notes) != 2 || notes[0].Kind != Judgment || len(w.openOn("s1-1")) != 1 || len(w.openOn("s1-2")) != 1 {
		t.Fatalf("ready to accept: %+v", notes)
	}
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "more"}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "obsolete"}))
	if len(w.openOn("s1-1"))+len(w.openOn("s1-2")) != 0 {
		t.Fatalf("still open: %v", w.s.Open)
	}
	w.clean("closed")
}
