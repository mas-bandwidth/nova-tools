package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// S6. A caller's operation id recorded for another verb is refused as a
// conflict naming the recorded verb; the other verb's result is never
// returned as a replay, and nothing is done.
func TestACallerOpOfAnotherVerbIsAConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	start := DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 2}})
	start.CallerOp = "op-A"
	h.must(start)
	before := h.revisions()
	c := h.snap().Fleet.Card("s1-1.w1")
	take := TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}})
	take.CallerOp = "op-A"
	res, err := h.st.Run(h.ctx, take)
	var ce *OpConflictError
	require.ErrorAs(t, err, &ce, "take under start's operation id: %+v %v", res, err)
	require.Equal(t, "deal", ce.Recorded, "take under start's operation id: %+v %v", res, err)
	require.False(t, res.Replay, "take under start's operation id: %+v %v", res, err)
	require.Empty(t, res.Moved, "take under start's operation id: %+v %v", res, err)
	require.Contains(t, err.Error(), "deal", "take under start's operation id: %+v %v", res, err)
	h.nothingWritten(before)
	again := h.must(start)
	require.True(t, again.Replay, "start's own retry: %+v", again)
}

// S6. The same verb with other arguments under a recorded caller's operation
// id is a conflict too; the same arguments replay.
func TestACallerOpWithOtherArgumentsIsAConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	add := func(n int) Step {
		r := sprint.AddReq{Stream: "s2", Count: n}
		s := AddStep(r)
		s.CallerOp, s.Args = "op-B", ArgsOf(r)
		return s
	}
	first := h.must(add(1))
	again := h.must(add(1))
	require.True(t, again.Replay, "the same add again: %+v", again)
	require.Equal(t, first.Op, again.Op, "the same add again: %+v", again)
	before := h.revisions()
	res, err := h.st.Run(h.ctx, add(2))
	var ce2 *OpConflictError
	require.ErrorAs(t, err, &ce2, "add with other arguments under the same id: %+v %v", res, err)
	require.True(t, ce2.OtherArgs, "add with other arguments under the same id: %+v %v", res, err)
	require.Equal(t, "add", ce2.Recorded, "add with other arguments under the same id: %+v %v", res, err)
	require.False(t, res.Replay, "add with other arguments under the same id: %+v %v", res, err)
	h.nothingWritten(before)
}
