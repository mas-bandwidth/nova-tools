package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHistoryCoversEveryWrite: from tla/ConfigApply.tla, every change of a row has
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

// TestRedisIsACopy: from tla/ConfigApply.tla, after a completed apply, Redis's views
// equal the store's rows (ConfigApply.tla:499 ViewEq).
func TestRedisIsACopy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	ap := newFake()

	// Seed store with rows
	id, err := st.Insert(ctx, KindMachine, Row{Name: "m1", Fields: map[string]string{"k": "v"}}, "actor")
	require.NoError(t, err)
	require.NotZero(t, id)

	// Apply to copy rows to Redis
	_, err = Apply(ctx, st, ap, KindMachine, "actor", false, func(Op) {})
	require.NoError(t, err)

	// After apply, each store row's view must be present in Redis and equal
	views, _, err := ap.Read(ctx, KindMachine)
	require.NoError(t, err)

	storeRows, err := st.List(ctx, KindMachine)
	require.NoError(t, err)

	require.Equal(t, len(storeRows), len(views), "each store row has a Redis view")
	for _, row := range storeRows {
		view, ok := views[row.Name]
		require.True(t, ok, "row %s present in Redis", row.Name)
		require.Equal(t, row.Fields, map[string]string(view), "Redis view equals store row for %s", row.Name)
	}
}

// TestConflictRefusesAhead: from tla/ConfigApply.tla, an apply never writes when the
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

// TestApplyOrder: from tla/ConfigApply.tla (ConfigApply.tla:511-521), in any prefix
// of an apply's writes, a machine ceiling precedes a friend placed on it.
func TestApplyOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	ap := newFake()

	// Seed a machine row and a friend placed on it
	mid, err := st.Insert(ctx, KindMachine, Row{Name: "m1", Fields: map[string]string{}}, "actor")
	require.NoError(t, err)
	require.NotZero(t, mid)

	fid, err := st.Insert(ctx, KindFriend, Row{Name: "f1", Fields: map[string]string{"machine": "m1"}}, "actor")
	require.NoError(t, err)
	require.NotZero(t, fid)

	// Apply to write friend; friend precedes machine ceiling in the order
	_, err = Apply(ctx, st, ap, KindFriend, "actor", false, func(Op) {})
	require.NoError(t, err)

	// In ap.log, machine add/set must come before friend add/set
	logMachineIdx := -1
	logFriendIdx := -1
	for i, entry := range ap.log {
		if entry.Kind == KindMachine {
			logMachineIdx = i
		}
		if entry.Kind == KindFriend {
			logFriendIdx = i
		}
	}

	require.NotEqual(t, -1, logMachineIdx, "machine op in log")
	require.NotEqual(t, -1, logFriendIdx, "friend op in log")
	require.Less(t, logMachineIdx, logFriendIdx, "machine op comes before friend op")

	// Friend remove is followed only by removes (ConfigApply.tla:511-521)
	friendRemoveIdx := -1
	for i, entry := range ap.log {
		if entry.Kind == KindFriend && entry.Op == OpRemove {
			friendRemoveIdx = i
		}
	}
	if friendRemoveIdx != -1 {
		for i := friendRemoveIdx + 1; i < len(ap.log); i++ {
			require.Equal(t, OpRemove, ap.log[i].Op, "after friend remove, only removes follow")
		}
	}
}

// TestStampOnlyWhenComplete: from tla/ConfigApply.tla (ConfigApply.tla:523), the
// revision stamp is written only after that kind's ops complete.
func TestStampOnlyWhenComplete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	ap := newFake()

	// Seed rows
	id, err := st.Insert(ctx, KindFriend, Row{Name: "f1", Fields: map[string]string{}}, "actor")
	require.NoError(t, err)
	require.NotZero(t, id)

	// Apply
	_, err = Apply(ctx, st, ap, KindFriend, "actor", false, func(Op) {})
	require.NoError(t, err)

	// Last entry in ap.log must be a stamp: "stamp <kind> <rev>"
	require.NotEmpty(t, ap.log, "log has entries")
	lastEntry := ap.log[len(ap.log)-1]
	require.Equal(t, OpStamp, lastEntry.Op, "last op is stamp")
	require.Equal(t, KindFriend, lastEntry.Kind, "stamp is for KindFriend")

	// Every op line comes before the stamp
	for i := 0; i < len(ap.log)-1; i++ {
		require.NotEqual(t, OpStamp, ap.log[i].Op, "stamp only at end")
	}

	// Test refusal case: apply refuses and revs[kind] does not move
	ap2 := newFake()
	ap2.revs[KindFriend] = 1000 // ahead, will cause conflict
	st2 := NewMem()

	_, err = Apply(ctx, st2, ap2, KindFriend, "actor", false, func(Op) {})
	require.Error(t, err, "apply refuses")
	require.True(t, IsConflict(err), "error is conflict")
	require.Equal(t, int64(1000), ap2.revs[KindFriend], "revs[KindFriend] did not move")
}
