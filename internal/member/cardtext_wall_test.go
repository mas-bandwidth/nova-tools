package member

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The card tells the child the wall the member ends it at, in minutes, and to have its
// RESULT.md written by 80% of it, whatever the brief names (fault 9, 2026-10-10: superman's
// DeepSeek children planned on a brief's 120 minutes and were ended at the route's 40 with
// no RESULT.md). A packet with no deadline says nothing of one.
func TestTheCardTellsTheChildItsWall(t *testing.T) {
	t.Parallel()
	brief := "DEADLINE: finish within 120 minutes\n"
	for _, kind := range []string{"", "read"} {
		text := CardText(Packet{Card: "c.w1", Kind: kind, Brief: brief, Deadline: 2400})
		assert.Contains(t, text, "Your wall: this run is ended at 40 minutes from its start, whatever time the brief names", kind)
		assert.Contains(t, text, "write RESULT.md by minute 32", kind)
		assert.NotContains(t, CardText(Packet{Card: "c.w1", Kind: kind, Brief: brief}), "Your wall:", "no deadline, no wall line")
	}
	assert.Equal(t, "", WallText(0))
	assert.Contains(t, WallText(30), "ended at 1 minutes", "never a wall of zero minutes")
	assert.Contains(t, WallText(30), "by minute 1")
}
