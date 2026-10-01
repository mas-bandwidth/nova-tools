package config

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// hostsFake is a HostReader over a map: where each friend's beat says she runs.
type hostsFake map[string]string

func (h hostsFake) FriendHosts(_ context.Context, names []string) (map[string]string, error) {
	out := map[string]string{}
	for _, n := range names {
		out[n] = h[n]
	}
	return out, nil
}

type hostsErr struct{}

func (hostsErr) FriendHosts(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("store down")
}

func seedWidths(t *testing.T) *Mem {
	t.Helper()
	ctx := context.Background()
	m := NewMem()
	for _, r := range []struct {
		name  string
		slots string
	}{{"m1", "8"}, {"m2", "4"}, {"m3", "0"}, {"m4", "2"}} {
		_, setupErr763 := m.Insert(ctx, KindMachine, Row{Name: r.name, Fields: map[string]string{"user": "u", "seat": "s", "slots": r.slots, "runners": "0"}}, "t")
		require.NoError(t, setupErr763)
	}
	return m
}

func addFriend(t *testing.T, m *Mem, name, slots string) {
	t.Helper()
	_, setupErr1036 := m.Insert(context.Background(), KindFriend, Row{Name: name, Fields: map[string]string{"slots": slots, "tiers": "flash", "roles": "builder"}}, "t")
	require.NoError(t, setupErr1036)
}

// TestWidthIsSlotsLessTheFriendsChargedThere: decision 1, the width of the
// sprint's member is the machine's ceiling less the desired slots of the
// friends charged to it, and a machine with no room is no member.
func TestWidthIsSlotsLessTheFriendsChargedThere(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := seedWidths(t)
	// no friend: the width is the ceiling, and no Redis is needed
	ws, err := Widths(ctx, m, nil)
	require.NoError(t, err)
	got := map[string]MachineWidth{}
	for _, w := range ws {
		got[w.Machine] = w
	}
	require.Equal(t, 8, got["m1"].Width, "no friends: %+v", ws)
	require.True(t, got["m1"].Member(), "m1 has a positive width")
	require.Zero(t, got["m3"].Width, "no friends: %+v", ws)
	require.False(t, got["m3"].Member(), "m3 has no width")
	for i := 1; i < len(ws); i++ {
		require.Less(t, ws[i-1].Machine, ws[i].Machine, "widths not in name order: %+v", ws)
	}

	// two friends on m1 by their beats, one on m4 taking all of it, one with
	// slots 0 that is charged nowhere
	addFriend(t, m, "f1", "3")
	addFriend(t, m, "f2", "2")
	addFriend(t, m, "f3", "2")
	addFriend(t, m, "f4", "0")
	ws, err = Widths(ctx, m, hostsFake{"f1": "m1", "f2": "m1", "f3": "m4"})
	require.NoError(t, err)
	got = map[string]MachineWidth{}
	for _, w := range ws {
		got[w.Machine] = w
	}
	{
		w := got["m1"]
		assertionMsg86 := []any{"m1: %+v", w}
		require.Equal(t, 8, w.Slots, assertionMsg86...)
		require.Equal(t, 5, w.Charged, assertionMsg86...)
		require.Equal(t, 3, w.Width, assertionMsg86...)
		require.True(t, w.Member(), assertionMsg86...)
	}
	{
		w := got["m2"]
		assertionMsg90 := []any{"m2: %+v", w}
		require.Equal(t, 0, w.Charged, assertionMsg90...)
		require.Equal(t, 4, w.Width, assertionMsg90...)
	}
	scopedW96 := got["m4"]
	require.Equal(t, 2, scopedW96.Charged, "m4 is full of its friend: %+v", scopedW96)
	require.Zero(t, scopedW96.Width, "m4 has no remaining width: %+v", scopedW96)
	require.False(t, scopedW96.Member(), "m4 is no member: %+v", scopedW96)
}

// TestAFriendWithNoBeatIsChargedToTheCoordinatorMachine: the default charge
// is the one apply makes (RedisApplier.charge), and with no coordinator
// machine either the width refuses naming the fix.
func TestAFriendWithNoBeatIsChargedToTheCoordinatorMachine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := seedWidths(t)
	addFriend(t, m, "f1", "3")
	{
		_, err := Widths(ctx, m, hostsFake{})
		assertionMsg108 := []any{"no beat, no coordinator: %v", err}
		require.Error(t, err, assertionMsg108...)
		require.ErrorContains(t, err, "nova-config fleet set --coordinator", assertionMsg108...)
	}
	_, _, setupErr3798 := m.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": "m2"}, "t")
	require.NoError(t, setupErr3798)
	ws, err := Widths(ctx, m, hostsFake{})
	require.NoError(t, err)
	{
		w, _ := WidthOf(ws, "m2")
		assertionMsg118 := []any{"m2 carries the friend: %+v", w}
		require.Equal(t, 3, w.Charged, assertionMsg118...)
		require.Equal(t, 1, w.Width, assertionMsg118...)
	}
}

// TestWidthsNeedARedisOnlyWhenAFriendCarriesSlots: a store with friends and
// no host reader refuses (never a silently wrong width), and a host reader
// that fails is the error.
func TestWidthsNeedARedisOnlyWhenAFriendCarriesSlots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := seedWidths(t)
	addFriend(t, m, "f1", "0")
	_, scopedErr138 := Widths(ctx, m, nil)
	require.NoError(t, scopedErr138, "a friend with no slots needs no Redis: %v", scopedErr138)
	addFriend(t, m, "f2", "1")
	{
		_, err := Widths(ctx, m, nil)
		assertionMsg137 := []any{"no host reader: %v", err}
		require.Error(t, err, assertionMsg137...)
		require.ErrorContains(t, err, "Redis address is needed", assertionMsg137...)
	}
	{
		_, err := Widths(ctx, m, hostsErr{})
		assertionMsg141 := []any{"host reader down: %v", err}
		require.Error(t, err, assertionMsg141...)
		require.ErrorContains(t, err, "store down", assertionMsg141...)
	}
}

// TestWidthIsDerivedNotStored: the machine kind declares no width or member
// field; the width is always the rows' arithmetic (decision 1, no new field).
func TestWidthIsDerivedNotStored(t *testing.T) {
	t.Parallel()
	k, _ := Lookup(KindMachine)
	for _, f := range k.Fields {
		assertionMsg151 := []any{"the machine kind declares %s; the width is derived from slots and the friends' charges", f.Name}
		require.NotEqual(t, "width", f.Name, assertionMsg151...)
		require.NotEqual(t, "member", f.Name, assertionMsg151...)
	}
}
