package refmodel

import "maps"

// The machine, the tick, sentinels' release and clear: from the spec
// (sections 13, 14 and 16), not yet in the model. The model's Next lets any
// enabled action happen at any time; the spec's machine runs the mechanical
// ones (Resolve, Start, Accept, Ask, FleetUp's levelling, and Resume on a
// landed cross need) as the parts of one tick, in a fixed order.
//
// The state of the model is the sprint as the next tick's pump will leave the
// work table (the owner's tick, 2026-09-30, errata 3 amendment 12): while the
// machine runs, every step but the pump queues its work-table changes, and the
// engine plans each on the work table with the ones before it applied
// (sprint.WithQueue), so the abstract state of the engine is read the same
// way (the differential harness's observe), and a step's effect on the work
// table is the model's at once. The pump, the tick's first update, is the only
// place a card is taken out of waiting or ready or accepted out of review.

// TickChoices is the choices a tick makes: the member each primary is dealt
// to, the member each levelled card moves to, and the two readers each
// primary is asked of. A primary the choices do not name is dealt or asked by
// the first allowed choice in row order.
type TickChoices struct {
	Deal  map[string]string
	Level map[string]string
	Ask   map[string][]string
}

// SetMachine is start and stop (spec section 14): the machine's state; the
// state it has already changes nothing. From the spec, not yet in the model.
func SetMachine(s State, running bool) (State, error) {
	n := s.Clone()
	n.Machine = Stopped
	if running {
		n.Machine = Running
	}
	return n, nil
}

// Tick is one tick of the machine (spec section 14): a STOPPED machine moves
// nothing; a RUNNING one runs its parts in order, each on the state the one
// before left: the start, the fleet's level (T4) and the readers' (levelReads),
// once; then the four tables' updates in the owner's order: the work pump's
// resolve (T1), deal (T3) and accept (the owner's ruling of 2026-09-30,
// "accept is mechanical"), the readers' ask (T2), the merge's resume (T7); and
// then done (R15, errata 3 amendment 6), which stops
// the machine on a done sprint. The tables the updates dirty are updated again
// until none is; the model's parts dirty no earlier table, so one pass is the
// fixpoint.
// The check, deadline and overdue parts write nothing while the rules hold
// and no clock deadline passes, which the differential test keeps so. From
// the spec, not yet in the model (the model's Resolve, Start, FleetUp and Ask
// happen one at a time, in any order).
func Tick(s State, ch TickChoices) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if s.Machine != Running {
		return s, nil
	}
	// the start: the fleet's and the readers' rebalance, once (the owner,
	// 2026-10-01: "just once before tick, rebalance each table.")
	n, err := s.level(ch.Level)
	if err != nil {
		return s, err
	}
	n.levelReads()
	n.tickResolve()
	if err := n.tickDeal(ch.Deal); err != nil {
		return s, err
	}
	if err := n.tickAccept(); err != nil {
		return s, err
	}
	if err := n.tickAsk(ch.Ask); err != nil {
		return s, err
	}
	n.tickResume()
	n.tickDone()
	return n, nil
}

// tickDone is R15 as errata 3 amendment 6 amends it (sprint.TickDone): the
// tick's last part; a sprint done (sprintDone) stops the machine, its note
// addressed to the coordinator, and no judgment opens. A stopped machine does
// not tick, so it says it once for each run that finishes the sprint.
func (n *State) tickDone() {
	if n.Machine == Running && n.sprintDone() {
		n.Machine = Stopped
	}
}

// tickResolve is T1: every stream's waiting cards in score order; a primary
// whose needs are all met moves to ready; a sentinel is never moved and is
// marked reached, its judgment opened; a dropped need is the blocked
// judgment, once.
func (n *State) tickResolve() {
	for _, st := range n.StreamNames() {
		for _, p := range n.StreamOrder(st) {
			pr := n.Primaries[p]
			if pr.State != Waiting {
				continue
			}
			if len(n.DroppedNeeds(p)) > 0 && !n.Open[Judgment{JBlocked, p}] {
				n.open(JBlocked, p)
			}
		}
	}
	n.resolveAll()
}

// tickResume is T7: a stream stopped only because a card needed another
// stream's card, once that card has landed, resumes.
func (n *State) tickResume() {
	for _, st := range n.StreamNames() {
		x := n.Streams[st]
		if x.State != SStopped || x.Cause != CCross {
			continue
		}
		stuck := n.MergeCell(st, Stuck)
		if len(stuck) == 0 {
			continue
		}
		ok := true
		for _, p := range stuck {
			if need := n.Merge[p].Need; need == "" || !n.InWork(need, Landed) {
				ok = false
			}
		}
		if ok {
			*n = n.resumed(st)
		}
	}
}

// tickDeal is T3: ready primaries in stream turns (one from each stream in
// turn, the streams from the first past the deal's stream index, a stream with none
// skipped, each stream's oldest first by score; Start moves the index past
// each primary's stream, errata 3 amendment 10), each to the up
// next member round the fleet (NextMember), no member holding more work
// cards, ready and working, than its Room (DealAhead times its Width, errata 3 amendment 9); with no member up and primaries to deal, the no-member
// judgment once, closed when the condition clears (section 14).
func (n *State) tickDeal(choice map[string]string) error {
	var ready []string
	bound := map[string]bool{}
	for id, p := range n.Primaries {
		if p.State == Ready && p.Kind != KindSentinel {
			if n.AtBound(id) != "" {
				bound[id] = true
				continue
			}
			ready = append(ready, id)
		}
	}
	ready = n.streamTurns(ready, n.StreamLast)
	// the bound's judgment: once per primary at its bound, closed when it
	// is no longer there (reworked, dropped)
	for j := range n.Open {
		if j.Type == JBound && !bound[j.Subject] {
			delete(n.Open, j)
		}
	}
	for j := range n.Acked {
		if j.Type == JBound && !bound[j.Subject] {
			delete(n.Acked, j)
		}
	}
	for p := range bound {
		if j := (Judgment{JBound, p}); !n.Acked[j] {
			n.Open[j] = true
		}
	}
	j := Judgment{JNoMember, "fleet"}
	if len(n.Up()) == 0 && len(ready) > 0 {
		if !n.Acked[j] {
			n.Open[j] = true
		}
	} else {
		delete(n.Open, j)
		delete(n.Acked, j)
	}
	for _, p := range ready {
		var room []string
		for _, m := range n.Up() {
			if n.Held(m) < Room {
				room = append(room, m)
			}
		}
		if len(room) == 0 {
			return nil
		}
		m := choice[p]
		if m == "" {
			m = n.NextMember(room)
		}
		next, err := Start(*n, p, m, Room)
		if err != nil {
			return err
		}
		*n = next
	}
	return nil
}

// tickAsk is T2: readers for each primary in review with no
// read card, whose work did not fail, in stream turns from the ask's stream
// index (ReadsNeeded: one for a flash card, two for a pro card).
func (n *State) tickAsk(choice map[string][]string) error {
	var review []string
	for id, p := range n.Primaries {
		if p.State == Review && !n.Failed(id) && len(n.LiveReadsOf(id)) == 0 {
			review = append(review, id)
		}
	}
	// in stream turns from the ask's stream index (errata 3 amendment 10);
	// Ask moves it past each primary's stream
	review = n.streamTurns(review, n.AskStreamLast)
	for _, p := range review {
		readers := choice[p]
		if readers == nil {
			readers = n.NextReaders(p, n.ReadsNeeded(p))
		}
		next, err := Ask(*n, p, readers)
		if err != nil {
			return err
		}
		*n = next
	}
	return nil
}

// tickAccept is the machine's accept (sprint.TickAccept): every primary in
// review whose work did not fail, with ok reads from ReadsNeeded readers,
// not held (AcceptHeld: its CI red at its head, or returned at its attempt),
// and no merge record but a returned one, moves to merging and into its
// stream's merge queue, in stream turns from the accept's stream index, in one
// step (Accept). The merge is the coordinator's: the machine never lands a
// card.
func (n *State) tickAccept() error {
	var ids []string
	for id, p := range n.Primaries {
		if p.State != Review || n.Failed(id) || !n.Acceptable(id) || n.AcceptHeld(id) != "" {
			continue
		}
		if m, ok := n.Merge[id]; ok && m.Place != Returned {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	next, err := Accept(*n, n.streamTurns(ids, n.AcceptStreamLast))
	if err != nil {
		return err
	}
	*n = next
	return nil
}

// Release is the spec's release (section 16): the coordinator alone lands
// reached sentinels (waiting -> landed, the only step that may), and in the
// same step moves every waiting primary whose needs are all met to ready and
// marks reached every sentinel now due; it closes the judgments open on each
// sentinel; the stream's state follows (section 7: landed when every primary
// of it on the table has landed). From the spec, not yet in the model.
func Release(s State, ids []string, who string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if who != s.Coordinator || who == "" {
		return s, refuse("%s is not the sprint's coordinator", who)
	}
	if len(ids) == 0 {
		return s, refuse("release names nothing")
	}
	for _, id := range ids {
		p, ok := s.Primaries[id]
		if !ok || p.Kind != KindSentinel || p.State != Waiting || !p.Reached && (s.HasBefore(id) || !s.NeedsMet(id)) {
			return s, refuse("%s is not a reached sentinel, nor one with nothing before it", id)
		}
	}
	n := s.Clone()
	for _, id := range ids {
		st := n.Primaries[id].Stream
		x := n.Streams[st]
		x.State = n.streamAfter(st, x.State, []string{id}, nil)
		n.Streams[st] = x
		n.setPrimary(id, func(x *Primary) { x.State = Landed })
		n.closeOn(id)
	}
	n.resolveAll()
	return n, nil
}

// Clear is the spec's clear (section 13): the machine STOPPED, the epoch
// advanced once; at the new epoch every table is empty with the same rows,
// every stream waiting, every member with its status and no work; the
// judgments and the fence are the new epoch's own, empty. The coordinator is
// the sprint's and is kept. From the spec, not yet in the model.
func Clear(s State) (State, error) {
	n := New(s.Readers, s.Order, s.Coordinator)
	maps.Copy(n.Members, s.Members)
	for st := range s.Streams {
		n.Streams[st] = Stream{State: SWaiting}
	}
	n.Epoch = s.Epoch + 1
	n.Machine = Stopped
	return n, nil
}
