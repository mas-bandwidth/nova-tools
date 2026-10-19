package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWidthsCarryTheMachineRowsGatesOnly: a machine row's gates_only (native
// Windows members that take only the cards whose GATE-NEEDS names windows) reaches nova-sprint
// fleet sync through Widths as its comma list; a row that names none carries nil.
func TestWidthsCarryTheMachineRowsGatesOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m := NewMem()
	k, _ := Lookup(KindMachine)
	gates, ok := k.Field("gates_only")
	require.True(t, ok, "the machine row has a gates_only field")
	for _, r := range []struct{ name, gates string }{{"win1", "windows"}, {"lin1", ""}} {
		_, err := m.Insert(ctx, KindMachine, Row{Name: r.name, Fields: map[string]string{"user": "u", "seat": "s", "slots": "8", "runners": "0", "gates_only": r.gates}}, "t")
		require.NoError(t, err)
	}
	got := widthsByName(t, m)
	assert.Equal(t, []string{"windows"}, got["win1"].GatesOnly, "win1's member takes only windows cards")
	assert.Nil(t, got["lin1"].GatesOnly, "a row with no gates_only is a member dealt any card")
	assert.Equal(t, TypeNames, gates.Type, "gates_only is a comma list of words")
}

// TestGateWordsTrimsAndDropsEmpties: the comma list as the field holds it.
func TestGateWordsTrimsAndDropsEmpties(t *testing.T) {
	t.Parallel()

	assert.Nil(t, gateWords(""))
	assert.Equal(t, []string{"windows", "xeon"}, gateWords(" windows, ,xeon "))
}
