package ntable

import (
	"context"
	"errors"
	"fmt"
	"strconv"

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
			if j >= len(r.Cells) || !c.HasSet() {
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

// Reader queues a read-only server snapshot, even on the first tick or a
// changed shape. QueueCells remains available for callers holding a shape.
type Reader struct{ Name string }

func NewReader(name string) *Reader { return &Reader{Name: name} }

type ReadCmd struct {
	name string
	cmd  *redis.Cmd
}

func (r *Reader) Queue(ctx context.Context, pipe redis.Pipeliner) *ReadCmd {
	return &ReadCmd{r.Name, pipe.FCallRO(ctx, FnRead, []string{DefKey(r.Name)}, r.Name, "read")}
}

var ErrNoTable = errors.New("no such table")

// Result's changed value is retained for source compatibility and is always
// false: the function returns one consistent shape and its cells together.
func (q *ReadCmd) Result() (Table, bool, error) {
	reply, err := q.cmd.Slice()
	if err != nil {
		return Table{}, false, fmt.Errorf("read table %q: %w", q.name, err)
	}
	t, err := decodeSnapshot(q.name, reply)
	return t, false, err
}
func Read(ctx context.Context, c redis.Cmdable, name string) (Table, error) {
	return NewReader(name).Read(ctx, c)
}

// ReadAt inspects one materialised epoch, including after its template was removed.
func ReadAt(ctx context.Context, c redis.Cmdable, name string, epoch uint64) (Table, error) {
	reply, err := (operation{table: name}).call(ctx, c, FnRead, true, "read", strconv.FormatUint(epoch, 10))
	if err != nil {
		return Table{}, err
	}
	return decodeSnapshot(name, reply)
}
func (r *Reader) Read(ctx context.Context, c redis.Cmdable) (Table, error) {
	reply, err := (operation{table: r.Name}).call(ctx, c, FnRead, true, "read")
	if err != nil {
		return Table{}, err
	}
	return decodeSnapshot(r.Name, reply)
}
func Shape(ctx context.Context, c redis.Cmdable, name string) (Table, error) {
	reply, err := (operation{table: name}).call(ctx, c, FnRead, true, "shape")
	if err != nil {
		return Table{}, err
	}
	return decodeSnapshot(name, reply)
}
func flatHash(raw any) (map[string]string, error) {
	a, ok := raw.([]any)
	if !ok || len(a)%2 != 0 {
		return nil, fmt.Errorf("table function returned malformed hash: %T", raw)
	}
	h := map[string]string{}
	for i := 0; i < len(a); i += 2 {
		h[fmt.Sprint(a[i])] = fmt.Sprint(a[i+1])
	}
	return h, nil
}
func flatMembers(raw any) ([]Member, error) {
	a, ok := raw.([]any)
	if !ok || len(a)%2 != 0 {
		return nil, fmt.Errorf("table function returned malformed members: %T", raw)
	}
	ms := make([]Member, 0, len(a)/2)
	for i := 0; i < len(a); i += 2 {
		score, err := strconv.ParseFloat(fmt.Sprint(a[i+1]), 64)
		if err != nil {
			return nil, err
		}
		ms = append(ms, Member{fmt.Sprint(a[i]), score})
	}
	return ms, nil
}
func decodeSnapshot(name string, reply []any) (Table, error) {
	if err := (operation{table: name}).refused(reply); err != nil {
		return Table{}, err
	}
	if len(reply) != 3 || fmt.Sprint(reply[0]) != "TABLE" {
		return Table{}, fmt.Errorf("table %q: malformed snapshot", name)
	}
	h, err := flatHash(reply[1])
	if err != nil {
		return Table{}, err
	}
	t, ok, err := decodeDefinition(name, h)
	if err != nil {
		return Table{}, err
	}
	if !ok {
		return Table{}, ErrNoTable
	}
	rows, ok := reply[2].([]any)
	if !ok {
		return Table{}, fmt.Errorf("table %q: malformed rows", name)
	}
	for _, raw := range rows {
		a, ok := raw.([]any)
		if !ok || len(a) != 3 {
			return Table{}, fmt.Errorf("table %q: malformed row", name)
		}
		h, err := flatHash(a[1])
		if err != nil {
			return Table{}, err
		}
		row := decodeRow(t, fmt.Sprint(a[0]), h)
		cells, ok := a[2].([]any)
		if !ok || (len(cells) != 0 && len(cells) != len(t.Columns)) {
			return Table{}, fmt.Errorf("table %q row %q: malformed cells", name, row.Key)
		}
		for j, raw := range cells {
			cell, ok := raw.([]any)
			if !ok || len(cell) < 2 {
				return Table{}, fmt.Errorf("table %q row %q: malformed cell", name, row.Key)
			}
			if fmt.Sprint(cell[0]) == "UNREAD" {
				row.Cells[j].Unread = true
				continue
			}
			if len(cell) != 3 {
				return Table{}, fmt.Errorf("table %q row %q: malformed cell values", name, row.Key)
			}
			n, err := strconv.ParseInt(fmt.Sprint(cell[1]), 10, 64)
			if err != nil {
				return Table{}, err
			}
			row.Cells[j].Count = n
			row.Cells[j].Members, err = flatMembers(cell[2])
			if err != nil {
				return Table{}, err
			}
		}
		t.Rows = append(t.Rows, row)
	}
	return t, nil
}

// Summary is a table's definition width and row count, read with the registry.
type Summary struct {
	Name          string
	Columns, Rows int64
}

func Summaries(ctx context.Context, c redis.Cmdable) ([]Summary, error) {
	reply, err := c.FCallRO(ctx, FnList, []string{Registry}).Slice()
	if err != nil {
		return nil, fmt.Errorf("list tables: %w; run: nova-table help", err)
	}
	if err := (operation{table: "tables registry"}).refused(reply); err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(reply)-1)
	for _, raw := range reply[1:] {
		a, ok := raw.([]any)
		if !ok || len(a) != 3 {
			return nil, fmt.Errorf("table list: malformed entry")
		}
		cols, err := strconv.ParseInt(fmt.Sprint(a[1]), 10, 64)
		if err != nil {
			return nil, err
		}
		rows, err := strconv.ParseInt(fmt.Sprint(a[2]), 10, 64)
		if err != nil {
			return nil, err
		}
		out = append(out, Summary{fmt.Sprint(a[0]), cols, rows})
	}
	return out, nil
}
func List(ctx context.Context, c redis.Cmdable) ([]string, error) {
	rows, err := Summaries(ctx, c)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	return names, nil
}
