//go:build functional

package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A write of the rows' order alone (the fleet's order by status, orderFleet) changes
// no record, so the twin catches up across it from the change stream; on 2026-10-04 the
// live server read the fleet table whole (~2,800 records) after each one. A set that
// changes more than the order (its footer: the reversed witness) still reads it whole.
func TestRedisAnOrderOnlySetNamesNoRecordsAndLeavesNoGap(t *testing.T) {
	t.Parallel()
	h, c := liveHarness(t)
	for _, m := range []string{"b", "a"} {
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m, Width: 2}))
	}
	pinned, err := h.st.Pinned(h.ctx)
	require.NoError(t, err)
	r := pinned.B.(*Redis)
	fleet := h.st.Names.Table(sprint.Fleet)
	rev := func() uint64 {
		t.Helper()
		shapes, err := r.Shapes(h.ctx, []string{fleet})
		require.NoError(t, err)
		require.Len(t, shapes, 1)
		return shapes[0].Revision
	}
	before := rev()
	require.NoError(t, r.RowsOrder(h.ctx, fleet, []string{"b", "a"}))
	require.NoError(t, r.RowsOrder(h.ctx, fleet, []string{"a", "b"}))
	after := rev()
	require.Greater(t, after, before)
	ids, ok, err := r.TableChanges(h.ctx, fleet, before, after)
	require.NoError(t, err)
	assert.True(t, ok, "two order-only sets leave no gap")
	assert.Empty(t, ids, "and name no record")

	footer := "the fleet"
	_, err = ntable.Set(h.ctx, c, fleet, ntable.SetOpts{Footer: &footer}, r.writeOpts())
	require.NoError(t, err)
	_, ok, err = r.TableChanges(h.ctx, fleet, before, rev())
	assert.False(t, ok, "a set that changes the footer is read whole")
	var gap *GapError
	require.ErrorAs(t, err, &gap)
	assert.Contains(t, gap.Why, `a write "set"`)
}
