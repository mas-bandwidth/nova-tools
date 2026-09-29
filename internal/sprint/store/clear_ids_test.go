package store

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// C2: an operation recorded under a caller's id before a clear (its reply
// lost), sent again after it, is refused naming the epoch it belongs to, and
// is never run again as new work.
func TestCallerOpOfAnEarlierEpochIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	step := AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"late"}})
	step.CallerOp = "caller-1"
	h.must(step)
	if _, err := h.st.Clear(h.ctx); err != nil {
		t.Fatal(err)
	}
	img := h.image()
	for _, held := range []*uint64{nil, new(uint64)} {
		step.Epoch = held
		res, err := h.st.Run(h.ctx, step)
		if err != nil || res.Replay || len(res.Moved) != 0 || len(res.Refused) != 1 {
			t.Fatalf("the same operation after the clear (held %v): %+v %v", held, res, err)
		}
		if why := res.Refused[0].Why; held == nil && (!strings.Contains(why, "cleared at") || !strings.Contains(why, "operation caller-1 belongs to epoch 0")) {
			t.Fatalf("the refusal: %s", why)
		}
		if c := h.snap().Work.Card("late"); c != nil && c.Placed() {
			t.Fatalf("the operation of epoch 0 ran again at epoch 1")
		}
		if got := h.image(); got != img {
			t.Fatalf("the refused operation changed the store:\n%s\nwas\n%s", got, img)
		}
	}
	// A caller's id holding the epoch's mark is refused before anything.
	step.Epoch, step.CallerOp = nil, "x~1"
	if _, err := h.st.Run(h.ctx, step); err == nil || !strings.Contains(err.Error(), "holds '~'") {
		t.Fatalf("an id with the epoch's mark: %v", err)
	}
}

// C2: notification and judgment ids carry the epoch: the same operation
// family at two epochs has two judgment ids, and an ack or an answer naming
// the old one is refused, naming its epoch, and closes nothing of the new.
func TestNoteIDsCarryTheEpoch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.st.NewID = func() string { return "same" }
	h.setup(2)
	failIt := func() string {
		h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		s := h.snap()
		c := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
		h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
		h.must(FinishStep(sprint.FinishReq{Failed: true, Report: "red", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
		v, err := h.st.Inbox(h.ctx, 0, 0, 100)
		if err != nil || len(v.Open) == 0 {
			t.Fatalf("no judgment: %v", err)
		}
		return v.Open[0].Note.ID
	}
	oldID := failIt()
	if _, err := h.st.Clear(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
	newID := failIt()
	if oldID == newID || sprint.IDEpoch(oldID) != 0 || sprint.IDEpoch(newID) != 1 {
		t.Fatalf("judgment ids: epoch 0 %s, epoch 1 %s", oldID, newID)
	}
	img := h.image()
	res, err := h.st.Run(h.ctx, AckStep(sprint.AckReq{Notes: []string{oldID}, Reason: "x"}))
	if err != nil || len(res.Moved) != 0 || len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Why, "belongs to epoch 0") {
		t.Fatalf("ack of the old judgment: %+v %v", res, err)
	}
	if err := h.st.SetReview(h.ctx, oldID, t0); err == nil || !strings.Contains(err.Error(), "belongs to epoch 0") {
		t.Fatalf("wait on the old judgment: %v", err)
	}
	if h.image() != img {
		t.Fatalf("the ack and the wait of the old judgment changed the store")
	}
	if v, _ := h.st.Inbox(h.ctx, 0, 0, 100); len(v.Open) == 0 || v.Open[0].Note.ID != newID {
		t.Fatalf("the new judgment is not open: %+v", v.Open)
	}
	// An answer naming the old judgment is refused by its id, naming its
	// epoch; the rework itself acts at the new epoch.
	res, err = h.st.Run(h.ctx, ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "x", Answers: []string{oldID}}))
	if err != nil || len(res.Refused) != 1 || res.Refused[0].Key != oldID || !strings.Contains(res.Refused[0].Why, "belongs to epoch 0") {
		t.Fatalf("an answer naming the old judgment: %+v %v", res, err)
	}
}
