package sprint

import "testing"

// When the first card of a green batch is refused (queued in merge but not
// merging in work, which only a broken rule 4 allows), the stream's control
// change and the started-merging note ride on the first card that lands, and
// the batch note lists only the cards that landed.
func TestMergeGreenKeepsTheStreamChangeWhenTheFirstCardIsRefused(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	accepted(w, "s1-1", "s1-2")
	wasWaiting := w.s.StreamCtl("s1").F("state") == StreamWaiting
	w.s.Work.Card("s1-1").Col = Review
	w.s.Work.cells, w.s.Work.byPrimary = nil, nil

	p := MergeStep(w.s, MergeReq{Stream: "s1", Batch: 10})
	if len(p.Refused) != 1 || p.Refused[0].Key != "s1-1" || len(p.Units) != 1 || p.Units[0].Key != "s1-2" {
		t.Fatalf("plan: units %d refused %v", len(p.Units), p.Refused)
	}
	u := p.Units[0]
	var ctl bool
	for _, c := range u.Changes {
		if c.Table == Merge && c.Entry.ID == CtlID("s1") {
			ctl = true
		}
	}
	if !ctl {
		t.Fatalf("the stream's control change is lost: %+v", u.Changes)
	}
	var started, batch int
	for _, n := range u.Notes {
		switch n.Type {
		case NStartedMerging:
			started++
		case NBatchLanded:
			batch++
			if len(n.Primaries) != 1 || n.Primaries[0] != "s1-2" {
				t.Fatalf("the batch note lists %v, want only what landed", n.Primaries)
			}
		case NStreamLanded:
			t.Fatalf("the stream is landed with s1-1 still open")
		}
	}
	if batch != 1 || (wasWaiting && started != 1) {
		t.Fatalf("notes: started %d batch %d (was waiting %v)", started, batch, wasWaiting)
	}
}
