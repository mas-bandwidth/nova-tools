package store

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// RejoinMembers reads the control cards of an inventory longer than one read
// set takes (ntable.LimitReadSetMembers) in read sets of at most that many, and
// places again the one a sync removed.
func TestRejoinReadsALongInventoryInPages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.must(FleetStep(sprint.FleetReq{Op: "sync", Who: "tester", Sync: []sprint.SyncMember{{Name: "m1", Width: 4}}, Machines: []string{"m1"}}))
	require.Nil(t, h.snap().MemberCtl("m2"), "the sync took m2's control card off")
	names := []string{"m2"}
	for i := 0; len(names) <= ntable.LimitReadSetMembers+10; i++ {
		names = append(names, fmt.Sprintf("x%04d", i))
	}
	got, err := h.st.RejoinMembers(h.ctx, names)
	require.NoError(t, err)
	assert.Equal(t, []string{"m2"}, got)
	assert.NotNil(t, h.snap().MemberCtl("m2"), "m2's control card is placed again")
}
