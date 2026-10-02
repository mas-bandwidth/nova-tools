package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Violation is one broken rule of docs/SPEC-SPRINT.md section 9, or a step
// cut short (section 10).
type Violation struct {
	Rule   int    `json:"rule"` // 1..9, 11 and 12 (a stall), or 10 for a cut step
	Detail string `json:"detail"`
}

func (v Violation) String() string { return fmt.Sprintf("rule %d: %s", v.Rule, v.Detail) }

// primarySet is a set of primary ids with the cards that put each one there.
type primarySet map[string][]string

func (ps primarySet) add(p, via string) { ps[p] = append(ps[p], via) }

// diff compares two sets of primaries exactly: every primary in one and not
// the other, and every primary the left side holds more than once.
func diff(rule int, left, right primarySet, lname, rname string) []Violation {
	var out []Violation
	for _, p := range slices.Sorted(maps.Keys(left)) {
		if len(left[p]) > 1 {
			out = append(out, Violation{rule, fmt.Sprintf("%s has %s %d times (%s)", lname, p, len(left[p]), strings.Join(left[p], ","))})
		}
		if _, ok := right[p]; !ok {
			out = append(out, Violation{rule, fmt.Sprintf("%s is in %s and not in %s", p, lname, rname)})
		}
	}
	for _, p := range slices.Sorted(maps.Keys(right)) {
		if len(right[p]) > 1 {
			out = append(out, Violation{rule, fmt.Sprintf("%s has %s %d times (%s)", rname, p, len(right[p]), strings.Join(right[p], ","))})
		}
		if _, ok := left[p]; !ok {
			out = append(out, Violation{rule, fmt.Sprintf("%s is in %s and not in %s", p, rname, lname)})
		}
	}
	return out
}

// Pending is the operation the sprint's fence holds, as check judges it: a
// rank's new scores, by primary, from its own manifest.
type Pending struct {
	ID, Verb string
	Scores   map[string]float64
}

// ranking says a copy's score and its primary's differ only because the
// pending rank is between its tables: one of them is the rank's new score.
func (p *Pending) ranking(primary string, a, b float64) bool {
	if p == nil {
		return false
	}
	t, ok := p.Scores[primary]
	return ok && (a == t || b == t)
}

// Check is what is always true, over an observed state with all four tables
// loaded (docs/SPEC-SPRINT.md section 9). Sets of primaries are compared
// exactly, member by member, never by their counts. pending is the operation
// the sprint's fence holds, if any: rules 2, 3, 4, 5 and 9 hold whenever no
// operation is pending, and are not judged while one is; 1, 6, 7 and 8 always,
// rule 7 against a pending rank's own new scores.
func Check(s *Snapshot, pending *Pending) []Violation {
	var out []Violation
	// The work table's queue is between the tables as a pending operation
	// is: the rules that compare the work table with another hold once the
	// pump has drained it (queue.go).
	quiet := pending == nil && s.QueueLen == 0
	// 1. One place in each table: the loaded cards are keyed by id, so a card
	// seen in two cells is caught by the loader; here, every placed card is on
	// a declared row.
	for _, t := range []*Table{s.Work, s.Readers, s.Merge, s.Fleet} {
		for _, c := range sortedCards(t) {
			if c.Placed() && !t.HasRow(c.Row) {
				out = append(out, Violation{1, fmt.Sprintf("%s: %s is placed on row %s, which the table does not declare", t.Name, c.ID, c.Row)})
			}
		}
	}
	// 1. A read card is on the row of the reader its id and its field name.
	for _, c := range sortedCards(s.Readers) {
		if c.Placed() && !ReadCardAgrees(c) {
			out = append(out, Violation{1, fmt.Sprintf("read card %s is on reader %s's row and names reader %s", c.ID, c.Row, orDash(c.F("reader")))})
		}
	}
	// 2. Primaries working = primaries of work cards in fleet ready + working.
	working, dealt := primarySet{}, primarySet{}
	for _, c := range s.Work.Column(Working) {
		working.add(c.ID, c.ID)
	}
	for _, c := range s.Fleet.Column(Ready, Working) {
		dealt.add(c.F("primary"), c.ID)
		if pr := s.Work.Placed(c.F("primary")); pr != nil && pr.Col == Working && pr.F("work") != c.ID {
			out = append(out, Violation{2, fmt.Sprintf("%s is dealt, and its primary %s names %s as its work card", c.ID, pr.ID, orDash(pr.F("work")))})
		}
	}
	if quiet {
		out = append(out, diff(2, working, dealt, "work working", "fleet ready+working")...)
		// A primary whose card was withdrawn is ready, not working.
		for _, c := range s.Fleet.Column(Withdrawn) {
			if st := s.StateOf(c.F("primary")); st != Ready {
				out = append(out, Violation{2, fmt.Sprintf("%s is withdrawn and its primary %s is %s, not ready", c.ID, c.F("primary"), orDash(st))})
			}
		}
	}
	// 3. Read cards in asked or reading belong to primaries in review.
	for _, c := range s.Readers.Column(Asked, Reading) {
		if st := s.StateOf(c.F("primary")); quiet && st != Review {
			out = append(out, Violation{3, fmt.Sprintf("%s is %s and its primary %s is %s", c.ID, c.Col, c.F("primary"), orDash(st))})
		}
	}
	// 4. Merge queued + stuck = work merging.
	merging, inMerge := primarySet{}, primarySet{}
	for _, c := range s.Work.Column(Merging) {
		merging.add(c.ID, c.ID)
	}
	for _, c := range s.Merge.Column(Queued, Stuck) {
		inMerge.add(c.ID, c.Col)
		if pr := s.Work.Placed(c.ID); pr != nil && pr.Row != c.Row {
			out = append(out, Violation{4, fmt.Sprintf("%s is queued in stream %s and belongs to %s", c.ID, c.Row, pr.Row)})
		}
	}
	if quiet {
		out = append(out, diff(4, merging, inMerge, "work merging", "merge queued+stuck")...)
	}
	// 4. A merge card names a card it needs (need_card) only while it is
	// stuck in a stream stopped for a cross: a return, a conflict and a
	// resume each clear it.
	for _, c := range sortedCards(s.Merge) {
		if !c.Placed() || c.F("need_card") == "" {
			continue
		}
		if ctl := s.StreamCtl(c.Row); c.Col != Stuck || ctl.F("state") != StreamStopped || ctl.F("cause") != "cross" {
			out = append(out, Violation{4, fmt.Sprintf("%s is %s in stream %s (%s, cause %s) and still names %s as a card it needs, which only a cross stop's stuck card does", c.ID, c.Col, c.Row, orDash(ctl.F("state")), orDash(ctl.F("cause")), c.F("need_card"))})
		}
	}
	// 5. Merge merged = work landed.
	landed, merged := primarySet{}, primarySet{}
	for _, c := range s.Work.Column(Landed) {
		if !IsSentinel(c) { // a sentinel lands by release, never merged
			landed.add(c.ID, c.ID)
		}
	}
	for _, c := range s.Merge.Column(Merged) {
		merged.add(c.ID, c.Col)
	}
	if quiet {
		out = append(out, diff(5, landed, merged, "work landed", "merge merged")...)
	}
	// 6. Nothing enters merging without ok reads from two different readers at its head.
	for _, c := range s.Work.Column(Merging, Landed) {
		if IsSentinel(c) {
			continue // never read
		}
		readers := map[string]bool{}
		for _, rc := range s.Readers.Of(c.ID) {
			if rc.Col == OK && rc.F("head") == c.F("head") && ReadCardAgrees(rc) {
				readers[rc.F("reader")] = true
			}
		}
		if len(readers) < 2 {
			out = append(out, Violation{6, fmt.Sprintf("%s is %s with ok reads at head %s from %d reader(s)", c.ID, c.Col, orDash(c.F("head")), len(readers))})
		}
	}
	// 7. A score never changes except by rank: every copy has its primary's score.
	for _, t := range []*Table{s.Fleet, s.Readers, s.Merge} {
		for _, c := range sortedCards(t) {
			if !c.Placed() || c.Col == Ctl {
				continue
			}
			pid := c.F("primary")
			if t == s.Merge {
				pid = c.ID
			}
			if pr := s.Work.Card(pid); pr != nil && pr.Placed() && pr.Score != c.Score && !pending.ranking(pid, pr.Score, c.Score) && s.QueueLen == 0 {
				out = append(out, Violation{7, fmt.Sprintf("%s: %s has score %s and its primary %s has %s", t.Name, c.ID, fmtScore(c.Score), pid, fmtScore(pr.Score))})
			}
		}
	}
	// 8. No card lost or made twice: a card in flight names a primary on the
	// table, and a primary has at most one live work card.
	for _, c := range append(s.Fleet.Column(Ready, Working), s.Readers.Column(Asked, Reading)...) {
		if s.Work.Placed(c.F("primary")) == nil {
			out = append(out, Violation{8, fmt.Sprintf("%s is in flight and its primary %s is not on the table", c.ID, orDash(c.F("primary")))})
		}
	}
	for _, p := range slices.Sorted(maps.Keys(dealt)) {
		if len(dealt[p]) > 1 {
			out = append(out, Violation{8, fmt.Sprintf("%s has %d live work cards (%s)", p, len(dealt[p]), strings.Join(dealt[p], ","))})
		}
	}
	for _, c := range s.Merge.Column(Queued, Stuck, Merged, Returned) {
		if s.Work.Placed(c.ID) == nil {
			out = append(out, Violation{8, fmt.Sprintf("%s is in merge %s and not on the work table", c.ID, c.Col)})
		}
	}
	// 9. A stopped stream has an open judgment notification.
	for _, st := range s.Merge.Rows() {
		ctl := s.StreamCtl(st)
		if !quiet || ctl.F("state") != StreamStopped {
			continue
		}
		open := false
		for _, o := range s.Open {
			if o.Subject() == StreamSubject(st) && o.Note.Kind == Judgment {
				open = true
			}
		}
		if !open {
			out = append(out, Violation{9, fmt.Sprintf("stream %s is stopped (%s) with no open judgment notification", st, ctl.F("cause"))})
		}
	}
	// 11. A primary anywhere but waiting has every need landed or waived.
	for _, c := range s.Work.Column(Ready, Working, Review, Merging, Landed) {
		if w := NamedWaits(s, c, nil); len(w) > 0 {
			out = append(out, Violation{11, fmt.Sprintf("%s is %s and needs %s, not landed", c.ID, c.Col, strings.Join(w, ","))})
		}
	}
	// 11. The needs make no cycle: add refuses the add that would close one
	// (errata 3 amendment 7), and a store written before add walked the
	// sentinels' needs may still hold one; each is named with the cards it
	// keeps from ever being reached.
	for _, f := range Cycles(s) {
		out = append(out, Violation{11, f.String()})
	}
	// 10. A step that did not finish: the fence holds it.
	if pending != nil {
		out = append(out, Violation{10, "operation " + pending.ID + " (" + pending.Verb + ") is pending; run: nova-sprint repair"})
	}
	return out
}

func sortedCards(t *Table) []*Card {
	return t.Cards()
}
