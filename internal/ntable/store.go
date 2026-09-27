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
func CellAdd(ctx context.Context, c redis.Cmdable, name, row, col, member string, score float64) (int64, error) {
	reply, err := (operation{name, row, col, member}).call(ctx, c, FnCellAdd, false, row, col, member, strconv.FormatFloat(score, 'g', -1, 64))
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}
func CellRemove(ctx context.Context, c redis.Cmdable, name, row, col, member string) (int64, error) {
	reply, err := (operation{name, row, col, member}).call(ctx, c, FnCellRemove, false, row, col, member)
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}
func CellMove(ctx context.Context, c redis.Cmdable, name, row, from, to, member string) (int64, error) {
	reply, err := (operation{name, row, from, member}).call(ctx, c, FnCellMove, false, row, from, member, to)
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
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
