package refmodel_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

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
// order.
func expect(t *testing.T, got []refmodel.Move, want ...string) {
	t.Helper()
	if !slices.Equal(briefs(got), want) {
		t.Errorf("moves:\n got %q\nwant %q\nfull:%s", briefs(got), want, show(got))
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
	if got[0].Words != "an unknown machine is beating: x1; add it with nova-sprint fleet up x1" || !strings.Contains(got[1].Words, "x2") {
		t.Errorf("the words: %q, %q", got[0].Words, got[1].Words)
	}
	if !slices.Equal(got[0].Attrs, []string{"who=machine"}) {
		t.Errorf("the notice is not the machine's: %v", got[0].Attrs)
	}
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
		"move fleet s1-1.w1 m1:working>m2:ready",
		"move fleet s1-3.w1 m1:ready>m2:ready",
		"notice fleet member down []")
	byCard := map[string]refmodel.Move{}
	for _, m := range got {
		byCard[m.Card] = m
	}
	if s := byCard["s1-1.w1"].Set; !slices.Contains(s, "gen=2") || !slices.Contains(s, "member=m2") || !slices.Contains(s, "redeals=1") {
		t.Errorf("a card taken and redealt is a new generation of the new member, and counts one redeal: %v", s)
	}
	if !slices.Contains(byCard["s1-1.w1"].Unset, "taken") {
		t.Errorf("a redealt card is no longer taken: %v", byCard["s1-1.w1"].Unset)
	}
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
	if w.state("s1-2") != sprint.Waiting || w.state("s1-3") != sprint.Waiting {
		t.Fatalf("the fixture: s1-2 is %s, s1-3 is %s", w.state("s1-2"), w.state("s1-3"))
	}
	got := refmodel.ResolveMoves(w.snapshot(w.fresh()), later(0))
	expect(t, got, "move work s1-2 s1:waiting>s1:ready")
	if got[0].Words != "s1-2 waiting -> ready" {
		t.Errorf("the words: %q", got[0].Words)
	}
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
	if !slices.Equal(got[0].Attrs, []string{"count=1", "needs=s1-1", "who=machine"}) {
		t.Errorf("the blocked judgment names its need: %v", got[0].Attrs)
	}
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
	if ctl := w.s.StreamCtl("s1"); ctl.F("state") != sprint.StreamStopped || ctl.F("cause") != "cross" {
		t.Fatalf("the fixture: s1 is %s, cause %s", ctl.F("state"), ctl.F("cause"))
	}
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
	// two members, room for two each: the four oldest, alternating to the shortest queue
	expect(t, got,
		"move work s1-1 s1:ready>s1:working",
		"move work s1-2 s1:ready>s1:working",
		"move work s1-3 s1:ready>s1:working",
		"move work s1-4 s1:ready>s1:working",
		"create fleet s1-1.w1 >m1:ready",
		"create fleet s1-2.w1 >m2:ready",
		"create fleet s1-3.w1 >m1:ready",
		"create fleet s1-4.w1 >m2:ready")
}

func TestDealTellsOnceWhenNoMemberIsUp(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 2)
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "hold", Member: "m1", Who: coordinator}))
	got := refmodel.DealMoves(w.snapshot(nil), later(0))
	expect(t, got, "open no fleet member is up [stream:]")
	if got[0].Stream != "" || !slices.Contains(got[0].Attrs, "stream_level=yes") || !strings.HasPrefix(got[0].Words, "2 primaries wait to be dealt") {
		t.Errorf("the judgment is about the sprint, and counts what waits: %+v", got[0])
	}
}

func TestDealStopsAtTheRedealBound(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1", "m2")
	w.add(t, "s1", 1)
	w.deal(t, "s1-1")
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "hold", Member: "m1", Who: coordinator}))
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "hold", Member: "m2", Who: coordinator}))
	// no member up: the card is withdrawn; make it the one that has been redealt to the bound
	wc := w.s.Fleet.Card("s1-1.w1")
	wc.Fields["redeals"] = fmt.Sprint(sprint.MaxRedeals)
	w.s.Fleet.Put(wc)
	got := refmodel.DealMoves(w.snapshot(nil), later(0))
	expect(t, got, "open a card reached its bound [s1-1]")
}

func TestLevelMovesTheNewestFromTheLongestToTheShortest(t *testing.T) {
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
	expect(t, got, "move fleet s1-2.w1 m1:ready>m2:ready")
	if !slices.Contains(got[0].Set, "gen=2") || !slices.Contains(got[0].Set, "member=m2") {
		t.Errorf("a card dealt again is the next generation, of its new member: %v", got[0].Set)
	}
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
		"create readers s1-1.r1.reader-a >reader-a:asked",
		"create readers s1-1.r1.reader-b >reader-b:asked")
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
	expect(t, got, "open cannot ask: fewer than two different readers are free [s1-1]")
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
		t.Errorf("a broken rule is one judgment on its card:%s", show(got))
	}
}

func TestCheckIsQuietWhenEveryRuleHolds(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 2)
	w.drive(t, "s1-1", sprint.Working)
	expect(t, refmodel.CheckMoves(w.snapshot(w.fresh()), later(0)))
}

func TestDeadlinesJudgeAWorkCardNotTakenPastFifteenMinutes(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.deal(t, "s1-1")
	snap := w.snapshot(nil)
	expect(t, refmodel.DeadlineMoves(snap, later(sprint.DeadlineUntaken)))
	got := refmodel.DeadlineMoves(snap, later(sprint.DeadlineUntaken+time.Second))
	expect(t, got, "open a work card is past its deadline [s1-1]")
	if got[0].Card != "s1-1.w1" || !strings.Contains(got[0].Words, "not taken") {
		t.Errorf("the judgment names the card and what is late: %+v", got[0])
	}
}

func TestDeadlinesCountRunningTimeOnly(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.deal(t, "s1-1")
	snap := w.snapshot(nil)
	snap.Stopped = []sprint.Span{{From: t0.Add(time.Minute), To: t0.Add(20 * time.Minute)}}
	// 20 minutes of the clock, 19 of them STOPPED: one minute has run
	expect(t, refmodel.DeadlineMoves(snap, later(20*time.Minute)))
	expect(t, refmodel.DeadlineMoves(snap, later(20*time.Minute+sprint.DeadlineUntaken)),
		"open a work card is past its deadline [s1-1]")
}

func TestDeadlinesJudgeAReadCardNotBegun(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.drive(t, "s1-1", sprint.Review)
	w.ask(t, "s1-1")
	got := refmodel.DeadlineMoves(w.snapshot(nil), later(sprint.DeadlineUnbegun+time.Second))
	expect(t, got,
		"open a read card is past its deadline [s1-1]",
		"open a read card is past its deadline [s1-1]")
}

func TestDeadlinesJudgeAStreamWithNoMergeStep(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.drive(t, "s1-1", sprint.Merging)
	got := refmodel.DeadlineMoves(w.snapshot(nil), later(sprint.DeadlineMergeIdle+time.Second))
	expect(t, got, "open a stream has had no merge step past its deadline [stream:s1]")
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
	if len(w.s.Open) != 1 {
		t.Fatalf("the fixture: %d open", len(w.s.Open))
	}
	snap := w.snapshot(nil)
	expect(t, refmodel.OverdueMoves(snap, later(sprint.DeadlineJudgment)))
	got := refmodel.OverdueMoves(snap, later(sprint.DeadlineJudgment+time.Second))
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
		{Name: "bob", Text: "keep going", Route: "file:/x/bob", Last: t0.Add(-sprint.RemindEvery), Count: 4},  // due
		{Name: "cyd", Text: "keep going", Route: "file:/x/cyd", Last: t0.Add(-time.Minute), Fail: "no route"}, // its route fails
	}}
	got := refmodel.RemindMoves(snap, later(0))
	expect(t, got,
		"open a reminder could not be delivered [stream:]",
		"push bob")
	if got[1].Words != "REMINDER 5 to bob over file:/x/bob" || got[1].To != "file:/x/bob" {
		t.Errorf("the push: %+v", got[1])
	}
	// once the judgment is written the record says so, and the tick has nothing more to write
	snap.Goals.Noted = snap.Goals.Failing()
	expect(t, refmodel.RemindMoves(snap, later(0)), "push bob")
}

func TestAStoppedMachineMovesNothing(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 3)
	snap := w.snapshot(w.fresh())
	snap.Untold = []string{"x1"}
	if len(refmodel.Decide(snap, later(0))) == 0 {
		t.Fatal("the fixture: a running machine has something to do")
	}
	snap.Running = false
	for _, d := range refmodel.Duties {
		if got := d.Moves(snap, later(time.Hour)); len(got) > 0 {
			t.Errorf("%s moves while the machine is STOPPED:%s", d.Name, show(got))
		}
	}
	if got := refmodel.Decide(snap, later(time.Hour)); len(got) > 0 {
		t.Errorf("Decide moves while the machine is STOPPED:%s", show(got))
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
			if m.Duty != d.Name {
				t.Errorf("%s made a move of duty %s", d.Name, m.Duty)
			}
			seen[d.Name] = true
		}
	}
	if !seen[refmodel.DutyStrangers] || !seen[refmodel.DutyDeal] {
		t.Fatalf("the fixture: strangers and deal have moves: %v", seen)
	}
	if ok, diff := refmodel.Equal(got, want); !ok {
		t.Errorf("Decide is not its duties: %s", diff)
	}
	if !slices.IsSortedFunc(got, func(a, b refmodel.Move) int { return rank(a) - rank(b) }) {
		t.Errorf("the moves are not in the ticks order by duty:%s", show(got))
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
		t.Errorf("a card dealt a minute ago is not late:%s", show(got))
	}
	snap.Tables.Now = t0
	if got := refmodel.Decide(snap, later(time.Hour)); len(got) == 0 {
		t.Error("a card dealt an hour ago is late")
	}
}

func TestEveryPartOfTheTickIsADutyAndEveryDutyIsNamedInOrder(t *testing.T) {
	t.Parallel()
	names := map[string]bool{}
	for _, d := range refmodel.Duties {
		names[d.Name] = true
	}
	for _, p := range sprint.TickParts {
		if !names[p.Name] {
			t.Errorf("the tick's part %s is no duty: Decide would leave it out", p.Name)
		}
	}
	want := []string{refmodel.DutyStrangers, refmodel.DutyPresence, refmodel.DutyResolve, refmodel.DutyResume, refmodel.DutyDeal,
		refmodel.DutyLevel, refmodel.DutyAsk, refmodel.DutyCheck, refmodel.DutyDeadlines, refmodel.DutyOverdue, refmodel.DutyRemind}
	var got []string
	for _, d := range refmodel.Duties {
		got = append(got, d.Name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the duties are %v, want %v", got, want)
	}
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
	if !slices.Equal(parts, inDuties) {
		t.Errorf("the tick's parts run %v; the duties have them as %v", parts, inDuties)
	}
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
				if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "refmodel:") {
					t.Errorf("%s: deciding did not refuse in words: %v", name, r)
				}
			}()
			refmodel.Decide(snap, later(0))
		}()
	}
	snap := w.snapshot(w.fresh())
	snap.Tables, snap.Running = nil, false
	if got := refmodel.Decide(snap, later(0)); len(got) != 0 {
		t.Errorf("a STOPPED machine decides nothing, whatever it holds:%s", show(got))
	}
}
