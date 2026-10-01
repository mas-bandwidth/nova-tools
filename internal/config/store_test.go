package config

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storeTests is the contract every Store keeps, run against Mem here and
// against Postgres in pg_functional_test.go, so the fake is held to the real
// one's refusals (docs/CONTRIBUTING.md: a fake is strict like the real tool).
func storeTests(t *testing.T, open func(t *testing.T) Store) {
	t.Helper()
	ctx := context.Background()
	friend, _ := Lookup(KindFriend)
	machine, _ := Lookup(KindMachine)
	mk := func(k *Kind, name string, raw map[string]string) Row {
		t.Helper()
		row, err := k.NewRow(name, raw)
		require.NoError(t, err)
		return row
	}

	t.Run("machines and fleet come from one read", func(t *testing.T) {
		t.Parallel()
		st := open(t)
		machines, fleet, err := st.MachinesAndFleet(ctx)
		require.False(t, err != nil || len(machines) != 0 || fleet.Fields["store"] != "" || fleet.Fields["coordinator"] != "", "empty store: %+v %+v %v", machines, fleet, err)
		for _, n := range []string{"bench-b", "bench-a"} {
			{
				_, err := st.Insert(ctx, KindMachine, mk(machine, n, map[string]string{"user": "user-x", "seat": "seat-x", "slots": "4"}), "operator")
				require.NoError(t, err)
			}
		}
		{
			_, _, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": "bench-b", "coordinator": "bench-a"}, "operator")
			require.NoError(t, err)
		}
		machines, fleet, err = st.MachinesAndFleet(ctx)
		require.False(t, err != nil || len(machines) != 2 || machines[0].Name != "bench-a" || machines[1].Name != "bench-b" || machines[0].Fields["slots"] != "4", "machines: %+v %v", machines, err)
		require.False(t, fleet.Fields["store"] != "bench-b" || fleet.Fields["coordinator"] != "bench-a", "fleet row: %+v", fleet)
		listed, _ := st.List(ctx, KindMachine)
		require.False(t, len(listed) != len(machines), "List sees %d machines, MachinesAndFleet %d", len(listed), len(machines))
	})

	t.Run("add, get, list, set, history, remove", func(t *testing.T) {
		t.Parallel()
		st := open(t)
		id, err := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"}), "rowan")
		require.False(t, err != nil || id != 1, "add machine: id %d err %v", id, err)
		id, err = st.Insert(ctx, KindFriend, mk(friend, "rowan", map[string]string{"slots": "32", "tiers": "frontier,pro", "roles": "builder"}), "rowan")
		require.False(t, err != nil || id != 2, "add friend: id %d err %v", id, err)
		row, found, err := st.Get(ctx, KindFriend, "rowan")
		require.False(t, err != nil || !found || row.Fields["slots"] != "32" || row.Fields["tiers"] != "frontier,pro" || row.Fields["roles"] != "builder" || row.CreatedAt == "" || row.UpdatedAt == "", "get: %+v %v %v", row, found, err)
		{
			_, found, err := st.Get(ctx, KindFriend, "nobody")
			require.False(t, err != nil || found, "get nobody: %v %v", found, err)
		}
		rows, err := st.List(ctx, KindFriend)
		require.False(t, err != nil || len(rows) != 1 || rows[0].Name != "rowan", "list: %+v %v", rows, err)
		after, id, err := st.Update(ctx, KindFriend, "rowan", map[string]string{"slots": "64", "roles": "builder,reader"}, "stella")
		require.False(t, err != nil || id != 3 || after.Fields["slots"] != "64" || after.Fields["roles"] != "builder,reader" || after.Fields["tiers"] != "frontier,pro", "set: %+v id %d err %v", after, id, err)
		rev, err := st.Rev(ctx, KindFriend)
		require.False(t, err != nil || rev != 3, "rev friend: %d %v", rev, err)
		rev, err = st.Rev(ctx, KindMachine)
		require.False(t, err != nil || rev != 1, "rev machine: %d %v", rev, err)
		{
			rev, err := st.Rev(ctx, "nothing")
			require.False(t, err != nil || rev != 0, "rev of an unknown kind: %d %v", rev, err)
		}
		counts, err := st.Counts(ctx)
		require.False(t, err != nil || counts[KindFriend] != 1 || counts[KindMachine] != 1, "counts %v %v", counts, err)
		for _, one := range []string{KindFleet, KindSprint} {
			{
				_, counted := counts[one]
				require.False(t, counted, "counts %v: %s is one row and is not counted", counts, one)
			}
		}
		id, err = st.Delete(ctx, KindFriend, "rowan", "rowan")
		require.False(t, err != nil || id != 4, "remove: id %d err %v", id, err)
		{
			_, found, _ := st.Get(ctx, KindFriend, "rowan")
			require.False(t, found, "removed row is still there")
		}
		hist, err := st.History(ctx, KindFriend, "rowan")
		require.False(t, err != nil || len(hist) != 3, "history: %+v %v", hist, err)
		assert.False(t, hist[0].Op != OpAdd || hist[0].Before != nil || hist[0].After["slots"] != "32" || hist[0].Actor != "rowan" || hist[0].At == "" || hist[0].ID != 2, "history add row %+v", hist[0])
		assert.False(t, hist[1].Op != OpSet || hist[1].Before["slots"] != "32" || hist[1].After["slots"] != "64" || hist[1].After["roles"] != "builder,reader" || hist[1].Actor != "stella", "history set row %+v", hist[1])
		assert.False(t, hist[2].Op != OpRemove || hist[2].Before["slots"] != "64" || hist[2].After != nil, "history remove row %+v", hist[2])
		{
			hist, err := st.History(ctx, KindFriend, "nobody")
			assert.False(t, err != nil || len(hist) != 0, "history of nobody: %v %v", hist, err)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		t.Parallel()
		st := open(t)
		refusal := func(err error, want error, detail string) {
			t.Helper()
			require.Error(t, err, "accepted; want a refusal %v saying %q", want, detail)
			require.False(t, !Refused(err) || !errors.Is(err, want), "error %v is not the refusal %v", err, want)
			require.False(t, !strings.Contains(err.Error(), detail), "refusal %q does not say %q", err, detail)
		}
		{
			_, err := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"}), "rowan")
			require.NoError(t, err)
		}
		_, err := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"}), "rowan")
		refusal(err, ErrExists, "machine studio exists")
		{
			_, err := st.Insert(ctx, KindFriend, mk(friend, "rowan", map[string]string{"slots": "1", "tiers": "frontier"}), "rowan")
			require.NoError(t, err)
		}
		_, err = st.Insert(ctx, KindFriend, mk(friend, "rowan", map[string]string{"slots": "1", "tiers": "frontier"}), "rowan")
		refusal(err, ErrExists, "friend rowan exists")
		_, _, err = st.Update(ctx, KindFriend, "nobody", map[string]string{"slots": "2"}, "rowan")
		refusal(err, ErrNotFound, "friend nobody not found")
		_, _, err = st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "nobody"}, "rowan")
		refusal(err, ErrNoRef, "--coordinator nobody names no friend row")
		{
			_, _, err := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "rowan"}, "rowan")
			require.NoError(t, err)
		}
		_, err = st.Delete(ctx, KindFriend, "rowan", "rowan")
		refusal(err, ErrReferenced, "friend rowan is the --coordinator of the sprint")
		_, err = st.Delete(ctx, KindFriend, "nobody", "rowan")
		refusal(err, ErrNotFound, "friend nobody not found")
		// A refused write leaves no history and moves no revision.
		{
			rev, _ := st.Rev(ctx, KindFriend)
			require.False(t, rev != 2, "rev after refusals %d, want 2", rev)
		}
		{
			hist, _ := st.History(ctx, KindFriend, "nobody")
			require.False(t, len(hist) != 0, "a refused write left history: %+v", hist)
		}
		// The handover frees the old coordinator's row.
		{
			_, _, err := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": ""}, "rowan")
			require.NoError(t, err)
		}
		{
			_, err := st.Delete(ctx, KindFriend, "rowan", "rowan")
			require.NoError(t, err, "remove the freed friend: %v", err)
		}
	})

	t.Run("the fleet row", func(t *testing.T) {
		t.Parallel()
		st := open(t)
		// The row is there before anything is set, both fields empty, and
		// has no history yet: migrate made it, nobody added it.
		row, found, err := st.Get(ctx, KindFleet, KindFleet)
		require.False(t, err != nil || !found || row.Fields["store"] != "" || row.Fields["coordinator"] != "" || row.CreatedAt == "", "fresh fleet row: %+v %v %v", row, found, err)
		{
			hist, err := st.History(ctx, KindFleet, KindFleet)
			require.False(t, err != nil || len(hist) != 0, "fresh fleet history %v %v", hist, err)
		}
		{
			rev, err := st.Rev(ctx, KindFleet)
			require.False(t, err != nil || rev != 0, "fresh fleet rev %d %v", rev, err)
		}
		rows, err := st.List(ctx, KindFleet)
		require.False(t, err != nil || len(rows) != 1 || rows[0].Name != KindFleet, "list fleet: %+v %v", rows, err)
		// A store or coordinator must be a machine row.
		_, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": "space"}, "rowan")
		require.False(t, err == nil || !errors.Is(err, ErrNoRef) || !strings.Contains(err.Error(), "--store space names no machine row"), "store naming no machine: %v", err)
		{
			_, err := st.Insert(ctx, KindMachine, mk(machine, "space", map[string]string{"user": "nova", "seat": "space", "slots": "0"}), "rowan")
			require.NoError(t, err)
		}
		{
			_, err := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"}), "rowan")
			require.NoError(t, err)
		}
		after, id, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": "space", "coordinator": "studio"}, "rowan")
		require.False(t, err != nil || id != 3 || after.Fields["store"] != "space" || after.Fields["coordinator"] != "studio", "set the fleet: %+v id %d err %v", after.Fields, id, err)
		{
			rev, _ := st.Rev(ctx, KindFleet)
			require.False(t, rev != 3, "fleet rev %d, want 3", rev)
		}
		// A machine the fleet names cannot be removed (a foreign key; the
		// tool names it).
		_, err = st.Delete(ctx, KindMachine, "space", "rowan")
		require.False(t, err == nil || !errors.Is(err, ErrReferenced) || !strings.Contains(err.Error(), "machine space is the --store of the fleet"), "remove the store machine: %v", err)
		// Clearing a field is an empty value; the machine is then free.
		after, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": ""}, "rowan")
		require.False(t, err != nil || after.Fields["store"] != "" || after.Fields["coordinator"] != "studio", "clear the store: %+v %v", after.Fields, err)
		{
			_, err := st.Delete(ctx, KindMachine, "space", "rowan")
			require.NoError(t, err, "remove the freed machine: %v", err)
		}
		hist, err := st.History(ctx, KindFleet, KindFleet)
		require.False(t, err != nil || len(hist) != 2 || hist[0].Op != OpSet || hist[0].Before["store"] != "" || hist[0].After["store"] != "space" || hist[1].After["store"] != "", "fleet history %+v %v", hist, err)
	})
}

func TestMemStoreKeepsTheContract(t *testing.T) {
	t.Parallel()
	storeTests(t, func(t *testing.T) Store { return NewMem() })
}

func TestMemStoreHandsOutCopies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	machine, _ := Lookup(KindMachine)
	row, _ := machine.NewRow("studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"})
	{
		_, err := st.Insert(ctx, KindMachine, row, "rowan")
		require.NoError(t, err)
	}
	row.Fields["slots"] = "1"
	got, _, _ := st.Get(ctx, KindMachine, "studio")
	require.False(t, got.Fields["slots"] != "64", "the store shares its row with the caller")
	got.Fields["slots"] = "2"
	again, _, _ := st.Get(ctx, KindMachine, "studio")
	require.False(t, again.Fields["slots"] != "64", "a row read from the store is the store's own")
}
