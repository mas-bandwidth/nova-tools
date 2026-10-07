package sprint_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The sides' tiers (nova-sprint set --fleet-tiers, --friends-tiers; the owner, 2026-10-06:
// "please open up the fleet for flash work only, but all the other work keeps going
// normally"): the deal hands a side only cards whose tier is in its set, on top of each
// row's own tiers; all, the default, takes every tier.

// sideCard is a machine card's brief whose line 1 names the tier ("" for none: flash).
func sideCard(tier string) string {
	return "c: work" + tier + "\nREPO: mas-bandwidth/nova-tools\n\nThe task."
}

// dealtTo is the row the primary's work card is placed on, "" while none is.
func dealtTo(s *sprint.Snapshot, primary string) string {
	if wc := s.Fleet.Placed(s.Work.Card(primary).F("work")); wc != nil {
		return wc.Row
	}
	return ""
}

func TestFleetTiersLimitWhatTheFleetTakes(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 0, 0) // amy and bob are pro friends; m1 and m2 are up
	res, err := r.st.Run(r.ctx, store.SetStep(sprint.SetReq{FleetTiers: "flash,turbo", Who: "coordinator"}))
	require.NoError(t, err)
	require.Len(t, res.Refused, 1, "an unknown tier is refused")
	assert.Contains(t, res.Refused[0].Why, "--fleet-tiers wants tiers of flash, pro, heavy, frontier")

	r.must(store.SetStep(sprint.SetReq{FleetTiers: "flash", Friends: sprint.SwitchOff, Who: "coordinator"}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "s1-1", Brief: sideCard("")}, {ID: "s1-2", Brief: sideCard(" tier: pro")}}}))
	r.tick()
	s := r.snap()
	assert.Contains(t, []string{"m1", "m2"}, dealtTo(s, "s1-1"), "a flash card goes to a member")
	assert.Empty(t, dealtTo(s, "s1-2"), "a pro card does not")
	assert.Equal(t, sprint.Ready, s.StateOf("s1-2"))
	var judged []string
	for _, o := range s.Open {
		if o.Subject() == sprint.StreamSubject(sprint.TierSubject("pro")) {
			judged = append(judged, o.Note.Type)
			assert.Contains(t, o.Note.What, "--fleet-tiers", "the tier's judgment says the fleet's tiers")
		}
	}
	assert.Equal(t, []string{sprint.NNoRoute}, judged, "no side may take it: the tier's one judgment")

	r.must(store.SetStep(sprint.SetReq{Friends: sprint.SwitchOn, Who: "coordinator"}))
	r.tick()
	s = r.snap()
	assert.Contains(t, []string{sprint.FriendRow("amy"), sprint.FriendRow("bob")}, dealtTo(s, "s1-2"), "the friends still take pro")

	r.must(store.SetStep(sprint.SetReq{FleetTiers: sprint.TiersAll, Who: "coordinator"}))
	r.tick() // a setting written while the machine runs is the next tick's
	assert.True(t, r.snap().FleetTakes("pro"), "all clears")
}

func TestFriendsTiersLimitWhatFriendsTake(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 0, 0)
	_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{{Name: "amy", Width: 2, Class: "pro"}, {Name: "bob", Width: 2, Class: "pro"}, {Name: "fay", Width: 2, Class: "flash,pro"}})
	require.NoError(t, err)
	_, err = r.st.FriendBeat(r.ctx, "fay")
	require.NoError(t, err)
	_, _, _, err = r.st.FriendHealth(r.ctx, "fay", "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}, "")
	require.NoError(t, err)
	r.must(store.SetStep(sprint.SetReq{FriendsTiers: "flash", Who: "coordinator"}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "s1-1", Brief: sideCard("")}, {ID: "s1-2", Brief: sideCard(" tier: pro")}}}))
	r.tick()
	s := r.snap()
	assert.Equal(t, sprint.FriendRow("fay"), dealtTo(s, "s1-1"), "a flash card goes to the flash friend")
	assert.Contains(t, []string{"m1", "m2"}, dealtTo(s, "s1-2"), "a pro card goes to no friend, pro on her row or not: the fleet takes it")
	assert.False(t, s.FriendsTake("pro"))
	assert.True(t, s.FleetTakes("pro"))
}
