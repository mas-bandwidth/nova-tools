package sprint

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// The rebalance (docs/SPEC-SPRINT.md section 1, the rebalance; the owner, 2026-10-06: "This
// should be a holistic rebalance, not just across friends, not just across tiers, but BOTH").

// rbWorld is a world with the members given up at width 1, a flash and a pro route, and
// stream s1.
func rbWorld(t *testing.T, members ...string) *world {
	t.Helper()
	w := newWorld(t)
	for _, m := range members {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m, Width: 1}))
	}
	w.s.Routes = []Route{
		{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Enabled: true},
		{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "p", Model: "pr", Enabled: true},
	}
	w.s.Work.SetRows([]string{"s1"})
	return w
}

// rbPlace places primary id of the tier working in s1, its work card on the row in col.
func rbPlace(w *world, id, tier, row, col string, fields ...string) *Card {
	if !w.s.Fleet.HasRow(row) {
		w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), row))
	}
	n := float64(len(w.s.Work.Column(Working)) + 1)
	pr := &Card{ID: id, Row: "s1", Col: Working, Score: n, Rev: 1, Fields: map[string]string{
		"kind": "primary", "attempt": "1", "stream": "s1", "brief": fleetBrief(tier), "work": WorkCardID(id, 1), FieldTierNow: tier}}
	wc := &Card{ID: WorkCardID(id, 1), Row: row, Col: col, Score: n, Rev: 1, Fields: map[string]string{
		"kind": "work", "primary": id, "stream": "s1", "attempt": "1", "gen": "1", "member": row}}
	for i := 0; i+1 < len(fields); i += 2 {
		if strings.HasPrefix(fields[i], "pr.") {
			pr.Fields[strings.TrimPrefix(fields[i], "pr.")] = fields[i+1]
		} else {
			wc.Fields[fields[i]] = fields[i+1]
		}
	}
	w.s.Work.Put(pr)
	w.s.Fleet.Put(wc)
	return wc
}

func rebalanced(p Plan) []string {
	var out []string
	for _, u := range p.Units {
		if strings.HasPrefix(u.Moved, "rebalanced ") {
			out = append(out, u.Moved)
		}
	}
	return out
}

func TestRebalanceIsTheWorkTablesPartAfterTheDeal(t *testing.T) {
	t.Parallel()
	var names []string
	for _, p := range TickTables[0].Parts {
		names = append(names, p.Name)
	}
	i := slices.Index(names, PartRebalance)
	require.GreaterOrEqual(t, i, 1, "the work table's update has the rebalance part")
	assert.Equal(t, "deal", names[i-1], "it runs on the deal applied")
}

// A flash card queued on a full flash friend moves to a member with an idle lane when the
// fleet's set admits flash; not when the set is pro only, nor with the fleet off.
func TestRebalanceMovesAFlashCardOffAFullFriendToAnIdleMember(t *testing.T) {
	t.Parallel()
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash}}
	setup := func() (*world, *Card) {
		w := rbWorld(t, "m1")
		rbPlace(w, "s1-1", cardhdr.RouteFlash, FriendRow("amy"), Working)
		return w, rbPlace(w, "s1-2", cardhdr.RouteFlash, FriendRow("amy"), Ready)
	}

	w, wc := setup()
	p := w.must(Rebalance(w.s, []FriendSeat{amy}, "machine"))
	require.Equal(t, []string{"rebalanced s1-2.w1 from friend.amy to m1 gen=2 (its lanes all work; an idle lane takes it)"}, rebalanced(p))
	got := w.s.Fleet.Card(wc.ID)
	assert.Equal(t, "m1", got.Row)
	assert.Equal(t, Ready, got.Col)
	assert.Equal(t, "2", got.F("gen"), "its next generation: her inbox job is stale")
	assert.Equal(t, "1", got.F("attempt"), "its attempt kept")
	assert.Equal(t, "flash-a", got.F(FieldRoute), "a machine draws a route of its tier")
	assert.Equal(t, FriendRow("amy"), got.F(FieldRebalancedFrom))
	assert.Equal(t, "amy", got.F(FieldFriendsLeft))
	require.Len(t, p.Units[0].Notes, 1, "a happened note, no judgment")
	n := p.Units[0].Notes[0]
	assert.Equal(t, NRebalanced, n.Type)
	assert.Equal(t, Happened, n.Kind)
	assert.Equal(t, "rebalanced s1-2.w1 from friend.amy to m1", n.What)
	assert.Empty(t, p.Notes)

	w, _ = setup()
	w.s.Work.SetProp(PropFleetTiers, cardhdr.RoutePro)
	assert.Empty(t, rebalanced(Rebalance(w.s, []FriendSeat{amy}, "machine")), "the fleet's set is pro only")

	w, _ = setup()
	w.s.Work.SetProp(PropFleet, SwitchOff)
	assert.Empty(t, rebalanced(Rebalance(w.s, []FriendSeat{amy}, "machine")), "the fleet is off")
}

// A pro card queued on a full heavy friend moves to a pro friend with an idle lane, the
// cheapest before a heavy one, and never to a member whose fleet set is flash.
func TestRebalanceMovesAProCardToAProFriendNeverAFlashMember(t *testing.T) {
	t.Parallel()
	hal := FriendSeat{Name: "hal", Width: 1, Status: Up, Tiers: []string{cardhdr.RoutePro, cardhdr.RouteHeavy}}
	hugh := FriendSeat{Name: "hugh", Width: 3, Status: Up, Tiers: []string{cardhdr.RoutePro, cardhdr.RouteHeavy}}
	pam := FriendSeat{Name: "pam", Width: 1, Status: Up, Tiers: []string{cardhdr.RoutePro}}
	setup := func() (*world, *Card) {
		w := rbWorld(t, "m1")
		w.s.Work.SetProp(PropFleetTiers, cardhdr.RouteFlash)
		rbPlace(w, "s1-1", cardhdr.RoutePro, FriendRow("hal"), Working)
		return w, rbPlace(w, "s1-2", cardhdr.RoutePro, FriendRow("hal"), Ready, FieldRoute, "pro-a")
	}

	w, wc := setup()
	w.must(Rebalance(w.s, []FriendSeat{hal, hugh, pam}, "machine"))
	got := w.s.Fleet.Card(wc.ID)
	assert.Equal(t, FriendRow("pam"), got.Row, "the pro friend first, though the heavy one has more idle lanes")
	assert.Empty(t, got.F(FieldRoute), "a friend runs her own model")

	w, wc = setup()
	assert.Empty(t, rebalanced(Rebalance(w.s, []FriendSeat{hal}, "machine")), "never a flash member")
	assert.Equal(t, FriendRow("hal"), w.s.Fleet.Card(wc.ID).Row)
}

// A started card never moves: working, or ready and named running by her beat.
func TestRebalanceNeverMovesAStartedCard(t *testing.T) {
	t.Parallel()
	w := rbWorld(t, "m1")
	busy := rbPlace(w, "s1-1", cardhdr.RouteFlash, FriendRow("amy"), Working)
	queued := rbPlace(w, "s1-2", cardhdr.RouteFlash, FriendRow("amy"), Ready)
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash}, Running: []string{queued.ID}}
	assert.Empty(t, rebalanced(Rebalance(w.s, []FriendSeat{amy}, "machine")))
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card(busy.ID).Row)
}

// A pinned card never moves: a hard pin, a pin to the friend it sits on, WHO: friend off the
// friends, a model pin.
func TestRebalanceNeverMovesAPinnedCard(t *testing.T) {
	t.Parallel()
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash}}
	for _, pin := range [][]string{
		{"pr." + FieldWho, "only." + FriendRow("amy")},
		{"pr." + FieldWho, FriendRow("amy")},
	} {
		w := rbWorld(t, "m1")
		rbPlace(w, "s1-1", cardhdr.RouteFlash, FriendRow("amy"), Working)
		rbPlace(w, "s1-2", cardhdr.RouteFlash, FriendRow("amy"), Ready, pin...)
		assert.Empty(t, rebalanced(Rebalance(w.s, []FriendSeat{amy}, "machine")), "%v", pin)
	}
	w := rbWorld(t, "m1")
	rbPlace(w, "s1-1", cardhdr.RouteFlash, FriendRow("amy"), Working)
	rbPlace(w, "s1-2", cardhdr.RouteFlash, FriendRow("amy"), Ready, "pr."+FieldWho, WhoFriend)
	assert.Empty(t, rebalanced(Rebalance(w.s, []FriendSeat{amy}, "machine")), "WHO: friend stays with the friends")

	w = rbWorld(t, "m1", "m2")
	rbPlace(w, "s1-1", cardhdr.RouteFlash, "m1", Working)
	rbPlace(w, "s1-2", cardhdr.RouteFlash, "m1", Ready, FieldRoute, RoutePin)
	assert.Empty(t, rebalanced(Rebalance(w.s, nil, "machine")), "a model pin")
}

// A card taken back from a unit, or rebalanced off it, never moves to it.
func TestRebalanceNeverMovesACardToAUnitItLeft(t *testing.T) {
	t.Parallel()
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash}}
	bob := FriendSeat{Name: "bob", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash}}
	w := rbWorld(t)
	rbPlace(w, "s1-1", cardhdr.RouteFlash, FriendRow("amy"), Working)
	rbPlace(w, "s1-2", cardhdr.RouteFlash, FriendRow("amy"), Ready, FieldFriendsLeft, "bob")
	assert.Empty(t, rebalanced(Rebalance(w.s, []FriendSeat{amy, bob}, "machine")), "taken back from bob")

	w = rbWorld(t, "m1", "m2")
	rbPlace(w, "s1-1", cardhdr.RouteFlash, "m1", Working)
	rbPlace(w, "s1-2", cardhdr.RouteFlash, "m1", Ready, FieldRebalancedFrom, "m2")
	assert.Empty(t, rebalanced(Rebalance(w.s, nil, "machine")), "rebalanced off m2")
}

// Two queued cards and one idle lane: one moves.
func TestRebalanceMovesOneCardPerIdleLane(t *testing.T) {
	t.Parallel()
	w := rbWorld(t, "m1", "m2")
	rbPlace(w, "s1-1", cardhdr.RouteFlash, "m1", Working)
	a := rbPlace(w, "s1-2", cardhdr.RouteFlash, "m1", Ready)
	b := rbPlace(w, "s1-3", cardhdr.RouteFlash, "m1", Ready)
	p := w.must(Rebalance(w.s, nil, "machine"))
	assert.Len(t, rebalanced(p), 1)
	assert.Equal(t, 1, w.s.Fleet.Count("m2", Ready))
	assert.Equal(t, "m2", w.s.Fleet.Card(a.ID).Row, "the first in the deal order")
	assert.Equal(t, "m1", w.s.Fleet.Card(b.ID).Row)
	assert.Empty(t, rebalanced(Rebalance(w.s, nil, "machine")), "no idle lane left")
}
