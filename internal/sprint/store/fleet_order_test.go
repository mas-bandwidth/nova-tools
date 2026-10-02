package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// fleetRowOrder is the fleet table's rows as the store holds them.
func (h *harness) fleetRowOrder() []string {
	h.t.Helper()
	shapes, err := h.m.Shapes(h.ctx, []string{h.st.Names.Table(sprint.Fleet)})
	require.NoError(h.t, err)
	require.Len(h.t, shapes, 1)
	var out []string
	for _, r := range shapes[0].Rows {
		out = append(out, r.Key)
	}
	return out
}

// The owner, 2026-10-01: "Please sort the fleet table such that we sort first
// alphabetically by machine name (as is current), then stable sort by status,
// such that "up" is first, then "held" then "down"". Members in all three
// states: the up ones first by name, then the held, then the down; a member
// that comes up moves to the up ones at the next tick.
func TestTheFleetRowsAreUpThenHeldThenDownEachByName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setLive("b", "c", "d", "e")
	h.beat()
	for _, m := range []string{"e", "d", "c", "b", "a"} {
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m, Width: 2}))
	}
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "c"}))
	h.startMachine()
	h.machine()
	s := h.snap()
	require.Equal(t, sprint.Down, s.MemberCtl("a").F("status"), "a never beats: down")
	assert.Equal(t, []string{"b", "d", "e", "c", "a"}, h.fleetRowOrder(), "up by name, then held, then down")
	h.setLive("a", "b", "c", "d", "e")
	h.tick(1)
	h.machine()
	assert.Equal(t, []string{"a", "b", "d", "e", "c"}, h.fleetRowOrder(), "a beats and comes up: it joins the up rows by name")
}

// FleetOrder: by name, then stably by status (up, held, down, anything else last).
func TestFleetOrderIsByStatusThenName(t *testing.T) {
	t.Parallel()
	got := FleetOrder([]string{"z", "m", "a", "k", "q"}, map[string]string{"z": sprint.Up, "m": sprint.Down, "a": sprint.Held, "k": sprint.Up, "q": "-"})
	assert.Equal(t, []string{"k", "z", "a", "m", "q"}, got)
}
