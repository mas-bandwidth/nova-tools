// Package ntable is a general, Redis-backed table built from one primitive:
// the ordered set (Glenn 2026-09-27: "at an even simpler level, I think there
// should be a concept of ordered sets" / "The work stream table is really just
// a series of ordered sets, per-cell" / "and the value printed, happens to be
// for each cell, |s|" / "(but it doesn't need to be always)"). It knows
// nothing about sprints: the sprint table's stream block is its first table
// (internal/nsprint/table/streams.go), and the nova-table tool is its face.
//
// A table has three kinds of cell (Glenn, the same day: "there are cells
// that are headers for columns, and cells that are headers for rows" / "and
// there are cells at the bottom of each row that are sums or some function of
// the column above." / "for example, for the stream table the bottom rows are
// the sum of the column above."):
//
//   - HEADER cells: the top row is the column labels (Column.Label, default
//     the column name); the left column is the row labels (Row.Label, default
//     the row key). Labels, not sets.
//   - BODY cells: every body cell is an ordered set, a Redis ZSET, rendered
//     by its column's projection: count (the cardinality, Glenn's |s|),
//     members (the members in score order, comma-joined), first, last (the
//     lowest and highest scored member), text (a label column: the row's
//     label, no set).
//   - FOOTER cells: one per column, the column's fold over the body: sum or
//     max of the counts, union of the members, none (blank). The footer row
//     carries the table's footer label (default total). A table whose
//     columns all fold none prints no footer row.
//
// A cell's set is either OWNED by the table, at table:<t>:cell:<row>:<col>,
// and written by the cell verbs, or BOUND to a set another tool owns (the
// stream block's ws:<s>:<state>): a bound cell is a view, read freely and
// never written here (ErrBound). A row may name one member its counts and
// members leave out (Row.Exclude: the stream's sentinel is the stream's stop,
// not work).
//
// Keys, all under one prefix so an ACL row can grant them (~table:*), plus
// the registry set tables:
//
//	tables                     SET  every table name (List)
//	table:<t>                  HASH order (the column names, comma-joined),
//	                                footer (the footer label), created_at,
//	                                col:<name> = <projection>:<fold>:<width>:<label>
//	table:<t>:rows             ZSET row key -> rank (the render order)
//	table:<t>:row:<r>          HASH label, exclude, owner, key:<col> (a bound
//	                                cell's set; absent: the owned cell)
//	table:<t>:cell:<r>:<c>     ZSET an owned cell
//
// Reads are pipelines (Reader, QueueCells): one round trip per tick in the
// steady state, never KEYS or SCAN. Writes are plain pipelined commands
// (HSET, ZADD, ZREM, DEL), except the two that must be atomic over several
// keys, which are functions of the nova_sprint library
// (internal/nsprint/fn/lua/table.lua): ns_oset_move (Move) and
// ns_table_clear (Clear).
package ntable

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The projections: what a body cell prints.
const (
	Count   = "count"
	Members = "members"
	First   = "first"
	Last    = "last"
	Text    = "text"
)

// The folds: what a footer cell prints over its column.
const (
	Sum   = "sum"
	Max   = "max"
	Union = "union"
	None  = "none"
)

// DefaultFooter is the footer label a table has when none is set.
const DefaultFooter = "total"

// Registry is the SET of every table name.
const Registry = "tables"

// Column is one column: its name (the key its cells and definition use),
// its header label, the projection its body cells print, the fold its
// footer cell prints, and a fixed width (0: as wide as its widest cell).
type Column struct {
	Name       string
	Label      string
	Projection string
	Fold       string
	Width      int
}

// LabelOrName is the header cell.
func (c Column) LabelOrName() string {
	if c.Label == "" {
		return c.Name
	}
	return c.Label
}

// Member is one member of an ordered set with its score.
type Member struct {
	Member string
	Score  float64
}

// Cell is one body cell: its set's key ("" for a text column), whether the
// set is bound (owned elsewhere), and what the last read found: the count,
// the members (for members, first and last), and Unread when the set did
// not come back (it prints "?", never a false 0).
type Cell struct {
	Key     string
	Bound   bool
	Count   int64
	Members []Member
	Unread  bool
}

// Row is one row: its key, its label (the row header; "" prints the key),
// the member its counts leave out ("" for none), the verb that owns its
// bound sets (for the refusal that names it), and one cell per column.
type Row struct {
	Key     string
	Label   string
	Exclude string
	Owner   string
	Cells   []Cell
}

// LabelOrKey is the row header cell.
func (r Row) LabelOrKey() string {
	if r.Label == "" {
		return r.Key
	}
	return r.Label
}

// Table is a table as read, or as a caller declares it before binding.
type Table struct {
	Name        string
	Columns     []Column
	FooterLabel string
	Rows        []Row
}

// Footer is the footer label, DefaultFooter when unset.
func (t Table) Footer() string {
	if t.FooterLabel == "" {
		return DefaultFooter
	}
	return t.FooterLabel
}

// Column finds a column by name; -1 when the table has none.
func (t Table) Column(name string) int {
	for i, c := range t.Columns {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// Row finds a row by key; -1 when the table has none.
func (t Table) Row(key string) int {
	for i, r := range t.Rows {
		if r.Key == key {
			return i
		}
	}
	return -1
}

// HasFooter says some column folds: the render prints a footer row.
func (t Table) HasFooter() bool {
	for _, c := range t.Columns {
		if c.Fold != None && c.Fold != "" {
			return true
		}
	}
	return false
}

// Keys.

// DefKey is the table's definition hash.
func DefKey(table string) string { return "table:" + table }

// RowsKey is the table's row order set.
func RowsKey(table string) string { return "table:" + table + ":rows" }

// RowKey is one row's hash.
func RowKey(table, row string) string { return "table:" + table + ":row:" + row }

// CellKey is one owned cell's set.
func CellKey(table, row, col string) string { return "table:" + table + ":cell:" + row + ":" + col }

var (
	nameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
)

// ValidName says a table or column name is one word of letters, digits,
// '_', '.' and '-', starting with a letter, digit or '_'.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// ValidRowKey says a row key is non-empty and holds no control character.
func ValidRowKey(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ValidateColumns refuses a column list a table cannot carry: a bad name, a
// repeated name, an unknown projection or fold, or a fold that does not fit
// its projection (sum and max fold counts; union folds members, first and
// last; none folds anything; a text column folds none).
func ValidateColumns(cols []Column) error {
	if len(cols) == 0 {
		return errors.New("a table wants at least one column")
	}
	seen := map[string]bool{}
	for _, c := range cols {
		if !ValidName(c.Name) {
			return fmt.Errorf("column %q wants a name of letters, digits, _ . and -", c.Name)
		}
		if seen[c.Name] {
			return fmt.Errorf("column %s is named twice", c.Name)
		}
		seen[c.Name] = true
		if c.Width < 0 {
			return fmt.Errorf("column %s wants a width of 0 or more", c.Name)
		}
		switch c.Projection {
		case Count, Members, First, Last, Text:
		default:
			return fmt.Errorf("column %s wants a projection of count, members, first, last or text, not %q", c.Name, c.Projection)
		}
		switch c.Fold {
		case None:
		case Sum, Max:
			if c.Projection != Count {
				return fmt.Errorf("column %s folds %s, which wants the count projection, not %s", c.Name, c.Fold, c.Projection)
			}
		case Union:
			if c.Projection == Count || c.Projection == Text {
				return fmt.Errorf("column %s folds union, which wants members, first or last, not %s", c.Name, c.Projection)
			}
		default:
			return fmt.Errorf("column %s wants a fold of sum, max, union or none, not %q", c.Name, c.Fold)
		}
	}
	return nil
}

// ParseColumn reads one column declaration, name[:projection[:fold[:label]]]
// (defaults: count, sum for a count column and none otherwise, the name).
func ParseColumn(spec string) (Column, error) {
	parts := strings.SplitN(spec, ":", 4)
	c := Column{Name: parts[0], Projection: Count}
	if len(parts) > 1 && parts[1] != "" {
		c.Projection = parts[1]
	}
	if c.Projection == Count {
		c.Fold = Sum
	} else {
		c.Fold = None
	}
	if len(parts) > 2 && parts[2] != "" {
		c.Fold = parts[2]
	}
	if len(parts) > 3 {
		c.Label = parts[3]
	}
	if err := ValidateColumns([]Column{c}); err != nil {
		return Column{}, err
	}
	return c, nil
}

// ParseColumns reads a comma-separated list of column declarations.
func ParseColumns(specs string) ([]Column, error) {
	var cols []Column
	for _, spec := range strings.Split(specs, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		c, err := ParseColumn(spec)
		if err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	if err := ValidateColumns(cols); err != nil {
		return nil, err
	}
	return cols, nil
}

// ParseWidths reads col=n,col=n.
func ParseWidths(spec string) (map[string]int, error) {
	out := map[string]int{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, v, ok := strings.Cut(part, "=")
		n, err := strconv.Atoi(v)
		if !ok || err != nil || n < 0 || !ValidName(name) {
			return nil, fmt.Errorf("width %q wants col=n with n 0 or more", part)
		}
		out[name] = n
	}
	return out, nil
}

// encodeColumn is the definition hash's col:<name> value.
func encodeColumn(c Column) string {
	return c.Projection + ":" + c.Fold + ":" + strconv.Itoa(c.Width) + ":" + c.Label
}

// decodeColumn reads a col:<name> value.
func decodeColumn(name, v string) (Column, error) {
	parts := strings.SplitN(v, ":", 4)
	if len(parts) != 4 {
		return Column{}, fmt.Errorf("column %s is defined as %q, not projection:fold:width:label", name, v)
	}
	w, err := strconv.Atoi(parts[2])
	if err != nil {
		return Column{}, fmt.Errorf("column %s width %q is not a number", name, parts[2])
	}
	c := Column{Name: name, Projection: parts[0], Fold: parts[1], Width: w, Label: parts[3]}
	if err := ValidateColumns([]Column{c}); err != nil {
		return Column{}, err
	}
	return c, nil
}

// definitionFields is the definition hash of t: order, footer and one
// col:<name> per column (created_at is added by Create).
func definitionFields(t Table) map[string]string {
	names := make([]string, 0, len(t.Columns))
	m := map[string]string{}
	for _, c := range t.Columns {
		names = append(names, c.Name)
		m["col:"+c.Name] = encodeColumn(c)
	}
	m["order"] = strings.Join(names, ",")
	m["footer"] = t.Footer()
	return m
}

// decodeDefinition reads a definition hash; ok is false for an absent table
// (an empty hash).
func decodeDefinition(name string, h map[string]string) (Table, bool, error) {
	if len(h) == 0 {
		return Table{}, false, nil
	}
	t := Table{Name: name, FooterLabel: h["footer"]}
	order := h["order"]
	if order == "" {
		return Table{}, true, fmt.Errorf("table %s has no column order", name)
	}
	for _, col := range strings.Split(order, ",") {
		v, ok := h["col:"+col]
		if !ok {
			return Table{}, true, fmt.Errorf("table %s orders column %s, which it does not define", name, col)
		}
		c, err := decodeColumn(col, v)
		if err != nil {
			return Table{}, true, err
		}
		t.Columns = append(t.Columns, c)
	}
	return t, true, nil
}

// SameDefinition says two tables declare the same columns and footer.
func SameDefinition(a, b Table) bool {
	if a.Footer() != b.Footer() || len(a.Columns) != len(b.Columns) {
		return false
	}
	for i := range a.Columns {
		if a.Columns[i] != b.Columns[i] {
			return false
		}
	}
	return true
}

// rowFields is one row's hash: its label, exclude and owner when set, and
// key:<col> for every bound cell.
func rowFields(t Table, r Row) map[string]string {
	m := map[string]string{}
	if r.Label != "" {
		m["label"] = r.Label
	}
	if r.Exclude != "" {
		m["exclude"] = r.Exclude
	}
	if r.Owner != "" {
		m["owner"] = r.Owner
	}
	for i, c := range t.Columns {
		if i < len(r.Cells) && r.Cells[i].Bound && r.Cells[i].Key != "" {
			m["key:"+c.Name] = r.Cells[i].Key
		}
	}
	return m
}

// decodeRow builds a row of t from its key and hash: every cell keyed, bound
// where the hash names a key, owned (CellKey) otherwise, none for a text
// column.
func decodeRow(t Table, key string, h map[string]string) Row {
	r := Row{Key: key, Label: h["label"], Exclude: h["exclude"], Owner: h["owner"], Cells: make([]Cell, len(t.Columns))}
	for i, c := range t.Columns {
		if c.Projection == Text {
			continue
		}
		if k, ok := h["key:"+c.Name]; ok && k != "" {
			r.Cells[i] = Cell{Key: k, Bound: true}
		} else {
			r.Cells[i] = Cell{Key: CellKey(t.Name, key, c.Name)}
		}
	}
	return r
}

// NewRow is a row of t with every non-text cell owned, before any binding.
func NewRow(t Table, key string) Row {
	return decodeRow(t, key, nil)
}

// SameShape says two tables have the same definition and the same rows with
// the same bindings, labels, excludes and owners (the cells' values aside):
// what a writer compares before binding again.
func SameShape(a, b Table) bool {
	if !SameDefinition(a, b) || len(a.Rows) != len(b.Rows) {
		return false
	}
	for i := range a.Rows {
		ra, rb := a.Rows[i], b.Rows[i]
		if ra.Key != rb.Key || ra.Label != rb.Label || ra.Exclude != rb.Exclude || ra.Owner != rb.Owner || len(ra.Cells) != len(rb.Cells) {
			return false
		}
		for j := range ra.Cells {
			if ra.Cells[j].Key != rb.Cells[j].Key || ra.Cells[j].Bound != rb.Cells[j].Bound {
				return false
			}
		}
	}
	return true
}
