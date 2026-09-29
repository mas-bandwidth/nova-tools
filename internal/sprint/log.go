package sprint

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The sprint's log (docs/SPEC-SPRINT.md, the log): one append-only record per
// epoch. Every change a step makes to a card is a move line, written in the
// same write as the change, and every notification the step writes is a line
// of its own kind beside them; nothing in it is ever rewritten, and the
// inbox's cursor never hides it. Replaying an epoch's move lines from empty
// gives every card's place and generation (rule 13). The log has no hidden
// lines: every line is every actor's to read.

// LineMove is the kind of a card's change. A notification's line has the
// notification's kind (judgment, happened, decided, acknowledged); a note on
// a card, when there is one, will be a line of kind "note".
const LineMove = "move"

// Line is one line of the log.
type Line struct {
	Kind  string    `json:"kind"`
	At    time.Time `json:"at"`
	Epoch uint64    `json:"epoch"`
	Op    string    `json:"op,omitempty"` // the step that wrote it: one op, many lines
	// The card a move line is of, its primary and its stream.
	Card    string `json:"card,omitempty"`
	Primary string `json:"primary,omitempty"`
	Stream  string `json:"stream,omitempty"`
	// Table is the logical table; From and To are member:column ("" From:
	// the step created it; Removed: the step took it off the table); Gen is
	// its generation after the step (0 when it has none).
	Table   string `json:"table,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Removed bool   `json:"removed,omitempty"`
	Gen     int    `json:"gen,omitempty"`
	// Actor is who acted (the machine, or the named actor); Verb is the
	// step; Cause is why, in the step's own words.
	Actor string `json:"actor,omitempty"`
	Verb  string `json:"verb,omitempty"`
	Cause string `json:"cause,omitempty"`
	// Answers is the judgments the step answered on this card.
	Answers []string `json:"answers,omitempty"`
	// Text is the words given with the change, whole, by field: fix,
	// report, finding, reason, return_reason, did, note, ci_note, brief.
	Text map[string]string `json:"text,omitempty"`
	// Set is the other fields the change set (a stamp, a counter, a head).
	Set map[string]string `json:"set,omitempty"`
	// Note is a notification's line: the notification as written.
	Note *Note `json:"note,omitempty"`
}

// TextFields are the fields whose words a move line carries in Text.
var TextFields = []string{"brief", "fix", "report", "finding", "reason", "return_reason", "did", "note", "ci_note"}

// Names is the cards a line is about: a move line's card, or a
// notification's subjects and named cards.
func (l Line) Names() []string {
	if l.Note == nil {
		return []string{l.Card}
	}
	out := append([]string(nil), l.Note.Subjects()...)
	for _, c := range []string{l.Note.Card, l.Note.Other} {
		if c != "" && !contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// About says the line is about the card: the card itself, or, for a
// primary, its work, read and merge cards (named <primary>.w<n>,
// <primary>.r<n>.<reader>, and the primary's own id in merge).
func (l Line) About(id string) bool {
	for _, n := range l.Names() {
		if n == id || strings.HasPrefix(n, id+".") {
			return true
		}
	}
	return l.Note == nil && l.Primary == id
}

// Place is a card's place and generation, as the log replays it.
type Place struct {
	Row, Col string
	Gen      int
}

// Replay is every card's place and generation after the log's move lines,
// in order, from empty: keyed by table and card id. A card the log removed
// is absent.
func Replay(lines []Line) map[string]Place {
	out := map[string]Place{}
	for _, l := range lines {
		if l.Kind != LineMove || l.Card == "" {
			continue
		}
		k := l.Table + "/" + l.Card
		if l.Removed {
			delete(out, k)
			continue
		}
		row, col, ok := strings.Cut(l.To, ":")
		if !ok {
			continue // a change of an unplaced card's fields
		}
		out[k] = Place{Row: row, Col: col, Gen: l.Gen}
	}
	return out
}

// LogViolations is rule 13 over an observed state and its epoch's log: the
// log replays to every card's place and generation (the control cards, which
// hold a row's state and no card, aside). Judged only when no operation is
// pending.
func LogViolations(s *Snapshot, lines []Line) []Violation {
	placed := Replay(lines)
	var out []Violation
	seen := map[string]bool{}
	for _, t := range []*Table{s.Work, s.Readers, s.Merge, s.Fleet} {
		if t == nil {
			continue
		}
		for _, c := range sortedCards(t) {
			if !c.Placed() || c.Col == Ctl {
				continue
			}
			k := t.Name + "/" + c.ID
			seen[k] = true
			p, ok := placed[k]
			switch {
			case !ok:
				out = append(out, Violation{13, fmt.Sprintf("%s: %s is at %s:%s and the log never placed it there", t.Name, c.ID, c.Row, c.Col)})
			case p.Row != c.Row || p.Col != c.Col:
				out = append(out, Violation{13, fmt.Sprintf("%s: %s is at %s:%s and the log last moved it to %s:%s", t.Name, c.ID, c.Row, c.Col, p.Row, p.Col)})
			case p.Gen != c.Int("gen"):
				out = append(out, Violation{13, fmt.Sprintf("%s: %s is at generation %d and the log says %d", t.Name, c.ID, c.Int("gen"), p.Gen)})
			}
		}
	}
	var gone []string
	for k, p := range placed {
		if seen[k] || p.Col == Ctl {
			continue
		}
		table, _, _ := strings.Cut(k, "/")
		if tb := s.T(table); tb == nil {
			continue
		}
		gone = append(gone, fmt.Sprintf("%s is not on the table and the log last moved it to %s:%s", k, p.Row, p.Col))
	}
	sort.Strings(gone)
	for _, g := range gone {
		out = append(out, Violation{13, g})
	}
	return out
}

// NoteLine is a notification's line of the log, written with the step that
// wrote the notification.
func NoteLine(n Note, op string) Line {
	nn := n
	return Line{Kind: n.Kind, At: n.At, Epoch: IDEpoch(n.ID), Op: op, Stream: n.Stream, Actor: n.Who, Note: &nn}
}

// StreamViolations is rule 14 over the epoch's log and its inbox stream:
// every notification is written to both, so each notification line of the
// log (an update aside, which rewrites an open judgment in place and is the
// log's alone) has its entry in the inbox with the same id, kind, type and
// text, and each inbox entry its line in the log.
func StreamViolations(lines []Line, inbox []Note) []Violation {
	key := func(n Note) string { return n.ID + " (" + n.Kind + ", " + n.Type + "): " + n.What }
	logged := map[string]int{} // each notification as written, counted
	var order []string
	for _, l := range lines {
		if l.Note == nil || l.Verb == "updated" {
			continue
		}
		k := key(*l.Note)
		if logged[k] == 0 {
			order = append(order, k)
		}
		logged[k]++
	}
	var out []Violation
	for _, n := range inbox {
		k := key(n)
		if logged[k] == 0 {
			out = append(out, Violation{14, "notification " + k + " is in the inbox and not in the log"})
			continue
		}
		logged[k]--
	}
	for _, k := range order {
		if logged[k] > 0 {
			out = append(out, Violation{14, "notification " + k + " is in the log and not in the inbox"})
		}
	}
	return out
}
