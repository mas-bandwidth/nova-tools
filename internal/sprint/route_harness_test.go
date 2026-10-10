package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A deal to a member draws only a route that member can launch (fault 10, 2026-10-10:
// heavy-opus-claude cards were dealt to four members that have no
// claude, and every launch was refused; tla/RouteIndex.tla, NeverUnlaunchable). A route
// of a headless harness is drawn for a member whose control card names it (fleet up
// --harnesses), walked past for any other; a tier whose every route is one the member
// cannot launch is not dealt to it, and says why; a check that names no member draws
// every route. A dealt card is never moved to a member that cannot launch its route.
func TestADealDrawsOnlyARouteItsMemberCanLaunch(t *testing.T) {
	t.Parallel()
	f := NewTable(Fleet)
	f.SetRows([]string{"m1", "m2"})
	f.Put(&Card{ID: CtlID("m1"), Row: "m1", Col: Ctl, Fields: map[string]string{"kind": "member", "status": Up}})
	f.Put(&Card{ID: CtlID("m2"), Row: "m2", Col: Ctl, Fields: map[string]string{"kind": "member", "status": Up, FieldHarnesses: "claude"}})
	s := &Snapshot{
		Now:   t0,
		Fleet: f,
		Routes: []Route{
			{Name: "flash-claude", Tier: cardhdr.RouteFlash, Provider: "subscription-claude", Model: "opus", Harness: "claude", Enabled: true},
			{Name: "flash-oc", Tier: cardhdr.RouteFlash, Provider: "p", Model: "a", Enabled: true},
		},
		Tiers: map[string][]string{cardhdr.RouteFlash: {"flash-claude", "flash-oc"}},
	}
	card := &Card{ID: "s1-1", Fields: map[string]string{"brief": "tier: flash\n"}}

	set, _, why, _ := s.routeOf(card, nil, nil, "m1")
	require.Empty(t, why)
	assert.Equal(t, "flash-oc", set[FieldRoute], "m1 has no claude: the claude entry at the place is walked past")
	set, _, why, _ = s.routeOf(card, nil, nil, "m2")
	require.Empty(t, why)
	assert.Equal(t, "flash-claude", set[FieldRoute], "m2 names claude: the entry at the place")
	set, _, why, _ = s.routeOf(card, nil, nil, "")
	require.Empty(t, why)
	assert.Equal(t, "flash-claude", set[FieldRoute], "no member named: every route draws")

	s.Routes[1].Enabled = false
	_, _, why, _ = s.routeOf(card, nil, nil, "m1")
	assert.Contains(t, why, "member m1 can launch no route of tier flash that serves: flash-claude (runs under claude)")
	_, _, why, _ = s.routeOf(card, nil, nil, "m2")
	assert.Empty(t, why, "m2 launches it")

	onClaude := &Card{ID: "s1-2.w1", Fields: map[string]string{FieldHarness: "claude"}}
	assert.Equal(t, []string{"m1"}, notLaunching(s, []string{"m1", "m2"}, onClaude))
	assert.Empty(t, notLaunching(s, []string{"m1", "m2"}, &Card{ID: "s1-3.w1", Fields: map[string]string{}}), "an opencode card launches anywhere")
}

// fleet up --harnesses: a comma list of headless harnesses, or none; anything else refused.
func TestHarnessesWordsAreHeadlessOrNone(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"", "none", "claude", "claude,codex", "grok,claude"} {
		assert.Empty(t, HarnessesWhy(ok), ok)
	}
	for _, bad := range []string{"opencode", "claude,vim"} {
		assert.Contains(t, HarnessesWhy(bad), "no headless harness", bad)
	}
	assert.Equal(t, "claude,codex", harnessesValue("codex,claude,codex"))
	assert.Equal(t, "", harnessesValue("none"))
}

// The deal chooses among the members that can launch a route of the card's tier, never
// chooses one and is then refused (the second cold read of nova-tools#5576): on a fleet of
// m1 (no claude) and m2 (claude), with the tier's one route under claude, every card is
// dealt to m2, none refused, whichever member the round starts at.
func TestTheDealSendsAClaudeCardToAMemberWithClaude(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 2}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2", Width: 2, Harnesses: "claude"}))
	assert.Equal(t, "claude", w.s.MemberCtl("m2").F(FieldHarnesses), "fleet up --harnesses names it on the control card")
	w.s.Routes = []Route{{Name: "flash-claude", Tier: cardhdr.RouteFlash, Provider: "subscription-claude", Model: "opus", Harness: "claude", Enabled: true, Deadline: 600}}
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 3}))
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1", "s1-2", "s1-3"}}}))
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		wc := w.s.Fleet.Card(WorkCardID(id, 1))
		require.NotNil(t, wc, id)
		assert.Equal(t, "m2", wc.Row, "%s dealt to the member with claude", id)
		assert.Equal(t, "flash-claude", wc.F(FieldRoute), id)
	}
}
