package store

// The engine defects the differential test found against the reference
// model, each as the sequence that showed it.

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
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
	require.Empty(t, res.Refused, "resume after a conflict: %+v", res)
	require.Equal(t, string(sprint.StreamMerging), h.snap().StreamCtl("s1").F("state"), "resume after a conflict: %+v", res)
	h.clean("resumed")
}

// 2. Releasing a stream's only landed card lands the stream: settle counts
// from the state after the step.
func TestReleasingTheOnlyCardLandsItsStream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.m.SetCoordinator(h.ctx, "tester"))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"p2"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"p4"}, Sentinel: true, Before: "p2"}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"p2"}}, Reason: "gone"}))
	h.must(ReleaseStep(sprint.ReleaseReq{IDs: []string{"p4"}, Reason: "done", Coordinator: "tester", Who: "tester"}))
	st := h.snap().StreamCtl("s3").F("state")
	require.Equal(t, string(sprint.StreamLanded), st, "s3 is %s after its only card landed", st)
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
	require.Equal(t, "0 landed, 1 dropped, took 0s from the first start", res.Done, "the sprint is done: %+v", res)
	require.Empty(t, h.openOf(sprint.NSprintDone), "the sprint is done: %+v", res)
	require.Equal(t, 1, h.written(sprint.NSprintDone), "the sprint is done: %+v", res)
	c := h.snap().StreamCtl("s3")
	require.Equal(t, sprint.StreamWaiting, c.F("state"), "the empty stream: %v", c.Fields)
	require.Equal(t, "", c.F("since"), "the empty stream: %v", c.Fields)
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
	require.Len(t, open, 1, "ci red open %d times on s1-1", len(open))
	w := open[0].Note.What
	require.Contains(t, w, "r2", "the open ci red says %q, not the second run", w)
	require.Contains(t, w, "TestB failed", "the open ci red says %q, not the second run", w)
	require.NotContains(t, w, "TestA", "the open ci red says %q, not the second run", w)
	h.must(AckStep(sprint.AckReq{Notes: []string{open[0].Note.ID}, Reason: "a flaky runner"}))
	n := len(h.openOf(sprint.NCIRed))
	require.Equal(t, 0, n, "ci red still open after its ack: %d", n)
	h.clean("acked")
}

// 5. ask --another's reader is for that attempt only: it leaves the primary's
// asked field as the two of the attempt, and after rework attempt 2 is asked
// of two different readers together (reads are asked together), and no third.
func TestAskAnotherIsForItsAttemptOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.a2ToReview("s1-1", false)
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	pair := h.snap().Work.Card("s1-1").F("asked")
	rc := h.snap().Readers.Of("s1-1")
	h.must(ReadStep(sprint.ReadReq{Usage: "input=1000 output=100", As: rc[0].Row, Verdict: "broken", Finding: "f:1", Sel: sprint.Sel{IDs: []string{rc[0].ID}}}))
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Another: true}))
	got := h.snap().Work.Card("s1-1").F("asked")
	require.Equal(t, pair, got, "ask --another changed the primary's asked field: %s, was %s", got, pair)
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "fix"}))
	c := h.snap().Fleet.Card("s1-1.w2")
	h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}})) // the machine's ask: the finish asks no reader
	var asked []string
	for _, rc := range h.snap().Readers.Of("s1-1") {
		if rc.Int("attempt") == 2 {
			asked = append(asked, rc.F("reader"))
		}
	}
	require.Len(t, asked, 2, "readers asked at attempt 2: both reads together, and no third")
	require.NotEqual(t, asked[0], asked[1], "two different readers")
	h.clean("asked again")
}

// 6. A refused add writes nothing: its stream's rows are not declared.
func TestARefusedAddWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	before := h.m.Revision("t-work")
	res := h.run(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b1"}, Needs: []string{"a1"}}))
	require.NotEmpty(t, res.Refused, "add with a need that does not exist: %+v", res)
	s := h.snap()
	if s.Work.HasRow("s2") || s.Merge.HasRow("s2") || h.m.Revision("t-work") != before {
		require.Failf(t, "", "the refused add declared s2: work %v merge %v", s.Work.Rows(), s.Merge.Rows())
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
	require.Len(t, res.Refused, 1, "a merge step with nothing queued: %+v", res)
	require.Empty(t, res.Moved, "a merge step with nothing queued: %+v", res)
	require.Contains(t, res.Refused[0].Why, "nothing queued", "a merge step with nothing queued: %+v", res)
	require.Equal(t, before, h.m.Revision("t-merge"), "the refused step wrote")
	require.Equal(t, string(sprint.StreamWaiting), h.snap().StreamCtl("s1").F("state"), "the refused step wrote")
}

// A verb that is refused has written nothing: a release naming an answer
// that is no judgment is refused whole, and the sentinel stays reached.
func TestAReleaseWithABadAnswerReleasesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.m.SetCoordinator(h.ctx, "tester"))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	require.NotEmpty(t, h.snap().Work.Card("stop").F("reached"), "stop is not reached")
	res := h.run(ReleaseStep(sprint.ReleaseReq{IDs: []string{"stop"}, Reason: "done", Coordinator: "tester", Who: "tester", Answers: []string{"no-such-note.1"}}))
	require.NotEmpty(t, res.Refused, "a release with a bad answer: %+v", res)
	require.Empty(t, res.Moved, "a release with a bad answer: %+v", res)
	require.Equal(t, sprint.Waiting, h.state("stop"), "the refused release moved stop: %s", h.state("stop"))
	require.NotEmpty(t, h.snap().Work.Card("stop").F("reached"), "the refused release moved stop: %s", h.state("stop"))
}

// persistNeed writes need_card onto a merge card as a store written before
// the need was cleared everywhere would hold it.
func (h *harness) persistNeed(id, need, stream string) {
	h.t.Helper()
	s := h.snap()
	m := s.Merge.Card(id)
	_, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-merge", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Merge.Revision),
		OperationID: "persisted-need-" + id, Members: []ntable.BatchMemberEntry{{ID: m.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(m.Rev)},
			Set: map[string]string{"need_card": need, "need_stream": stream}}}})
	require.NoError(h.t, err)
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
		require.Failf(t, "", "x stopped by a conflict: %s need=%q", m.Col, m.F("need_card"))
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
	require.Empty(t, res.Refused, "resume after a conflict, a need persisted on its card: %+v", res)
	require.Equal(t, string(sprint.StreamMerging), h.snap().StreamCtl("s1").F("state"), "resume after a conflict, a need persisted on its card: %+v", res)
	h.clean("resumed")
}

// The reference model's redeal bound is the engine's.
func TestTheModelsRedealBoundIsTheEngines(t *testing.T) {
	t.Parallel()
	require.Equal(t, sprint.MaxRedeals, refmodel.MaxRedeals, "the model's bounds (%d, %d, %d) are not the engine's (%d, %d, %d)", refmodel.MaxRedeals, refmodel.Width, refmodel.DealAhead, sprint.MaxRedeals, sprint.DefaultWidth, sprint.DealAhead)
	require.Equal(t, sprint.DefaultWidth, refmodel.Width, "the model's bounds (%d, %d, %d) are not the engine's (%d, %d, %d)", refmodel.MaxRedeals, refmodel.Width, refmodel.DealAhead, sprint.MaxRedeals, sprint.DefaultWidth, sprint.DealAhead)
	require.Equal(t, sprint.DealAhead, refmodel.DealAhead, "the model's bounds (%d, %d, %d) are not the engine's (%d, %d, %d)", refmodel.MaxRedeals, refmodel.Width, refmodel.DealAhead, sprint.MaxRedeals, sprint.DefaultWidth, sprint.DealAhead)
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
		_, known := dClassify(f)
		require.True(t, known, "a difference between the engine and the model on the deal:\n%s", f)
	}
	s := h.observe()
	var dealt []string
	for id, p := range s.Primaries {
		if p.State == refmodel.Working {
			dealt = append(dealt, id+">"+s.Work[refmodel.WC(id, p.Attempt)].Member)
		}
	}
	slices.Sort(dealt)
	want := []string{"a1>m1", "a2>m1", "a3>m1", "a4>m1", "b1>m2", "b2>m2", "b3>m2", "b4>m2"}
	require.True(t, slices.Equal(dealt, want), "the tick dealt %v, want %v: each stream's front in turn, round the fleet", dealt, want)
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
		{Kind: "take", Member: "m2", Card: "a2.w1", Gen: 1},
		{Kind: "finish", Member: "m2", Card: "a2.w1", Gen: 1, OK: true},
		{Kind: "tick"},
	} {
		h.do(a)
	}
	for _, f := range h.findings {
		_, known := dClassify(f)
		require.True(t, known, "a difference between the engine and the model on the deal or the ask:\n%s", f)
	}
	s := h.observe()
	w := s.Work["a2.w1"]
	require.Equal(t, "m2", w.Member, "a2 was dealt to %q, want m2: past m1, round the fleet", w.Member)
	var readers []string
	for _, rc := range s.Reads {
		if rc.Primary == "a2" {
			readers = append(readers, rc.Reader)
		}
	}
	slices.Sort(readers)
	want := []string{"r1", "r3"} // both reads together (reads are asked together): r3 and r1, past r2
	require.True(t, slices.Equal(readers, want), "a2 was asked of %v, want %v: past r2, round the readers", readers, want)
	require.Equal(t, "m2", indexPast(s.Order, s.DealLast), "the store's indexes are past %q and %q, want m2 and r1", s.DealLast, s.AskLast)
	require.Equal(t, "r1", indexPast(s.Readers, s.AskLast), "the store's indexes are past %q and %q, want m2 and r1", s.DealLast, s.AskLast)
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
				_, known := dClassify(f)
				require.True(t, known, "a difference between the engine and the model:\n%s", f)
			}
			s := h.observe()
			w := s.Work[c.card]
			require.Equal(t, c.want, w.Member, "%s is on %q, want %s: round the fleet from the index", c.card, w.Member, c.want)
			require.Equal(t, c.want, indexPast(s.Order, s.DealLast), "the store's deal index is past %q, want %s: the placement moves it", s.DealLast, c.want)
		})
	}
}

// The model's brief bound is the engine's (sprint.AtBriefBound): the stream's attempt cap, else
// the sprint's, counted from the attempt its brief was last replaced at, and the same landing
// refusal twice. A model with the default cap of 4 counted from attempt 0 reworked the card the
// engine stops at its second attempt under a stream cap of 2, and stopped the card the engine
// reworks at its third attempt, one attempt after a brief replaced at its second.
func TestTheModelsBriefBoundIsTheEngines(t *testing.T) {
	t.Parallel()
	h := newDHarness(t)
	do := func(a dAction) {
		t.Helper()
		h.do(a)
		require.Empty(t, h.findings, "the engine and the model differ after %+v", a)
	}
	run := func(step Step) {
		t.Helper()
		res, err := h.st.Run(h.ctx, step)
		require.NoError(t, err)
		require.Empty(t, res.Refused, step.Verb)
		h.model = h.observe()
	}
	for _, a := range []dAction{{Kind: "fleet", Op: "up", Member: "m1"}, {Kind: "start"}, {Kind: "add", Stream: "s1", IDs: []string{"x"}}} {
		do(a)
	}
	run(SetStep(sprint.SetReq{Streams: []string{"s1"}, Attempts: "2", Who: dCoordinator}))
	require.Equal(t, 2, h.model.AttemptsCap("s1"))
	toMerging := func() {
		t.Helper()
		do(dAction{Kind: "tick"})
		w := refmodel.WC("x", h.model.Primaries["x"].Attempt)
		do(dAction{Kind: "take", Member: "m1", Card: w, Gen: h.model.Work[w].Gen})
		do(dAction{Kind: "finish", Member: "m1", Card: w, Gen: h.model.Work[w].Gen, OK: true})
		for range 4 {
			do(dAction{Kind: "tick"})
			if h.model.Primaries["x"].State == refmodel.Merging {
				return
			}
			for _, id := range refmodel.Keys(h.model.Reads) {
				if rc := h.model.Reads[id]; rc.Primary == "x" && rc.Place == refmodel.Asked {
					do(dAction{Kind: "read", Reader: rc.Reader, Card: id, Gen: max(h.readGen[id], 1), OK: true})
					require.Equal(t, refmodel.OK, h.model.Reads[id].Place, "the exact-generation verdict must close %s", id)
				}
			}
		}
		require.Equal(t, refmodel.Merging, h.model.Primaries["x"].State)
	}
	refused := func(way string) refmodel.Primary {
		t.Helper()
		do(dAction{Kind: "merge", Stream: "s1", Batch: 1, Fact: "conflict", IDs: []string{"x"}, Way: way})
		return h.model.Primaries["x"]
	}

	toMerging()
	require.Equal(t, refmodel.Ready, refused(refmodel.RefusedGate).State, "attempt 1 of 2: reworked")
	toMerging()
	p := refused(refmodel.RefusedConflict)
	require.Equal(t, refmodel.Review, p.State, "attempt 2 of 2: at the stream's cap")
	require.True(t, h.model.Open[refmodel.Judgment{Type: refmodel.JBriefWrong, Subject: "x"}])
	require.Less(t, p.Attempt, refmodel.AttemptsDefault, "a model with the default cap would have reworked it")

	// a new brief at attempt 2: the cap counts from it
	snap, err := h.st.Load(h.ctx, []string{sprint.Work}, nil)
	require.NoError(t, err)
	run(BriefStep(sprint.BriefReq{ID: "x", Brief: snap.Work.Card("x").F("brief") + "\nThe brief, replaced.\n", Who: dCoordinator}))
	require.Equal(t, 2, h.model.Primaries["x"].BriefAt)
	do(dAction{Kind: "rework", IDs: []string{"x"}})
	toMerging()
	p = refused(refmodel.RefusedGate)
	require.Equal(t, refmodel.Ready, p.State, "attempt 3, one since its brief: reworked")
	require.GreaterOrEqual(t, p.Attempt-1, 2, "a cap counted from attempt 0 would have stopped it")
}
