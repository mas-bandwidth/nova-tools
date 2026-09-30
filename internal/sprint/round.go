package sprint

import (
	"slices"
	"strconv"
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
// The index is kept in the store, in the step that deals (asks), so that it is
// atomic with the deal and survives a stop and a start of the machine and a new
// loop: the sprint's own keys have no wire in a rule's step (TimeWrites), and
// members and readers are not all on control cards a step writes after its
// first manifest, so it is on the streams' control cards (the merge table,
// which a step writes after the fleet and the readers, ApplyOrder: a deal whose
// first manifest never applies is abandoned whole, the index with it). A step
// that moves the index writes, on the control card of the stream of its last
// card that moved it, the name the index is past (deal_last, ask_last) and a
// sequence one above the highest the read found (deal_seq, ask_seq); the index
// is the last name of the control card with the highest sequence. The model is
// tla/SprintEvents.tla: dcur and acur, moved by the deal's and the ask's
// effects, and PlanDeal's and PlanAsk's choice from them (RoundAssign,
// RoundTwo).
const (
	FieldDealSeq  = "deal_seq"
	FieldDealLast = "deal_last"
	FieldAskSeq   = "ask_seq"
	FieldAskLast  = "ask_last"
)

// dealRoundFields and askRoundFields are what a read names of the streams'
// control cards for the deal's and the ask's index.
var (
	dealRoundFields = []string{FieldDealSeq, FieldDealLast}
	askRoundFields  = []string{FieldAskSeq, FieldAskLast}
)

// round is a rolling index into names, in name order: the next name is the
// first from the index, wrapping, that is acceptable.
type round struct {
	order []string
	at    int    // the index: the place in order the next scan starts at
	seq   int    // the highest sequence the read found
	last  string // the name the index moved past in this plan, "" when none
}

// newRound is the index over the names just past last (the first name above
// it in name order, so a name since removed still places it), at seq.
func newRound(names []string, last string, seq int) *round {
	order := append([]string(nil), names...)
	slices.Sort(order)
	r := &round{order: order, seq: seq}
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

// roundAt is the index a snapshot holds: the last name and the sequence of the
// streams' control card with the highest sequence (the first stream in name
// order on a tie).
func roundAt(s *Snapshot, seqField, lastField string) (last string, seq int) {
	seq = -1
	streams := append([]string(nil), s.Streams()...)
	slices.Sort(streams)
	for _, st := range streams {
		ctl := s.StreamCtl(st)
		if ctl == nil || ctl.F(seqField) == "" {
			continue
		}
		if n := ctl.Int(seqField); n > seq {
			seq, last = n, ctl.F(lastField)
		}
	}
	return last, max(seq, 0)
}

// dealRound is the deal's rolling index over the fleet's members.
func dealRound(s *Snapshot) *round {
	last, seq := roundAt(s, FieldDealSeq, FieldDealLast)
	return newRound(s.Fleet.Rows(), last, seq)
}

// askRound is the ask's rolling index over the readers.
func askRound(s *Snapshot) *round {
	last, seq := roundAt(s, FieldAskSeq, FieldAskLast)
	return newRound(s.Readers.Rows(), last, seq)
}

// roundMove is what a unit moved an index by: the stream whose control card
// carries it and the name the index moved past ("" when the unit did not move
// it: an ask of the readers the primary names).
type roundMove struct{ stream, last string }

// roundWrites writes where a plan's kept units left an index: on the control
// card of the stream of the last kept unit that moved it, the name that unit
// moved it past and the sequence one above the read's, so that one step writes
// one control card, with the deals (asks) that apply.
func roundWrites(p *Plan, s *Snapshot, seqField, lastField string, r *round, moves map[string]roundMove) {
	at := -1
	var mv roundMove
	for i, u := range p.Units {
		if m, ok := moves[u.Key]; ok && m.last != "" {
			at, mv = i, m
		}
	}
	if at < 0 {
		return
	}
	ctl := s.StreamCtl(mv.stream)
	if ctl == nil {
		return
	}
	p.Units[at].Changes = append(p.Units[at].Changes, change(Merge, setEntry(ctl, map[string]string{
		seqField: strconv.Itoa(r.seq + 1), lastField: mv.last})))
}
