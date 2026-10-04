package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// seedWidths makes four machine rows whose slots are all 160 and whose widths
// differ, so a width read from the slots would be seen.
func seedWidths(t *testing.T) *Mem {
	t.Helper()
	ctx := context.Background()
	m := NewMem()
	for _, r := range []struct {
		name  string
		width string
	}{{"m1", "8"}, {"m2", "4"}, {"m3", "0"}, {"m4", "2"}} {
		_, setupErr763 := m.Insert(ctx, KindMachine, Row{Name: r.name, Fields: map[string]string{"user": "u", "seat": "s", "slots": "160", "runners": "0", "width": r.width}}, "t")
		require.NoError(t, setupErr763)
	}
	return m
}

func addFriend(t *testing.T, m *Mem, name, slots string) {
	t.Helper()
	_, setupErr1036 := m.Insert(context.Background(), KindFriend, Row{Name: name, Fields: map[string]string{"slots": slots, "tiers": "flash", "roles": "builder"}}, "t")
	require.NoError(t, setupErr1036)
}

func widthsByName(t *testing.T, m *Mem) map[string]MachineWidth {
	t.Helper()
	ws, err := Widths(context.Background(), m)
	require.NoError(t, err)
	for i := 1; i < len(ws); i++ {
		require.Less(t, ws[i-1].Machine, ws[i].Machine, "widths not in name order: %+v", ws)
	}
	got := map[string]MachineWidth{}
	for _, w := range ws {
		got[w.Machine] = w
	}
	return got
}

// TestWidthIsTheRowsWidthField: the width of the sprint's member is the
// machine row's width field as set, never its slots; width 0 is no member.
func TestWidthIsTheRowsWidthField(t *testing.T) {
	t.Parallel()
	got := widthsByName(t, seedWidths(t))
	require.Len(t, got, 4)
	for name, want := range map[string]int{"m1": 8, "m2": 4, "m3": 0, "m4": 2} {
		require.Equal(t, want, got[name].Width, "%s: %+v", name, got[name])
		require.Equal(t, want > 0, got[name].Member(), "%s: %+v", name, got[name])
	}
	require.Equal(t, "CONFIG WIDTH machine=m1 width=8 member=true", got["m1"].Line())
	require.Equal(t, "CONFIG WIDTH machine=m3 width=0 member=false", got["m3"].Line())
}

// TestSettingOneWidthChangesNoOther: a width set on one machine is that
// machine's width and moves no other machine's.
func TestSettingOneWidthChangesNoOther(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := seedWidths(t)
	before := widthsByName(t, m)
	_, _, err := m.Update(ctx, KindMachine, "m2", map[string]string{"width": "32"}, "t")
	require.NoError(t, err)
	after := widthsByName(t, m)
	require.Equal(t, 32, after["m2"].Width)
	for _, name := range []string{"m1", "m3", "m4"} {
		require.Equal(t, before[name], after[name], "setting m2's width moved %s", name)
	}
}

// TestAFriendRowAffectsNoWidth: friend rows carrying slots, a fleet row naming
// a coordinator machine, and no Redis at all: every width is still its row's.
func TestAFriendRowAffectsNoWidth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := seedWidths(t)
	before := widthsByName(t, m)
	for _, f := range []string{"f1", "f2", "f3", "f4"} {
		addFriend(t, m, f, "32")
	}
	_, _, err := m.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": "m1"}, "t")
	require.NoError(t, err)
	require.Equal(t, before, widthsByName(t, m), "a friend row changed a width")
}

// TestWidthDefaultsToTheDefaultWidth: a machine added with no width has the
// default width, half its cores, which fleet sync resolves: a member whose
// config holds no number.
func TestWidthDefaultsToTheDefaultWidth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	_, err := m.Insert(ctx, KindMachine, Row{Name: "m1", Fields: map[string]string{"user": "u", "seat": "s", "slots": "8"}}, "t")
	require.NoError(t, err)
	got := widthsByName(t, m)
	require.Zero(t, got["m1"].Width, "%+v", got["m1"])
	require.True(t, got["m1"].Default, "%+v", got["m1"])
	require.True(t, got["m1"].Member())
	require.Equal(t, "CONFIG WIDTH machine=m1 width=default member=true", got["m1"].Line())
}

// TestAnUnsetWidthIsUnsetInTheAppliedView: apply writes an unset width as an
// empty field, and reads it back unset, so a steady apply finds no difference
// and writes nothing (machineView; a "0" there would rewrite every machine on
// every apply).
func TestAnUnsetWidthIsUnsetInTheAppliedView(t *testing.T) {
	t.Parallel()
	require.Equal(t, "", machineView(map[string]string{"width": ""}, "8")["width"])
	require.Equal(t, "0", machineView(map[string]string{"width": "0"}, "8")["width"])
	require.Equal(t, "16", machineView(map[string]string{"width": "16"}, "8")["width"])
}
