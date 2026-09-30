package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 60}}))
	var ids []string
	for _, c := range h.snap().Work.Cards() {
		if c.Col == sprint.Working {
			ids = append(ids, c.ID)
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

// outsideWrite is a racer that changes a primary's brief on the work table,
// as a writer outside the sprint's fence, just before the step's work
// manifest applies.
func (h *harness) outsideWrite(id string) *racer {
	return &racer{Backend: h.m, at: "apply t-work", do: func() {
		s := h.snap()
		p := s.Work.Card(id)
		_, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Work.Revision),
			OperationID: "outside", Members: []ntable.BatchMemberEntry{{ID: p.ID, Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: p.Row, Col: p.Col}}, Set: map[string]string{"brief": "outside"}}}})
		if err != nil {
			h.t.Error(err)
		}
	}}
}

// cutStart starts s1-1 and s1-2 in one step whose work manifest finds s1-1
// changed by a writer outside the fence: the step is cut, pending.
func (h *harness) cutStart(callerOp string) Step {
	h.t.Helper()
	st := *h.st
	st.B = h.outsideWrite("s1-1")
	step := DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}})
	step.CallerOp = callerOp
	_, err := st.Run(h.ctx, step)
	var cut *CutError
	if !errors.As(err, &cut) || h.m.Pending() == nil {
		h.t.Fatalf("the step was not cut: %v", err)
	}
	return step
}

func (h *harness) skipNotes() []sprint.Note {
	all, _, _ := h.m.NotesSince(h.ctx, "", 10000)
	var out []sprint.Note
	for _, n := range all {
		if n.Type == NRepairSkipped {
			out = append(out, n)
		}
	}
	return out
}

// F5 (probe 8, expectation no longer true). Repair applies the entries whose
// expectation holds, skips the one that does not, leaves the newer state as
// it is, writes one judgment naming the card, the table, what was expected and
// what was found, with its decisions, and releases the fence; check then
// judges the tables as they are, with nothing pending.
func TestRepairSkipsWhatNoLongerHoldsAndReleases(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.cutStart("")
	h.tick(time.Hour)
	rr, err := h.st.Repair(h.ctx)
	if err != nil || len(rr) != 1 || rr[0].Done != RepairSkipped {
		t.Fatalf("repair: %+v %v", rr, err)
	}
	if h.m.Pending() != nil {
		t.Fatalf("the fence is still held")
	}
	s := h.snap()
	if c := s.Work.Card("s1-1"); c.Col != sprint.Ready || c.F("brief") != "outside" {
		t.Fatalf("s1-1 overwritten: %s brief=%s", c.Col, c.F("brief"))
	}
	if c := s.Work.Card("s1-2"); c.Col != sprint.Working {
		t.Fatalf("s1-2, whose expectation held, is %s", c.Col)
	}
	ns := h.skipNotes()
	if len(ns) != 1 {
		t.Fatalf("%d skip judgments", len(ns))
	}
	n := ns[0]
	if n.Kind != sprint.Judgment || len(n.Primaries) != 1 || n.Primaries[0] != "s1-1" || len(n.Decisions) != len(RepairSkippedDecisions) {
		t.Fatalf("skip judgment: %+v", n)
	}
	for _, want := range []string{"card s1-1", "t-work", "expected", "found revision 2"} {
		if !strings.Contains(n.What, want) && !strings.Contains(rr[0].Skipped[0], want) {
			t.Fatalf("the skip does not say %q: %s | %v", want, n.What, rr[0].Skipped)
		}
	}
	open, _ := h.m.OpenNotes(h.ctx)
	found := false
	for _, o := range open {
		found = found || (o.Note.ID == n.ID && o.Subject() == "s1-1")
	}
	if !found {
		t.Fatalf("the skip judgment is not open on s1-1: %v", open)
	}
	rep, _, err := h.st.Check(h.ctx, 3)
	if err != nil || rep.Pending != "" {
		t.Fatalf("check after the repair: pending %q %v", rep.Pending, err)
	}
	named := false
	for _, v := range rep.Violations {
		named = named || strings.Contains(v.Detail, "s1-1")
	}
	if !named {
		t.Fatalf("check does not report the skipped move as it is: %v", rep.Violations)
	}
}

// F5. After a cut that repair skips, the next mutating verb finishes it on
// its way (no PendingError, even past the grace), and every verb runs again.
func TestEveryVerbRunsAfterASkippingRepair(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.cutStart("")
	h.tick(time.Hour)
	res, err := h.st.Run(h.ctx, CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}, Run: "r"}))
	if err != nil || len(res.Repaired) != 1 || !strings.HasSuffix(res.Repaired[0], RepairSkipped) {
		t.Fatalf("the verb after the cut: %+v %v", res, err)
	}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 1}))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s2-1"}}}))
	if h.state("s2-1") != sprint.Working {
		t.Fatalf("start after the repair: s2-1 is %s", h.state("s2-1"))
	}
	if len(h.skipNotes()) != 1 {
		t.Fatalf("%d skip judgments", len(h.skipNotes()))
	}
}

// F5. A repair whose release is lost is run again: the entries the first
// applied count as applied, the skip is found again, and the judgment is
// written once.
func TestTheSkipJudgmentIsWrittenOnceOverTwoRepairs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.cutStart("")
	h.m.Fail = func(p string) error {
		if p == "release" {
			return errors.New("lost")
		}
		return nil
	}
	rr, err := h.st.Repair(h.ctx)
	if err != nil || len(rr) != 1 || rr[0].Done != RepairOpen || h.m.Pending() == nil {
		t.Fatalf("a repair whose release is lost: %+v %v", rr, err)
	}
	h.m.Fail = nil
	if len(h.skipNotes()) != 0 {
		t.Fatalf("a judgment was written before the release")
	}
	rr, err = h.st.Repair(h.ctx)
	if err != nil || len(rr) != 1 || rr[0].Done != RepairSkipped || len(rr[0].Skipped) != 1 {
		t.Fatalf("the second repair: %+v %v", rr, err)
	}
	if rr, err := h.st.Repair(h.ctx); err != nil || len(rr) != 0 {
		t.Fatalf("a third repair: %+v %v", rr, err)
	}
	if n := len(h.skipNotes()); n != 1 {
		t.Fatalf("%d skip judgments over two repairs", n)
	}
	if h.state("s1-2") != sprint.Working || h.state("s1-1") != sprint.Ready {
		t.Fatalf("states %s %s", h.state("s1-1"), h.state("s1-2"))
	}
}

// F5. A replay of the cut step's caller operation id returns the result the
// repair recorded, with the skips.
func TestAReplayOfASkippedOperationReturnsTheSkips(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	step := h.cutStart("caller-9")
	if _, err := h.st.Repair(h.ctx); err != nil {
		t.Fatal(err)
	}
	res, err := h.st.Run(h.ctx, step)
	if err != nil || !res.Replay || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "s1-1") {
		t.Fatalf("replay: %+v %v", res, err)
	}
	if len(h.skipNotes()) != 1 {
		t.Fatalf("%d skip judgments", len(h.skipNotes()))
	}
}
