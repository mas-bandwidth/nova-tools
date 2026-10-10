package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.Len(t, ids, 60, "started %d primaries", len(ids))
	h.takeAndFinish(true, ids...)
	open, err := h.m.OpenNotes(h.ctx)
	require.NoError(t, err)
	notes := map[string]bool{}
	subjects := map[string]bool{}
	for _, o := range open {
		if o.Note.Type == sprint.NWorkFailed {
			notes[o.Note.ID] = true
			subjects[o.Subject()] = true
		}
	}
	require.Len(t, subjects, 60, "60 failures leave %d open obligations", len(subjects))
	all, _, _ := h.m.NotesSince(h.ctx, "", 1000)
	lines := 0
	for _, n := range all {
		if n.Type == sprint.NWorkFailed {
			lines++
			require.LessOrEqual(t, len(n.Primaries), int(sprint.MaxListed), "the notification lists %d primaries, count %d", len(n.Primaries), n.Count)
			require.NotEqual(t, 0, n.Count, "the notification lists %d primaries, count %d", len(n.Primaries), n.Count)
		}
	}
	require.Equal(t, 1, lines, "%d failed-work lines for %d judgments; want one of each", lines, len(notes))
	require.Len(t, notes, 1, "%d failed-work lines for %d judgments; want one of each", lines, len(notes))
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
		assert.NoError(h.t, err)
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
	require.ErrorAs(h.t, err, &cut, "the step was not cut: %v", err)
	require.NotNil(h.t, h.m.Pending(), "the step was not cut: %v", err)
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
	require.NoError(t, err, "repair: %+v %v", rr, err)
	require.Len(t, rr, 1, "repair: %+v %v", rr, err)
	require.Equal(t, RepairSkipped, rr[0].Done, "repair: %+v %v", rr, err)
	require.Nil(t, h.m.Pending(), "the fence is still held")
	s := h.snap()
	if c := s.Work.Card("s1-1"); c.Col != sprint.Ready || c.F("brief") != "outside" {
		require.Failf(t, "", "s1-1 overwritten: %s brief=%s", c.Col, c.F("brief"))
	}
	c := s.Work.Card("s1-2")
	require.Equal(t, sprint.Working, c.Col, "s1-2, whose expectation held, is %s", c.Col)
	ns := h.skipNotes()
	require.Len(t, ns, 1, "%d skip judgments", len(ns))
	n := ns[0]
	require.Equal(t, sprint.Judgment, n.Kind, "skip judgment: %+v", n)
	require.Len(t, n.Primaries, 1, "skip judgment: %+v", n)
	require.Equal(t, "s1-1", n.Primaries[0], "skip judgment: %+v", n)
	require.Len(t, n.Decisions, len(RepairSkippedDecisions), "skip judgment: %+v", n)
	for _, want := range []string{"card s1-1", "t-work", "expected", "found revision 2"} {
		require.True(t, strings.Contains(n.What, want) || strings.Contains(rr[0].Skipped[0], want), "the skip does not say %q: %s | %v", want, n.What, rr[0].Skipped)
	}
	open, _ := h.m.OpenNotes(h.ctx)
	found := false
	for _, o := range open {
		found = found || (o.Note.ID == n.ID && o.Subject() == "s1-1")
	}
	require.True(t, found, "the skip judgment is not open on s1-1: %v", open)
	rep, _, err := h.st.Check(h.ctx, 3)
	require.NoError(t, err, "check after the repair: pending %q %v", rep.Pending, err)
	require.Empty(t, rep.Pending, "check after the repair: pending %q %v", rep.Pending, err)
	named := false
	for _, v := range rep.Violations {
		named = named || strings.Contains(v.Detail, "s1-1")
	}
	require.True(t, named, "check does not report the skipped move as it is: %v", rep.Violations)
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
	require.NoError(t, err, "the verb after the cut: %+v %v", res, err)
	require.Len(t, res.Repaired, 1, "the verb after the cut: %+v %v", res, err)
	require.True(t, strings.HasSuffix(res.Repaired[0], RepairSkipped), "the verb after the cut: %+v %v", res, err)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 1}))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s2-1"}}}))
	require.Equal(t, sprint.Working, h.state("s2-1"), "start after the repair: s2-1 is %s", h.state("s2-1"))
	require.Len(t, h.skipNotes(), 1, "%d skip judgments", len(h.skipNotes()))
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
	require.NoError(t, err, "a repair whose release is lost: %+v %v", rr, err)
	require.Len(t, rr, 1, "a repair whose release is lost: %+v %v", rr, err)
	require.Equal(t, RepairOpen, rr[0].Done, "a repair whose release is lost: %+v %v", rr, err)
	require.NotNil(t, h.m.Pending(), "a repair whose release is lost: %+v %v", rr, err)
	h.m.Fail = nil
	require.Empty(t, h.skipNotes(), "a judgment was written before the release")
	rr, err = h.st.Repair(h.ctx)
	require.NoError(t, err, "the second repair: %+v %v", rr, err)
	require.Len(t, rr, 1, "the second repair: %+v %v", rr, err)
	require.Equal(t, RepairSkipped, rr[0].Done, "the second repair: %+v %v", rr, err)
	require.Len(t, rr[0].Skipped, 1, "the second repair: %+v %v", rr, err)
	rr, err = h.st.Repair(h.ctx)
	require.NoError(t, err, "a third repair: %+v %v", rr, err)
	require.Empty(t, rr, "a third repair: %+v %v", rr, err)
	n := len(h.skipNotes())
	require.Equal(t, 1, n, "%d skip judgments over two repairs", n)
	require.Equal(t, sprint.Working, h.state("s1-2"), "states %s %s", h.state("s1-1"), h.state("s1-2"))
	require.Equal(t, sprint.Ready, h.state("s1-1"), "states %s %s", h.state("s1-1"), h.state("s1-2"))
}

// F5. A replay of the cut step's caller operation id returns the result the
// repair recorded, with the skips.
func TestAReplayOfASkippedOperationReturnsTheSkips(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	step := h.cutStart("caller-9")
	_, err := h.st.Repair(h.ctx)
	require.NoError(t, err)
	res, err := h.st.Run(h.ctx, step)
	require.NoError(t, err, "replay: %+v %v", res, err)
	require.True(t, res.Replay, "replay: %+v %v", res, err)
	require.Len(t, res.Skipped, 1, "replay: %+v %v", res, err)
	require.Contains(t, res.Skipped[0], "s1-1", "replay: %+v %v", res, err)
	require.Len(t, h.skipNotes(), 1, "%d skip judgments", len(h.skipNotes()))
}
