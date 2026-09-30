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
// Two lines give events. ParseEvent reads the line of Layer 2's contract (the
// log contract, sections 1 and 4), by that contract's own words: the semantic
// ones the lines read returns (kind, at_ms, table, from, to, ids, about, set,
// meta) and the ones of the stored body (k, ms, tbl, and shared beside set).
// EventOf maps the present Line (log.go), which the sprint writes today.
// Fields of a line that no rule reads (its time, its scores and revisions,
// the rows a rows line adds) are not decoded: the stream id says which line it
// is. The one such field that is read is a semantic line's own seq, and only to
// refuse a line whose seq is not the stream id's.

// EventFields are the fields of a line's Set that an event keeps: the ones a
// rule reads (result on a card entering review, status and held on a member's
// control card, kind on a card that is a sentinel). Every other field the
// line set (a score, a stamp, a head) stays in the log.
var EventFields = []string{"result", "status", "held", "kind"}

// verbUpdated is the verb of a note line that rewrites an open judgment in
// place: it is neither the opening nor the closing of one.
const verbUpdated = "updated"

// The kinds of a line of Layer 2's contract that are not a card's move: a
// create and a remove are moves of the sprint's own (LineMove) for the rules,
// which read the place before and after, and Removed.
const (
	kindCreate  = "create"
	kindRemove  = "remove"
	kindRows    = "rows"
	kindAdvance = "advance"
	kindNote    = "note"
)

// Event is a line of the log as the tick reads it.
type Event struct {
	// Seq is the line's place in the log: the number in its stream id.
	Seq uint64
	// Kind is the line's kind: move for a card's change (a create, a move and
	// a remove), rows and advance for the table's own, and for a note
	// judgment, happened, decided or acknowledged.
	Kind string
	// Table is the table of a move line (Work, Readers, Merge, Fleet); Cards
	// are the cards the line names (Line.Names, or the contract's ids), and
	// Stream the stream: the line's, or for a work line the row its card is
	// placed in.
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
	// Count is how many subjects the note is on. Subjects lists them all
	// unless the writer cut the list (Note.Bound keeps MaxListed of them and
	// the total in Count): a Count larger than the subjects listed says the
	// list was cut. A note of a stream or of the sprint has one subject.
	Count int
}

// contractLine is a line of Layer 2's contract, decoded as far as the rules
// read it. The stored body and the semantic line say the same in different
// words (k, ms, tbl or kind, at_ms, table), and either is read; a field that
// the wrong words leave empty is empty. From and To are held raw, since on an
// advance they are epochs and on a member line cell references.
type contractLine struct {
	Seq    json.RawMessage            `json:"seq"`
	Kind   string                     `json:"kind"`
	K      string                     `json:"k"`
	Table  string                     `json:"table"`
	Tbl    string                     `json:"tbl"`
	From   json.RawMessage            `json:"from"`
	To     json.RawMessage            `json:"to"`
	IDs    []string                   `json:"ids"`
	About  []string                   `json:"about"`
	Shared map[string]string          `json:"shared"`
	Set    []map[string]string        `json:"set"`
	Meta   map[string]json.RawMessage `json:"meta"`
}

// contractKinds are the words of the kind of a contract line, semantic and
// stored, by the kind they say.
var contractKinds = map[string]string{
	"create": kindCreate, "c": kindCreate,
	"move": LineMove, "m": LineMove,
	"remove": kindRemove, "x": kindRemove,
	"rows": kindRows, "w": kindRows,
	"advance": kindAdvance, "a": kindAdvance,
	"note": kindNote, "n": kindNote,
}

// ParseEvent builds an event from a line of the log as Layer 2 writes it: its
// stream id, which is <seq>-0 (the seq is the whole of it, the layer below
// writes no counter), and the line's JSON, in the contract's words. A line it
// cannot read is an error that still names the seq when the id was good, so a
// caller that must move past it can. A line that names cards never reads as
// naming none: one that says it is a card's change and gives no ids is
// refused, and so is one on a table the rules do not know, which would queue
// no key at all. A line that carries a seq of its own (the semantic line does)
// must carry the stream id's: the two say which line it is, and a mismatch is
// a wrong pairing of id and line.
func ParseEvent(id string, line []byte) (Event, error) {
	seq, err := parseSeq(id)
	if err != nil {
		return Event{}, err
	}
	var c contractLine
	if err := json.Unmarshal(line, &c); err != nil {
		return Event{Seq: seq}, fmt.Errorf("line %d is not a line of the log: %w", seq, err)
	}
	if err := c.agreesWith(seq); err != nil {
		return Event{Seq: seq}, fmt.Errorf("line %d is not a line of the log: %w", seq, err)
	}
	e, err := eventOfContract(seq, c)
	if err != nil {
		return Event{Seq: seq}, fmt.Errorf("line %d is not a line of the log: %w", seq, err)
	}
	return e, nil
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

// agreesWith is nil when the line carries no seq of its own, or carries the
// seq of its stream id, as a JSON number or a string of the same decimal: the
// semantic line's seq is copied from the stream id.
func (c contractLine) agreesWith(seq uint64) error {
	if c.Seq == nil {
		return nil
	}
	word := string(c.Seq)
	var s string
	if json.Unmarshal(c.Seq, &s) == nil {
		word = s
	}
	if want := strconv.FormatUint(seq, 10); word != want {
		return fmt.Errorf("its own seq is %s and its stream id says %s", c.Seq, want)
	}
	return nil
}

// isTable says a table name is one of the four the rules know, by its logical
// name (work, readers, merge, fleet): a deployment's prefixed name is not.
func isTable(name string) bool {
	switch name {
	case Work, Readers, Merge, Fleet:
		return true
	}
	return false
}

// eventOfContract is the event of a decoded contract line.
func eventOfContract(seq uint64, c contractLine) (Event, error) {
	kind, err := c.kind()
	if err != nil {
		return Event{}, err
	}
	names := []string{"verb", "actor", "stream"}
	if kind == kindNote {
		names = append(names, "kind", "type")
	}
	meta, err := c.metaStrings(names...)
	if err != nil {
		return Event{}, err
	}
	e := Event{Seq: seq, Kind: kind, Table: c.Table, Stream: meta["stream"], Verb: meta["verb"], Actor: meta["actor"]}
	if e.Table == "" {
		e.Table = c.Tbl
	}
	switch kind {
	case kindCreate, LineMove, kindRemove:
		// A card's change on a table the rules do not know queues no key, not
		// even the stream's: it is refused, so that nothing is lost silently.
		if !isTable(e.Table) {
			return Event{}, fmt.Errorf("its table %q is none of %s, %s, %s or %s", e.Table, Work, Readers, Merge, Fleet)
		}
		e.Kind = LineMove
		if err := c.member(kind, &e); err != nil {
			return Event{}, err
		}
		e.placeDefaults()
	case kindNote:
		// The contract carries a note's own words in meta: its judgment kind
		// and type. A note that names none is a plain note, which neither
		// opens nor closes a judgment.
		if k := meta["kind"]; k != "" {
			switch k {
			case Judgment, Happened, Decided, Acknowledged:
				e.Kind = k
			default:
				return Event{}, fmt.Errorf("its meta says the note is a %q, not a judgment, happened, decided or acknowledged", k)
			}
		}
		e.NoteType = meta["type"]
		e.Subjects = append([]string(nil), c.About...)
		e.Cards = append([]string(nil), c.About...)
		e.Count = len(e.Subjects)
		e.Opens, e.Closes = noteOpens(e.Kind, e.Verb), noteCloses(e.Kind)
	}
	return e, nil
}

// kind is the kind a contract line says, from its semantic word or its stored
// tag; a line that says both says one.
func (c contractLine) kind() (string, error) {
	var kinds []string
	for _, w := range []string{c.Kind, c.K} {
		if w == "" {
			continue
		}
		k, ok := contractKinds[w]
		if !ok {
			return "", fmt.Errorf("it has the kind %q, which the log contract does not name", w)
		}
		kinds = append(kinds, k)
	}
	switch {
	case len(kinds) == 0:
		return "", fmt.Errorf("it has no kind")
	case len(kinds) == 2 && kinds[0] != kinds[1]:
		return "", fmt.Errorf("it says it is a %s and a %s", c.Kind, c.K)
	}
	return kinds[0], nil
}

// metaStrings is the named words of the caller's meta object, each a string: a
// value of another type is not read as empty but refused.
func (c contractLine) metaStrings(names ...string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range names {
		raw, ok := c.Meta[name]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("its meta has %s that is not a string", name)
		}
		out[name] = s
	}
	return out, nil
}

// cell is the string of a from or a to: a cell reference on a card's line.
func cell(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("a place %s is not a string", raw)
	}
	return s, nil
}

// member reads what a card's line says into the event: the cards it names
// (ids), the primary of the one card, the place before and after, and the
// fields the rules read.
func (c contractLine) member(kind string, e *Event) error {
	if len(c.IDs) == 0 {
		return fmt.Errorf("it is a card's change and names no ids")
	}
	for _, id := range c.IDs {
		if id == "" {
			return fmt.Errorf("it names an empty id")
		}
	}
	if len(c.About) != 0 && len(c.About) != len(c.IDs) {
		return fmt.Errorf("it names %d ids and %d about", len(c.IDs), len(c.About))
	}
	if len(c.Set) != 0 && len(c.Set) != len(c.IDs) {
		return fmt.Errorf("it names %d ids and %d sets", len(c.IDs), len(c.Set))
	}
	from, err := cell(c.From)
	if err != nil {
		return err
	}
	to, err := cell(c.To)
	if err != nil {
		return err
	}
	switch kind {
	case kindCreate:
		if to == "" {
			return fmt.Errorf("a create has no place to")
		}
		e.To = to
	case LineMove:
		if from == "" {
			return fmt.Errorf("a move has no place from")
		}
		// A move that gives no place to stays where it was.
		if to == "" {
			to = from
		}
		e.From, e.To = from, to
	case kindRemove:
		if from == "" {
			return fmt.Errorf("a remove has no place from")
		}
		e.From, e.Removed = from, true
	}
	e.Cards = append([]string(nil), c.IDs...)
	if len(c.IDs) == 1 && len(c.About) == 1 {
		e.Primary = c.About[0]
	}
	for _, f := range EventFields {
		v, ok, err := c.field(f)
		if err != nil {
			return err
		}
		if ok {
			if e.Set == nil {
				e.Set = map[string]string{}
			}
			e.Set[f] = v
		}
	}
	return nil
}

// field is what a line sets a field to: a value it shares across its ids, or
// one on each id's own set. An event holds one value of a field for the whole
// line, so a line that sets it on some of its ids only, or to different values,
// is refused rather than read as setting one. The contract's per-id set allows
// such a line: the refusal is this layer's choice, not the contract's.
func (c contractLine) field(name string) (string, bool, error) {
	var val string
	found := 0
	for i := range c.IDs {
		v, ok := c.Shared[name]
		if i < len(c.Set) {
			if sv, sok := c.Set[i][name]; sok {
				v, ok = sv, true
			}
		}
		if !ok {
			continue
		}
		if found > 0 && v != val {
			return "", false, fmt.Errorf("it sets %s to different values on its ids", name)
		}
		val = v
		found++
	}
	if found != 0 && found != len(c.IDs) {
		return "", false, fmt.Errorf("it sets %s on some of its ids only", name)
	}
	return val, found > 0, nil
}

// EventOf is the event of the present Line at seq.
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
	e.placeDefaults()
	if n := l.Note; n != nil {
		e.NoteType = n.Type
		e.Subjects = append([]string(nil), n.Subjects()...)
		e.Count = n.Count
		if n.StreamLevel || n.SprintLevel {
			// The one subject of a stream or of the sprint is never cut.
			e.Count = len(e.Subjects)
		}
		e.Opens, e.Closes = noteOpens(l.Kind, l.Verb), noteCloses(l.Kind)
		if e.Stream == "" {
			e.Stream = n.Stream
		}
	}
	return e
}

// noteOpens says a note line of the kind opens a judgment: a judgment does,
// unless its verb says it only rewrites one that is open.
func noteOpens(kind, verb string) bool { return kind == Judgment && verb != verbUpdated }

// noteCloses says a note line of the kind closes a judgment.
func noteCloses(kind string) bool { return kind == Decided || kind == Acknowledged }

// placeDefaults fills what a work line leaves to be read from its place: a
// work card's row is its stream, as the step that wrote the line took it (a
// card taken off the table has no place after), and a line of one card is of
// that card's own primary.
func (e *Event) placeDefaults() {
	if e.Table != Work {
		return
	}
	if e.Stream == "" {
		e.Stream = e.placeRow()
	}
	if e.Primary == "" && len(e.Cards) == 1 {
		e.Primary = e.Cards[0]
	}
}

// placeRow is the row of the place the line puts the card at, or when it
// takes it off the table, the row it left.
func (e Event) placeRow() string {
	for _, p := range []string{e.To, e.From} {
		if row, _, ok := cutCell(p); ok && row != "" {
			return row
		}
	}
	return ""
}

// cutCell splits a cell reference <row>:<col> at its last colon: the row may
// hold colons, the column may not (the table contract's cell reference).
func cutCell(place string) (row, column string, ok bool) {
	i := strings.LastIndex(place, ":")
	if i < 0 {
		return place, "", false
	}
	return place[:i], place[i+1:], true
}

// placeCol is the column of a place, "" when there is none.
func placeCol(place string) string {
	_, c, _ := cutCell(place)
	return c
}

// enters says the line puts cards in the column of a table: they are there
// after it and were not in that cell before.
func (e Event) enters(table, column string) bool {
	return e.Kind == LineMove && e.Table == table && !e.Removed && placeCol(e.To) == column && e.To != e.From
}

// leaves says the line takes cards out of a cell in one of the columns of a
// table: they were there before it and are not in that cell after.
func (e Event) leaves(table string, columns ...string) bool {
	if e.Kind != LineMove || e.Table != table || e.From == e.To {
		return false
	}
	for _, c := range columns {
		if placeCol(e.From) == c {
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
	return e.Kind == LineMove && e.Table == Work && !e.Removed && IsOpen(placeCol(e.To))
}

// control says the line is a change of a member's control card: a card placed
// in the control column of the fleet table.
func (e Event) control() bool {
	return e.Kind == LineMove && e.Table == Fleet && placeCol(e.To) == Ctl
}
