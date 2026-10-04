package refmodel_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// brief is a move as one short line: what it does to which card or judgment,
// without its fields and words, which the tests of a duty read where they
// matter. The fixtures of a duty state the moves it makes as these lines.
func brief(m refmodel.Move) string {
	switch m.Kind {
	case refmodel.KindMove:
		return fmt.Sprintf("move %s %s %s>%s", m.Table, m.Card, m.From, m.To)
	case refmodel.KindCreate:
		return fmt.Sprintf("create %s %s >%s", m.Table, m.Card, m.To)
	case refmodel.KindProp:
		return fmt.Sprintf("prop %s %s", m.Table, strings.Join(m.Set, ","))
	case refmodel.KindSet, refmodel.KindRemove, refmodel.KindBump:
		return fmt.Sprintf("%s %s %s %s", m.Kind, m.Table, m.Card, strings.Join(m.Set, ","))
	case refmodel.KindClose:
		return fmt.Sprintf("close %s %v", m.Type, m.Subjects)
	case refmodel.KindDue, refmodel.KindPush, refmodel.KindRow, refmodel.KindRefuse:
		return m.Kind + " " + m.Card + strings.Join(m.Attrs, ",")
	}
	return fmt.Sprintf("%s %s %v", m.Kind, m.Type, m.Subjects)
}

func briefs(ms []refmodel.Move) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = brief(m)
	}
	return out
}

// expect fails the test unless the moves are exactly the lines, in canonical
// order. It stops the test there: what a test reads of the moves after it is
// read only of moves that are the ones expected.
func expect(t *testing.T, got []refmodel.Move, want ...string) {
	t.Helper()
	if !slices.Equal(briefs(got), want) {
		require.Failf(t, "assertion failed", "moves:\n got %q\nwant %q\nfull:%s", briefs(got), want, show(got))
	}
}

func TestStrangersAreToldOnceEachInNameOrder(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	snap := w.snapshot(w.fresh())
	snap.Untold = []string{"x2", "m1", "x1"}
	got := refmodel.StrangerMoves(snap, later(0))
	expect(t, got,
		"notice an unknown machine is beating []",
		"notice an unknown machine is beating []")
	assert.Equal(t, "an unknown machine is beating: x1; add it with nova-sprint fleet up x1", got[0].Words, "the words: %q, %q", got[0].Words, got[1].Words)
	assert.Contains(t, got[1].Words, "x2", "the words: %q, %q", got[0].Words, got[1].Words)
	assert.Equal(t, []string{"who=machine"}, got[0].Attrs, "the notice is not the machine's: %v", got[0].Attrs)
	snap.Untold = nil
	expect(t, refmodel.StrangerMoves(snap, later(0)))
}

func TestPresenceTakesAMemberDownAndDealsItsCards(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1", "m2")
	w.add(t, "s1", 3)
	w.deal(t, "s1-1") // m1
	w.deal(t, "s1-2") // m2
	w.deal(t, "s1-3") // m1
	w.take(t, "s1-1")
	beats := w.fresh()
	beats["m1"] = 2 * time.Minute // its last beat is long past
	got := refmodel.PresenceMoves(w.snapshot(beats), later(0))
	expect(t, got,
		"set fleet ctl-m1 since=2030-01-02T03:04:05Z,status=down",
		"prop fleet deal_index=4", // every placement moves the deal's counter, by two past the member down (errata 3 amendment 5)
		"move fleet s1-1.w1 m1:working>m2:ready",
		"move fleet s1-3.w1 m1:ready>m2:ready",
		"notice fleet member down []")
	byCard := map[string]refmodel.Move{}
	for _, m := range got {
		byCard[m.Card] = m
	}
	s := byCard["s1-1.w1"].Set
	assert.Contains(t, s, "gen=2", "a card taken and redealt is a new generation of the new member, and counts one redeal: %v", s)
	assert.Contains(t, s, "member=m2", "a card taken and redealt is a new generation of the new member, and counts one redeal: %v", s)
	assert.Contains(t, s, "redeals=1", "a card taken and redealt is a new generation of the new member, and counts one redeal: %v", s)
	assert.Contains(t, byCard["s1-1.w1"].Unset, "taken", "a redealt card is no longer taken: %v", byCard["s1-1.w1"].Unset)
}

func TestPresenceBringsAMemberUpWhenItBeatsAndLevelsTheQueues(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 3)
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		w.deal(t, id)
	}
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "release", Member: "m2", Who: coordinator})) // added down until it beats
	got := refmodel.PresenceMoves(w.snapshot(w.fresh()), later(0))
	expect(t, got,
		"set fleet ctl-m2 since=2030-01-02T03:04:05Z,status=up",
		"prop fleet deal_index=2", // the levelling moves the deal's counter too
		"move fleet s1-3.w1 m1:ready>m2:ready",
		"notice fleet member up []")
}

func TestPresenceLeavesAMemberThatBeatsAlone(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1", "m2")
	w.add(t, "s1", 2)
	expect(t, refmodel.PresenceMoves(w.snapshot(w.fresh()), later(0)))
	// nothing read of the beats at all: the duty does nothing
	snap := w.snapshot(nil)
	snap.Beats = nil
	expect(t, refmodel.PresenceMoves(snap, later(time.Hour)))
}

func TestResolveReadiesAWaitingPrimaryWhoseNeedsLanded(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.addOne(t, "s1", "s1-2", "s1-1")
	w.addOne(t, "s1", "s1-3", "s1-2")
	w.drive(t, "s1-1", sprint.Merging)
	w.land(t, "s1-1") // landed, and not by a step that moves what waited on it
	require.Equal(t, sprint.Waiting, w.state("s1-2"), "the fixture: s1-2 is %s, s1-3 is %s", w.state("s1-2"), w.state("s1-3"))
	require.Equal(t, sprint.Waiting, w.state("s1-3"), "the fixture: s1-2 is %s, s1-3 is %s", w.state("s1-2"), w.state("s1-3"))
	got := refmodel.ResolveMoves(w.snapshot(w.fresh()), later(0))
	expect(t, got, "move work s1-2 s1:waiting>s1:ready")
	assert.Equal(t, "s1-2 waiting -> ready", got[0].Words, "the words: %q", got[0].Words)
}

func TestResolveRaisesABlockedJudgmentOnceForADroppedNeed(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.addOne(t, "s1", "s1-2", "s1-1")
	w.must(t, sprint.Drop(w.s, sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "not wanted", Who: coordinator}))
	// dropping told the coordinator already; the judgment is open, so the tick writes none again
	expect(t, refmodel.ResolveMoves(w.snapshot(w.fresh()), later(0)))
	// with the judgment closed, the tick raises it
	snap := w.snapshot(w.fresh())
	snap.Tables = withoutJudgments(snap.Tables, sprint.NBlocked)
	got := refmodel.ResolveMoves(snap, later(0))
	expect(t, got, "open a primary is blocked on something dropped [s1-2]")
	assert.Equal(t, []string{"count=1", "needs=s1-1", "who=machine"}, got[0].Attrs, "the blocked judgment names its need: %v", got[0].Attrs)
}

func TestResolveReachesASentinelWhoseNeedsLanded(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.sentinel(t, "s1", "gate-1")
	w.drive(t, "s1-1", sprint.Merging)
	w.land(t, "s1-1")
	got := refmodel.ResolveMoves(w.snapshot(w.fresh()), later(0))
	expect(t, got,
		"set work gate-1 reached=2030-01-02T03:04:05Z",
		"open sentinel reached [gate-1]")
	// reached once, and never made ready: the coordinator releases it
	w.must(t, sprint.Resolve(w.s, sprint.ResolveReq{Sel: sprint.Sel{Only: []string{"gate-1"}}}))
	expect(t, refmodel.ResolveMoves(w.snapshot(w.fresh()), later(0)))
}

// withoutJudgments is the tables with the open judgments of the types closed.
func withoutJudgments(s *sprint.Snapshot, types ...string) *sprint.Snapshot {
	c := *s
	c.Open = nil
	for _, o := range s.Open {
		if !slices.Contains(types, o.Note.Type) {
			c.Open = append(c.Open, o)
		}
	}
	return &c
}

func TestResumeGoesOnWhenTheCardAStreamNeededLanded(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.add(t, "s2", 1)
	w.drive(t, "s1-1", sprint.Merging)
	w.must(t, sprint.MergeStep(w.s, sprint.MergeReq{Stream: "s1", Batch: 1, Cross: "s1-1=s2-1", Who: merger}))
	ctl := w.s.StreamCtl("s1")
	require.Equal(t, sprint.StreamStopped, ctl.F("state"), "the fixture: s1 is %s, cause %s", ctl.F("state"), ctl.F("cause"))
	require.Equal(t, "cross", ctl.F("cause"), "the fixture: s1 is %s, cause %s", ctl.F("state"), ctl.F("cause"))
	// the card it needs has not landed: nothing to resume
	expect(t, refmodel.ResumeMoves(w.snapshot(w.fresh()), later(0)))
	w.drive(t, "s2-1", sprint.Landed)
	got := refmodel.ResumeMoves(w.snapshot(w.fresh()), later(0))
	expect(t, got,
		"set merge ctl-s1 did=the card it needed landed,since=2030-01-02T03:04:05Z,state=merging",
		"move merge s1-1 s1:stuck>s1:queued",
		"notice stream resumed: the card it needed landed [s1-1]",
		"close stream stopped: needs a card of another stream first [stream:s1]")
}

func TestDealDealsTheOldestReadyToTheMembersWithRoom(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1", "m2")
	w.add(t, "s1", 6)
	got := refmodel.DealMoves(w.snapshot(w.fresh()), later(0))
	// two members, each at the default width of 64: all six in one deal,
	// round the fleet (errata 3, amendments 5 and 9), the fleet table's
	// deal_index past m2
	expect(t, got,
		"move work s1-1 s1:ready>s1:working",
		"move work s1-2 s1:ready>s1:working",
		"move work s1-3 s1:ready>s1:working",
		"move work s1-4 s1:ready>s1:working",
		"move work s1-5 s1:ready>s1:working",
		"move work s1-6 s1:ready>s1:working",
		"prop work stream_index=6",
		"prop fleet deal_index=6",
		"create fleet s1-1.w1 >m1:ready",
		"create fleet s1-2.w1 >m2:ready",
		"create fleet s1-3.w1 >m1:ready",
		"create fleet s1-4.w1 >m2:ready",
		"create fleet s1-5.w1 >m1:ready",
		"create fleet s1-6.w1 >m2:ready")
}

func TestDealTellsOnceWhenNoMemberIsUp(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 2)
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "hold", Member: "m1", Who: coordinator}))
	got := refmodel.DealMoves(w.snapshot(nil), later(0))
	expect(t, got, "open no fleet member is up [stream:]")
	assert.Empty(t, got[0].Stream, "the judgment is about the sprint, and counts what waits: %+v", got[0])
	assert.Contains(t, got[0].Attrs, "stream_level=yes", "the judgment is about the sprint, and counts what waits: %+v", got[0])
	assert.True(t, strings.HasPrefix(got[0].Words, "2 primaries wait to be dealt"), "the judgment is about the sprint, and counts what waits: %+v", got[0])
	want := []string{"fleet beat", "fleet up", "wait"}
	assert.Equal(t, want, got[0].Decisions, "the decisions offered: %q, want %q", got[0].Decisions, want)
}

// A card is dealt again after a take of it ended up to three times: withdrawn
// after a fourth (its take ended, its count at three) the tick deals it no more
// and says so; at two it is still dealt, and so is a card at three that was
// withdrawn while ready, never taken (here no member is up, so it waits, and
// the sprint is told there is none).
func TestDealStopsAtTheRedealBound(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		redeals string
		ended   bool
		want    string
	}{
		{"2", true, "open no fleet member is up [stream:]"},
		{"3", false, "open no fleet member is up [stream:]"},
		{"3", true, "open a card reached its bound [s1-1]"},
	} {
		w := sprintOf(t, "m1", "m2")
		w.add(t, "s1", 1)
		w.deal(t, "s1-1")
		w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "hold", Member: "m1", Who: coordinator}))
		w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "hold", Member: "m2", Who: coordinator}))
		// no member up: the card is withdrawn; make it the one that has been
		// redealt that many times, and, ended, the one whose take just ended
		wc := w.s.Fleet.Card("s1-1.w1")
		wc.Fields["redeals"] = tc.redeals
		if tc.ended {
			wc.Fields[sprint.FieldTakeEnded] = "2026-09-30T00:00:00Z"
		}
		w.s.Fleet.Put(wc)
		got := refmodel.DealMoves(w.snapshot(nil), later(0))
		expect(t, got, tc.want)
		if tc.ended && tc.redeals == "3" {
			// a take that ended with no record (its member down) is no cause a wait changes
			assert.Equal(t, []string{"rework with a fix", "drop"}, got[0].Decisions, "the decisions offered at the bound: %q", got[0].Decisions)
		}
	}
}

func TestLevelMovesTheNewestFromTheLongestRoundTheFleet(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 2)
	w.deal(t, "s1-1")
	w.deal(t, "s1-2")
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "release", Member: "m2", Who: coordinator}))
	ctl := w.s.MemberCtl("m2")
	ctl.Fields["status"] = sprint.Up // up without the step that would level the queues
	w.s.Fleet.Put(ctl)
	got := refmodel.LevelMoves(w.snapshot(w.fresh()), later(0))
	expect(t, got, "prop fleet deal_index=2", "move fleet s1-2.w1 m1:ready>m2:ready")
	assert.Contains(t, got[1].Set, "gen=2", "a card dealt again is the next generation, of its new member: %v", got[1].Set)
	assert.Contains(t, got[1].Set, "member=m2", "a card dealt again is the next generation, of its new member: %v", got[1].Set)
}

func TestAskAsksTwoReadersOfAPrimaryInReview(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 2)
	w.deal(t, "s1-1")
	w.take(t, "s1-1")
	w.finish(t, "s1-1", false)
	w.deal(t, "s1-2")
	w.take(t, "s1-2")
	w.finish(t, "s1-2", true) // failed work is not read
	got := refmodel.AskMoves(w.snapshot(w.fresh()), later(0))
	expect(t, got,
		"set work s1-1 asked=reader-a,reader-b",
		"prop readers ask_index=2",
		"create readers s1-1.r1.reader-a >reader-a:asked",
		"create readers s1-1.r1.reader-b >reader-b:asked",
		"prop readers stream_index_ask=1")
}

func TestAcceptMovesAPrimaryWithTwoOkReadsToMergingAndTellsTheCoordinatorOnce(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 2)
	for _, id := range []string{"s1-1", "s1-2"} {
		w.deal(t, id)
		w.take(t, id)
		w.finish(t, id, false)
		w.ask(t, id)
	}
	w.report(t, "s1-1", "ok")
	w.report(t, "s1-2", "broken") // a broken read is not two ok reads
	got := refmodel.AcceptMoves(w.snapshot(w.fresh()), later(0))
	require.NotEmpty(t, got, "a primary in review with two ok reads is accepted by the tick")
	for _, m := range got {
		if m.Card == "s1-2" {
			assert.Failf(t, "assertion failed", "a primary with a broken read is accepted:%s", show(got))
		}
	}
	notices := 0
	for _, m := range got {
		if m.Kind == refmodel.KindNotice && m.Type == sprint.NReadyToMerge {
			notices++
		}
	}
	if notices != 1 {
		assert.Failf(t, "assertion failed", "the coordinator is told %d times that a stream is ready to merge, want once:%s", notices, show(got))
	}
}

func TestAskTellsOnceWhenFewerThanTwoReadersAreFree(t *testing.T) {
	t.Parallel()
	w := newWorld("reader-a")
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "up", Member: "m1", Who: coordinator}))
	w.add(t, "s1", 1)
	w.deal(t, "s1-1")
	w.take(t, "s1-1")
	w.finish(t, "s1-1", false)
	got := refmodel.AskMoves(w.snapshot(w.fresh()), later(0))
	expect(t, got, "open cannot ask [s1-1]")
}

func TestCheckRaisesAJudgmentForABrokenRule(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.drive(t, "s1-1", sprint.Working)
	wc := w.s.Fleet.Card("s1-1.w1")
	wc.Row, wc.Col = "", "" // the live work card of a working primary is gone
	w.s.Fleet.Put(wc)
	got := refmodel.CheckMoves(w.snapshot(w.fresh()), later(0))
	if len(got) == 0 || got[0].Kind != refmodel.KindOpen || got[0].Type != sprint.NInvariant || !slices.Contains(got[0].Subjects, "s1-1") {
		assert.Failf(t, "assertion failed", "a broken rule is one judgment on its card:%s", show(got))
	}
}

func TestCheckIsQuietWhenEveryRuleHolds(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 2)
	w.drive(t, "s1-1", sprint.Working)
	expect(t, refmodel.CheckMoves(w.snapshot(w.fresh()), later(0)))
}

// A work card's deadline runs from its take (nova-tools#5096 item 22): dealt and
// waiting in its member's ready queue it is the machine's queue, not the card's
// fault, so it is late only past the dealt bound, three take deadlines from its
// deal, and the judgment says where it waits and offers the fleet's answers.
func TestDeadlinesJudgeAWorkCardDealtAndNeverTakenOnlyPastTheDealtBound(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.deal(t, "s1-1")
	snap := w.snapshot(nil)
	expect(t, refmodel.DeadlineMoves(snap, later(16*time.Minute)))
	expect(t, refmodel.DeadlineMoves(snap, later(sprint.DealtMaxDefault)))
	require.Equal(t, 6*time.Hour, sprint.DealtMaxDefault, "the dealt bound is three take deadlines")
	got := refmodel.DeadlineMoves(snap, later(sprint.DealtMaxDefault+time.Second))
	expect(t, got, "open a work card is past its deadline [s1-1]")
	assert.Equal(t, "s1-1.w1", got[0].Card, "the judgment names the card and what is late: %+v", got[0])
	assert.Contains(t, got[0].Words, "dealt, never taken, at m1:ready", "the judgment says where the card waits: %+v", got[0])
	want := []string{"fleet level", "fleet down m1", "wait"}
	assert.Equal(t, want, got[0].Decisions, "the decisions offered: %q, want %q", got[0].Decisions, want)
}

func TestDeadlinesCountRunningTimeOnly(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.drive(t, "s1-1", sprint.Working)
	snap := w.snapshot(nil)
	snap.Stopped = []sprint.Span{{From: t0.Add(time.Minute), To: t0.Add(20 * time.Minute)}}
	// 20 minutes of the clock, 19 of them STOPPED: one minute has run
	expect(t, refmodel.DeadlineMoves(snap, later(20*time.Minute)))
	// two hours of running time are 2h19m of the clock: not past the deadline, and a second more is
	expect(t, refmodel.DeadlineMoves(snap, later(sprint.DeadlineUnfinished+19*time.Minute)))
	expect(t, refmodel.DeadlineMoves(snap, later(sprint.DeadlineUnfinished+19*time.Minute+time.Second)),
		"open a work card is past its deadline [s1-1]")
}

func TestDeadlinesJudgeAReadCardNotBegun(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.drive(t, "s1-1", sprint.Review)
	w.ask(t, "s1-1")
	snap := w.snapshot(nil)
	expect(t, refmodel.DeadlineMoves(snap, later(30*time.Minute)))
	got := refmodel.DeadlineMoves(snap, later(30*time.Minute+time.Second))
	expect(t, got,
		"open a read card is past its deadline [s1-1]",
		"open a read card is past its deadline [s1-1]")
	want := []string{"ask --another", "wait", "drop"}
	assert.Equal(t, want, got[0].Decisions, "the decisions offered: %q, want %q", got[0].Decisions, want)
}

func TestDeadlinesJudgeAStreamWithNoMergeStep(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.drive(t, "s1-1", sprint.Merging)
	snap := w.snapshot(nil)
	expect(t, refmodel.DeadlineMoves(snap, later(30*time.Minute)))
	got := refmodel.DeadlineMoves(snap, later(30*time.Minute+time.Second))
	expect(t, got, "open a stream has had no merge step past its deadline [stream:s1]")
	want := []string{"merge --stream s1", "look", "wait"}
	assert.Equal(t, want, got[0].Decisions, "the decisions offered: %q, want %q", got[0].Decisions, want)
}

func TestOverdueMarksAJudgmentOncePastTenMinutes(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 2)
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "hold", Member: "m1", Who: coordinator}))
	// the judgment the tick raises when no member is up, written at t0
	w.must(t, sprint.Plan{Notes: func() []sprint.Note {
		p, _ := sprint.TickDeal(w.s, sprint.TickReq{})
		return p.Notes
	}()})
	require.Len(t, w.s.Open, 1, "the fixture: %d open", len(w.s.Open))
	snap := w.snapshot(nil)
	expect(t, refmodel.OverdueMoves(snap, later(10*time.Minute)))
	got := refmodel.OverdueMoves(snap, later(10*time.Minute+time.Second))
	expect(t, got,
		"hold a judgment notification has waited past its deadline [stream:]",
		"notice a judgment notification has waited past its deadline []")
}

func TestRemindPushesEachPersonDueAndRaisesAFailingRoute(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	snap := w.snapshot(w.fresh())
	snap.Goals = sprint.Goals{People: []sprint.Goal{
		{Name: "ann", Text: "keep going", Route: "file:/x/ann", Last: t0.Add(-time.Minute), Count: 2},         // pushed a minute ago: not due
		{Name: "bob", Text: "keep going", Route: "file:/x/bob", Last: t0.Add(-5 * time.Minute), Count: 4},     // due at five minutes
		{Name: "cyd", Text: "keep going", Route: "file:/x/cyd", Last: t0.Add(-time.Minute), Fail: "no route"}, // its route fails
		{Name: "dee", Text: "keep going", Route: "file:/x/dee", Last: t0.Add(-5*time.Minute + time.Second)},   // a second short of five minutes: not due
	}}
	got := refmodel.RemindMoves(snap, later(0))
	expect(t, got,
		"open a reminder could not be delivered [stream:]",
		"push bob")
	assert.Equal(t, "REMINDER 5 to bob over file:/x/bob", got[1].Words, "the push: %+v", got[1])
	assert.Equal(t, "file:/x/bob", got[1].To, "the push: %+v", got[1])
	want := []string{"goal set cyd --to <route>", "goal drop cyd", "ack"}
	assert.Equal(t, want, got[0].Decisions, "the decisions offered of the failing route: %q, want %q", got[0].Decisions, want)
	// once the judgment is written the record says so, and the tick has nothing more to write
	snap.Goals.Noted = snap.Goals.Failing()
	expect(t, refmodel.RemindMoves(snap, later(0)), "push bob")
}

// The time the machine was STOPPED does not count toward a reminder: five
// minutes of running time, not of the clock. The snapshot's Since stays before
// the span, as it does in the deadline fixtures: the reference is asked what
// running time is, and not what a machine that started again would say.
func TestRemindCountsRunningTimeOnly(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	snap := w.snapshot(w.fresh())
	snap.Goals = sprint.Goals{People: []sprint.Goal{{Name: "ann", Text: "keep going", Route: "file:/x/ann", Last: t0.Add(-6 * time.Minute)}}}
	snap.Stopped = []sprint.Span{{From: t0.Add(-330 * time.Second), To: t0.Add(-30 * time.Second)}} // five of the six minutes
	expect(t, refmodel.RemindMoves(snap, later(0)))                                                 // one minute has run
	expect(t, refmodel.RemindMoves(snap, later(4*time.Minute-time.Second)))                         // four minutes and fifty-nine seconds
	expect(t, refmodel.RemindMoves(snap, later(4*time.Minute)), "push ann")                         // five
}

func TestAStoppedMachineMovesNothing(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 3)
	snap := w.snapshot(w.fresh())
	snap.Untold = []string{"x1"}
	require.NotEmpty(t, refmodel.Decide(snap, later(0)), "the fixture: a running machine has something to do")
	snap.Running = false
	for _, d := range refmodel.Duties {
		if got := d.Moves(snap, later(time.Hour)); len(got) > 0 {
			assert.Failf(t, "assertion failed", "%s moves while the machine is STOPPED:%s", d.Name, show(got))
		}
	}
	if got := refmodel.Decide(snap, later(time.Hour)); len(got) > 0 {
		assert.Failf(t, "assertion failed", "Decide moves while the machine is STOPPED:%s", show(got))
	}
}

func TestDecideIsEveryDutyInTheTicksOrder(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1", "m2")
	w.add(t, "s1", 3)
	snap := w.snapshot(w.fresh())
	snap.Untold = []string{"x1"}
	now := later(0)
	got := refmodel.Decide(snap, now)
	var want []refmodel.Move
	seen := map[string]bool{}
	for _, d := range refmodel.Duties {
		ms := d.Moves(snap, now)
		want = append(want, ms...)
		for _, m := range ms {
			assert.Equal(t, d.Name, m.Duty, "%s made a move of duty %s", d.Name, m.Duty)
			seen[d.Name] = true
		}
	}
	require.True(t, seen[refmodel.DutyStrangers], "the fixture: strangers and deal have moves: %v", seen)
	require.True(t, seen[refmodel.DutyDeal], "the fixture: strangers and deal have moves: %v", seen)
	ok, diff := refmodel.Equal(got, want)
	assert.True(t, ok, "Decide is not its duties: %s", diff)
	if !slices.IsSortedFunc(got, func(a, b refmodel.Move) int { return rank(a) - rank(b) }) {
		assert.Failf(t, "assertion failed", "the moves are not in the ticks order by duty:%s", show(got))
	}
}

// rank is the position of a move's duty in the tick's order.
func rank(m refmodel.Move) int {
	for i, d := range refmodel.Duties {
		if d.Name == m.Duty {
			return i
		}
	}
	return len(refmodel.Duties)
}

func TestDecideReadsTheTimeItIsGivenAndNotTheSnapshots(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.deal(t, "s1-1")
	snap := w.snapshot(nil)
	snap.Beats = nil                   // no beats read: the presence duty does nothing
	snap.Tables.Now = later(time.Hour) // the snapshot's own clock is not read
	if got := refmodel.Decide(snap, later(time.Minute)); len(got) != 0 {
		assert.Failf(t, "assertion failed", "a card dealt a minute ago is not late:%s", show(got))
	}
	snap.Tables.Now = t0
	got := refmodel.Decide(snap, later(sprint.DealtMaxDefault+time.Hour))
	assert.NotEmpty(t, got, "a card dealt and never taken an hour past the dealt bound is late")
}

func TestEveryPartOfTheTickIsADutyAndEveryDutyIsNamedInOrder(t *testing.T) {
	t.Parallel()
	names := map[string]bool{}
	for _, d := range refmodel.Duties {
		names[d.Name] = true
	}
	for _, p := range sprint.TickParts {
		assert.True(t, names[p.Name], "the tick's part %s is no duty: Decide would leave it out", p.Name)
	}
	want := []string{refmodel.DutyLevel, refmodel.DutyLevelReads, refmodel.DutyResolve, refmodel.DutyDeal, refmodel.DutyAccept, refmodel.DutyAsk, refmodel.DutyResume,
		refmodel.DutyStrangers, refmodel.DutyPresence, refmodel.DutyCheck, refmodel.DutyDeadlines, refmodel.DutyOverdue, refmodel.DutyDone, refmodel.DutyRemind}
	var got []string
	for _, d := range refmodel.Duties {
		got = append(got, d.Name)
	}
	assert.Equal(t, want, got, "the duties are %v, want %v", got, want)
	// the parts run in the order of their duties
	var parts []string
	for _, p := range sprint.TickParts {
		parts = append(parts, p.Name)
	}
	var inDuties []string
	for _, n := range got {
		if slices.Contains(parts, n) {
			inDuties = append(inDuties, n)
		}
	}
	assert.Equal(t, inDuties, parts, "the tick's parts run %v; the duties have them as %v", parts, inDuties)
}

func TestASnapshotThatLacksATableIsRefusedInWords(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	for name, cut := range map[string]func(*refmodel.Snapshot){
		"no tables":  func(s *refmodel.Snapshot) { s.Tables = nil },
		"no readers": func(s *refmodel.Snapshot) { s.Tables.Readers = nil },
	} {
		snap := w.snapshot(w.fresh())
		snap = snap.Clone()
		cut(&snap)
		func() {
			defer func() {
				r := recover()
				if assert.True(t, r != nil, "%s: deciding did not refuse in words: %v", name, r) {
					assert.Contains(t, fmt.Sprint(r), "refmodel:", "%s: deciding did not refuse in words: %v", name, r)
				}
			}()
			refmodel.Decide(snap, later(0))
		}()
	}
	snap := w.snapshot(w.fresh())
	snap.Tables, snap.Running = nil, false
	if got := refmodel.Decide(snap, later(0)); len(got) != 0 {
		assert.Failf(t, "assertion failed", "a STOPPED machine decides nothing, whatever it holds:%s", show(got))
	}
}
