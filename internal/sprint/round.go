package sprint

import (
	"slices"
	"strconv"
	"strings"
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
// a member that goes down (R2, fleet down and the tick's presence) and the
// levelling (R7, T4, fleet up and level). A rework skips the
// member of the attempt it sends back while another up member has room. The
// model is tla/SprintEvents.tla: RoundOne from dcur in PlanDeal, PlanDown,
// PlanRework and PlanLate, and dcur moved by each of their effects; the
// reference model's PlaceOn, ReworkChoice and levelRound.
//
// The index is a property of the fleet table (the deal's, deal_index) and of
// the readers table (the ask's, ask_index), the owner's ruling ("a property
// of the reader table and the fleet table respectively; not a property of the
// work stream"): the index's counter (round), read with the table and written
// by the step that deals (asks) in the same atomic batch as its cards, guarded
// on the value the step read (Plan.Props; the table layer's properties, L1
// contract amendment of 2026-09-30). So it is atomic with the deal and
// survives a stop and a start of the machine and a new loop, and a clear
// starts the next epoch at counter 0, the first name. The model is
// tla/SprintEvents.tla: dcur and acur, moved by the deal's and the ask's
// effects, and PlanDeal's and PlanAsk's choice from them (RoundAssign,
// RoundTwo).
const (
	// PropDealIndex is the fleet table's property: the deal's counter, a
	// decimal uint64 (round).
	PropDealIndex = "deal_index"
	// PropAskIndex is the readers table's property: the ask's counter round
	// the readers, a decimal uint64 (round).
	PropAskIndex = "ask_index"
	// PropStreamIndex is the work table's property of the deal: its counter
	// round the streams, moved past the stream of each primary dealt
	// (streamTurns). PropAskStreamIndex (the readers table's: the ask is the
	// readers' update) and
	// PropAcceptStreamIndex are the ask's and the accept's: each step that
	// takes cards across the streams keeps its own, so that one step's move
	// never resets another's rotation (a shared index moved by an ask of one
	// stream's card sends the next deal back to the stream after it, every
	// tick, and a third stream waits).
	PropStreamIndex       = "stream_index"
	PropAskStreamIndex    = "stream_index_ask"
	PropAcceptStreamIndex = "stream_index_accept"
)

// round is a rolling index into names, in name order: the next name is the
// first from the index, wrapping, that is acceptable.
//
// The index is a counter (the owner's form, errata 3 amendment 5: "The modulo
// index can and does start at zero, it's just that past that point it can be
// a uint64 that increments with each step, and then is modulo by count"): a
// uint64 that starts at 0 with the epoch and goes up with every placement, the
// place a scan starts at the counter modulo the number of names. A placement
// on a name moves the counter past it: by one when the name is the one at the
// counter, and past each name skipped before it (a member down or full), so a
// skipped name's turn is not given to its neighbour twice. The counter is
// never a name: it is written as a decimal with the step that moves it,
// persists across plans, parts, ticks, stops and loops, and only a clear, which
// starts the next epoch's table with no property, resets it. The model is
// tla/SprintEvents.tla, dcur and acur: the counter modulo the number of names
// is the model's place in the ring.
type round struct {
	order []string
	count uint64 // the counter: placements made, and the names skipped before them
	table string // the table whose property holds it
	name  string // the property
	read  string // the value the step read
	had   bool   // whether the table had the property
	steps bool   // a unit's moves are the steps it took, a decimal (a route index, route.go)
}

// newRound is the index over names at the counter a table's property holds.
// A property that is not a counter (a name, as a store written before the
// counter holds it) places the index just past that name (the first name above
// it in name order, so a name since removed still places it), at the counter
// of that place.
func newRound(names []string, value string) *round {
	order := append([]string(nil), names...)
	slices.Sort(order)
	return &round{order: order, count: roundCount(order, value)}
}

// roundCount is the counter a property's value holds over order: its decimal,
// 0 when it has none, and for a name the place just past it.
func roundCount(order []string, value string) uint64 {
	if value == "" {
		return 0
	}
	if n, err := strconv.ParseUint(value, 10, 64); err == nil {
		return n
	}
	i, found := slices.BinarySearch(order, value)
	if found {
		i++
	}
	return uint64(i)
}

// start is the place in order the next scan starts at: the counter modulo the
// number of names.
func (r *round) start() int {
	if len(r.order) == 0 {
		return 0
	}
	return int(r.count % uint64(len(r.order)))
}

// value is the counter as the table's property holds it.
func (r *round) value() string { return strconv.FormatUint(r.count, 10) }

// scan is the first name from the index, wrapping, that ok accepts; "" when
// none does.
func (r *round) scan(ok func(string) bool) string {
	n := len(r.order)
	for i := 0; i < n; i++ {
		if x := r.order[(r.start()+i)%n]; ok(x) {
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
	at := r.start()
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

// moved moves the index past name: the counter goes up by one for the
// placement and by one for each name it passed over to reach name, so the next
// scan starts at the name after it (errata 3 amendment 5, the owner's form).
func (r *round) moved(name string) {
	if i, found := slices.BinarySearch(r.order, name); found {
		n := len(r.order)
		r.count += uint64((i-r.start()+n)%n) + 1
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
// and below its room, the avoid member only when no other has room; "" when
// none has room (a card is never placed past a width: tla/DirtyTick.tla,
// WidthRespected). It neither moves the index nor counts the card: the caller
// that places it moves the index past it (moved) and counts it (q).
func (r *round) next(up []string, q, room map[string]int, avoid string) string {
	return r.member(up, q, room, avoid)
}

// levelTo is where the level moves the newest card of the longest queue, and
// moves the index past it (errata 3 amendment 5): the next member round the
// fleet from the index that is below its room (held, the work cards it
// holds, under widths: DealAhead times its width, width.go) and whose backlog
// (n, which may be below zero: level) is below the up members' mean rounded
// down, or, when none such is below it, at it. A member that receives is
// at least two below the source's backlog. Each move therefore strictly
// decreases the sum of squared backlogs, even when staging refusals prevent
// reaching the shortest queue (tla/Level.tla, PotentialFalls). A member of
// avoid (the card's StagingRefusers) is never the target. "" when none, and the
// index does not move.
func (r *round) levelTo(up []string, n, held, widths map[string]int, from string, avoid []string) string {
	if len(up) == 0 {
		return ""
	}
	total := 0
	for _, m := range up {
		total += n[m]
	}
	mean := total / len(up)
	if total < 0 && total%len(up) != 0 {
		mean-- // rounded down, below zero too
	}
	isUp := make(map[string]bool, len(up))
	for _, x := range up {
		isUp[x] = true
	}
	open := func(x string) bool {
		return isUp[x] && held[x] < widths[x] && n[from]-n[x] > 1 && !contains(avoid, x)
	}
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
	names := s.Members()
	for _, x := range extra {
		if x != "" && !contains(names, x) {
			names = append(names, x)
		}
	}
	return tableRound(s.Fleet, PropDealIndex, names)
}

// tableRound is the rolling index a table's property holds over names.
func tableRound(t *Table, name string, names []string) *round {
	value, had := t.Prop(name)
	r := newRound(names, value)
	r.table, r.name, r.read, r.had = t.Name, name, value, had
	return r
}

// dealRound is the deal's rolling index over the fleet's members.
func dealRound(s *Snapshot) *round { return tableRound(s.Fleet, PropDealIndex, s.Members()) }

// askRound is the ask's rolling index over the readers.
func askRound(s *Snapshot) *round { return tableRound(s.Readers, PropAskIndex, s.Readers.Rows()) }

// The streams take turns (the owner's ruling of 2026-09-30, errata 3
// amendment 10: "we should deal fairly from each work stream, perhaps with
// ... another index in that table"): every step that takes cards across the
// streams (the deal, and the withdrawn card dealt again with it; the ask; the
// accept) takes one card from each stream in turn, starting at the stream
// past its rolling index on the work table (stream_index for the deal,
// stream_index_ask and stream_index_accept), wrapping, within a stream by
// work order, and moves its index past the stream of the last card it took,
// written with its cards and guarded on the value it read, as the deal's and
// the ask's member and reader indexes are. A stream with nothing to take for that
// step is skipped and costs no turn. So consecutive steps do not always start
// at the first stream: a step that takes k cards over n streams gives each
// k/n, give or take one, and the one a step gives the extra card to is the
// last served, so the next step starts past it. The index survives a stop and
// a start of the machine; a clear starts the next epoch at the first stream.

// askStreamRound is the ask's rolling index over the streams, a property of
// the readers table: the ask is the readers' update, and only the pump writes
// the work table while the machine runs (errata 3 amendment 12).
func askStreamRound(s *Snapshot) *round {
	if s.Work == nil || s.Readers == nil {
		return newRound(nil, "")
	}
	return tableRound(s.Readers, PropAskStreamIndex, s.Work.Rows())
}

// streamRound is a step's rolling index over the streams (the work table's
// rows), the work table's property name.
func streamRound(s *Snapshot, name string) *round {
	if s.Work == nil {
		return newRound(nil, "")
	}
	return tableRound(s.Work, name, s.Work.Rows())
}

// streamTurns is the cards in stream turns from the index: one card of each
// stream in turn, the streams from the first past the index, wrapping, a
// stream with no card left skipped; within a stream in work order (SortCards).
// A card of a stream the index does not name takes its turn after them, its
// streams in name order. It neither moves the index nor counts a card: the
// step that takes them moves the index past the last it took (streamMoves).
func streamTurns(cards []*Card, r *round) []*Card {
	by := map[string][]*Card{}
	known := map[string]bool{}
	var order []string
	n := len(r.order)
	for i := 0; i < n; i++ {
		st := r.order[(r.start()+i)%n]
		order = append(order, st)
		known[st] = true
	}
	var extra []string
	for _, c := range cards {
		if !known[c.Row] {
			known[c.Row] = true
			extra = append(extra, c.Row)
		}
		by[c.Row] = append(by[c.Row], c)
	}
	slices.Sort(extra)
	order = append(order, extra...)
	for _, st := range order {
		SortCards(by[st])
	}
	out := make([]*Card, 0, len(cards))
	for turn := 0; len(out) < len(cards); turn++ {
		for _, st := range order {
			if turn < len(by[st]) {
				out = append(out, by[st][turn])
			}
		}
	}
	return out
}

// eligibleTurns is the cards a step may take, in stream turns from the index:
// the ones eligible says nothing against, so a card the step cannot take
// costs its stream no turn.
func eligibleTurns(cards []*Card, eligible func(*Card) string, r *round) []*Card {
	var ok []*Card
	for _, c := range cards {
		if eligible(c) == "" {
			ok = append(ok, c)
		}
	}
	return streamTurns(ok, r)
}

// streamMoves is the stream each unit of a plan took its card from, by the
// unit's key: the plan's kept units move the index past the stream of the last
// of them (roundWrites).
func streamMoves(p Plan, streamOf func(key string) string) roundMoves {
	moves := roundMoves{}
	for _, u := range p.Units {
		if st := streamOf(u.Key); st != "" {
			moves[u.Key] = st
		}
	}
	return moves
}

// streamIndexWrite moves the work table's stream index past the stream of the
// last kept unit of the plan that took a primary across the streams: primary
// is the primary a unit's key names (nil when it names none).
func streamIndexWrite(p *Plan, r *round, primary func(key string) *Card) {
	roundWrites(p, r, streamMoves(*p, func(key string) string {
		if c := primary(key); c != nil {
			return c.Row
		}
		return ""
	}))
}

// roundMoves are the names the units of a plan moved an index past, by unit
// key, in the order the unit moved it past them, joined by commas ("" when the
// unit did not move it: an ask of the readers the primary names).
type roundMoves map[string]string

// roundWrites writes where a plan's kept units left an index: the counter the
// step read moved past the names of each kept unit, in the plan's order, so it
// goes up with every placement the step makes (a unit the plan dropped moves
// it no more), written as the table's property, guarded on the value the step
// read, in the step's batch of that table (errata 3 amendment 5, the owner's
// form; tla/SprintEvents.tla dcur and acur).
func roundWrites(p *Plan, r *round, moves roundMoves) {
	if r == nil || len(moves) == 0 {
		return
	}
	p.rounds = append(p.rounds, roundRecord{r, moves})
	roundWrite(p, r, moves)
}

// roundRecord is an index a plan moved and the names each of its units moved
// it past.
type roundRecord struct {
	r     *round
	moves roundMoves
}

// rewriteRounds makes a plan's index writes again from its units: after a
// unit is dropped, each index is up by the placements the kept units make
// and the names they pass over, never by the dropped one's (errata 3, the
// form of the index: one a placement made).
func rewriteRounds(p *Plan) {
	var props []PropWrite
	for _, pw := range p.Props {
		mine := false
		for _, rr := range p.rounds {
			mine = mine || rr.r.table == pw.Table && rr.r.name == pw.Name
		}
		if !mine {
			props = append(props, pw)
		}
	}
	p.Props = props
	for _, rr := range p.rounds {
		roundWrite(p, rr.r, rr.moves)
	}
}

// roundWrite is roundWrites' write, from the plan's units as they stand.
func roundWrite(p *Plan, r *round, moves roundMoves) {
	w := &round{order: r.order, count: roundCount(r.order, r.read)}
	moved := false
	for _, u := range p.Units {
		m := moves[u.Key]
		if m == "" {
			continue
		}
		if r.steps {
			n, _ := strconv.ParseUint(m, 10, 64)
			w.count += n
		} else {
			for _, x := range strings.Split(m, ",") {
				w.moved(x)
			}
		}
		moved = true
	}
	if !moved {
		return
	}
	p.Props = append(p.Props, PropWrite{Table: r.table, Name: r.name, Value: w.value(), Was: r.read, WasAbsent: !r.had})
}

// joinMoves is a unit's moves with one more name it moved an index past.
func joinMoves(moves, name string) string {
	if moves == "" {
		return name
	}
	return moves + "," + name
}
