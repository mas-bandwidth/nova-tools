package store

// The audit's gaps of the trigger rule (every card is final, held by an
// outside actor, moved by a trigger, or the subject of an open judgment),
// each inverted: the sequence that left a card silent now moves it or names
// it in the inbox, read as a coordinator reads it (cursor advanced).

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// readInbox is the coordinator reading the inbox and moving the cursor past it.
func (h *harness) readInbox() {
	h.t.Helper()
	v, err := h.st.Inbox(h.ctx, 10*time.Minute, 30*time.Minute, 10000)
	if err != nil {
		h.t.Fatal(err)
	}
	if v.Last != "" {
		if err := h.m.SetCursor(h.ctx, v.Last); err != nil {
			h.t.Fatal(err)
		}
	}
}

// judgmentsOn is the types of the open judgments on the primary.
func (h *harness) judgmentsOn(id string) []string {
	h.t.Helper()
	open, err := h.m.OpenNotes(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, o := range open {
		if o.Subject() == id {
			out = append(out, o.Note.Type)
		}
	}
	return out
}

// The landing merge step resolves what waits on the card it lands.
func TestTriggerLandingResolvesWaiters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"s2-1"}, Needs: []string{"s1-1"}}))
	h.through("s1-1")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 10}))
	h.readInbox()
	if h.state("s1-1") != sprint.Landed || h.state("s2-1") != sprint.Ready {
		t.Fatalf("s1-1 %s, s2-1 %s", h.state("s1-1"), h.state("s2-1"))
	}
	h.clean("resolved by the landing")
}
