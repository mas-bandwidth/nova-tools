package sprint

import (
	"fmt"
	"strings"
)

// The steps of review: ask and read (mechanical, and the readers' own), and
// the coordinator's verbs accept, rework, return, drop, rank and ci.

// AskReq deals primaries in review to readers.
type AskReq struct {
	Sel
	Another bool // one more reader for a primary already asked
	Answers []string
	Who     string
}

// readsAt is the primary's placed read cards at an attempt, in reader row order.
func readsAt(s *Snapshot, pr *Card, attempt int) []*Card {
	var out []*Card
	for _, r := range s.Readers.Rows {
		c := s.Readers.Placed(ReadCardID(pr.ID, attempt, r))
		if c != nil {
			out = append(out, c)
		}
	}
	return out
}

// Ask deals every primary in review that lacks reads to TWO DIFFERENT readers,
// each to the shortest asked queue, in work order; a primary reworked after a
// read is asked of the same readers again. With Another, a primary already
// asked is dealt to one more reader.
func Ask(s *Snapshot, r AskReq) Plan {
	var p Plan
	chosen := pick(&p, r.Sel, s.Work.Column(Review), rowOf, func(c *Card) string {
		if why := inState(c, Review); why != "" {
			return why
		}
		if c.F("result") == "failed" {
			return "its work came back failed: rework or drop it"
		}
		have := len(readsAt(s, c, c.Int("attempt")))
		if r.Another && have == 0 {
			return "not asked yet: run nova-sprint ask " + c.ID
		}
		if !r.Another && have > 0 {
			return "asked already"
		}
		return ""
	}, s.primaryCard)
	q := map[string]int{}
	for _, rd := range s.Readers.Rows {
		q[rd] = s.Readers.Count(rd, Asked)
	}
	for _, c := range chosen {
		attempt := c.Int("attempt")
		have := map[string]bool{}
		var all []string
		for _, rc := range readsAt(s, c, attempt) {
			have[rc.F("reader")] = true
			all = append(all, rc.F("reader"))
		}
		var free []string
		for _, rd := range s.Readers.Rows {
			if !have[rd] {
				free = append(free, rd)
			}
		}
		want := 2
		if r.Another {
			want = 1
		}
		var chosenReaders []string
		if !r.Another {
			for _, rd := range Split(c.F("asked")) {
				if contains(free, rd) && len(chosenReaders) < want {
					chosenReaders = append(chosenReaders, rd)
				}
			}
		}
		for len(chosenReaders) < want {
			var left []string
			for _, rd := range free {
				if !contains(chosenReaders, rd) {
					left = append(left, rd)
				}
			}
			if len(left) == 0 {
				break
			}
			chosenReaders = append(chosenReaders, shortest(left, q))
		}
		if len(chosenReaders) < want {
			p.refuse(c.ID, fmt.Sprintf("needs %d different readers and has %d free; run: nova-sprint reader add <name>", want, len(chosenReaders)))
			continue
		}
		u := Unit{Key: c.ID}
		for _, rd := range chosenReaders {
			q[rd]++
			u.Changes = append(u.Changes, change(Readers, createEntry(ReadCardID(c.ID, attempt, rd), rd, Asked, c.Score,
				map[string]string{"kind": "read", "primary": c.ID, "stream": c.Row, "reader": rd, "attempt": itoa(attempt), "head": c.F("head")})))
		}
		all = append(all, chosenReaders...)
		u.Changes = append(u.Changes, change(Work, setEntry(c, map[string]string{"asked": strings.Join(all, ",")})))
		u.Moved = c.ID + " asked of " + strings.Join(chosenReaders, ", ")
		if r.Another {
			u.Closes = closesFor(s.Open, c.ID)
		}
		p.Units = append(p.Units, u)
	}
	p.Closes = answering(s.Open, r.Answers)
	return p
}

// ReadReq is a reader moving its own read cards.
type ReadReq struct {
	Sel
	As      string
	Begin   bool   // asked -> reading
	Verdict string // ok or broken
	Finding string
	Who     string
}

// Read moves a reader's read cards: asked -> reading, or asked|reading -> ok|broken
// with the finding. A broken read is a judgment; the second different reader's
// ok at the primary's head says it is ready to accept.
func Read(s *Snapshot, r ReadReq) Plan {
	var p Plan
	sel := r.Sel
	if len(sel.IDs) == 0 && sel.Only == nil && sel.Limit == 0 {
		sel.Limit = 1
	}
	from := []string{Asked, Reading}
	if r.Begin {
		from = []string{Asked}
	}
	var all []*Card
	for _, col := range from {
		all = append(all, s.Readers.Cell(r.As, col)...)
	}
	SortCards(all)
	chosen := pick(&p, sel, all, fieldStream, func(c *Card) string {
		if !c.Placed() || c.Row != r.As {
			return "not " + r.As + "'s to read (it is " + placeWord(c) + ")"
		}
		if !contains(from, c.Col) {
			return "not " + strings.Join(from, " or ") + " (it is " + c.Col + ")"
		}
		return ""
	}, s.Readers.Card)
	for _, c := range chosen {
		pr := s.Work.Card(c.F("primary"))
		if r.Begin {
			p.Units = append(p.Units, Unit{Key: c.ID, Changes: []Change{change(Readers, moveEntry(c, c.Row, Reading, nil))},
				Moved: c.ID + " asked -> reading"})
			continue
		}
		col := OK
		if r.Verdict == "broken" {
			col = Broken
		}
		set := map[string]string{"verdict": r.Verdict, "read": stamp(s.Now)}
		if r.Finding != "" {
			set["finding"] = r.Finding
		}
		u := Unit{Key: c.ID, Changes: []Change{change(Readers, moveEntry(c, c.Row, col, set))},
			Moved: fmt.Sprintf("%s %s -> %s", c.ID, c.Col, col)}
		if pr != nil {
			attempt := c.Int("attempt")
			if col == Broken {
				before := pr.Int("broken_reads")
				for _, o := range s.Readers.Of(pr.ID) {
					if o.Col == Broken && o.ID != c.ID {
						before++
					}
				}
				n := judgment(NReadBroken, pr.Row, s.Now, before, pr.ID)
				n.Who, n.Attempt, n.What = r.As, attempt, r.Finding
				u.Notes = append(u.Notes, n)
			} else if pr.Col == Review && attempt == pr.Int("attempt") && c.F("head") == pr.F("head") {
				others := map[string]bool{}
				for _, o := range readsAt(s, pr, attempt) {
					if o.ID != c.ID && o.Col == OK && o.F("head") == pr.F("head") {
						others[o.F("reader")] = true
					}
				}
				if len(others) == 1 {
					n := happened(NReadyToAccept, pr.Row, s.Now, pr.ID)
					n.Who, n.Attempt = r.As, attempt
					u.Notes = append(u.Notes, n)
				}
			}
		}
		p.Units = append(p.Units, u)
	}
	return p
}

// AcceptReq is the coordinator accepting primaries in review.
type AcceptReq struct {
	Sel
	Answers []string
	Who     string
}

// okReaders is two ok read cards from different readers at the primary's
// current attempt and head, in reader row order; fewer when it has fewer.
func okReaders(s *Snapshot, pr *Card) []*Card {
	var out []*Card
	for _, c := range readsAt(s, pr, pr.Int("attempt")) {
		if c.Col == OK && c.F("head") == pr.F("head") && len(out) < 2 {
			out = append(out, c)
		}
	}
	return out
}

// Accept moves review -> merging and places the primary in merge queued with
// its score. It is refused without ok reads from two different readers at the
// primary's head, whoever the readers.
func Accept(s *Snapshot, r AcceptReq) Plan {
	var p Plan
	chosen := pick(&p, r.Sel, s.Work.Column(Review), rowOf, func(c *Card) string {
		if why := inState(c, Review); why != "" {
			return why
		}
		if oks := okReaders(s, c); len(oks) < 2 {
			var names []string
			for _, o := range oks {
				names = append(names, o.F("reader"))
			}
			return fmt.Sprintf("needs ok from two different readers at head %s; has ok from %d (%s)", orDash(c.F("head")), len(oks), orDash(strings.Join(names, ",")))
		}
		if m := s.Merge.Card(c.ID); m != nil && (!m.Placed() || m.Col != Returned) {
			return "its merge record is " + placeWord(m)
		}
		if s.StreamCtl(c.Row) == nil {
			return "stream " + c.Row + " has no merge row"
		}
		return ""
	}, s.primaryCard)
	for _, c := range chosen {
		oks := okReaders(s, c)
		u := Unit{Key: c.ID}
		for _, o := range oks {
			u.Changes = append(u.Changes, change(Readers, guardEntry(o)))
		}
		if m := s.Merge.Placed(c.ID); m != nil {
			e := moveEntry(m, c.Row, Queued, nil)
			sc := c.Score
			e.Move.Score = &sc
			u.Changes = append(u.Changes, change(Merge, e))
		} else {
			u.Changes = append(u.Changes, change(Merge, createEntry(c.ID, c.Row, Queued, c.Score,
				map[string]string{"kind": "merge", "primary": c.ID, "stream": c.Row})))
		}
		readers := oks[0].F("reader") + "," + oks[1].F("reader")
		u.Changes = append(u.Changes, change(Work, moveEntry(c, c.Row, Merging, map[string]string{"readers": readers, "accepted": stamp(s.Now)})))
		u.Closes = closesFor(s.Open, c.ID)
		u.Moved = fmt.Sprintf("%s review -> merging queued (ok from %s)", c.ID, strings.ReplaceAll(readers, ",", ", "))
		p.Units = append(p.Units, u)
	}
	p.Closes = answering(s.Open, r.Answers)
	return p
}

// ReworkReq is the coordinator sending primaries back with a fix.
type ReworkReq struct {
	Sel
	Fix     string
	Answers []string
	Who     string
}

// Rework moves review -> ready with the fix, retires the primary's read cards
// and counts the reads; the next work card attempt is cut when it is started
// again, and when the fixed work returns it is asked of the same readers.
func Rework(s *Snapshot, r ReworkReq) Plan {
	var p Plan
	chosen := pick(&p, r.Sel, s.Work.Column(Review), rowOf, func(c *Card) string {
		if c.Placed() && c.Col == Merging {
			return "merging: return it first: nova-sprint return " + c.ID
		}
		return inState(c, Review)
	}, s.primaryCard)
	for _, c := range chosen {
		attempt := c.Int("attempt")
		var asked []string
		broken := 0
		u := Unit{Key: c.ID}
		for _, rc := range s.Readers.Of(c.ID) {
			if rc.Int("attempt") == attempt && !contains(asked, rc.F("reader")) {
				asked = append(asked, rc.F("reader"))
			}
			if rc.Col == Broken {
				broken++
			}
			u.Changes = append(u.Changes, change(Readers, removeEntry(rc, map[string]string{"retired": stamp(s.Now)})))
		}
		askedField := c.F("asked")
		if len(asked) > 0 {
			askedField = strings.Join(orderLike(s.Readers.Rows, asked, ""), ",")
		}
		set := map[string]string{"fix": r.Fix, "reworks": itoa(c.Int("reworks") + 1), "broken_reads": itoa(c.Int("broken_reads") + broken)}
		if askedField != "" {
			set["asked"] = askedField
		}
		u.Changes = append(u.Changes, change(Work, moveEntry(c, c.Row, Ready, set, "result", "readers")))
		u.Closes = closesFor(s.Open, c.ID)
		u.Moved = fmt.Sprintf("%s review -> ready (fix; %d read cards retired)", c.ID, len(u.Changes)-1)
		p.Units = append(p.Units, u)
	}
	p.Closes = answering(s.Open, r.Answers)
	return p
}

// ReturnReq is the coordinator sending merging primaries back to review.
type ReturnReq struct {
	Sel
	Reason  string
	Answers []string
	Who     string
}

// Return moves merging -> review: off the merge queue (or stuck), into the
// merge table's hidden returned column, so a later accept moves it back.
func Return(s *Snapshot, r ReturnReq) Plan {
	var p Plan
	chosen := pick(&p, r.Sel, s.Work.Column(Merging), rowOf, func(c *Card) string {
		if why := inState(c, Merging); why != "" {
			return why
		}
		if m := s.Merge.Placed(c.ID); m == nil || (m.Col != Queued && m.Col != Stuck) {
			return "not queued or stuck in merge (it is " + placeWord(orEmpty(s.Merge.Card(c.ID), c.ID)) + ")"
		}
		return ""
	}, s.primaryCard)
	for _, c := range chosen {
		m := s.Merge.Placed(c.ID)
		set := map[string]string{"returns": itoa(c.Int("returns") + 1)}
		if r.Reason != "" {
			set["return_reason"] = r.Reason
		}
		p.Units = append(p.Units, Unit{Key: c.ID, Changes: []Change{
			change(Merge, moveEntry(m, c.Row, Returned, nil)),
			change(Work, moveEntry(c, c.Row, Review, set)),
		}, Closes: closesFor(s.Open, c.ID), Moved: fmt.Sprintf("%s merging -> review (off merge %s)", c.ID, m.Col)})
	}
	p.Closes = answering(s.Open, r.Answers)
	return p
}

func orEmpty(c *Card, id string) *Card {
	if c == nil {
		return &Card{ID: id, Fields: map[string]string{"outcome": "none"}}
	}
	return c
}

// DropReq is the coordinator taking primaries off the table.
type DropReq struct {
	Sel
	Reason  string
	Answers []string
	Who     string
}

// Drop takes open primaries off the table with the reason: their record,
// outcome and reason are kept; their live work card, unread read cards and
// merge place go with them. Waiting primaries that need one are blocked, and
// the coordinator is told.
func Drop(s *Snapshot, r DropReq) Plan {
	var p Plan
	var all []*Card
	for _, st := range []State{Waiting, Ready, Working, Review, Merging} {
		all = append(all, s.Work.Column(st)...)
	}
	chosen := pick(&p, r.Sel, all, rowOf, func(c *Card) string {
		if !c.Placed() {
			return "not on the table (" + orDash(c.F("outcome")) + ")"
		}
		if !IsOpen(c.Col) {
			return "landed; landed is final"
		}
		return ""
	}, s.primaryCard)
	dropping := map[string]bool{}
	for _, c := range chosen {
		dropping[c.ID] = true
	}
	for _, c := range chosen {
		u := Unit{Key: c.ID}
		for _, fc := range s.Fleet.Of(c.ID) {
			if fc.Col == Ready || fc.Col == Working {
				u.Changes = append(u.Changes, change(Fleet, removeEntry(fc, map[string]string{"dropped": stamp(s.Now)})))
			}
		}
		for _, rc := range s.Readers.Of(c.ID) {
			if rc.Col == Asked || rc.Col == Reading {
				u.Changes = append(u.Changes, change(Readers, removeEntry(rc, map[string]string{"dropped": stamp(s.Now)})))
			}
		}
		if m := s.Merge.Placed(c.ID); m != nil {
			u.Changes = append(u.Changes, change(Merge, removeEntry(m, map[string]string{"dropped": stamp(s.Now)})))
		}
		u.Changes = append(u.Changes, change(Work, removeEntry(c, map[string]string{
			"outcome": "dropped", "reason": r.Reason, "dropped_from": c.Col, "dropped_at": stamp(s.Now)})))
		for _, w := range s.Work.Column(Waiting) {
			if dropping[w.ID] || !contains(Split(w.F("needs")), c.ID) || hasOpen(s.Open, NBlocked, w.ID) {
				continue
			}
			n := judgment(NBlocked, w.Row, s.Now, 0, w.ID)
			n.What, n.Who = w.ID+" needs "+c.ID+", dropped", r.Who
			u.Notes = append(u.Notes, n)
		}
		u.Closes = closesFor(s.Open, c.ID)
		u.Moved = fmt.Sprintf("%s %s -> off the table (%s)", c.ID, c.Col, r.Reason)
		p.Units = append(p.Units, u)
	}
	p.Closes = answering(s.Open, r.Answers)
	return p
}

// RankReq is the coordinator changing scores.
type RankReq struct {
	IDs     []string
	Score   *float64 // the new score of the first; the rest follow it
	First   bool     // ahead of every primary of its stream
	Answers []string
	Who     string
	Only    []string
}

// Rank changes a primary's score, and every copy of it: its placed work
// cards, read cards and merge place. Only rank changes a score.
func Rank(s *Snapshot, r RankReq) Plan {
	var p Plan
	chosen := pick(&p, Sel{IDs: r.IDs, Only: r.Only}, nil, rowOf, func(c *Card) string {
		if !c.Placed() {
			return "not on the table"
		}
		return ""
	}, s.primaryCard)
	score := 0.0
	if r.Score != nil {
		score = *r.Score
	} else if r.First {
		low := 0.0
		first := true
		for _, c := range s.Work.Cards {
			if c.Placed() && (first || c.Score < low) {
				low, first = c.Score, false
			}
		}
		score = low - float64(len(chosen))
	}
	for _, c := range chosen {
		if c.Score == score {
			p.refuse(c.ID, "already at score "+fmtScore(score))
			score++
			continue
		}
		u := Unit{Key: c.ID}
		for _, fc := range s.Fleet.Of(c.ID) {
			u.Changes = append(u.Changes, change(Fleet, scoreEntry(fc, score)))
		}
		for _, rc := range s.Readers.Of(c.ID) {
			u.Changes = append(u.Changes, change(Readers, scoreEntry(rc, score)))
		}
		if m := s.Merge.Placed(c.ID); m != nil {
			u.Changes = append(u.Changes, change(Merge, scoreEntry(m, score)))
		}
		u.Changes = append(u.Changes, change(Work, scoreEntry(c, score)))
		u.Closes = closesFor(s.Open, c.ID)
		u.Moved = fmt.Sprintf("%s score %s -> %s (%d copies)", c.ID, fmtScore(c.Score), fmtScore(score), len(u.Changes)-1)
		p.Units = append(p.Units, u)
		score++
	}
	p.Closes = answering(s.Open, r.Answers)
	return p
}

// CIReq records a CI result for primaries, in any state.
type CIReq struct {
	Sel
	Red  bool
	Note string
	Who  string
}

// RecordCI records the result on the primary; red is a judgment, green happened.
// A CI result recorded for a primary is always a notification.
func RecordCI(s *Snapshot, r CIReq) Plan {
	var p Plan
	var all []*Card
	for _, st := range States {
		all = append(all, s.Work.Column(st)...)
	}
	chosen := pick(&p, r.Sel, all, rowOf, func(c *Card) string {
		if !c.Placed() {
			return "not on the table"
		}
		return ""
	}, s.primaryCard)
	result := "green"
	if r.Red {
		result = "red"
	}
	for _, c := range chosen {
		set := map[string]string{"ci": result, "ci_at": stamp(s.Now)}
		if r.Note != "" {
			set["ci_note"] = r.Note
		}
		var n Note
		if r.Red {
			n = judgment(NCIRed, c.Row, s.Now, 0, c.ID)
		} else {
			n = happened(NCIGreen, c.Row, s.Now, c.ID)
		}
		n.What, n.Who, n.Attempt = r.Note, r.Who, c.Int("attempt")
		p.Units = append(p.Units, Unit{Key: c.ID, Changes: []Change{change(Work, setEntry(c, set))}, Notes: []Note{n},
			Moved: fmt.Sprintf("%s ci %s (%s)", c.ID, result, c.Col)})
	}
	return p
}
