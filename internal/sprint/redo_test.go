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
