package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A placed read from two readers and a retired record for the third must be
// enough for the tick's store load to rule out ask --another. The ordinary
// ask step already reads retired candidate records; the tick must see them too.
func TestTickStallDoesNotOfferAReaderWithARetiredReadRecord(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.toReview()
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Another: true}))
	reads := h.snap().Readers.Of("s1-1")
	if len(reads) != 3 {
		t.Fatalf("read cards: %d, want 3", len(reads))
	}
	for _, rc := range reads {
		h.must(ReadStep(sprint.ReadReq{As: rc.Row, Verdict: "broken", Finding: "f", Sel: sprint.Sel{IDs: []string{rc.ID}}}))
	}
	h.poke(sprint.Readers, ntable.BatchMemberEntry{ID: reads[2].ID, Remove: true})
	if row, col, _, _, ok := h.m.Record("t-readers", reads[2].ID); !ok || row != "" || col != "" {
		t.Fatalf("the third read record was not retired: %s:%s exists=%v", row, col, ok)
	}
	// Simulate judgments closed without a resolving step, as the stall check
	// must detect. This leaves the two completed cards placed and the third
	// record retired in the real Mem backend.
	h.m.mu.Lock()
	h.m.log().open = map[string]string{}
	h.m.mu.Unlock()
	h.startMachine()
	h.machine()
	stalled := h.openOf(sprint.NStalled)
	if len(stalled) != 1 || stalled[0].Subject() != "s1-1" {
		t.Fatalf("stalled judgment: %+v", stalled)
	}
	if contains(stalled[0].Note.Decisions, "ask") || contains(stalled[0].Note.Decisions, "ask --another") {
		t.Fatalf("the tick offered a retired reader: %v", stalled[0].Note.Decisions)
	}
	res := h.run(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Another: true}))
	if len(res.Moved) != 0 || len(res.Refused) != 1 {
		t.Fatalf("ask --another unexpectedly accepted: %+v", res)
	}
}
