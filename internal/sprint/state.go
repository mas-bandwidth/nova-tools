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

	// load says which fields a card loaded from a read plan holds (its
	// projection): nil on a card built whole, all of whose fields are held.
	load *cardLoad
}

// cardLoad is which fields of a record a read plan loaded: every one (a record
// asked for whole), or the fields the queries that read it named, together.
type cardLoad struct {
	whole  bool
	fields map[string]bool
	log    *unloadedLog
}

// newCardLoad is what a query that named these fields loads of the records it
// returns. Layer 1 draws the line between no list and an empty one (the errata
// to version 2.1, E6; tset.Mem's projectReadRecord): a nil list is every field
// (the whole record), and a list that is not nil but has no field is the summary
// of a record, its id, place, score and revision and no field of it.
func newCardLoad(fields []string, log *unloadedLog) *cardLoad {
	l := &cardLoad{whole: fields == nil, log: log}
	if !l.whole {
		l.fields = make(map[string]bool, len(fields))
		for _, f := range fields {
			l.fields[f] = true
		}
	}
	return l
}

// with is what a record holds when this load and o both read it: every field
// of either.
func (l *cardLoad) with(o *cardLoad) *cardLoad {
	switch {
	case l == o:
		return l
	case l == nil || o == nil:
		return nil // a card built whole holds every field
	case l.whole:
		return l
	case o.whole:
		return o
	}
	m := &cardLoad{fields: make(map[string]bool, len(l.fields)+len(o.fields)), log: l.log}
	for f := range l.fields {
		m.fields[f] = true
	}
	for f := range o.fields {
		m.fields[f] = true
	}
	return m
}

// Placed says the card has a place in its table.
func (c *Card) Placed() bool { return c != nil && c.Col != "" }

// PrimaryField is the field of a card that names its primary: the read cards,
// the merge card and the work cards of the fleet's table carry it, and Table.Of
// finds them by it.
const PrimaryField = "primary"

// field is the field's value and whether the card holds the field: true for a
// card built whole, for a card loaded from a read plan by a query that named no
// projection, and for a field that a query which read the card named. It is
// the one read of a field; F and the table's index of primaries both go through
// it, so that a field the plan did not load is never read as absent.
func (c *Card) field(name string) (value string, held bool) {
	if c.load != nil && !c.load.whole && !c.load.fields[name] {
		return "", false
	}
	return c.Fields[name], true
}

// F is a field, "" when absent. On a card loaded from a read plan, a field
// that no query that read the card named is refused (1.0: every rule read names
// its fields): the read panics in a test build, and in a release build is
// recorded (Snapshot.Unloaded) and gives "", which would otherwise read as
// absent. A field that was read and has no value is absent.
func (c *Card) F(name string) string {
	if c == nil {
		return ""
	}
	v, held := c.field(name)
	if !held {
		c.load.log.note(unloadedFieldMessage + ": " + c.ID + " " + name)
		return ""
	}
	return v
}

// Has says the card has the field, set to any value, an empty one too. It is
// read as F is: on a card loaded from a read plan, a field that no query that
// read the card named is refused (the read panics in a test build, and in a
// release build is recorded in Snapshot.Unloaded and says no), so that a field
// the plan did not load is never taken for one the card lacks.
func (c *Card) Has(name string) bool {
	if c == nil {
		return false
	}
	if _, held := c.field(name); !held {
		c.load.log.note(unloadedFieldMessage + ": " + c.ID + " " + name)
		return false
	}
	_, ok := c.Fields[name]
	return ok
}

// Int is a counter field, 0 when absent or unreadable.
func (c *Card) Int(name string) int {
	n, _ := strconv.Atoi(c.F(name))
	return n
}

// Table is one table as observed: its revision, its rows in order, its text
// cells, and its cards (placed, and any unplaced records the step asked for),
// which are read by Card, Cards and LoadedCards.
type Table struct {
	Name     string // logical name
	Epoch    uint64
	Revision uint64
	Texts    map[string]map[string]string // row -> text column -> value

	// cards are the table's cards by id, read by Card, Placed, Cards and
	// LoadedCards and set by Put: on a table loaded from a read plan the cards are
	// only some of the table's, and an exported field could not say so.
	cards map[string]*Card

	// rows are the table's rows in order, read by Rows and set by SetRows: on a
	// table loaded from a read plan the rows are read only when the plan asked
	// for them, and a field could not say so.
	rows []string

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

// unloadedFieldMessage is the same for a field of a record that the plan read
// without it.
const unloadedFieldMessage = "the planner read a field its plan did not load"

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
	// followed are the follows of a primary that a `related` query read into
	// this table, as {primary, follow} (followTables): Of answers for a primary
	// when every follow that reads one of its cards here was read, and for no
	// other.
	followed map[[2]string]bool
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
	t.cards[c.ID] = c
	t.cells, t.byPrimary = nil, nil
}

func (t *Table) index() {
	if t.cells != nil {
		return
	}
	t.cells, t.byPrimary, t.lines = map[[2]string][]*Card{}, map[string][]*Card{}, nil
	for _, c := range t.cards {
		if !c.Placed() {
			continue
		}
		k := [2]string{c.Row, c.Col}
		t.cells[k] = append(t.cells[k], c)
		// The index finds a card by its primary through the card's own read of the
		// field. A card that does not hold primary (a control card, read by a
		// listing with a projection that leaves it out) is no primary's, and is not
		// indexed; the cards a follow reads are checked to hold it (ReadPlan.Validate),
		// so that Of never answers from records that came without it.
		if p, held := c.field(PrimaryField); held && p != "" {
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
	return &Table{Name: name, Texts: map[string]map[string]string{}, cards: map[string]*Card{}}
}

// Rows are the table's rows in order, not to be changed. On a table loaded from
// a read plan, the rows are the ones a stream, member or reader query listed,
// and a table whose rows the plan did not read has none to give: the read is
// refused (see Loaded), and the result is empty, never the rows that happen to
// be known.
func (t *Table) Rows() []string {
	if t == nil || !t.needRows() {
		return nil
	}
	return t.rows
}

// SetRows sets the table's rows, in order: it builds a table, and loads one
// from a read that listed its rows.
func (t *Table) SetRows(rows []string) { t.rows = rows }

// needRows is the guard of every read of the table's rows: true when they are
// known (a table built whole, or one whose plan read them), and otherwise the
// read is put in the snapshot's log.
func (t *Table) needRows() bool {
	if t.part == nil || t.part.rows {
		return true
	}
	t.part.log.note(unloadedMessage + ": " + t.Name + " rows")
	return false
}

// HasRow says the table declares the row. On a table loaded from a read plan
// whose rows were not read it is refused (see Rows) and says no.
func (t *Table) HasRow(row string) bool {
	if !t.needRows() {
		return false
	}
	for _, r := range t.rows {
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
	return t.cards[id]
}

// Cards is every card the table holds, placed or kept, in id order: a copy of
// the list, of the same cards. It is a scan of the whole table, so on a table
// loaded from a read plan it is answered only when the plan loaded the table
// whole: the work table, whose rows a stream query listed and every one of
// whose cells (a row's cell in each state) was read whole. Any other partial
// table is refused (see Loaded), and the result is empty, never the cards that
// happen to be known (a table's other columns are not named here, and the
// readers, merge and fleet tables hold hidden ones). A record kept with no
// place is held only when a query named its id, so even a whole table may hold
// fewer of those than the store does: no query lists them. A rule that means
// exactly the records the read loaded asks for them by name (LoadedCards).
func (t *Table) Cards() []*Card {
	if t == nil || !t.needAll() {
		return nil
	}
	return t.loadedCards()
}

// LoadedCards is the cards the table holds, in id order, whatever the read that
// loaded them read of the rest: a scan of the records that came, for a rule that
// asks for exactly that. It is never refused, and on a table loaded from a read
// plan it is not every card of the table; Cards is.
func (t *Table) LoadedCards() []*Card {
	if t == nil {
		return nil
	}
	return t.loadedCards()
}

func (t *Table) loadedCards() []*Card {
	out := make([]*Card, 0, len(t.cards))
	for _, c := range t.cards {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// needAll is the guard of a scan of every card of the table: true when the
// table was built whole, or is the work table loaded whole (its rows read and
// every cell of every row read whole); otherwise the scan is put in the
// snapshot's log.
func (t *Table) needAll() bool {
	if t.part == nil {
		return true
	}
	if t.part.rows && t.Name == Work {
		whole := true
		for _, row := range t.rows {
			for _, st := range States {
				whole = whole && t.part.whole[[2]string{row, string(st)}]
			}
		}
		if whole {
			return true
		}
	}
	t.part.log.note(unloadedMessage + ": " + t.Name + " cards")
	return false
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
		if !t.needRows() {
			return nil
		}
		whole := true
		for _, r := range t.rows {
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
	for _, r := range t.rows {
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

// Of is the placed cards whose primary field names p, in score order. On a
// table loaded from a read plan it answers only for a primary whose cards in
// this table a `related` query read with every follow that reaches them: rcards
// for the readers' read cards, merge for the merge card, and for the fleet's
// work cards both work (the live card) and withdrawn (the same card, when it is
// withdrawn), which 1.0 gives as separate follows of one card in disjoint
// states. It gives the cards those follows loaded; for any other primary, or
// one of which only some of the follows were read, it is refused (see Loaded),
// and the result is empty, never the cards that happen to be known.
func (t *Table) Of(p string) []*Card {
	if t.part != nil && !t.part.followedAll(t.Name, p) {
		t.part.log.note(unloadedMessage + ": " + t.Name + " cards of " + p)
		return nil
	}
	t.index()
	return t.byPrimary[p]
}

// followedAll says every follow that reads a card of primary p in the table was
// read, and that some follow reads them at all (no follow reads the work
// table's cards of a primary: Of is refused there).
func (l *loadedCells) followedAll(table, p string) bool {
	need := followsInto[table]
	if len(need) == 0 {
		return false
	}
	for _, f := range need {
		if !l.followed[[2]string{p, f}] {
			return false
		}
	}
	return true
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
	// Dropping maps a stream being dropped or removed to the op that drops it
	// ({p}dropping@e): what Printed and Answerable leave out the decisions
	// DROPPING refuses by (errata 3, H8). nil says no stream is dropping.
	Dropping map[string]string
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

// UpMembers is the fleet members whose status is up, in row order. On a
// snapshot loaded from a read plan that did not read the fleet's rows it is
// refused (see Table.Rows).
func (s *Snapshot) UpMembers() []string {
	var out []string
	for _, m := range s.Fleet.Rows() {
		if s.MemberCtl(m).F("status") == Up {
			out = append(out, m)
		}
	}
	return out
}

// Streams is the work table's streams, in row order. On a snapshot loaded from
// a read plan that did not read them it is refused (see Table.Rows).
func (s *Snapshot) Streams() []string { return s.Work.Rows() }

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
