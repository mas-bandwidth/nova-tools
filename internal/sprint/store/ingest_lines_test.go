package store

import (
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The line the store writes for the ack of a judgment, read by the tick's
// ingest. The ack step's plan holds a decided note that names no subject; the
// store replaces it with one that names the subjects the step closes, so the
// close queues the owner key of its subject (the held rule of the primary), and
// the line the log holds is one Ingest reads a subject from.
func TestIngestReadsTheDecidedLineTheStoreWritesForAnAck(t *testing.T) {
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
		var closing []sprint.Event
		for i, l := range h.lines()[before:] {
			if e := sprint.EventOf(uint64(before+i+1), l); e.Closes {
				closing = append(closing, e)
			}
		}
		if len(closing) != 1 || closing[0].NoteType != typ || !slices.Equal(closing[0].Subjects, []string{"waiter"}) {
			t.Fatalf("%s: the ack closed with %+v", typ, closing)
		}
		var keys []string
		for _, k := range sprint.Ingest(closing).Keys {
			keys = append(keys, k.Key)
		}
		if !slices.Equal(keys, []string{"held:waiter"}) {
			t.Fatalf("%s: the close of the waiter's judgment queued %v", typ, keys)
		}
	}
}
