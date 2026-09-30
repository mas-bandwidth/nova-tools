package refmodel

// The machine, the tick, sentinels' release and clear: from the spec
// (sections 13, 14 and 16), not yet in the model. The model's Next lets any
// enabled action happen at any time; the spec's machine runs the mechanical
// ones (Resolve, Start, Ask, FleetUp's levelling, and Resume on a landed
// cross need) as the parts of one tick, in a fixed order.

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
// before left: resolve (T1), resume (T7), deal (T3), level (T4), ask (T2), and
// done (R15, errata 3 amendment 6), which stops the machine on a done sprint.
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
	n := s.Clone()
	n.tickResolve()
	n.tickResume()
	if err := n.tickDeal(ch.Deal); err != nil {
		return s, err
	}
	lv, err := n.level(ch.Level)
	if err != nil {
		return s, err
	}
	n = lv
	if err := n.tickAsk(ch.Ask); err != nil {
		return s, err
	}
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
// turn, streams in name order, each stream's oldest first by score), each to the up
// next member round the fleet (NextMember), no ready queue longer than
// MaxReadyPerMember; with no member up and primaries to deal, the no-member
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
	ready = n.streamTurns(ready)
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
			if n.RL(m) < MaxReadyPerMember {
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
		next, err := Start(*n, p, m, MaxReadyPerMember)
		if err != nil {
			return err
		}
		*n = next
	}
	return nil
}

// tickAsk is T2: two different readers for each primary in review with no
// read card, whose work did not fail, in work order.
func (n *State) tickAsk(choice map[string][]string) error {
	var review []string
	for id, p := range n.Primaries {
		if p.State == Review && !n.Failed(id) && len(n.LiveReadsOf(id)) == 0 {
			review = append(review, id)
		}
	}
	n.sortByScore(review)
	for _, p := range review {
		two := choice[p]
		if two == nil {
			two = n.defaultPair(p)
		}
		next, err := Ask(*n, p, two)
		if err != nil {
			return err
		}
		*n = next
	}
	return nil
}

func (s State) defaultPair(p string) []string {
	if pair := s.Primaries[p].Pair; len(pair) > 0 {
		return append([]string(nil), pair...)
	}
	return s.NextReaders(p, 2)
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
		if !ok || p.Kind != KindSentinel || p.State != Waiting || !p.Reached {
			return s, refuse("%s is not a reached sentinel", id)
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
	for m, st := range s.Members {
		n.Members[m] = st
	}
	for st := range s.Streams {
		n.Streams[st] = Stream{State: SWaiting}
	}
	n.Epoch = s.Epoch + 1
	n.Machine = Stopped
	return n, nil
}
