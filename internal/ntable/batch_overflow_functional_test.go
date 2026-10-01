//go:build functional

package ntable_test

// A counter at its maximum cannot advance: every change that would step it
// refuses OVERFLOW before any write, naming the member, and the store is
// untouched.

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const maxCounter = "18446744073709551615"

func TestBatchMemberRevisionAtItsMaximumRefusesEveryChange(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	require.NoError(t, c.HSet(ctx, ntable.MemberKey("a"), "revision", maxCounter).Err())
	cases := map[string]ntable.BatchMemberEntry{
		"field set": {ID: "a", Expect: &ntable.MemberExpect{}, Set: map[string]string{"k": "v"}},
		"unset":     {ID: "a", Expect: &ntable.MemberExpect{}, Unset: []string{"role"}},
		"move":      {ID: "a", Expect: &ntable.MemberExpect{}, Move: &ntable.MemberMoveOp{Row: "test", Col: "working"}},
		"remove":    {ID: "a", Expect: &ntable.MemberExpect{}, Remove: true},
	}
	for name, entry := range cases {
		before := storeImage(t, c)
		_, err := ntable.ApplyBatch(ctx, c, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: probeRev(ctx, c), OperationID: "ov-" + name, Members: []ntable.BatchMemberEntry{entry}})
		assert.ErrorIs(t, err, ntable.ErrCounterOverflow, "%s: %v; want OVERFLOW naming member a with changed=no", name, err)
		assert.ErrorContains(t, err, `member "a"`, "%s: %v; want OVERFLOW naming member a with changed=no", name, err)
		assert.ErrorContains(t, err, "changed=no", "%s: %v; want OVERFLOW naming member a with changed=no", name, err)
		assert.Equal(t, before, storeImage(t, c), "%s: the store changed", name)
	}
	// a late entry at the maximum refuses the whole batch, and an entry that changes nothing does not step it
	before := storeImage(t, c)
	_, err := ntable.ApplyBatch(ctx, c, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: probeRev(ctx, c), OperationID: "ov-late", Members: []ntable.BatchMemberEntry{
		{ID: "b", Expect: &ntable.MemberExpect{}, Set: map[string]string{"k": "v"}},
		{ID: "a", Expect: &ntable.MemberExpect{}, Set: map[string]string{"k": "v"}}}})
	assert.ErrorIs(t, err, ntable.ErrCounterOverflow, "a late entry at the maximum: %v", err)
	assert.Equal(t, before, storeImage(t, c), "a late entry at the maximum: %v", err)
	r, err := ntable.ApplyBatch(ctx, c, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: probeRev(ctx, c), OperationID: "ov-guard", Members: []ntable.BatchMemberEntry{
		{ID: "a", Expect: &ntable.MemberExpect{Revision: maxCounter}, Move: &ntable.MemberMoveOp{Row: "build", Col: "ready"}}}})
	assert.NoError(t, err, "a move to the current cell at the maximum changes nothing: %+v", r)
	assert.Equal(t, "noop", r.Outcome, "a move to the current cell at the maximum changes nothing: %+v", r)
}

func TestBatchTableRevisionAtItsMaximumRefusesTheBatch(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	require.NoError(t, c.HSet(ctx, ntable.RevisionKey("demo"), "n", maxCounter).Err())
	before := storeImage(t, c)
	for name, entry := range map[string]ntable.BatchMemberEntry{
		"change": {ID: "a", Expect: &ntable.MemberExpect{}, Set: map[string]string{"k": "v"}},
		"guard":  {ID: "a", Expect: &ntable.MemberExpect{}},
	} {
		_, err := ntable.ApplyBatch(ctx, c, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: maxCounter, OperationID: "tv-" + name, Members: []ntable.BatchMemberEntry{entry}})
		assert.ErrorIs(t, err, ntable.ErrCounterOverflow, "%s: %v; want an overflow of the table revision with changed=no", name, err)
		assert.ErrorContains(t, err, "table revision", "%s: %v; want an overflow of the table revision with changed=no", name, err)
		assert.ErrorContains(t, err, "changed=no", "%s: %v; want an overflow of the table revision with changed=no", name, err)
		assert.Equal(t, before, storeImage(t, c), "%s: the store changed", name)
	}
}
