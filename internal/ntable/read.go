package ntable

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/redis/go-redis/v9"
)

// CellsCmd is one queued read of every body cell of a table the caller
// holds in memory: the ZCARD (and the excluded member's ZSCORE) of every
// count cell, the ZRANGE of every members, first and last cell. Result
// fills the table's cells in place.
type CellsCmd struct {
	t      *Table
	counts map[[2]int]*CountCmd
	lists  map[[2]int]*membersCmd
}

// QueueCells queues the cells of t on pipe: one command per count cell (two
// with a row exclude), one per members, first or last cell, none for a text
// column. It is how a caller that already knows a table's shape (the sprint
// tick, whose stream rows are ws:order) reads every cell in its own one
// pipeline.
func QueueCells(ctx context.Context, pipe redis.Pipeliner, t *Table) *CellsCmd {
	q := &CellsCmd{t: t, counts: map[[2]int]*CountCmd{}, lists: map[[2]int]*membersCmd{}}
	for i := range t.Rows {
		r := &t.Rows[i]
		for j, c := range t.Columns {
			if j >= len(r.Cells) || c.Projection == Text {
				continue
			}
			key := r.Cells[j].Key
			switch c.Projection {
			case Count:
				q.counts[[2]int{i, j}] = QueueCount(ctx, pipe, key, r.Exclude)
			default:
				q.lists[[2]int{i, j}] = queueMembers(ctx, pipe, key, r.Exclude)
			}
		}
	}
	return q
}

// Result fills every queued cell: Count and Members from the store, Unread
// for a cell whose read did not come back (it prints "?", never a false 0).
func (q *CellsCmd) Result() {
	for at, cmd := range q.counts {
		cell := &q.t.Rows[at[0]].Cells[at[1]]
		n, err := cmd.Result()
		cell.Count, cell.Unread, cell.Members = n, err != nil, nil
	}
	for at, cmd := range q.lists {
		cell := &q.t.Rows[at[0]].Cells[at[1]]
		ms, err := cmd.result()
		cell.Members, cell.Unread, cell.Count = ms, err != nil, int64(len(ms))
	}
}

// Reader reads a whole table from the store, keeping its shape (the
// definition, the row order and every row's bindings) across reads so a
// reader that reads again (the watch verb's tick) takes one pipeline: the
// shape is re-read in the same pipeline as the cells, and a read that finds
// it moved reads again with the new shape, the way the sprint table's tick
// reads its memberships.
type Reader struct {
	Name   string
	shape  Table
	primed bool
}

// NewReader is a reader of the named table with no shape yet: its first
// Read takes two round trips (the definition and the row order, then the
// row hashes with the cells of the shape they imply), three when a row
// hash binds a cell elsewhere.
func NewReader(name string) *Reader { return &Reader{Name: name} }

// ReadCmd is one queued read.
type ReadCmd struct {
	r     *Reader
	def   *redis.MapStringStringCmd
	rows  *redis.StringSliceCmd
	hash  []*redis.MapStringStringCmd // per cached row
	cells *CellsCmd
	t     Table // the cached shape the cells were queued for
}

// Queue queues the read on pipe: the definition, the row order, the row
// hashes of the rows the last read found, and the cells of the shape the
// last read built.
func (r *Reader) Queue(ctx context.Context, pipe redis.Pipeliner) *ReadCmd {
	q := &ReadCmd{r: r, def: pipe.HGetAll(ctx, DefKey(r.Name)), rows: pipe.ZRange(ctx, RowsKey(r.Name), 0, -1)}
	q.t = cloneTable(r.shape)
	for _, row := range q.t.Rows {
		q.hash = append(q.hash, pipe.HGetAll(ctx, RowKey(r.Name, row.Key)))
	}
	q.cells = QueueCells(ctx, pipe, &q.t)
	return q
}

// ErrNoTable is a read of a table the store does not define.
var ErrNoTable = errors.New("no such table")

// Result is the table once the pipeline ran. changed says the shape moved
// under the read (or it was the first): the cells are not the shape's and
// the caller reads again, which queues the new shape. ErrNoTable when the
// store defines no such table.
func (q *ReadCmd) Result() (Table, bool, error) {
	r := q.r
	h, err := q.def.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Table{}, false, fmt.Errorf("hgetall %s: %w", DefKey(r.Name), err)
	}
	def, ok, err := decodeDefinition(r.Name, h)
	if err != nil {
		return Table{}, false, err
	}
	if !ok {
		r.primed, r.shape = false, Table{}
		return Table{}, false, fmt.Errorf("table %s: %w", r.Name, ErrNoTable)
	}
	keys, err := q.rows.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Table{}, false, fmt.Errorf("zrange %s: %w", RowsKey(r.Name), err)
	}
	// the shape as this read found it: the definition, the rows, and the
	// hashes of the rows the last read knew (a new row's hash is read next
	// time, under the new shape)
	found := Table{Name: r.Name, Columns: def.Columns, FooterLabel: def.FooterLabel}
	known := map[string]map[string]string{}
	for i, row := range q.t.Rows {
		hh, err := q.hash[i].Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return Table{}, false, fmt.Errorf("hgetall %s: %w", RowKey(r.Name, row.Key), err)
		}
		known[row.Key] = hh
	}
	complete := true
	for _, key := range keys {
		hh, ok := known[key]
		if !ok {
			complete = false
		}
		found.Rows = append(found.Rows, decodeRow(found, key, hh))
	}
	changed := !r.primed || !complete || !SameShape(found, q.t)
	r.primed = true
	if changed {
		r.shape = found
		return Table{}, true, nil
	}
	q.cells.Result()
	return q.t, false, nil
}

// Read is one read of the named table: a pipeline, again while the shape
// moves (at most four).
func Read(ctx context.Context, c redis.Cmdable, name string) (Table, error) {
	return NewReader(name).Read(ctx, c)
}

// Read is one read: a pipeline, again while the shape moves (at most four
// from cold: the definition and rows, the hashes with the implied cells,
// the cells of a bound shape, and one for a shape that moved under the
// read).
func (r *Reader) Read(ctx context.Context, c redis.Cmdable) (Table, error) {
	for trip := 0; trip < 4; trip++ {
		pipe := c.Pipeline()
		q := r.Queue(ctx, pipe)
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
			return Table{}, fmt.Errorf("read table %s: %w", r.Name, err)
		}
		t, changed, err := q.Result()
		if err != nil {
			return Table{}, err
		}
		if !changed {
			return t, nil
		}
	}
	return Table{}, fmt.Errorf("read table %s: its shape changed on four reads in a row", r.Name)
}

func isReplyError(err error) bool {
	var re redis.Error
	return errors.As(err, &re)
}

// cloneTable copies a table's shape (the cells' values are read anew).
func cloneTable(t Table) Table {
	out := Table{Name: t.Name, Columns: slices.Clone(t.Columns), FooterLabel: t.FooterLabel, Rows: make([]Row, len(t.Rows))}
	for i, r := range t.Rows {
		out.Rows[i] = Row{Key: r.Key, Label: r.Label, Exclude: r.Exclude, Owner: r.Owner, Cells: make([]Cell, len(r.Cells))}
		for j, c := range r.Cells {
			out.Rows[i].Cells[j] = Cell{Key: c.Key, Bound: c.Bound}
		}
	}
	return out
}

// List is every table's name, sorted, from the registry set.
func List(ctx context.Context, c redis.Cmdable) ([]string, error) {
	names, err := c.SMembers(ctx, Registry).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("smembers %s: %w", Registry, err)
	}
	sort.Strings(names)
	return names, nil
}

// Shape reads a table's definition, row order and bindings, with no cell
// read: two round trips.
func Shape(ctx context.Context, c redis.Cmdable, name string) (Table, error) {
	pipe := c.Pipeline()
	def := pipe.HGetAll(ctx, DefKey(name))
	rows := pipe.ZRange(ctx, RowsKey(name), 0, -1)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
		return Table{}, fmt.Errorf("read table %s: %w", name, err)
	}
	h, err := def.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Table{}, fmt.Errorf("hgetall %s: %w", DefKey(name), err)
	}
	t, ok, err := decodeDefinition(name, h)
	if err != nil {
		return Table{}, err
	}
	if !ok {
		return Table{}, fmt.Errorf("table %s: %w", name, ErrNoTable)
	}
	keys, err := rows.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Table{}, fmt.Errorf("zrange %s: %w", RowsKey(name), err)
	}
	if len(keys) == 0 {
		return t, nil
	}
	pipe = c.Pipeline()
	hashes := make([]*redis.MapStringStringCmd, len(keys))
	for i, key := range keys {
		hashes[i] = pipe.HGetAll(ctx, RowKey(name, key))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
		return Table{}, fmt.Errorf("read table %s rows: %w", name, err)
	}
	for i, key := range keys {
		hh, err := hashes[i].Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return Table{}, fmt.Errorf("hgetall %s: %w", RowKey(name, key), err)
		}
		t.Rows = append(t.Rows, decodeRow(t, key, hh))
	}
	return t, nil
}
