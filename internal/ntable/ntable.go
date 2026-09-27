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
//     the row key), in front of every declared column. Labels, not sets.
//   - BODY cells: every body cell is an ordered set, a Redis ZSET, rendered
//     by its column's projection: count (the cardinality, Glenn's |s|),
//     members (the members in score order, comma-joined), first, last (the
//     lowest and highest scored member), text (a value per row, set by
//     row set, blank when none; no set).
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
// Each table operation is one Redis function call. Read-only snapshots
// include the definition, current rows, bindings and cells even on a cold
// read. QueueCells remains available to callers already holding a shape,
// so they can include cell reads in their own larger pipeline. Writes
// validate bindings and permissions before changing any key.
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
	Sum    = "sum"
	Max    = "max"
	Union  = "union"
	Avg    = "avg"    // the mean of a count column over the rows
	Pooled = "pooled" // a pct column's footer: the named counts summed over every row's counts summed (never the mean of percentages: Glenn 2026-09-27)
	None   = "none"
)

// A formula projection (SPEC-NOVA-TABLE, computed cells; Glenn 2026-09-27:
// "waiting% ... the % of waiting tasks as a % of all tasks in that row"):
// pct(<col>) is the named count column as a percentage of the row's count
// columns together. A formula cell holds no set: computed at render, never
// stored, never written.
const pctPrefix = "pct("

// IsFormula is whether a projection is computed rather than read.
func IsFormula(projection string) bool {
	return strings.HasPrefix(projection, pctPrefix) && strings.HasSuffix(projection, ")")
}

// FormulaArg is the column a pct(<col>) projection names.
func FormulaArg(projection string) string {
	return strings.TrimSuffix(strings.TrimPrefix(projection, pctPrefix), ")")
}

// HasSet is whether a column's cells are ordered sets in the store (text
// and formula columns have none).
func (c Column) HasSet() bool { return c.Projection != Text && !IsFormula(c.Projection) }

// DefaultFooter is the footer label a table has when none is set: none
// (Glenn 2026-09-27: "the footer title should be off by default"); the
// footer row still prints the folds, with a blank label cell, and
// `create --footer <label>` names it.
const DefaultFooter = ""

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
	Texts   map[string]string // a text column's value for this row (row set <col>=<value>), else blank
	Hidden  bool              // kept and counted in the folds, not drawn (row hide)
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
	// EpochKey/Field bind this table to a shared epoch domain. An empty key
	// means epoch zero permanently; the default field for a key is "n".
	EpochKey     string
	EpochField   string
	MemberPrefix string
	// Epoch and Revision describe the snapshot returned by the store.
	Epoch       uint64
	Revision    uint64
	Name        string
	Columns     []Column
	FooterLabel string
	Hidden      []string // columns kept, read and used by formulas, but not drawn (set --hide)
	HiddenTable bool     // the whole table kept and read, not drawn by watch (set --hidden / --visible)
	Rows        []Row
}

// IsHidden is whether a column is kept but not drawn.
func (t Table) IsHidden(col string) bool {
	for _, h := range t.Hidden {
		if h == col {
			return true
		}
	}
	return false
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

// EpochPrefix is the storage namespace for one table generation.
func EpochPrefix(table string, epoch uint64) string {
	p := DefKey(table)
	if epoch != 0 {
		p += ":" + strconv.FormatUint(epoch, 10)
	}
	return p
}

func RowsKeyAt(table string, epoch uint64) string { return EpochPrefix(table, epoch) + ":rows" }
func RowKeyAt(table, row string, epoch uint64) string {
	return EpochPrefix(table, epoch) + ":row:" + row
}
func CellKeyAt(table, row, col string, epoch uint64) string {
	return EpochPrefix(table, epoch) + ":cell:" + row + ":" + col
}

// The original key helpers name epoch zero for existing callers.
func RowsKey(table string) string           { return RowsKeyAt(table, 0) }
func RowKey(table, row string) string       { return RowKeyAt(table, row, 0) }
func CellKey(table, row, col string) string { return CellKeyAt(table, row, col, 0) }

// MemberKey is reserved metadata, outside every valid table-name prefix.
func MemberKey(id string) string      { return "table::member:" + id }
func ChangesKey(table string) string  { return DefKey(table) + ":changes" }
func RevisionKey(table string) string { return DefKey(table) + ":revision" }

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
		if seen[c.Name] {
			return fmt.Errorf("column %s is named twice", c.Name)
		}
		seen[c.Name] = true
		if err := validateColumn(c, cols); err != nil {
			return err
		}
	}
	return nil
}

// validateColumn checks one column's grammar; with cols (the whole table) it
// also checks that a pct(<col>) names a count column of that table.
func validateColumn(c Column, cols []Column) error {
	if !ValidName(c.Name) {
		return fmt.Errorf("column %q wants a name of letters, digits, _ . and -", c.Name)
	}
	if c.Width < 0 {
		return fmt.Errorf("column %s wants a width of 0 or more", c.Name)
	}
	switch {
	case c.Projection == Count, c.Projection == Members, c.Projection == First, c.Projection == Last, c.Projection == Text:
	case IsFormula(c.Projection):
		if cols != nil {
			arg := FormulaArg(c.Projection)
			found := false
			for _, o := range cols {
				if o.Name == arg && o.Projection == Count {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("column %s is pct(%s), which wants a count column named %s in the same table", c.Name, arg, arg)
			}
		}
	default:
		return fmt.Errorf("column %s wants a projection of count, members, first, last, text or pct(<count column>), not %q", c.Name, c.Projection)
	}
	switch c.Fold {
	case None:
	case Sum, Max:
		if c.Projection != Count {
			return fmt.Errorf("column %s folds %s, which wants the count projection, not %s", c.Name, c.Fold, c.Projection)
		}
	case Avg:
		if IsFormula(c.Projection) {
			return fmt.Errorf("column %s folds avg over percentages, which is not accurate; fold pooled (the counts summed over the rows, then the share)", c.Name)
		}
		if c.Projection != Count {
			return fmt.Errorf("column %s folds avg, which wants a count column, not %s", c.Name, c.Projection)
		}
	case Pooled:
		if !IsFormula(c.Projection) {
			return fmt.Errorf("column %s folds pooled, which wants a pct column, not %s", c.Name, c.Projection)
		}
	case Union:
		if c.Projection == Count || c.Projection == Text || IsFormula(c.Projection) {
			return fmt.Errorf("column %s folds union, which wants members, first or last, not %s", c.Name, c.Projection)
		}
	default:
		return fmt.Errorf("column %s wants a fold of sum, max, avg, pooled, union or none, not %q", c.Name, c.Fold)
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
	switch {
	case c.Projection == Count:
		c.Fold = Sum
	case IsFormula(c.Projection):
		c.Fold = Pooled
	default:
		c.Fold = None
	}
	if len(parts) > 2 && parts[2] != "" {
		c.Fold = parts[2]
	}
	if len(parts) > 3 {
		c.Label = parts[3]
	}
	if err := validateColumn(c, nil); err != nil {
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
	if err := validateColumn(c, nil); err != nil {
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
	if t.MemberPrefix != "" {
		m["member_prefix"] = t.MemberPrefix
	}
	if t.EpochKey != "" {
		m["epoch_key"] = t.EpochKey
		m["epoch_field"] = t.EpochField
		if m["epoch_field"] == "" {
			m["epoch_field"] = "n"
		}
	}
	if len(t.Hidden) > 0 {
		m["hidden"] = strings.Join(t.Hidden, ",")
	}
	if t.HiddenTable {
		m["visible"] = "0"
	}
	return m
}

// decodeDefinition reads a definition hash; ok is false for an absent table
// (an empty hash).
func decodeDefinition(name string, h map[string]string) (Table, bool, error) {
	if len(h) == 0 {
		return Table{}, false, nil
	}
	t := Table{Name: name, FooterLabel: h["footer"], EpochKey: h["epoch_key"], EpochField: h["epoch_field"], MemberPrefix: h["member_prefix"]}
	for key, dst := range map[string]*uint64{"read_epoch": &t.Epoch, "read_revision": &t.Revision} {
		if v := h[key]; v != "" {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return Table{}, true, fmt.Errorf("table %s: malformed %s %q", name, key, v)
			}
			*dst = n
		}
	}
	if v := strings.TrimSpace(h["hidden"]); v != "" {
		t.Hidden = strings.Split(v, ",")
	}
	t.HiddenTable = h["visible"] == "0"
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
	if a.EpochField == "" || a.EpochKey == "" {
		a.EpochField = "n"
	}
	if b.EpochField == "" || b.EpochKey == "" {
		b.EpochField = "n"
	}
	if a.MemberPrefix == "" {
		a.MemberPrefix = "table::member:"
	}
	if b.MemberPrefix == "" {
		b.MemberPrefix = "table::member:"
	}
	if a.Footer() != b.Footer() || len(a.Columns) != len(b.Columns) || a.EpochKey != b.EpochKey || a.EpochField != b.EpochField || a.MemberPrefix != b.MemberPrefix {
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
	r := Row{Key: key, Label: h["label"], Exclude: h["exclude"], Owner: h["owner"], Hidden: h["hidden"] == "1", Cells: make([]Cell, len(t.Columns))}
	for i, c := range t.Columns {
		if c.Projection == Text {
			if v, ok := h["text:"+c.Name]; ok {
				if r.Texts == nil {
					r.Texts = map[string]string{}
				}
				r.Texts[c.Name] = v
			}
			continue
		}
		if IsFormula(c.Projection) {
			continue
		}
		if k, ok := h["key:"+c.Name]; ok && k != "" {
			r.Cells[i] = Cell{Key: k, Bound: true}
		} else {
			r.Cells[i] = Cell{Key: CellKeyAt(t.Name, key, c.Name, t.Epoch)}
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
