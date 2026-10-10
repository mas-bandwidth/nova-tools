package sprint

import (
	"fmt"
	"slices"
	"strings"
)

// The one wait (docs/SPEC-ISA.md, "the one wait kind"): a sentinel, a held card
// and the wave behind it are one wait, read by WaitOf in held.go. A sentinel is
// a stop in its stream: it waits for the line before it, only the coordinator's
// release lands it, and the same step moves what waited behind it to ready.

// IsSentinel says the primary is a sentinel.
func IsSentinel(c *Card) bool { return c.F("kind") == "sentinel" }

// IsHeld says the primary was admitted held (add --held): it stays waiting
// until the coordinator's release (nova-tools#5096 item 15).
func IsHeld(c *Card) bool { return c.F(FieldHeld) != "" }

// FieldHeld is the stamp of a primary admitted held.
const FieldHeld = "held"

// heldWave is the first held sentinel waiting for nothing itself: what the
// tick offers when the fleet starves (NStarving), since its release lets the
// wave through; nil when none is held.
func heldWave(s *Snapshot) *Card {
	for _, c := range s.Work.Column(Waiting) {
		if w := WaitOf(s, c); IsSentinel(c) && w.Operand == WaitOnRelease && len(w.On) == 0 {
			return c
		}
	}
	return nil
}

// HeldBack is how many primaries no tick moves on its own: every sentinel not
// released, every card admitted held, and every waiting card that waits,
// through a need or its place in line, on one of those. where shows them as
// held=N (nova-tools#5096 item 16), and its ETA counts every card not landed,
// held ones included (docs/SPEC-SPRINT.md section 1, 2026-10-02). Everything
// it counts is waiting, so a snapshot of the work table's waiting column is
// enough.
func HeldBack(s *Snapshot) int {
	memo := map[string]bool{}
	var back func(c *Card) bool
	back = func(c *Card) bool {
		if v, ok := memo[c.ID]; ok {
			return v
		}
		memo[c.ID] = false // a cycle holds nothing back by itself
		wait := WaitOf(s, c)
		v := wait.Operand == WaitOnLine || wait.Operand == WaitOnRelease
		for _, n := range wait.On {
			if w := s.Work.Placed(n); !v && w != nil && w.Col == Waiting {
				v = back(w)
			}
		}
		memo[c.ID] = v
		return v
	}
	n := 0
	for _, c := range s.Work.Column(Waiting) {
		if back(c) {
			n++
		}
	}
	return n
}

// streamLine is the stream's primaries on the table, in work order.
func streamLine(s *Snapshot, stream string) []*Card {
	var out []*Card
	for _, st := range States {
		out = append(out, s.Work.Cell(stream, st)...)
	}
	SortCards(out)
	return out
}

// sentinelBefore is the latest unlanded sentinel of the stream that sorts
// before score: what a card placed there waits behind.
func sentinelBefore(s *Snapshot, stream string, score float64) *Card {
	var out *Card
	for _, c := range s.Work.Cell(stream, Waiting) {
		if IsSentinel(c) && c.Score < score && (out == nil || c.Score > out.Score) {
			out = c
		}
	}
	return out
}

// sentinelAfter is the first unlanded sentinel of the stream that sorts after
// score: what waits for a card placed there.
func sentinelAfter(s *Snapshot, stream string, score float64) *Card {
	var out *Card
	for _, c := range s.Work.Cell(stream, Waiting) {
		if IsSentinel(c) && c.Score > score && (out == nil || c.Score < out.Score) {
			out = c
		}
	}
	return out
}

// withdrawnCard says a ready primary holds a work card withdrawn because no
// member was up: it has been started, and is past any stop inserted now.
func withdrawnCard(s *Snapshot, c *Card) bool {
	if s.Fleet == nil {
		return false
	}
	for _, fc := range s.Fleet.Of(c.ID) {
		if IsWithdrawn(fc.Col) {
			return true
		}
	}
	return false
}

// addScores is the scores of n cards admitted: after every primary (or from
// the given score), or in line between the anchor and its neighbour. Where no
// score lies between, the add is refused; the line is never renumbered.
func addScores(s *Snapshot, r AddReq, n int) ([]float64, string) {
	out := make([]float64, n)
	if r.Before != "" && r.After != "" {
		return nil, "--before or --after, not both"
	}
	if r.Before == "" && r.After == "" {
		score := 1.0
		for _, c := range s.Work.Cards() {
			if c.Score >= score {
				score = c.Score + 1
			}
		}
		if r.Score != nil {
			score = *r.Score
		}
		for i := range out {
			out[i] = score + float64(i)
		}
		return out, ""
	}
	if r.Score != nil {
		return nil, "--score or a place in line, not both"
	}
	anchor := r.Before + r.After
	line := streamLine(s, r.Stream)
	k := -1
	for i, c := range line {
		if c.ID == anchor {
			k = i
		}
	}
	if k < 0 {
		return nil, anchor + " is not a primary of stream " + r.Stream + " on the table"
	}
	var lo, hi float64
	var between string
	if r.Before != "" {
		hi = line[k].Score
		lo = hi - float64(n+1)
		between = "before " + anchor
		if k > 0 {
			lo, between = line[k-1].Score, line[k-1].ID+" and "+anchor
		}
	} else {
		lo = line[k].Score
		hi = lo + float64(n+1)
		between = "after " + anchor
		if k+1 < len(line) {
			hi, between = line[k+1].Score, anchor+" and "+line[k+1].ID
		}
	}
	step := (hi - lo) / float64(n+1)
	prev := lo
	for i := range out {
		out[i] = lo + step*float64(i+1)
		if !(out[i] > prev && out[i] < hi) {
			return nil, fmt.Sprintf("no score lies between %s for %d card(s); the line is not renumbered", between, n)
		}
		prev = out[i]
	}
	return out, ""
}

// mod is a change a step makes to a primary already in line: a
// move back to waiting, and the step's reason when it is no longer reached.
type mod struct {
	to      State
	unreach string
}

func modOf(mods map[string]*mod, c *Card) *mod {
	if m := mods[c.ID]; m != nil {
		return m
	}
	m := &mod{}
	mods[c.ID] = m
	return m
}

func (m *mod) change(c *Card) Change {
	var unset []string
	if m.unreach != "" {
		unset = append(unset, "reached")
	}
	if m.to != "" {
		return change(Work, moveEntry(c, c.Row, m.to, nil, unset...))
	}
	return change(Work, setEntry(c, nil, unset...))
}

// reachedNote is the judgment a sentinel is reached: how many cards of its
// stream have landed (with this step's: landing), and how many wait behind it
// (with extra, the ones this step puts there).
func reachedNote(s *Snapshot, c *Card, landing map[string]bool, extra int, who string) Note {
	landed := s.Work.Count(c.Row, Landed)
	for id := range landing {
		if pc := s.Work.Placed(id); pc != nil && pc.Row == c.Row && pc.Col != Landed {
			landed++
		}
	}
	behind := extra
	if c.Placed() {
		behind += len(Behind(s, c))
	}
	for _, w := range s.Work.Column(Waiting) {
		if w.Row != c.Row && contains(Split(w.F("needs")), c.ID) {
			behind++ // it names the stop from another stream
		}
	}
	n := judgment(NSentinelReached, c.Row, s.Now, 0, c.ID)
	n.Who, n.Card = who, c.ID
	n.What = fmt.Sprintf("sentinel %s reached: %d cards of %s have landed; %d cards wait behind it", c.ID, landed, c.Row, behind)
	return n
}

// Reachable says a sentinel whose waits are met is reached now: something came
// before it (HasBefore), or, with nothing before it, no other work of the
// sprint is in flight (docs/SPEC-SPRINT.md section 16).
func Reachable(s *Snapshot, c *Card, landing map[string]bool) bool {
	return HasBefore(s, c) || workInFlight(s, landing) == ""
}

// workInFlight is a primary ready, working, in review or merging and not
// landing in this step: work that moves, "" when there is none.
func workInFlight(s *Snapshot, landing map[string]bool) string {
	for _, st := range []State{Ready, Working, Review, Merging} {
		for _, c := range s.Work.Column(st) {
			if !landing[c.ID] {
				return c.ID
			}
		}
	}
	return ""
}

// HasBefore says a sentinel has something to be reached after: a need it names,
// or a primary of its stream that sorts before it.
func HasBefore(s *Snapshot, c *Card) bool {
	if len(Split(c.F("needs"))) > 0 {
		return true
	}
	return anyBefore(s, c.Row, c.ID, c.Score)
}

// anyBefore says a primary of the stream other than id is on the table before score.
func anyBefore(s *Snapshot, stream, id string, score float64) bool {
	for _, x := range streamLine(s, stream) {
		if x.ID != id && x.Score < score {
			return true
		}
	}
	return false
}

// reachUnit marks a sentinel reached, with the time, and tells the coordinator once.
func reachUnit(s *Snapshot, c *Card, landing map[string]bool, who string) Unit {
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, map[string]string{"reached": stamp(s.Now)}))},
		Notes: []Note{reachedNote(s, c, landing, 0, who)}, Moved: "sentinel " + c.ID + " reached"}
}

// ReleaseReq is the coordinator releasing reached sentinels, with what it
// looked at and found. Coordinator is the sprint's coordinator: release is
// refused for any other actor.
type ReleaseReq struct {
	IDs         []string
	Reason      string
	Coordinator string
	Answers     []string
	Who         string
}

// Release is the coordinator resolving a wait whose operand is the release: a
// sentinel lands (waiting -> landed, the only step that may), a held card has
// its hold cleared and moves to ready when it waits for nothing else, and
// every waiting primary whose needs have now landed moves to ready in the same
// step. A sentinel with nothing before it (HasBefore), or whose waits are each
// under way (notUnderWay), is released past them, waived on it
// (nova-tools#5096 item c13).
func Release(s *Snapshot, r ReleaseReq) Plan {
	var p Plan
	p.on(s)
	p.releasing = true
	refuseAll := func(why string) Plan {
		var q Plan
		for _, id := range r.IDs {
			q.refuse(id, why)
		}
		return q
	}
	switch {
	case len(r.IDs) == 0:
		p.refuse("release", "names the sentinels it releases")
		return p
	case strings.TrimSpace(r.Reason) == "":
		return refuseAll("release wants --reason <what you looked at and found>")
	case r.Coordinator == "":
		return refuseAll("the sprint has no coordinator, and release is the coordinator's alone")
	case r.Who != r.Coordinator:
		return refuseAll("release is the coordinator's alone: " + r.Coordinator + ", not " + orDash(r.Who))
	}
	landing, unheld, past := map[string]bool{}, map[string]bool{}, map[string][]string{}
	var chosen []*Card
	for _, id := range r.IDs {
		c := s.Work.Card(id)
		w := WaitOf(s, c)
		switch {
		case landing[id] || unheld[id]:
			p.refuse(id, "named twice")
			continue
		case !c.Placed():
			p.refuse(id, "not on the table")
			continue
		case w.Operand != WaitOnLine && w.Operand != WaitOnRelease:
			p.refuse(id, "not a sentinel or a held card: a primary lands by merging")
			continue
		case c.Col != Waiting:
			p.refuse(id, "is "+c.Col+", not a sentinel waiting to be released")
			continue
		case !IsSentinel(c):
			unheld[id] = true
			p.Units = append(p.Units, releaseHeld(s, c, r))
			continue
		}
		if n, why := notUnderWay(s, w.On); n != "" {
			p.refuse(id, "not reached: it waits for "+n+" ("+why+"); release lands a sentinel whose waits have each landed, been dropped, or are in flight (taken, in review or merging)")
			continue
		}
		landing[id] = true
		chosen = append(chosen, c)
		past[id] = w.On
	}
	after := resolveAfter(s, landing, r.Who)
	moving := map[string]bool{}
	for _, u := range after {
		moving[u.Key] = true
	}
	for _, c := range chosen {
		ready := 0
		for _, b := range Behind(s, c) {
			if moving[b.ID] {
				ready++
			}
		}
		for _, u := range after {
			if pc := s.Work.Card(u.Key); !IsSentinel(pc) && pc.Row != c.Row && contains(Split(pc.F("needs")), c.ID) {
				ready++ // named it from another stream
			}
		}
		set := map[string]string{"landed": stamp(s.Now), "released": stamp(s.Now), "released_by": r.Who, "release_reason": r.Reason}
		by := "released by " + r.Who
		if w := past[c.ID]; len(w) > 0 {
			// released before it was reached: what it still waited for is in flight
			// or dropped, waived on it with who and when, so it lands with every need met
			var st []string
			for _, n := range w {
				_, why := notUnderWay(s, []string{n})
				st = append(st, n+" ("+why+")")
			}
			set["waived"] = strings.Join(append(Split(c.F("waived")), w...), ",")
			set["waived_by"], set["waived_at"] = r.Who, stamp(s.Now)
			by += " before it was reached, past " + strings.Join(st, ", ")
		}
		n := happened(NSentinelLanded, c.Row, s.Now, c.ID)
		n.Who = r.Who
		n.What = fmt.Sprintf("sentinel %s landed, %s: %d cards are now ready; %s", c.ID, by, ready, r.Reason)
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row,
			Changes: []Change{change(Work, moveEntry(c, c.Row, Landed, set, FieldHeld))},
			Notes:   []Note{n}, Closes: closesFor(s.Open, nil, c.ID),
			Moved: fmt.Sprintf("sentinel %s waiting -> landed (%s); %d cards are now ready", c.ID, by, ready)})
	}
	if len(chosen) == 0 && len(unheld) == 0 {
		return p
	}
	if len(chosen) == 0 {
		answered(&p, s, r.Answers, r.Who)
		return Lawful(p)
	}
	p.Units = append(p.Units, after...)
	settle(&p, s, r.Who, nil, nil, landing)
	// A sprint this release finishes is found done by the tick's done part
	// (TickDone), which says so and stops the machine.
	answered(&p, s, r.Answers, r.Who)
	return Lawful(p)
}

// notUnderWay is the first of a sentinel's waits that is not under way, with
// its state in words (nova-tools#5096 item c13): "" when every wait is under
// way (landed, dropped, or in flight), else the wait and its state.
func notUnderWay(s *Snapshot, waits []string) (string, string) {
	taken := func(c *Card) bool {
		if s.Fleet == nil {
			return false
		}
		wc := s.Fleet.Placed(c.F("work"))
		return wc != nil && wc.Col == Working
	}
	why := ""
	for _, id := range waits {
		c := s.Work.Card(id)
		switch {
		case c == nil:
			return id, "not on the table"
		case !c.Placed() && c.F("outcome") == "dropped":
			why = "dropped"
		case !c.Placed():
			return id, "off the table (" + orDash(c.F("outcome")) + ")"
		case c.Col == Review, c.Col == Merging, c.Col == Landed:
			why = c.Col
		case c.Col == Working && taken(c):
			why = "working, taken"
		case c.Col == Working:
			return id, "working, its work card not taken"
		default:
			return id, c.Col
		}
	}
	return "", why
}

// releaseHeld clears a held primary's hold, with who and why, and moves it to
// ready when it waits for nothing else.
func releaseHeld(s *Snapshot, c *Card, r ReleaseReq) Unit {
	set := map[string]string{"released": stamp(s.Now), "released_by": r.Who, "release_reason": r.Reason}
	if w := WaitsFor(s, c, nil); len(w) > 0 {
		return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, set, FieldHeld))},
			Moved: fmt.Sprintf("%s released by %s; it waits for %s", c.ID, r.Who, strings.Join(w, ","))}
	}
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, moveEntry(c, c.Row, Ready, set, FieldHeld))},
		Moved: fmt.Sprintf("%s waiting -> ready (released by %s)", c.ID, r.Who)}
}

// FieldNeedsSet is a sentinel's record of its needs re-pointed (sentinel set): "<before> ->
// <after> <stamp> by <who>", the last set.
const FieldNeedsSet = "needs_set"

// SentinelSetReq replaces a sentinel's needs (sentinel set <id> --needs a,b).
type SentinelSetReq struct {
	ID    string
	Needs []string
	Who   string
}

// SentinelSet replaces the needs of a waiting sentinel in one step (docs/SPEC-SPRINT.md
// section 16; 2026-10-04: the cards a release sentinel waited on were deferred, and the
// sentinel could only be dropped and added again, which lost its id, its place and its
// log). The sentinel keeps its id, stream, score and log; one line names its needs before
// and after. A sentinel the new needs leave waiting for nothing is marked reached, as the
// step that lands its last need marks it; one reached that waits again is reached no more,
// and its reached judgment is answered. The blocked or missing judgments on it that named
// only needs it no longer names are answered. The coordinator's alone. Refused whole,
// writing nothing, for no needs (a sentinel with nothing to wait on is released, not
// emptied), an id that is no sentinel waiting on the table, a need named twice, the
// sentinel itself, needs that are no card on the table (every one named at once), the
// needs it has already, and a cycle the new needs would close.
func SentinelSet(s *Snapshot, r SentinelSetReq) Plan {
	var p Plan
	p.on(s)
	if why := notCoordinator(s, r.Who, "sentinel set"); why != "" {
		p.refuse(r.ID, strings.Replace(why, "answers a judgment, which is", "is", 1))
		return p
	}
	if len(r.Needs) == 0 {
		p.refuse(r.ID, "sentinel set wants --needs <a,b>: a sentinel with nothing to wait on is released, not emptied: nova-sprint release "+r.ID+" --reason '<why>'")
		return p
	}
	c := s.Work.Placed(r.ID)
	switch {
	case c == nil:
		p.refuse(r.ID, r.ID+" is no sentinel on the table")
		return p
	case !IsSentinel(c):
		p.refuse(r.ID, r.ID+" is no sentinel: a primary's needs change with its brief's DEPENDS-ON line (nova-sprint brief)")
		return p
	case c.Col != Waiting:
		p.refuse(r.ID, r.ID+" is "+c.Col+": a sentinel's needs are set while it waits")
		return p
	}
	var why, gone []string
	seen := map[string]bool{}
	for _, n := range r.Needs {
		switch {
		case seen[n]:
			why = append(why, n+" is named twice")
		case n == r.ID:
			why = append(why, "a sentinel does not need itself")
		case s.Work.Card(n) == nil:
			gone = append(gone, n+" (no card)")
		case s.Work.Placed(n) == nil:
			gone = append(gone, n+" (off the table, "+orDash(s.Work.Card(n).F("outcome"))+")")
		}
		seen[n] = true
	}
	if len(gone) > 0 {
		why = append(why, "not a card on the table: "+strings.Join(gone, ", "))
	}
	old := c.F("needs")
	now := strings.Join(r.Needs, ",")
	if len(why) == 0 && old == now {
		why = append(why, r.ID+" needs "+now+" already")
	}
	if len(why) > 0 {
		p.refuse(r.ID, strings.Join(why, "; ")+"; nothing was changed")
		return p
	}
	waived := Split(c.F("waived"))
	var live []string
	for _, n := range r.Needs {
		if !contains(waived, n) {
			live = append(live, n)
		}
	}
	g := newNeedGraph(s, map[string][]string{c.ID: live}, nil, nil)
	if cycle := g.closes([]string{c.ID}); cycle != nil {
		p.refuse(r.ID, "the needs would make a cycle: "+g.tellLoop(cycle)+"; nothing was changed")
		return p
	}
	after := withField(c, "needs", now)
	set := map[string]string{"needs": now, FieldNeedsSet: fmt.Sprintf("%s -> %s %s by %s", orDash(old), now, stamp(s.Now), orDash(r.Who))}
	var unset []string
	moved := fmt.Sprintf("sentinel %s needs %s -> %s", c.ID, orDash(old), now)
	u := Unit{Key: c.ID, Stream: c.Row}
	waits := WaitsFor(s, after, nil)
	switch {
	case c.F("reached") != "" && len(waits) > 0:
		unset = append(unset, "reached")
		moved += "; reached no more: it waits for " + strings.Join(waits, ",")
		for _, o := range closesFor(s.Open, []string{NSentinelReached}, c.ID) {
			u.Closes = append(u.Closes, o)
			u.Notes = append(u.Notes, decided(o, "not reached: "+moved, r.Who, s.Now, c.ID))
		}
	case c.F("reached") == "" && !IsHeld(c) && len(waits) == 0 && Reachable(s, after, nil):
		set["reached"] = stamp(s.Now)
		u.Notes = append(u.Notes, reachedNote(s, after, nil, 0, r.Who))
		moved += "; reached"
	}
	for _, o := range s.Open {
		if o.Subject() != c.ID || (o.Note.Type != NBlocked && o.Note.Type != NMissingNeed) {
			continue
		}
		if len(o.Note.Needs) > 0 && slices.ContainsFunc(o.Note.Needs, func(n string) bool { return contains(r.Needs, n) }) {
			continue // it still names a need the sentinel keeps
		}
		u.Closes = append(u.Closes, o)
		u.Notes = append(u.Notes, decided(o, moved, r.Who, s.Now, c.ID))
	}
	u.Changes = []Change{change(Work, setEntry(c, set, unset...))}
	u.Moved = moved
	p.Units = append(p.Units, u)
	return Lawful(p)
}
