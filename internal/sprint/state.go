package sprint

import (
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
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

// PrimaryField is the field of a card that names its primary: the read cards,
// the merge card and the work cards of the fleet's table carry it, and Table.Of
// finds them by it.
const PrimaryField = "primary"

// F is a field, "" when absent.
func (c *Card) F(name string) string {
	if c == nil {
		return ""
	}
	return c.Fields[name]
}

// Has says the card has the field, set to any value, an empty one too.
func (c *Card) Has(name string) bool {
	if c == nil {
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

// withField is a copy of c with one field set: the card as the step's own write
// leaves it, for the planning that follows in the same step (a rework's tier, read
// by the deal of the attempt it cuts).
func withField(c *Card, name, value string) *Card {
	cp := *c
	cp.Fields = make(map[string]string, len(c.Fields)+1)
	maps.Copy(cp.Fields, c.Fields)
	cp.Fields[name] = value
	return &cp
}

// Table is one table as observed: its revision, its rows in order, its text
// cells, and its cards (placed, and any unplaced records the step asked for),
// which are read by Card and Cards.
type Table struct {
	Name     string // logical name
	Epoch    uint64
	Revision uint64
	Texts    map[string]map[string]string // row -> text column -> value

	// cards are the table's cards by id, read by Card, Placed and Cards and
	// set by Put.
	cards map[string]*Card

	// props are the table's properties, read by Prop and set by SetProps.
	props map[string]string

	// rows are the table's rows in order, read by Rows and set by SetRows.
	rows []string

	cells     map[[2]string][]*Card // built on first use; Put resets it
	byPrimary map[string][]*Card
	lines     map[string][]*Card // each row's cards not landed, in score order; built with cells
	stops     map[string][]int   // each line's last sentinel at or before each place (lineStops); built with lines
}

// Put adds or replaces a card.
func (t *Table) Put(c *Card) {
	if c.Fields == nil {
		c.Fields = map[string]string{}
	}
	t.cards[c.ID] = c
	t.cells, t.byPrimary = nil, nil
}

// Frozen is a copy of the table as it is now that later changes to the
// table leave as it was: its own map of the same cards (a card is replaced
// whole, never changed in place, by the store's twin), rows, texts and
// properties. The tick's first read keeps one while the twin it was read
// from moves on.
func (t *Table) Frozen() *Table {
	if t == nil {
		return nil
	}
	c := *t
	c.cards = make(map[string]*Card, len(t.cards))
	maps.Copy(c.cards, t.cards)
	c.props = make(map[string]string, len(t.props))
	maps.Copy(c.props, t.props)
	c.Texts = make(map[string]map[string]string, len(t.Texts))
	maps.Copy(c.Texts, t.Texts)
	c.rows = append([]string(nil), t.rows...)
	c.cells, c.byPrimary, c.lines, c.stops = nil, nil, nil, nil
	return &c
}

// Drop takes a card out of the table's cards: a record the table no longer
// holds as a read would find it (store's twin, twin.go).
func (t *Table) Drop(id string) {
	if _, ok := t.cards[id]; !ok {
		return
	}
	delete(t.cards, id)
	t.cells, t.byPrimary = nil, nil
}

// SetProp sets one of the table's properties as a write left it (a batch's
// receipt): the rest are kept.
func (t *Table) SetProp(name, value string) {
	if t.props == nil {
		t.props = map[string]string{}
	}
	t.props[name] = value
}

// Props is the table's properties: a copy.
func (t *Table) Props() map[string]string {
	out := make(map[string]string, len(t.props))
	maps.Copy(out, t.props)
	return out
}

func (t *Table) index() {
	if t.cells != nil {
		return
	}
	t.cells, t.byPrimary, t.lines, t.stops = map[[2]string][]*Card{}, map[string][]*Card{}, nil, nil
	for _, c := range t.cards {
		if !c.Placed() {
			continue
		}
		k := [2]string{c.Row, c.Col}
		t.cells[k] = append(t.cells[k], c)
		// The index finds a card by its primary field; a card without one (a
		// control card) is no primary's, and is not indexed.
		if p := c.Fields[PrimaryField]; p != "" {
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

// Rows are the table's rows in order, not to be changed.
func (t *Table) Rows() []string {
	if t == nil {
		return nil
	}
	return t.rows
}

// SetRows sets the table's rows, in order.
func (t *Table) SetRows(rows []string) { t.rows = rows }

// SetProps sets the table's properties as read (a copy).
func (t *Table) SetProps(p map[string]string) {
	t.props = make(map[string]string, len(p))
	maps.Copy(t.props, p)
}

// Prop is the table's property name and whether it is present.
func (t *Table) Prop(name string) (string, bool) {
	if t == nil {
		return "", false
	}
	v, ok := t.props[name]
	return v, ok
}

// HasRow says the table declares the row.
func (t *Table) HasRow(row string) bool {
	return slices.Contains(t.rows, row)
}

// Card is the card with the id, nil when the table has none.
func (t *Table) Card(id string) *Card {
	if t == nil {
		return nil
	}
	return t.cards[id]
}

// Cards is every card the table holds, placed or kept, in id order: a copy of
// the list, of the same cards.
func (t *Table) Cards() []*Card {
	if t == nil {
		return nil
	}
	out := make([]*Card, 0, len(t.cards))
	for _, c := range t.cards {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Placed is the card with the id if it has a place.
func (t *Table) Placed(id string) *Card {
	c := t.Card(id)
	if !c.Placed() {
		return nil
	}
	return c
}

// Cell is the cards placed at row and column, in score order (then id).
func (t *Table) Cell(row, col string) []*Card {
	t.index()
	return t.cells[[2]string{row, col}]
}

// Column is the cards placed in the column on any row, in score order.
func (t *Table) Column(cols ...string) []*Card {
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

// Count is the number of cards at row and column.
func (t *Table) Count(row, col string) int {
	k := [2]string{row, col}
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
	// QueueLen is the length of the work table's queue as the step read it
	// (queue.go): changes steps planned while the machine ran, which the next
	// tick's pump applies. Queue is the queue itself, read only by the pump
	// that drains it.
	QueueLen int
	Queue    []QueuedChange
	// Held is the work cards a change queued after the pump's drain names,
	// read with a pump part's step: the part leaves them for the next tick's
	// pump (LeaveQueued), and a part that plans a card's move reads it to
	// plan none for them (TickAccept), so its notes and its stream's state
	// are planned for the cards it moves only. nil is none.
	Held map[string]bool
	// Routes are the model tiers' routes nova-config applied to the store,
	// read by a step that deals (route.go); nil is none.
	Routes []Route
	// Tiers are the tiers' route arrays nova-config applied (the tier kind), by
	// tier, read with the routes; a tier with none deals from its enabled routes
	// in name order (tierArray).
	Tiers map[string][]string
	// ReaderStates is each reader's state as the store derives it (ReaderState:
	// up, away or down), read by a step that asks (docs/SPEC-SPRINT.md section
	// 6); nil is none read, and every reader is held up.
	ReaderStates map[string]string
	// Running says the machine was RUNNING as the step read the sprint: its
	// pump accepts a primary with the ok reads it needs, so no step opens a "ready to
	// accept" judgment for it ("accept is mechanical").
	Running bool
	// rests is the routes resting at Now, settled once by a step that deals
	// (withRests, route_rest.go); nil is not yet settled.
	rests map[string]RouteRest
	// restScans, when set, counts withRests' scans of the fleet table: the tick's
	// cost gate (TestTheTicksCheckSettlesTheRestsOnceAtScale) holds them to one a part.
	restScans *int
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
	for _, m := range s.Fleet.Rows() {
		if s.MemberCtl(m).F("status") == Up {
			out = append(out, m)
		}
	}
	return out
}

// Streams is the work table's streams, in row order.
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
