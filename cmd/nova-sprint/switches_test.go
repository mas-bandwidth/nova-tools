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
	assert.Empty(t, switchesLine(sprint.SwitchOn, sprint.SwitchOn, nil, nil), "both on: nothing said")
	assert.Equal(t, "fleet: off", switchesLine(sprint.SwitchOff, sprint.SwitchOn, nil, nil))
	assert.Equal(t, "friends: off", switchesLine(sprint.SwitchOn, sprint.SwitchOff, nil, nil))
	assert.Equal(t, "fleet: off  friends: off", switchesLine(sprint.SwitchOff, sprint.SwitchOff, nil, nil))

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

// The sides' tiers (nova-sprint set --fleet-tiers, --friends-tiers): while one is set, where
// and view coordinator's summary say both sides, each its switch and its tiers ("fleet: on,
// tiers flash  friends: on, tiers all"); where --json carries fleet_tiers and friends_tiers
// always, "all" or the list; view coordinator's JSON carries a side's tiers only when set.
func TestTheTierSettingsAreSaidWhereTheCoordinatorLooks(t *testing.T) {
	t.Parallel()
	flash := []string{"flash"}
	assert.Equal(t, "fleet: on, tiers flash  friends: on, tiers all", switchesLine(sprint.SwitchOn, sprint.SwitchOn, flash, nil))
	assert.Equal(t, "fleet: off, tiers all  friends: on, tiers flash,pro", switchesLine(sprint.SwitchOff, "", nil, []string{"flash", "pro"}))

	b, err := json.Marshal(whereView{FleetWork: sprint.SwitchOn, FriendsWork: sprint.SwitchOn, FleetTiers: tiersJSON(flash), FriendsTiers: tiersJSON(nil)})
	require.NoError(t, err)
	var top map[string]any
	require.NoError(t, json.Unmarshal(b, &top))
	assert.Equal(t, []any{"flash"}, top["fleet_tiers"])
	assert.Equal(t, "all", top["friends_tiers"])

	v := coordinatorView{FleetTiers: flash}
	assert.Contains(t, coordinatorSum(v, false, store.Machine{}), "| fleet: on, tiers flash  friends: on, tiers all")
	b, err = json.Marshal(v)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"fleet_tiers":["flash"]`)
	assert.NotContains(t, string(b), `"friends_tiers"`, "all is the default and not carried")
}
