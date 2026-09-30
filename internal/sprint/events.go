package sprint

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Events, the third layer of the event-driven tick (the upper design, version
// 2, section 1.1): the log's lines as the tick reads them. An event is a
// line cut down to what the rules read, with its sequence number taken from
// the stream id and never from the line's words. Nothing here reads a store
// or a clock: a line goes in, an Event comes out, and the same line always
// gives the same Event.
//
// The line is the present Line (log.go), whole and by its own names; a field
// the layer below adds later (its sequence number, its time in milliseconds)
// is ignored here, since the stream id says which line it is.

// EventFields are the fields of a line's Set that an event keeps: the ones a
// rule reads (result on a card entering review, status and held on a member's
// control card, kind on a card that is a sentinel). Every other field the
// line set (a score, a stamp, a head) stays in the log.
var EventFields = []string{"result", "status", "held", "kind"}

// verbUpdated is the verb of a note line that rewrites an open judgment in
// place: it is neither the opening nor the closing of one.
const verbUpdated = "updated"

// Event is a line of the log as the tick reads it.
type Event struct {
	// Seq is the line's place in the log: the number in its stream id.
	Seq uint64
	// Kind is the line's kind: move for a card's change, and for a note
	// judgment, happened, decided or acknowledged.
	Kind string
	// Table is the logical table of a move line (Work, Readers, Merge,
	// Fleet); Cards are the cards the line names (Line.Names), and Stream the
	// stream: the line's, or for a work line the row its card is placed in.
	Table  string
	Cards  []string
	Stream string
	// Primary is the primary of the one card a line names: for a work line
	// the card itself.
	Primary string
	// From and To are the place before and after as member:column, "" for a
	// card created (From) or taken off the table (To); Removed says the
	// latter.
	From, To string
	Removed  bool
	// Verb and Actor are the step and who took it.
	Verb, Actor string
	// Set is the fields the line set that a rule reads (EventFields).
	Set map[string]string
	// NoteType and Subjects are a note line's type and the subjects its
	// judgment is open on (Note.Subjects); Opens says the line opens a
	// judgment, Closes that it closes one (a decided or an acknowledged line).
	NoteType string
	Subjects []string
	Opens    bool
	Closes   bool
}

// ParseEvent builds an event from a line of the log: its stream id, which is
// <seq>-0 (the seq is the whole of it, the layer below writes no counter),
// and the line's JSON. A line it cannot read is an error that still names the
// seq when the id was good, so a caller that must move past it can.
func ParseEvent(id string, line []byte) (Event, error) {
	seq, err := parseSeq(id)
	if err != nil {
		return Event{}, err
	}
	var l Line
	if err := json.Unmarshal(line, &l); err != nil {
		return Event{Seq: seq}, fmt.Errorf("line %d is not a line of the log: %w", seq, err)
	}
	if l.Kind == "" {
		return Event{Seq: seq}, fmt.Errorf("line %d is not a line of the log: it has no kind", seq)
	}
	return EventOf(seq, l), nil
}

// parseSeq is the seq of a stream id <seq>-0: a decimal from 1.
func parseSeq(id string) (uint64, error) {
	num, ok := strings.CutSuffix(id, "-0")
	if !ok {
		return 0, fmt.Errorf("stream id %q is not <seq>-0", id)
	}
	seq, err := strconv.ParseUint(num, 10, 64)
	if err != nil || seq == 0 || num != strconv.FormatUint(seq, 10) {
		return 0, fmt.Errorf("stream id %q is not <seq>-0 with a seq from 1", id)
	}
	return seq, nil
}

// EventOf is the event of the line at seq.
func EventOf(seq uint64, l Line) Event {
	e := Event{Seq: seq, Kind: l.Kind, Table: l.Table, Stream: l.Stream, Primary: l.Primary,
		From: l.From, To: l.To, Removed: l.Removed, Verb: l.Verb, Actor: l.Actor}
	for _, c := range l.Names() {
		if c != "" {
			e.Cards = append(e.Cards, c)
		}
	}
	for _, f := range EventFields {
		if v, ok := l.Set[f]; ok {
			if e.Set == nil {
				e.Set = map[string]string{}
			}
			e.Set[f] = v
		}
	}
	if e.Table == Work {
		// A work card's row is its stream, as the step that wrote the line
		// took it (a card taken off the table has no place after).
		if e.Stream == "" {
			e.Stream = e.placeRow()
		}
		if e.Primary == "" && len(e.Cards) == 1 {
			e.Primary = e.Cards[0]
		}
	}
	if n := l.Note; n != nil {
		e.NoteType = n.Type
		e.Subjects = append([]string(nil), n.Subjects()...)
		e.Opens = l.Kind == Judgment && l.Verb != verbUpdated
		e.Closes = l.Kind == Decided || l.Kind == Acknowledged
		if e.Stream == "" {
			e.Stream = n.Stream
		}
	}
	return e
}

// placeRow is the row of the place the line puts the card at, or when it
// takes it off the table, the row it left.
func (e Event) placeRow() string {
	for _, p := range []string{e.To, e.From} {
		if row, _, ok := strings.Cut(p, ":"); ok && row != "" {
			return row
		}
	}
	return ""
}

// col is the column of a place, "" when there is none.
func col(place string) string {
	_, c, _ := strings.Cut(place, ":")
	return c
}

// enters says the line puts cards in the column of a table: they are there
// after it and were not in that cell before.
func (e Event) enters(table, column string) bool {
	return e.Kind == LineMove && e.Table == table && !e.Removed && col(e.To) == column && e.To != e.From
}

// leaves says the line takes cards out of a cell in one of the columns of a
// table: they were there before it and are not in that cell after.
func (e Event) leaves(table string, columns ...string) bool {
	if e.Kind != LineMove || e.Table != table || e.From == e.To {
		return false
	}
	for _, c := range columns {
		if col(e.From) == c {
			return true
		}
	}
	return false
}

// sentinel says the line itself says its card is a sentinel: it set the kind.
func (e Event) sentinel() bool { return e.Set["kind"] == Sentinel }

// openAfter says the line leaves a card of the work table in one of the open
// states.
func (e Event) openAfter() bool {
	return e.Kind == LineMove && e.Table == Work && !e.Removed && IsOpen(col(e.To))
}

// control says the line is a change of a member's control card: a card placed
// in the control column of the fleet table.
func (e Event) control() bool {
	return e.Kind == LineMove && e.Table == Fleet && col(e.To) == Ctl
}
