package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// TestALaneEnvCarriesItsMarkAndItsMembersNameOnly pins the one place a lane's environment gets
// its lane entries (laneEnv, every launch's native): the launch's mark and the member's name
// replace whatever lane entries the member inherited (a member run inside another member's
// lane, a card's own test), so a lane is never marked as another member's; a member with no
// name writes no owner, and no restart treats its lanes as orphans of a name.
func TestALaneEnvCarriesItsMarkAndItsMembersNameOnly(t *testing.T) {
	t.Parallel()
	inherited := []string{"PATH=/bin", swarm.LaneEnv + "=/outer/slots/x", swarm.LaneOwnerEnv + "=outer"}
	require.ElementsMatch(t, []string{"PATH=/bin", swarm.LaneEnv + "=/r/slots/a-1", swarm.LaneOwnerEnv + "=m1"},
		laneEnv(inherited, "/r/slots/a-1", "m1"))
	require.ElementsMatch(t, []string{"PATH=/bin", swarm.LaneEnv + "=/r/slots/a-1"},
		laneEnv(inherited, "/r/slots/a-1", ""))
}
