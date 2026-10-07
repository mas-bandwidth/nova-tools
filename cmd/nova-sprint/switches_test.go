package main

import (
	"encoding/json"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The work switches (nova-sprint set --fleet off, --friends off; the owner, 2026-10-06):
// where prints "fleet: off" and "friends: off" under its header while a side is off and
// nothing while both are on; where --json carries fleet_work and friends_work, on or off,
// always, for the dashboard to grey a side that is off; view coordinator's summary says
// the sides that are off, and its JSON carries them only when off.
func TestTheWorkSwitchesAreSaidWhereTheCoordinatorLooks(t *testing.T) {
	t.Parallel()
	assert.Empty(t, switchesLine(sprint.SwitchOn, sprint.SwitchOn), "both on: nothing said")
	assert.Equal(t, "fleet: off", switchesLine(sprint.SwitchOff, sprint.SwitchOn))
	assert.Equal(t, "friends: off", switchesLine(sprint.SwitchOn, sprint.SwitchOff))
	assert.Equal(t, "fleet: off  friends: off", switchesLine(sprint.SwitchOff, sprint.SwitchOff))

	b, err := json.Marshal(whereView{FleetWork: sprint.SwitchOff, FriendsWork: sprint.SwitchOn})
	require.NoError(t, err)
	var top map[string]any
	require.NoError(t, json.Unmarshal(b, &top))
	assert.Equal(t, "off", top["fleet_work"])
	assert.Equal(t, "on", top["friends_work"])

	v := coordinatorView{Fleet: sprint.SwitchOff}
	assert.Contains(t, coordinatorSum(v, false, store.Machine{}), "| fleet: off")
	assert.NotContains(t, coordinatorSum(v, false, store.Machine{}), "friends: off")
	b, err = json.Marshal(coordinatorView{})
	require.NoError(t, err)
	assert.NotContains(t, string(b), `"fleet"`, "on is the default and not carried")
}
