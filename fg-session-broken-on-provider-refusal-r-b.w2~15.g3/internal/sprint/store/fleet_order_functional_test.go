//go:build functional

package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The fleet's rows by status on a real store (Redis.RowsOrder, the table
// layer's set with row_order): up by name, then held, then down, and a standing
// sort on the table is ended by the same call, never refused (SORTED).
func TestRedisTheFleetRowsAreUpThenHeldThenDown(t *testing.T) {
	t.Parallel()
	h, c := liveHarness(t)
	h.setLive("b", "c", "d", "e")
	h.beat()
	for _, m := range []string{"e", "d", "c", "b", "a"} {
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m, Width: 2}))
	}
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "c"}))
	fleet := h.st.Names.Table(sprint.Fleet)
	_, err := ntable.Set(h.ctx, c, fleet, ntable.SetOpts{RowSort: &ntable.Sort{By: "name", Keep: true}})
	require.NoError(t, err, "a standing sort by name, as a coordinator may set by hand")
	order := func() []string {
		t.Helper()
		shapes, err := h.st.B.Shapes(h.ctx, []string{fleet})
		require.NoError(t, err)
		require.Len(t, shapes, 1)
		var out []string
		for _, r := range shapes[0].Rows {
			out = append(out, r.Key)
		}
		return out
	}
	h.startMachine()
	h.machine()
	assert.Equal(t, []string{"b", "d", "e", "c", "a"}, order(), "up by name, then held, then down")
	h.setLive("a", "b", "c", "d", "e")
	h.tick(1)
	h.machine()
	assert.Equal(t, []string{"a", "b", "d", "e", "c"}, order(), "a comes up and joins the up rows by name")
}
