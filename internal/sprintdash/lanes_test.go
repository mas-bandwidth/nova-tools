package sprintdash

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// The copy preserves lane data for clients while the dashboard omits the panel.
func TestPageOmitsLanesPanelButKeepsLaneData(t *testing.T) {
	t.Parallel()
	c := copyOf(t, fixture(t))
	require.Len(t, c.Lanes, 1)
	assert.Equal(t, LaneRow{Kind: "go", Machine: "bench-a", Width: 1, Held: []string{"amy"}, Waiting: []string{"bob"}}, c.Lanes[0])
	assert.NotContains(t, string(file("index.html")), `id="lanes"`)
	assert.NotContains(t, string(file("app.js")), "renderLanes")
}
