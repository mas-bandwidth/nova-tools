package refmodel

import (
	"sort"
)

// The line numbers cited are tla/SprintTables.tla at 4bf919875 on the
// branch rowan/sprint-tables-model.

func free(s State) error {
	if s.Pending != "" {
		return refuse("operation %s is pending (D1: Free)", s.Pending)
	}
	return nil
}

// ------------------------------------------------------------------ add

// AddArgs is add's arguments: new ids of one stream, the needs they name,
// whether the one id is a sentinel, and --before or --after a card.
type AddArgs struct {
	Stream        string
	IDs           []string
	Needs         []string
	Sentinel      bool
	Before, After string
}

// Add is SprintTables.tla Add(p) (line 276) for each id in order: admitted
// waiting if a need is not met, else ready; refused when the needs make a
// cycle (NoNeedCycle); the blocked judgment when a need was dropped. scores is
// the choice of each new id's score (the model's Score0, a constant there).
//
// Sentinels by position, add --before/--after, a sentinel pulling ready
// cards back to waiting, and add closing the sprint-done judgment are from
// the spec (sections 8 and 16), not yet in the model.
func Add(s State, a AddArgs, scores map[string]float64) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if a.Stream == "" || len(a.IDs) == 0 {
		return s, refuse("add names no stream or no id")
	}
	if a.Sentinel && len(a.IDs) != 1 {
		return s, refuse("a sentinel is admitted one at a time")
	}
	if a.Before != "" && a.After != "" {
		return s, refuse("--before and --after together")
	}
	seen := map[string]bool{}
	for _, id := range a.IDs {
		if _, ok := s.Primaries[id]; ok || seen[id] {
			return s, refuse("%s is admitted already", id)
		}
		seen[id] = true
	}
	for _, q := range a.Needs {
		if _, ok := s.Primaries[q]; !ok && !seen[q] {
			return s, refuse("need %s names no primary", q)
		}
	}
	anchor := a.Before + a.After
	if anchor != "" {
		ap, ok := s.Primaries[anchor]
		if !ok || ap.State == Off || ap.Stream != a.Stream {
			return s, refuse("%s is not a card of stream %s", anchor, a.Stream)
		}
	}
	n := s.Clone()
	if _, ok := n.Streams[a.Stream]; !ok {
		n.Streams[a.Stream] = Stream{State: SWaiting}
	}
	for _, id := range a.IDs {
		sc, ok := scores[id]
		if !ok {
			return s, badChoice("no score chosen for %s", id)
		}
		if err := n.checkScore(a, id, sc); err != nil {
			return s, err
		}
		needs := addSorted(nil, a.Needs...)
		kind := KindPrimary
		if a.Sentinel {
			kind = KindSentinel
		}
		n.Primaries[id] = Primary{Stream: a.Stream, Kind: kind, State: Waiting, Needs: needs, Score: sc, Attempt: 1}
		if a.Sentinel {
			n.placeSentinel(id)
		} else {
			n.placeBehindSentinel(id)
		}
	}
	// NoNeedCycle: a cycle refuses the whole add.
	for _, id := range a.IDs {
		if n.inCycle(id) {
			return s, refuse("the needs of %s make a cycle", id)
		}
	}
	for _, id := range a.IDs {
		if n.Primaries[id].Kind != KindSentinel && n.NeedsMet(id) {
			n.setPrimary(id, func(p *Primary) { p.State = Ready })
		}
		if len(n.DroppedNeeds(id)) > 0 {
			n.open(JBlocked, id)
		}
	}
	n.closeOn(SprintSubject, JDone)
	return n, nil
}

// checkScore holds a chosen score to the spec's order: a card in line lies
// strictly between its neighbours (section 16); any other lies after every
// card of its stream on the table (section 4: a score is given once, at
// admission, so a later card sits behind).
func (s State) checkScore(a AddArgs, id string, sc float64) error {
	order := s.StreamOrder(a.Stream)
	switch {
	case a.Before != "":
		x := s.Primaries[a.Before].Score
		if !(sc < x) {
			return badChoice("%s --before %s has score %v, not below %v", id, a.Before, sc, x)
		}
		for _, q := range order {
			if q != id && s.Primaries[q].Score < x && !(s.Primaries[q].Score < sc) {
				return badChoice("%s --before %s has score %v, not above its neighbour %s at %v", id, a.Before, sc, q, s.Primaries[q].Score)
			}
		}
	case a.After != "":
		x := s.Primaries[a.After].Score
		if !(sc > x) {
			return badChoice("%s --after %s has score %v, not above %v", id, a.After, sc, x)
		}
		for _, q := range order {
			if q != id && s.Primaries[q].Score > x && !(s.Primaries[q].Score > sc) {
				return badChoice("%s --after %s has score %v, not below its neighbour %s at %v", id, a.After, sc, q, s.Primaries[q].Score)
			}
		}
	default:
		for _, q := range order {
			if q != id && !(s.Primaries[q].Score < sc) {
				return badChoice("%s at the end of %s has score %v, not after %s at %v", id, a.Stream, sc, q, s.Primaries[q].Score)
			}
		}
	}
	return nil
}

// placeSentinel is the spec's section 16 for a sentinel just admitted: it
// waits for every primary of its stream that sorts before it and has not
// landed; the cards behind it that wait wait on it too, the ready ones go
// back to waiting, and those in flight are past the stop and waited for.
// From the spec, not yet in the model.
func (n *State) placeSentinel(id string) {
	me := n.Primaries[id]
	for _, q := range n.StreamOrder(me.Stream) {
		if q == id {
			continue
		}
		qp := n.Primaries[q]
		if qp.State == Landed {
			continue
		}
		if qp.Score < me.Score {
			me.Needs = addSorted(me.Needs, q)
			continue
		}
		switch qp.State {
		case Waiting:
			qp.Needs = addSorted(qp.Needs, id)
		case Ready:
			qp.State = Waiting
			qp.Needs = addSorted(qp.Needs, id)
		default:
			me.Needs = addSorted(me.Needs, q)
		}
		n.Primaries[q] = qp
	}
	n.Primaries[id] = me
}

// placeBehindSentinel is the spec's section 16 for a card just admitted: it
// waits on the latest unlanded sentinel before it, and is a need of every
// unlanded sentinel after it, which it un-reaches. From the spec, not yet in
// the model.
func (n *State) placeBehindSentinel(id string) {
	me := n.Primaries[id]
	latest := ""
	for _, q := range n.StreamOrder(me.Stream) {
		qp := n.Primaries[q]
		if q == id || qp.Kind != KindSentinel || qp.State == Landed {
			continue
		}
		if qp.Score < me.Score {
			latest = q
			continue
		}
		qp.Needs = addSorted(qp.Needs, id)
		if qp.Reached {
			qp.Reached = false
			delete(n.Open, Judgment{JReached, q})
		}
		n.Primaries[q] = qp
	}
	if latest != "" {
		me.Needs = addSorted(me.Needs, latest)
	}
	n.Primaries[id] = me
}

// inCycle is SprintTables.tla InCycle(p, added).
func (s State) inCycle(p string) bool {
	seen := map[string]bool{}
	stack := append([]string(nil), s.Primaries[p].Needs...)
	for len(stack) > 0 {
		q := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if q == p {
			return true
		}
		if seen[q] {
			continue
		}
		seen[q] = true
		stack = append(stack, s.Primaries[q].Needs...)
	}
	return false
}

// ------------------------------------------------------------------ resolve

// Resolve is SprintTables.tla Resolve(p) (line 288): waiting -> ready where
// every need has landed or was waived. A sentinel never moves (spec section
// 16). The blocked judgment for a dropped need is the spec's (section 8,
// "Resolve ... a need that was dropped is a judgment, once"), not in the
// model's Resolve.
func Resolve(s State, p string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if !s.InWork(p, Waiting) {
		return s, refuse("%s is not waiting", p)
	}
	if len(s.DroppedNeeds(p)) > 0 && !s.Open[Judgment{JBlocked, p}] {
		n := s.Clone()
		n.open(JBlocked, p)
		return n, nil
	}
	if s.Primaries[p].Kind == KindSentinel {
		return s, refuse("%s is a sentinel: only release lands it", p)
	}
	if !s.NeedsMet(p) {
		return s, refuse("%s needs %v", p, s.Primaries[p].Needs)
	}
	n := s.Clone()
	n.setPrimary(p, func(x *Primary) { x.State = Ready })
	return n, nil
}

// Waive is SprintTables.tla Waive(p, q) (line 298) for every dropped need of
// p the blocked judgment names: the need counts as met. The model closes the
// blocked judgment when no dropped need is left unwaived. In the spec the
// coordinator waives by ack of the blocked judgment (section 8); Ack calls it.
func Waive(s State, p string, qs []string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if !s.Placedp(p) {
		return s, refuse("%s is not on the table", p)
	}
	n := s.Clone()
	n.setPrimary(p, func(x *Primary) { x.Waived = addSorted(x.Waived, qs...) })
	if len(n.DroppedNeeds(p)) == 0 {
		delete(n.Open, Judgment{JBlocked, p})
	}
	return n, nil
}

// ------------------------------------------------------------------ fleet

// Start is SprintTables.tla Start(p) (line 310): ready -> working; the work
// card of the attempt is dealt to m, which must be an up member with the
// shortest ready queue: cut at generation 1, or, when it was withdrawn, the
// same card dealt again at a new generation (G1, D3). limit, when above zero,
// is the tick's MaxReadyPerMember (spec section 5): only members whose ready
// queue is shorter are dealt to.
func Start(s State, p, m string, limit int) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if !s.InWork(p, Ready) || s.Primaries[p].Kind == KindSentinel {
		return s, refuse("%s is not a ready primary", p)
	}
	up := s.Up()
	if limit > 0 {
		var room []string
		for _, x := range up {
			if s.RL(x) < limit {
				room = append(room, x)
			}
		}
		up = room
	}
	if len(up) == 0 {
		return s, refuse("no up member to deal %s to", p)
	}
	if !s.ShortestIn(m, up) {
		return s, badChoice("%s dealt to %s, whose ready queue (%d) is not the shortest of %v", p, m, s.RL(m), up)
	}
	n := s.Clone()
	pr := n.Primaries[p]
	id := WC(p, pr.Attempt)
	if w, ok := n.Work[id]; ok {
		if w.Place != FWithdrawn {
			return s, badChoice("%s cut a second time (NoCardLostOrTwice)", id)
		}
		w.Place, w.Member, w.Gen = FReady, m, w.Gen+1
		n.Work[id] = w
	} else {
		n.Work[id] = WorkCard{Primary: p, Attempt: pr.Attempt, Member: m, Place: FReady, Gen: 1}
	}
	pr.State = Working
	n.Primaries[p] = pr
	return n, nil
}

// Take is SprintTables.tla Take(m, c) (line 326): the worker on an up
// member moves a work card ready -> working at its generation (F2: a take
// by id names the generation; a stale one is refused).
func Take(s State, m, c string, gen int) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	w, ok := s.Work[c]
	if !ok || s.Members[m] != Up || w.Member != m || w.Place != FReady {
		return s, refuse("%s is not in %s's ready cell", c, m)
	}
	if w.Gen != gen {
		return s, refuse("%s@%d is stale: the live generation is %d", c, gen, w.Gen)
	}
	n := s.Clone()
	w.Place = FWorking
	n.Work[c] = w
	return n, nil
}

// Finish is SprintTables.tla Finish(m, c, g, v) (line 340), and
// FinishRefused (line 364) when the generation is not live: accepted only
// for the live generation in its member's working cell (D3); the card to
// done, its primary to review at the head the card produced; failed opens
// the failed judgment; ok asks the readers kept on the primary again (G2).
func Finish(s State, m, c string, gen int, ok bool) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	w, found := s.Work[c]
	if !found || w.Member != m || w.Place != FWorking || w.Gen != gen {
		return s, refuse("FinishRefused: %s@%d is not live in %s's working cell", c, gen, m)
	}
	n := s.Clone()
	w.Place = FDone
	w.OK = "ok"
	if !ok {
		w.OK = "failed"
	}
	n.Work[c] = w
	p := w.Primary
	pr := n.Primaries[p]
	if pr.State != Working {
		// ApplyWork skips a move whose expectation fails and reports it.
		n.open(JSkipped, p)
		return n, nil
	}
	pr.State = Review
	pr.Head = w.Attempt
	n.Primaries[p] = pr
	if !ok {
		n.open(JFailed, p)
		return n, nil
	}
	for _, r := range pr.Pair {
		id := RC(p, w.Attempt, r)
		if _, made := n.Reads[id]; made {
			return s, badChoice("%s cut a second time (NoCardLostOrTwice)", id)
		}
		n.Reads[id] = ReadCard{Primary: p, Attempt: w.Attempt, Reader: r, Place: Asked}
	}
	return n, nil
}

// ------------------------------------------------------------------ readers

// Ask is SprintTables.tla Ask(p) (line 375): a primary in review whose work
// did not fail, with no read card on the table, is dealt to two different
// readers: the two kept on it (D2), or the shortest asked queues (the
// choice). It closes stranded in review (spec section 6).
func Ask(s State, p string, two []string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if !s.InWork(p, Review) || s.Failed(p) || len(s.LiveReadsOf(p)) > 0 {
		return s, refuse("%s is not a primary in review lacking reads", p)
	}
	if len(s.Readers) < 2 {
		return s, refuse("fewer than two readers")
	}
	if len(two) != 2 || two[0] == two[1] {
		return s, badChoice("%s asked of %v, not two different readers", p, two)
	}
	pr := s.Primaries[p]
	sorted := addSorted(nil, two...)
	if len(pr.Pair) > 0 {
		if Join(sorted) != Join(pr.Pair) {
			return s, badChoice("%s asked of %v, not the readers kept on it %v", p, sorted, pr.Pair)
		}
	} else if !s.shortestPair(two) {
		return s, badChoice("%s asked of %v, not the two shortest asked queues", p, two)
	}
	n := s.Clone()
	for _, r := range two {
		id := RC(p, pr.Attempt, r)
		if _, made := n.Reads[id]; made {
			return s, badChoice("%s cut a second time (NoCardLostOrTwice)", id)
		}
		n.Reads[id] = ReadCard{Primary: p, Attempt: pr.Attempt, Reader: r, Place: Asked}
	}
	n.setPrimary(p, func(x *Primary) { x.Pair = sorted })
	delete(n.Open, Judgment{JStranded, p})
	return n, nil
}

// shortestPair is Ask's choice rule: one reader with the shortest asked
// queue, the other with the shortest among the rest.
func (s State) shortestPair(two []string) bool {
	for _, order := range [][2]string{{two[0], two[1]}, {two[1], two[0]}} {
		r1, r2 := order[0], order[1]
		if !has(s.Readers, r1) || !has(s.Readers, r2) {
			return false
		}
		ok := true
		for _, x := range s.Readers {
			if s.AskedLen(x) < s.AskedLen(r1) {
				ok = false
			}
			if x != r1 && s.AskedLen(x) < s.AskedLen(r2) {
				ok = false
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// AskAnother is SprintTables.tla AskAnother(p, r) (line 397): one more reader
// for a primary in review (free, F1, FreeCoordinator); it answers a broken
// read and reads exhausted.
func AskAnother(s State, p, r string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if !s.InWork(p, Review) {
		return s, refuse("%s is not in review", p)
	}
	pr := s.Primaries[p]
	id := RC(p, pr.Attempt, r)
	if !has(s.Readers, r) {
		return s, badChoice("%s is not a reader", r)
	}
	if _, made := s.Reads[id]; made {
		return s, badChoice("%s cut a second time (NoCardLostOrTwice)", id)
	}
	n := s.Clone()
	n.Reads[id] = ReadCard{Primary: p, Attempt: pr.Attempt, Reader: r, Place: Asked}
	delete(n.Open, Judgment{JBroken, p})
	delete(n.Open, Judgment{JReads, p})
	return n, nil
}

// ReadStart is SprintTables.tla ReadStart(r, c) (line 408): a reader moves
// its own read card asked -> reading.
func ReadStart(s State, r, c string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	rc, ok := s.Reads[c]
	if !ok || rc.Reader != r || rc.Place != Asked {
		return s, refuse("%s is not in %s's asked cell", c, r)
	}
	n := s.Clone()
	rc.Place = Reading
	n.Reads[c] = rc
	return n, nil
}

// Read is SprintTables.tla Read(r, c, v) (line 418): a reader records ok or
// broken; a report against a retired card is refused. Broken opens the
// broken judgment; the read that leaves the reads exhausted opens reads
// exhausted (G3). A report on a card still asked is the begin and the report
// in one step, and the read that completes two different readers' ok at the
// head opens ready to accept: both the spec's (section 6), not the model's.
func Read(s State, r, c string, ok bool) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	rc, found := s.Reads[c]
	if !found || rc.Reader != r || (rc.Place != Reading && rc.Place != Asked) {
		return s, refuse("%s is not in %s's asked or reading cell", c, r)
	}
	n := s.Clone()
	p := rc.Primary
	before := len(n.OkReaders(p))
	v := OK
	if !ok {
		v = Broken
	}
	rc.Place, rc.Verdict = v, v
	n.Reads[c] = rc
	if !ok {
		n.open(JBroken, p)
	} else if before < 2 && len(n.OkReaders(p)) >= 2 && n.InWork(p, Review) {
		n.open(JAccept, p)
	}
	n.exhaust(p)
	return n, nil
}

// exhaust is SprintTables.tla ExhaustNote (G3) as the spec states it
// (section 6): a primary in review with no read outstanding, not acceptable
// and no open judgment is a judgment: reads exhausted when it was asked at its
// attempt, else stranded in review. Stranded is the spec's, not the model's.
func (n *State) exhaust(p string) {
	if !n.InWork(p, Review) || len(n.OutOf(p)) > 0 || n.Acceptable(p) || n.OpenOn(p) {
		return
	}
	if n.AskedNow(p) {
		n.open(JReads, p)
	} else {
		n.open(JStranded, p)
	}
}

// ------------------------------------------------------------------ coordinator

// Accept is SprintTables.tla Accept(S) (line 436): named, all or nothing
// (D4): review -> merging and into merge queued; refused unless every one has
// two different readers ok at its head. Its asked and reading read cards
// retire (F1); a stream not stopped becomes merging (G4); its card judgments
// close.
func Accept(s State, set []string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if len(set) == 0 {
		return s, refuse("accept names nothing")
	}
	for _, p := range set {
		if !s.InWork(p, Review) || !s.Acceptable(p) {
			return s, refuse("%s is not in review with two different readers ok at its head", p)
		}
	}
	n := s.Clone()
	for _, p := range set {
		for _, id := range n.OutOf(p) {
			rc := n.Reads[id]
			rc.Place = Retired
			n.Reads[id] = rc
		}
		n.Merge[p] = MergeCard{Place: Queued}
		st := n.Primaries[p].Stream
		if n.Streams[st].State != SStopped {
			x := n.Streams[st]
			x.State = SMerging
			n.Streams[st] = x
		}
		n.closeOn(p)
		n.setPrimary(p, func(x *Primary) { x.State = Merging })
	}
	return n, nil
}

// Rework is SprintTables.tla Rework(p) (line 456): the primary's read cards
// retire; with a member up the next work card is cut into m, which must be an
// up member with the shortest ready queue, and the primary goes review ->
// working; with none up (m is "") review -> ready. The readers are kept (D2);
// its card judgments close.
func Rework(s State, p, m string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if !s.InWork(p, Review) {
		return s, refuse("%s is not in review", p)
	}
	up := s.Up()
	n := s.Clone()
	for _, id := range n.LiveReadsOf(p) {
		rc := n.Reads[id]
		rc.Place = Retired
		n.Reads[id] = rc
	}
	pr := n.Primaries[p]
	pr.Attempt++
	if len(up) > 0 {
		if !s.ShortestIn(m, up) {
			return s, badChoice("%s reworked into %s, whose ready queue (%d) is not the shortest of %v", p, m, s.RL(m), up)
		}
		id := WC(p, pr.Attempt)
		if _, made := n.Work[id]; made {
			return s, badChoice("%s cut a second time (NoCardLostOrTwice)", id)
		}
		n.Work[id] = WorkCard{Primary: p, Attempt: pr.Attempt, Member: m, Place: FReady, Gen: 1}
		pr.State = Working
	} else {
		if m != "" {
			return s, badChoice("%s reworked into %s with no member up", p, m)
		}
		pr.State = Ready
	}
	n.Primaries[p] = pr
	n.closeOn(p)
	return n, nil
}

// Drop is SprintTables.tla Drop(p) (line 478): off the table. Its
// unfinished work card is withdrawn, its outstanding read cards retire, its
// merge place goes (the returned place too, spec section 7); work last. A
// waiting primary that needs it is blocked. Every judgment on it closes. The
// sprint-done judgment is the spec's (section 8).
func Drop(s State, p string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if !s.Placedp(p) || s.InWork(p, Landed) {
		return s, refuse("%s is not an open primary on the table", p)
	}
	doneBefore := s.sprintDone()
	n := s.Clone()
	st := n.Primaries[p].Stream
	for _, id := range n.UnfinishedOf(p) {
		w := n.Work[id]
		w.Place = Gone
		n.Work[id] = w
	}
	for _, id := range n.OutOf(p) {
		rc := n.Reads[id]
		rc.Place = Retired
		n.Reads[id] = rc
	}
	if mc, ok := n.Merge[p]; ok && mc.Place != Merged {
		n.Merge[p] = MergeCard{Place: Gone}
	}
	x := n.Streams[st]
	x.State = n.streamAfter(st, x.State, nil, []string{p})
	n.Streams[st] = x
	for _, q := range Keys(n.Primaries) {
		qp := n.Primaries[q]
		if qp.State == Waiting && has(qp.Needs, p) && !has(qp.Waived, p) {
			n.open(JBlocked, q)
		}
	}
	n.closeOn(p)
	n.setPrimary(p, func(x *Primary) { x.State = Off })
	if !doneBefore && n.sprintDone() {
		n.open(JDone, SprintSubject)
	}
	return n, nil
}

// streamAfter is SprintTables.tla StreamAfter (G4) after the merge cells of
// the stream changed: a stopped stream stays stopped; with cards queued it
// is merging; landed when every primary of it on the table has landed (or
// lands or leaves in this step); else waiting. With sentinels (spec section
// 16) a primary landed by release counts as landed, merged or not.
func (s State) streamAfter(stream, was string, landing, leaving []string) string {
	if was == SStopped {
		return SStopped
	}
	for id, m := range s.Merge {
		if m.Place == Queued && s.Primaries[id].Stream == stream && !has(landing, id) && !has(leaving, id) {
			return SMerging
		}
	}
	for id, p := range s.Primaries {
		if p.Stream != stream || p.State == Off || p.State == Landed || has(landing, id) || has(leaving, id) {
			continue
		}
		return SWaiting
	}
	return SLanded
}

// sprintDone is the spec's sprint-done condition (section 8): every primary
// landed or off the table, at least one landed.
func (s State) sprintDone() bool {
	landed := 0
	for _, p := range s.Primaries {
		switch p.State {
		case Landed:
			landed++
		case Off:
		default:
			return false
		}
	}
	return landed > 0
}

// Rank is SprintTables.tla Rank(p) (line 501): ranks a card first, refused
// for a landed primary (G6). score is the choice: below every other primary of
// its stream on the table (the spec's "first": ahead of every primary of its
// stream).
func Rank(s State, p string, score float64) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if !s.Placedp(p) || s.InWork(p, Landed) {
		return s, refuse("%s is not an open primary on the table", p)
	}
	for _, q := range s.StreamOrder(s.Primaries[p].Stream) {
		if q != p && !(score < s.Primaries[q].Score) {
			return s, badChoice("%s ranked first at %v, not ahead of %s at %v", p, score, q, s.Primaries[q].Score)
		}
	}
	n := s.Clone()
	n.setPrimary(p, func(x *Primary) { x.Score = score })
	return n, nil
}

// Return is SprintTables.tla Return(p) (line 513): merging -> review; the
// card leaves merge queued or stuck, into returned (spec section 7), with its
// need; the stream's state follows (G4); it answers the card's red CI and a
// skipped repair (F3), and opens returned to review (spec section 7, not in
// the model).
func Return(s State, p string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if !s.InWork(p, Merging) {
		return s, refuse("%s is not merging", p)
	}
	n := s.Clone()
	st := n.Primaries[p].Stream
	n.Merge[p] = MergeCard{Place: Returned}
	x := n.Streams[st]
	x.State = n.streamAfter(st, x.State, nil, nil)
	n.Streams[st] = x
	n.closeOn(p)
	n.setPrimary(p, func(x *Primary) { x.State = Review })
	n.open(JReturned, p)
	return n, nil
}

// ------------------------------------------------------------------ merge

// MergeGreen is SprintTables.tla MergeGreen(s, B) (line 532), given the fact
// green: the batch is the first n of the stream's queued cards in score order
// (a prefix: section 7, 1), and lands. The same step moves every waiting
// primary whose needs have all landed to ready and marks reached every
// sentinel whose needs have all landed (spec section 7), and writes the
// sprint-done judgment (section 8): the spec's, not the model's.
func MergeGreen(s State, stream string, batch int) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if s.Streams[stream].State != SMerging {
		return s, refuse("stream %s is not merging", stream)
	}
	q := s.MergeCell(stream, Queued)
	if len(q) == 0 || batch < 1 {
		return s, refuse("stream %s has nothing queued", stream)
	}
	if batch > len(q) {
		batch = len(q)
	}
	b := q[:batch]
	for _, x := range s.MergeCell(stream, Stuck) {
		if s.Primaries[x].Score < s.Primaries[b[len(b)-1]].Score {
			return s, refuse("stuck %s is a barrier", x)
		}
	}
	doneBefore := s.sprintDone()
	n := s.Clone()
	x := n.Streams[stream]
	x.State = n.streamAfter(stream, SMerging, b, nil)
	n.Streams[stream] = x
	for _, p := range b {
		mc := n.Merge[p]
		mc.Place = Merged
		n.Merge[p] = mc
		n.setPrimary(p, func(x *Primary) { x.State = Landed })
	}
	n.resolveAll()
	if !doneBefore && n.sprintDone() {
		n.open(JDone, SprintSubject)
	}
	return n, nil
}

// resolveAll moves every waiting primary whose needs are all met to ready,
// and marks reached every sentinel whose needs are all met, opening its
// judgment (spec sections 7 and 16).
func (n *State) resolveAll() {
	for _, p := range Keys(n.Primaries) {
		pr := n.Primaries[p]
		if pr.State != Waiting || !n.NeedsMet(p) {
			continue
		}
		if pr.Kind == KindSentinel {
			if !pr.Reached {
				pr.Reached = true
				n.Primaries[p] = pr
				n.open(JReached, p)
			}
			continue
		}
		pr.State = Ready
		n.Primaries[p] = pr
	}
}

// MergeStop is SprintTables.tla MergeStop(s, p, why, q) (line 551): a fact on
// card p of the batch (the first n queued) stops the stream: a conflict, or its
// need of card q of another stream first (D6), refused unless q is placed, in
// another stream and not landed (F4). The card goes to stuck, the stream
// stops, the coordinator is told.
func MergeStop(s State, stream string, batch int, p, cause, q string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if s.Streams[stream].State != SMerging {
		return s, refuse("stream %s is not merging", stream)
	}
	queued := s.MergeCell(stream, Queued)
	if batch > len(queued) {
		batch = len(queued)
	}
	if !has(queued[:batch], p) {
		return s, refuse("%s is not in the batch %v", p, queued[:batch])
	}
	note := JConflict
	if cause == CCross {
		note = JCross
		if !s.Placedp(q) || s.Primaries[q].Stream == stream || s.InWork(q, Landed) {
			return s, refuse("cross need %s is not placed in another stream and unlanded", q)
		}
	} else {
		q = ""
	}
	n := s.Clone()
	n.Merge[p] = MergeCard{Place: Stuck, Need: q}
	n.Streams[stream] = Stream{State: SStopped, Cause: cause}
	n.open(note, StreamSubject(stream))
	return n, nil
}

// MergeRed is SprintTables.tla MergeRed(s) (line 569): the branch red or the
// merge queue rejected the batch; the stream stops, no card moves, the
// coordinator is told.
func MergeRed(s State, stream string, rejected bool) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if s.Streams[stream].State != SMerging {
		return s, refuse("stream %s is not merging", stream)
	}
	n := s.Clone()
	cause, note := CRed, JRed
	if rejected {
		cause, note = CRejected, JRejected
	}
	n.Streams[stream] = Stream{State: SStopped, Cause: cause}
	n.open(note, StreamSubject(stream))
	return n, nil
}

// Resume is SprintTables.tla Resume(s) (line 585) with what was done (--did):
// refused while a stuck card's cross-stream need has not landed (D6), and
// after a red branch without --did (spec section 7). The stuck cards go back
// to queued; the stream's state follows; the stream's judgment closes (D5).
func Resume(s State, stream, did string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	st := s.Streams[stream]
	if st.State != SStopped {
		return s, refuse("stream %s is not stopped", stream)
	}
	if st.Cause == CRed && did == "" {
		return s, refuse("after a red branch --did is required")
	}
	for _, p := range s.MergeCell(stream, Stuck) {
		if need := s.Merge[p].Need; need != "" && !s.InWork(need, Landed) {
			return s, refuse("%s needs %s landed first", p, need)
		}
	}
	return s.resumed(stream), nil
}

func (s State) resumed(stream string) State {
	n := s.Clone()
	for _, p := range n.MergeCell(stream, Stuck) {
		mc := n.Merge[p]
		mc.Place = Queued
		n.Merge[p] = mc
	}
	n.Streams[stream] = Stream{State: n.streamAfter(stream, SMerging, nil, nil)}
	n.closeOn(StreamSubject(stream))
	return n
}

// ------------------------------------------------------------------ fleet

// FleetDown is SprintTables.tla FleetDown(m) (line 603): the member goes
// down; its unfinished work cards are dealt to up members at a new
// generation; with no member up they are withdrawn (a new generation) and
// their primaries return to ready. dest is the choice, card to member: the
// model deals every card to one member with the shortest ready queue; the
// spec says "dealt to up members" (section 5), so each card, in work order,
// must go to an up member whose queue is the shortest at that moment.
func FleetDown(s State, m string, dest map[string]string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if s.Members[m] != Up {
		return s, refuse("%s is not up", m)
	}
	n := s.Clone()
	n.Members[m] = Down
	var cs []string
	for _, id := range Keys(n.Work) {
		w := n.Work[id]
		if w.Member == m && (w.Place == FReady || w.Place == FWorking) {
			cs = append(cs, id)
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		return n.Primaries[n.Work[cs[i]].Primary].Score < n.Primaries[n.Work[cs[j]].Primary].Score
	})
	others := n.Up()
	for _, id := range cs {
		w := n.Work[id]
		if len(others) == 0 {
			w.Place, w.Gen = FWithdrawn, w.Gen+1
			n.Work[id] = w
			n.setPrimary(w.Primary, func(x *Primary) { x.State = Ready })
			continue
		}
		t := dest[id]
		if !n.ShortestIn(t, others) {
			return s, badChoice("%s dealt from %s to %s, whose ready queue (%d) is not the shortest of %v", id, m, t, n.RL(t), others)
		}
		w.Member, w.Place, w.Gen = t, FReady, w.Gen+1
		n.Work[id] = w
	}
	return n, nil
}

// FleetUp is SprintTables.tla FleetUp(m) (line 637): the member comes up and
// the ready queues are levelled in one call; the newest cards move at a new
// generation. moves is the choice, card to member; Level holds it to the
// spec's rule (section 14, T4).
func FleetUp(s State, m string, moves map[string]string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if st, ok := s.Members[m]; ok && st == Up {
		return s, refuse("%s is up", m)
	}
	n := s.Clone()
	if _, ok := n.Members[m]; !ok {
		n.Order = append(n.Order, m)
	}
	n.Members[m] = Up
	return n.level(moves)
}

// Level is the fleet verb level and the tick's T4 (spec section 14): the
// up members' ready queues are evened when two differ by more than one, the
// newest cards going to the shorter queues. From the spec, not yet in the
// model (whose FleetUp moves half of the longest queue).
func Level(s State, moves map[string]string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	return s.level(moves)
}

func (s State) level(moves map[string]string) (State, error) {
	up := s.Up()
	n := s.Clone()
	bySource := map[string][]string{}
	for _, id := range Keys(moves) {
		w, ok := s.Work[id]
		to := moves[id]
		if !ok || w.Place != FReady || !has(up, w.Member) || !has(up, to) || to == w.Member {
			return s, badChoice("level moves %s to %s: not a ready card of an up member to another", id, to)
		}
		if s.RL(to) >= s.RL(w.Member) {
			return s, badChoice("level moves %s from %s (%d ready) to %s (%d ready)", id, w.Member, s.RL(w.Member), to, s.RL(to))
		}
		bySource[w.Member] = append(bySource[w.Member], id)
		w.Member, w.Gen = to, w.Gen+1
		n.Work[id] = w
	}
	for src, ids := range bySource {
		for _, id := range ids {
			for _, other := range Keys(s.Work) {
				o := s.Work[other]
				if o.Member == src && o.Place == FReady && moves[other] == "" &&
					s.Primaries[o.Primary].Score > s.Primaries[s.Work[id].Primary].Score {
					return s, badChoice("level moves %s off %s and keeps the newer %s", id, src, other)
				}
			}
		}
	}
	if len(moves) == 0 {
		n.levelDefault()
	}
	lo, hi := -1, -1
	for _, x := range up {
		l := n.RL(x)
		if lo < 0 || l < lo {
			lo = l
		}
		if l > hi {
			hi = l
		}
	}
	if hi-lo > 1 {
		return s, badChoice("the ready queues differ by %d after levelling", hi-lo)
	}
	return n, nil
}

// levelDefault levels when no choice was given: the newest card of the
// first longest queue goes to the first shortest, until no two differ by more
// than one.
func (n *State) levelDefault() {
	for {
		up := n.Up()
		if len(up) < 2 {
			return
		}
		lo, hi := up[0], up[0]
		for _, x := range up {
			if n.RL(x) < n.RL(lo) {
				lo = x
			}
			if n.RL(x) > n.RL(hi) {
				hi = x
			}
		}
		if n.RL(hi)-n.RL(lo) <= 1 {
			return
		}
		newest := ""
		for _, id := range Keys(n.Work) {
			w := n.Work[id]
			if w.Member == hi && w.Place == FReady && (newest == "" || n.Primaries[w.Primary].Score > n.Primaries[n.Work[newest].Primary].Score) {
				newest = id
			}
		}
		w := n.Work[newest]
		w.Member, w.Gen = lo, w.Gen+1
		n.Work[newest] = w
	}
}

// ------------------------------------------------------------------ ci, ack

// CiRed is SprintTables.tla CiRed(p) (line 654): a red CI observation on a
// placed primary in any state opens the ci judgment and moves nothing.
func CiRed(s State, p string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if !s.Placedp(p) {
		return s, refuse("%s is not on the table", p)
	}
	n := s.Clone()
	n.open(JCI, p)
	return n, nil
}

// CiGreen is the spec's green observation (section 8): a happened note; on
// the current head it resolves a red one, and when that closes the primary's
// last judgment in review the exhausted or stranded judgment is written
// (section 6). From the spec, not yet in the model.
func CiGreen(s State, p string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if !s.Placedp(p) {
		return s, refuse("%s is not on the table", p)
	}
	n := s.Clone()
	if n.Open[Judgment{JCI, p}] {
		delete(n.Open, Judgment{JCI, p})
		n.exhaust(p)
	}
	return n, nil
}

// Ack is SprintTables.tla Ack(n) (line 667) on every subject of one
// notification: the coordinator looked and nothing is to be done; refused for
// a stopped stream's judgment while the stream is stopped (F3). The ack that
// leaves a primary in review with no judgment writes its exhausted or
// stranded judgment (G3), unless it acknowledged that judgment itself.
//
// The model acknowledges only ci and skipped; the spec lets the coordinator
// look at any judgment (section 8), waives the dropped needs by the ack of a
// blocked judgment, and keeps the tick's judgments as acknowledged conditions
// (section 14): those are from the spec, not yet in the model.
func Ack(s State, typ string, subjects []string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	if len(subjects) == 0 {
		return s, refuse("ack names no judgment")
	}
	for _, sub := range subjects {
		if !s.Open[Judgment{typ, sub}] {
			return s, refuse("no open judgment %s on %s", typ, sub)
		}
		if has(stopTypes, typ) {
			if st := s.Streams[sub[len("stream:"):]]; st.State == SStopped {
				return s, refuse("the judgment of stopped stream %s stays open", sub)
			}
		}
	}
	n := s.Clone()
	for _, sub := range subjects {
		delete(n.Open, Judgment{typ, sub})
		switch typ {
		case JBlocked:
			if drops := n.DroppedNeeds(sub); len(drops) > 0 {
				n.setPrimary(sub, func(x *Primary) { x.Waived = addSorted(x.Waived, drops...) })
			}
		case JNoMember, JCannotAsk:
			n.Acked[Judgment{typ, sub}] = true
		}
	}
	if typ != JReads && typ != JStranded {
		for _, sub := range subjects {
			if _, ok := n.Primaries[sub]; ok {
				n.exhaust(sub)
			}
		}
	}
	return n, nil
}

// ------------------------------------------------------------------ the fence

// Crash is SprintTables.tla Crash (line 687): the process writing a step
// over more than one table is cut before its last write; the operation stays
// pending. The state it leaves is the step's but for the work table.
func Crash(pre, post State, verb string) State {
	n := post.Clone()
	for id, p := range pre.Primaries {
		q := n.Primaries[id]
		q.State, q.Attempt, q.Head = p.State, p.Attempt, p.Head
		n.Primaries[id] = q
	}
	n.Open = pre.Clone().Open
	n.Pending = verb
	return n
}

// Repair is SprintTables.tla Repair (line 695) after Crash: the work-table
// write is applied from the operation record, where its expectation holds, and
// the operation releases. Continue (line 679) is the same write by the same
// process; a step that is not cut is Continue at once, so the action
// functions above return the state after Continue.
func Repair(post State) State {
	n := post.Clone()
	n.Pending = ""
	return n
}
