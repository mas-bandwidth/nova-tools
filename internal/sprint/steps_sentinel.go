package sprint

import (
	"fmt"
	"strings"
)

// Sentinel cards (docs/SPEC-SPRINT.md, sentinel cards): a sentinel is a
// primary of kind sentinel, a stop in its stream. It waits for every primary
// of its stream that sorts before it (and for the needs it names in other
// streams); what sorts after it waits for it. It is never dealt, read or
// merged. When everything it needs has landed, the step that landed the last
// of them marks it reached and tells the coordinator; only the coordinator's
// release lands it, and the same step moves what waited behind it to ready.

// IsSentinel says the primary is a sentinel.
func IsSentinel(c *Card) bool { return c.F("kind") == "sentinel" }

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
		if fc.Col == Withdrawn {
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

// mod is a change a step makes to a primary already in line: its needs, a
// move back to waiting, and the step's reason when it is no longer reached.
type mod struct {
	needs   []string
	named   int // the needs it names before the step: needs are written only when this step adds one
	to      State
	unreach string
}

func modOf(mods map[string]*mod, c *Card) *mod {
	if m := mods[c.ID]; m != nil {
		return m
	}
	m := &mod{needs: Split(c.F("needs"))}
	m.named = len(m.needs)
	mods[c.ID] = m
	return m
}

func (m *mod) addNeed(id string) {
	if !contains(m.needs, id) {
		m.needs = append(m.needs, id)
	}
}

func (m *mod) change(c *Card) Change {
	var set map[string]string
	if len(m.needs) != m.named {
		set = map[string]string{"needs": strings.Join(m.needs, ",")}
	}
	var unset []string
	if m.unreach != "" {
		unset = append(unset, "reached")
	}
	if m.to != "" {
		return change(Work, moveEntry(c, c.Row, m.to, set, unset...))
	}
	return change(Work, setEntry(c, set, unset...))
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

// reachUnit marks a sentinel reached, with the time, and tells the
// coordinator once.
func reachUnit(s *Snapshot, c *Card, landing map[string]bool, who string) Unit {
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, map[string]string{"reached": stamp(s.Now)}))},
		Notes: []Note{reachedNote(s, c, landing, 0, who)}, Moved: "sentinel " + c.ID + " reached"}
}

// SentinelsDue marks reached every sentinel whose needs have all landed (or
// were waived) and that is not reached yet: the backstop of the steps that
// land. It never lands a sentinel; only release does.
func SentinelsDue(s *Snapshot, who string) Plan {
	var p Plan
	p.on(s)
	for _, c := range s.Work.Column(Waiting) {
		if IsSentinel(c) && c.F("reached") == "" && len(WaitsFor(s, c, nil)) == 0 {
			p.Units = append(p.Units, reachUnit(s, c, nil, who))
		}
	}
	return Lawful(p)
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

// Release lands reached sentinels (waiting -> landed, the only step that may)
// and, in the same step, moves every waiting primary whose needs have all now
// landed to ready, and marks reached every sentinel that is now due. It
// closes the judgments open on each sentinel and always writes that it
// landed, by whom, why, and how many cards are now ready.
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
	landing := map[string]bool{}
	var chosen []*Card
	for _, id := range r.IDs {
		c := s.Work.Card(id)
		switch {
		case landing[id]:
			p.refuse(id, "named twice")
			continue
		case !c.Placed():
			p.refuse(id, "not on the table")
			continue
		case !IsSentinel(c):
			p.refuse(id, "not a sentinel: a primary lands by merging")
			continue
		case c.Col != Waiting:
			p.refuse(id, "is "+c.Col+", not a sentinel waiting to be released")
			continue
		}
		if w := WaitsFor(s, c, nil); len(w) > 0 || c.F("reached") == "" {
			var st []string
			for _, n := range w {
				st = append(st, n+" ("+orDash(s.StateOf(n))+")")
			}
			p.refuse(id, "not reached: it waits for "+strings.Join(st, ", "))
			continue
		}
		landing[id] = true
		chosen = append(chosen, c)
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
		n := happened(NSentinelLanded, c.Row, s.Now, c.ID)
		n.Who = r.Who
		n.What = fmt.Sprintf("sentinel %s landed, released by %s: %d cards are now ready; %s", c.ID, r.Who, ready, r.Reason)
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row,
			Changes: []Change{change(Work, moveEntry(c, c.Row, Landed, map[string]string{
				"landed": stamp(s.Now), "released": stamp(s.Now), "released_by": r.Who, "release_reason": r.Reason}))},
			Notes: []Note{n}, Closes: closesFor(s.Open, nil, c.ID),
			Moved: fmt.Sprintf("sentinel %s waiting -> landed (released by %s); %d cards are now ready", c.ID, r.Who, ready)})
	}
	if len(chosen) == 0 {
		return p
	}
	p.Units = append(p.Units, after...)
	settle(&p, s, r.Who, nil, nil, landing)
	// A sprint this release finishes is found done by the tick's done part
	// (TickDone), which says so and stops the machine.
	answered(&p, s, r.Answers, r.Who)
	return Lawful(p)
}
