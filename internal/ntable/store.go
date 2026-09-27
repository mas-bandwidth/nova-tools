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
	FnDrop           = "ns_table_drop"
	FnDropDefinition = "ns_table_drop_definition"
	FnMemberCreate   = "ns_table_member_create"
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
func CellAdd(ctx context.Context, c redis.Cmdable, name, row, col, member string, score float64, opts ...WriteOptions) (int64, error) {
	reply, err := (operation{name, row, col, member}).write(ctx, c, FnCellAdd, opts, row, col, member, strconv.FormatFloat(score, 'g', -1, 64))
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}
func CellRemove(ctx context.Context, c redis.Cmdable, name, row, col, member string, opts ...WriteOptions) (int64, error) {
	reply, err := (operation{name, row, col, member}).write(ctx, c, FnCellRemove, opts, row, col, member)
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}
func CellMove(ctx context.Context, c redis.Cmdable, name, row, from, to, member string, opts ...WriteOptions) (int64, error) {
	reply, err := (operation{name, row, from, member}).write(ctx, c, FnCellMove, opts, row, from, member, to)
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
