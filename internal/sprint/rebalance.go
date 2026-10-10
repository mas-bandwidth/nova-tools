package sprint

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// The rebalance (docs/SPEC-SPRINT.md section 1, the rebalance; the owner, 2026-10-06: "This
// should be a holistic rebalance, not just across friends, not just across tiers, but BOTH,
// depending on what tiers are enabled per-fleet/friends table." and "When you rebalance,
// always rebalance this way from now on."; 2026-10-10: "cards need to fill working slots FIRST
// across fleet and friends, then go to ready overflow up to 2X", "the deal is lowest cost
// first"). The work table's part after the deal's work-now part (TickDeal): a work card dealt
// and not started, in a row's overflow (the row holds more cards than its lanes, whether or not
// all its lanes work), goes to a unit of either side with a free lane that may take it, friend
// or member, within the side's tier set, the cheapest first; a row's ready past its cap,
// DealAhead times its width, goes back to the pool for the deal's stack part (TickStack). tla/DealCost.tla (WorkNow, Return) and tla/WhoPreference.tla,
// Rebalance.

// PartRebalance is the work table's part after the deal (TickTables).
const PartRebalance = "rebalance"

// FieldRebalancedFrom is the rows a work card was rebalanced off, comma joined: the
// rebalance never places it on one of them again.
const FieldRebalancedFrom = "rebalanced_from"

// NRebalanced is the happened note of a queued card rebalanced to an idle lane.
const NRebalanced = "a queued card rebalanced to an idle lane"

// NReturnedToPool is the happened note of a dealt card returned to the pool: its row held more
// ready than its cap.
const NReturnedToPool = "a dealt card returned to the pool"

// rebalanceUnit is a row the rebalance reads: a friend dealable or a member up, its lanes,
// the lanes working (reads at half a slot) and the cards it holds, ready and working.
type rebalanceUnit struct {
	name, row string
	friend    bool
	seat      FriendSeat
	width     int
	working   int
	load      int
	// cost is its cost rank: a subscription friend's, the fleet's or an API-rate friend's
	// (friendCostRank); the cheapest takes a card first
	cost int
}

// idle is the unit's idle lanes: its width less every card it holds.
func (u *rebalanceUnit) idle() int { return u.width - u.load }

// TickRebalance is the rebalance as a part of the tick (Rebalance).
func TickRebalance(s *Snapshot, r TickReq) (Plan, int) {
	seats := r.Friends
	if seats == nil {
		seats = s.Friends
	}
	return Rebalance(s, seats, r.who()), 0
}

// Rebalance moves each work card dealt and not started (ready on its row: a friend has not
// started it, friendStarted; a member has not taken it) in a unit's overflow (the unit holds
// more cards than its width: its own free lanes take the rest), to a unit with an idle lane
// that may take it, one move a card, at most the idle lanes and at most the overflow; a unit's
// overflow moves whether or not all its lanes work (2026-10-10: a friend at 28 of 32 working
// held 50 ready while another friend sat at 0 of 32 and the fleet at 42 of 126). Then each
// unit's ready past its cap, DealAhead times its width, goes back to the pool
// (rebalanceReturns). A card stays
// when it is a read, a sentinel's, of a held stream, a bench card, a hard pin (OnlyFriend), a pin honoured where it sits (its WHO names the friend it is on, or
// a model pin on a member; a WHO: friend card on a friend's row goes to no member), run by a lane (laneRunsIt), or its route rests. A friend may take
// it when she is dealable, her tiers and the friends' set hold its tier (friendTakes) and it
// has not left her (friendsLeft); a member up, while the fleet's work is on, when the fleet's
// set holds its tier, a route of the tier serves it (routeOf) and it did not refuse it at
// staging. Never a row it was rebalanced off (FieldRebalancedFrom). The cheapest unit
// first: its cost rank (a subscription friend, then a machine, then an API-rate friend:
// friendCostRank), then the card's own tier (a member draws on its tier's routes, a friend
// whose highest tier is the card's), then one tier up as overflow, never more; then the most
// idle lanes, then the name. The card moves at its next generation, its attempt kept and nothing
// carried; to a member it draws a route of its tier, to a friend its route comes off.
func Rebalance(s *Snapshot, seats []FriendSeat, who string) Plan {
	var p Plan
	if s == nil || s.Work == nil || s.Fleet == nil {
		return p
	}
	s, _ = s.withRests()
	var units []*rebalanceUnit
	add := func(u *rebalanceUnit) {
		work, reads := rowWorking(s, u.row)
		u.working = halfLoad(work, reads)
		u.load = halfLoad(rowLoad(s, u.row))
		units = append(units, u)
	}
	for _, f := range seats {
		if friendDealable(s, f) {
			g := f
			g.ReadsFirst = 0
			_, width := friendRoom(g)
			add(&rebalanceUnit{name: f.Name, row: FriendRow(f.Name), friend: true, seat: f, width: width, cost: friendCostRank(f)})
		}
	}
	if !s.FleetOff() {
		// a quiet member is dealt nothing until its quiet ends (fleet_quiet.go), by the
		// rebalance as by the deal
		for _, m := range notQuiet(s, s.UpMembers()) {
			add(&rebalanceUnit{name: m, row: m, width: s.Width(m), cost: costRankFleet})
		}
	}
	idle := false
	for _, u := range units {
		idle = idle || u.idle() > 0
	}
	moved := map[string]bool{}
	if !idle {
		p.Units = append(p.Units, rebalanceReturns(s, seats, units, moved, who)...)
		return p
	}
	from, work := map[string]*rebalanceUnit{}, map[string]*Card{}
	var prims []*Card
	for _, g := range units {
		if g.width <= 0 || g.load <= g.width {
			continue // no overflow: its own free lanes take what it holds
		}
		for _, wc := range s.Fleet.Cell(g.row, Ready) {
			pr := s.Work.Placed(wc.F("primary"))
			if wc.F("kind") != "work" || pr == nil || pr.Col != Working || pr.F("work") != wc.ID || IsSentinel(pr) ||
				OnlyFriend(pr) || StreamHeld(s, pr.Row) || len(Bench(pr)) > 0 || laneRunsIt(s, seats, pr, wc) != "" ||
				wc.F(FieldRoute) == RoutePin {
				continue
			}
			if name, ok := FriendCard(pr); ok && g.friend && name == g.name {
				continue // its pin is honoured where it sits
			}
			if g.friend && friendStarted(s, g.seat, wc) {
				continue
			}
			if _, rests := cardRest(s, wc); rests {
				continue // the deal withdraws it (restWithdrawals)
			}
			from[pr.ID], work[pr.ID] = g, wc
			prims = append(prims, pr)
		}
	}
	ri := routeIndexesOf(s)
	declared := map[string]bool{}
	for _, pr := range ladderOrder(dealOrder(s, prims)) {
		g, wc := from[pr.ID], work[pr.ID]
		if g.load <= g.width {
			continue // its overflow has gone: the rest its own lanes take
		}
		tier := s.DealTier(pr)
		to := rebalanceTo(s, units, g, pr, wc, tier)
		if to == nil {
			continue
		}
		to.load++
		g.load--
		moved[wc.ID] = true
		p.Units = append(p.Units, rebalanceMove(s, &p, declared, ri, g, to, pr, wc, tier, who))
	}
	ri.write(&p)
	p.Units = append(p.Units, rebalanceReturns(s, seats, units, moved, who)...)
	return p
}

// rebalanceReturns is the dealt cards the rebalance returns to the pool (tla/DealCost.tla,
// Return), each withdrawn (withdrawUnit) and its primary back to ready for the deal's stack
// part: on each unit, its ready work cards past its cap, DealAhead times its width, the newest
// first (the owner, 2026-10-10: "then go to ready overflow up to 2X"; the stack is a
// reservation the next tick may revoke, never a hold). A friend down or held is no unit: her
// cards are the hold's and the stall ladder's, as before. A card a lane runs
// (laneRunsIt), one she started (friendStarted), a hard pin (OnlyFriend: no other worker may
// take it), a sentinel's, a read, a bench card and one of a held stream stay. moved is the
// cards the rebalance moved already this plan: none is returned too.
func rebalanceReturns(s *Snapshot, seats []FriendSeat, units []*rebalanceUnit, moved map[string]bool, who string) []Unit {
	var out []Unit
	returnable := func(wc *Card, seat *FriendSeat) *Card {
		pr := s.Work.Placed(wc.F("primary"))
		if moved[wc.ID] || wc.F("kind") != "work" || pr == nil || pr.Col != Working || pr.F("work") != wc.ID ||
			IsSentinel(pr) || OnlyFriend(pr) || StreamHeld(s, pr.Row) || len(Bench(pr)) > 0 || laneRunsIt(s, seats, pr, wc) != "" {
			return nil
		}
		if seat != nil && friendStarted(s, *seat, wc) {
			return nil
		}
		return pr
	}
	give := func(wc *Card, why string) {
		moved[wc.ID] = true
		out = append(out, withdrawUnit(s, wc, nil, nil, NReturnedToPool, who, fmt.Sprintf("returned %s from %s to the pool: %s", wc.ID, wc.Row, why)))
	}
	for _, u := range units {
		var seat *FriendSeat
		if u.friend {
			seat = &u.seat
		}
		var ready []*Card
		for _, wc := range s.Fleet.Cell(u.row, Ready) {
			if wc.F("kind") == "work" && !moved[wc.ID] && (seat == nil || !friendStarted(s, *seat, wc)) {
				ready = append(ready, wc)
			}
		}
		excess := len(ready) - DealAhead*u.width
		if excess <= 0 {
			continue
		}
		SortCards(ready)
		for i := len(ready) - 1; i >= 0 && excess > 0; i-- {
			if returnable(ready[i], seat) != nil {
				give(ready[i], fmt.Sprintf("its ready %d is past its cap %d (twice its width %d)", len(ready), DealAhead*u.width, u.width))
				excess--
			}
		}
	}
	return out
}

// rebalanceTo is the unit the card goes to, nil when none may take it (Rebalance).
func rebalanceTo(s *Snapshot, units []*rebalanceUnit, g *rebalanceUnit, pr, wc *Card, tier string) *rebalanceUnit {
	want := slices.Index(capLadder, tier)
	gone := Split(wc.F(FieldRebalancedFrom))
	var may []*rebalanceUnit
	dist := map[*rebalanceUnit]int{}
	for _, u := range units {
		if u == g || u.idle() <= 0 || slices.Contains(gone, u.row) {
			continue
		}
		if name, ok := FriendCard(pr); ok && name == "" && g.friend && !u.friend {
			continue // WHO: friend on a friend's row: its pin is honoured, it stays with the friends
		}
		d := 0
		if u.friend {
			if !friendTakes(s, u.seat, tier) || slices.Contains(friendsLeft(wc), u.name) {
				continue
			}
			top := -1
			for _, t := range friendTiers(u.seat) {
				top = max(top, slices.Index(capLadder, t))
			}
			d = top - want
		} else {
			if !s.FleetTakes(tier) || slices.Contains(StagingRefusers(wc), u.name) {
				continue
			}
			if _, _, why, byFriend := s.routeOf(pr, wc, nil); why != "" || byFriend {
				continue
			}
		}
		if want < 0 || d < 0 || d > 1 {
			continue // own tier, or one up as overflow: never more, never below
		}
		dist[u] = d
		may = append(may, u)
	}
	if len(may) == 0 {
		return nil
	}
	slices.SortStableFunc(may, func(a, b *rebalanceUnit) int {
		return cmp.Or(cmp.Compare(a.cost, b.cost), cmp.Compare(dist[a], dist[b]), cmp.Compare(b.idle(), a.idle()), cmp.Compare(a.name, b.name))
	})
	return may[0]
}

// rebalanceMove is the card moved from g to to at its next generation, its old row named on
// it and in the note.
func rebalanceMove(s *Snapshot, p *Plan, declared map[string]bool, ri routeIndexes, g, to *rebalanceUnit, pr, wc *Card, tier, who string) Unit {
	set := nextGen(wc, to.row, s.Now)
	unset := []string{FieldFriendDeadline}
	set[FieldRebalancedFrom] = strings.Join(append(Split(wc.F(FieldRebalancedFrom)), g.row), ",")
	if g.friend {
		set[FieldFriendsLeft] = strings.Join(append(friendsLeft(wc), g.name), ",")
	}
	var prim map[string]string
	switch {
	case to.friend:
		// a friend runs her own model: the fleet route comes off
		unset = append(unset, FieldRoute, FieldModel, FieldTokens, FieldUSD, FieldHarness, FieldDeadline)
		prim = tierNowSet(pr, tier)
		if !s.Fleet.HasRow(to.row) && !declared[to.row] {
			p.Rows = append(p.Rows, RowAdd{Fleet, to.row})
			declared[to.row] = true
		}
	case g.friend:
		// off a friend's row onto a machine: a route of its tier, as a deal draws one
		route, _, _, _ := s.routeOf(pr, wc, ri)
		w, pm := splitRoute(route)
		s.dealDeadline(to.name, w)
		for k, v := range w {
			if v != "" {
				set[k] = v
			}
		}
		prim = pm
	default:
		s.movedDeadline(to.name, wc, set) // machine to machine: its route kept, as the level's
	}
	changes := []Change{change(Fleet, moveEntry(wc, to.row, Ready, set, unset...))}
	if len(prim) > 0 {
		changes = append(changes, change(Work, setEntry(pr, prim)))
	}
	what := fmt.Sprintf("rebalanced %s from %s to %s", wc.ID, g.row, to.row)
	n := happened(NRebalanced, pr.Row, s.Now, pr.ID)
	n.Who, n.What = who, what
	return Unit{Key: pr.ID, Stream: pr.Row, Changes: changes, Notes: []Note{n},
		Moved: fmt.Sprintf("%s gen=%d (past its lanes; an idle lane takes it)", what, wc.Int("gen")+1)}
}
