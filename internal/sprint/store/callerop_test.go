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
	h.assertConflict(take, "deal", false, "take under start's operation id")
	h.assertNoRevisionsWritten(before)
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
	h.assertConflict(add(2), "add", true, "add with other arguments under the same id")
	h.assertNoRevisionsWritten(before)
}
