package refmodel_test

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// t0 is the clock of every fixture: the tables are built at it and a duty
// decides at a time after it.
var t0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

// world is the tables of a sprint that a test moves by applying plans to them,
// the way a store would: a guard is checked against the state the plan was
// built on, and a plan that breaks one is an error. It is the tests' harness,
// not the store's semantics: nothing refuses here that the planners did not.
type world struct {
	s   *sprint.Snapshot
	seq int
}

// newWorld is an empty sprint with the readers named, a coordinator, and the
// clock at t0.
func newWorld(readers ...string) *world {
	s := &sprint.Snapshot{Now: t0, Work: sprint.NewTable(sprint.Work), Readers: sprint.NewTable(sprint.Readers),
		Merge: sprint.NewTable(sprint.Merge), Fleet: sprint.NewTable(sprint.Fleet), Coordinator: "coordinator", Actor: "coordinator"}
	s.Readers.SetRows(append(s.Readers.Rows(), readers...))
	return &world{s: s}
}

// must applies a plan and fails the test on a refusal or a broken guard.
func (w *world) must(t *testing.T, p sprint.Plan) {
	t.Helper()
	if len(p.Refused) > 0 {
		t.Fatalf("refused: %v", p.Refused)
	}
	if err := w.apply(p); err != nil {
		t.Fatal(err)
	}
}

// apply carries out a plan: its rows, then each unit's changes, bumps, closes
// and notes, then the notes, closes and updates of the plan as a whole. A
// judgment is open on each of its subjects, and an acknowledged condition is
// kept beside them.
func (w *world) apply(p sprint.Plan) error {
	for _, r := range p.Rows {
		if tb := w.s.T(r.Table); !tb.HasRow(r.Row) {
			tb.SetRows(append(tb.Rows(), r.Row))
		}
	}
	for _, u := range p.Units {
		for _, c := range u.Changes {
			if err := w.entry(c); err != nil {
				return err
			}
		}
		for _, b := range u.Bumps {
			tb := w.s.T(b.Table)
			c := tb.Card(b.ID)
			if c == nil {
				return fmt.Errorf("%s: bump %s: no such card", b.Table, b.ID)
			}
			c.Fields[b.Field] = strconv.Itoa(c.Int(b.Field) + b.Delta)
			c.Rev++
			tb.Put(c)
		}
		w.closeAll(u.Closes)
		w.note(u.Notes...)
	}
	w.closeAll(p.Closes)
	w.note(p.Notes...)
	for _, n := range p.Updates {
		for i, o := range w.s.Open {
			if o.Note.ID == n.ID {
				w.s.Open[i].Note = n
			}
		}
	}
	return nil
}

// note writes notifications: each gets an id, and a judgment is open on each
// of its subjects.
func (w *world) note(ns ...sprint.Note) {
	for _, n := range ns {
		if n.Type == "" {
			continue
		}
		w.seq++
		n.ID = fmt.Sprintf("n%d", w.seq)
		switch n.Kind {
		case sprint.Judgment:
			for _, sub := range n.Subjects() {
				w.s.Open = append(w.s.Open, sprint.Open{Key: sprint.OpenKey(n.ID, sub), Note: n})
			}
		case sprint.Acknowledged:
			for _, sub := range n.Subjects() {
				w.s.Acked = append(w.s.Acked, sprint.Open{Key: sprint.OpenKey(n.ID, sub), Note: n})
			}
		}
	}
}

// closeAll closes the judgments and acknowledged conditions named.
func (w *world) closeAll(cs []sprint.Open) {
	for _, c := range cs {
		w.s.Open = without(w.s.Open, c.Key)
		w.s.Acked = without(w.s.Acked, c.Key)
	}
}

func without(os []sprint.Open, key string) []sprint.Open {
	var out []sprint.Open
	for _, o := range os {
		if o.Key != key {
			out = append(out, o)
		}
	}
	return out
}

// entry carries out one change to one card, checking the entry's guard against
// the card as it stands.
func (w *world) entry(ch sprint.Change) error {
	tb := w.s.T(ch.Table)
	e := ch.Entry
	c := tb.Card(e.ID)
	if e.Expect != nil && e.Expect.Absent {
		if c != nil {
			return fmt.Errorf("%s: create %s: it exists", ch.Table, e.ID)
		}
		c = &sprint.Card{ID: e.ID, Row: e.Create.Row, Col: e.Create.Col, Score: e.Create.Score, Rev: 1, Fields: map[string]string{}}
		for k, v := range e.Set {
			c.Fields[k] = v
		}
		tb.Put(c)
		return nil
	}
	if c == nil {
		return fmt.Errorf("%s: %s: no such card", ch.Table, e.ID)
	}
	if x := e.Expect; x != nil {
		if x.Revision != "" && x.Revision != strconv.FormatUint(c.Rev, 10) {
			return fmt.Errorf("%s: %s: revision %d, the plan read %s", ch.Table, e.ID, c.Rev, x.Revision)
		}
		if pl := x.Place; pl != nil && (pl.Row != c.Row || pl.Col != c.Col) {
			return fmt.Errorf("%s: %s: at %s:%s, the plan read %s:%s", ch.Table, e.ID, c.Row, c.Col, pl.Row, pl.Col)
		}
	}
	changed := false
	if e.Move != nil {
		if e.Move.Row != c.Row || e.Move.Col != c.Col {
			if !tb.HasRow(e.Move.Row) {
				return fmt.Errorf("%s: %s: no row %s", ch.Table, e.ID, e.Move.Row)
			}
			c.Row, c.Col, changed = e.Move.Row, e.Move.Col, true
		}
		if e.Move.Score != nil && *e.Move.Score != c.Score {
			c.Score, changed = *e.Move.Score, true
		}
	}
	if e.Remove {
		c.Row, c.Col, changed = "", "", true
	}
	for k, v := range e.Set {
		if c.Fields[k] != v {
			c.Fields[k], changed = v, true
		}
	}
	for _, k := range e.Unset {
		if _, ok := c.Fields[k]; ok {
			delete(c.Fields, k)
			changed = true
		}
	}
	if changed {
		c.Rev++
	}
	tb.Put(c)
	return nil
}

// state is the primary's state, "" when it is not on the table.
func (w *world) state(id string) sprint.State { return w.s.StateOf(id) }

// gen is the live generation of a work card.
func (w *world) gen(id string) int { return w.s.Fleet.Card(id).Int("gen") }
