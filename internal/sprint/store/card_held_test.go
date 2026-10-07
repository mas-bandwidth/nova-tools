package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
)

// TestCardHeldReadsTheTablesOnce pins the card read: the card, what holds it and
// the work table's column for its place in line come from one Load of the
// tables, where CardOf and Held each loaded the work table whole.
func TestCardHeldReadsTheTablesOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Brief: proBrief, Stream: "s1", Count: 2}))

	loads := func(f func()) int {
		before := h.m.Calls["cells"]
		f()
		return h.m.Calls["cells"] - before
	}
	var v CardInfo
	n := loads(func() {
		var err error
		v, err = h.st.CardHeld(h.ctx, "s1-1")
		require.NoError(t, err)
	})
	require.NotNil(t, v.Primary)
	assert.Equal(t, 1, n, "CardHeld loaded the tables %d times", n)
	assert.NotNil(t, v.Hold, "a card on the table has a named hold")
	assert.NotEmpty(t, v.Column, "the work table's column is there for the place in line")

	plain, err := h.st.CardOf(h.ctx, "s1-1")
	require.NoError(t, err)
	assert.Equal(t, plain.Primary.ID, v.Primary.ID)
	assert.Equal(t, plain.Needs, v.Needs)
	assert.Equal(t, plain.NeededBy, v.NeededBy)
	held, err := h.st.Held(h.ctx, "s1-1")
	require.NoError(t, err)
	assert.Equal(t, held, *v.Hold, "the one read holds as Held does")
}
