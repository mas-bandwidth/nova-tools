package units

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTheSeatPushLoopIsNoUnitKind pins the seat push loop's retirement (nova-sprint
// v1.3.6, #338: the server sends the coordinator's judgments to its name on the bus):
// no kind installs or lists it, and its label and service stay only so seat uninstall
// can remove the unit an older build left.
func TestTheSeatPushLoopIsNoUnitKind(t *testing.T) {
	t.Parallel()
	for _, k := range UnitKinds {
		assert.NotEqual(t, "seat-push", k.Kind)
		assert.NotEqual(t, SeatLabel, k.Label)
		assert.NotEqual(t, SeatService, k.Service)
	}
	_, ok := UnitKindOf("seat-push")
	assert.False(t, ok, "seat-push is no kind")
	assert.Equal(t, []string{"server", "member", "friend-sync", "table"}, UnitKindNames("nova-sprint"))
	assert.Equal(t, "nova-sprint.seat-push", SeatLabel)
	assert.Equal(t, "nova-sprint-seat-push.service", SeatService)
}
