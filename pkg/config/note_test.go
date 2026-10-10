package config

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The note column of the route row and the machine row (Glenn 2026-10-02:
// "your choices, these should be saved somewhere permanent with notes
// (ideally, nova-config)"): free text, empty by default, the last field of
// the row, carried in the history row like every other field, shown whole by
// show and cut to one line by the list; and the rule that a disabled route
// carries its reason.

const sayWhy = "say why: --note '<the measured reason>'"

func TestRouteAndMachineRowsCarryANoteAsTheirLastField(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{KindRoute, KindMachine} {
		k, _ := Lookup(kind)
		f, ok := k.Field("note")
		require.True(t, ok, "%s has a note field", kind)
		assert.Equal(t, TypeText, f.Type, kind)
		assert.False(t, f.Required, "%s: add never requires a note", kind)
		assert.Equal(t, "note", k.FieldNames()[len(k.Fields)-1], "%s: the note is the last field, so no line moves", kind)
		assert.NotEmpty(t, f.Help, kind)
	}
	for _, kind := range []string{KindFriend, KindLoop, KindFleet, KindSprint, KindTier} {
		k, _ := Lookup(kind)
		_, ok := k.Field("note")
		assert.False(t, ok, "%s has no note: the two kinds an operator decides about carry one", kind)
	}
	k, _ := Lookup(KindRoute)
	row, err := k.NewRow("r1", proRoute("p"))
	require.NoError(t, err)
	assert.Equal(t, "", row.Fields["note"], "empty by default")
	row, err = k.NewRow("r1", map[string]string{"tier": "pro", "provider": "p", "model": "m", "deadline": "60", "note": "  held for cost  "})
	require.NoError(t, err)
	assert.Equal(t, "held for cost", row.Fields["note"], "one line of free text, trimmed")
	_, err = k.NewRow("r1", map[string]string{"tier": "pro", "provider": "p", "model": "m", "deadline": "60", "note": "two\nlines"})
	require.ErrorContains(t, err, "--note: want one line")
}

func TestADisabledRouteCarriesItsReason(t *testing.T) {
	t.Parallel()

	k, _ := Lookup(KindRoute)

	// set --enabled false with no --note names no reason, before any store
	_, err := k.Changes(map[string]string{"enabled": "false"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), sayWhy)
	_, err = k.Changes(map[string]string{"enabled": "false", "note": " "})
	require.Error(t, err, "a blank note is no reason")
	assert.Contains(t, err.Error(), sayWhy)
	got, err := k.Changes(map[string]string{"enabled": "false", "note": "4 of 52 ok"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"enabled": "false", "note": "4 of 52 ok"}, got)
	got, err = k.Changes(map[string]string{"enabled": "true"})
	require.NoError(t, err, "enabling needs no note")
	assert.Equal(t, map[string]string{"enabled": "true"}, got)

	// add --enabled false is the same rule
	disabled := proRoute("p")
	disabled["enabled"] = "false"
	_, err = k.NewRow("r1", disabled)
	require.Error(t, err)
	assert.Contains(t, err.Error(), sayWhy)
	disabled["note"] = "not yet measured"
	row, err := k.NewRow("r1", disabled)
	require.NoError(t, err)
	assert.Equal(t, "not yet measured", row.Fields["note"])
}

func TestAStoreRefusesADisabledRouteWithNoReasonAndKeepsTheRuleOnClear(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	k, _ := Lookup(KindRoute)
	row, err := k.NewRow("r1", proRoute("p"))
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindRoute, row, "t")
	require.NoError(t, err)

	// a row a write would leave disabled with an empty note is refused by the
	// store itself, whoever calls it
	_, _, err = st.Update(ctx, KindRoute, "r1", map[string]string{"enabled": "false"}, "t")
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), sayWhy)
	_, _, err = st.Update(ctx, KindRoute, "r1", map[string]string{"enabled": "false", "note": "36 of 62 ok"}, "t")
	require.NoError(t, err)

	// the reason is not cleared out from under a disabled route
	_, _, err = st.Update(ctx, KindRoute, "r1", map[string]string{"note": ""}, "t")
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), sayWhy)
	got, _, _ := st.Get(ctx, KindRoute, "r1")
	assert.Equal(t, "36 of 62 ok", got.Fields["note"], "the refused clear changed nothing")

	// enabling again leaves the note to be cleared or kept
	_, _, err = st.Update(ctx, KindRoute, "r1", map[string]string{"enabled": "true", "note": ""}, "t")
	require.NoError(t, err)

	// the dry run refuses from the same checks
	_, err = PlanWrite(ctx, st, OpSet, KindRoute, Row{Name: "r1"}, map[string]string{"enabled": "false"})
	require.ErrorIs(t, err, ErrInvalid)
}

func TestTheNoteIsInTheHistoryRowLikeEveryOtherField(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	machine, _ := Lookup(KindMachine)
	m, err := machine.NewRow("superman", map[string]string{"user": "u", "seat": "s", "slots": "8", "width": "8"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindMachine, m, "rowan")
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindMachine, "superman", map[string]string{"note": "held 1:46 PM"}, "rowan")
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindMachine, "superman", map[string]string{"note": ""}, "stella")
	require.NoError(t, err)

	hist, err := st.History(ctx, KindMachine, "superman")
	require.NoError(t, err)
	require.Len(t, hist, 3)
	assert.Equal(t, "", hist[0].After["note"], "an add records the empty note")
	assert.Equal(t, "", hist[1].Before["note"])
	assert.Equal(t, "held 1:46 PM", hist[1].After["note"])
	assert.Equal(t, "rowan", hist[1].Actor, "who wrote the note")
	assert.NotEmpty(t, hist[1].At, "and when")
	assert.Equal(t, "held 1:46 PM", hist[2].Before["note"])
	assert.Equal(t, "", hist[2].After["note"])
	assert.Equal(t, "stella", hist[2].Actor)
	assert.Contains(t, HistoryLine(hist[1]), `note=->held\x201:46\x20PM`)
	assert.Contains(t, HistoryLine(hist[2]), `note=held\x201:46\x20PM>-`)
}

func TestTheListCutsANoteToOneLineAndShowKeepsItWhole(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("slow on superman, ", 20) + "END"
	for _, kind := range []string{KindRoute, KindMachine} {
		k, _ := Lookup(kind)
		row := Row{Name: "x1", Fields: map[string]string{"note": long}}
		list := ListLine(k, row)
		show := ShowLine(k, row)
		assert.Contains(t, show, Value(long), "%s: show carries the whole note", kind)
		assert.NotContains(t, list, "END", "%s: list cuts it", kind)
		assert.NotContains(t, list, "\n", kind)
		assert.Contains(t, list, "note="+Value(long[:ListNoteRunes])+"...", "%s: cut at %d runes and marked", kind, ListNoteRunes)
		assert.Equal(t, len(strings.Fields(RowLine(k, row))), len(strings.Fields(list)), "%s: still one token per field", kind)
	}
	k, _ := Lookup(KindRoute)
	short := Row{Name: "x1", Fields: map[string]string{"note": "short one"}}
	assert.Equal(t, RowLine(k, short), ListLine(k, short), "a note under the cut is printed as it is")
	assert.Equal(t, 0, strings.Count(ListLine(k, Row{Name: "x1", Fields: map[string]string{"note": "é" + strings.Repeat("ü", 200)}}), "�"), "the cut is on a rune, never inside one")
}
