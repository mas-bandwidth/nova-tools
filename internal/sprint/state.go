package sprint

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Card is one member of a table as observed: its place (empty when the record
// is kept but unplaced), score, member revision and fields.
type Card struct {
	ID       string
	Row, Col string
	Score    float64
	Rev      uint64
	Fields   map[string]string
}

// Placed says the card has a place in its table.
func (c *Card) Placed() bool { return c != nil && c.Col != "" }

// F is a field, "" when absent.
func (c *Card) F(name string) string {
	if c == nil {
		return ""
	}
	return c.Fields[name]
}

// Int is a counter field, 0 when absent or unreadable.
func (c *Card) Int(name string) int {
	n, _ := strconv.Atoi(c.F(name))
	return n
}

// Table is one table as observed: its revision, its rows in order, its text
// cells, and its cards (placed, and any unplaced records the step asked for).
type Table struct {
	Name     string // logical name
	Epoch    uint64
	Revision uint64
	Rows     []string
	Texts    map[string]map[string]string // row -> text column -> value
	Cards    map[string]*Card

	cells     map[[2]string][]*Card // built on first use; Put resets it
	byPrimary map[string][]*Card
	lines     map[string][]*Card // each row's cards not landed, in score order; built with cells

	// part says which cells a table loaded from a read plan holds whole (the
	// upper design, version 2.1, section 1.5.2). Nil on a table built whole,
	// every cell of which is loaded.
	part *loadedCells
}

// unloadedMessage is the refusal of a planner that read a cell its plan did
// not load (1.5.2): the cell's cards, or a count or a position the read did
// not ask for, so that a scan cannot come back unnoticed.
const unloadedMessage = "the planner read a cell its plan did not load"

// MaxUnloadedNoted is how many such reads a snapshot keeps to name (a planner
// that scans a table would otherwise write a line for every cell it meets).
const MaxUnloadedNoted = 64

// loadedCells is what a table loaded from a read plan knows.
type loadedCells struct {
	// whole are the cells whose every card is in the table.
	whole map[[2]string]bool
	// counts are the counts a read gave for cells it did not load whole.
	counts map[[2]string]int
	// rows says the table's rows were read (a stream, member or reader
	// query), so that a read of a column knows which cells it means.
	rows bool
	// log is where a read of anything else is put; the snapshot's tables share
	// one.
	log *unloadedLog
}

// unloadedLog is what a snapshot does with a read of what its plan did not
// load: in a test build it panics, at the read, so the test that meets it
// fails where it happened; in a release build it keeps the read (up to
// MaxUnloadedNoted of them, then their count) and the planner's caller
// refuses the plan (Snapshot.Unloaded).
type unloadedLog struct {
	strict bool

	mu      sync.Mutex
	noted   []string
	dropped int
}

// note records a read of what was not loaded.
func (l *unloadedLog) note(msg string) {
	if l.strict {
		panic(msg)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.noted) >= MaxUnloadedNoted {
		l.dropped++
		return
	}
	l.noted = append(l.noted, msg)
}

// list is what was noted, with a last line counting what was not kept.
func (l *unloadedLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := append([]string(nil), l.noted...)
	if l.dropped > 0 {
		out = append(out, unloadedMessage+": and "+itoa(l.dropped)+" more")
	}
	return out
}

// Loaded says the cell's cards are all in the table: true for every cell of a
// table built whole, and for a table loaded from a read plan only the cells
// its answer gave whole (Table.Cell and Table.Column on any other cell panic
// in a test build and are refused in a release build, 1.5.2).
func (t *Table) Loaded(row, col string) bool {
	if t == nil {
		return false
	}
	return t.part == nil || t.part.whole[[2]string{row, col}]
}

// need is the guard of every read of a cell's cards: true when the cell is
// loaded, and otherwise the read is put in the snapshot's log.
func (t *Table) need(row, col string) bool {
	if t.Loaded(row, col) {
		return true
	}
	t.part.log.note(unloadedMessage + ": " + t.Name + " " + row + ":" + col)
	return false
}

// Put adds or replaces a card.
func (t *Table) Put(c *Card) {
	if c.Fields == nil {
		c.Fields = map[string]string{}
	}
	t.Cards[c.ID] = c
	t.cells, t.byPrimary = nil, nil
}

func (t *Table) index() {
	if t.cells != nil {
		return
	}
	t.cells, t.byPrimary, t.lines = map[[2]string][]*Card{}, map[string][]*Card{}, nil
	for _, c := range t.Cards {
		if !c.Placed() {
			continue
		}
		k := [2]string{c.Row, c.Col}
		t.cells[k] = append(t.cells[k], c)
		if p := c.F("primary"); p != "" {
			t.byPrimary[p] = append(t.byPrimary[p], c)
		}
	}
	for _, cs := range t.cells {
		SortCards(cs)
	}
	for _, cs := range t.byPrimary {
		SortCards(cs)
	}
}

// NewTable is an empty observed table.
func NewTable(name string) *Table {
	return &Table{Name: name, Texts: map[string]map[string]string{}, Cards: map[string]*Card{}}
}

// HasRow says the table declares the row.
func (t *Table) HasRow(row string) bool {
	for _, r := range t.Rows {
		if r == row {
			return true
		}
	}
	return false
}

// Card is the card with the id, nil when the table has none.
func (t *Table) Card(id string) *Card {
	if t == nil {
		return nil
	}
	return t.Cards[id]
}

// Placed is the card with the id if it has a place.
func (t *Table) Placed(id string) *Card {
	c := t.Card(id)
	if !c.Placed() {
		return nil
	}
	return c
}

// Cell is the cards placed at row and column, in score order (then id). On a
// table loaded from a read plan, a cell that was not loaded whole is refused
// (see Loaded).
func (t *Table) Cell(row, col string) []*Card {
	if !t.need(row, col) {
		return nil
	}
	t.index()
	return t.cells[[2]string{row, col}]
}

// Column is the cards placed in the column on any row, in score order. On a
// table loaded from a read plan, a column with a cell that was not loaded
// whole, or a table whose rows were not read, is refused (see Loaded): the
// result is empty, never the cards that happen to be known.
func (t *Table) Column(cols ...string) []*Card {
	if t.part != nil {
		if !t.part.rows {
			t.part.log.note(unloadedMessage + ": " + t.Name + " rows")
			return nil
		}
		whole := true
		for _, r := range t.Rows {
			for _, col := range cols {
				whole = t.need(r, col) && whole
			}
		}
		if !whole {
			return nil
		}
	}
	t.index()
	var out []*Card
	for _, r := range t.Rows {
		for _, col := range cols {
			out = append(out, t.cells[[2]string{r, col}]...)
		}
	}
	SortCards(out)
	return out
}

// Count is the number of cards at row and column. On a table loaded from a
// read plan it is the count the read gave, or the cards of a cell loaded
// whole; a cell with neither is refused (see Loaded) and counts 0.
func (t *Table) Count(row, col string) int {
	k := [2]string{row, col}
	if t.part != nil && !t.part.whole[k] {
		if n, ok := t.part.counts[k]; ok {
			return n
		}
		t.need(row, col)
		return 0
	}
	t.index()
	return len(t.cells[k])
}

// Of is the placed cards whose primary field names p, in score order.
func (t *Table) Of(p string) []*Card {
	t.index()
	return t.byPrimary[p]
}

// SortCards orders cards by score, then by id: work order.
func SortCards(cs []*Card) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Score != cs[j].Score {
			return cs[i].Score < cs[j].Score
		}
		return cs[i].ID < cs[j].ID
	})
}

// Snapshot is the observed pre-state of a step: the clock reading the step
// runs at (given, never read), the tables it loaded, and the open judgment
// notifications. A table the step did not load is nil.
type Snapshot struct {
	Now     time.Time
	Epoch   uint64    // the sprint's epoch the tables were read at
	Cleared time.Time // when that epoch began (the last clear); zero for the first
	// Coordinator is the sprint's coordinator: judgments are theirs to
	// answer. Actor is who runs the step (a request's Who, when it names
	// none).
	Coordinator, Actor          string
	Work, Readers, Merge, Fleet *Table
	Open                        []Open
	// Acked is the tick's conditions the coordinator acknowledged, held
	// while they hold (Acknowledged).
	Acked []Open
	// Partial is what a snapshot loaded from a read plan was loaded from
	// (LoadPartial, 1.5.2); nil for a snapshot built whole.
	Partial *Partial
}

// T is the loaded table by logical name.
func (s *Snapshot) T(name string) *Table {
	switch name {
	case Work:
		return s.Work
	case Readers:
		return s.Readers
	case Merge:
		return s.Merge
	case Fleet:
		return s.Fleet
	}
	return nil
}

// Primary is the primary's record in the work table (placed or kept).
func (s *Snapshot) Primary(id string) *Card { return s.Work.Card(id) }

// StateOf is the primary's state, "" when it is not on the table.
func (s *Snapshot) StateOf(id string) State {
	c := s.Work.Placed(id)
	if c == nil {
		return ""
	}
	return c.Col
}

// StreamCtl is a stream's control card in the merge table.
func (s *Snapshot) StreamCtl(stream string) *Card { return s.Merge.Placed(CtlID(stream)) }

// MemberCtl is a fleet member's control card.
func (s *Snapshot) MemberCtl(member string) *Card { return s.Fleet.Placed(CtlID(member)) }

// UpMembers is the fleet members whose status is up, in row order.
func (s *Snapshot) UpMembers() []string {
	var out []string
	for _, m := range s.Fleet.Rows {
		if s.MemberCtl(m).F("status") == Up {
			out = append(out, m)
		}
	}
	return out
}

// Streams is the work table's streams, in row order.
func (s *Snapshot) Streams() []string { return s.Work.Rows }

// Split is a comma list, without empty items.
func Split(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }
