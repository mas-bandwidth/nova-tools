package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The stream coverage card of 2026-10-04: no unit test reached RulesPath or
// SetRulesPath (both 0.0% in the unit tier's per-function table), so these two
// tests are the whole reach.

// TestRulesCoverSetAndReadBack pins SetRulesPath and RulesPath on the main
// path: a sprint that names no child-rules file reads "", a set path reads
// back unchanged, and an empty path records none again.
func TestRulesCoverSetAndReadBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	got, err := h.st.RulesPath(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "", got, "a sprint naming no rules names none")

	require.NoError(t, h.st.SetRulesPath(h.ctx, "fleet/child-rules.txt"))
	got, err = h.st.RulesPath(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "fleet/child-rules.txt", got)

	require.NoError(t, h.st.SetRulesPath(h.ctx, ""))
	got, err = h.st.RulesPath(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "", got, "an empty path records none")
}

// TestRulesCoverAStoreWithNoKeysRefuses pins the refusal: a backend that keeps
// no keys refuses SetRulesPath with errNoKeys, and RulesPath on it names none.
func TestRulesCoverAStoreWithNoKeysRefuses(t *testing.T) {
	t.Parallel()
	st := &Store{B: kvless{NewMem()}}
	ctx := context.Background()

	require.ErrorIs(t, st.SetRulesPath(ctx, "fleet/child-rules.txt"), errNoKeys)

	got, err := st.RulesPath(ctx)
	require.NoError(t, err)
	assert.Equal(t, "", got, "a backend with no keys names none")
}
