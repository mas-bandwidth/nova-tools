package store

import (
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RejoinMembers receives every positive-width inventory member, including those
// with no retained record. Its input is not one ReadSet: Backend caps each read
// at LimitReadSetMembers. The existing rig supplies the actual Mem limit and a
// removed control card to restore, a placed card to leave, and missing records.
func TestFleetRejoinAcceptsInventoryPastOneReadSet(t *testing.T) {
	t.Parallel()
	for _, count := range []int{ntable.LimitReadSetMembers, ntable.LimitReadSetMembers + 1} {
		t.Run(strconv.Itoa(count)+" members", func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 1}))
			h.must(FleetStep(sprint.FleetReq{
				Op: "sync", Who: "tester", Sync: []sprint.SyncMember{{Name: "m2", Width: 1}}, Machines: []string{"m2"},
			}))
			// the cleanup keeps the inventory's members and deletes m1's row
			dropped, err := h.st.DropMembers(h.ctx, []string{"m2"})
			require.NoError(t, err)
			require.Equal(t, []string{"m1"}, dropped)
			before := h.table()
			require.False(t, before.Fleet.HasRow("m1"))
			require.Nil(t, before.MemberCtl("m1"))
			require.NotNil(t, before.MemberCtl("m2"))
			placedRevision := before.MemberCtl("m2").Rev

			members := make([]string, count)
			for i := range members {
				members[i] = "m" + strconv.Itoa(i+1)
				require.True(t, sprint.ValidID(members[i]), "valid inventory name")
			}
			rejoined, err := h.st.RejoinMembers(h.ctx, members)
			require.NoError(t, err, "%d valid inventory members must be read in bounded chunks", count)
			assert.Equal(t, []string{"m1"}, rejoined, "only the removed control record rejoins")
			after := h.table()
			assert.Len(t, after.Fleet.Rows(), 2, "missing records must not create phantom members")
			require.NotNil(t, after.MemberCtl("m1"))
			assert.Equal(t, sprint.CtlID("m1"), after.MemberCtl("m1").ID, "the same control identity returns")
			assert.Equal(t, sprint.HeldBySync, after.MemberCtl("m1").F(sprint.FieldHeldBy), "rejoin leaves release to the following step")
			require.NotNil(t, after.MemberCtl("m2"))
			assert.Equal(t, placedRevision, after.MemberCtl("m2").Rev, "an already placed member is unchanged")
		})
	}
}
