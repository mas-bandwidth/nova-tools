package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// C2: an operation recorded under a caller's id before a clear (its reply
// lost), sent again after it, is refused naming the epoch it belongs to, and
// is never run again as new work.
func TestCallerOpOfAnEarlierEpochIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	step := AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"late"}})
	step.CallerOp = "caller-1"
	h.must(step)
	_, err := h.st.Clear(h.ctx)
	require.NoError(t, err)
	img := h.image()
	for _, held := range []*uint64{nil, new(uint64)} {
		step.Epoch = held
		res, err := h.st.Run(h.ctx, step)
		require.NoError(t, err, "the same operation after the clear (held %v): %+v %v", held, res, err)
		require.False(t, res.Replay, "the same operation after the clear (held %v): %+v %v", held, res, err)
		require.Empty(t, res.Moved, "the same operation after the clear (held %v): %+v %v", held, res, err)
		require.Len(t, res.Refused, 1, "the same operation after the clear (held %v): %+v %v", held, res, err)
		why := res.Refused[0].Why
		if held == nil {
			require.Contains(t, why, "cleared at", "the refusal: %s", why)
			require.Contains(t, why, "operation caller-1 belongs to epoch 0", "the refusal: %s", why)
		}
		c := h.snap().Work.Card("late")
		require.True(t, c == nil || !c.Placed(), "the operation of epoch 0 ran again at epoch 1")
		got := h.image()
		require.Equal(t, img, got, "the refused operation changed the store:\n%s\nwas\n%s", got, img)
	}
	// A caller's id holding the epoch's mark is refused before anything.
	step.Epoch, step.CallerOp = nil, "x~1"
	_, err = h.st.Run(h.ctx, step)
	require.ErrorContains(t, err, "holds '~'", "an id with the epoch's mark: %v", err)
}

// C2: notification and judgment ids carry the epoch: the same operation
// family at two epochs has two judgment ids, and an ack or an answer naming
// the old one is refused, naming its epoch, and closes nothing of the new.
func TestNoteIDsCarryTheEpoch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.st.NewID = func() string { return "same" }
	h.setup(2)
	failIt := func() string {
		h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		s := h.snap()
		c := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
		h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
		h.must(FinishStep(sprint.FinishReq{Failed: true, Report: "red", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
		v, err := h.st.Inbox(h.ctx, 0, 0, 100)
		require.NoError(t, err, "no judgment: %v", err)
		require.NotEmpty(t, v.Open, "no judgment: %v", err)
		return v.Open[0].Note.ID
	}
	oldID := failIt()
	_, err := h.st.Clear(h.ctx)
	require.NoError(t, err)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
	newID := failIt()
	require.NotEqual(t, oldID, newID, "judgment ids: epoch 0 %s, epoch 1 %s", oldID, newID)
	require.Equal(t, uint64(0), sprint.IDEpoch(oldID), "judgment ids: epoch 0 %s, epoch 1 %s", oldID, newID)
	require.Equal(t, uint64(1), sprint.IDEpoch(newID), "judgment ids: epoch 0 %s, epoch 1 %s", oldID, newID)
	img := h.image()
	res, err := h.st.Run(h.ctx, AckStep(sprint.AckReq{Notes: []string{oldID}, Reason: "x"}))
	require.NoError(t, err, "ack of the old judgment: %+v %v", res, err)
	require.Empty(t, res.Moved, "ack of the old judgment: %+v %v", res, err)
	require.Len(t, res.Refused, 1, "ack of the old judgment: %+v %v", res, err)
	require.Contains(t, res.Refused[0].Why, "belongs to epoch 0", "ack of the old judgment: %+v %v", res, err)
	err = h.st.SetReview(h.ctx, oldID, t0)
	require.ErrorContains(t, err, "belongs to epoch 0", "wait on the old judgment: %v", err)
	require.Equal(t, img, h.image(), "the ack and the wait of the old judgment changed the store")
	v, _ := h.st.Inbox(h.ctx, 0, 0, 100)
	require.NotEmpty(t, v.Open, "the new judgment is not open: %+v", v.Open)
	require.Equal(t, newID, v.Open[0].Note.ID, "the new judgment is not open: %+v", v.Open)
	// An answer naming the old judgment refuses the whole step, naming its
	// epoch and the clear: the rework moves nothing.
	img = h.image()
	res, err = h.st.Run(h.ctx, ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "x", Answers: []string{oldID}}))
	require.NoError(t, err, "an answer naming the old judgment: %+v %v", res, err)
	require.Empty(t, res.Moved, "an answer naming the old judgment: %+v %v", res, err)
	require.Len(t, res.Refused, 1, "an answer naming the old judgment: %+v %v", res, err)
	require.Equal(t, oldID, res.Refused[0].Key, "an answer naming the old judgment: %+v %v", res, err)
	require.Contains(t, res.Refused[0].Why, "judgment of epoch 0; the sprint was cleared at", "an answer naming the old judgment: %+v %v", res, err)
	require.Equal(t, img, h.image(), "the refused rework changed the store")
}
