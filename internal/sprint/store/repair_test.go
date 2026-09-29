package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// takeAndFinish takes every live work card of the primaries by id at its
// generation, as the member it was dealt to, then finishes each member's cards
// in one step per member.
func (h *harness) takeAndFinish(failed bool, ids ...string) {
	h.t.Helper()
	s := h.snap()
	byMember := map[string][]string{}
	gens := map[string]int{}
	var members []string
	for _, id := range ids {
		c := s.Fleet.Card(s.Work.Card(id).F("work"))
		gens[c.ID] = c.Int("gen")
		if byMember[c.Row] == nil {
			members = append(members, c.Row)
		}
		byMember[c.Row] = append(byMember[c.Row], c.ID)
		h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	}
	for _, m := range members {
		g := map[string]int{}
		for _, id := range byMember[m] {
			g[id] = gens[id]
		}
		h.must(FinishStep(sprint.FinishReq{As: m, Sel: sprint.Sel{IDs: byMember[m]}, Gens: g, Failed: failed, Report: "same"}))
	}
}

// F6. A judgment over more primaries than a notification lists keeps every
// primary an open subject: the listing is bounded, the obligations are not.
func TestEverySubjectOfALargeJudgmentStaysOpen(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// one member up: the sixty cards are one member's, finished in one step
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 60}))
	h.must(StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 60}}))
	var ids []string
	for id, c := range h.snap().Work.Cards {
		if c.Col == sprint.Working {
			ids = append(ids, id)
		}
	}
	if len(ids) != 60 {
		t.Fatalf("started %d primaries", len(ids))
	}
	h.takeAndFinish(true, ids...)
	open, err := h.m.OpenNotes(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	notes := map[string]bool{}
	subjects := map[string]bool{}
	for _, o := range open {
		if o.Note.Type == sprint.NWorkFailed {
			notes[o.Note.ID] = true
			subjects[o.Subject()] = true
		}
	}
	if len(subjects) != 60 {
		t.Fatalf("60 failures leave %d open obligations", len(subjects))
	}
	all, _, _ := h.m.NotesSince(h.ctx, "", 1000)
	lines := 0
	for _, n := range all {
		if n.Type == sprint.NWorkFailed {
			lines++
			if len(n.Primaries) > sprint.MaxListed || n.Count == 0 {
				t.Fatalf("the notification lists %d primaries, count %d", len(n.Primaries), n.Count)
			}
		}
	}
	if lines != 1 || len(notes) != 1 {
		t.Fatalf("%d failed-work lines for %d judgments; want one of each", lines, len(notes))
	}
}
