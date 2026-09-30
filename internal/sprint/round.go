package sprint

import (
	"math"
	"slices"
)

// The deal goes round the fleet and the ask goes round the readers (errata 3,
// amendment 5): each keeps a rolling index into its names, in name order,
// modulo their number, that moves on with every card dealt (every read asked)
// past the name it went to. A deal scans the members from the index,
// wrapping, and takes the first that is up and has room for the card (a
// member down or full is skipped, and the index moves past the member dealt
// to, so a skipped member's turn is not given to its neighbour twice); an ask
// scans the readers from the index and takes the first that has no read card
// at the attempt and is not already chosen. So with every member idle
// consecutive deals go m1, m2, ..., mN, m1, ..., and over a run every member's
// done is within a few of the others; the shortest queue with its ties broken
// by name, which this replaces, gives every card of an idle fleet to the first
// members in name order and the tail of the fleet none.
//
// Every placement of a card on a member goes through the deal's index and
// moves it, first attempts and redeals and levelling alike (errata 3,
// amendment 5): the deal (R6, T3) and the withdrawn card dealt again, the
// rework of failed or broken work (R10, the coordinator's rework), the cards of
// a member that goes down (R2, fleet down), the levelling (R7, T4, fleet up
// and level) and the replacement of a late card (R11). A rework skips the
// member of the attempt it sends back while another up member has room. The
// model is tla/SprintEvents.tla: RoundOne from dcur in PlanDeal, PlanDown,
// PlanRework and PlanLate, and dcur moved by each of their effects; the
// reference model's PlaceOn, ReworkChoice and levelRound.
//
// The index is a property of the fleet table (the deal's, deal_index) and of
// the readers table (the ask's, ask_index), the owner's ruling ("a property
// of the reader table and the fleet table respectively; not a property of the
// work stream"): the name the index is past, read with the table and written
// by the step that deals (asks) in the same atomic batch as its cards, guarded
// on the value the step read (Plan.Props; the table layer's properties, L1
// contract amendment of 2026-09-30). So it is atomic with the deal and
// survives a stop and a start of the machine and a new loop, and a clear
// starts the next epoch at the first name. The model is
// tla/SprintEvents.tla: dcur and acur, moved by the deal's and the ask's
// effects, and PlanDeal's and PlanAsk's choice from them (RoundAssign,
// RoundTwo).
const (
	// PropDealIndex is the fleet table's property: the member the last card
	// was dealt to.
	PropDealIndex = "deal_index"
	// PropAskIndex is the readers table's property: the reader the last read
	// was asked of round the readers.
	PropAskIndex = "ask_index"
)

// round is a rolling index into names, in name order: the next name is the
// first from the index, wrapping, that is acceptable.
type round struct {
	order []string
	at    int    // the index: the place in order the next scan starts at
	table string // the table whose property holds it
	name  string // the property
	read  string // the value the step read
	had   bool   // whether the table had the property
	last  string // the name the index moved past in this plan, "" when none
}

// newRound is the index over the names just past last (the first name above
// it in name order, so a name since removed still places it).
func newRound(names []string, last string) *round {
	order := append([]string(nil), names...)
	slices.Sort(order)
	r := &round{order: order}
	if last != "" {
		i, found := slices.BinarySearch(order, last)
		if found {
			i++
		}
		r.at = i
	}
	return r
}

// scan is the first name from the index, wrapping, that ok accepts; "" when
// none does.
func (r *round) scan(ok func(string) bool) string {
	n := len(r.order)
	for i := 0; i < n; i++ {
		if x := r.order[(r.at+i)%n]; ok(x) {
			return x
		}
	}
	return ""
}

// picks is up to k names from the index, each scan starting past the name the
// one before took, that ok accepts and that are not taken already; fewer when
// fewer are acceptable. It does not move the index: an ask that takes them
// moves it past each (moved), in order.
func (r *round) picks(k int, taken []string, ok func(string) bool) []string {
	var out []string
	at := r.at
	n := len(r.order)
	for len(out) < k {
		pick := -1
		for i := 0; i < n; i++ {
			j := (at + i) % n
			if x := r.order[j]; ok(x) && !contains(taken, x) && !contains(out, x) {
				pick = j
				break
			}
		}
		if pick < 0 {
			break
		}
		out = append(out, r.order[pick])
		at = pick + 1
	}
	return out
}

// moved moves the index past name: the next scan starts at the name after it.
func (r *round) moved(name string) {
	if i, found := slices.BinarySearch(r.order, name); found {
		r.at = i + 1
		r.last = name
	}
}

// member is the member the next card goes to: the first from the index that is
// up and holds fewer cards (q) than its room (room; a member with none there
// has no room), the avoid member only when no other is; "" when none has room.
// It does not move the index. The room of a placement on a member is its
// width (width.go, errata 3 amendment 9), q its work cards held, ready and
// working (memberLoads).
func (r *round) member(up []string, q, room map[string]int, avoid string) string {
	isUp := make(map[string]bool, len(up))
	for _, x := range up {
		isUp[x] = true
	}
	ok := func(x string) bool { return isUp[x] && q[x] < room[x] }
	m := r.scan(func(x string) bool { return x != avoid && ok(x) })
	if m == "" && avoid != "" && ok(avoid) {
		m = avoid
	}
	return m
}

// next is the member a card placed on the fleet goes to (errata 3 amendment
// 5: every placement, first attempts and redeals and levelling alike, goes
// round the fleet and moves the index): the first from the index that is up
// and below its room, the avoid member only when no other has room; with
// spill, when none has room, the first up from the index, the avoid member
// only when it is the one up. "" when none. It neither moves the index nor
// counts the card: the caller that places it moves the index past it (moved)
// and counts it (q).
func (r *round) next(up []string, q, room map[string]int, avoid string, spill bool) string {
	m := r.member(up, q, room, avoid)
	if m == "" && spill {
		m = r.member(up, q, roomOf(up, math.MaxInt), avoid)
	}
	return m
}

// roomOf is the same room, most, for every member of up.
func roomOf(up []string, most int) map[string]int {
	out := make(map[string]int, len(up))
	for _, x := range up {
		out[x] = most
	}
	return out
}

// levelTo is where the level moves the newest card of the longest queue, and
// moves the index past it (errata 3 amendment 5): the next member round the
// fleet from the index that is below its width (held, the work cards it
// holds, under widths: a member at its width takes no more, errata 3
// amendment 9) and whose queue (n) is below the up members' mean rounded
// down, or, when none such is below it, at it. A member that receives is
// never the longest while two queues differ by more than one, so no card is
// moved twice, and every move takes a card from a queue at least two longer
// than the one it joins. "" when none.
func (r *round) levelTo(up []string, n, held, widths map[string]int) string {
	if len(up) == 0 {
		return ""
	}
	total := 0
	for _, m := range up {
		total += n[m]
	}
	mean := total / len(up)
	isUp := make(map[string]bool, len(up))
	for _, x := range up {
		isUp[x] = true
	}
	open := func(x string) bool { return isUp[x] && held[x] < widths[x] }
	to := r.scan(func(x string) bool { return open(x) && n[x] < mean })
	if to == "" {
		to = r.scan(func(x string) bool { return open(x) && n[x] <= mean })
	}
	if to != "" {
		r.moved(to)
	}
	return to
}

// dealRoundWith is the deal's rolling index over the fleet's members and the
// names given that it has no row of yet (a member added by the step that
// places cards on it).
func dealRoundWith(s *Snapshot, extra ...string) *round {
	names := append([]string(nil), s.Fleet.Rows()...)
	for _, x := range extra {
		if !contains(names, x) {
			names = append(names, x)
		}
	}
	return tableRound(s.Fleet, PropDealIndex, names)
}

// tableRound is the rolling index a table's property holds over names.
func tableRound(t *Table, name string, names []string) *round {
	last, had := t.Prop(name)
	r := newRound(names, last)
	r.table, r.name, r.read, r.had = t.Name, name, last, had
	return r
}

// dealRound is the deal's rolling index over the fleet's members.
func dealRound(s *Snapshot) *round { return tableRound(s.Fleet, PropDealIndex, s.Fleet.Rows()) }

// askRound is the ask's rolling index over the readers.
func askRound(s *Snapshot) *round { return tableRound(s.Readers, PropAskIndex, s.Readers.Rows()) }

// roundMoves are the names the units of a plan moved an index past, by unit
// key ("" when the unit did not move it: an ask of the readers the primary
// names).
type roundMoves map[string]string

// roundWrites writes where a plan's kept units left an index: the table's
// property set to the name the last kept unit that moved it moved it past,
// guarded on the value the step read, in the step's batch of that table.
func roundWrites(p *Plan, r *round, moves roundMoves) {
	last := ""
	for _, u := range p.Units {
		if m := moves[u.Key]; m != "" {
			last = m
		}
	}
	if last == "" {
		return
	}
	p.Props = append(p.Props, PropWrite{Table: r.table, Name: r.name, Value: last, Was: r.read, WasAbsent: !r.had})
}
