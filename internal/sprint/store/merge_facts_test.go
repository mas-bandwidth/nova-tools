package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// A cross fact names a card that is placed, in another stream, and not
// landed; otherwise the merge step refuses it, saying which condition failed,
// and the stream keeps merging.
func TestCrossFactNeedsAnOpenCardOfAnotherStream(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ other, why string }{
		{"s1-2", "same stream"},
		{"s1-1", "the card itself"},
		{"nosuch", "not on the table"},
		{"", "names no other card"},
		{"b1", "landed already"},
	} {
		t.Run("other="+tc.other, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(2)
			h.through("s1-1", "s1-2")
			if tc.other == "b1" {
				h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b1"}}))
				h.through("b1")
				h.must(MergeStep(sprint.MergeReq{Stream: "s2"}))
				require.Equal(t, sprint.Landed, h.state("b1"), "b1 is %s", h.state("b1"))
			}
			res := h.run(MergeStep(sprint.MergeReq{Stream: "s1", Cross: "s1-1=" + tc.other}))
			require.Len(t, res.Refused, 1, "cross s1-1=%s: moved %v refused %v, want refused saying %q", tc.other, res.Moved, res.Refused, tc.why)
			require.Contains(t, res.Refused[0].Why, tc.why, "cross s1-1=%s: moved %v refused %v, want refused saying %q", tc.other, res.Moved, res.Refused, tc.why)
			require.Empty(t, res.Moved, "cross s1-1=%s: moved %v refused %v, want refused saying %q", tc.other, res.Moved, res.Refused, tc.why)
			require.NotEqual(t, string(sprint.StreamStopped), h.snap().StreamCtl("s1").F("state"), "a refused cross fact stopped the stream")
			h.clean("refused cross")
		})
	}
}

// A cross fact on an open card of another stream stops the stream, and the
// notification offers return and drop, which move the stuck card.
func TestCrossStopOffersReturnAndDrop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.through("s1-1")
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b1"}}))
	h.through("b1")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Cross: "s1-1=b1"}))
	s := h.snap()
	require.Equal(t, string(sprint.StreamStopped), s.StreamCtl("s1").F("state"), "cross on an open card of another stream did not stop the stream")
	require.Equal(t, string(sprint.Stuck), s.Merge.Placed("s1-1").Col, "cross on an open card of another stream did not stop the stream")
	var found bool
	for _, o := range s.Open {
		if o.Note.Type == sprint.NCross {
			found = true
			for _, d := range []string{"return", "drop"} {
				require.True(t, hasString(o.Note.Decisions, d), "the cross notification's decisions %v lack %q", o.Note.Decisions, d)
			}
		}
	}
	require.True(t, found, "no open cross notification: %v", s.Open)
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "waits for b1"}))
	h.must(ResumeStep(sprint.ResumeReq{Stream: "s1"}))
	h.clean("returned and resumed")
}

// A stream stopped for a red branch resumes only when the coordinator says
// what was done.
func TestResumeAfterRedWantsWhatWasDone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.through("s1-1", "s1-2")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Red: true}))
	for _, did := range []string{"", "  "} {
		res := h.run(ResumeStep(sprint.ResumeReq{Stream: "s1", Did: did}))
		require.Empty(t, res.Moved, "resume after red with did %q: moved %v refused %v", did, res.Moved, res.Refused)
		require.Len(t, res.Refused, 1, "resume after red with did %q: moved %v refused %v", did, res.Moved, res.Refused)
		require.Contains(t, res.Refused[0].Why, "--did", "resume after red with did %q: moved %v refused %v", did, res.Moved, res.Refused)
	}
	require.Equal(t, string(sprint.StreamStopped), h.snap().StreamCtl("s1").F("state"), "a refused resume moved the stream")
	h.must(ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "reverted the suspect"}))
	s := h.snap().StreamCtl("s1")
	require.Equal(t, sprint.StreamMerging, s.F("state"), "resume with did: state %s did %q", s.F("state"), s.F("did"))
	require.Equal(t, "reverted the suspect", s.F("did"), "resume with did: state %s did %q", s.F("state"), s.F("did"))
	h.clean("resumed")
}

// rank refuses a landed primary: landed is final.
func TestRankRefusesALandedPrimary(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.through("s1-1")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1"}))
	res := h.run(RankStep(sprint.RankReq{IDs: []string{"s1-1"}, First: true}))
	require.Empty(t, res.Moved, "rank of a landed primary: moved %v refused %v", res.Moved, res.Refused)
	require.Len(t, res.Refused, 1, "rank of a landed primary: moved %v refused %v", res.Moved, res.Refused)
	require.Contains(t, res.Refused[0].Why, "landed", "rank of a landed primary: moved %v refused %v", res.Moved, res.Refused)
	h.must(RankStep(sprint.RankReq{IDs: []string{"s1-2"}, First: true}))
}

// add refuses a need that names no primary; a need on a primary on the
// table, placed or kept (dropped), is admitted waiting.
func TestAddRefusesANeedThatDoesNotExist(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	res := h.run(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b1", "b2"}, Needs: []string{"s1-1", "nosuch"}}))
	require.Empty(t, res.Moved, "add with a need on no primary: moved %v refused %v", res.Moved, res.Refused)
	require.Len(t, res.Refused, 2, "add with a need on no primary: moved %v refused %v", res.Moved, res.Refused)
	require.Contains(t, res.Refused[0].Why, "nosuch", "add with a need on no primary: moved %v refused %v", res.Moved, res.Refused)
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}, Reason: "obsolete"}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b1", "b2"}, Needs: []string{"s1-1", "s1-2"}}))
	require.Equal(t, sprint.Waiting, h.state("b1"), "b1 is %s, b2 is %s", h.state("b1"), h.state("b2"))
	require.Equal(t, sprint.Waiting, h.state("b2"), "b1 is %s, b2 is %s", h.state("b1"), h.state("b2"))
	h.clean("added")
}

// Two needs dropped in one step give one blocked note per waiting primary.
func TestTwoDroppedNeedsGiveOneBlockedNote(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"later"}, Needs: []string{"s1-1", "s1-2"}}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}, Reason: "obsolete"}))
	notes, _, err := h.m.NotesSince(h.ctx, "", 1000)
	require.NoError(t, err)
	var blocked []sprint.Note
	for _, n := range notes {
		if n.Type == sprint.NBlocked {
			blocked = append(blocked, n)
		}
	}
	require.Len(t, blocked, 1, "blocked notes: %+v", blocked)
	require.Contains(t, blocked[0].What, "s1-1", "blocked notes: %+v", blocked)
	require.Contains(t, blocked[0].What, "s1-2", "blocked notes: %+v", blocked)
	h.clean("dropped")
}

func hasString(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
