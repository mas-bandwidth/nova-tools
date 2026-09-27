package ntable

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// FnClear is the library function Clear calls: every owned cell of the
// table emptied and every row removed in one call; refused BOUND when any
// cell is bound (internal/nsprint/fn/lua/table.lua).
const FnClear = "ns_table_clear"

// ErrExists is Create's refusal: the table exists with another definition.
var ErrExists = errors.New("exists with another definition")

// BoundError is the refusal of a write to a bound cell, or of a clear over
// a table holding one: the set is owned elsewhere, and Owner names the verb
// that writes it (Glenn's one-writer principle).
type BoundError struct {
	Table, Row, Col, Key, Owner string
}

func (e *BoundError) Error() string {
	s := fmt.Sprintf("%s.%s.%s is bound to %s, owned elsewhere", e.Table, e.Row, e.Col, e.Key)
	if e.Owner != "" {
		s += "; run: " + e.Owner
	}
	return s
}

// Create defines the table: its columns, its footer label and the
// registry membership, in one pipeline after one read. An existing table
// with the same definition is left as it is; one with another is refused
// with ErrExists.
func Create(ctx context.Context, c redis.Cmdable, t Table, now time.Time) error {
	if !ValidName(t.Name) {
		return fmt.Errorf("table %q wants a name of letters, digits, _ . and -", t.Name)
	}
	if err := ValidateColumns(t.Columns); err != nil {
		return err
	}
	h, err := c.HGetAll(ctx, DefKey(t.Name)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("hgetall %s: %w", DefKey(t.Name), err)
	}
	if have, ok, err := decodeDefinition(t.Name, h); err != nil {
		return err
	} else if ok {
		if !SameDefinition(have, t) {
			return fmt.Errorf("table %s: %w", t.Name, ErrExists)
		}
		return nil
	}
	fields := definitionFields(t)
	fields["created_at"] = now.UTC().Format(time.RFC3339)
	pipe := c.Pipeline()
	pipe.HSet(ctx, DefKey(t.Name), fields)
	pipe.SAdd(ctx, Registry, t.Name)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("create table %s: %w", t.Name, err)
	}
	return nil
}

// Drop deletes the table: its definition, its row order, every row hash,
// every owned cell and its registry membership. A bound cell's set is left
// where it is. It returns the rows it dropped.
func Drop(ctx context.Context, c redis.Cmdable, name string) (int, error) {
	t, err := Shape(ctx, c, name)
	if err != nil {
		return 0, err
	}
	pipe := c.Pipeline()
	keys := []string{DefKey(name), RowsKey(name)}
	for _, r := range t.Rows {
		keys = append(keys, RowKey(name, r.Key))
		for _, cell := range r.Cells {
			if cell.Key != "" && !cell.Bound {
				keys = append(keys, cell.Key)
			}
		}
	}
	pipe.Del(ctx, keys...)
	pipe.SRem(ctx, Registry, name)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("drop table %s: %w", name, err)
	}
	return len(t.Rows), nil
}

// RowSpec is what RowAdd is told about a row: its label, the member its
// counts leave out, the bindings (column -> the set another tool owns) and
// the verb that owns them.
type RowSpec struct {
	Label   string
	Exclude string
	Owner   string
	Binds   map[string]string
}

// RowAdd puts a row in the table: its hash written whole and its place in
// the row order taken after the last row (an existing row keeps its place
// and its cells). Every bound column must be one the table defines and not
// a text column.
func RowAdd(ctx context.Context, c redis.Cmdable, name, key string, spec RowSpec) (Row, error) {
	t, err := Shape(ctx, c, name)
	if err != nil {
		return Row{}, err
	}
	if !ValidRowKey(key) {
		return Row{}, fmt.Errorf("row %q wants a non-empty key with no control character", key)
	}
	row, err := specRow(t, key, spec)
	if err != nil {
		return Row{}, err
	}
	rank := float64(len(t.Rows) + 1)
	if i := t.Row(key); i >= 0 {
		rank = float64(i + 1)
	}
	if err := writeRows(ctx, c, t, []Row{row}, []float64{rank}, nil, false); err != nil {
		return Row{}, err
	}
	return row, nil
}

// specRow builds the row spec describes, checked against t.
func specRow(t Table, key string, spec RowSpec) (Row, error) {
	row := NewRow(t, key)
	row.Label, row.Exclude, row.Owner = spec.Label, spec.Exclude, spec.Owner
	for col, k := range spec.Binds {
		j := t.Column(col)
		if j < 0 {
			return Row{}, fmt.Errorf("table %s has no column %s to bind", t.Name, col)
		}
		if t.Columns[j].Projection == Text {
			return Row{}, fmt.Errorf("column %s is a text column and binds no set", col)
		}
		if k == "" {
			return Row{}, fmt.Errorf("column %s wants a key to bind to", col)
		}
		row.Cells[j] = Cell{Key: k, Bound: true}
	}
	return row, nil
}

// writeRows is one pipeline: each row's hash deleted and written whole and
// its rank set (NX unless rerank), and each gone row's hash, owned cells
// and place removed.
func writeRows(ctx context.Context, c redis.Cmdable, t Table, rows []Row, ranks []float64, gone []Row, rerank bool) error {
	pipe := c.Pipeline()
	for i, r := range rows {
		pipe.Del(ctx, RowKey(t.Name, r.Key))
		if fields := rowFields(t, r); len(fields) > 0 {
			pipe.HSet(ctx, RowKey(t.Name, r.Key), fields)
		}
		z := redis.Z{Score: ranks[i], Member: r.Key}
		if rerank {
			pipe.ZAdd(ctx, RowsKey(t.Name), z)
		} else {
			pipe.ZAddNX(ctx, RowsKey(t.Name), z)
		}
	}
	for _, r := range gone {
		keys := []string{RowKey(t.Name, r.Key)}
		for _, cell := range r.Cells {
			if cell.Key != "" && !cell.Bound {
				keys = append(keys, cell.Key)
			}
		}
		pipe.Del(ctx, keys...)
		pipe.ZRem(ctx, RowsKey(t.Name), r.Key)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("write table %s rows: %w", t.Name, err)
	}
	return nil
}

// RowDel takes a row out of the table: its hash, its owned cells and its
// place. A row the table does not hold is not an error; it returns whether
// one was there.
func RowDel(ctx context.Context, c redis.Cmdable, name, key string) (bool, error) {
	t, err := Shape(ctx, c, name)
	if err != nil {
		return false, err
	}
	i := t.Row(key)
	if i < 0 {
		return false, nil
	}
	if err := writeRows(ctx, c, t, nil, nil, []Row{t.Rows[i]}, false); err != nil {
		return false, err
	}
	return true, nil
}

// Bind makes the store's table the one the caller holds: the definition
// created when absent (refused when it exists with another), every row
// written whole in the given order with its bindings, and every row the
// store holds that the caller does not removed. It is the writer's one
// verb for a table whose rows are a view of sets owned elsewhere (the
// sprint's streams), called only when the shape moved: one read and one
// pipeline of writes.
func Bind(ctx context.Context, c redis.Cmdable, t Table, now time.Time) error {
	if err := Create(ctx, c, t, now); err != nil {
		return err
	}
	have, err := Shape(ctx, c, t.Name)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	ranks := make([]float64, len(t.Rows))
	for i, r := range t.Rows {
		if !ValidRowKey(r.Key) {
			return fmt.Errorf("row %q wants a non-empty key with no control character", r.Key)
		}
		if len(r.Cells) != len(t.Columns) {
			return fmt.Errorf("row %s has %d cells for %d columns", r.Key, len(r.Cells), len(t.Columns))
		}
		keep[r.Key] = true
		ranks[i] = float64(i + 1)
	}
	var gone []Row
	for _, r := range have.Rows {
		if !keep[r.Key] {
			gone = append(gone, r)
		}
	}
	return writeRows(ctx, c, t, t.Rows, ranks, gone, true)
}

// Clear empties every owned cell of the table and removes every row, in one
// library call; the definition stays. A table with a bound cell is refused
// whole (BoundError) and nothing is cleared. It returns the rows removed.
func Clear(ctx context.Context, c redis.Cmdable, name string) (int64, error) {
	reply, err := c.FCall(ctx, FnClear, []string{DefKey(name)}, name).Slice()
	if err != nil {
		return 0, fmt.Errorf("%s %s: %w", FnClear, name, err)
	}
	if len(reply) >= 2 && fmt.Sprint(reply[0]) == "REFUSED" {
		switch fmt.Sprint(reply[1]) {
		case "NOTABLE":
			return 0, fmt.Errorf("table %s: %w", name, ErrNoTable)
		case "BOUND":
			e := &BoundError{Table: name}
			if len(reply) >= 6 {
				e.Row, e.Col, e.Key, e.Owner = fmt.Sprint(reply[2]), fmt.Sprint(reply[3]), fmt.Sprint(reply[4]), fmt.Sprint(reply[5])
			}
			return 0, e
		}
		return 0, fmt.Errorf("%s: REFUSED %v", FnClear, reply[1])
	}
	if len(reply) < 2 {
		return 0, fmt.Errorf("%s: short reply %v", FnClear, reply)
	}
	var n int64
	switch v := reply[1].(type) {
	case int64:
		n = v
	default:
		if _, err := fmt.Sscan(fmt.Sprint(v), &n); err != nil {
			return 0, fmt.Errorf("%s: rows %v is not a number", FnClear, v)
		}
	}
	return n, nil
}

// cellOf finds a row's cell in the table's shape, refusing a bound one
// for a write.
func cellOf(ctx context.Context, c redis.Cmdable, name, row, col string, write bool) (Table, Cell, error) {
	t, err := Shape(ctx, c, name)
	if err != nil {
		return Table{}, Cell{}, err
	}
	i, j := t.Row(row), t.Column(col)
	if i < 0 {
		return Table{}, Cell{}, fmt.Errorf("table %s has no row %s", name, row)
	}
	if j < 0 {
		return Table{}, Cell{}, fmt.Errorf("table %s has no column %s", name, col)
	}
	if t.Columns[j].Projection == Text {
		return Table{}, Cell{}, fmt.Errorf("column %s is a text column and holds no set", col)
	}
	cell := t.Rows[i].Cells[j]
	if write && cell.Bound {
		return Table{}, Cell{}, &BoundError{Table: name, Row: row, Col: col, Key: cell.Key, Owner: t.Rows[i].Owner}
	}
	return t, cell, nil
}

// CellAdd puts member in the owned cell at row, col and returns the cell's
// count after; a bound cell is refused (BoundError).
func CellAdd(ctx context.Context, c redis.Cmdable, name, row, col, member string, score float64) (int64, error) {
	_, cell, err := cellOf(ctx, c, name, row, col, true)
	if err != nil {
		return 0, err
	}
	if err := Add(ctx, c, cell.Key, member, score); err != nil {
		return 0, err
	}
	return Card(ctx, c, cell.Key)
}

// CellRemove takes member out of the owned cell at row, col and returns
// the cell's count after; a bound cell is refused (BoundError).
func CellRemove(ctx context.Context, c redis.Cmdable, name, row, col, member string) (int64, error) {
	_, cell, err := cellOf(ctx, c, name, row, col, true)
	if err != nil {
		return 0, err
	}
	if err := Remove(ctx, c, cell.Key, member); err != nil {
		return 0, err
	}
	return Card(ctx, c, cell.Key)
}

// CellMove moves member from the owned cell at row, from to the owned cell
// at row, to, keeping its score, in one library call; either cell bound is
// refused (BoundError), and a member not in from is ErrNotMember. It
// returns the count of to after.
func CellMove(ctx context.Context, c redis.Cmdable, name, row, from, to, member string) (int64, error) {
	_, src, err := cellOf(ctx, c, name, row, from, true)
	if err != nil {
		return 0, err
	}
	_, dst, err := cellOf(ctx, c, name, row, to, true)
	if err != nil {
		return 0, err
	}
	if err := Move(ctx, c, src.Key, dst.Key, member, true, 0); err != nil {
		return 0, err
	}
	return Card(ctx, c, dst.Key)
}

// CellMembers is every member of the cell at row, col, bound or owned, the
// row's excluded member left out.
func CellMembers(ctx context.Context, c redis.Cmdable, name, row, col string) ([]Member, error) {
	t, cell, err := cellOf(ctx, c, name, row, col, false)
	if err != nil {
		return nil, err
	}
	ms, err := MembersOf(ctx, c, cell.Key)
	if err != nil {
		return nil, err
	}
	exclude := t.Rows[t.Row(row)].Exclude
	if exclude == "" {
		return ms, nil
	}
	kept := ms[:0]
	for _, m := range ms {
		if m.Member != exclude {
			kept = append(kept, m)
		}
	}
	return kept, nil
}
