package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// A friend row with a class holds only cards whose tier her class covers, unless the card is
// pinned to her (dealt-packet-carries-the-tier.w2): the friend deal gates every card on her
// tiers (friendTakes), and the one deal that places a card outside them, the attempt cap's
// default answer (AttemptCapDeal), writes her WHO line onto it first. Every packet on her row
// names a tier (DealtTier), so a runner that hands back a tier it does not do can tell its own
// pinned card from a card dealt to it in error.
func TestAFriendRowHoldsItsTiersUnlessTheCardIsPinnedToHer(t *testing.T) {
	t.Parallel()
	w := capWorld(t) // s1-1: a machine's card, no tier (flash), past the attempt cap
	w.must(Add(w.s, AddReq{Stream: "s2", Cards: []CardAdd{
		{ID: "s2-1", Brief: friendBrief("friend")}, // flash, any friend
		{ID: "s2-2", Brief: "c: a friend's card tier: frontier\nREPO: mas-bandwidth/nova-tools\nWHO: friend\n\nThe task."}, // frontier, any friend
		{ID: "s2-3", Brief: "c: a friend's card tier: pro\nREPO: mas-bandwidth/nova-tools\nWHO: friend fay\n\nThe task."},  // pro, named fay (flash)
	}}))
	seats := []FriendSeat{
		{Name: "fay", Width: 4, Status: Up, Class: cardhdr.RouteFlash},
		{Name: "gus", Width: 4, Status: Up, Class: cardhdr.RouteFrontier},
		{Name: "pam", Width: 4, Status: Up, Class: cardhdr.RoutePro},
	}
	capDeal(w, seats...)

	held := map[string]string{}
	for _, f := range seats {
		row := FriendRow(f.Name)
		for _, col := range []State{Ready, Working} {
			for _, wc := range w.s.Fleet.Cell(row, col) {
				pr := w.s.Primary(wc.F("primary"))
				require.NotNil(t, pr)
				tier := DealtTier(wc, pr)
				require.NotEmpty(t, tier, "the packet of %s names a tier", wc.ID)
				pinned := strings.TrimPrefix(pr.F(FieldWho), "only.") == row
				assert.True(t, friendTakes(w.s, f, tier) || pinned, "%s (%s) holds %s on %s, not pinned to her", f.Name, f.Class, pr.ID, tier)
				held[pr.ID] = f.Name + " " + tier
			}
		}
	}
	assert.Equal(t, map[string]string{
		"s1-1": "gus flash",    // the attempt cap's friend card: pinned to gus by its WHO line
		"s2-1": "fay flash",    // flash: the flash friend
		"s2-2": "gus frontier", // frontier: the frontier friend
		"s2-3": "pam pro",      // named fay, who does not do pro: passed over for the pro friend
	}, held)
	assert.Equal(t, FriendRow("gus"), w.s.Primary("s1-1").F(FieldWho), "the one card outside her class is pinned to her")
	assert.Empty(t, Check(w.s, nil))
}
