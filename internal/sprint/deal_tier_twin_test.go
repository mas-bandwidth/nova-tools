package sprint_test

import (
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dealt packet names its card's tier, always (dealt-packet-carries-the-tier.w1; on
// 2026-10-05 the audit cards were dealt with no tier in the packet, a flash friend's runner
// read "tier -" and handed back its own pinned cards for an hour, and the dealer rotated them
// to subscription friends). On the twin store with no route, where no deal draws a tier onto
// the work card: a machine's card and a friend's card each carry the tier they are on (the
// tier pinned, else the tier the deal drew: the tier its brief's `tier:` field names,
// flash when it names none), and a friend of one class is dealt only the cards her class
// covers.
func TestADealtPacketAlwaysNamesItsTier(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 0, 0)
	_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{{Name: "amy", Width: 2, Class: "pro"}, {Name: "bob", Width: 2, Class: "pro"}, {Name: "fay", Width: 2, Class: "flash"}})
	require.NoError(t, err)
	machine := func(tier string) string {
		return "c: work" + tier + "\nREPO: mas-bandwidth/nova-tools\n\nThe task."
	}
	friend := func(tier, who string) string {
		return "c: a friend's card" + tier + "\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
	}
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{
		{ID: "s1-1", Brief: machine(" tier: pro")},
		{ID: "s1-2", Brief: machine("")},
		{ID: "s1-3", Brief: machine(" tier: flash")},
	}}))
	r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: []sprint.CardAdd{
		{ID: "f1-1", Brief: friend("", "friend")},
		{ID: "f1-2", Brief: friend(" tier: pro", "friend fay")},
		{ID: "f1-3", Brief: friend(" tier: pro", "only friend fay")},
	}}))
	r.must(store.BriefStep(sprint.BriefReq{ID: "s1-3", Tier: "pro", Who: "coordinator"}))
	_, err = r.st.FriendBeat(r.ctx, "fay")
	require.NoError(t, err)
	_, _, _, err = r.st.FriendHealth(r.ctx, "fay", "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}, "")
	require.NoError(t, err)
	r.tick()

	s := r.snap()
	var dealt []*sprint.Card
	for _, row := range s.Fleet.Rows() {
		for _, col := range []sprint.State{sprint.Ready, sprint.Working} {
			dealt = append(dealt, s.Fleet.Cell(row, col)...)
		}
	}
	packets, err := r.st.Packets(r.ctx, dealt)
	require.NoError(t, err)
	tiers, rows := map[string]string{}, map[string]string{}
	for i, p := range packets {
		assert.NotEmpty(t, p.Tier, "the packet of %s names a tier", p.Card)
		tiers[p.Primary], rows[p.Primary] = p.Tier, dealt[i].Row
		now, _ := sprint.CardTiers(s.Primary(p.Primary))
		assert.Equal(t, now, p.Tier, "the packet of %s names the tier its card is on", p.Card)
	}
	assert.Equal(t, map[string]string{
		"s1-1": "pro",   // the brief's tier is the tier it starts on
		"s1-2": "flash", // no tier: the default
		"s1-3": "pro",   // brief --tier pins it over line 1
		"f1-1": "flash", // a friend's card with no tier: the default, on the flash friend
		"f1-2": "pro",   // named fay (flash): passed over for a pro friend
	}, tiers)

	// a friend of a class is dealt only the cards her class covers
	assert.Equal(t, sprint.FriendRow("fay"), rows["f1-1"])
	assert.Contains(t, []string{sprint.FriendRow("amy"), sprint.FriendRow("bob")}, rows["f1-2"])
	assert.Equal(t, sprint.Ready, s.StateOf("f1-3"), "a hard pin to fay, who does not do pro, waits for her")
	for _, f := range []struct{ name, class string }{{"amy", "pro"}, {"bob", "pro"}, {"fay", "flash"}} {
		for primary, row := range rows {
			if row == sprint.FriendRow(f.name) {
				assert.True(t, slices.Contains([]string{f.class}, tiers[primary]), "%s (%s) holds %s on %s", f.name, f.class, primary, tiers[primary])
			}
		}
	}
}
