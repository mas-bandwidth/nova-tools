package store

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The tick's twin: one read of the sprint per tick (the owner's requirement
// of 2026-09-30, "the whole intent is sub-second ticks"). The tick's first
// read builds it; every later part of the tick plans on it instead of reading
// the four tables again, and each part's own writes are applied to it from
// their receipts (the store's own account of what each batch changed).
//
// Why it is the same state a fresh read gives. Every write of a record or a
// table property is a step's operation, and every operation takes the fence
// at the generation its plan read (Acquire, WATCH + MULTI/EXEC), so the
// fence's generation names the state of every record, property and the work
// table's queue: while it is the generation the twin holds, no writer has
// changed any of them since. A part's step reads the fence first; at the
// twin's generation it plans on the twin, and its Acquire at that generation
// is the same guard it is after a fresh read (no model changes:
// tla/DirtyTick.tla and the engine's fence are as they were; a read of an
// unchanged state twice gives the same state). What changes outside the fence
// (the display cells, the rows a step declares, a judgment's review time) is
// read again for every part: the tables' shapes (rows, texts, properties,
// revisions) in one exchange, the open judgments and the coordinator. At any
// other generation, or a pending operation, the step reads the store whole
// and the twin is built again from that read (counted: Stats.stale).
//
// After a part commits, the twin is the state its plan read with the
// operation's receipts applied: each record's place, score and revision as
// the store left them, its fields as the entry set and unset them, each
// table's revision and changed properties, the queue as the commit trimmed and
// pushed it, at the generation its Acquire set. Anything else (a lost or cut
// operation, a receipt without its account, a refusal) drops the twin, and
// the next part reads the store. A test build checks the twin against a
// fresh read at every part (Store.CheckTwin).

// Twin is a tick's copy of the sprint (see above). The zero value is empty:
// the first step that uses it reads the store.
type Twin struct {
	valid  bool
	gen    uint64 // the fence's generation the twin is the state at
	epoch  uint64
	tables map[string]*sprint.Table // by logical name: its placed records, and the kept ones a part's extras named
	// kept is the records read that are on no cell (an extra a part named),
	// absent the ids read and not found, and shown the kept records put in a
	// table for the part in flight, each by logical table and id.
	kept, shown map[string]map[string]*sprint.Card
	absent      map[string]map[string]bool
	queue       []sprint.QueuedChange
	queueKnown  bool
}

// NewTwin is an empty twin: its first step reads the store.
func NewTwin() *Twin { return &Twin{} }

func (tw *Twin) drop() { tw.valid, tw.queue, tw.queueKnown = false, nil, false }

// seed makes the twin the state a whole read of the four tables at gen found.
func (tw *Twin) seed(s *sprint.Snapshot, gen uint64) {
	tw.valid, tw.gen, tw.epoch = true, gen, s.Epoch
	tw.tables = map[string]*sprint.Table{}
	tw.kept, tw.shown, tw.absent = map[string]map[string]*sprint.Card{}, map[string]map[string]*sprint.Card{}, map[string]map[string]bool{}
	tw.queue, tw.queueKnown = nil, false
	for _, name := range All {
		t := s.T(name)
		tw.tables[name] = t
		tw.kept[name], tw.shown[name], tw.absent[name] = map[string]*sprint.Card{}, map[string]*sprint.Card{}, map[string]bool{}
		for _, c := range t.LoadedCards() {
			if !c.Placed() {
				tw.kept[name][c.ID] = c
				tw.shown[name][c.ID] = c
			}
		}
	}
}

// view is the twin as a part's snapshot: the records it holds, the tables'
// shapes read now, the open judgments and the coordinator read now, and the
// records the step's extras name (read once, then kept).
func (st *Store) twinView(ctx context.Context, tw *Twin, extras func(*sprint.Snapshot) map[string][]string) (*sprint.Snapshot, error) {
	stored := make([]string, len(All))
	for i, t := range All {
		stored[i] = st.Names.Table(t)
	}
	shapes, err := st.shapes(ctx, stored)
	if err != nil {
		return nil, err
	}
	s := &sprint.Snapshot{Now: st.now(), Epoch: st.epoch, Cleared: st.cleared, Actor: st.Actor}
	for i, shape := range shapes {
		if shape.Epoch != st.epoch {
			return nil, errCleared
		}
		t := tw.tables[All[i]]
		t.Epoch, t.Revision = shape.Epoch, shape.Revision
		t.SetProps(shape.Props)
		rows := make([]string, 0, len(shape.Rows))
		texts := map[string]map[string]string{}
		for _, r := range shape.Rows {
			rows = append(rows, r.Key)
			if len(r.Texts) > 0 {
				texts[r.Key] = r.Texts
			}
		}
		t.SetRows(rows)
		t.Texts = texts
		switch All[i] {
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
	if s.Coordinator, err = st.B.Coordinator(ctx); err != nil {
		return nil, err
	}
	// The extras are named over the placed records (a fresh read names them
	// before it reads any), which the kept ones shown do not change.
	want := map[string]map[string]bool{}
	if extras != nil {
		for table, ids := range extras(s) {
			if want[table] == nil {
				want[table] = map[string]bool{}
			}
			var missing []string
			for _, id := range ids {
				t := tw.tables[table]
				if t.Placed(id) != nil {
					continue
				}
				want[table][id] = true
				if tw.kept[table][id] == nil && !tw.absent[table][id] && !slices.Contains(missing, id) {
					missing = append(missing, id)
				}
			}
			if len(missing) > 0 {
				found := sprint.NewTable(table)
				found.Revision = tw.tables[table].Revision
				if err := st.readInto(ctx, found, st.sids(missing), false); err != nil {
					return nil, err
				}
				for _, id := range missing {
					if c := found.Card(id); c != nil {
						tw.kept[table][id] = c
					} else {
						tw.absent[table][id] = true
					}
				}
			}
		}
	}
	for _, table := range All {
		t := tw.tables[table]
		for id := range tw.shown[table] {
			if !want[table][id] {
				t.Drop(id)
				delete(tw.shown[table], id)
			}
		}
		for id := range want[table] {
			if c := tw.kept[table][id]; c != nil && tw.shown[table][id] != c {
				t.Put(c)
				tw.shown[table][id] = c
			}
		}
	}
	st.stats().twin.Add(1)
	return s, nil
}

// twinQueue is the work table's queue at the generation the step read: the
// twin's when it holds it at that length, else read from the store (and held
// by the twin from then on).
func (st *Store) twinQueue(ctx context.Context, tw *Twin, queued int) ([]sprint.QueuedChange, error) {
	if tw != nil && tw.valid && tw.queueKnown && len(tw.queue) == queued {
		return slices.Clone(tw.queue), nil
	}
	q, err := st.B.QueueRead(ctx)
	if err != nil {
		return nil, err
	}
	if tw != nil && tw.valid {
		tw.queue, tw.queueKnown = slices.Clone(q), true
	}
	return q, nil
}

// receipt is one manifest's receipt, with the manifest.
type receipt struct {
	man ntable.BatchManifest
	rc  ntable.Receipt
}

// committed applies a committed operation to the twin: its receipts, its
// queue, and the generation its Acquire set. A receipt without the store's
// account of what it changed drops the twin.
func (st *Store) twinCommitted(tw *Twin, op OpRecord, receipts []receipt, gen uint64) {
	if tw == nil || !tw.valid {
		return
	}
	if len(receipts) != len(op.Manifests) || tw.epoch != st.epoch {
		tw.drop()
		return
	}
	for _, r := range receipts {
		if err := tw.apply(st.Names.Logical(r.man.Table), r.man, r.rc); err != nil {
			tw.drop()
			return
		}
	}
	if tw.queueKnown {
		if op.Drain > len(tw.queue) {
			tw.queue, tw.queueKnown = nil, false
		} else {
			tw.queue = append(slices.Clone(tw.queue[op.Drain:]), op.Queue...)
		}
	}
	tw.gen = gen
}

// apply is one receipt applied to its table: each record as the store left
// it.
func (tw *Twin) apply(table string, man ntable.BatchManifest, rc ntable.Receipt) error {
	t := tw.tables[table]
	if t == nil || rc.BatchDelta == nil {
		return fmt.Errorf("no account of table %s", table)
	}
	entries := make(map[string]ntable.BatchMemberEntry, len(man.Members))
	for _, e := range man.Members {
		entries[e.ID] = e
	}
	for _, d := range rc.BatchDelta.Members {
		id := sprint.CardID(d.ID)
		e, ok := entries[d.ID]
		if !ok {
			return fmt.Errorf("receipt names %s, which the manifest does not", d.ID)
		}
		old := t.Card(id)
		if old == nil {
			old = tw.kept[table][id]
		}
		if old != nil && strconv.FormatUint(old.Rev, 10) != d.BeforeRev || old == nil && d.BeforeRev != "" && d.BeforeRev != "0" {
			// the store's record was not the twin's before the batch
			return fmt.Errorf("%s: revision %s before the batch, the twin's is not", d.ID, d.BeforeRev)
		}
		c := &sprint.Card{ID: id, Fields: map[string]string{}}
		if old != nil {
			for k, v := range old.Fields {
				c.Fields[k] = v
			}
		}
		for k, v := range e.Set {
			c.Fields[k] = v
		}
		for _, k := range e.Unset {
			delete(c.Fields, k)
		}
		rev, err := strconv.ParseUint(d.AfterRev, 10, 64)
		if err != nil {
			return err
		}
		c.Rev = rev
		delete(tw.absent[table], id)
		if d.AfterPlace == "" {
			t.Drop(id)
			delete(tw.shown[table], id)
			tw.kept[table][id] = c
			continue
		}
		i := strings.LastIndexByte(d.AfterPlace, ':')
		if i < 0 || d.AfterScoreText == nil {
			return fmt.Errorf("receipt place %q of %s", d.AfterPlace, d.ID)
		}
		c.Row, c.Col = d.AfterPlace[:i], d.AfterPlace[i+1:]
		if c.Score, err = strconv.ParseFloat(*d.AfterScoreText, 64); err != nil {
			return err
		}
		delete(tw.kept[table], id)
		delete(tw.shown[table], id)
		t.Put(c)
	}
	for k, v := range rc.BatchDelta.Props {
		t.SetProp(k, v)
	}
	t.Revision = rc.After
	return nil
}

// fencedStep is the step's read: from the tick's twin while the fence is at
// its generation with nothing pending, else the store read whole (fenced),
// which the twin is built again from.
func (st *Store) fencedStep(ctx context.Context, step Step, repaired *[]string) (*sprint.Snapshot, Fence, error) {
	tw := step.Twin
	if tw == nil || !slices.Equal(step.Load, All) {
		return st.fenced(ctx, step.Load, step.Extras, repaired)
	}
	if tw.valid && tw.epoch == st.epoch {
		snap, f, ok, err := st.fromTwin(ctx, tw, step.Extras)
		if err != nil || ok {
			return snap, f, err
		}
		st.stats().stale.Add(1)
		tw.drop()
	}
	snap, f, err := st.fenced(ctx, step.Load, step.Extras, repaired)
	if err == nil {
		tw.seed(snap, f.Gen)
	}
	return snap, f, err
}

// fromTwin is the twin's view at the fence's generation, the fence read
// before and after it; ok is false when the fence is at another generation
// or holds an operation.
func (st *Store) fromTwin(ctx context.Context, tw *Twin, extras func(*sprint.Snapshot) map[string][]string) (*sprint.Snapshot, Fence, bool, error) {
	f, err := st.B.ReadFence(ctx)
	if err != nil || f.Pending != nil || f.Gen != tw.gen {
		return nil, Fence{}, false, err
	}
	snap, err := st.twinView(ctx, tw, extras)
	if err != nil {
		return nil, Fence{}, false, err
	}
	f2, err := st.B.ReadFence(ctx)
	if err != nil || f2.Pending != nil || f2.Gen != f.Gen {
		return nil, Fence{}, false, err
	}
	snap.QueueLen = f2.Queued
	if st.CheckTwin != nil {
		// the check's read is its own: it is not the step's, and not counted
		chk := *st
		chk.Stats, chk.CheckTwin = &Stats{}, nil
		fresh, gen, err := chk.Fenced(ctx, All, extras, nil)
		if err != nil {
			return nil, Fence{}, false, err
		}
		if gen == f.Gen {
			if err := st.CheckTwin(snap, fresh); err != nil {
				return nil, Fence{}, false, fmt.Errorf("the tick's twin differs from a fresh read at generation %d: %w", gen, err)
			}
		}
	}
	return snap, f2, true, nil
}

// TwinDiff is how a snapshot planned on from the twin differs from a fresh
// read of the same generation: "" when they are the same state (the tables'
// records, rows, texts, properties and revisions, the open judgments and the
// coordinator). A test's CheckTwin.
func TwinDiff(twin, fresh *sprint.Snapshot) string {
	var out []string
	for _, name := range All {
		a, b := twin.T(name), fresh.T(name)
		if a.Revision != b.Revision || a.Epoch != b.Epoch {
			out = append(out, fmt.Sprintf("%s: revision %d/%d epoch %d/%d", name, a.Revision, b.Revision, a.Epoch, b.Epoch))
		}
		if !slices.Equal(a.Rows(), b.Rows()) {
			out = append(out, fmt.Sprintf("%s: rows %v, fresh %v", name, a.Rows(), b.Rows()))
		}
		if fmt.Sprint(a.Props()) != fmt.Sprint(b.Props()) {
			out = append(out, fmt.Sprintf("%s: props %v, fresh %v", name, a.Props(), b.Props()))
		}
		if fmt.Sprint(a.Texts) != fmt.Sprint(b.Texts) {
			out = append(out, fmt.Sprintf("%s: texts %v, fresh %v", name, a.Texts, b.Texts))
		}
		ac, bc := a.LoadedCards(), b.LoadedCards()
		byID := map[string]*sprint.Card{}
		for _, c := range bc {
			byID[c.ID] = c
		}
		for _, c := range ac {
			f := byID[c.ID]
			delete(byID, c.ID)
			switch {
			case f == nil:
				out = append(out, fmt.Sprintf("%s: %s in the twin (%s:%s rev %d), not in the fresh read", name, c.ID, c.Row, c.Col, c.Rev))
			case c.Row != f.Row || c.Col != f.Col || c.Score != f.Score || c.Rev != f.Rev || fmt.Sprint(c.Fields) != fmt.Sprint(f.Fields):
				out = append(out, fmt.Sprintf("%s: %s twin %s:%s %v rev %d %v, fresh %s:%s %v rev %d %v", name, c.ID, c.Row, c.Col, c.Score, c.Rev, c.Fields, f.Row, f.Col, f.Score, f.Rev, f.Fields))
			}
		}
		for id, f := range byID {
			out = append(out, fmt.Sprintf("%s: %s in the fresh read (%s:%s rev %d), not in the twin", name, id, f.Row, f.Col, f.Rev))
		}
	}
	if fmt.Sprint(twin.Open) != fmt.Sprint(fresh.Open) || fmt.Sprint(twin.Acked) != fmt.Sprint(fresh.Acked) {
		out = append(out, "the open judgments differ")
	}
	if twin.Coordinator != fresh.Coordinator {
		out = append(out, "the coordinator differs")
	}
	slices.Sort(out)
	if len(out) > 8 {
		out = append(out[:8], fmt.Sprintf("and %d more", len(out)-8))
	}
	return strings.Join(out, "; ")
}
