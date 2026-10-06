package config

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHistoryCoversEveryWrite checks that an insert, an update and a delete
// record history ids 1, 2 and 3 with ops add, set and remove (tla/ConfigApply.tla).
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

// TestRedisIsACopy checks that every store row has a view and that the view
// equals the row (tla/ConfigApply.tla).
func TestRedisIsACopy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	put(t, st, KindMachine, "m1", machineRaw("64"))
	put(t, st, KindMachine, "m2", machineRaw("32"))
	ap := newFake()
	_, err := Apply(ctx, st, ap, KindMachine, "actor", false, func(Op) {})
	require.NoError(t, err)

	views, _, err := ap.Read(ctx, KindMachine)
	require.NoError(t, err)
	storeRows, err := st.List(ctx, KindMachine)
	require.NoError(t, err)
	require.NotEmpty(t, storeRows)
	require.Equal(t, len(storeRows), len(views))
	for _, row := range storeRows {
		view, ok := views[row.Name]
		require.True(t, ok, "missing view %s", row.Name)
		require.Equal(t, row.Fields, map[string]string(view))
	}
}

// TestConflictRefusesAhead checks that an apply returns a conflict and leaves
// the log empty when the stamp is ahead (tla/ConfigApply.tla).
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

// TestApplyOrder checks that a machine add or set precedes a friend add or
// set, and that a friend remove is followed only by removes (tla/ConfigApply.tla).
func TestApplyOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	put(t, st, KindMachine, "m1", machineRaw("64"))
	put(t, st, KindFriend, "f1", friendRaw("8"))
	put(t, st, KindFriend, "f2", friendRaw("8"))
	ap := newFake()
	applyInKindOrder(t, ctx, st, ap, KindMachine, KindFriend)

	machineAt := firstLine(ap.log, OpAdd+" "+KindMachine+" ", OpSet+" "+KindMachine+" ")
	friendAt := firstLine(ap.log, OpAdd+" "+KindFriend+" ", OpSet+" "+KindFriend+" ")
	require.NotEqual(t, -1, machineAt)
	require.NotEqual(t, -1, friendAt)
	require.Less(t, machineAt, friendAt)

	require.NoError(t, mustDelete(ctx, st, KindFriend, "f1"))
	require.NoError(t, mustDelete(ctx, st, KindFriend, "f2"))
	_, err := Apply(ctx, st, ap, KindFriend, "actor", false, func(Op) {})
	require.NoError(t, err)
	start := firstLine(ap.log, OpRemove+" "+KindFriend+" ")
	require.NotEqual(t, -1, start)
	removes := 0
	for _, line := range ap.log[start:] {
		if strings.HasPrefix(line, "stamp ") {
			break
		}
		require.True(t, strings.HasPrefix(line, OpRemove+" "), line)
		removes++
	}
	require.GreaterOrEqual(t, removes, 2)
}

// TestStampOnlyWhenComplete checks that the last log line is the stamp, that
// every op line comes before it, and that a refusal leaves the revision where
// it was (tla/ConfigApply.tla).
func TestStampOnlyWhenComplete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	put(t, st, KindMachine, "m1", machineRaw("64"))
	ap := newFake()
	_, err := Apply(ctx, st, ap, KindMachine, "actor", false, func(Op) {})
	require.NoError(t, err)
	rev, err := st.Rev(ctx, KindMachine)
	require.NoError(t, err)
	require.Greater(t, len(ap.log), 1)
	require.Equal(t, fmt.Sprintf("stamp %s %d", KindMachine, rev), ap.log[len(ap.log)-1])
	for _, line := range ap.log[:len(ap.log)-1] {
		require.True(t, isOpLine(line), line)
	}

	before := ap.revs[KindMachine]
	ap.refuse["m1"] = &RefusedError{Err: ErrCeiling, Detail: "refused"}
	_, _, err = st.Update(ctx, KindMachine, "m1", map[string]string{"slots": "32"}, "actor")
	require.NoError(t, err)
	_, err = Apply(ctx, st, ap, KindMachine, "actor", false, func(Op) {})
	require.Error(t, err)
	require.Equal(t, before, ap.revs[KindMachine])
}

// TestWitnessStampFirst checks that a stamp line is not written before the op
// lines (tla/ConfigApply.tla, stampfirst).
func TestWitnessStampFirst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	put(t, st, KindMachine, "m1", machineRaw("64"))
	ap := newFake()
	_, err := Apply(ctx, st, ap, KindMachine, "actor", false, func(Op) {})
	require.NoError(t, err)
	require.Greater(t, len(ap.log), 1)
	require.False(t, strings.HasPrefix(ap.log[0], "stamp "))
	for _, line := range ap.log[:len(ap.log)-1] {
		require.False(t, strings.HasPrefix(line, "stamp "), line)
	}
	require.True(t, strings.HasPrefix(ap.log[len(ap.log)-1], "stamp "))
}

// TestWitnessFriendFirst checks that a machine add or set precedes a friend
// add or set (tla/ConfigApply.tla, friendfirst).
func TestWitnessFriendFirst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	put(t, st, KindMachine, "m1", machineRaw("64"))
	put(t, st, KindFriend, "f1", friendRaw("8"))
	ap := newFake()
	applyInKindOrder(t, ctx, st, ap, KindMachine, KindFriend)
	machineAt := firstLine(ap.log, OpAdd+" "+KindMachine+" ", OpSet+" "+KindMachine+" ")
	friendAt := firstLine(ap.log, OpAdd+" "+KindFriend+" ", OpSet+" "+KindFriend+" ")
	require.NotEqual(t, -1, machineAt)
	require.NotEqual(t, -1, friendAt)
	require.Less(t, machineAt, friendAt)
	require.Contains(t, ap.log[machineAt], " "+KindMachine+" m1 ")
	require.Contains(t, ap.log[friendAt], " "+KindFriend+" f1 ")
}

// TestWitnessNoHistory checks that each written machine row equals the fold of
// its history, including a row whose last op is remove (tla/ConfigApply.tla, nohistory).
func TestWitnessNoHistory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	put(t, st, KindMachine, "m1", machineRaw("8"))
	_, _, err := st.Update(ctx, KindMachine, "m1", map[string]string{"slots": "9"}, "actor")
	require.NoError(t, err)
	require.NoError(t, mustDelete(ctx, st, KindMachine, "m1"))
	put(t, st, KindMachine, "m2", machineRaw("8"))

	h1, err := st.History(ctx, KindMachine, "m1")
	require.NoError(t, err)
	require.Equal(t, []string{OpAdd, OpSet, OpRemove}, opNames(h1))
	present, fields := foldHistory(h1)
	_, ok, err := st.Get(ctx, KindMachine, "m1")
	require.NoError(t, err)
	require.False(t, ok)
	require.False(t, present)
	require.Nil(t, fields)

	h2, err := st.History(ctx, KindMachine, "m2")
	require.NoError(t, err)
	require.Equal(t, []string{OpAdd}, opNames(h2))
	present, fields = foldHistory(h2)
	row, ok, err := st.Get(ctx, KindMachine, "m2")
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, present)
	require.Equal(t, row.Fields, fields)
}

// TestWitnessOverAhead checks that an apply writes nothing and returns a
// conflict when the stamp is ahead of a seeded revision (tla/ConfigApply.tla, overahead).
func TestWitnessOverAhead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	put(t, st, KindMachine, "m1", machineRaw("64"))
	rev, err := st.Rev(ctx, KindMachine)
	require.NoError(t, err)
	ap := newFake()
	ap.revs[KindMachine] = rev + 1
	_, err = Apply(ctx, st, ap, KindMachine, "actor", false, func(Op) {})
	require.Error(t, err)
	require.True(t, IsConflict(err))
	require.Empty(t, ap.log)
	require.Equal(t, rev+1, ap.revs[KindMachine])
}

func put(t *testing.T, st *Mem, kind, name string, raw map[string]string) {
	t.Helper()
	k, ok := Lookup(kind)
	require.True(t, ok)
	row, err := k.NewRow(name, raw)
	require.NoError(t, err)
	_, err = st.Insert(context.Background(), kind, row, "actor")
	require.NoError(t, err)
}

func machineRaw(slots string) map[string]string {
	return map[string]string{"user": "u", "seat": "s", "slots": slots}
}

func friendRaw(slots string) map[string]string {
	return map[string]string{"slots": slots, "tiers": "flash"}
}

func mustDelete(ctx context.Context, st *Mem, kind, name string) error {
	_, err := st.Delete(ctx, kind, name, "actor")
	return err
}

func applyInKindOrder(t *testing.T, ctx context.Context, st *Mem, ap *fakeApplier, kinds ...string) {
	t.Helper()
	want := map[string]bool{}
	for _, kind := range kinds {
		want[kind] = true
	}
	for _, kind := range KindNames() {
		if !want[kind] {
			continue
		}
		_, err := Apply(ctx, st, ap, kind, "actor", false, func(Op) {})
		require.NoError(t, err)
		delete(want, kind)
	}
	require.Empty(t, want)
}

func firstLine(log []string, prefixes ...string) int {
	for i, line := range log {
		for _, prefix := range prefixes {
			if strings.HasPrefix(line, prefix) {
				return i
			}
		}
	}
	return -1
}

func isOpLine(line string) bool {
	return strings.HasPrefix(line, OpAdd+" ") || strings.HasPrefix(line, OpSet+" ") || strings.HasPrefix(line, OpRemove+" ")
}

func opNames(h []Change) []string {
	out := make([]string, len(h))
	for i, c := range h {
		out[i] = c.Op
	}
	return out
}

func foldHistory(h []Change) (present bool, fields map[string]string) {
	for _, c := range h {
		switch c.Op {
		case OpAdd, OpSet:
			present, fields = true, c.After
		case OpRemove:
			present, fields = false, nil
		}
	}
	return present, fields
}
