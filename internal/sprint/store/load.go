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
	var last *movedError
	r := st.retry()
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
		last.table, r.tries, r.slept.Round(time.Millisecond))
}

func (st *Store) loadOnce(ctx context.Context, tables []string, extras func(*sprint.Snapshot) map[string][]string) (*sprint.Snapshot, error) {
	s := &sprint.Snapshot{Now: st.Now()}
	stored := make([]string, len(tables))
	for i, t := range tables {
		stored[i] = st.Names.Table(t)
	}
	shapes, err := st.B.Shapes(ctx, stored)
	if err != nil {
		return nil, err
	}
	ids, err := st.B.CellIDs(ctx, shapes)
	if err != nil {
		return nil, err
	}
	for i, shape := range shapes {
		t := sprint.NewTable(tables[i])
		t.Epoch, t.Revision = shape.Epoch, shape.Revision
		for _, r := range shape.Rows {
			t.Rows = append(t.Rows, r.Key)
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
	if s.Open, err = st.B.OpenNotes(ctx); err != nil {
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
			if err := st.readInto(ctx, t, missing, false); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

// readInto reads ids into t in read sets, each of which must see t's revision.
func (st *Store) readInto(ctx context.Context, t *sprint.Table, ids []string, placed bool) error {
	for start := 0; start < len(ids); start += ntable.LimitReadSetMembers {
		end := min(start+ntable.LimitReadSetMembers, len(ids))
		res, err := st.B.ReadSet(ctx, st.Names.Table(t.Name), ids[start:end])
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
			c := &sprint.Card{ID: m.ID, Score: m.Score, Rev: m.Revision, Fields: m.Fields}
			if m.Placed {
				c.Row, c.Col = m.Row, m.Col
			}
			t.Put(c)
		}
	}
	return nil
}
