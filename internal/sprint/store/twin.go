package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The tick's twin: the sprint read once and kept (the owner's requirement
// of 2026-09-30, "the whole intent is sub-second ticks"). The first read
// builds it; every later read of a step that loads the four tables brings it
// up to date instead of reading them whole, and each step's own writes are
// applied to it from their receipts (the store's own account of what each
// batch changed). The run loop keeps it from one tick to the next.
//
// Why it is the state a fresh read gives. Every write of a record or a table
// property is a step's operation, and every operation takes the fence at the
// generation its plan read (Acquire, WATCH + MULTI/EXEC). A read from the
// twin reads the fence, then each table's shape (its rows, texts, properties
// and revision), then brings each table whose revision moved since the twin
// last saw it up to date: the records its writes since then changed, named by
// the table layer's change stream (every write of a table appends its event
// there, with the members it moved and the batch's account of every entry),
// read again from the store; a table whose stream does not account for every
// revision between is read whole. It reads the open judgments and the
// coordinator every time, and the fence again last: a generation that moved
// during the read, or an operation in flight, is read again, as a fresh read
// is. So each step plans on the state at the generation its Acquire guards,
// as it does after a fresh read (no model changes: tla/DirtyTick.tla and the
// engine's fence are as they were; a state read twice gives the same state).
//
// After a step commits, the twin is the state its plan read with the
// operation's receipts applied: each record's place, score and revision as
// the store left them, its fields as the entry set and unset them, each
// table's revision and changed properties, the queue as the commit trimmed and
// pushed it, at the generation its Acquire set. Anything else (a lost or cut
// operation, a receipt without its account, a refusal) drops the twin's
// records of every table, and the next read reads them whole. A test build
// checks every read of the twin against a fresh read (Store.CheckTwin).

// GrantError is a read the store refused to this user for want of a grant:
// the twin reads the table whole instead, and the tick says so (a NOTE).
type GrantError struct {
	Command, Key string
	Cause        error
}

func (e *GrantError) Error() string {
	return fmt.Sprintf("the store refused %s on %s to this user (%v): grant +%s to its seat; the tick reads the table whole until then", e.Command, e.Key, e.Cause, strings.ToLower(e.Command))
}

// TableChanger is a store that says which records a table's writes changed
// between two of its revisions: the table layer's change stream. ok is false
// when the stream does not account for every revision between (a gap,
// another epoch, a write it cannot name the records of): the caller reads the
// table whole.
type TableChanger interface {
	TableChanges(ctx context.Context, table string, from, to uint64) (ids []string, ok bool, err error)
}

// View is what a read of the twin reads first: the fence, the tables'
// shapes, the open judgments and the coordinator.
type View struct {
	Fence       Fence
	Shapes      []ntable.Table
	Open        []sprint.Open
	Coordinator string
}

// ViewReader is a store that reads a View in fewer exchanges than one each.
type ViewReader interface {
	ReadView(ctx context.Context, tables []string) (View, error)
}

// readView reads the view: in one exchange where the store can, else one
// read each, in the order a fresh read takes them (the fence first).
func (st *Store) readView(ctx context.Context, load []string) (View, error) {
	stored := make([]string, len(load))
	for i, t := range load {
		stored[i] = st.Names.Table(t)
	}
	if vr, ok := st.B.(ViewReader); ok && !st.old {
		return vr.ReadView(ctx, stored)
	}
	var v View
	var err error
	if v.Fence, err = st.B.ReadFence(ctx); err != nil || v.Fence.Pending != nil {
		return v, err
	}
	if v.Shapes, err = st.shapes(ctx, stored); err != nil {
		return v, err
	}
	if v.Open, err = st.B.OpenNotes(ctx); err != nil {
		return v, err
	}
	v.Coordinator, err = st.B.Coordinator(ctx)
	return v, err
}

// Twin is a copy of the sprint (see above). The zero value is empty: its
// first read reads the four tables whole.
type Twin struct {
	// mu is held by the step that reads and writes through the twin, from
	// its read to its commit: a step that finds it held (a step run inside
	// another's, or another goroutine's) reads the store itself.
	mu     sync.Mutex
	valid  bool   // gen and queue are known
	gen    uint64 // the fence's generation the twin is the state at
	epoch  uint64
	tables map[string]*sprint.Table // by logical name: its placed records, and the kept ones a step's extras named
	// kept is the records read that are on no cell (an extra a step named),
	// absent the ids read and not found, and shown the kept records put in a
	// table for the step in flight, each by logical table and id.
	kept, shown map[string]map[string]*sprint.Card
	absent      map[string]map[string]bool
	queue       []sprint.QueuedChange
	queueKnown  bool
	// last is the last snapshot read from the twin (its judgments, its
	// coordinator, the machine's state): what peek answers with beside the
	// tables.
	last *sprint.Snapshot
}

// NewTwin is an empty twin: its first read reads the store whole.
func NewTwin() *Twin { return &Twin{} }

// drop forgets the twin's records: the next read reads every table whole.
func (tw *Twin) drop() {
	tw.valid, tw.tables, tw.queue, tw.queueKnown = false, nil, nil, false
}

// reset is an empty twin of the epoch.
func (tw *Twin) reset(epoch uint64) {
	tw.drop()
	tw.epoch = epoch
	tw.tables = map[string]*sprint.Table{}
	tw.kept, tw.shown, tw.absent = map[string]map[string]*sprint.Card{}, map[string]map[string]*sprint.Card{}, map[string]map[string]bool{}
	for _, name := range All {
		tw.kept[name], tw.shown[name], tw.absent[name] = map[string]*sprint.Card{}, map[string]*sprint.Card{}, map[string]bool{}
	}
}

// ShareTwin has the store read and write through the twin (a process's
// one twin of a store: every step it runs, a verb's or a tick's part, reads
// only what changed since the last); nil is none.
func (st *Store) ShareTwin(tw *Twin) { st.tw = tw }

// twin is the store's twin, made on first use; its pinned copies share it.
func (st *Store) twin() *Twin {
	if st.tw == nil {
		st.tw = NewTwin()
	}
	return st.tw
}

// twinRead is the sprint as a fresh fenced read of the four tables gives it,
// from the twin brought up to date (see above): the snapshot and the fence it
// was read at. A pending operation is finished first, as fencedRead finishes
// it.
func (st *Store) twinRead(ctx context.Context, tw *Twin, load []string, extras func(*sprint.Snapshot) map[string][]string, repaired *[]string) (*sprint.Snapshot, Fence, error) {
	r := st.retry(ctx)
	for r.next(st.attempts()) {
		v, err := st.readView(ctx, load)
		if err != nil {
			return nil, Fence{}, err
		}
		f := v.Fence
		if f.Pending != nil {
			res, err := st.finish(ctx, *f.Pending)
			if err != nil {
				return nil, Fence{}, err
			}
			if res.Done == "open" {
				if st.now().Sub(f.Pending.At) < st.grace() {
					continue // in flight: its writer is at it
				}
				return nil, Fence{}, &PendingError{Op: res.Op, Why: res.Detail}
			}
			if repaired != nil {
				*repaired = append(*repaired, res.Op+" "+res.Done)
			}
			continue
		}
		if tw.tables == nil || tw.epoch != st.epoch {
			tw.reset(st.epoch)
		}
		snap, err := st.twinView(ctx, tw, load, v, extras)
		var moved *movedError
		if errors.As(err, &moved) {
			continue
		}
		if err != nil {
			return nil, Fence{}, err
		}
		f2, err := st.B.ReadFence(ctx)
		if err != nil {
			return nil, Fence{}, err
		}
		if f2.Pending != nil || f2.Gen != f.Gen {
			continue
		}
		if !tw.valid || tw.gen != f.Gen {
			// another writer's commit pushed to the queue or took from it
			tw.queue, tw.queueKnown = nil, false
		}
		tw.valid, tw.gen = true, f.Gen
		snap.QueueLen, snap.Running = f2.Queued, f2.Running
		f2.Gen = f.Gen
		tw.last = snap
		if st.CheckTwin != nil {
			if err := st.checkTwin(ctx, snap, f.Gen, load, extras); err != nil {
				return nil, Fence{}, err
			}
		}
		return snap, f2, nil
	}
	return nil, Fence{}, fmt.Errorf("the sprint is busy: other operations kept the fence moving, %d reads in %s; nothing was changed; run the verb again", r.tries, r.slept().Round(time.Millisecond))
}

// checkTwin gives CheckTwin the twin's snapshot with a fresh read of the
// same generation (its own read, not counted).
func (st *Store) checkTwin(ctx context.Context, snap *sprint.Snapshot, gen uint64, load []string, extras func(*sprint.Snapshot) map[string][]string) error {
	chk := *st
	chk.Stats, chk.CheckTwin, chk.tw = &Stats{}, nil, nil
	fresh, at, err := chk.Fenced(ctx, load, extras, nil)
	if err != nil || at != gen {
		return err
	}
	if err := st.CheckTwin(snap, fresh); err != nil {
		return fmt.Errorf("the tick's twin differs from a fresh read at generation %d: %w", gen, err)
	}
	return nil
}

// twinView brings the twin up to date with the store as it is now (the
// tables' shapes, the records written since, the open judgments, the
// coordinator, the records the step's extras name) and is it as a step's
// snapshot. A table that moved while it was read is a movedError.
func (st *Store) twinView(ctx context.Context, tw *Twin, load []string, v View, extras func(*sprint.Snapshot) map[string][]string) (*sprint.Snapshot, error) {
	shapes := v.Shapes
	s := &sprint.Snapshot{Now: st.now(), Epoch: st.epoch, Cleared: st.cleared, Actor: st.Actor}
	for _, shape := range shapes {
		if shape.Epoch != st.epoch {
			return nil, errCleared
		}
	}
	// Each table is brought to its shape's revision, or read whole; a table
	// is in the twin only once its records are the ones at the revision it
	// holds, so a read cut short leaves no table half read.
	var whole []ntable.Table
	for i, shape := range shapes {
		t := tw.tables[load[i]]
		switch {
		case t == nil:
			whole = append(whole, shape)
		case t.Revision != shape.Revision:
			if err := st.catchUp(ctx, tw, load[i], shape); err != nil {
				return nil, err
			}
			tw.tables[load[i]].Revision = shape.Revision
			if why := countsAgree(tw.tables[load[i]], shape, st.epoch); why != "" {
				// the records the twin holds do not add up to the store's own
				// counts at the revision both are of: the table is read whole
				// (counted, Stats.mismatch), whatever the cause
				st.stats().mismatch.Add(1)
				delete(tw.tables, load[i])
				whole = append(whole, shape)
			}
		}
	}
	if len(whole) > 0 {
		ids, err := st.B.CellIDs(ctx, whole)
		if err != nil {
			return nil, err
		}
		for _, shape := range whole {
			name := st.Names.Logical(shape.Name)
			fresh := sprint.NewTable(name)
			fresh.Revision = shape.Revision
			st.stats().reads.Add(1)
			if err := st.readInto(ctx, fresh, ids[shape.Name], true); err != nil {
				return nil, err
			}
			tw.tables[name] = fresh
			tw.kept[name], tw.shown[name], tw.absent[name] = map[string]*sprint.Card{}, map[string]*sprint.Card{}, map[string]bool{}
		}
	}
	for i, shape := range shapes {
		t := tw.tables[load[i]]
		t.Epoch = shape.Epoch
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
	}
	for _, name := range load {
		switch name {
		case sprint.Work:
			s.Work = tw.tables[name]
		case sprint.Readers:
			s.Readers = tw.tables[name]
		case sprint.Merge:
			s.Merge = tw.tables[name]
		case sprint.Fleet:
			s.Fleet = tw.tables[name]
		}
	}
	s.Open, s.Acked = sprint.SplitOpen(v.Open)
	s.Coordinator = v.Coordinator
	if err := st.showExtras(ctx, tw, s, load, extras); err != nil {
		return nil, err
	}
	st.stats().twin.Add(1)
	return s, nil
}

// catchUp brings one table of the twin from the revision it holds to the
// shape's: the records the writes between changed, named by the table's
// change stream and read again; the table read whole when the stream does not
// account for every revision between.
func (st *Store) catchUp(ctx context.Context, tw *Twin, name string, shape ntable.Table) error {
	t := tw.tables[name]
	tc, ok := st.B.(TableChanger)
	var ids []string
	if ok {
		var err error
		ids, ok, err = tc.TableChanges(ctx, shape.Name, t.Revision, shape.Revision)
		var grant *GrantError
		if errors.As(err, &grant) {
			st.stats().note(grant.Error())
			ok, err = false, nil
		}
		if err != nil {
			return err
		}
	}
	if !ok {
		delete(tw.tables, name)
		return st.wholeTable(ctx, tw, name, shape)
	}
	st.stats().stale.Add(1)
	if len(ids) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var cards []string
	for _, id := range ids {
		if c := sprint.CardID(id); !seen[c] {
			seen[c] = true
			cards = append(cards, c)
		}
	}
	found := sprint.NewTable(name)
	found.Revision = shape.Revision
	if err := st.readInto(ctx, found, st.sids(cards), false); err != nil {
		return err
	}
	for _, id := range cards {
		c := found.Card(id)
		delete(tw.shown[name], id)
		switch {
		case c == nil:
			t.Drop(id)
			delete(tw.kept[name], id)
			tw.absent[name][id] = true
		case c.Placed():
			t.Put(c)
			delete(tw.kept[name], id)
			delete(tw.absent[name], id)
		default:
			t.Drop(id)
			tw.kept[name][id] = c
			delete(tw.absent[name], id)
		}
	}
	return nil
}

// countsAgree says the twin's table holds, in each cell, as many placed
// records as the store's shape of the same revision counts there (the row's
// excluded member, which the counts leave out, not counted): "" when it
// does, else the first cell that differs.
func countsAgree(t *sprint.Table, shape ntable.Table, epoch uint64) string {
	for _, row := range shape.Rows {
		for k, col := range shape.Columns {
			if !col.HasSet() || k >= len(row.Cells) {
				continue
			}
			n := t.Count(row.Key, col.Name)
			if row.Exclude != "" {
				if c := t.Placed(sprint.CardID(row.Exclude)); c != nil && c.Row == row.Key && c.Col == col.Name {
					n--
				}
			}
			if int64(n) != row.Cells[k].Count {
				return fmt.Sprintf("%s %s:%s holds %d, the store counts %d", t.Name, row.Key, col.Name, n, row.Cells[k].Count)
			}
		}
	}
	return ""
}

// wholeTable reads one table of the twin whole.
func (st *Store) wholeTable(ctx context.Context, tw *Twin, name string, shape ntable.Table) error {
	ids, err := st.B.CellIDs(ctx, []ntable.Table{shape})
	if err != nil {
		return err
	}
	t := sprint.NewTable(name)
	t.Revision = shape.Revision
	st.stats().reads.Add(1)
	if err := st.readInto(ctx, t, ids[shape.Name], true); err != nil {
		return err
	}
	tw.tables[name] = t
	tw.kept[name], tw.shown[name], tw.absent[name] = map[string]*sprint.Card{}, map[string]*sprint.Card{}, map[string]bool{}
	return nil
}

// showExtras puts in each table the kept records the step's extras name, read
// once (a fresh read reads them with its tables), and takes out the ones it
// does not name.
func (st *Store) showExtras(ctx context.Context, tw *Twin, s *sprint.Snapshot, load []string, extras func(*sprint.Snapshot) map[string][]string) error {
	// The extras are named over the placed records (a fresh read names them
	// before it reads any), which the kept ones shown do not change.
	want := map[string]map[string]bool{}
	if extras != nil {
		for table, ids := range extras(s) {
			if want[table] == nil {
				want[table] = map[string]bool{}
			}
			t := s.T(table)
			if t == nil {
				continue // an extra of a table the step does not load: a fresh read reads none
			}
			var missing []string
			for _, id := range ids {
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
				found.Revision = t.Revision
				if err := st.readInto(ctx, found, st.sids(missing), false); err != nil {
					return err
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
	for _, table := range load {
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
	return nil
}

// peek is the twin as the steps that wrote through it left it, read from no
// store: the tables with their receipts applied, the queue as their commits
// left it (projected for a step other than the pump's, as its step plans on
// it), and the last read's judgments and coordinator. nil when the twin is not
// known (dropped, or held by another step). The tick asks it only whether a
// part has anything to do; the part's step then reads the store.
func (st *Store) peek(tw *Twin, pump bool) *sprint.Snapshot {
	if tw == nil || !tw.mu.TryLock() {
		return nil
	}
	defer tw.mu.Unlock()
	if !tw.valid || tw.last == nil || tw.tables == nil || !tw.queueKnown {
		return nil
	}
	s := *tw.last
	s.Now = st.now()
	s.Work, s.Readers, s.Merge, s.Fleet = tw.tables[sprint.Work], tw.tables[sprint.Readers], tw.tables[sprint.Merge], tw.tables[sprint.Fleet]
	if s.Work == nil || s.Readers == nil || s.Merge == nil || s.Fleet == nil {
		return nil
	}
	s.QueueLen, s.Queue = len(tw.queue), nil
	if !pump && len(tw.queue) > 0 {
		return sprint.WithQueue(&s, slices.Clone(tw.queue))
	}
	return &s
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
		if tw.tables[st.Names.Logical(r.man.Table)] == nil {
			continue // a table the twin does not hold: its first read reads it whole
		}
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

// fencedStep is the step's read: from the twin when the step has one and
// loads the four tables (twinRead), else fenced.
func (st *Store) fencedStep(ctx context.Context, tw *Twin, step Step, repaired *[]string) (*sprint.Snapshot, Fence, error) {
	if tw == nil || len(step.Load) == 0 || !twinTables(step.Load) {
		return st.fenced(ctx, step.Load, step.Extras, repaired)
	}
	return st.twinRead(ctx, tw, step.Load, step.Extras, repaired)
}

// twinTables says the tables are the sprint's, each once.
func twinTables(load []string) bool {
	seen := map[string]bool{}
	for _, t := range load {
		if seen[t] || !slices.Contains(All, t) {
			return false
		}
		seen[t] = true
	}
	return true
}

// stepTwin is the twin a step reads and writes through, held for it: the
// step's own, else the store's; nil when there is none or it is held (a
// step inside another, or another goroutine's), and the step reads the
// store itself. The release is to be called when the step ends.
func (st *Store) stepTwin(step Step) (*Twin, func()) {
	tw := step.Twin
	if tw == nil {
		tw = st.tw
	}
	if tw == nil || !tw.mu.TryLock() {
		return nil, func() {}
	}
	return tw, tw.mu.Unlock
}

// TwinDiff is how a snapshot planned on from the twin differs from a fresh
// read of the same generation: "" when they are the same state (the tables'
// records, rows, texts, properties and revisions, the open judgments and the
// coordinator). A test's CheckTwin.
func TwinDiff(twin, fresh *sprint.Snapshot) string {
	var out []string
	for _, name := range All {
		a, b := twin.T(name), fresh.T(name)
		if a == nil || b == nil {
			if (a == nil) != (b == nil) {
				out = append(out, name+": loaded in one read and not the other")
			}
			continue
		}
		if a.Epoch != b.Epoch {
			out = append(out, fmt.Sprintf("%s: epoch %d/%d", name, a.Epoch, b.Epoch))
		}
		// A table's revision, rows and texts move with the writes outside the
		// fence too (the display cells, the rows a step declares), which the
		// fresh read, made after, may see and the twin's read not: they are
		// compared when the two reads saw the same revision. The records and
		// properties are written only under the fence: always compared.
		if a.Revision == b.Revision {
			if !slices.Equal(a.Rows(), b.Rows()) {
				out = append(out, fmt.Sprintf("%s: rows %v, fresh %v", name, a.Rows(), b.Rows()))
			}
			if fmt.Sprint(a.Texts) != fmt.Sprint(b.Texts) {
				out = append(out, fmt.Sprintf("%s: texts %v, fresh %v", name, a.Texts, b.Texts))
			}
		}
		if fmt.Sprint(a.Props()) != fmt.Sprint(b.Props()) {
			out = append(out, fmt.Sprintf("%s: props %v, fresh %v", name, a.Props(), b.Props()))
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
	if twin.QueueLen != fresh.QueueLen || twin.Running != fresh.Running {
		out = append(out, fmt.Sprintf("the queue %d/%d, running %v/%v", twin.QueueLen, fresh.QueueLen, twin.Running, fresh.Running))
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
