package ntable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	FnClear          = "ns_table_clear"
	FnCreate         = "ns_table_create"
	FnSet            = "ns_table_set"
	FnRowSet         = "ns_table_row_set"
	FnRowsAdd        = "ns_table_rows_add"
	FnRowsHide       = "ns_table_rows_hide"
	FnDrop           = "ns_table_drop"
	FnDropDefinition = "ns_table_drop_definition"
	FnMemberCreate   = "ns_table_member_create"
	FnMemberFind     = "ns_table_member_find"
	FnRowAdd         = "ns_table_row_add"
	FnRowDel         = "ns_table_row_del"
	FnCellAdd        = "ns_table_cell_add"
	FnCellRemove     = "ns_table_cell_remove"
	FnCellMove       = "ns_table_cell_move"
	FnBind           = "ns_table_bind"
	FnRead           = "ns_table_read"
	FnCheck          = "ns_table_check"
	FnList           = "ns_table_list"
	FnMembers        = "ns_table_members"
)

var (
	ErrExists       = errors.New("exists with another definition")
	ErrOccupied     = errors.New("shape would delete or hide placed members")
	ErrOwnedAlias   = errors.New("binding target is table-owned storage")
	ErrStale        = errors.New("observed epoch is stale")
	ErrMemberEpoch  = errors.New("member belongs to another epoch")
	ErrPlaced       = errors.New("member already has a place in this table")
	ErrDrift        = errors.New("member record and owned set disagree")
	ErrMemberExists = errors.New("member identity already exists")
)

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

// WriteOptions binds a mutation to the epoch its caller observed. Omitting
// options means epoch zero; a stale call is never retried into a new epoch.
// Actor, Fence and Idem accompany the change event; authorization and the
// coordinator lease are separate from this table primitive.
type WriteOptions struct {
	Epoch uint64
	Actor string
	Fence string
	Idem  string
	// Receipt receives the committed event without another store exchange.
	Receipt *Receipt
}

// Receipt identifies the durable table change. Idem is recorded as caller
// metadata; this primitive does not deduplicate attempts.
type Receipt struct {
	ID                   string
	Epoch, Before, After uint64
	Outcome              string
}

type operation struct {
	table, row, col, member string
	view                    bool
}

func (o operation) location() string {
	kind := "table"
	if o.view {
		if o.table == "" {
			return "views"
		}
		kind = "view"
	}
	s := fmt.Sprintf("%s %q", kind, o.table)
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
func shellWord(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func (o operation) remedy() string {
	if o.view {
		if o.table == "" {
			return "nova-table help view"
		}
		return "nova-table view show " + shellWord(o.table)
	}
	return "nova-table show " + shellWord(o.table)
}
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
	case "STALE":
		cause = fmt.Errorf("%w: observed %v, active %v", ErrStale, reply[2], reply[3])
	case "MEMBEREPOCH":
		cause = fmt.Errorf("%w: %v", ErrMemberEpoch, reply[2:])
	case "MEMBEREXISTS":
		cause = ErrMemberExists
	case "PLACED":
		cause = fmt.Errorf("%w: %v", ErrPlaced, reply[2:])
	case "DRIFT":
		cause = fmt.Errorf("%w: %v", ErrDrift, reply[2:])
	case "NOTABLE":
		cause = ErrNoTable
		remedy = "nova-table create " + shellWord(o.table) + " --columns <columns>"
	case "EXISTS":
		cause = ErrExists
		remedy = "nova-table set " + shellWord(o.table) + " --columns <columns>"
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
	case "OCCUPIEDVALUE":
		o.row, o.col = fmt.Sprint(reply[2]), fmt.Sprint(reply[3])
		cause = fmt.Errorf("%w: text cell is nonempty; clear it with row set first", ErrOccupied)
	case "OCCUPIED":
		if len(reply) != 5 {
			return fmt.Errorf("%s: malformed occupied-cell refusal", o.location())
		}
		members, ok := reply[4].([]any)
		if !ok || len(members) == 0 {
			return fmt.Errorf("%s: malformed occupied members", o.location())
		}
		o.row, o.col = fmt.Sprint(reply[2]), fmt.Sprint(reply[3])
		names := make([]string, 0, len(members))
		for _, member := range members {
			names = append(names, fmt.Sprintf("%q", member))
		}
		cause = fmt.Errorf("%w: %s; move each member to a retained owned cell or remove it first", ErrOccupied, strings.Join(names, ", "))
		remedy = "nova-table cell remove " + shellWord(o.table) + " " + shellWord(o.row) + " " + shellWord(o.col) + " " + shellWord(fmt.Sprint(members[0]))
	case "OWNEDALIAS":
		if len(reply) != 5 {
			return fmt.Errorf("%s: malformed owned-alias refusal", o.location())
		}
		o.row, o.col = fmt.Sprint(reply[2]), fmt.Sprint(reply[3])
		cause = fmt.Errorf("%w: %q; choose a set owned outside nova-table", ErrOwnedAlias, reply[4])
	case "NOMEMBER":
		cause = errors.New("wants at least one member")
	case "NOVIEW":
		cause = errors.New("no such view")
		remedy = "nova-table view set " + shellWord(o.table) + " --tables <a,b,...>"
	case "VIEWTABLE":
		if len(reply) != 3 {
			return fmt.Errorf("%s: malformed view-table refusal", o.location())
		}
		cause = fmt.Errorf("referenced table %q does not exist", reply[2])
		remedy = "nova-table create " + shellWord(fmt.Sprint(reply[2])) + " --columns <columns>"
	case "SUMMARY":
		if len(reply) != 4 {
			return fmt.Errorf("%s: malformed summary refusal", o.location())
		}
		cause = fmt.Errorf("summary wants a count column in table %q; %q is not one", reply[2], reply[3])
		remedy = "nova-table show " + shellWord(fmt.Sprint(reply[2]))
	case "SELF":
		cause = fmt.Errorf("%v cannot go before or after itself", reply[2])
	case "WHERE":
		cause = errors.New("a place is --first, --last, --before <x> or --after <x>")
	case "COLEXISTS":
		o.col = fmt.Sprint(reply[2])
		cause = errors.New("the column is already there")
		remedy = "nova-table col move " + shellWord(o.table) + " " + shellWord(o.col) + " --last"
	case "DEPENDS":
		o.col = fmt.Sprint(reply[2])
		cause = fmt.Errorf("column %q is a percentage of it; remove that column first", reply[3])
		remedy = "nova-table col del " + shellWord(o.table) + " " + shellWord(fmt.Sprint(reply[3]))
	case "LASTCOL":
		o.col = fmt.Sprint(reply[2])
		cause = errors.New("a table keeps at least one column")
		remedy = "nova-table drop " + shellWord(o.table)
	case "SORTED":
		cause = fmt.Errorf("the rows are kept sorted by %v, so no row is placed by hand; end the standing sort first", reply[2])
		remedy = "nova-table row sort " + shellWord(o.table) + " --manual"
	case "SORTKEY":
		cause = fmt.Errorf("rows sort by name, label, a count column or a text column, not %v", reply[2])
	case "SORTKEEP":
		cause = fmt.Errorf("a standing sort is by name or label; by %v the rows are sorted once, without --keep", reply[2])
		remedy = "nova-table row sort " + shellWord(o.table) + " --by " + shellWord(fmt.Sprint(reply[2]))
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
	if reason == "NOROW" && len(reply) >= 3 {
		o.row = fmt.Sprint(reply[2])
		remedy = "nova-table row add " + shellWord(o.table) + " " + shellWord(o.row)
	}
	return fmt.Errorf("%s: %w; run: %s", o.location(), cause, remedy)
}
func (o operation) call(ctx context.Context, c redis.Cmdable, fn string, ro bool, args ...any) ([]any, error) {
	all := append([]any{o.table}, args...)
	key := DefKey(o.table)
	if o.view {
		key = "view:" + o.table
	}
	if fn == "ns_view_list" {
		key = "views"
		all = nil
	}
	var cmd *redis.Cmd
	if ro {
		cmd = c.FCallRO(ctx, fn, []string{key}, all...)
	} else {
		cmd = c.FCall(ctx, fn, []string{key}, all...)
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
func (o operation) write(ctx context.Context, c redis.Cmdable, fn string, options []WriteOptions, args ...any) ([]any, error) {
	if len(options) > 1 {
		return nil, fmt.Errorf("%s: one write-options value is allowed", o.location())
	}
	var opts WriteOptions
	if len(options) == 1 {
		opts = options[0]
	}
	body, err := payload(struct {
		Epoch string `json:"epoch"`
		Actor string `json:"actor"`
		Fence string `json:"fence"`
		Idem  string `json:"idem"`
	}{strconv.FormatUint(opts.Epoch, 10), opts.Actor, opts.Fence, opts.Idem})
	if err != nil {
		return nil, err
	}
	reply, err := o.call(ctx, c, fn, false, append(args, body)...)
	if err != nil {
		return nil, err
	}
	if len(reply) < 2 {
		return nil, fmt.Errorf("%s: missing committed receipt", o.location())
	}
	wire, ok := reply[len(reply)-1].([]any)
	if !ok || len(wire) != 6 || fmt.Sprint(wire[0]) != "RECEIPT" {
		return nil, fmt.Errorf("%s: malformed committed receipt", o.location())
	}
	var r Receipt
	r.ID, r.Outcome = fmt.Sprint(wire[1]), fmt.Sprint(wire[5])
	for i, target := range []*uint64{&r.Epoch, &r.Before, &r.After} {
		*target, err = strconv.ParseUint(fmt.Sprint(wire[i+2]), 10, 64)
		if err != nil {
			return nil, err
		}
	}
	if opts.Receipt != nil {
		*opts.Receipt = r
	}
	return reply[:len(reply)-1], nil
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
func Create(ctx context.Context, c redis.Cmdable, t Table, now time.Time, opts ...WriteOptions) error {
	fields, err := definitionPayload(t, now)
	if err != nil {
		return err
	}
	body, err := payload(fields)
	if err != nil {
		return err
	}
	_, err = (operation{table: t.Name}).write(ctx, c, FnCreate, opts, body)
	return err
}

// SetOpts changes the active definition while retaining rows and members.
// A removed or hidden occupied owned cell is refused. Rename moves the table
// identity, all retained epochs and its change stream to the new name.
type SetOpts struct {
	Footer     *string
	Rename     string
	Columns    []Column
	Hidden     *[]string
	Hide, Show []string // atomic deltas to the hidden column list
	Visible    *bool
	// One column added (at the end, or where At says), removed (refused
	// while it holds members or text) or moved; the other columns stand.
	ColAdd  *Column
	ColAt   *Place // where ColAdd enters; nil is last
	ColDel  string
	ColMove *Reorder
	// The rows' order: all sorted, then the named rows first, then one row
	// moved, in that order when several are given. A standing sort refuses
	// RowOrder and RowMove unless RowSort clears it in the same call.
	RowSort  *Sort
	RowOrder []string
	RowMove  *Reorder
}

// Place is a position in an order: first, last, or before or after Ref.
type Place struct {
	Where string // first, last, before, after
	Ref   string // the neighbour, for before and after
}

// Reorder puts one column or one row at a place; nothing else moves.
type Reorder struct {
	Item string
	Place
}

// Sort orders the rows once by name, label, or a column's value (a count,
// or a text value). Keep makes it standing (name and label only): every
// row added or rebound later takes its place. Manual ends a standing sort and leaves
// the rows where they are.
type Sort struct {
	By     string
	Desc   bool
	Keep   bool
	Manual bool
}

func (p Place) valid() error {
	switch p.Where {
	case "first", "last":
		if p.Ref != "" {
			return fmt.Errorf("--%s takes no neighbour", p.Where)
		}
		return nil
	case "before", "after":
		if p.Ref == "" {
			return fmt.Errorf("--%s wants a neighbour", p.Where)
		}
		return nil
	}
	return fmt.Errorf("a place is --first, --last, --before <x> or --after <x>")
}

func (p Place) wire(m map[string]any) {
	m["where"] = p.Where
	if p.Ref != "" {
		m["ref"] = p.Ref
	}
}

// Set validates and commits a complete definition edit in one call. The
// return value counts physical keys moved when renaming, otherwise zero.
func Set(ctx context.Context, c redis.Cmdable, name string, change SetOpts, opts ...WriteOptions) (int, error) {
	spec := map[string]any{}
	if change.Footer != nil {
		spec["footer"] = *change.Footer
	}
	if change.Rename != "" {
		if !ValidName(change.Rename) {
			return 0, fmt.Errorf("table %q: the new name wants letters, digits, _ . and -", change.Rename)
		}
		spec["rename"] = change.Rename
	}
	if len(change.Columns) > 0 {
		if err := ValidateColumns(change.Columns); err != nil {
			return 0, err
		}
		fields := definitionFields(Table{Name: name, Columns: change.Columns})
		for key := range fields {
			if key != "order" && !strings.HasPrefix(key, "col:") {
				delete(fields, key)
			}
		}
		spec["columns"] = fields
	}
	if change.Hidden != nil {
		spec["hidden"] = strings.Join(*change.Hidden, ",")
	}
	if len(change.Hide) > 0 {
		spec["hide"] = change.Hide
	}
	if len(change.Show) > 0 {
		spec["show"] = change.Show
	}
	if change.Visible != nil {
		spec["visible"] = *change.Visible
	}
	if change.ColAdd != nil {
		if err := validateColumn(*change.ColAdd, nil); err != nil {
			return 0, err
		}
		add := map[string]any{"name": change.ColAdd.Name, "def": encodeColumn(*change.ColAdd)}
		if change.ColAt != nil {
			if err := change.ColAt.valid(); err != nil {
				return 0, err
			}
			change.ColAt.wire(add)
		}
		spec["col_add"] = add
	}
	if change.ColDel != "" {
		spec["col_del"] = change.ColDel
	}
	for key, m := range map[string]*Reorder{"col_move": change.ColMove, "row_move": change.RowMove} {
		if m == nil {
			continue
		}
		if err := m.valid(); err != nil {
			return 0, err
		}
		item := map[string]any{}
		if key == "col_move" {
			item["col"] = m.Item
		} else {
			item["row"] = m.Item
		}
		m.wire(item)
		spec[key] = item
	}
	if len(change.RowOrder) > 0 {
		spec["row_order"] = change.RowOrder
	}
	if change.RowSort != nil {
		spec["row_sort"] = map[string]any{"by": change.RowSort.By, "desc": change.RowSort.Desc, "keep": change.RowSort.Keep, "manual": change.RowSort.Manual}
	}
	if len(spec) == 0 {
		return 0, fmt.Errorf("table %q: set wants a change: --footer <label>, --rename <name>, --columns <spec>, --hide, --show, --hidden or --visible", name)
	}
	body, err := payload(spec)
	if err != nil {
		return 0, err
	}
	reply, err := (operation{table: name}).write(ctx, c, FnSet, opts, body)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

// RowSet writes text values as one validated mutation, with a single receipt.
func RowSet(ctx context.Context, c redis.Cmdable, name, key string, texts map[string]string, opts ...WriteOptions) (int, error) {
	if len(texts) == 0 {
		return 0, fmt.Errorf("table %q: row set wants at least one text value", name)
	}
	body, err := payload(texts)
	if err != nil {
		return 0, err
	}
	reply, err := (operation{table: name, row: key}).write(ctx, c, FnRowSet, opts, key, body)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

// Drop removes the active epoch and its owned cells. The template and older
// epochs survive; a later epoch starts with the same definition and no rows.
func Drop(ctx context.Context, c redis.Cmdable, name string, opts ...WriteOptions) (int, error) {
	reply, err := (operation{table: name}).write(ctx, c, FnDrop, opts)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

// DropDefinition also removes the stable template. Materialised epoch
// snapshots and immutable member identities remain available for inspection.
func DropDefinition(ctx context.Context, c redis.Cmdable, name string, opts ...WriteOptions) (int, error) {
	reply, err := (operation{table: name}).write(ctx, c, FnDropDefinition, opts)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

// MemberCreate allocates an unplaced identity in the table's record namespace.
// Existing IDs, including removed members and older epochs, are refused.
func MemberCreate(ctx context.Context, c redis.Cmdable, name, id string, opts ...WriteOptions) error {
	_, err := (operation{table: name, member: id}).write(ctx, c, FnMemberCreate, opts, id)
	return err
}

type RowSpec struct {
	Label   string            `json:"label"`
	Exclude string            `json:"exclude"`
	Owner   string            `json:"owner"`
	Binds   map[string]string `json:"binds,omitempty"`
}

func RowAdd(ctx context.Context, c redis.Cmdable, name, key string, spec RowSpec, opts ...WriteOptions) (Row, error) {
	o := operation{table: name, row: key}
	if !ValidRowKey(key) {
		return Row{}, fmt.Errorf("%s: row wants a non-empty key with no control characters; run: nova-table row help", o.location())
	}
	body, err := payload(spec)
	if err != nil {
		return Row{}, err
	}
	reply, err := o.write(ctx, c, FnRowAdd, opts, key, body)
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

// RowsAdd adds a list of rows using the same staged writer as RowAdd.
func RowsAdd(ctx context.Context, c redis.Cmdable, name string, keys []string, opts ...WriteOptions) (int, error) {
	return RowsAddWithSpec(ctx, c, name, keys, RowSpec{}, opts...)
}

// RowsAddWithSpec applies one metadata specification to every named row.
func RowsAddWithSpec(ctx context.Context, c redis.Cmdable, name string, keys []string, spec RowSpec, opts ...WriteOptions) (int, error) {
	body, err := payload(struct {
		Rows []string `json:"rows"`
		Spec RowSpec  `json:"spec"`
	}{keys, spec})
	if err != nil {
		return 0, err
	}
	reply, err := (operation{table: name}).write(ctx, c, FnRowsAdd, opts, body)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

// RowsHide hides rows from the render while retaining their contribution to folds.
func RowsHide(ctx context.Context, c redis.Cmdable, name string, hide bool, keys []string, opts ...WriteOptions) (int, error) {
	body, err := payload(keys)
	if err != nil {
		return 0, err
	}
	flag := "0"
	if hide {
		flag = "1"
	}
	reply, err := (operation{table: name}).write(ctx, c, FnRowsHide, opts, flag, body)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

func RowDel(ctx context.Context, c redis.Cmdable, name, key string, opts ...WriteOptions) (bool, error) {
	reply, err := (operation{table: name, row: key}).write(ctx, c, FnRowDel, opts, key)
	if err != nil {
		return false, err
	}
	n, err := replyCount(reply)
	return n != 0, err
}

// Bind replaces the caller-owned table shape in one atomic call. Existing
// owned cells of retained rows survive; removed rows leave bound sets alone.
// Omitting a row holding owned members or nonempty text is refused until the
// caller explicitly clears that content (or deletes the row).
func Bind(ctx context.Context, c redis.Cmdable, t Table, now time.Time, opts ...WriteOptions) error {
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
	_, err = (operation{table: t.Name}).write(ctx, c, FnBind, opts, body)
	return err
}
func Clear(ctx context.Context, c redis.Cmdable, name string, opts ...WriteOptions) (int64, error) {
	reply, err := (operation{table: name}).write(ctx, c, FnClear, opts)
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}

// Single-member helpers use the same list operation and wire protocol.
func CellAdd(ctx context.Context, c redis.Cmdable, name, row, col, member string, score float64, opts ...WriteOptions) (int64, error) {
	return CellsAdd(ctx, c, name, row, col, score, []string{member}, opts...)
}
func CellRemove(ctx context.Context, c redis.Cmdable, name, row, col, member string, opts ...WriteOptions) (int64, error) {
	return CellsRemove(ctx, c, name, row, col, []string{member}, opts...)
}
func CellMove(ctx context.Context, c redis.Cmdable, name, row, from, to, member string, opts ...WriteOptions) (int64, error) {
	return CellsMove(ctx, c, name, row, from, to, []string{member}, opts...)
}

// CellsAdd atomically adds every member at one score and returns the cell count.
func CellsAdd(ctx context.Context, c redis.Cmdable, name, row, col string, score float64, members []string, opts ...WriteOptions) (int64, error) {
	return writeMembers(ctx, c, operation{table: name, row: row, col: col}, FnCellAdd, opts, []any{row, col, strconv.FormatFloat(score, 'g', -1, 64)}, members)
}

// CellsRemove removes a list; absent members are accepted no-ops.
func CellsRemove(ctx context.Context, c redis.Cmdable, name, row, col string, members []string, opts ...WriteOptions) (int64, error) {
	return writeMembers(ctx, c, operation{table: name, row: row, col: col}, FnCellRemove, opts, []any{row, col}, members)
}

// CellsMove keeps scores and refuses the complete list if any member is absent or inconsistent.
func CellsMove(ctx context.Context, c redis.Cmdable, name, row, from, to string, members []string, opts ...WriteOptions) (int64, error) {
	return writeMembers(ctx, c, operation{table: name, row: row, col: from}, FnCellMove, opts, []any{row, from, to}, members)
}
func writeMembers(ctx context.Context, c redis.Cmdable, o operation, fn string, opts []WriteOptions, args []any, members []string) (int64, error) {
	if len(members) == 0 {
		return 0, fmt.Errorf("%s: wants at least one member", o.location())
	}
	o.member = members[0]
	for _, m := range members {
		args = append(args, m)
	}
	reply, err := o.write(ctx, c, fn, opts, args...)
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

// A view: a named list of tables with a title, read by watch every frame.
type View struct {
	Name    string
	Tables  []string
	Title   string
	Summary string // the count column of the first table the summary line counts as done ("" for no line)
}

// ViewSet writes a view; every table must exist.
func ViewSet(ctx context.Context, c redis.Cmdable, v View) error {
	_, err := (operation{table: v.Name, view: true}).call(ctx, c, "ns_view_set", false, strings.Join(v.Tables, ","), v.Title, v.Summary)
	return err
}

// ViewGet reads a view.
func ViewGet(ctx context.Context, c redis.Cmdable, name string) (View, error) {
	reply, err := (operation{table: name, view: true}).call(ctx, c, "ns_view_get", true)
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

// ViewList returns stored view names in lexical order, in one exchange.
func ViewList(ctx context.Context, c redis.Cmdable) ([]string, error) {
	reply, err := (operation{view: true}).call(ctx, c, "ns_view_list", true)
	if err != nil {
		return nil, err
	}
	if len(reply) != 2 {
		return nil, fmt.Errorf("views: malformed list reply")
	}
	raw, ok := reply[1].([]any)
	if !ok {
		return nil, fmt.Errorf("views: malformed names")
	}
	names := make([]string, len(raw))
	for i, v := range raw {
		name, ok := v.(string)
		if !ok || !ValidName(name) {
			return nil, fmt.Errorf("views: malformed name")
		}
		names[i] = name
	}
	return names, nil
}

// ViewDelete removes only presentation configuration; tables are untouched.
func ViewDelete(ctx context.Context, c redis.Cmdable, name string) (int64, error) {
	reply, err := (operation{table: name, view: true}).call(ctx, c, "ns_view_del", false)
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}
