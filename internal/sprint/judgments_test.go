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

// H3: add with a need that names a dropped primary writes the blocked
// judgment itself, in the same step; its decisions are drop and ack.
func TestAddOnADroppedNeedIsBlockedAtOnce(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	p := w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"later"}, Needs: []string{"s1-1"}}))
	var notes []Note
	for _, u := range p.Units {
		notes = append(notes, u.Notes...)
	}
	if len(notes) != 1 || notes[0].Type != NBlocked || len(w.openOn("later")) != 1 {
		t.Fatalf("add did not write the blocked judgment: %+v", notes)
	}
	if got := notes[0].Decisions; len(got) != 2 || got[0] != "drop" || got[1] != "ack" {
		t.Fatalf("decisions: %v", got)
	}
	g := Inbox(InboxReq{Now: w.s.Now, Open: w.s.Open})
	var ds []string
	for _, c := range g[0].Commands {
		ds = append(ds, c.Decision+": "+c.Lines[0])
	}
	if len(ds) != 2 || ds[1] != "ack: nova-sprint ack "+g[0].ID+" --reason "+noneText {
		t.Fatalf("commands: %v", ds)
	}
	w.must(Resolve(w.s, ResolveReq{}))
	if len(w.notesOf(NBlocked)) != 1 {
		t.Fatalf("blocked written again")
	}
	w.clean("blocked")
}
