package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRedoReturnsReworksAndResumesInOneVerb verifies that redo moves the card and resumes
// the stream in one step with one history line; refused when the card is not in a conflict.
// (docs/SPEC-SPRINT.md section 11, redo).
func TestRedoReturnsReworksAndResumesInOneVerb(t *testing.T) {
	t.Parallel()
	w := stoppedForConflict(t)

	// Refused when card is not in a conflict.
	p := Redo(w.s, RedoReq{Sel: Sel{IDs: []string{"s1-1"}}})
	require.Len(t, p.Refused, 1, "s1-1 is already merged, not in a conflict")
	require.Equal(t, "s1-1", p.Refused[0].Key)
	require.Equal(t, "not in a conflict", p.Refused[0].Why)
	require.Empty(t, p.Units)

	p = Redo(w.s, RedoReq{Sel: Sel{IDs: []string{"s1-3"}}})
	require.Len(t, p.Refused, 1, "s1-3 is queued in merge, not in a conflict")
	require.Equal(t, "s1-3", p.Refused[0].Key)
	require.Equal(t, "not in a conflict", p.Refused[0].Why)
	require.Empty(t, p.Units)

	// Preconditions for the conflicted card s1-2.
	require.Equal(t, Merging, w.state("s1-2"))
	require.Equal(t, Stuck, w.s.Merge.Placed("s1-2").Col)
	require.Equal(t, StreamStopped, w.s.StreamCtl("s1").F("state"))
	require.Equal(t, "conflict", w.s.StreamCtl("s1").F("cause"))
	require.Equal(t, "s1-2", w.s.StreamCtl("s1").F("card"))
	require.Len(t, w.openOn(StreamSubject("s1")), 1, "open conflict judgment on s1")

	// Redo moves s1-2 and resumes stream s1 in one step.
	p = w.must(Redo(w.s, RedoReq{Sel: Sel{IDs: []string{"s1-2"}}, Who: "coordinator"}))

	// Postconditions:
	// Primary s1-2 moves to working (attempt 2).
	require.Equal(t, Working, w.state("s1-2"), "s1-2 state")
	c := w.s.Work.Card("s1-2")
	require.Equal(t, "2", c.F("attempt"))
	require.Equal(t, 1, c.Int("returns"))
	require.Equal(t, 1, c.Int("reworks"))
	require.Equal(t, "conflict", c.F("return_reason"))
	require.Equal(t, "1", c.F(FieldReturnedAttempt))
	require.Equal(t, RedoFixText, c.F("fix"))

	// Work card created in Fleet ready queue.
	wc := w.s.Fleet.Card("s1-2.w2")
	require.NotNil(t, wc)
	require.Equal(t, "ready", wc.Col)
	require.Equal(t, RedoFixText, wc.F("fix"))

	// Merge card s1-2 is Returned.
	require.Equal(t, Returned, w.s.Merge.Placed("s1-2").Col)

	// Stream s1 is resumed to merging.
	ctl := w.s.StreamCtl("s1")
	require.Equal(t, StreamMerging, ctl.F("state"))
	require.Empty(t, ctl.F("cause"))
	require.Empty(t, ctl.F("card"))

	// Conflict judgment on s1 is closed.
	require.Empty(t, w.openOn(StreamSubject("s1")), "conflict judgment closed")

	// Verify plan units and history line description.
	require.Len(t, p.Units, 2, "card unit and stream unit")
	require.Contains(t, p.Units[0].Moved, "s1-2 work merging -> working (rework)")
	require.Contains(t, p.Units[0].Moved, "off merge stuck")
	require.Contains(t, p.Units[1].Moved, "stream s1 stopped -> merging")
}

func TestRedoByStream(t *testing.T) {
	t.Parallel()
	w := stoppedForConflict(t)
	p := w.must(Redo(w.s, RedoReq{Sel: Sel{Stream: "s1"}, Who: "coordinator"}))
	require.Equal(t, Working, w.state("s1-2"))
	require.Equal(t, StreamMerging, w.s.StreamCtl("s1").F("state"))
	require.Len(t, p.Units, 2)
}

func TestRedoTimelineHasOneHistoryLineForPrimary(t *testing.T) {
	t.Parallel()
	workLine := Line{
		Table: Work, Card: "s1-2", Primary: "s1-2", Stream: "s1", Op: "op1",
		From: "s1:" + string(Merging), To: "s1:" + string(Working), Verb: "redo", Actor: "coordinator",
		Set: map[string]string{"attempt": "2", "fix": RedoFixText},
	}
	mergeLine := Line{
		Table: Merge, Card: "s1-2", Primary: "s1-2", Stream: "s1", Op: "op1",
		From: "s1:" + string(Stuck), To: "s1:" + string(Returned), Verb: "redo", Actor: "coordinator",
	}
	fleetLine := Line{
		Table: Fleet, Card: "s1-2.w2", Primary: "s1-2", Stream: "s1", Op: "op1",
		From: "", To: "m1:" + string(Ready), Verb: "redo", Actor: "coordinator",
	}
	ctlLine := Line{
		Table: Merge, Card: CtlID("s1"), Stream: "s1", Op: "op1",
		From: "s1:" + string(StreamStopped), To: "s1:" + string(StreamMerging), Verb: "redo", Actor: "coordinator",
		Set: map[string]string{"state": StreamMerging},
	}
	var about []Line
	for _, l := range []Line{workLine, mergeLine, fleetLine, ctlLine} {
		if l.About("s1-2") {
			about = append(about, l)
		}
	}
	tl := Timeline(about, "s1-2")
	require.Len(t, tl, 2, "work line and fleet line; merge returned line suppressed")
	require.Equal(t, Work, tl[0].Table)
	require.Equal(t, Fleet, tl[1].Table)
	rendered := renderPrimary(tl[0], string(Merging), string(Working), true, "by coordinator")
	require.Equal(t, "s1-2 reworked by coordinator: attempt 2", rendered)
}

func TestRedoWhenNoMemberIsUp(t *testing.T) {
	t.Parallel()
	w := stoppedForConflict(t)
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
	p := w.must(Redo(w.s, RedoReq{Sel: Sel{IDs: []string{"s1-2"}}, Who: "coordinator"}))
	require.Equal(t, Ready, w.state("s1-2"), "s1-2 state ready")
	require.Contains(t, p.Units[0].Moved, "s1-2 merging -> ready (rework; no fleet member is up: start delegates it)")
	require.Equal(t, StreamMerging, w.s.StreamCtl("s1").F("state"))
}

// TestADroppedCardIsRestoredByRedo pins the restore half of redo
// (docs/SPEC-SPRINT.md section 11, redo): a stream of three cards, one needing
// another, is dropped, and redo --stream restores all three to waiting at
// their old scores with the need edge back; a card dropped as replaced by a
// twin is refused naming the twin.
func TestADroppedCardIsRestoredByRedo(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b", "reader-c")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: proBrief}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-2"}, Needs: []string{"s1-1"}, Brief: proBrief}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-3"}, Brief: proBrief}))
	score := w.s.Work.Card("s1-2").Score
	w.must(Drop(w.s, DropReq{Sel: Sel{Stream: "s1"}, Reason: "dropped by mistake", Cascade: true}))
	require.Equal(t, "", w.state("s1-1"), "dropped: s1-1 off the table")
	require.Equal(t, "dropped", w.s.Work.Card("s1-2").F("outcome"))

	p := w.must(Redo(w.s, RedoReq{Sel: Sel{Stream: "s1"}, Reason: "re-admitted", Who: "coordinator"}))
	require.Equal(t, Waiting, w.state("s1-1"))
	require.Equal(t, Waiting, w.state("s1-2"))
	require.Equal(t, Waiting, w.state("s1-3"))
	require.Equal(t, score, w.s.Work.Card("s1-2").Score, "the old score is kept")
	require.Equal(t, "s1-1", w.s.Work.Card("s1-2").F("needs"), "the need edge is back")
	require.Empty(t, w.s.Work.Card("s1-1").F("outcome"), "the drop mark is cleared")
	require.Len(t, p.Units, 3, "one MOVED line per card")

	// A card dropped as replaced by a twin is refused naming the twin.
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-3"}}, Reason: "replaced by s1-9"}))
	p = Redo(w.s, RedoReq{Sel: Sel{Stream: "s1"}, Reason: "re-admitted", Who: "coordinator"})
	require.Len(t, p.Refused, 1, "the twinned card is refused: %+v", p.Refused)
	require.Equal(t, "s1-3", p.Refused[0].Key)
	require.Contains(t, p.Refused[0].Why, "s1-9", "the refusal names the twin")
	require.Equal(t, "", w.state("s1-3"), "the twinned card stays dropped")
}

func TestRedoRestoreRefusesWithoutAReason(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b", "reader-c")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: proBrief}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	p := Redo(w.s, RedoReq{Sel: Sel{IDs: []string{"s1-1"}}, Who: "coordinator"})
	require.Len(t, p.Refused, 1, "restore without a reason: %+v", p.Refused)
	require.Equal(t, "s1-1", p.Refused[0].Key)
	require.Contains(t, p.Refused[0].Why, "--reason")
	require.Empty(t, p.Units)
}

// TestRedoByStreamRefusesWithoutAReason pins the stream selection's restore
// refusal (docs/SPEC-SPRINT.md section 11, redo): a redo --stream on a stream
// holding a dropped primary without --reason is refused, never a silent no-op.
func TestRedoByStreamRefusesWithoutAReason(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b", "reader-c")
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: proBrief}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	p := Redo(w.s, RedoReq{Sel: Sel{Stream: "s1"}, Who: "coordinator"})
	require.Len(t, p.Refused, 1, "restore by stream without a reason: %+v", p.Refused)
	require.Equal(t, "s1-1", p.Refused[0].Key)
	require.Contains(t, p.Refused[0].Why, "--reason")
	require.Empty(t, p.Units)
	require.Equal(t, "", w.state("s1-1"), "the card stays dropped")
}

// TestRedoRestoreUndoesTheDropBookkeeping pins the restore's reverse of the
// drop's bookkeeping (docs/SPEC-SPRINT.md section 11, redo): a restored card's
// need edge brings its weight back on the card it waits on, and the stream's
// dropped counter is brought back down by the count restored.
func TestRedoRestoreUndoesTheDropBookkeeping(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b", "reader-c")
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: proBrief}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-2"}, Needs: []string{"s1-1"}, Brief: proBrief}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-3"}, Brief: proBrief}))
	require.Equal(t, "1", w.s.Work.Card("s1-1").F("behind"), "s1-2 waits on s1-1")

	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "obsolete"}))
	require.Empty(t, w.s.Work.Card("s1-1").F("behind"), "the weight is recomputed away by the drop")
	require.Equal(t, "1", w.s.StreamCtl("s1").F("dropped"), "the drop counts one on the stream")

	w.must(Redo(w.s, RedoReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "re-admitted", Who: "coordinator"}))
	require.Equal(t, "1", w.s.Work.Card("s1-1").F("behind"), "the need's weight is back")
	require.Equal(t, "0", w.s.StreamCtl("s1").F("dropped"), "the dropped counter is undone")
}
