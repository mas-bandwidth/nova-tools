package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The level evens every card a friend holds but a hard pin (WHO: only friend): friends
// first gives friends cards with no WHO line and cards preferring a named friend, so those
// move from a friend's ready backlog to a friend up with an idle lane like WHO: friend.
func TestTheLevelMovesUnpinnedCardsToAnIdleFriend(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, "c: no WHO\n\nThe task.", friendBrief("friend amy"), friendBrief("only friend amy"))
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash}}
	dealStarted(w, amy, FriendSeat{Name: "bob", Width: 1, Status: Held, Tiers: []string{cardhdr.RouteFlash}})
	require.Equal(t, 1, w.s.Fleet.Count(FriendRow("amy"), Working))
	require.Equal(t, 1, w.s.Fleet.Count(FriendRow("amy"), Ready), "room 2: one working, one ready behind")
	ready := w.s.Fleet.Cell(FriendRow("amy"), Ready)[0]
	require.False(t, OnlyFriend(w.s.Primary(ready.F("primary"))), "the ready card behind her is not the hard pin")

	// bob comes up idle: the tick's level moves amy's unpinned ready card to him
	dealStarted(w, amy, FriendSeat{Name: "bob", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash}})
	moved := w.s.Fleet.Card(ready.ID)
	assert.Equal(t, FriendRow("bob"), moved.Row, "an unpinned card is levelled to the idle friend")
	assert.Equal(t, Working, moved.Col)
	assert.Equal(t, "amy", moved.F(FieldFriendsLeft))
	assert.Empty(t, Check(w.s, nil))
}

// A withdrawn attempt at its redeal bound below its ceiling (three takes the provider
// ended, out of credit; or two takes that ended the same way) is not at AtRedealBound: the
// machines would escalate it to the next tier. With every route of that tier unfunded it
// goes to a friend whose tiers hold the escalated tier, as a new attempt on that tier,
// never under a "no route serves the tier" hold while she has room.
func TestACardWithTwoProviderFailuresGoesToAFriendNotAnUnfundedRoute(t *testing.T) {
	t.Parallel()
	take := func(line string) string { return ProviderTake{Route: "fl", Error: line}.String() }
	out := "402 Payment Required: insufficient credits"
	nr := "no result: no RESULT.md shape; q"
	for _, tc := range []struct {
		name    string
		redeals int
		takes   map[int]string
	}{
		{"three provider failures", MaxRedeals, map[int]string{1: take(out), 2: take(out), 3: take(out), 4: take(out)}},
		{"two identical ends", 1, map[int]string{1: take(nr), 2: take(nr)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := friendWorld(t, "c: work tier: heavy\n\nThe task.") // flash first, pro next
			w.s.Routes = []Route{
				{Name: "fl", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Enabled: true},
				{Name: "pr", Tier: cardhdr.RoutePro, Provider: "p", Model: "m", Enabled: true},
			}
			w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
			pr := w.s.Primary("s1-1")
			require.Equal(t, cardhdr.RouteFlash, pr.F(FieldTierNow), "flash first")
			wc := w.s.Fleet.Card("s1-1.w1")
			f := map[string]string{"redeals": itoa(tc.redeals), FieldTakeEnded: stamp(w.s.Now)}
			for n, v := range tc.takes {
				f[FieldProviderTake+itoa(n)] = v
			}
			w.must(Plan{Units: []Unit{{Key: pr.ID, Changes: []Change{
				change(Fleet, moveEntry(wc, wc.Row, Withdrawn, f)),
				change(Work, moveEntry(pr, pr.Row, Ready, nil)),
			}}}})
			rest := RouteRest{At: w.s.Now, Until: OpenUntil, Cause: RestCredit, Why: "out of credit: provider p refused card s1-1"}
			w.s.Fleet.SetProps(map[string]string{PropProviderRest("p"): rest.value()})
			require.True(t, redealBound(w.s.Fleet.Card("s1-1.w1")), "at its redeal bound")
			require.Nil(t, AtRedealBound(w.s, w.s.Primary("s1-1")), "below its ceiling: escalated, not judged")

			dealStarted(w,
				FriendSeat{Name: "bob", Width: 4, Status: Up, Tiers: []string{cardhdr.RouteFlash}},
				FriendSeat{Name: "amy", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash, cardhdr.RoutePro}})
			next := w.s.Fleet.Card("s1-1.w2")
			require.NotNil(t, next, "a new attempt")
			assert.Equal(t, FriendRow("amy"), next.Row, "the friend with the escalated tier, pro")
			assert.Equal(t, Working, next.Col)
			assert.Equal(t, cardhdr.RoutePro, w.s.Primary("s1-1").F(FieldTierNow), "escalated to pro")
			assert.Nil(t, w.s.Fleet.Placed("s1-1.w1"), "the bound attempt is retired")
			assert.Empty(t, w.notesOf(NNoRoute), "no no-route hold while a friend has room")
		})
	}
}

// A friend is dealt a card only when her tiers hold its tier, whatever its WHO line: a
// frontier card goes to the friend with frontier, never to a friend it names without it
// or to a friend whose row names no tier; a hard pin to a friend without the tier waits.
func TestAFrontierCardGoesOnlyToAFriendWithFrontier(t *testing.T) {
	t.Parallel()
	brief := func(who string) string { return "c: work tier: frontier\nWHO: " + who + "\n\nThe task." }
	amy := FriendSeat{Name: "amy", Width: 4, Status: Up, Tiers: []string{cardhdr.RouteFlash, cardhdr.RoutePro}}
	dan := FriendSeat{Name: "dan", Width: 8, Status: Up}
	cat := FriendSeat{Name: "cat", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFrontier}}

	w := friendWorld(t, brief("friend amy"), brief("friend"))
	dealStarted(w, amy, dan, cat)
	assert.Equal(t, FriendRow("cat"), w.s.Fleet.Card("s1-1.w1").Row, "named amy, but only cat has frontier")
	assert.Equal(t, FriendRow("cat"), w.s.Fleet.Card("s1-2.w1").Row, "any friend: cat, not dan whose row names no tier")

	only := friendWorld(t, brief("only friend amy"))
	dealStarted(only, amy, dan, cat)
	assert.Nil(t, only.s.Fleet.Card("s1-1.w1"), "a hard pin to amy without frontier waits for her")
	assert.Equal(t, Ready, only.s.StateOf("s1-1"))
}
