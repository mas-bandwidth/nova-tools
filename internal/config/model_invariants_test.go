package config

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// model_invariants_test.go: TLA+ ConfigApply invariants as unit tests.
// Each test pins one invariant from tla/ConfigApply.tla (lines 55-75).
// Reversed witness models in tla/ConfigApply.tla demonstrate that violating
// these invariants maps to the Broken constants:
//   Broken = "stampfirst"  -> StampOnlyWhenComplete
//   Broken = "friendfirst" -> ApplyOrder
//   Broken = "nohistory"    -> HistoryCoversEveryWrite
//   Broken = "overahead"    -> ConflictRefusesAhead

// TestRedisIsACopy pins RedisIsACopy invariant (tla/ConfigApply.tla line 348).
// Invariant: after a completed apply, Redis's views equal the store's rows:
//   RedisIsACopy == \A k \in Kinds : clean[k] => ViewEq(k)
// This test runs Apply (check=false) on Mem with newFake(), requiring that the
// fake's views for the kind equal the store's rows (names and fields), after an
// add, a set, and a remove.
func TestRedisIsACopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	ap := newFake()

	// 1. Add: insert a machine row and apply.
	m1 := Row{
		Name:   "m1",
		Fields: map[string]string{"user": "glenn", "seat": "studio", "slots": "64", "runners": "1"},
	}
	_, err := m.Insert(ctx, KindMachine, m1, "test")
	require.NoError(t, err)

	res, err := Apply(ctx, m, ap, KindMachine, "test", false, func(Op) {})
	require.NoError(t, err)
	require.Equal(t, 1, res.Add)

	rows, err := m.List(ctx, KindMachine)
	require.NoError(t, err)
	require.Len(t, ap.views[KindMachine], len(rows))
	for _, r := range rows {
		require.Equal(t, View(r.Fields), ap.views[KindMachine][r.Name])
	}

	// 2. Set: update a field and apply.
	_, _, err = m.Update(ctx, KindMachine, "m1", map[string]string{"slots": "128"}, "test")
	require.NoError(t, err)

	res, err = Apply(ctx, m, ap, KindMachine, "test", false, func(Op) {})
	require.NoError(t, err)
	require.Equal(t, 1, res.Set)

	rows, err = m.List(ctx, KindMachine)
	require.NoError(t, err)
	require.Len(t, ap.views[KindMachine], len(rows))
	for _, r := range rows {
		require.Equal(t, View(r.Fields), ap.views[KindMachine][r.Name])
	}

	// 3. Remove: delete the row and apply.
	_, err = m.Delete(ctx, KindMachine, "m1", "test")
	require.NoError(t, err)

	res, err = Apply(ctx, m, ap, KindMachine, "test", false, func(Op) {})
	require.NoError(t, err)
	require.Equal(t, 1, res.Remove)

	rows, err = m.List(ctx, KindMachine)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Empty(t, ap.views[KindMachine])
}

// TestConflictRefusesAhead pins ConflictRefusesAhead invariant (tla/ConfigApply.tla line 352).
// Invariant: Apply never writes when the stamp it read is ahead of the revision:
//   ConflictRefusesAhead == ~wroteAhead
// Reversed witness: Broken = "overahead" (tla/ConfigApply.tla line 73).
// This test requires that fake logged no write (ap.log empty, views unchanged,
// ap.revs[KindMachine] still 100). The store is given at least one row so a
// write would otherwise occur.
func TestConflictRefusesAhead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	ap := newFake()

	// Give the store at least one row so a write would otherwise occur.
	m1 := Row{
		Name:   "m1",
		Fields: map[string]string{"user": "glenn", "seat": "studio", "slots": "64", "runners": "0"},
	}
	_, err := m.Insert(ctx, KindMachine, m1, "test")
	require.NoError(t, err)

	// Set Redis stamp ahead of store revision (rev is 1).
	ap.revs[KindMachine] = 100

	res, err := Apply(ctx, m, ap, KindMachine, "test", false, func(Op) {})
	require.Error(t, err)
	require.True(t, IsConflict(err), "expected Conflict error when Redis stamp is ahead")
	require.True(t, Refused(err))
	require.Equal(t, int64(100), res.RedisRev)

	// Invariant verification: no writes occurred.
	require.Empty(t, ap.log, "fake logged writes despite conflict ahead")
	require.Empty(t, ap.views[KindMachine], "views were modified despite conflict ahead")
	require.Equal(t, int64(100), ap.revs[KindMachine], "stamp was modified despite conflict ahead")
}

// TestApplyOrder pins ApplyOrder invariant (tla/ConfigApply.tla lines 356-368).
// Invariant: in any apply, machine ceiling precedes friend placed on it, and
// friend removals come last:
//   ApplyOrder ==
//     \A i \in 1..Len(prefix) :
//       /\ IsPlace(prefix[i]) =>
//            \/ ceilAtStart[prefix[i].value]
//            \/ \E j \in 1..(i - 1) :
//                 /\ prefix[j].kind = "machine"
//                 /\ prefix[j].name = prefix[i].value
//                 /\ prefix[j].op \in {"add", "set"}
//       /\ IsFrRem(prefix[i]) =>
//            \A j \in (i + 1)..Len(prefix) : prefix[j].op = "remove"
// Reversed witness: Broken = "friendfirst" (tla/ConfigApply.tla line 70).
// This test applies KindMachine then KindFriend against fake with a machine and
// a friend, requiring the log order (machine add before friend add); and adds
// a case with a remove to check that removes come last.
func TestApplyOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := NewMem()
	ap := newFake()

	// Seed one machine and one friend.
	m1 := Row{Name: "m1", Fields: map[string]string{"user": "glenn", "seat": "studio", "slots": "64", "runners": "0"}}
	_, err := st.Insert(ctx, KindMachine, m1, "test")
	require.NoError(t, err)

	f1 := Row{Name: "f1", Fields: map[string]string{"slots": "32", "tiers": "pro", "roles": "builder"}}
	_, err = st.Insert(ctx, KindFriend, f1, "test")
	require.NoError(t, err)

	// Apply KindMachine then KindFriend.
	_, err = Apply(ctx, st, ap, KindMachine, "test", false, func(Op) {})
	require.NoError(t, err)

	_, err = Apply(ctx, st, ap, KindFriend, "test", false, func(Op) {})
	require.NoError(t, err)

	// Require log order: machine add before friend add.
	var machAddIdx, friendAddIdx int = -1, -1
	for i, entry := range ap.log {
		if strings.HasPrefix(entry, "add machine m1") {
			machAddIdx = i
		}
		if strings.HasPrefix(entry, "add friend f1") {
			friendAddIdx = i
		}
	}
	require.NotEqual(t, -1, machAddIdx, "machine add not found in log")
	require.NotEqual(t, -1, friendAddIdx, "friend add not found in log")
	require.Less(t, machAddIdx, friendAddIdx, "machine add must precede friend add in log")

	// Case with a remove: add a second friend f2, remove f1 from store.
	f2 := Row{Name: "f2", Fields: map[string]string{"slots": "16", "tiers": "flash", "roles": "reader"}}
	_, err = st.Insert(ctx, KindFriend, f2, "test")
	require.NoError(t, err)

	_, err = st.Delete(ctx, KindFriend, "f1", "test")
	require.NoError(t, err)

	// Clear log before the next apply to isolate the plan's order.
	ap.log = nil
	res, err := Apply(ctx, st, ap, KindFriend, "test", false, func(Op) {})
	require.NoError(t, err)
	require.Equal(t, 1, res.Add)
	require.Equal(t, 1, res.Remove)

	// Verify plan and log order: friend add before friend remove, and remove comes last before stamp.
	var addIdx, removeIdx, stampIdx int = -1, -1, -1
	for i, entry := range ap.log {
		if strings.HasPrefix(entry, "add friend f2") {
			addIdx = i
		}
		if strings.HasPrefix(entry, "remove friend f1") {
			removeIdx = i
		}
		if strings.HasPrefix(entry, "stamp friend") {
			stampIdx = i
		}
	}
	require.NotEqual(t, -1, addIdx, "add friend f2 not found in log")
	require.NotEqual(t, -1, removeIdx, "remove friend f1 not found in log")
	require.NotEqual(t, -1, stampIdx, "stamp friend not found in log")
	require.Less(t, addIdx, removeIdx, "adds must precede removes")
	require.Less(t, removeIdx, stampIdx, "removes must come before stamp")

	// Verify invariant: once a remove occurs, no subsequent op is an add or set.
	foundRemove := false
	for _, entry := range ap.log {
		if strings.HasPrefix(entry, "remove ") {
			foundRemove = true
		}
		if foundRemove {
			require.False(t, strings.HasPrefix(entry, "add ") || strings.HasPrefix(entry, "set "),
				"no add or set may follow a remove in log: %s", entry)
		}
	}
}

// TestStampOnlyWhenComplete pins StampOnlyWhenComplete invariant (tla/ConfigApply.tla line 371).
// Invariant: the revision stamp is written only after that kind's ops:
//   StampOnlyWhenComplete == \A k \in Kinds : stampMoved[k] => opsDone[k]
// Reversed witness: Broken = "stampfirst" (tla/ConfigApply.tla line 69).
// This test applies with check=false and ops pending, requiring that every op
// appears in fake's log before stamp (ap.revs set only after last op), and that
// a refused op (ap.refuse) leaves ap.revs unset.
func TestStampOnlyWhenComplete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Part 1: Apply with check=false and ops pending. Every op appears before stamp.
	st := NewMem()
	ap := newFake()

	m1 := Row{Name: "m1", Fields: map[string]string{"user": "glenn", "seat": "studio", "slots": "64", "runners": "0"}}
	m2 := Row{Name: "m2", Fields: map[string]string{"user": "gaffer", "seat": "swarm-hulk", "slots": "32", "runners": "0"}}
	_, err := st.Insert(ctx, KindMachine, m1, "test")
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindMachine, m2, "test")
	require.NoError(t, err)

	rev, err := st.Rev(ctx, KindMachine)
	require.NoError(t, err)
	require.Equal(t, int64(2), rev)

	res, err := Apply(ctx, st, ap, KindMachine, "test", false, func(Op) {})
	require.NoError(t, err)
	require.Equal(t, 2, res.Add)
	require.Equal(t, rev, ap.revs[KindMachine])

	// Require every op appears in fake's log before stamp.
	stampEntry := "stamp machine 2"
	stampIdx := -1
	for i, entry := range ap.log {
		if entry == stampEntry {
			stampIdx = i
			break
		}
	}
	require.NotEqual(t, -1, stampIdx, "stamp entry not found in fake's log")
	require.Equal(t, len(ap.log)-1, stampIdx, "stamp must be the last entry in fake's log")

	// Ensure all ops precede the stamp.
	for i := 0; i < stampIdx; i++ {
		require.True(t, strings.HasPrefix(ap.log[i], "add machine "), "expected op before stamp, got: %s", ap.log[i])
	}

	// Part 2: Refused op leaves ap.revs unset and writes no stamp.
	stRefused := NewMem()
	apRefused := newFake()
	_, err = stRefused.Insert(ctx, KindMachine, m1, "test")
	require.NoError(t, err)
	_, err = stRefused.Insert(ctx, KindMachine, m2, "test")
	require.NoError(t, err)

	apRefused.refuse["m2"] = errors.New("write refused by applier")

	_, err = Apply(ctx, stRefused, apRefused, KindMachine, "test", false, func(Op) {})
	require.Error(t, err)
	require.Equal(t, int64(0), apRefused.revs[KindMachine], "a refused op must leave ap.revs unset")

	for _, entry := range apRefused.log {
		require.False(t, strings.HasPrefix(entry, "stamp"), "stamp must not be written when an op is refused")
	}
}

// TestHistoryCoversEveryWrite pins HistoryCoversEveryWrite invariant (tla/ConfigApply.tla line 344).
// Invariant: every change of a row has one history row (store.go; SPEC-CONFIG History):
//   HistoryCoversEveryWrite == mRows = FoldM /\ fRows = FoldF
// Reversed witness: Broken = "nohistory" (tla/ConfigApply.tla line 71).
// This test requires that Mem records a history row with Kind and Name for every
// Insert, Update, and Delete, and that a refused write (duplicate name, missing name)
// adds none.
func TestHistoryCoversEveryWrite(t *testing.T) {
	t.Parallel()
	m := NewMem()
	ctx := context.Background()

	// Initial history should be empty.
	require.Empty(t, m.history)

	// 1. Insert creates one history entry with Kind and Name.
	m1 := Row{Name: "m1", Fields: map[string]string{"user": "glenn", "seat": "studio", "slots": "64", "runners": "0"}}
	id1, err := m.Insert(ctx, KindMachine, m1, "test")
	require.NoError(t, err)
	require.Len(t, m.history, 1)
	require.Equal(t, id1, m.history[0].ID)
	require.Equal(t, KindMachine, m.history[0].Kind)
	require.Equal(t, "m1", m.history[0].Name)
	require.Equal(t, OpAdd, m.history[0].Op)
	require.Equal(t, "64", m.history[0].After["slots"])
	require.Nil(t, m.history[0].Before)

	// 2. Refused write (duplicate name) adds no history row.
	_, err = m.Insert(ctx, KindMachine, m1, "test")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrExists) || Refused(err))
	require.Len(t, m.history, 1, "refused duplicate insert must not add a history row")

	// 3. Update creates one history entry with Kind and Name.
	_, id2, err := m.Update(ctx, KindMachine, "m1", map[string]string{"slots": "128"}, "test")
	require.NoError(t, err)
	require.Len(t, m.history, 2)
	require.Equal(t, id2, m.history[1].ID)
	require.Equal(t, KindMachine, m.history[1].Kind)
	require.Equal(t, "m1", m.history[1].Name)
	require.Equal(t, OpSet, m.history[1].Op)
	require.Equal(t, "64", m.history[1].Before["slots"])
	require.Equal(t, "128", m.history[1].After["slots"])

	// 4. Refused write (missing name on update) adds no history row.
	_, _, err = m.Update(ctx, KindMachine, "nonexistent", map[string]string{"slots": "256"}, "test")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound) || Refused(err))
	require.Len(t, m.history, 2, "refused update of missing row must not add a history row")

	// 5. Delete creates one history entry with Kind and Name.
	id3, err := m.Delete(ctx, KindMachine, "m1", "test")
	require.NoError(t, err)
	require.Len(t, m.history, 3)
	require.Equal(t, id3, m.history[2].ID)
	require.Equal(t, KindMachine, m.history[2].Kind)
	require.Equal(t, "m1", m.history[2].Name)
	require.Equal(t, OpRemove, m.history[2].Op)
	require.Equal(t, "128", m.history[2].Before["slots"])
	require.Nil(t, m.history[2].After)

	// 6. Refused write (missing name on delete) adds no history row.
	_, err = m.Delete(ctx, KindMachine, "nonexistent", "test")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound) || Refused(err))
	require.Len(t, m.history, 3, "refused delete of missing row must not add a history row")
}
