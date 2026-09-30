package sprint

import "slices"

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
// up and has fewer than most ready cards, the avoid member only when no other
// is; "" when none has room. It does not move the index.
func (r *round) member(up []string, q map[string]int, most int, avoid string) string {
	m := r.scan(func(x string) bool { return x != avoid && contains(up, x) && q[x] < most })
	if m == "" && avoid != "" && contains(up, avoid) && q[avoid] < most {
		m = avoid
	}
	return m
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
