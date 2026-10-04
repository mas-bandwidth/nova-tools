package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend row's restriction (docs/SPEC-CONFIG.md, the friend kind; docs/SPEC-SPRINT.md
// section 1, a friend's card; the owner, 2026-10-04: "Alex does security work only"): the
// dealer never deals a friend a card whose stream or kind is outside her streams and kinds.

// restrictedBrief is a friend's card brief of the kind given.
func restrictedBrief(who, kind string) string {
	return "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nKIND: " + kind + "\nWHO: " + who + "\n\nThe task."
}

func TestDealerNeverDealsAFriendOutsideHerStreams(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, restrictedBrief("friend", "fix-red"))
	amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Streams: []string{"security*"}}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up}

	// stream s1 is outside security*: she is skipped for any-friend work, so bob is dealt it
	dealWith(w, amy, bob)
	require.NotNil(t, w.s.Fleet.Card("s1-1.w1"))
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row, "an out-of-scope card skips her")

	// with only her up it waits ready, dealt to no one
	w = friendWorld(t, restrictedBrief("friend", "fix-red"))
	dealWith(w, amy)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Nil(t, w.s.Fleet.Card("s1-1.w1"))

	// a card naming her outside the restriction is not dealt either (add refuses it; a row
	// restricted since is held the same)
	w = friendWorld(t, restrictedBrief("friend amy", "fix-red"))
	dealWith(w, amy)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))

	// an in-scope stream is dealt to her, by glob
	w = friendWorld(t, restrictedBrief("friend", "fix-red"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Streams: []string{"security*", "s?"}})
	require.NotNil(t, w.s.Fleet.Card("s1-1.w1"))
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-1.w1").Row)
}

func TestDealerNeverDealsAFriendACardKindOutsideHerKinds(t *testing.T) {
	t.Parallel()
	seat := FriendSeat{Name: "amy", Width: 2, Status: Up, Kinds: []string{"audit"}}
	w := friendWorld(t, restrictedBrief("friend", "fix-red"), restrictedBrief("friend", "audit"), friendBrief("friend"))
	dealWith(w, seat)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"), "fix-red is outside her kinds")
	assert.Equal(t, Working, w.s.StateOf("s1-2"), "audit is inside them")
	assert.Equal(t, Ready, w.s.StateOf("s1-3"), "a card naming no kind is outside a kinds restriction")

	// both fields: a card must be inside each
	both := FriendSeat{Name: "amy", Width: 2, Status: Up, Streams: []string{"x*"}, Kinds: []string{"audit"}}
	w = friendWorld(t, restrictedBrief("friend", "audit"))
	dealWith(w, both)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"), "inside her kinds, outside her streams")
}

func TestARestrictionNamesWhatItRefuses(t *testing.T) {
	t.Parallel()
	f := FriendSeat{Name: "amy", Streams: []string{"security*"}, Kinds: []string{"audit"}}
	assert.Empty(t, FriendRestrictionWhy(FriendSeat{Name: "amy"}, "s1", "fix-red"), "no restriction: none refused")
	assert.Empty(t, FriendRestrictionWhy(f, "security-1", "audit"))
	why := FriendRestrictionWhy(f, "s1", "audit")
	assert.Contains(t, why, "stream is s1")
	assert.Contains(t, why, "security*")
	why = FriendRestrictionWhy(f, "security-1", "fix-red")
	assert.Contains(t, why, "kind fix-red")
	assert.Contains(t, why, "audit")
}
