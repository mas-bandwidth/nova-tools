package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A card is read with one whole read of the tables: its record, what holds it and its
// place in its stream's line all come from the same load, where they were three
// (CardOf's, Held's and the verb's own). docs/STANDARD.md: one round trip per verb.
func TestACardIsOneWholeRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.addReady("s1", 3, briefOf("flash", ""))

	before := h.st.stats().reads.Load()
	v, err := h.st.CardOfHeld(h.ctx, "s1-2")
	require.NoError(t, err)
	assert.Equal(t, int64(1), h.st.stats().reads.Load()-before, "whole reads for one card")

	require.NotNil(t, v.Primary)
	assert.Equal(t, "s1-2", v.Primary.ID)
	require.NotNil(t, v.Held, "what holds the card")
	assert.Equal(t, "s1-2", v.Held.ID)
	assert.NotEmpty(t, v.Line, "the work table's cards, for the card's place in line")

	plain, err := h.st.CardOf(h.ctx, "s1-2")
	require.NoError(t, err)
	assert.Equal(t, plain.Primary.ID, v.Primary.ID)
	assert.Equal(t, plain.Needs, v.Needs)
	assert.Equal(t, plain.NeededBy, v.NeededBy)
	held, err := h.st.Held(h.ctx, "s1-2")
	require.NoError(t, err)
	assert.Equal(t, held, *v.Held, "the same answer Held gives")
}
