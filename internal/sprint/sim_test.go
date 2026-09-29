package sprint

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

// world is a snapshot the tests move by applying plans to it, the way a store
// would: every guard is checked against the pre-state of the unit, and a
// failed guard fails the test. It is the core's own harness; the store
// binding has the real refusal semantics.
type world struct {
	t     *testing.T
	s     *Snapshot
	notes []Note
	seq   int
}

var t0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func newWorld(t *testing.T, readers ...string) *world {
	t.Helper()
	s := &Snapshot{Now: t0, Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	s.Readers.Rows = append(s.Readers.Rows, readers...)
	return &world{t: t, s: s}
}

func (w *world) tick(d time.Duration) { w.s.Now = w.s.Now.Add(d) }

// do applies a plan and fails on any refusal unless allowed.
func (w *world) do(p Plan) Plan {
	w.t.Helper()
	for _, ra := range p.Rows {
		tb := w.s.T(ra.Table)
		if !tb.HasRow(ra.Row) {
			tb.Rows = append(tb.Rows, ra.Row)
		}
	}
	for _, u := range p.Units {
		for _, c := range u.Changes {
			w.entry(c)
		}
		for _, b := range u.Bumps {
			c := w.s.T(b.Table).Card(b.ID)
			c.Fields[b.Field] = strconv.Itoa(c.Int(b.Field) + b.Delta)
			c.Rev++
		}
		w.closeAll(u.Closes)
		w.note(u.Notes...)
	}
	w.closeAll(p.Closes)
	w.note(p.Notes...)
	for _, tb := range []*Table{w.s.Work, w.s.Readers, w.s.Merge, w.s.Fleet} {
		tb.cells, tb.byPrimary = nil, nil
	}
	return p
}

func (w *world) must(p Plan) Plan {
	w.t.Helper()
	if len(p.Refused) > 0 {
		w.t.Fatalf("refused: %v", p.Refused)
	}
	return w.do(p)
}

func (w *world) note(ns ...Note) {
	for _, n := range ns {
		if n.Type == "" {
			continue
		}
		w.seq++
		n.ID = fmt.Sprintf("n%d", w.seq)
		w.notes = append(w.notes, n)
		if n.Kind == Judgment {
			for _, sub := range n.Subjects() {
				w.s.Open = append(w.s.Open, Open{Key: OpenKey(n.ID, sub), Note: n})
			}
		}
	}
}

func (w *world) closeAll(cs []Open) {
	for _, c := range cs {
		for i, o := range w.s.Open {
			if o.Key == c.Key {
				w.s.Open = append(w.s.Open[:i], w.s.Open[i+1:]...)
				break
			}
		}
	}
}

func (w *world) entry(ch Change) {
	w.t.Helper()
	tb := w.s.T(ch.Table)
	e := ch.Entry
	c := tb.Card(e.ID)
	if e.Expect != nil && e.Expect.Absent {
		if c != nil {
			w.t.Fatalf("%s: create %s: exists", ch.Table, e.ID)
		}
		c = &Card{ID: e.ID, Row: e.Create.Row, Col: e.Create.Col, Score: e.Create.Score, Rev: 1, Fields: map[string]string{}}
		for k, v := range e.Set {
			c.Fields[k] = v
		}
		tb.Cards[c.ID] = c
		return
	}
	if c == nil {
		w.t.Fatalf("%s: %s: no such member", ch.Table, e.ID)
	}
	if e.Expect != nil {
		if e.Expect.Revision != "" && e.Expect.Revision != strconv.FormatUint(c.Rev, 10) {
			w.t.Fatalf("%s: %s: revision %s, expected %s", ch.Table, e.ID, strconv.FormatUint(c.Rev, 10), e.Expect.Revision)
		}
		if pl := e.Expect.Place; pl != nil && (pl.Row != c.Row || pl.Col != c.Col) {
			w.t.Fatalf("%s: %s: at %s:%s, expected %s:%s", ch.Table, e.ID, c.Row, c.Col, pl.Row, pl.Col)
		}
	}
	changed := false
	if e.Move != nil {
		if e.Move.Row != c.Row || e.Move.Col != c.Col {
			if !tb.HasRow(e.Move.Row) {
				w.t.Fatalf("%s: %s: no row %s", ch.Table, e.ID, e.Move.Row)
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
}

// clean fails when the snapshot breaks any rule of section 9.
func (w *world) clean(when string) {
	w.t.Helper()
	if v := Check(w.s, nil); len(v) > 0 {
		w.t.Fatalf("%s: %v", when, v)
	}
}

func (w *world) state(id string) State { return w.s.StateOf(id) }

// gens is the live generation of each work card, as its worker holds it.
func (w *world) gens(ids ...string) map[string]int {
	out := map[string]int{}
	for _, id := range ids {
		out[id] = w.s.Fleet.Card(id).Int("gen")
	}
	return out
}

func (w *world) notesOf(typ string) []Note {
	var out []Note
	for _, n := range w.notes {
		if n.Type == typ {
			out = append(out, n)
		}
	}
	return out
}

func (w *world) openOn(subject string) []Open {
	var out []Open
	for _, o := range w.s.Open {
		if o.Subject() == subject {
			out = append(out, o)
		}
	}
	return out
}
