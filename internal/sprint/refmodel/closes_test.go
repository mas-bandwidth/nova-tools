package refmodel_test

import (
	"slices"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// A duty closes the judgment it raised when its cause is gone: the tick writes
// the close as it writes the judgment, once. Each fixture has the duty raise its
// judgment (the plan applied to the sprint, as the store applies it), takes the
// cause away, and expects the duty's close among its moves: the close of that
// judgment, on that subject, by the judgment's id.

// theClose is the one close move of the type among the moves, and fails the
// test when there is not exactly one. It says the close names the judgment the
// duty raised (id) and its subjects.
func theClose(t *testing.T, got []refmodel.Move, typ, id string, subjects ...string) refmodel.Move {
	t.Helper()
	var closes []refmodel.Move
	for _, m := range got {
		if m.Kind == refmodel.KindClose {
			closes = append(closes, m)
		}
	}
	if len(closes) != 1 {
		t.Fatalf("want one close, got %d:%s", len(closes), show(got))
	}
	c := closes[0]
	if c.Type != typ || c.Card != id || !slices.Equal(c.Subjects, subjects) {
		t.Errorf("the close is of %q %s on %v, want %q %s on %v", c.Type, c.Card, c.Subjects, typ, id, subjects)
	}
	return c
}

// openIDs is the ids of the judgments open now, in the order they were written.
func openIDs(w *world, typ string) []string {
	var ids []string
	for _, o := range w.s.Open {
		if o.Note.Type == typ && !slices.Contains(ids, o.Note.ID) {
			ids = append(ids, o.Note.ID)
		}
	}
	return ids
}

func TestDealClosesTheJudgmentForNoMemberWhenAMemberIsUp(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 2)
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "hold", Member: "m1", Who: coordinator}))
	w.applyPart(t, "deal", 0)
	ids := openIDs(w, sprint.NNoMember)
	if len(ids) != 1 {
		t.Fatalf("the fixture: %d judgments for no member", len(ids))
	}
	expect(t, refmodel.DealMoves(w.snapshot(nil), later(0))) // the judgment is open: nothing is raised again
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "up", Member: "m1", Who: coordinator}))
	got := refmodel.DealMoves(w.snapshot(nil), later(0))
	expect(t, got,
		"move work s1-1 s1:ready>s1:working",
		"move work s1-2 s1:ready>s1:working",
		"prop work stream_index=s1",
		"prop fleet deal_index=m1",
		"create fleet s1-1.w1 >m1:ready",
		"create fleet s1-2.w1 >m1:ready",
		"close no fleet member is up [stream:]")
	theClose(t, got, sprint.NNoMember, ids[0], "stream:")
}

func TestAskClosesTheJudgmentForTooFewReadersWhenAReaderIsAdded(t *testing.T) {
	t.Parallel()
	w := newWorld("reader-a")
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "up", Member: "m1", Who: coordinator}))
	w.add(t, "s1", 1)
	w.deal(t, "s1-1")
	w.take(t, "s1-1")
	w.finish(t, "s1-1", false)
	w.applyPart(t, "ask", 0)
	ids := openIDs(w, sprint.NCannotAsk)
	if len(ids) != 1 {
		t.Fatalf("the fixture: %d judgments that it cannot ask", len(ids))
	}
	w.s.Readers.SetRows(append(w.s.Readers.Rows(), "reader-b"))
	got := refmodel.AskMoves(w.snapshot(w.fresh()), later(0))
	// today's tick also raises "stranded in review" here: it plans the close of
	// the judgment on the read where the primary is still in review and not yet
	// asked, and the reference says what the tick does
	expect(t, got,
		"set work s1-1 asked=reader-a,reader-b",
		"prop work stream_index_ask=s1",
		"prop readers ask_index=reader-b",
		"create readers s1-1.r1.reader-a >reader-a:asked",
		"create readers s1-1.r1.reader-b >reader-b:asked",
		"open stranded in review [s1-1]",
		"close cannot ask [s1-1]")
	theClose(t, got, sprint.NCannotAsk, ids[0], "s1-1")
}

func TestDeadlinesCloseALatenessWhenTheCardIsTaken(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.deal(t, "s1-1")
	w.applyPart(t, "deadlines", 15*time.Minute+time.Second)
	ids := openIDs(w, sprint.NWorkLate)
	if len(ids) != 1 {
		t.Fatalf("the fixture: %d lateness judgments", len(ids))
	}
	// still late: the judgment stays, and the tick has nothing to write
	expect(t, refmodel.DeadlineMoves(w.snapshot(nil), later(16*time.Minute)))
	w.take(t, "s1-1")
	got := refmodel.DeadlineMoves(w.snapshot(nil), later(16*time.Minute))
	expect(t, got, "close a work card is past its deadline [s1-1]")
	theClose(t, got, sprint.NWorkLate, ids[0], "s1-1")
}

func TestCheckClosesAnInvariantWhenTheRuleHoldsAgain(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.drive(t, "s1-1", sprint.Working)
	wc := w.s.Fleet.Card("s1-1.w1")
	row, col := wc.Row, wc.Col
	wc.Row, wc.Col = "", "" // the live work card of a working primary is gone
	w.s.Fleet.Put(wc)
	w.applyPart(t, "check", 0)
	ids := openIDs(w, sprint.NInvariant)
	if len(ids) != 1 {
		t.Fatalf("the fixture: %d invariant judgments", len(ids))
	}
	// the primary is also stalled until a judgment holds it: the next check
	// closes that one, and then there is nothing more to say while it is broken
	w.applyPart(t, "check", 0)
	expect(t, refmodel.CheckMoves(w.snapshot(w.fresh()), later(0)))
	wc = w.s.Fleet.Card("s1-1.w1")
	wc.Row, wc.Col = row, col // it holds again
	w.s.Fleet.Put(wc)
	got := refmodel.CheckMoves(w.snapshot(w.fresh()), later(0))
	expect(t, got, "close an invariant is broken [s1-1]")
	theClose(t, got, sprint.NInvariant, ids[0], "s1-1")
}

func TestOverdueClosesTheHoldWhenItsJudgmentCloses(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 2)
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "hold", Member: "m1", Who: coordinator}))
	w.applyPart(t, "deal", 0) // the judgment for no member up, written at t0
	w.applyPart(t, "overdue", 10*time.Minute+time.Second)
	if len(w.s.Acked) != 1 {
		t.Fatalf("the fixture: %d holds", len(w.s.Acked))
	}
	hold := w.s.Acked[0]
	expect(t, refmodel.OverdueMoves(w.snapshot(nil), later(11*time.Minute))) // marked once: nothing more to write
	w.closeAll(slices.Clone(w.s.Open))                                       // the judgment closes: a member came up, say
	got := refmodel.OverdueMoves(w.snapshot(nil), later(11*time.Minute))
	expect(t, got, "close a judgment notification has waited past its deadline [stream:]")
	theClose(t, got, sprint.NOverdue, hold.Note.ID, "stream:")
}

func TestResolveClosesTheJudgmentForAMissingNeedWhenTheNeedIsOnTheTable(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.addOne(t, "s1", "s1-2", "s1-1")
	c := w.s.Work.Card("s1-2")
	c.Fields["needs"] = "s1-9" // a need that is not on the table
	w.s.Work.Put(c)
	w.applyPart(t, "resolve", 0)
	ids := openIDs(w, sprint.NMissingNeed)
	if len(ids) != 1 {
		t.Fatalf("the fixture: %d judgments for a missing need", len(ids))
	}
	w.addOne(t, "s1", "s1-9") // it is there now, and has not landed
	got := refmodel.ResolveMoves(w.snapshot(nil), later(0))
	expect(t, got, "close a primary is blocked on something missing [s1-2]")
	theClose(t, got, sprint.NMissingNeed, ids[0], "s1-2")
}

func TestRemindClosesTheJudgmentForAFailingRouteWhenItIsReached(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	failing := sprint.Goal{Name: "cyd", Text: "keep going", Route: "file:/x/cyd", Last: t0.Add(-time.Minute), Fail: "no route"}
	goals := sprint.Goals{People: []sprint.Goal{failing}}
	w.must(t, sprint.Applied(w.s, sprint.RemindNotes(w.s, goals, sprint.MachineActor))) // the judgment is written
	ids := openIDs(w, sprint.NRemindFailed)
	if len(ids) != 1 {
		t.Fatalf("the fixture: %d judgments for a failing route", len(ids))
	}
	snap := w.snapshot(w.fresh())
	reached := failing
	reached.Fail = "" // a delivery arrived
	snap.Goals = sprint.Goals{People: []sprint.Goal{reached}, Noted: goals.Failing()}
	got := refmodel.RemindMoves(snap, later(0))
	expect(t, got, "close a reminder could not be delivered [stream:]")
	theClose(t, got, sprint.NRemindFailed, ids[0], "stream:")
}
