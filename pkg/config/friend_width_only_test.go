package config

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// errOldCeiling is the refusal the removed per-machine friend ceiling
// answered with (6aa7e9445c, 2026-09-26; removed 2026-10-13). It lives on
// only in this test's reversed witness.
var errOldCeiling = errors.New("old friend ceiling")

// ceilingApplier is the reversed witness: the fake Redis with the removed
// ns_capacity_desired rule put back, a friend charged to the fleet's
// coordinator machine refused when her number exceeds that machine's slots.
// The same apply that the design takes is refused by it, so the setup below
// would see a ceiling if one came back.
type ceilingApplier struct {
	*fakeApplier
	machineSlots map[string]int
	coordinator  string
}

func (c *ceilingApplier) Write(ctx context.Context, kind string, row Row, prev View, actor, idem string) error {
	switch kind {
	case KindMachine:
		c.machineSlots[row.Name] = row.Int("slots")
	case KindFleet:
		c.coordinator = row.Fields["coordinator"]
	case KindFriend:
		if w := row.Int("width"); w > c.machineSlots[c.coordinator] {
			return &RefusedError{Err: errOldCeiling, Detail: fmt.Sprintf("CEILING %s: friend %s makes the sum %d over the machine ceiling %d", c.coordinator, row.Name, w, c.machineSlots[c.coordinator])}
		}
	}
	return c.fakeApplier.Write(ctx, kind, row, prev, actor, idem)
}

// TestAFriendRowIsWidthOnlyAndNoCeilingRefusesIt: a friend's capacity is her
// width and nothing else (the owner, 2026-10-13: "there is only width in
// nova-config."). The friend kind has
// no slots field, Check takes width 16 with no slots, and apply writes the
// friend at width 16 though the only machine has one slot, with no refusal.
// The reversed witness (the old ceiling put back in the applier) refuses the
// same apply (tla/ConfigApply.tla, FriendWidthNeverRefused and its
// "friendceiling" witness).
func TestAFriendRowIsWidthOnlyAndNoCeilingRefusesIt(t *testing.T) {
	t.Parallel()

	friend, ok := Lookup(KindFriend)
	require.True(t, ok)
	for _, f := range friend.Fields {
		require.NotEqual(t, "slots", f.Name, "the friend kind has a slots field; a friend row is width, tiers, roles and the rest")
	}
	_, found := friend.Field("slots")
	require.False(t, found, "friend.Field(slots) found a field")

	row := Row{Name: "stella", Fields: map[string]string{"width": "16", "tiers": "frontier"}}
	require.NoError(t, checkFriend(row), "checkFriend refused width 16 with no slots")
	_, err := friend.NewRow("stella", map[string]string{"width": "16", "tiers": "frontier", "slots": "8"})
	require.ErrorContains(t, err, "--slots is not a friend field", "a friend row still takes --slots")
	for _, w := range []int{1, 16, 64, 1000} {
		_, err := friend.NewRow("stella", map[string]string{"width": strconv.Itoa(w), "tiers": "frontier"})
		require.NoError(t, err, "friend set --width %d was refused", w)
	}
	_, err = friend.NewRow("stella", map[string]string{"width": "0", "tiers": "frontier"})
	require.Error(t, err, "width 0 was taken: a friend's width is at least 1")

	build := func(t *testing.T) *Mem {
		t.Helper()
		ctx := context.Background()
		st := NewMem()
		machine, _ := Lookup(KindMachine)
		m, err := machine.NewRow("m", map[string]string{"user": "u", "seat": "m", "slots": "1"})
		require.NoError(t, err)
		_, err = st.Insert(ctx, KindMachine, m, "rowan")
		require.NoError(t, err)
		_, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": "m", "redis_port": "6380", "pg_dsn": "postgres://nova_config@localhost:5432/nova"}, "rowan")
		require.NoError(t, err)
		x, err := friend.NewRow("x", map[string]string{"width": "16", "tiers": "frontier,pro", "roles": "builder"})
		require.NoError(t, err)
		_, err = st.Insert(ctx, KindFriend, x, "rowan")
		require.NoError(t, err)
		return st
	}
	run := func(st *Mem, ap Applier) error {
		for _, kind := range []string{KindMachine, KindFleet, KindFriend} {
			if _, err := Apply(context.Background(), st, ap, kind, "rowan", false, func(Op) {}); err != nil {
				return err
			}
		}
		return nil
	}

	t.Run("the design applies width 16 on a one-slot machine", func(t *testing.T) {
		t.Parallel()
		ap := newFake()
		require.NoError(t, run(build(t), ap), "apply refused a friend of width 16 on a machine of one slot")
		require.Equal(t, "16", ap.views[KindFriend]["x"]["width"], "friend x was written at width %q", ap.views[KindFriend]["x"]["width"])
		require.Equal(t, "1", ap.views[KindMachine]["m"]["slots"], "machine m was written with slots %q", ap.views[KindMachine]["m"]["slots"])
		_, has := ap.views[KindFriend]["x"]["slots"]
		require.False(t, has, "the friend view carries slots: %v", ap.views[KindFriend]["x"])
	})

	t.Run("a desired hash whose slots are not its width is planned as a width set", func(t *testing.T) {
		t.Parallel()
		// A hash written while slots was the friend's own field: Redis says
		// width 8 and slots 32. The view reads no width, so the plan sets the
		// row's width and writeFriend rewrites the slots from it.
		require.Equal(t, "", friendWidthView("8", "32"), "a width whose slots disagree was read as a width")
		require.Equal(t, "8", friendWidthView("8", "8"))
		row := Row{Name: "stella", Fields: map[string]string{"width": "8", "tiers": "frontier"}}
		ops := Plan(friend, []Row{row}, map[string]View{"stella": {"width": friendWidthView("8", "32"), "tiers": "frontier"}})
		require.Len(t, ops, 1, "plan %+v", ops)
		require.Equal(t, OpSet, ops[0].Op)
		require.Contains(t, ops[0].Changed, "width", "plan %+v", ops)
	})

	t.Run("reversed witness: the old ceiling refuses the same apply", func(t *testing.T) {
		t.Parallel()
		ap := &ceilingApplier{fakeApplier: newFake(), machineSlots: map[string]int{}}
		err := run(build(t), ap)
		require.ErrorIs(t, err, errOldCeiling, "the old ceiling took a friend of width 16 on one slot: the witness is not red")
		require.ErrorContains(t, err, "CEILING m: friend x makes the sum 16 over the machine ceiling 1")
	})
}
