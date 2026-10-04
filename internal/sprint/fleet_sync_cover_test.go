package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFleetSyncCoverHeldInInventory: HeldInInventory names the members of want
// the coordinator holds down and refuses the rest. Its main path is the
// coordinator's hold, returned sorted by name; its refusal is a member the sync
// held (the sync's own mark, which the sync leaves), a member no one holds, and
// a name with no control card at all. A hold outside want is not that sync's to
// report.
func TestFleetSyncCoverHeldInInventory(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		build func(t *testing.T) *Snapshot
		want  []SyncMember
		held  []string
	}{
		{
			name: "the coordinators hold is named",
			build: func(t *testing.T) *Snapshot {
				w := newWorld(t, "reader-a")
				w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
				w.must(FleetStep(w.s, FleetReq{Op: "hold", Member: "m1", Who: "coordinator"}))
				return w.s
			},
			want: []SyncMember{{Name: "m1", Width: DefaultWidth}},
			held: []string{"m1"},
		},
		{
			name: "the syncs hold is left alone",
			build: func(t *testing.T) *Snapshot {
				w := newWorld(t, "reader-a")
				w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
				w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
				w.must(FleetStep(w.s, FleetReq{Op: "sync", Who: "sync", Sync: []SyncMember{{Name: "m1", Width: DefaultWidth}}, Machines: []string{"m1", "m2"}}))
				return w.s
			},
			want: []SyncMember{{Name: "m1", Width: DefaultWidth}, {Name: "m2", Width: DefaultWidth}},
			held: nil,
		},
		{
			name: "a member no one holds is not named",
			build: func(t *testing.T) *Snapshot {
				w := newWorld(t, "reader-a")
				w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
				return w.s
			},
			want: []SyncMember{{Name: "m1", Width: DefaultWidth}},
			held: nil,
		},
		{
			name: "a name with no control card is not named",
			build: func(t *testing.T) *Snapshot {
				return newWorld(t, "reader-a").s
			},
			want: []SyncMember{{Name: "ghost", Width: DefaultWidth}},
			held: nil,
		},
		{
			name: "holds are named sorted, not in the want order",
			build: func(t *testing.T) *Snapshot {
				w := newWorld(t, "reader-a")
				w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
				w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
				w.must(FleetStep(w.s, FleetReq{Op: "hold", Member: "m2", Who: "coordinator"}))
				w.must(FleetStep(w.s, FleetReq{Op: "hold", Member: "m1", Who: "coordinator"}))
				return w.s
			},
			want: []SyncMember{{Name: "m2", Width: DefaultWidth}, {Name: "m1", Width: DefaultWidth}},
			held: []string{"m1", "m2"},
		},
		{
			name: "a hold outside want is not named",
			build: func(t *testing.T) *Snapshot {
				w := newWorld(t, "reader-a")
				w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
				w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
				w.must(FleetStep(w.s, FleetReq{Op: "hold", Member: "m1", Who: "coordinator"}))
				return w.s
			},
			want: []SyncMember{{Name: "m2", Width: DefaultWidth}},
			held: nil,
		},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.held, HeldInInventory(c.build(t), c.want))
		})
	}
}
