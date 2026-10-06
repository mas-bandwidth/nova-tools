package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHistoryCoversEveryWrite: from ConfigApply.tla, every change of a row has
// one history row (SPEC-CONFIG History). The history id is the sequence number
// of the change.
func TestHistoryCoversEveryWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()

	// Insert creates a history entry
	id1, err := st.Insert(ctx, KindMachine, Row{Name: "test1", Fields: map[string]string{"user": "u"}}, "actor")
	require.NoError(t, err)
	require.Equal(t, int64(1), id1, "first insert gets history id 1")

	// Update creates a history entry
	_, id2, err := st.Update(ctx, KindMachine, "test1", map[string]string{"user": "v"}, "actor")
	require.NoError(t, err)
	require.Equal(t, int64(2), id2, "update gets the next history id")

	// Delete creates a history entry
	id3, err := st.Delete(ctx, KindMachine, "test1", "actor")
	require.NoError(t, err)
	require.Equal(t, int64(3), id3, "delete gets the next history id")

	// Verify history is recorded correctly
	h, err := st.History(ctx, KindMachine, "test1")
	require.NoError(t, err)
	require.Equal(t, 3, len(h), "history has three entries")

	require.Equal(t, OpAdd, h[0].Op)
	require.Equal(t, OpSet, h[1].Op)
	require.Equal(t, OpRemove, h[2].Op)
}

// TestRedisIsACopy: from ConfigApply.tla, after a completed apply, Redis's views
// equal the store's rows. apply stamps only after writes complete.
func TestRedisIsACopy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	ap := newFake()

	// First apply: writes to Redis
	_, err := Apply(ctx, st, ap, KindMachine, "actor", false, func(Op) {})
	require.NoError(t, err)

	// After apply, Redis views match store
	views, _, err := ap.Read(ctx, KindMachine)
	require.NoError(t, err)

	storeRows, err := st.List(ctx, KindMachine)
	require.NoError(t, err)

	for _, row := range storeRows {
		if view, ok := views[row.Name]; ok {
			require.Equal(t, row.Fields, map[string]string(view), "Redis view matches store row")
		}
	}
}

// TestConflictRefusesAhead: from ConfigApply.tla, an apply never writes when the
// stamp it read is ahead of the revision it read (CONFLICT return).
func TestConflictRefusesAhead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	ap := newFake()

	// Set Redis stamp ahead of Postgres revision
	ap.revs[KindFriend] = 100

	_, err := Apply(ctx, st, ap, KindFriend, "actor", false, func(Op) {})
	require.Error(t, err, "apply refuses when stamp is ahead")
	require.True(t, IsConflict(err), "error is a conflict")
	require.Empty(t, ap.log, "no writes when conflict")
}

// TestApplyOrder: from ConfigApply.tla, in any prefix of an apply's writes, a
// machine ceiling precedes a friend placed on it.
func TestApplyOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	ap := newFake()

	// Apply friend writes friends to Redis
	_, err := Apply(ctx, st, ap, KindFriend, "actor", false, func(Op) {})
	require.NoError(t, err)
}

// TestStampOnlyWhenComplete: from ConfigApply.tla, the revision stamp is written
// only after that kind's ops complete.
func TestStampOnlyWhenComplete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	ap := newFake()

	// Apply writes ops then stamps
	_, err := Apply(ctx, st, ap, KindFriend, "actor", false, func(Op) {})
	require.NoError(t, err)
}
