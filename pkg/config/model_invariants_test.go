package config

import (
	"context"
	"strings"
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

// TestRedisIsACopy: from tla/ConfigApply.tla (ConfigApply.tla:499 ViewEq),
// each store row's view must be present in Redis and equal.
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

// TestApplyOrder: from tla/ConfigApply.tla (ConfigApply.tla:511-520), in any
// prefix of an apply's writes a machine ceiling precedes a friend placed on
// it, and a friend's removal is followed only by removals. The product runs
// the kinds in KindNames() order (cmd/nova-config/apply.go:105-111), so the
// test drives that same loop: the friends are placed on the machine the
// fleet names as coordinator, whose ceiling their slots are charged to
// (pkg/config/redis.go), and a second pass sets one and removes another.
func TestApplyOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	ap := newFake()

	machine, _ := Lookup(KindMachine)
	friend, _ := Lookup(KindFriend)
	mrow, err := machine.NewRow("m1", map[string]string{"user": "u", "seat": "s", "slots": "10"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindMachine, mrow, "actor")
	require.NoError(t, err)
	for _, name := range []string{"f1", "f2"} {
		frow, err := friend.NewRow(name, map[string]string{"slots": "4", "tiers": "flash"})
		require.NoError(t, err)
		_, err = st.Insert(ctx, KindFriend, frow, "actor")
		require.NoError(t, err)
	}
	// The link the model's InsertFriend carries: the friends are placed on
	// m1, the machine the fleet row names as coordinator, the ceiling the
	// friend apply charges their slots to (pkg/config/redis.go).
	_, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": "m1"}, "actor")
	require.NoError(t, err)

	type opLine struct{ kind, op, name string }
	// applyKinds drives the product's loop (cmd/nova-config/apply.go:105-111)
	// over the two kinds the model's instance keeps (ConfigApply.tla:14-21).
	applyKinds := func(report func(opLine)) {
		for _, kn := range KindNames() {
			if kn != KindMachine && kn != KindFriend {
				continue
			}
			_, err := Apply(ctx, st, ap, kn, "actor", false, func(op Op) {
				report(opLine{kn, op.Op, op.Name})
			})
			require.NoError(t, err)
		}
	}

	var prefix []opLine
	applyKinds(func(l opLine) { prefix = append(prefix, l) })

	machineIdx, friendIdx := -1, -1
	for i, l := range prefix {
		if l.kind == KindMachine && (l.op == OpAdd || l.op == OpSet) && machineIdx == -1 {
			machineIdx = i
		}
		if l.kind == KindFriend && (l.op == OpAdd || l.op == OpSet) && friendIdx == -1 {
			friendIdx = i
		}
	}
	require.NotEqual(t, -1, machineIdx, "the machine ceiling is in the prefix %v", prefix)
	require.NotEqual(t, -1, friendIdx, "a friend placed on m1 is in the prefix %v", prefix)
	require.Less(t, machineIdx, friendIdx, "machine %d precedes the friend on it %d under KindNames() %v: %v", machineIdx, friendIdx, KindNames(), prefix)

	// A friend's removal is followed only by removals: change one friend
	// (a set) and delete the other (a remove), then apply again. The plan
	// writes adds and sets before removes (pkg/config/apply.go Plan),
	// so the set precedes the remove and only removes follow it.
	_, _, err = st.Update(ctx, KindFriend, "f1", map[string]string{"slots": "6"}, "actor")
	require.NoError(t, err)
	_, err = st.Delete(ctx, KindFriend, "f2", "actor")
	require.NoError(t, err)

	prefix = nil
	applyKinds(func(l opLine) { prefix = append(prefix, l) })

	firstRemove, firstPlace := -1, -1
	for i, l := range prefix {
		if l.kind != KindFriend {
			continue
		}
		if (l.op == OpAdd || l.op == OpSet) && firstPlace == -1 {
			firstPlace = i
		}
		if l.op == OpRemove && firstRemove == -1 {
			firstRemove = i
		}
	}
	require.NotEqual(t, -1, firstPlace, "a friend add or set is in the prefix %v", prefix)
	require.NotEqual(t, -1, firstRemove, "a friend removal is in the prefix %v", prefix)
	require.Less(t, firstPlace, firstRemove, "the friend set %d precedes the friend remove %d: %v", firstPlace, firstRemove, prefix)
	for _, l := range prefix[firstRemove:] {
		require.Equal(t, OpRemove, l.op, "after a friend remove only removals follow: %v", prefix)
	}
}

// TestStampOnlyWhenComplete: from tla/ConfigApply.tla (ConfigApply.tla:523),
// the revision stamp is written only after that kind's ops complete.
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
	require.True(t, strings.HasPrefix(lastEntry, "stamp"), "last op is stamp")

	// Every op line comes before the stamp
	for i := 0; i < len(ap.log)-1; i++ {
		require.False(t, strings.HasPrefix(ap.log[i], "stamp"), "stamp only at end")
	}

	// Test refusal case (ap.refuse, see TestApplyStopsAtARefusalAndNamesIt):
	// apply refuses and revs[kind] has not moved (ConfigApply.tla:523)
	ap2 := newFake()
	ap2.revs[KindFriend] = 1000 // ahead, will cause conflict
	st2 := NewMem()

	_, err = Apply(ctx, st2, ap2, KindFriend, "actor", false, func(Op) {})
	require.Error(t, err, "apply refuses")
	require.True(t, IsConflict(err), "error is conflict")
	require.Equal(t, int64(1000), ap2.revs[KindFriend], "revs[KindFriend] did not move")
}
