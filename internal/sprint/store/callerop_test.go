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

// S6. A caller's operation id is one word of letters, digits, '_' and '-',
// with a '.' too, but no '/' and no '..': the shape every other identity is
// held to (sprint.ValidID). The word becomes the operation's id family
// (sprint.OpFamily), and a judgment id built on it becomes a file name
// (nova-sprint inbox --wait --push writes id+".md"), so a '/' in it would
// name a note outside the push directory (security#68 finding 1). The door
// the '~' check guards refuses the step before anything is read or written.
func TestACallerOpWithASlashIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	before := h.revisions()
	for _, op := range []string{"../../../outside", "a/b", "a..b", "a b"} {
		step := AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"late"}})
		step.CallerOp = op
		_, err := h.st.Run(h.ctx, step)
		require.ErrorContains(t, err, "one word of letters, digits", "--op %q: %v", op, err)
	}
	h.nothingWritten(before)
	// a word with a dot stands: the shape nova-sprint land builds for its own
	// steps (land.go, l.c.op + "." + r.Stream + "." + step.Args)
	ok := AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"late"}})
	ok.CallerOp = "land.s1-1.a1b2c3d4e5f6"
	h.must(ok)
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
