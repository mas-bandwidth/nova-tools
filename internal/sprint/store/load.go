package store

import (
	"context"
	"errors"
	"fmt"
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
