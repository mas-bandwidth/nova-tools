package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// errMoved is a read that saw a table change between its exchanges.
var errMoved = errors.New("a table changed while it was read")

// movedError is errMoved in a load, naming the table.
type movedError struct{ table string }

func (e *movedError) Unwrap() error { return errMoved }

func (e *movedError) Error() string { return "table " + e.table + " changed while it was read" }

// Load is the set read of a step: the tables' shapes (one exchange), their
// cells' member ids (one exchange), every member's place, score, revision and
// fields (one read set per ntable.LimitReadSetMembers members), and the open
// judgments. A table's read sets must all see the revision its shape saw, or
// the read is taken again, after a jittered wait, up to LoadTries reads and
// RetryBudget asleep: the snapshot is one consistent state of each table.
// extras names records to read as well (unplaced ones included), by table.
func (st *Store) Load(ctx context.Context, tables []string, extras func(*sprint.Snapshot) map[string][]string) (*sprint.Snapshot, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	var last *movedError
	r := st.retry(ctx)
	for r.next(LoadTries) {
		s, err := st.loadOnce(ctx, tables, extras)
		var moved *movedError
		if errors.As(err, &moved) {
			last = moved
			continue
		}
		return s, err
	}
	return nil, fmt.Errorf("the tables are busy: table %s kept changing while it was read, %d reads in %s; nothing was changed; run the verb again",
		last.table, r.tries, r.slept().Round(time.Millisecond))
}

func (st *Store) loadOnce(ctx context.Context, tables []string, extras func(*sprint.Snapshot) map[string][]string) (*sprint.Snapshot, error) {
	s := &sprint.Snapshot{Now: st.now(), Epoch: st.epoch, Cleared: st.cleared}
	stored := make([]string, len(tables))
	for i, t := range tables {
		stored[i] = st.Names.Table(t)
	}
	shapes, err := st.shapes(ctx, stored)
	if err != nil {
		return nil, err
	}
	if len(tables) > 0 {
		st.stats().reads.Add(1)
	}
	ids, err := st.B.CellIDs(ctx, shapes)
	if err != nil {
		return nil, err
	}
	for i, shape := range shapes {
		if st.pinned && shape.Epoch != st.epoch {
			return nil, errCleared
		}
		t := sprint.NewTable(tables[i])
		t.Epoch, t.Revision = shape.Epoch, shape.Revision
		t.SetProps(shape.Props)
		for _, r := range shape.Rows {
			t.SetRows(append(t.Rows(), r.Key))
			if len(r.Texts) > 0 {
				t.Texts[r.Key] = r.Texts
			}
		}
		if err := st.readInto(ctx, t, ids[shape.Name], true); err != nil {
			return nil, err
		}
		switch tables[i] {
		case sprint.Work:
			s.Work = t
		case sprint.Readers:
			s.Readers = t
		case sprint.Merge:
			s.Merge = t
		case sprint.Fleet:
			s.Fleet = t
		}
	}
	open, err := st.B.OpenNotes(ctx)
	if err != nil {
		return nil, err
	}
	s.Open, s.Acked = sprint.SplitOpen(open)
	s.Actor = st.Actor
	if s.Coordinator, err = st.B.Coordinator(ctx); err != nil {
		return nil, err
	}
	if extras != nil {
		for table, want := range extras(s) {
			t := s.T(table)
			var missing []string
			for _, id := range want {
				if t.Card(id) == nil {
					missing = append(missing, id)
				}
			}
			if err := st.readInto(ctx, t, st.sids(missing), false); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

// HeldBack is sprint.HeldBack over the work table's waiting column, read
// alone (workColumn). A table with nothing waiting is read no further than its
// shape.
func (st *Store) HeldBack(ctx context.Context) (int, error) {
	t, err := st.workColumn(ctx, sprint.Waiting)
	if err != nil || t == nil {
		return 0, err
	}
	return sprint.HeldBack(&sprint.Snapshot{Work: t}), nil
}

// FriendWaits is the friends' cards of the work table's waiting and ready columns, each
// column read alone (workColumn), for where with no where record (WhereFacts).
func (st *Store) FriendWaits(ctx context.Context) ([]FriendWait, error) {
	var out []FriendWait
	for _, col := range []string{sprint.Waiting, sprint.Ready} {
		t, err := st.workColumn(ctx, col)
		if err != nil {
			return nil, err
		}
		out = append(out, friendWaits(t)...)
	}
	return out, nil
}

// LandedAt is the landed stamps of the work table's landed column, read alone
// (workColumn), for the landing rate of the ETA (sprint.LandingRate); a card
// with no stamp is left out.
func (st *Store) LandedAt(ctx context.Context) ([]time.Time, error) {
	t, err := st.workColumn(ctx, sprint.Landed)
	if err != nil || t == nil {
		return nil, err
	}
	var out []time.Time
	for _, c := range t.Column(sprint.Landed) {
		if at, err := time.Parse(time.RFC3339, c.F("landed")); err == nil {
			out = append(out, at)
		}
	}
	return out, nil
}

// workColumn is the work table with the cards of one column read: its shape,
// the ids of that column's cells, and their records, taken again as Load takes
// a read that saw the table move; nil when the column is empty.
func (st *Store) workColumn(ctx context.Context, col string) (*sprint.Table, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	r := st.retry(ctx)
	for r.next(LoadTries) {
		t, err := st.workColumnOnce(ctx, col)
		if !errors.Is(err, errMoved) {
			return t, err
		}
	}
	return nil, fmt.Errorf("the tables are busy: the work table kept changing while its %s cards were read, %d reads in %s", col, r.tries, r.slept().Round(time.Millisecond))
}

func (st *Store) workColumnOnce(ctx context.Context, col string) (*sprint.Table, error) {
	shapes, err := st.shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil {
		return nil, err
	}
	shape := shapes[0]
	if st.pinned && shape.Epoch != st.epoch {
		return nil, errCleared
	}
	j := shape.Column(col)
	n := int64(0)
	for _, row := range shape.Rows {
		if j >= 0 && j < len(row.Cells) {
			n += row.Cells[j].Count
		}
	}
	if n == 0 {
		return nil, nil
	}
	// only the column's cells are read: every other set column is read as
	// text, which has no cell ids
	shape.Columns = slices.Clone(shape.Columns)
	for k := range shape.Columns {
		if k != j && shape.Columns[k].HasSet() {
			shape.Columns[k].Projection = ntable.Text
		}
	}
	ids, err := st.B.CellIDs(ctx, []ntable.Table{shape})
	if err != nil {
		return nil, err
	}
	t := sprint.NewTable(sprint.Work)
	t.Epoch, t.Revision = shape.Epoch, shape.Revision
	for _, row := range shape.Rows {
		t.SetRows(append(t.Rows(), row.Key))
	}
	if err := st.readInto(ctx, t, ids[shape.Name], true); err != nil {
		return nil, err
	}
	return t, nil
}

// shapes reads the tables' shapes in one exchange. Reading an earlier epoch,
// a table the table layer holds no definition of at that epoch (NOTABLE: it
// keeps one only from the epoch's first write) had no write at it, and is
// empty at it, when the table itself is there.
func (st *Store) shapes(ctx context.Context, stored []string) ([]ntable.Table, error) {
	out, err := st.B.Shapes(ctx, stored)
	if err == nil || !st.old || refusalCode(err) != "NOTABLE" {
		return out, err
	}
	live, lerr := st.root.Shapes(ctx, stored)
	if lerr != nil {
		return nil, err
	}
	out = make([]ntable.Table, len(stored))
	for i, name := range stored {
		one, err := st.B.Shapes(ctx, []string{name})
		switch {
		case err == nil:
			out[i] = one[0]
		case refusalCode(err) == "NOTABLE":
			empty := live[i]
			empty.Rows, empty.Epoch, empty.Revision = nil, st.epoch, 0
			out[i] = empty
		default:
			return nil, err
		}
	}
	return out, nil
}

// readInto reads ids into t in read sets, each of which must see t's revision.
func (st *Store) readInto(ctx context.Context, t *sprint.Table, ids []string, placed bool) error {
	for start := 0; start < len(ids); start += ntable.LimitReadSetMembers {
		end := min(start+ntable.LimitReadSetMembers, len(ids))
		res, err := st.readSet(ctx, st.Names.Table(t.Name), ids[start:end])
		if err != nil {
			return err
		}
		if res.Revision != t.Revision || (placed && len(res.Missing) > 0) {
			return &movedError{table: st.Names.Table(t.Name)}
		}
		for _, m := range res.Members {
			if placed && !m.Placed {
				return &movedError{table: st.Names.Table(t.Name)}
			}
			c := &sprint.Card{ID: sprint.CardID(m.ID), Score: m.Score, Rev: m.Revision, Fields: m.Fields}
			if m.Placed {
				c.Row, c.Col = m.Row, m.Col
			}
			t.Put(c)
		}
	}
	return nil
}

// readSet is one read set of a table, its records counted (stats.go).
func (st *Store) readSet(ctx context.Context, table string, ids []string) (ntable.ReadSetResult, error) {
	res, err := st.B.ReadSet(ctx, table, ids)
	st.stats().rows.Add(int64(len(res.Members)))
	return res, err
}
