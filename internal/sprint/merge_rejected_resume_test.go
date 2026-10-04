package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A stream stopped by a rejected push (a transient push refusal: the merge
// queue rejected the batch) resumes when the push succeeds: the landing
// report is that push, and the machine takes the stream on again by itself
// (docs/SPEC-SPRINT.md section 7, the merge step). While the push keeps
// failing the stop keeps its one judgment, and only one: a second rejected
// fact is refused, and no other cause (a red branch) is resumed by a landing.

// The landing report of a stream stopped by a rejected push resumes the
// stream and lands the batch: the push succeeded, so the machine takes the
// stream on again, the stop's cause goes, and the judgment the stop raised
// closes.
func TestAStreamStoppedByARejectedPushResumesWhenTheLandingReportsIt(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	accepted(w, "s1-1", "s1-2")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Rejected: true, Note: "the base moved twice", Who: "lander"}))
	require.Equal(t, StreamStopped, w.s.StreamCtl("s1").F("state"), "the fixture: %s", w.s.StreamCtl("s1").F("state"))
	require.Equal(t, "rejected", w.s.StreamCtl("s1").F("cause"), "the fixture: %s", w.s.StreamCtl("s1").F("cause"))
	require.Len(t, w.openOn(StreamSubject("s1")), 1, "the stop raised its judgment: %v", w.s.Open)
	w.clean("rejected")

	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1, Who: "lander"}))

	assert.Equal(t, StreamMerging, w.s.StreamCtl("s1").F("state"), "the stream resumed: %s", w.s.StreamCtl("s1").F("state"))
	assert.Equal(t, "", w.s.StreamCtl("s1").F("cause"), "the stop's cause survives the resume: %s", w.s.StreamCtl("s1").F("cause"))
	assert.Equal(t, Merged, w.s.Merge.Placed("s1-1").Col, "the first card did not land: %s", w.s.Merge.Placed("s1-1").Col)
	assert.Equal(t, Landed, w.state("s1-1"), "the first card did not land: %s", w.state("s1-1"))
	assert.Equal(t, Queued, w.s.Merge.Placed("s1-2").Col, "the second card left the queue: %s", w.s.Merge.Placed("s1-2").Col)
	assert.Empty(t, w.openOn(StreamSubject("s1")), "the stop's judgment stays open: %v", w.s.Open)
	assert.Len(t, w.notesOf(NBatchLanded), 1, "the landing is not reported")
	w.clean("resumed")
}

// A second rejected fact on the stopped stream is refused: the stop keeps its
// one judgment open while the push keeps failing, and nothing raises another.
func TestASecondRejectedPushOnAStoppedStreamKeepsOneJudgment(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	accepted(w, "s1-1", "s1-2")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Rejected: true, Note: "the base moved twice", Who: "lander"}))
	w.clean("rejected")

	p := MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Rejected: true, Note: "the base moved again", Who: "lander"})

	require.Len(t, p.Refused, 1, "the second rejection: %+v", p.Refused)
	assert.Contains(t, p.Refused[0].Why, "stopped (rejected)", "the refusal names the stop: %+v", p.Refused)
	assert.Empty(t, p.Units, "the second rejection moves nothing: %d units", len(p.Units))
	assert.Len(t, w.notesOf(NRejected), 1, "a second judgment opens: %v", w.notesOf(NRejected))
	assert.Equal(t, StreamStopped, w.s.StreamCtl("s1").F("state"), "the second rejection moved the stream: %s", w.s.StreamCtl("s1").F("state"))
}

// A landing does not resume a stream stopped red: that cause is the
// coordinator's to resolve, and the report is refused as before.
func TestALandingDoesNotResumeAStreamStoppedRed(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	accepted(w, "s1-1", "s1-2")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Red: true, Note: "the check failed", Who: "lander"}))
	require.Equal(t, "red", w.s.StreamCtl("s1").F("cause"), "the fixture: %s", w.s.StreamCtl("s1").F("cause"))
	w.clean("red")

	p := MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1, Who: "lander"})

	require.Len(t, p.Refused, 1, "the landing of a red stop: %+v", p.Refused)
	assert.Contains(t, p.Refused[0].Why, "stopped (red)", "the refusal names the stop: %+v", p.Refused)
	assert.Empty(t, p.Units, "nothing lands on a red stop: %d units", len(p.Units))
	assert.Equal(t, StreamStopped, w.s.StreamCtl("s1").F("state"), "the red stop moved: %s", w.s.StreamCtl("s1").F("state"))
}
