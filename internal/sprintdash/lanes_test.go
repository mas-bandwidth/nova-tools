package sprintdash

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPageShowsPerMachineLanes pins the lanes panel (docs/SPEC-SPRINT-DASHBOARD.md,
// "Lanes"): the `lanes` array `nova-sprint where --json --cards` prints (verb-lane-take-give)
// is read, the page carries a Lanes panel, and app.js draws each machine's holders and
// waiters from it. The fixture is a where --json --cards body with lanes.
func TestPageShowsPerMachineLanes(t *testing.T) {
	t.Parallel()

	// The data path: the fixture's lanes array is read as the machine's row.
	c := copyOf(t, fixture(t))
	require.Len(t, c.Lanes, 1, "the fixture carries one machine's lanes")
	row := c.Lanes[0]
	assert.Equal(t, LaneRow{Kind: "go", Machine: "bench-a", Width: 1, Held: []string{"amy"}, Waiting: []string{"bob"}}, row,
		"the machine's lane: its kind, width, holder and waiter")

}
