package store

// The engine defects the differential test found against the reference
// model, each as the sequence that showed it.

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// 1. A cross need is gone with a return: a later conflict stop resumes by
// what was done, whatever the card once needed.
func TestACrossNeedDoesNotSurviveAReturn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"x"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"y"}}))
	h.through("x")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Cross: "x=y"}))
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"x"}}, Reason: "cross wait"}))
	h.must(ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "d"}))
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"x"}}}))
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Conflict: "x"}))
	res := h.run(ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "rebased x"}))
	if len(res.Refused) != 0 || h.snap().StreamCtl("s1").F("state") != sprint.StreamMerging {
		t.Fatalf("resume after a conflict: %+v", res)
	}
	h.clean("resumed")
}

// 2. Releasing a stream's only landed card lands the stream: settle counts
// from the state after the step.
func TestReleasingTheOnlyCardLandsItsStream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.m.SetCoordinator(h.ctx, "tester"); err != nil {
		t.Fatal(err)
	}
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"p2"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"p4"}, Sentinel: true, Before: "p2"}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"p2"}}, Reason: "gone"}))
	h.must(ReleaseStep(sprint.ReleaseReq{IDs: []string{"p4"}, Reason: "done", Coordinator: "tester", Who: "tester"}))
	if st := h.snap().StreamCtl("s3").F("state"); st != sprint.StreamLanded {
		t.Fatalf("s3 is %s after its only card landed", st)
	}
	h.clean("released")
}

// 3. A sprint whose every card was dropped is done, 0 landed (said by the
// tick, which stops the machine: errata 3 amendment 6); a stream with every
// primary dropped is empty (waiting, no since), never landed.
func TestAnAllDroppedSprintIsDoneAndItsStreamEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"p1"}}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"p1"}}, Reason: "gone"}))
	res := h.machine()
	if res.Done != "0 landed, 1 dropped, took 0s from the first start" || len(h.openOf(sprint.NSprintDone)) != 0 || h.written(sprint.NSprintDone) != 1 {
		t.Fatalf("the sprint is done: %+v", res)
	}
	if c := h.snap().StreamCtl("s3"); c.F("state") != sprint.StreamWaiting || c.F("since") != "" {
		t.Fatalf("the empty stream: %v", c.Fields)
	}
	h.clean("all dropped")
}

// 4. A judgment is open per card and cause: a second ci red on the same card
// writes no second judgment; it takes the first one's place with the new
// run's text (reader finding 6), and one ack closes it.
func TestASecondCIRedOnACardWritesNoSecondJudgment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Red: true, Run: "r1", Note: "TestA timed out"}))
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Red: true, Run: "r2", Note: "TestB failed"}))
	open := h.openOf(sprint.NCIRed)
	if len(open) != 1 {
		t.Fatalf("ci red open %d times on s1-1", len(open))
	}
	if w := open[0].Note.What; !strings.Contains(w, "r2") || !strings.Contains(w, "TestB failed") || strings.Contains(w, "TestA") {
		t.Fatalf("the open ci red says %q, not the second run", w)
	}
	h.must(AckStep(sprint.AckReq{Notes: []string{open[0].Note.ID}, Reason: "a flaky runner"}))
	if n := len(h.openOf(sprint.NCIRed)); n != 0 {
		t.Fatalf("ci red still open after its ack: %d", n)
	}
	h.clean("acked")
}

// 5. ask --another's reader is for that attempt only: after rework the two
// original readers are asked again, and no third.
func TestAskAnotherDoesNotWidenTheReadersKept(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.a2ToReview("s1-1", false)
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	pair := h.snap().Work.Card("s1-1").F("asked")
	rc := h.snap().Readers.Of("s1-1")
	h.must(ReadStep(sprint.ReadReq{As: rc[0].Row, Verdict: "broken", Finding: "f", Sel: sprint.Sel{IDs: []string{rc[0].ID}}}))
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Another: true}))
	if got := h.snap().Work.Card("s1-1").F("asked"); got != pair {
		t.Fatalf("ask --another changed the readers kept: %s, was %s", got, pair)
	}
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "fix"}))
	c := h.snap().Fleet.Card("s1-1.w2")
	h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	var asked []string
	for _, rc := range h.snap().Readers.Of("s1-1") {
		if rc.Int("attempt") == 2 {
			asked = append(asked, rc.F("reader"))
		}
	}
	if len(asked) != 2 {
		t.Fatalf("readers asked at attempt 2: %v (kept %s)", asked, pair)
	}
	h.clean("asked again")
}

// 6. A refused add writes nothing: its stream's rows are not declared.
func TestARefusedAddWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	before := h.m.Revision("t-work")
	res := h.run(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b1"}, Needs: []string{"a1"}}))
	if len(res.Refused) == 0 {
		t.Fatalf("add with a need that does not exist: %+v", res)
	}
	s := h.snap()
	if s.Work.HasRow("s2") || s.Merge.HasRow("s2") || h.m.Revision("t-work") != before {
		t.Fatalf("the refused add declared s2: work %v merge %v", s.Work.Rows(), s.Merge.Rows())
	}
}

// 7. A merge step on a stream with nothing queued is refused and writes
// nothing, whatever its fact.
func TestAMergeStepWithNothingQueuedIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	before := h.m.Revision("t-merge")
	res := h.run(MergeStep(sprint.MergeReq{Stream: "s1", Red: true}))
	if len(res.Refused) != 1 || len(res.Moved) != 0 || !strings.Contains(res.Refused[0].Why, "nothing queued") {
		t.Fatalf("a merge step with nothing queued: %+v", res)
	}
	if h.m.Revision("t-merge") != before || h.snap().StreamCtl("s1").F("state") != sprint.StreamWaiting {
		t.Fatalf("the refused step wrote")
	}
}

// A verb that is refused has written nothing: a release naming an answer
// that is no judgment is refused whole, and the sentinel stays reached.
func TestAReleaseWithABadAnswerReleasesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.m.SetCoordinator(h.ctx, "tester"); err != nil {
		t.Fatal(err)
	}
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	if h.snap().Work.Card("stop").F("reached") == "" {
		t.Fatalf("stop is not reached")
	}
	res := h.run(ReleaseStep(sprint.ReleaseReq{IDs: []string{"stop"}, Reason: "done", Coordinator: "tester", Who: "tester", Answers: []string{"no-such-note.1"}}))
	if len(res.Refused) == 0 || len(res.Moved) != 0 {
		t.Fatalf("a release with a bad answer: %+v", res)
	}
	if h.state("stop") != sprint.Waiting || h.snap().Work.Card("stop").F("reached") == "" {
		t.Fatalf("the refused release moved stop: %s", h.state("stop"))
	}
}

// persistNeed writes need_card onto a merge card as a store written before
// the need was cleared everywhere would hold it.
func (h *harness) persistNeed(id, need, stream string) {
	h.t.Helper()
	s := h.snap()
	m := s.Merge.Card(id)
	if _, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-merge", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Merge.Revision),
		OperationID: "persisted-need-" + id, Members: []ntable.BatchMemberEntry{{ID: m.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(m.Rev)},
			Set: map[string]string{"need_card": need, "need_stream": stream}}}}); err != nil {
		h.t.Fatal(err)
	}
}

// Item 1a, the conflict's guard alone (reader finding 5): a queued card
// carrying a need (persisted) stopped by a conflict is stuck with no need.
func TestAConflictStopClearsANeed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"x"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"y"}}))
	h.through("x")
	h.persistNeed("x", "y", "s2")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Conflict: "x"}))
	if m := h.snap().Merge.Card("x"); m.Col != sprint.Stuck || m.F("need_card") != "" {
		t.Fatalf("x stopped by a conflict: %s need=%q", m.Col, m.F("need_card"))
	}
	h.clean("conflict")
}

// Item 1a, resume's guard alone: a conflict stop's stuck card carrying a
// need (persisted) does not hold the resume; only a cross stop waits.
func TestResumeWaitsForANeedOnlyAfterACross(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"x"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"y"}}))
	h.through("x")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Conflict: "x"}))
	h.persistNeed("x", "y", "s2")
	res := h.run(ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "rebased x"}))
	if len(res.Refused) != 0 || h.snap().StreamCtl("s1").F("state") != sprint.StreamMerging {
		t.Fatalf("resume after a conflict, a need persisted on its card: %+v", res)
	}
	h.clean("resumed")
}

// The reference model's redeal bound is the engine's.
func TestTheModelsRedealBoundIsTheEngines(t *testing.T) {
	t.Parallel()
	if refmodel.MaxRedeals != sprint.MaxRedeals || refmodel.Width != sprint.DefaultWidth || refmodel.DealAhead != sprint.DealAhead {
		t.Fatalf("the model's bounds (%d, %d, %d) are not the engine's (%d, %d, %d)", refmodel.MaxRedeals, refmodel.Width, refmodel.DealAhead, sprint.MaxRedeals, sprint.DefaultWidth, sprint.DealAhead)
	}
}

// The deal takes one card from each stream's front in turn (2.3 R6, the
// model's tickDeal and tla/SprintEvents.tla's TurnSorted): with two streams of
// four ready cards and two members at the default width, the engine deals all
// eight in one tick (errata 3 amendment 9), in turns a1 b1 a2 b2 ..., round the
// fleet m1 m2 m1 m2 ..., so every card of s1 goes to m1 and every card of s2 to
// m2, and the model agrees with the same choice; the order of the whole table
// would give each member two of each stream.
func TestTheDealTakesEachStreamsFrontInTurnAsTheModelDoes(t *testing.T) {
	t.Parallel()
	h := newDHarness(t)
	for _, a := range []dAction{
		{Kind: "fleet", Op: "up", Member: "m1"},
		{Kind: "fleet", Op: "up", Member: "m2"},
		{Kind: "start"},
		{Kind: "add", Stream: "s1", IDs: []string{"a1", "a2", "a3", "a4"}},
		{Kind: "add", Stream: "s2", IDs: []string{"b1", "b2", "b3", "b4"}},
		{Kind: "tick"},
	} {
		h.do(a)
	}
	for _, f := range h.findings {
		if _, known := dClassify(f); !known {
			t.Fatalf("a difference between the engine and the model on the deal:\n%s", f)
		}
	}
	s := h.observe()
	var dealt []string
	for id, p := range s.Primaries {
		if p.State == refmodel.Working {
			dealt = append(dealt, id+">"+s.Work[refmodel.WC(id, p.Attempt)].Member)
		}
	}
	slices.Sort(dealt)
	if want := []string{"a1>m1", "a2>m1", "a3>m1", "a4>m1", "b1>m2", "b2>m2", "b3>m2", "b4>m2"}; !slices.Equal(dealt, want) {
		t.Fatalf("the tick dealt %v, want %v: each stream's front in turn, round the fleet", dealt, want)
	}
}

// The deal goes round the fleet and the ask goes round the readers (errata 3,
// amendment 5; the model's NextMember and NextReaders, tla/SprintEvents.tla's
// RoundAssign and RoundTwo): with three idle members the second card goes to
// m2, past m1, where the shortest queue with its ties by name gives m1 again;
// with the first two readers reading, the second primary is asked of r3 and
// r1, past r2, where the shortest asked queues give r1 and r2. Red when either
// the engine's choice or the model's is put back to the shortest queue.
func TestTheDealAndTheAskGoRoundAsTheModelDoes(t *testing.T) {
	t.Parallel()
	h := newDHarness(t)
	for _, a := range []dAction{
		{Kind: "fleet", Op: "up", Member: "m1"},
		{Kind: "fleet", Op: "up", Member: "m2"},
		{Kind: "fleet", Op: "up", Member: "m3"},
		{Kind: "start"},
		{Kind: "add", Stream: "s1", IDs: []string{"a1"}},
		{Kind: "tick"},
		{Kind: "take", Member: "m1", Card: "a1.w1", Gen: 1},
		{Kind: "add", Stream: "s1", IDs: []string{"a2"}},
		{Kind: "tick"},
		{Kind: "finish", Member: "m1", Card: "a1.w1", Gen: 1, OK: true},
		{Kind: "tick"},
		{Kind: "begin", Reader: "r1", Card: "a1.r1.r1"},
		{Kind: "begin", Reader: "r2", Card: "a1.r1.r2"},
		{Kind: "take", Member: "m2", Card: "a2.w1", Gen: 1},
		{Kind: "finish", Member: "m2", Card: "a2.w1", Gen: 1, OK: true},
		{Kind: "tick"},
	} {
		h.do(a)
	}
	for _, f := range h.findings {
		if _, known := dClassify(f); !known {
			t.Fatalf("a difference between the engine and the model on the deal or the ask:\n%s", f)
		}
	}
	s := h.observe()
	if w := s.Work["a2.w1"]; w.Member != "m2" {
		t.Fatalf("a2 was dealt to %q, want m2: past m1, round the fleet", w.Member)
	}
	var readers []string
	for _, rc := range s.Reads {
		if rc.Primary == "a2" {
			readers = append(readers, rc.Reader)
		}
	}
	slices.Sort(readers)
	if want := []string{"r1", "r3"}; !slices.Equal(readers, want) {
		t.Fatalf("a2 was asked of %v, want %v: past r2, round the readers", readers, want)
	}
	if indexPast(s.Order, s.DealLast) != "m2" || indexPast(s.Readers, s.AskLast) != "r1" {
		t.Fatalf("the store's indexes are past %q and %q, want m2 and r1", s.DealLast, s.AskLast)
	}
}

// Every placement of a card on a member goes round the fleet and moves the
// index (errata 3, amendment 5: first attempts and redeals and levelling alike;
// the model's PlaceOn, ReworkChoice and levelRound): a rework, a down member's
// card dealt again and a card levelled each go to the next member past the
// index, where the shortest queue with its ties by name gives another. Red when
// either the engine's choice or the model's is put back to the shortest queue.
func TestTheRedealsAndTheLevelGoRoundAsTheModelDoes(t *testing.T) {
	t.Parallel()
	up := func(ms ...string) []dAction {
		var out []dAction
		for _, m := range ms {
			out = append(out, dAction{Kind: "fleet", Op: "up", Member: m})
		}
		return out
	}
	for _, c := range []struct {
		name    string
		actions []dAction
		card    string
		want    string // the member round the fleet
	}{
		// a1 on m1 and a2 on m2, the index past m2; a1 fails on m1: its rework
		// goes to m3, where the shortest queue gives m1
		{"rework", append(up("m1", "m2", "m3"),
			dAction{Kind: "start"},
			dAction{Kind: "add", Stream: "s1", IDs: []string{"a1", "a2"}},
			dAction{Kind: "tick"},
			dAction{Kind: "take", Member: "m1", Card: "a1.w1", Gen: 1},
			dAction{Kind: "finish", Member: "m1", Card: "a1.w1", Gen: 1, OK: false},
			dAction{Kind: "rework", IDs: []string{"a1"}}),
			"a1.w2", "m3"},
		// a1..a3 on m1..m3, the index past m3; m2 takes its card and m3 goes
		// down: a3 goes to m1, where the shortest queue gives m2
		{"down", append(up("m1", "m2", "m3"),
			dAction{Kind: "start"},
			dAction{Kind: "add", Stream: "s1", IDs: []string{"a1", "a2", "a3"}},
			dAction{Kind: "tick"},
			dAction{Kind: "take", Member: "m2", Card: "a2.w1", Gen: 1},
			dAction{Kind: "fleet", Op: "down", Member: "m3"}),
			"a3.w1", "m1"},
		// a1..a6 dealt m1 m2 m3 m1 m2 m3, the index past m3; m2 and m3 take
		// and finish all theirs: the backlogs (held less the width) are -62
		// -64 -64, and the level moves m1's newest, a4, round the fleet from
		// the index past m3 to m2 (m1 is the one it leaves)
		{"level", append(up("m1", "m2", "m3"),
			dAction{Kind: "start"},
			dAction{Kind: "add", Stream: "s1", IDs: []string{"a1", "a2", "a3", "a4", "a5", "a6"}},
			dAction{Kind: "tick"},
			dAction{Kind: "take", Member: "m2", Card: "a2.w1", Gen: 1},
			dAction{Kind: "take", Member: "m2", Card: "a5.w1", Gen: 1},
			dAction{Kind: "take", Member: "m3", Card: "a3.w1", Gen: 1},
			dAction{Kind: "take", Member: "m3", Card: "a6.w1", Gen: 1},
			dAction{Kind: "finish", Member: "m2", Card: "a2.w1", Gen: 1, OK: true},
			dAction{Kind: "finish", Member: "m2", Card: "a5.w1", Gen: 1, OK: true},
			dAction{Kind: "finish", Member: "m3", Card: "a3.w1", Gen: 1, OK: true},
			dAction{Kind: "finish", Member: "m3", Card: "a6.w1", Gen: 1, OK: true},
			dAction{Kind: "fleet", Op: "level"}),
			"a4.w1", "m2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newDHarness(t)
			for _, a := range c.actions {
				h.do(a)
			}
			for _, f := range h.findings {
				if _, known := dClassify(f); !known {
					t.Fatalf("a difference between the engine and the model:\n%s", f)
				}
			}
			s := h.observe()
			if w := s.Work[c.card]; w.Member != c.want {
				t.Fatalf("%s is on %q, want %s: round the fleet from the index", c.card, w.Member, c.want)
			}
			if indexPast(s.Order, s.DealLast) != c.want {
				t.Fatalf("the store's deal index is past %q, want %s: the placement moves it", s.DealLast, c.want)
			}
		})
	}
}
