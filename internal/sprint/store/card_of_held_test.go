package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestACardIsOneWholeRead pins that CardOfHeld reads the tables whole once, where CardOf,
// Held and a Load of the work table read them three times, and that its answers are theirs.
func TestACardIsOneWholeRead(t *testing.T) {
	t.Parallel()
	h := midFlight(t)
	for _, id := range []string{"s1-1", "s1-4", "s1-6", "later"} {
		want, err := h.st.CardOf(h.ctx, id)
		require.NoError(t, err)
		wantHeld, err := h.st.Held(h.ctx, id)
		require.NoError(t, err)
		before := h.stats().reads.Load()
		got, err := h.st.CardOfHeld(h.ctx, id)
		require.NoError(t, err)
		assert.EqualValues(t, 1, h.stats().reads.Load()-before, "%s: the tables were read whole more than once", id)
		assert.Equal(t, want.Primary.ID, got.Primary.ID)
		assert.Equal(t, want.Needs, got.Needs, id)
		assert.Equal(t, want.NeededBy, got.NeededBy, id)
		assert.Len(t, got.Work, len(want.Work), id)
		assert.Len(t, got.Reads, len(want.Reads), id)
		require.NotNil(t, got.Held, id)
		assert.Equal(t, wantHeld.By, got.Held.By, id)
		assert.Equal(t, wantHeld.Why, got.Held.Why, id)
		require.NotNil(t, got.Table, id)
		assert.NotNil(t, got.Table.Card(id), id)
	}
	plain, err := h.st.CardOf(h.ctx, "s1-1")
	require.NoError(t, err)
	assert.Nil(t, plain.Held, "CardOf alone names no hold")
	assert.Nil(t, plain.Table)
	none, err := h.st.CardOfHeld(h.ctx, "no-such")
	require.NoError(t, err)
	assert.Nil(t, none.Primary)
}

// TestACardUnderAPendingOperationIsHeldByRepair pins that CardOfHeld answers a pending
// operation as Held does: with the repair line, and no tables read for it.
func TestACardUnderAPendingOperationIsHeldByRepair(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.pendingOp(OpRecord{ID: "op-1", Verb: "deal"})
	want, err := h.st.Held(h.ctx, "s1-1")
	require.NoError(t, err)
	got, err := h.st.CardOfHeld(h.ctx, "s1-1")
	require.NoError(t, err)
	require.NotNil(t, got.Held)
	assert.Equal(t, want, *got.Held)
	assert.Contains(t, got.Held.Why, "nova-sprint repair")
}
