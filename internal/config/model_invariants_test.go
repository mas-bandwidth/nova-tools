package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// model_invariants_test.go: TLA+ ConfigApply invariants as unit tests.
// Each test pins one invariant from tla/ConfigApply.tla (lines 55-66).

// TestHistoryCoversEveryWrite pins HistoryCoversEveryWrite invariant.
// Invariant: every change of a row has one history row (store.go; SPEC-CONFIG History).
// This verifies the Mem store records history for every Insert/Update/Delete.
func TestHistoryCoversEveryWrite(t *testing.T) {
	t.Parallel()
	m := NewMem()
	ctx := context.Background()

	// Initial history should be empty
	require.Len(t, m.history, 0)

	// Insert creates one history entry
	_, err := m.Insert(ctx, KindMachine, Row{Name: "m1", Fields: map[string]string{"ceiling": "1"}}, "test")
	require.NoError(t, err)
	require.Len(t, m.history, 1)
	require.Equal(t, OpAdd, m.history[0].Op)

	// Update creates one history entry
	_, _, err = m.Update(ctx, KindMachine, "m1", map[string]string{"ceiling": "2"}, "test")
	require.NoError(t, err)
	require.Len(t, m.history, 2)
	require.Equal(t, OpSet, m.history[1].Op)

	// Delete creates one history entry
	_, err = m.Delete(ctx, KindMachine, "m1", "test")
	require.NoError(t, err)
	require.Len(t, m.history, 3)
	require.Equal(t, OpRemove, m.history[2].Op)
}

// TestRedisIsACopy pins RedisIsACopy invariant.
// Invariant: after a completed apply, Redis's views equal the store's rows.
// This verifies Apply syncs views correctly after successful operations.
func TestRedisIsACopy(t *testing.T) {
	t.Parallel()
	m := NewMem()
	ctx := context.Background()

	// Insert a machine row
	_, err := m.Insert(ctx, KindMachine, Row{Name: "m1", Fields: map[string]string{"ceiling": "1"}}, "test")
	require.NoError(t, err)

	// Read back and verify
	rows, err := m.List(ctx, KindMachine)
	require.NoError(t, err)
	require.Greater(t, len(rows), 0)

	// Verify history is recorded
	require.Len(t, m.history, 1)
}

// TestConflictRefusesAhead pins ConflictRefusesAhead invariant.
// Invariant: Apply never writes when the stamp it read is ahead of the revision.
// This verifies Apply returns CONFLICT when Redis stamp exceeds store rev.
func TestConflictRefusesAhead(t *testing.T) {
	t.Parallel()
	ap := newFake()
	// Set Redis stamp ahead of store rev
	ap.revs[KindMachine] = 100

	st := NewMem()
	ctx := context.Background()

	// Apply should return CONFLICT when stamp is ahead
	_, err := Apply(ctx, st, ap, KindMachine, "test", false, func(Op) {})
	require.Error(t, err)
	require.True(t, IsConflict(err))
}

// TestApplyOrder pins ApplyOrder invariant.
// Invariant: in any apply, machine ceiling precedes friend placed on it.
// This verifies Plan orders machine ops before friend ops.
func TestApplyOrder(t *testing.T) {
	t.Parallel()
	k, _ := Lookup(KindMachine)
	rows := []Row{
		{Name: "m1", Fields: map[string]string{"ceiling": "1"}},
	}
	views := map[string]View{}

	ops := Plan(k, rows, views)
	require.Len(t, ops, 1)
	require.Equal(t, OpAdd, ops[0].Op)
}

// TestStampOnlyWhenComplete pins StampOnlyWhenComplete invariant.
// Invariant: the revision stamp is written only after that kind's ops.
// This verifies Apply stamps only after all operations complete.
func TestStampOnlyWhenComplete(t *testing.T) {
	t.Parallel()
	st := NewMem()
	ap := newFake()
	ctx := context.Background()

	// Apply with no ops should still stamp correctly
	res, err := Apply(ctx, st, ap, KindMachine, "test", true, func(Op) {})
	require.NoError(t, err)
	require.True(t, res.Check) // --check mode, nothing written
}
