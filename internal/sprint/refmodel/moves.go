package refmodel

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The kinds of a move: what one decision of a duty does.
const (
	KindCreate  = "create"  // a card is placed for the first time
	KindRemove  = "remove"  // a card leaves the table, its record kept
	KindMove    = "move"    // a card changes place, and maybe its fields or score
	KindSet     = "set"     // a card changes fields or score where it is
	KindBump    = "bump"    // a counter of a card is raised or lowered
	KindProp    = "prop"    // a table property is set (Card is its name, Set its name=value)
	KindRow     = "row"     // a row is added to a table
	KindOpen    = "open"    // a judgment is opened
	KindNotice  = "notice"  // a notification that needs no decision
	KindDecided = "decided" // a judgment is answered
	KindHold    = "hold"    // an acknowledged condition is kept
	KindClose   = "close"   // an open judgment closes on one subject
	KindUpdate  = "update"  // an open judgment is rewritten with the latest facts
	KindRefuse  = "refuse"  // a card the planner would not move, and why
	KindPush    = "push"    // a reminder is delivered to a person
	KindDue     = "due"     // moves and judgments left past the duty's bounds
)

// Move is one decision of one duty as data: a change to one card, a
// notification, an open judgment closing, a reminder, a count left due. Every
// field is text or a list of text, in a fixed form, so two moves are the same
// exactly when their String is. A field a kind does not use is empty.
type Move struct {
	// Duty is the duty that decided it (Duties).
	Duty string
	Kind string
	// Table is the logical table of a change to a card, empty for the rest,
	// and Card is that card. For a note (an update too), Card is the card it
	// is about where it names one; for a close, the note's id; for a
	// reminder, the person; for a refusal, the card refused.
	Table string
	Card  string
	// From and To are the place of the card before and after, row:col, for
	// a change of place; From alone for a change in place. Score is the
	// score the change gives it, where it gives one.
	From, To string
	Score    string
	// Set and Unset are the fields a change sets (field=value) and unsets,
	// in name order; a bump is one Set entry (field+1).
	Set, Unset []string
	// Type, Stream, Subjects, Decisions, Attrs and Words are a note's: its
	// type, its stream, the cards (or the stream, or the sprint) it is open
	// on in name order, the decisions offered in the order given, the rest
	// of its fields (attr=value, in name order), and its words. An update
	// is a note's move with the id of the judgment it rewrites in its
	// attrs (id=). A refusal and a reminder use Words for theirs, and a
	// change to a card uses it for the one line of what moved. A note's id
	// (the store gives each its own) and its time (the time Decide is
	// given) are not carried.
	Type      string
	Stream    string
	Subjects  []string
	Decisions []string
	Attrs     []string
	Words     string
}

// String is the move in one canonical line: the same moves give the same
// line, and two lines are equal exactly when the moves are.
func (m Move) String() string {
	var b strings.Builder
	b.WriteString(m.Duty + " " + m.Kind)
	field := func(name, v string) {
		if v != "" {
			fmt.Fprintf(&b, " %s=%q", name, v)
		}
	}
	list := func(name string, vs []string) {
		if len(vs) > 0 {
			fmt.Fprintf(&b, " %s=%q", name, vs)
		}
	}
	field("table", m.Table)
	field("card", m.Card)
	field("from", m.From)
	field("to", m.To)
	field("score", m.Score)
	list("set", m.Set)
	list("unset", m.Unset)
	field("type", m.Type)
	field("stream", m.Stream)
	list("subjects", m.Subjects)
	list("decisions", m.Decisions)
	list("attrs", m.Attrs)
	field("words", m.Words)
	return b.String()
}

// Shape is the moves without their words: a machine that says the same in
// other words decides the same. What it does to the tables and to the
// judgments (the cards, places, fields, types, subjects and decisions) stays.
func Shape(ms []Move) []Move {
	out := make([]Move, len(ms))
	for i, m := range ms {
		m.Words = ""
		out[i] = m
	}
	return out
}

// dutyRank is the place of a duty in the tick's order (dutyNames); one that is
// not a duty sorts after them all.
func dutyRank(name string) int {
	if i := slices.Index(dutyNames, name); i >= 0 {
		return i
	}
	return len(dutyNames)
}

// tableRank is the place of a table in the order a view shows them; a move of
// no table sorts after them.
func tableRank(name string) int {
	if i := slices.Index(sprint.ViewOrder, name); i >= 0 {
		return i
	}
	return len(sprint.ViewOrder)
}

// order is the canonical order of moves: by duty in the tick's order, then by
// table, card and kind, and by their line where those are equal, so equal
// moves are the only moves that compare equal.
func order(a, b Move) int {
	return cmp.Or(
		cmp.Compare(dutyRank(a.Duty), dutyRank(b.Duty)),
		cmp.Compare(tableRank(a.Table), tableRank(b.Table)),
		strings.Compare(a.Card, b.Card),
		strings.Compare(a.Kind, b.Kind),
		strings.Compare(a.String(), b.String()),
	)
}

// sortMoves puts the moves in canonical order, in place, and returns them.
func sortMoves(ms []Move) []Move {
	slices.SortStableFunc(ms, order)
	return ms
}

// Equal says the two decisions are the same set of moves, whatever order they
// were given in, and if they are not, names the first card that differs: the
// first in canonical order, by the duty that decided it, with the moves each
// of the two has for it.
func Equal(a, b []Move) (ok bool, diff string) {
	a, b = sortMoves(slices.Clone(a)), sortMoves(slices.Clone(b))
	for i, j := 0, 0; i < len(a) || j < len(b); {
		switch {
		case j >= len(b) || i < len(a) && order(a[i], b[j]) < 0:
			return false, differs(a[i], "the first", a, "the second", b)
		case i >= len(a) || order(a[i], b[j]) > 0:
			return false, differs(b[j], "the second", b, "the first", a)
		default:
			i, j = i+1, j+1
		}
	}
	return true, ""
}

// differs is the words of a difference at a move that one side has and the
// other does not: the first card, and what each side, by its name, does with
// it in that duty. A move that is of no card (a count left due, a notice of
// nothing in particular) is named by its duty and kind: the presence due move.
func differs(first Move, mine string, mineMoves []Move, other string, otherMoves []Move) string {
	subject := first.Card
	if subject == "" && len(first.Subjects) > 0 {
		subject = first.Subjects[0]
	}
	what := "card " + subject + " differs at " + first.Duty
	if subject == "" {
		what = "the " + first.Duty + " " + strings.TrimSpace(first.Kind+" "+first.Type) + " move differs"
	}
	inDuty := func(ms []Move) []string {
		var out []string
		for _, m := range ms {
			if m.Duty == first.Duty && m.Table == first.Table && m.Card == first.Card && m.Kind == first.Kind && m.Type == first.Type {
				out = append(out, m.String())
			}
		}
		return out
	}
	return fmt.Sprintf("%s: %s has %s; %s has %s", what, mine, listOrNone(inDuty(mineMoves)), other, listOrNone(inDuty(otherMoves)))
}

func listOrNone(lines []string) string {
	if len(lines) == 0 {
		return "no such move"
	}
	return strings.Join(lines, " and ")
}

// planMoves is the moves of a plan the duty decided, on the state it was
// planned from (the places the cards are moved from are read there): the rows
// it adds, each unit's changes, bumps, notes and closes, then the notes, closes
// and updates of the plan as a whole, and the cards it refused.
func planMoves(duty string, s *sprint.Snapshot, p sprint.Plan) []Move {
	var out []Move
	for _, r := range p.Rows {
		out = append(out, Move{Duty: duty, Kind: KindRow, Table: r.Table, Card: r.Row})
	}
	for _, u := range p.Units {
		first := len(out)
		for _, c := range u.Changes {
			if m, ok := changeMove(duty, s, c); ok {
				out = append(out, m)
			}
		}
		for _, b := range u.Bumps {
			out = append(out, Move{Duty: duty, Kind: KindBump, Table: b.Table, Card: b.ID, Set: []string{b.Field + fmt.Sprintf("%+d", b.Delta)}})
		}
		if len(out) > first {
			out[first].Words = u.Moved
		}
		out = appendNotes(out, duty, u.Notes...)
		out = appendCloses(out, duty, u.Closes...)
	}
	for _, pw := range p.Props {
		out = append(out, Move{Duty: duty, Kind: KindProp, Table: pw.Table, Card: pw.Name, Set: []string{pw.Name + "=" + pw.Value}})
	}
	out = appendNotes(out, duty, p.Notes...)
	out = appendCloses(out, duty, p.Closes...)
	for _, n := range p.Updates {
		m := noteMove(duty, n)
		m.Kind = KindUpdate
		m.Attrs = append(m.Attrs, "id="+n.ID) // the judgment it rewrites; the card it is about stays in Card
		sort.Strings(m.Attrs)
		out = append(out, m)
	}
	for _, r := range p.Refused {
		out = append(out, Move{Duty: duty, Kind: KindRefuse, Card: r.Key, Words: r.Why})
	}
	return out
}

// changeMove is the move an entry of a plan makes to a card; false for an
// entry that changes nothing (a guard).
func changeMove(duty string, s *sprint.Snapshot, ch sprint.Change) (Move, bool) {
	e := ch.Entry
	m := Move{Duty: duty, Table: ch.Table, Card: e.ID, From: placeAt(s, ch.Table, e.ID), Set: fieldList(e.Set), Unset: slices.Sorted(slices.Values(e.Unset))}
	switch {
	case e.Create != nil:
		m.Kind, m.From, m.To, m.Score = KindCreate, "", place(e.Create.Row, e.Create.Col), scoreText(e.Create.Score)
	case e.Remove:
		m.Kind = KindRemove
	case e.Move != nil:
		m.Kind, m.To = KindMove, place(e.Move.Row, e.Move.Col)
		if e.Move.Score != nil {
			m.Score = scoreText(*e.Move.Score)
		}
		if m.To == m.From {
			m.Kind, m.To = KindSet, ""
		}
	case len(e.Set)+len(e.Unset) > 0:
		m.Kind = KindSet
	default:
		return Move{}, false
	}
	return m, true
}

// placeAt is where a card is in the table, row:col, "" when it has no place.
func placeAt(s *sprint.Snapshot, table, id string) string {
	c := s.T(table).Placed(id)
	if c == nil {
		return ""
	}
	return place(c.Row, c.Col)
}

func place(row, col string) string { return row + ":" + col }

func scoreText(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// fieldList is the fields set as field=value, in field name order.
func fieldList(set map[string]string) []string {
	var out []string
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func appendNotes(out []Move, duty string, ns ...sprint.Note) []Move {
	for _, n := range ns {
		out = append(out, noteMove(duty, n))
	}
	return out
}

func appendCloses(out []Move, duty string, os ...sprint.Open) []Move {
	for _, o := range os {
		out = append(out, Move{Duty: duty, Kind: KindClose, Type: o.Note.Type, Stream: o.Note.Stream, Card: o.Note.ID, Subjects: []string{o.Subject()}})
	}
	return out
}

// noteKinds is the kind of move each kind of notification is.
var noteKinds = map[string]string{
	sprint.Judgment:     KindOpen,
	sprint.Happened:     KindNotice,
	sprint.Decided:      KindDecided,
	sprint.Acknowledged: KindHold,
}

// noteMove is the move a notification is.
func noteMove(duty string, n sprint.Note) Move {
	kind, ok := noteKinds[n.Kind]
	if !ok {
		kind = n.Kind
	}
	return Move{Duty: duty, Kind: kind, Type: n.Type, Stream: n.Stream, Card: n.Card, Words: n.What,
		Subjects: slices.Sorted(slices.Values(n.Subjects())), Decisions: slices.Clone(n.Decisions), Attrs: noteAttrs(n)}
}

// noteAttrs is the fields of a note that are not its type, stream, card,
// subjects, decisions and words, as attr=value in name order; a field that is
// zero is left out.
func noteAttrs(n sprint.Note) []string {
	var out []string
	add := func(name, v string) {
		if v != "" {
			out = append(out, name+"="+v)
		}
	}
	num := func(name string, v int) {
		if v != 0 {
			add(name, strconv.Itoa(v))
		}
	}
	flag := func(name string, v bool) {
		if v {
			add(name, "yes")
		}
	}
	add("answers", n.Answers)
	num("attempt", n.Attempt)
	num("before", n.Before)
	num("count", n.Count)
	flag("marked", n.Marked)
	add("needs", strings.Join(slices.Sorted(slices.Values(n.Needs)), ","))
	add("other", n.Other)
	add("other_stream", n.OtherStream)
	if !n.Review.IsZero() {
		add("review", n.Review.UTC().Format(time.RFC3339))
	}
	if !n.ReviewSet.IsZero() {
		add("review_set", n.ReviewSet.UTC().Format(time.RFC3339))
	}
	flag("sprint_level", n.SprintLevel)
	flag("stream_level", n.StreamLevel)
	add("suspects", strings.Join(slices.Sorted(slices.Values(n.Suspects)), ","))
	add("who", n.Who)
	sort.Strings(out)
	return out
}

// dueMove is the count of moves and judgments a duty left past its bounds:
// the next ticks make them.
func dueMove(duty string, due int) Move {
	return Move{Duty: duty, Kind: KindDue, Attrs: []string{"due=" + strconv.Itoa(due)}}
}

// PlanMoves is the moves of the plan p that the duty planned on the state s: the
// plan as data, in canonical order, converted as it is given. Decide's moves
// are those of plans held to what the store applies (sprint.Applied), so a
// machine that plans with the same planners as today's tick applies that to
// its plans first, and then converts them with this to compare them with
// Decide's.
func PlanMoves(duty string, s *sprint.Snapshot, p sprint.Plan) []Move {
	return sortMoves(planMoves(duty, s, p))
}
