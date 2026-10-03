package config

import (
	"context"
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

	t.Run("add, get, list, set, history, remove", func(t *testing.T) {
		t.Parallel()
		st := open(t)
		id, err := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"}), "rowan")
		assertionMsg54 := []any{"add machine: id %d err %v", id, err}
		require.NoError(t, err, assertionMsg54...)
		require.Equal(t, int64(1), id, assertionMsg54...)
		id, err = st.Insert(ctx, KindFriend, mk(friend, "rowan", map[string]string{"slots": "32", "tiers": "frontier,pro", "roles": "builder"}), "rowan")
		assertionMsg56 := []any{"add friend: id %d err %v", id, err}
		require.NoError(t, err, assertionMsg56...)
		require.Equal(t, int64(2), id, assertionMsg56...)
		row, found, err := st.Get(ctx, KindFriend, "rowan")
		assertionMsg58 := []any{"get: %+v %v %v", row, found, err}
		require.NoError(t, err, assertionMsg58...)
		require.True(t, found, assertionMsg58...)
		require.Equal(t, "32", row.Fields["slots"], assertionMsg58...)
		require.Equal(t, "frontier,pro", row.Fields["tiers"], assertionMsg58...)
		require.Equal(t, "builder", row.Fields["roles"], assertionMsg58...)
		require.NotEqual(t, "", row.CreatedAt, assertionMsg58...)
		require.NotEqual(t, "", row.UpdatedAt, assertionMsg58...)
		{
			_, found, err := st.Get(ctx, KindFriend, "nobody")
			assertionMsg61 := []any{"get nobody: %v %v", found, err}
			require.NoError(t, err, assertionMsg61...)
			require.False(t, found, assertionMsg61...)
		}
		rows, err := st.List(ctx, KindFriend)
		assertionMsg64 := []any{"list: %+v %v", rows, err}
		require.NoError(t, err, assertionMsg64...)
		require.Len(t, rows, 1, assertionMsg64...)
		require.Equal(t, "rowan", rows[0].Name, assertionMsg64...)
		after, id, err := st.Update(ctx, KindFriend, "rowan", map[string]string{"slots": "64", "roles": "builder,reader"}, "stella")
		assertionMsg66 := []any{"set: %+v id %d err %v", after, id, err}
		require.NoError(t, err, assertionMsg66...)
		require.Equal(t, int64(3), id, assertionMsg66...)
		require.Equal(t, "64", after.Fields["slots"], assertionMsg66...)
		require.Equal(t, "builder,reader", after.Fields["roles"], assertionMsg66...)
		require.Equal(t, "frontier,pro", after.Fields["tiers"], assertionMsg66...)
		rev, err := st.Rev(ctx, KindFriend)
		assertionMsg68 := []any{"rev friend: %d %v", rev, err}
		require.NoError(t, err, assertionMsg68...)
		require.Equal(t, int64(3), rev, assertionMsg68...)
		rev, err = st.Rev(ctx, KindMachine)
		assertionMsg70 := []any{"rev machine: %d %v", rev, err}
		require.NoError(t, err, assertionMsg70...)
		require.Equal(t, int64(1), rev, assertionMsg70...)
		{
			rev, err := st.Rev(ctx, "nothing")
			assertionMsg73 := []any{"rev of an unknown kind: %d %v", rev, err}
			require.NoError(t, err, assertionMsg73...)
			require.Equal(t, int64(0), rev, assertionMsg73...)
		}
		counts, err := st.Counts(ctx)
		assertionMsg76 := []any{"counts %v %v", counts, err}
		require.NoError(t, err, assertionMsg76...)
		require.Equal(t, 1, counts[KindFriend], assertionMsg76...)
		require.Equal(t, 1, counts[KindMachine], assertionMsg76...)
		for _, one := range []string{KindFleet, KindSprint} {
			_, scopedCounted113 := counts[one]
			require.False(t, scopedCounted113, "counts %v: %s is one row and is not counted", counts, one)
		}
		id, err = st.Delete(ctx, KindFriend, "rowan", "rowan")
		assertionMsg84 := []any{"remove: id %d err %v", id, err}
		require.NoError(t, err, assertionMsg84...)
		require.Equal(t, int64(4), id, assertionMsg84...)
		_, scopedFound122, _ := st.Get(ctx, KindFriend, "rowan")
		require.False(t, scopedFound122, "removed row is still there")
		hist, err := st.History(ctx, KindFriend, "rowan")
		assertionMsg90 := []any{"history: %+v %v", hist, err}
		require.NoError(t, err, assertionMsg90...)
		require.Len(t, hist, 3, assertionMsg90...)
		assertionMsg136 := []any{"history add row %+v", hist[0]}
		func() {
			if !assert.Equal(t, OpAdd, hist[0].Op, assertionMsg136...) {
				return
			}
			if !assert.Nil(t, hist[0].Before, assertionMsg136...) {
				return
			}
			if !assert.Equal(t, "32", hist[0].After["slots"], assertionMsg136...) {
				return
			}
			if !assert.Equal(t, "rowan", hist[0].Actor, assertionMsg136...) {
				return
			}
			if !assert.NotEqual(t, "", hist[0].At, assertionMsg136...) {
				return
			}
			assert.Equal(t, int64(2), hist[0].ID, assertionMsg136...)
		}()
		assertionMsg137 := []any{"history set row %+v", hist[1]}
		func() {
			if !assert.Equal(t, OpSet, hist[1].Op, assertionMsg137...) {
				return
			}
			if !assert.Equal(t, "32", hist[1].Before["slots"], assertionMsg137...) {
				return
			}
			if !assert.Equal(t, "64", hist[1].After["slots"], assertionMsg137...) {
				return
			}
			if !assert.Equal(t, "builder,reader", hist[1].After["roles"], assertionMsg137...) {
				return
			}
			assert.Equal(t, "stella", hist[1].Actor, assertionMsg137...)
		}()
		assertionMsg138 := []any{"history remove row %+v", hist[2]}
		func() {
			if !assert.Equal(t, OpRemove, hist[2].Op, assertionMsg138...) {
				return
			}
			if !assert.Equal(t, "64", hist[2].Before["slots"], assertionMsg138...) {
				return
			}
			assert.Nil(t, hist[2].After, assertionMsg138...)
		}()
		{
			hist, err := st.History(ctx, KindFriend, "nobody")
			assertionMsg141 := []any{"history of nobody: %v %v", hist, err}
			func() {
				if !assert.NoError(t, err, assertionMsg141...) {
					return
				}
				assert.Empty(t, hist, assertionMsg141...)
			}()
		}
	})

	t.Run("refusals", func(t *testing.T) {
		t.Parallel()
		st := open(t)
		refusal := func(err error, want error, detail string) {
			t.Helper()
			require.Error(t, err, "accepted; want a refusal %v saying %q", want, detail)
			assertionMsg106 := []any{"error %v is not the refusal %v", err, want}
			require.True(t, Refused(err), assertionMsg106...)
			require.ErrorIs(t, err, want, assertionMsg106...)
			require.ErrorContains(t, err, detail, "refusal %q does not say %q", err, detail)
		}
		_, setupErr8221 := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"}), "rowan")
		require.NoError(t, setupErr8221)
		_, err := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"}), "rowan")
		refusal(err, ErrExists, "machine studio exists")
		_, setupErr8590 := st.Insert(ctx, KindFriend, mk(friend, "rowan", map[string]string{"slots": "1", "tiers": "frontier"}), "rowan")
		require.NoError(t, setupErr8590)
		_, err = st.Insert(ctx, KindFriend, mk(friend, "rowan", map[string]string{"slots": "1", "tiers": "frontier"}), "rowan")
		refusal(err, ErrExists, "friend rowan exists")
		_, _, err = st.Update(ctx, KindFriend, "nobody", map[string]string{"slots": "2"}, "rowan")
		refusal(err, ErrNotFound, "friend nobody not found")
		_, _, err = st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "nobody"}, "rowan")
		refusal(err, ErrNoRef, "--coordinator nobody names no friend row")
		_, _, setupErr9243 := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "rowan"}, "rowan")
		require.NoError(t, setupErr9243)
		_, err = st.Delete(ctx, KindFriend, "rowan", "rowan")
		refusal(err, ErrReferenced, "friend rowan is the --coordinator of the sprint")
		_, err = st.Delete(ctx, KindFriend, "nobody", "rowan")
		refusal(err, ErrNotFound, "friend nobody not found")
		// A refused write leaves no history and moves no revision.
		scopedRev217, _ := st.Rev(ctx, KindFriend)
		require.Equal(t, int64(2), scopedRev217, "rev after refusals %d, want 2", scopedRev217)
		scopedHist221, _ := st.History(ctx, KindFriend, "nobody")
		require.Empty(t, scopedHist221, "a refused write left history: %+v", scopedHist221)
		// The handover frees the old coordinator's row.
		_, _, setupErr9994 := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": ""}, "rowan")
		require.NoError(t, setupErr9994)
		_, scopedErr228 := st.Delete(ctx, KindFriend, "rowan", "rowan")
		require.NoError(t, scopedErr228, "remove the freed friend: %v", scopedErr228)
	})

	t.Run("the fleet row", func(t *testing.T) {
		t.Parallel()
		st := open(t)
		// The row is there before anything is set, with both endpoints unset, and
		// has no history yet: migrate made it, nobody added it.
		row, found, err := st.Get(ctx, KindFleet, KindFleet)
		assertionMsg159 := []any{"fresh fleet row: %+v %v %v", row, found, err}
		require.NoError(t, err, assertionMsg159...)
		require.True(t, found, assertionMsg159...)
		require.Equal(t, "", row.Fields["store"], assertionMsg159...)
		require.Equal(t, "", row.Fields["coordinator"], assertionMsg159...)
		require.Empty(t, row.Fields["redis_port"], assertionMsg159...)
		require.Equal(t, "", row.Fields["pg_dsn"], assertionMsg159...)
		require.NotEqual(t, "", row.CreatedAt, assertionMsg159...)
		{
			hist, err := st.History(ctx, KindFleet, KindFleet)
			assertionMsg162 := []any{"fresh fleet history %v %v", hist, err}
			require.NoError(t, err, assertionMsg162...)
			require.Empty(t, hist, assertionMsg162...)
		}
		{
			rev, err := st.Rev(ctx, KindFleet)
			assertionMsg166 := []any{"fresh fleet rev %d %v", rev, err}
			require.NoError(t, err, assertionMsg166...)
			require.Equal(t, int64(0), rev, assertionMsg166...)
		}
		rows, err := st.List(ctx, KindFleet)
		assertionMsg169 := []any{"list fleet: %+v %v", rows, err}
		require.NoError(t, err, assertionMsg169...)
		require.Len(t, rows, 1, assertionMsg169...)
		require.Equal(t, KindFleet, rows[0].Name, assertionMsg169...)
		// The migration seeds loops_dir; Mem and a file have no SQL seed, so
		// the test writes it before a set whose refusal is about another field.
		_, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"loops_dir": seededLoopsDir}, "rowan")
		require.NoError(t, err)
		// A store or coordinator must be a machine row.
		_, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": "space"}, "rowan")
		assertionMsg172 := []any{"store naming no machine: %v", err}
		require.Error(t, err, assertionMsg172...)
		require.ErrorIs(t, err, ErrNoRef, assertionMsg172...)
		require.ErrorContains(t, err, "--store space names no machine row", assertionMsg172...)
		_, setupErr11974 := st.Insert(ctx, KindMachine, mk(machine, "space", map[string]string{"user": "nova", "seat": "space", "slots": "0"}), "rowan")
		require.NoError(t, setupErr11974)
		_, setupErr12147 := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"}), "rowan")
		require.NoError(t, setupErr12147)
		for _, changes := range []map[string]string{
			{"redis_port": "65536"},
			{"pg_dsn": "postgres://user:do-not-print@localhost:5432/nova"},
			{"pg_dsn": "postgres://user@localhost:5432/nova?password=do-not-print;sslmode=disable"},
			{"pg_dsn": "postgres://user@localhost:5432/nova?password=do-not-print%zz"},
			{"pg_dsn": "postgres://user@localhost:5432/nova?%70aSsWoRd=do-not-print"},
		} {
			_, _, err = st.Update(ctx, KindFleet, KindFleet, changes, "rowan")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "do-not-print")
		}
		rev, err := st.Rev(ctx, KindFleet)
		require.NoError(t, err)
		require.Equal(t, int64(1), rev, "a refused endpoint update wrote history")
		after, id, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": "space", "coordinator": "studio"}, "rowan")
		assertionMsg182 := []any{"set the fleet: %+v id %d err %v", after.Fields, id, err}
		require.NoError(t, err, assertionMsg182...)
		require.Equal(t, int64(4), id, assertionMsg182...)
		require.Equal(t, "space", after.Fields["store"], assertionMsg182...)
		require.Equal(t, "studio", after.Fields["coordinator"], assertionMsg182...)
		scopedRev279, _ := st.Rev(ctx, KindFleet)
		require.Equal(t, int64(4), scopedRev279, "fleet rev %d, want 4", scopedRev279)
		// A machine the fleet names cannot be removed (a foreign key; the
		// tool names it).
		_, err = st.Delete(ctx, KindMachine, "space", "rowan")
		assertionMsg190 := []any{"remove the store machine: %v", err}
		require.Error(t, err, assertionMsg190...)
		require.ErrorIs(t, err, ErrReferenced, assertionMsg190...)
		require.ErrorContains(t, err, "machine space is the --store of the fleet", assertionMsg190...)
		// Clearing a field is an empty value; the machine is then free.
		after, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": ""}, "rowan")
		assertionMsg193 := []any{"clear the store: %+v %v", after.Fields, err}
		require.NoError(t, err, assertionMsg193...)
		require.Equal(t, "", after.Fields["store"], assertionMsg193...)
		require.Equal(t, "studio", after.Fields["coordinator"], assertionMsg193...)
		_, scopedErr296 := st.Delete(ctx, KindMachine, "space", "rowan")
		require.NoError(t, scopedErr296, "remove the freed machine: %v", scopedErr296)
		hist, err := st.History(ctx, KindFleet, KindFleet)
		assertionMsg199 := []any{"fleet history %+v %v", hist, err}
		require.NoError(t, err, assertionMsg199...)
		require.Len(t, hist, 3, assertionMsg199...)
		require.Equal(t, OpSet, hist[1].Op, assertionMsg199...)
		require.Equal(t, "", hist[1].Before["store"], assertionMsg199...)
		require.Equal(t, "space", hist[1].After["store"], assertionMsg199...)
		require.Equal(t, "", hist[2].After["store"], assertionMsg199...)
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
	_, setupErr14718 := st.Insert(ctx, KindMachine, row, "rowan")
	require.NoError(t, setupErr14718)
	row.Fields["slots"] = "1"
	got, _, _ := st.Get(ctx, KindMachine, "studio")
	require.Equal(t, "64", got.Fields["slots"], "the store shares its row with the caller")
	got.Fields["slots"] = "2"
	again, _, _ := st.Get(ctx, KindMachine, "studio")
	require.Equal(t, "64", again.Fields["slots"], "a row read from the store is the store's own")
}
