package store

import (
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The line the store writes for the ack of a judgment. The ack step's plan
// holds a decided note that names no subject; the store replaces it with one
// that names the subjects the step closes, so the line the log holds says
// which judgment closed and on what.
func TestTheDecidedLineTheStoreWritesForAnAckNamesItsSubjects(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{sprint.NBlocked, sprint.NMissingNeed} {
		h := newHarness(t)
		h.setup(2)
		h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1", "s1-2"}}))
		if typ == sprint.NBlocked {
			h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
		} else {
			seedMissingNeeds(h, "waiter", "first.bad")
			h.must(ResolveStep(sprint.ResolveReq{}))
		}
		open := h.nOpenOf(typ, "waiter")
		if len(open) != 1 {
			t.Fatalf("%s: %d open judgments on the waiter", typ, len(open))
		}
		before := len(h.lines())
		h.must(AckStep(sprint.AckReq{Notes: []string{open[0].Note.ID}, Reason: "unneeded", Who: "tester"}))
		var closing []*sprint.Note
		for _, l := range h.lines()[before:] {
			if l.Note != nil && (l.Kind == sprint.Decided || l.Kind == sprint.Acknowledged) {
				closing = append(closing, l.Note)
			}
		}
		if len(closing) != 1 || closing[0].Type != typ || !slices.Equal(closing[0].Subjects(), []string{"waiter"}) {
			t.Fatalf("%s: the ack closed with %+v", typ, closing)
		}
	}
}
