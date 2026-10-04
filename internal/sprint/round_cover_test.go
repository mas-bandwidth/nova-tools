package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// round_cover_test.go covers the rewrite of the deal's and the ask's rolling
// indexes after a plan drops a unit (round.go, tla/SprintEvents.tla dcur and
// acur): what rewriteRounds makes of a plan's index writes. Every case is in
// memory: the index and its writes are the core's own, so no store, clock,
// subprocess or socket is reached.

// TestRoundCoverRewriteRounds pins rewriteRounds: after a unit is dropped,
// each index the plan moved stands at the placements and pass-overs of the
// units kept and never at the dropped one's, guarded on the value the step
// read; an index no kept unit moved writes nothing at all; a property the plan
// writes that is no index stands untouched.
func TestRoundCoverRewriteRounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		read    string     // the deal_index the step read ("" is no property)
		moves   roundMoves // what each planned unit moved the index past
		keep    []string   // the unit keys the plan keeps after the drop
		planned string     // the index as the step planned it over every unit
		value   string     // the index after the rewrite ("" is no write at all)
	}{
		{
			name:    "the dropped unit's placement is gone and the kept one's stands",
			read:    "",
			moves:   roundMoves{"c1": "m1", "c2": "m2"},
			keep:    []string{"c1"},
			planned: "2",
			value:   "1",
		},
		{
			name:    "the rewrite is guarded on the value the step read",
			read:    "5",
			moves:   roundMoves{"c1": "m1", "c2": "m2"},
			keep:    []string{"c1"},
			planned: "8",
			value:   "7",
		},
		{
			name:    "a kept unit moves the index past each name it passed over",
			read:    "",
			moves:   roundMoves{"c1": "m1,m2", "c2": "m3"},
			keep:    []string{"c1"},
			planned: "3",
			value:   "2",
		},
		{
			name:    "no unit kept moved the index: the write is refused",
			read:    "",
			moves:   roundMoves{"c1": "m1,m2", "c2": "m3"},
			keep:    nil,
			planned: "3",
			value:   "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// planDeal is the step before the drop: the fleet table's deal
			// index over m1,m2,m3 read at tc.read, both units kept, the
			// index written with them through the step's own seam
			// (roundWrites), beside one property no index owns.
			fleet := NewTable(Fleet)
			if tc.read != "" {
				fleet.SetProps(map[string]string{PropDealIndex: tc.read})
			}
			p := &Plan{
				Units: []Unit{{Key: "c1"}, {Key: "c2"}},
				Props: []PropWrite{{Table: Merge, Name: "cur", Value: "x"}},
			}
			roundWrites(p, tableRound(fleet, PropDealIndex, []string{"m1", "m2", "m3"}), tc.moves)
			other := PropWrite{Table: Merge, Name: "cur", Value: "x"}
			guard := PropWrite{Table: Fleet, Name: PropDealIndex, Was: tc.read, WasAbsent: tc.read == ""}
			guard.Value = tc.planned
			require.Equal(t, []PropWrite{other, guard}, p.Props, "the step planned its index over every unit")
			// the drop: LeaveQueued's doing (queue.go) — the units left
			// move no index, so the writes are made again.
			var keep []Unit
			for _, k := range tc.keep {
				keep = append(keep, Unit{Key: k})
			}
			p.Units = keep
			rewriteRounds(p)
			want := []PropWrite{other}
			if tc.value != "" {
				guard.Value = tc.value
				want = append(want, guard)
			}
			assert.Equal(t, want, p.Props, "the plan's writes after the rewrite: %+v", p.Props)
		})
	}
}
