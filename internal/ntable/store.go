package ntable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	FnClear      = "ns_table_clear"
	FnCreate     = "ns_table_create"
	FnDrop       = "ns_table_drop"
	FnSet        = "ns_table_set"
	FnRowSet     = "ns_table_row_set"
	FnRowsAdd    = "ns_table_rows_add"
	FnRowsHide   = "ns_table_rows_hide"
	FnRowAdd     = "ns_table_row_add"
	FnRowDel     = "ns_table_row_del"
	FnCellAdd    = "ns_table_cell_add"
	FnCellRemove = "ns_table_cell_remove"
	FnCellMove   = "ns_table_cell_move"
	FnBind       = "ns_table_bind"
	FnRead       = "ns_table_read"
	FnList       = "ns_table_list"
	FnMembers    = "ns_table_members"
)

var ErrExists = errors.New("exists with another definition")

// BoundError names the other writer. The table may read the binding but
// cannot acquire write ownership merely by displaying it.
type BoundError struct{ Table, Row, Col, Key, Owner string }

func (e *BoundError) Error() string {
	s := fmt.Sprintf("%s.%s.%s is bound to %s, owned elsewhere", e.Table, e.Row, e.Col, e.Key)
	if e.Owner != "" {
		s += "; run: " + e.Owner
	}
	return s
}

type operation struct{ table, row, col, member string }

func (o operation) location() string {
	s := fmt.Sprintf("table %q", o.table)
	if o.row != "" {
		s += fmt.Sprintf(" row %q", o.row)
	}
	if o.col != "" {
		s += fmt.Sprintf(" column %q", o.col)
	}
	if o.member != "" {
		s += fmt.Sprintf(" member %q", o.member)
	}
	return s
}

// shellWord makes the suggested command safe to paste even for spaced
// rows or members containing shell metacharacters.
func shellWord(s string) string    { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func (o operation) remedy() string { return "nova-table show " + shellWord(o.table) }
func (o operation) refused(reply []any) error {
	if len(reply) == 0 {
		return fmt.Errorf("%s: empty function reply; run: %s", o.location(), o.remedy())
	}
	if fmt.Sprint(reply[0]) != "REFUSED" {
		return nil
	}
	if len(reply) < 2 {
		return fmt.Errorf("%s: malformed refusal; run: %s", o.location(), o.remedy())
	}
	reason := fmt.Sprint(reply[1])
	var cause error
	remedy := o.remedy()
	switch reason {
	case "NOTABLE":
		cause = ErrNoTable
		remedy = "nova-table create " + shellWord(o.table) + " --columns <columns>"
	case "EXISTS":
		cause = ErrExists
	case "NOROW":
		cause = errors.New("no such row")
		remedy = "nova-table row add " + shellWord(o.table) + " " + shellWord(o.row)
	case "NOCOL":
		cause = errors.New("no such column")
	case "TEXT":
		cause = errors.New("text column holds no ordered set; choose a body column")
	case "NOTMEMBER":
		cause = ErrNotMember
		remedy = "nova-table cell members " + shellWord(o.table) + " " + shellWord(o.row) + " " + shellWord(o.col)
	case "NOMEMBER":
		cause = errors.New("wants at least one member")
	case "NOVIEW":
		cause = errors.New("no such view; run: nova-table view set <name> --tables <a,b,...>")
	case "OCCUPIED":
		cause = errors.New("the column holds owned members in that row; move or remove them first")
	case "NOTTEXT":
		cause = errors.New("not a text column; row set writes text columns only")
	case "BOUND":
		if len(reply) < 6 {
			return fmt.Errorf("%s: malformed bound-cell refusal", o.location())
		}
		return fmt.Errorf("%s: %w", o.location(), &BoundError{Table: o.table, Row: fmt.Sprint(reply[2]), Col: fmt.Sprint(reply[3]), Key: fmt.Sprint(reply[4]), Owner: fmt.Sprint(reply[5])})
	default:
		cause = fmt.Errorf("%s %v", reason, reply[2:])
	}
	// The server can identify a different column (e.g. the move destination).
	if (reason == "NOCOL" || reason == "TEXT") && len(reply) >= 4 {
		o.row = fmt.Sprint(reply[2])
		o.col = fmt.Sprint(reply[3])
	}
	return fmt.Errorf("%s: %w; run: %s", o.location(), cause, remedy)
}
func (o operation) call(ctx context.Context, c redis.Cmdable, fn string, ro bool, args ...any) ([]any, error) {
	all := append([]any{o.table}, args...)
	var cmd *redis.Cmd
	if ro {
		cmd = c.FCallRO(ctx, fn, []string{DefKey(o.table)}, all...)
	} else {
		cmd = c.FCall(ctx, fn, []string{DefKey(o.table)}, all...)
	}
	reply, err := cmd.Slice()
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w; run: %s", o.location(), fn, err, o.remedy())
	}
	if err := o.refused(reply); err != nil {
		return nil, err
	}
	return reply, nil
}
func replyCount(reply []any) (int64, error) {
	if len(reply) < 2 {
		return 0, fmt.Errorf("table function returned no count: %v", reply)
	}
	return strconv.ParseInt(fmt.Sprint(reply[1]), 10, 64)
}
func definitionPayload(t Table, now time.Time) (map[string]string, error) {
	if !ValidName(t.Name) {
		return nil, fmt.Errorf("table %q wants letters, digits, _ . and -; run: nova-table help", t.Name)
	}
	if err := ValidateColumns(t.Columns); err != nil {
		return nil, fmt.Errorf("table %q: %w; run: nova-table help", t.Name, err)
	}
	fields := definitionFields(t)
	fields["created_at"] = now.UTC().Format(time.RFC3339)
	return fields, nil
}
func payload(v any) (string, error) { b, err := json.Marshal(v); return string(b), err }

// Create checks the definition and writes it atomically in one round trip.
func Create(ctx context.Context, c redis.Cmdable, t Table, now time.Time) error {
	fields, err := definitionPayload(t, now)
	if err != nil {
		return err
	}
	body, err := payload(fields)
	if err != nil {
		return err
	}
	_, err = (operation{table: t.Name}).call(ctx, c, FnCreate, false, body)
	return err
}

// Drop removes owned keys and the definition; bound sets remain untouched.
// SetOpts is what Set changes: the footer label (Footer set, "" for none)
// and the table's name (Rename non-empty). Rows, cells and bindings stay.
type SetOpts struct {
	Footer  *string
	Rename  string
	Columns []Column  // the columns replaced in place: rows kept, cells of removed columns dropped
	Hidden  *[]string // the hidden columns, replaced whole: kept and read, not drawn
	Visible *bool     // the whole table drawn by watch (true) or kept but not drawn (false)
}

// Set changes a table's definition in place, one call: the footer label,
// the name (every key of the table moved, the registry updated; a name in
// use is refused EXISTS). It returns how many keys a rename moved.
func Set(ctx context.Context, c redis.Cmdable, name string, o SetOpts) (int, error) {
	var args []any
	if o.Footer != nil {
		args = append(args, "footer", *o.Footer)
	}
	if o.Rename != "" {
		if !ValidName(o.Rename) {
			return 0, fmt.Errorf("table %q: the new name wants letters, digits, _ . and -", o.Rename)
		}
		args = append(args, "rename", o.Rename)
	}
	if len(o.Columns) > 0 {
		if err := ValidateColumns(o.Columns); err != nil {
			return 0, fmt.Errorf("table %q: %w; run: nova-table help", name, err)
		}
		fields := definitionFields(Table{Name: name, Columns: o.Columns})
		delete(fields, "footer")
		body, err := payload(fields)
		if err != nil {
			return 0, err
		}
		args = append(args, "columns", body)
	}
	if o.Hidden != nil {
		args = append(args, "hidden", strings.Join(*o.Hidden, ","))
	}
	if o.Visible != nil {
		v := "1"
		if !*o.Visible {
			v = "0"
		}
		args = append(args, "visible", v)
	}
	if len(args) == 0 {
		return 0, fmt.Errorf("table %q: set wants --footer <label>, --rename <name>, --columns <spec>, --hide <cols> or --show <cols>", name)
	}
	reply, err := (operation{table: name}).call(ctx, c, FnSet, false, args...)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

func Drop(ctx context.Context, c redis.Cmdable, name string) (int, error) {
	reply, err := (operation{table: name}).call(ctx, c, FnDrop, false)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

type RowSpec struct {
	Label   string            `json:"label"`
	Exclude string            `json:"exclude"`
	Owner   string            `json:"owner"`
	Binds   map[string]string `json:"binds,omitempty"`
}

func RowAdd(ctx context.Context, c redis.Cmdable, name, key string, spec RowSpec) (Row, error) {
	o := operation{table: name, row: key}
	if !ValidRowKey(key) {
		return Row{}, fmt.Errorf("%s: row wants a non-empty key with no control characters; run: nova-table row help", o.location())
	}
	body, err := payload(spec)
	if err != nil {
		return Row{}, err
	}
	reply, err := o.call(ctx, c, FnRowAdd, false, key, body)
	if err != nil {
		return Row{}, err
	}
	if len(reply) != 3 {
		return Row{}, fmt.Errorf("%s: malformed row reply", o.location())
	}
	h, err := flatHash(reply[1])
	if err != nil {
		return Row{}, err
	}
	t, _, err := decodeDefinition(name, h)
	if err != nil {
		return Row{}, err
	}
	h, err = flatHash(reply[2])
	if err != nil {
		return Row{}, err
	}
	return decodeRow(t, key, h), nil
}

// RowSet writes a text column's value for one row (text:<col> on the row
// hash), several columns in one call; a column that is not a text column
// is refused NOTTEXT.
func RowSet(ctx context.Context, c redis.Cmdable, name, key string, texts map[string]string) (int, error) {
	args := []any{key}
	names := make([]string, 0, len(texts))
	for col := range texts {
		names = append(names, col)
	}
	sort.Strings(names)
	for _, col := range names {
		args = append(args, col, texts[col])
	}
	reply, err := (operation{table: name, row: key}).call(ctx, c, FnRowSet, false, args...)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

// RowsAdd adds many rows with empty specs in one call; every key is
// checked before the first is written.
func RowsAdd(ctx context.Context, c redis.Cmdable, name string, keys []string) (int, error) {
	args := make([]any, 0, len(keys))
	for _, k := range keys {
		if !ValidRowKey(k) {
			return 0, fmt.Errorf("table %q row %q: row wants a non-empty key with no control characters; run: nova-table row help", name, k)
		}
		args = append(args, k)
	}
	reply, err := (operation{table: name}).call(ctx, c, FnRowsAdd, false, args...)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

// RowsHide hides rows from the render (kept, counted in the folds) or
// shows them again; many rows, one call.
func RowsHide(ctx context.Context, c redis.Cmdable, name string, hide bool, keys []string) (int, error) {
	flag := "0"
	if hide {
		flag = "1"
	}
	args := []any{flag}
	for _, k := range keys {
		args = append(args, k)
	}
	reply, err := (operation{table: name}).call(ctx, c, FnRowsHide, false, args...)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

func RowDel(ctx context.Context, c redis.Cmdable, name, key string) (bool, error) {
	reply, err := (operation{table: name, row: key}).call(ctx, c, FnRowDel, false, key)
	if err != nil {
		return false, err
	}
	n, err := replyCount(reply)
	return n != 0, err
}

// Bind replaces the caller-owned table shape in one atomic call. Existing
// owned cells of retained rows survive; removed rows leave bound sets alone.
func Bind(ctx context.Context, c redis.Cmdable, t Table, now time.Time) error {
	fields, err := definitionPayload(t, now)
	if err != nil {
		return err
	}
	type boundRow struct {
		Key string `json:"key"`
		RowSpec
	}
	rows := make([]boundRow, 0, len(t.Rows))
	seen := map[string]bool{}
	for _, r := range t.Rows {
		if !ValidRowKey(r.Key) || seen[r.Key] {
			return fmt.Errorf("table %q row %q: invalid or repeated row key; run: nova-table show %s", t.Name, r.Key, shellWord(t.Name))
		}
		seen[r.Key] = true
		if len(r.Cells) != len(t.Columns) {
			return fmt.Errorf("table %q row %q: %d cells for %d columns", t.Name, r.Key, len(r.Cells), len(t.Columns))
		}
		spec := RowSpec{Label: r.Label, Exclude: r.Exclude, Owner: r.Owner, Binds: map[string]string{}}
		for i, cell := range r.Cells {
			if cell.Bound {
				spec.Binds[t.Columns[i].Name] = cell.Key
			}
		}
		rows = append(rows, boundRow{r.Key, spec})
	}
	body, err := payload(struct {
		Fields map[string]string `json:"fields"`
		Rows   []boundRow        `json:"rows"`
	}{fields, rows})
	if err != nil {
		return err
	}
	_, err = (operation{table: t.Name}).call(ctx, c, FnBind, false, body)
	return err
}
func Clear(ctx context.Context, c redis.Cmdable, name string) (int64, error) {
	reply, err := (operation{table: name}).call(ctx, c, FnClear, false)
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}

// CellAdd puts members into a cell at one score, one call; every member
// is checked before the first write. It returns the cell's count after.
func CellAdd(ctx context.Context, c redis.Cmdable, name, row, col string, score float64, members ...string) (int64, error) {
	args := []any{row, col, strconv.FormatFloat(score, 'g', -1, 64)}
	for _, m := range members {
		args = append(args, m)
	}
	reply, err := (operation{name, row, col, first(members)}).call(ctx, c, FnCellAdd, false, args...)
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}

// CellRemove takes members out of a cell, one call.
func CellRemove(ctx context.Context, c redis.Cmdable, name, row, col string, members ...string) (int64, error) {
	args := []any{row, col}
	for _, m := range members {
		args = append(args, m)
	}
	reply, err := (operation{name, row, col, first(members)}).call(ctx, c, FnCellRemove, false, args...)
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}

// CellMove moves members from one cell to another of the same row, one
// call, keeping every score; a member not in the source refuses the whole
// call (NOTMEMBER names it) and nothing moves.
func CellMove(ctx context.Context, c redis.Cmdable, name, row, from, to string, members ...string) (int64, error) {
	args := []any{row, from, to}
	for _, m := range members {
		args = append(args, m)
	}
	reply, err := (operation{name, row, from, first(members)}).call(ctx, c, FnCellMove, false, args...)
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// A view: a named list of tables with a title, read by watch every frame.
type View struct {
	Name    string
	Tables  []string
	Title   string
	Summary string // the count column of the first table the summary line counts as done ("" for no line)
}

// ViewSet writes a view; every table must exist.
func ViewSet(ctx context.Context, c redis.Cmdable, v View) error {
	_, err := (operation{table: v.Name}).call(ctx, c, "ns_view_set", false, strings.Join(v.Tables, ","), v.Title, v.Summary)
	return err
}

// ViewGet reads a view.
func ViewGet(ctx context.Context, c redis.Cmdable, name string) (View, error) {
	reply, err := (operation{table: name}).call(ctx, c, "ns_view_get", true)
	if err != nil {
		return View{}, err
	}
	if len(reply) < 2 {
		return View{}, fmt.Errorf("view %q: malformed reply", name)
	}
	h, err := flatHash(reply[1])
	if err != nil {
		return View{}, err
	}
	v := View{Name: name, Title: h["title"], Summary: h["summary"]}
	if t := strings.TrimSpace(h["tables"]); t != "" {
		v.Tables = strings.Split(t, ",")
	}
	return v, nil
}
func CellMembers(ctx context.Context, c redis.Cmdable, name, row, col string) ([]Member, error) {
	reply, err := (operation{table: name, row: row, col: col}).call(ctx, c, FnMembers, true, row, col)
	if err != nil {
		return nil, err
	}
	if len(reply) != 2 {
		return nil, fmt.Errorf("table %q row %q column %q: malformed members reply", name, row, col)
	}
	return flatMembers(reply[1])
}
