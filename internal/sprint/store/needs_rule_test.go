package store

// The needs rule held whatever builds the plan, and the tests that keep the
// stamps, ready to accept and the waiver of a dropped need honest: every claim
// driven as a sequence of verbs through the store on Mem, with check after
// every step.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// do runs a step that must not refuse, then checks every rule.
func (h *harness) nDo(step Step) Result {
	h.t.Helper()
	res := h.must(step)
	h.clean(step.Verb)
	h.tick(time.Minute)
	return res
}

// doR runs a step that may refuse, then checks every rule.
func (h *harness) nDoR(step Step) Result {
	h.t.Helper()
	res := h.run(step)
	h.clean(step.Verb)
	h.tick(time.Minute)
	return res
}

func (h *harness) nAllNotes(typ string) []sprint.Note {
	h.t.Helper()
	all, _, err := h.m.NotesSince(h.ctx, "", 100000)
	require.NoError(h.t, err)
	var out []sprint.Note
	for _, n := range all {
		if n.Type == typ && n.Kind == sprint.Judgment {
			out = append(out, n)
		}
	}
	return out
}

func (h *harness) nOpenOf(typ, subject string) []sprint.Open {
	h.t.Helper()
	open, err := h.m.OpenNotes(h.ctx)
	require.NoError(h.t, err)
	var out []sprint.Open
	for _, o := range open {
		if o.Note.Type == typ && (subject == "" || o.Subject() == subject) {
			out = append(out, o)
		}
	}
	return out
}

// work drives ready primaries to review with ok work.
func (h *harness) nWork(ids ...string) {
	h.t.Helper()
	h.nDo(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: ids}}))
	for _, id := range ids {
		s := h.snap()
		c := s.Fleet.Card(s.Work.Card(id).F("work"))
		h.nDo(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
		s = h.snap()
		c = s.Fleet.Card(c.ID)
		h.nDo(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	}
}

// readAll reports every outstanding read card of the primary with the verdict.
func (h *harness) nReadAll(id, verdict string) {
	h.t.Helper()
	s := h.snap()
	for _, rc := range s.Readers.Of(id) {
		if rc.Col == sprint.Asked || rc.Col == sprint.Reading {
			h.nDo(ReadStep(sprint.ReadReq{As: rc.Row, Verdict: verdict, Finding: "f", Sel: sprint.Sel{IDs: []string{rc.ID}}}))
		}
	}
}

// toMerging drives ready primaries to merging queued.
func (h *harness) nToMerging(ids ...string) {
	h.t.Helper()
	h.nWork(ids...)
	h.nDo(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: ids}}))
	for _, id := range ids {
		h.nReadAll(id, "ok")
	}
	h.nDo(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: ids}}))
}

func (h *harness) nLandStream(stream string) {
	h.t.Helper()
	h.nDo(MergeStep(sprint.MergeReq{Stream: stream, Batch: 1000}))
}

func TestStampsOnEveryPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.nDo(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	s := h.snap()
	wc := s.Fleet.Card("s1-1.w1")
	require.Equal(t, nStamp(t0), wc.F("dealt"), "deal: dealt=%q taken=%q", wc.F("dealt"), wc.F("taken"))
	require.Empty(t, wc.F("taken"), "deal: dealt=%q taken=%q", wc.F("dealt"), wc.F("taken"))
	h.nDo(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: 1}}))
	s = h.snap()
	wc = s.Fleet.Card("s1-1.w1")
	require.NotEmpty(t, wc.F("taken"), "take: no taken")
	takenAt := h.now
	_ = takenAt
	// the member holding it goes down: redealt, taken reset, dealt new
	h.nDo(FleetStep(sprint.FleetReq{Op: "down", Member: wc.Row}))
	s = h.snap()
	wc2 := s.Fleet.Card("s1-1.w1")
	if wc2.Col != sprint.Ready || wc2.F("taken") != "" || wc2.F("dealt") == wc.F("dealt") || wc2.F("dealt") == "" {
		require.Failf(t, "", "redeal after down: %s:%s dealt=%q (was %q) taken=%q", wc2.Row, wc2.Col, wc2.F("dealt"), wc.F("dealt"), wc2.F("taken"))
	}
	// the last member down: withdrawn, taken and dealt unset
	h.nDo(FleetStep(sprint.FleetReq{Op: "down", Member: wc2.Row}))
	s = h.snap()
	wc3 := s.Fleet.Card("s1-1.w1")
	if wc3.Col != sprint.Withdrawn || wc3.F("dealt") != "" || wc3.F("taken") != "" {
		require.Failf(t, "", "withdrawn: %s dealt=%q taken=%q", wc3.Col, wc3.F("dealt"), wc3.F("taken"))
	}
	// up again, start deals the same card again: dealt stamped
	h.nDo(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.nDo(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	s = h.snap()
	wc4 := s.Fleet.Card("s1-1.w1")
	if wc4.Col != sprint.Ready || wc4.F("dealt") != nStamp(h.now.Add(-time.Minute)) || wc4.F("withdrawn") != "" {
		require.Failf(t, "", "re-deal after withdrawal: %s dealt=%q withdrawn=%q", wc4.Col, wc4.F("dealt"), wc4.F("withdrawn"))
	}
	// level: more cards on m1, m2 comes up, the newest moves with dealt new
	h.nDo(AddStep(sprint.AddReq{Stream: "s1", Count: 3}))
	h.nDo(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-2", "s1-3", "s1-4"}}}))
	before := h.snap()
	h.nDo(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	after := h.snap()
	moved := 0
	for _, c := range after.Fleet.Cell("m2", sprint.Ready) {
		moved++
		require.NotEqual(t, before.Fleet.Card(c.ID).F("dealt"), c.F("dealt"), "level moved %s without a new dealt: %q", c.ID, c.F("dealt"))
		require.NotEmpty(t, c.F("dealt"), "level moved %s without a new dealt: %q", c.ID, c.F("dealt"))
	}
	require.NotEqual(t, 0, moved, "level moved nothing")
	// read cards: ask, ask --another, report from asked, rework re-ask
	h.nDo(TakeStep(sprint.TakeReq{As: wc4.Row, Sel: sprint.Sel{IDs: []string{wc4.ID}}, Gens: map[string]int{wc4.ID: wc4.Int("gen")}}))
	h.nDo(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc4.ID}}, Gens: map[string]int{wc4.ID: wc4.Int("gen")}}))
	h.nDo(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.nDo(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Another: true}))
	s = h.snap()
	rcs := s.Readers.Of("s1-1")
	require.Len(t, rcs, 3, "%d read cards", len(rcs))
	for _, rc := range rcs {
		if rc.F("asked") == "" || rc.F("begun") != "" {
			require.Failf(t, "", "%s asked=%q begun=%q", rc.ID, rc.F("asked"), rc.F("begun"))
		}
	}
	h.nDo(ReadStep(sprint.ReadReq{As: rcs[0].Row, Begin: true, Sel: sprint.Sel{IDs: []string{rcs[0].ID}}}))
	h.nDo(ReadStep(sprint.ReadReq{As: rcs[1].Row, Verdict: "broken", Finding: "x", Sel: sprint.Sel{IDs: []string{rcs[1].ID}}}))
	s = h.snap()
	for _, id := range []string{rcs[0].ID, rcs[1].ID} {
		require.NotEmpty(t, s.Readers.Card(id).F("begun"), "%s: no begun", id)
	}
	h.nDo(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	s = h.snap()
	w2 := s.Fleet.Card("s1-1.w2")
	require.NotNil(t, w2, "rework's card: %+v", w2)
	require.NotEmpty(t, w2.F("dealt"), "rework's card: %+v", w2)
	require.Empty(t, w2.F("taken"), "rework's card: %+v", w2)
	h.nDo(TakeStep(sprint.TakeReq{As: w2.Row, Sel: sprint.Sel{IDs: []string{w2.ID}}, Gens: map[string]int{w2.ID: 1}}))
	h.nDo(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{w2.ID}}, Gens: map[string]int{w2.ID: 1}}))
	h.nDo(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}})) // the machine's ask: the finish asks no reader
	s = h.snap()
	again := 0
	for _, rc := range s.Readers.Of("s1-1") {
		if rc.Int("attempt") == 2 {
			again++
			if rc.F("asked") == "" || rc.F("begun") != "" {
				require.Failf(t, "", "re-ask %s asked=%q begun=%q", rc.ID, rc.F("asked"), rc.F("begun"))
			}
		}
	}
	require.Equal(t, 2, again, "re-asked %d", again)
}

// ---- H2 -------------------------------------------------------------------

func TestReadyToAcceptOncePerAttempt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	h.nWork("s1-1", "s1-2", "s1-3")
	h.nDo(AskStep(sprint.AskReq{}))
	// s1-1: two oks, then a third reader broken
	h.nReadAll("s1-1", "ok")
	if n := len(h.nAllNotes(sprint.NReadyToAccept)); n != 1 || len(h.nOpenOf(sprint.NReadyToAccept, "s1-1")) != 1 {
		require.Fail(t, fmt.Sprintf("after two oks: %d notes", n))
	}
	h.nDo(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Another: true}))
	h.nReadAll("s1-1", "broken")
	if n := len(h.nAllNotes(sprint.NReadyToAccept)); n != 1 || len(h.nOpenOf(sprint.NReadyToAccept, "s1-1")) != 1 {
		require.Fail(t, fmt.Sprintf("after a third broken: %d notes", n))
	}
	t.Logf("s1-1 after a third reader's broken: open %v", h.judgmentsOn("s1-1"))
	// accept closes it, then return: two oks at head, no judgment
	h.nDo(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	require.Empty(t, h.judgmentsOn("s1-1"), "accept left open: %v", h.judgmentsOn("s1-1"))
	h.nDo(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "look again"}))
	t.Logf("s1-1 returned to review, open judgments: %v (ready to accept notes total %d)", h.judgmentsOn("s1-1"), len(h.nAllNotes(sprint.NReadyToAccept)))
	// s1-2: ok, broken, another, ok -> one; rework closes; new attempt: one more
	s := h.snap()
	rcs := s.Readers.Of("s1-2")
	h.nDo(ReadStep(sprint.ReadReq{As: rcs[0].Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rcs[0].ID}}}))
	h.nDo(ReadStep(sprint.ReadReq{As: rcs[1].Row, Verdict: "broken", Finding: "b", Sel: sprint.Sel{IDs: []string{rcs[1].ID}}}))
	require.Empty(t, h.nOpenOf(sprint.NReadyToAccept, "s1-2"), "ready to accept on one ok")
	h.nDo(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}, Another: true}))
	h.nReadAll("s1-2", "ok")
	if len(h.nOpenOf(sprint.NReadyToAccept, "s1-2")) != 1 {
		require.Fail(t, fmt.Sprintf("ok, broken, another ok: %v", h.judgmentsOn("s1-2")))
	}
	h.nDo(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}, Fix: "again"}))
	require.Empty(t, h.nOpenOf(sprint.NReadyToAccept, "s1-2"), "rework left ready to accept open")
	s = h.snap()
	w := s.Fleet.Card("s1-2.w2")
	h.nDo(TakeStep(sprint.TakeReq{As: w.Row, Sel: sprint.Sel{IDs: []string{w.ID}}, Gens: map[string]int{w.ID: 1}}))
	h.nDo(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{w.ID}}, Gens: map[string]int{w.ID: 1}}))
	h.nDo(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}})) // the machine's ask: the finish asks no reader
	h.nReadAll("s1-2", "ok")
	var on2 int
	for _, n := range h.nAllNotes(sprint.NReadyToAccept) {
		if len(n.Primaries) == 1 && n.Primaries[0] == "s1-2" {
			on2++
		}
	}
	require.Equal(t, 2, on2, "s1-2 over two attempts: %d notes, open %d", on2, len(h.nOpenOf(sprint.NReadyToAccept, "s1-2")))
	require.Len(t, h.nOpenOf(sprint.NReadyToAccept, "s1-2"), 1, "s1-2 over two attempts: %d notes, open %d", on2, len(h.nOpenOf(sprint.NReadyToAccept, "s1-2")))
	// s1-3: two oks, drop closes
	h.nReadAll("s1-3", "ok")
	h.nDo(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-3"}}, Reason: "gone"}))
	require.Empty(t, h.nOpenOf(sprint.NReadyToAccept, "s1-3"), "drop left ready to accept open")
}

// ---- H3 + waived ----------------------------------------------------------

func TestAddOnDroppedNeedAndWaive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.nDo(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "gone"}))
	// a need dropped and one not landed
	h.nDo(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1", "s1-2"}}))
	require.Equal(t, sprint.Waiting, h.state("b"), "b %s, blocked %d", h.state("b"), len(h.nOpenOf(sprint.NBlocked, "b")))
	require.Len(t, h.nOpenOf(sprint.NBlocked, "b"), 1, "b %s, blocked %d", h.state("b"), len(h.nOpenOf(sprint.NBlocked, "b")))
	id := h.nOpenOf(sprint.NBlocked, "b")[0].Note.ID
	h.nDo(AckStep(sprint.AckReq{Notes: []string{id}, Reason: "fine", Who: "tester"}))
	b := h.snap().Work.Card("b")
	require.Equal(t, sprint.Waiting, b.Col, "ack with another need open: %s waived=%q", b.Col, b.F("waived"))
	require.Equal(t, "s1-1", b.F("waived"), "ack with another need open: %s waived=%q", b.Col, b.F("waived"))
	h.nToMerging("s1-2")
	h.nLandStream("s1")
	require.Equal(t, sprint.Ready, h.state("b"), "b after s1-2 landed (s1-1 waived): %s", h.state("b"))
	// resolve on an add naming a dropped need: blocked only once
	h.nDo(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"c"}, Needs: []string{"s1-1"}}))
	h.nDoR(ResolveStep(sprint.ResolveReq{}))
	n := len(h.nOpenOf(sprint.NBlocked, "c"))
	require.Equal(t, 1, n, "c blocked %d times after resolve", n)
}

// A plan whose landing unit the lifecycle refuses still lends its landing to
// a waiting primary's move in the same plan: Lawful's landing set is built
// from every unit before any is refused.
func TestRefusedLandingSatisfiesNoNeed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.nWork("s1-1") // s1-1 in review
	h.nDo(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	mv := func(c *sprint.Card, col string) sprint.Unit {
		return sprint.Unit{Key: c.ID, Stream: c.Row, Changes: []sprint.Change{{Table: sprint.Work, Entry: ntable.BatchMemberEntry{ID: c.ID,
			Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}},
			Move:   &ntable.MemberMoveOp{Row: c.Row, Col: col}}}}}
	}
	step := Step{Verb: "mutant", Load: []string{sprint.Work}, Plan: func(s *sprint.Snapshot) sprint.Plan {
		p := sprint.Resolve(s, sprint.ResolveReq{}) // a plan carrying the pre-state
		p.Units = append(p.Units, mv(s.Work.Card("s1-1"), sprint.Landed), mv(s.Work.Card("b"), sprint.Ready))
		return p
	}}
	res := h.run(step)
	t.Logf("mutant: moved %v refused %v; s1-1 %s, b %s", res.Moved, res.Refused, h.state("s1-1"), h.state("b"))
	if h.state("b") != sprint.Waiting {
		rep, _, _ := h.st.Check(h.ctx, 3)
		require.Fail(t, fmt.Sprintf("b left waiting with s1-1 %s (landing refused): %v", h.state("s1-1"), rep.Violations))
	}
}

// A move with no place expectation is judged by neither unlawful nor unmet.
func TestMoveWithoutPlaceIsJudgedFromThePreState(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.nDo(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	step := Step{Verb: "mutant", Load: []string{sprint.Work}, Plan: func(s *sprint.Snapshot) sprint.Plan {
		p := sprint.Resolve(s, sprint.ResolveReq{})
		c := s.Work.Card("b")
		p.Units = append(p.Units, sprint.Unit{Key: c.ID, Stream: c.Row, Changes: []sprint.Change{{Table: sprint.Work, Entry: ntable.BatchMemberEntry{ID: c.ID,
			Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev)},
			Move:   &ntable.MemberMoveOp{Row: c.Row, Col: sprint.Ready}}}}})
		return p
	}}
	res := h.run(step)
	t.Logf("mutant: moved %v refused %v; b %s", res.Moved, res.Refused, h.state("b"))
	if h.state("b") != sprint.Waiting {
		rep, _, _ := h.st.Check(h.ctx, 3)
		require.Fail(t, fmt.Sprintf("b left waiting past s1-1 with an expectation of revision only: %v", rep.Violations))
	}
}

// A landing merge cut before its work manifest, the landed card changed by a
// writer outside the fence: repair skips the landing and applies the waiter's
// move, which was built on that landing.
func TestRepairSkipsTheWaiterOfASkippedLanding(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.nDo(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	h.nToMerging("s1-1")
	st := *h.st
	st.B = h.outsideWrite("s1-1")
	_, err := st.Run(h.ctx, MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1}))
	var cut *CutError
	require.ErrorAs(t, err, &cut, "not cut: %v", err)
	h.tick(time.Hour)
	rr, err := h.st.Repair(h.ctx)
	require.NoError(t, err)
	rep, _, _ := h.st.Check(h.ctx, 3)
	t.Logf("repair %+v; s1-1 %s, b %s; violations %v", rr, h.state("s1-1"), h.state("b"), rep.Violations)
	if h.state("b") == sprint.Ready && h.state("s1-1") != sprint.Landed {
		require.Fail(t, fmt.Sprintf("b is ready with s1-1 %s after a skipping repair (skip judgment names %v)", h.state("s1-1"), h.skipNotes()[0].Primaries))
	}
	// One skip judgment lists the landing and the waiter, with the reason.
	sk := h.skipNotes()
	if len(sk) != 1 || strings.Join(sk[0].Primaries, ",") != "b,s1-1" || !strings.Contains(sk[0].What, "the lifecycle") || !strings.Contains(sk[0].What, "b needs s1-1, not landed") {
		require.Fail(t, fmt.Sprintf("skip judgments: %+v", sk))
	}
	// The skipped landing leaves s1-1 merging and its merge card merged
	// (rules 4 and 5, the skip judgment's to decide); b stays waiting.
	for _, v := range rep.Violations {
		if v.Rule == 11 {
			require.Fail(t, fmt.Sprintf("b %s; %v", h.state("b"), v))
		}
	}
	require.Equal(t, sprint.Waiting, h.state("b"), "b %s", h.state("b"))
}

func nStamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// A primary returned to review with two different readers' ok at its head is
// acceptable and the subject of no open judgment: nothing in the inbox asks
// for its accept, rework or drop, and no further read raises one (the ready to
// accept judgment is written only by the second ok).
func TestReturnedAcceptablePrimaryIsNotSilent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.nToMerging("s1-1")
	h.nDo(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "hold it"}))
	h.nDo(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Another: true}))
	h.nReadAll("s1-1", "ok")
	h.readInbox()
	require.Equal(t, sprint.Review, h.state("s1-1"), "s1-1 %s", h.state("s1-1"))
	require.NotEmpty(t, h.judgmentsOn("s1-1"), "s1-1 is in review, acceptable (three oks at its head), and the subject of no open judgment")
}
