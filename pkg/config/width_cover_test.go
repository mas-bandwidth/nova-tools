package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWidthCoverOfFindsTheNamedMachine covers WidthOf's main path
// (pkg/config/width.go:60): from the widths Widths read, the machine
// named is the one row returned, found, carrying its own width and default
// flag and no other row's, wherever in the list it stands.
func TestWidthCoverOfFindsTheNamedMachine(t *testing.T) {
	t.Parallel()
	ws := []MachineWidth{
		{Machine: "m1", Width: 8},
		{Machine: "m2", Width: 4},
		{Machine: "m3", Default: true},
		{Machine: "m4", Width: 2},
	}
	cases := []struct {
		name string
		ask  string
		want MachineWidth
	}{
		{"first row of the list", "m1", MachineWidth{Machine: "m1", Width: 8}},
		{"middle row of the list", "m2", MachineWidth{Machine: "m2", Width: 4}},
		{"row of the default width", "m3", MachineWidth{Machine: "m3", Default: true}},
		{"last row of the list", "m4", MachineWidth{Machine: "m4", Width: 2}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, found := WidthOf(ws, tc.ask)
			assert.True(t, found, "%s: not found in %+v", tc.ask, ws)
			assert.Equal(t, tc.want, got, "%s: got %+v", tc.ask, got)
		})
	}
}

// TestWidthCoverOfRefusesAnUnknownName covers WidthOf's refusal
// (pkg/config/width.go:60): no row of the name, and no list at all,
// answer found false with the zero MachineWidth, never a neighbour's row
// and never a guess.
func TestWidthCoverOfRefusesAnUnknownName(t *testing.T) {
	t.Parallel()
	ws := []MachineWidth{
		{Machine: "m1", Width: 8},
		{Machine: "m2", Width: 4},
	}
	got, found := WidthOf(ws, "m9")
	assert.False(t, found, "m9: found in %+v", ws)
	assert.Equal(t, MachineWidth{}, got, "m9: %+v", got)

	got, found = WidthOf(nil, "m1")
	assert.False(t, found, "no list: found m1")
	assert.Equal(t, MachineWidth{}, got, "no list: %+v", got)
}
