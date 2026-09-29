package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Mem is a Backend in memory with the batch's refusal semantics: the
// manifest is validated by the table layer's own validator (bounds included);
// then, against one pre-state, the operation id is looked up first (the same
// bytes replay the original receipt, other bytes conflict), then the epoch,
// the table revision, and every entry's expectations (absent, member revision,
// place, fields); a refused batch changes nothing, an accepted one advances
// the table revision once and each changed member's revision once.
//
// Fail, when set, is asked at each store call ("apply <table> before",
// "apply <table> after", "fence", "acquire", "release"): an error it returns
// is a lost reply. After "apply ... after" the batch has been applied.
type Mem struct {
	mu       sync.Mutex
	tables   map[string]*memTable
	dropped  map[string]*memResidue // what a drop keeps of a table, as the table layer's does
	views    map[string]ntable.View
	fence    *OpRecord
	gen      uint64
	done     map[string]string
	progress map[string]time.Time
	inbox    []memNote
	notes    map[string]sprint.Note
	open     map[string]string
	cursor   string
	seq      int
	Fail     func(point string) error
	// Calls counts store exchanges by kind.
	Calls map[string]int
}

type memNote struct {
	id   string
	note sprint.Note
}

type memTable struct {
	def     ntable.Table
	rows    []string
	texts   map[string]map[string]string
	rev     uint64
	members map[string]*memMember
	ops     map[string]memOp
}

type memMember struct {
	placed   bool
	row, col string
	score    float64
	rev      uint64
	fields   map[string]string
}

type memOp struct {
	body    string
	receipt ntable.Receipt
}

// NewMem is an empty store.
func NewMem() *Mem {
	return &Mem{tables: map[string]*memTable{}, dropped: map[string]*memResidue{}, views: map[string]ntable.View{}, done: map[string]string{}, progress: map[string]time.Time{},
		notes: map[string]sprint.Note{}, open: map[string]string{}, Calls: map[string]int{}}
}

var errLost = errors.New("the store did not answer")

func (m *Mem) fail(point string) error {
	if m.Fail == nil {
		return nil
	}
	if err := m.Fail(point); err != nil {
		return fmt.Errorf("%s: %w: %w", point, errLost, err)
	}
	return nil
}

func refusal(code, sentence string) error {
	return &ntable.Refusal{Code: code, Location: "mem", Sentence: sentence, Next: "nova-sprint check", Guarded: true}
}

func (m *Mem) table(name string) (*memTable, error) {
	t := m.tables[name]
	if t == nil {
		return nil, refusal("NOTABLE", "no such table "+name)
	}
	return t, nil
}

func (m *Mem) Shapes(_ context.Context, tables []string) ([]ntable.Table, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["shapes"]++
	out := make([]ntable.Table, len(tables))
	for i, name := range tables {
		t, err := m.table(name)
		if err != nil {
			return nil, err
		}
		s := t.def
		s.Revision = t.rev
		s.Rows = nil
		for _, r := range t.rows {
			row := ntable.NewRow(s, r)
			row.Texts = map[string]string{}
			for k, v := range t.texts[r] {
				row.Texts[k] = v
			}
			for j, c := range s.Columns {
				if c.HasSet() {
					for _, mm := range t.members {
						if mm.placed && mm.row == r && mm.col == c.Name {
							row.Cells[j].Count++
						}
					}
				}
			}
			s.Rows = append(s.Rows, row)
		}
		out[i] = s
	}
	return out, nil
}

func (m *Mem) CellIDs(_ context.Context, shapes []ntable.Table) (map[string][]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["cells"]++
	out := map[string][]string{}
	for _, s := range shapes {
		t, err := m.table(s.Name)
		if err != nil {
			return nil, err
		}
		rows := map[string]bool{}
		for _, r := range s.Rows {
			rows[r.Key] = true
		}
		for id, mm := range t.members {
			if mm.placed && rows[mm.row] {
				out[s.Name] = append(out[s.Name], id)
			}
		}
		sort.Strings(out[s.Name])
	}
	return out, nil
}

func (m *Mem) ReadSet(_ context.Context, table string, ids []string) (ntable.ReadSetResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["readset"]++
	if len(ids) == 0 || len(ids) > ntable.LimitReadSetMembers {
		return ntable.ReadSetResult{}, refusal("LIMIT", fmt.Sprintf("read set members: bound %d, observed %d", ntable.LimitReadSetMembers, len(ids)))
	}
	t, err := m.table(table)
	if err != nil {
		return ntable.ReadSetResult{}, err
	}
	res := ntable.ReadSetResult{Table: table, Revision: t.rev}
	for _, id := range ids {
		mm := t.members[id]
		if mm == nil {
			res.Missing = append(res.Missing, id)
			continue
		}
		f := map[string]string{}
		for k, v := range mm.fields {
			f[k] = v
		}
		rm := ntable.ReadSetMember{ID: id, Revision: mm.rev, Placed: mm.placed, Fields: f}
		if mm.placed {
			rm.Row, rm.Col, rm.Score = mm.row, mm.col, mm.score
			rm.ScoreText = strconv.FormatFloat(mm.score, 'g', -1, 64)
		}
		res.Members = append(res.Members, rm)
	}
	return res, nil
}

func (t *memTable) owned(row, col string) bool {
	found := false
	for _, r := range t.rows {
		found = found || r == row
	}
	if !found {
		return false
	}
	for _, c := range t.def.Columns {
		if c.Name == col {
			return c.HasSet()
		}
	}
	return false
}

// Apply is ns_table_apply in memory.
func (m *Mem) Apply(_ context.Context, man ntable.BatchManifest) (ntable.Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["apply"]++
	if man.Members == nil {
		man.Members = []ntable.BatchMemberEntry{}
	}
	raw, err := json.Marshal(man)
	if err != nil {
		return ntable.Receipt{}, err
	}
	if _, verr := ntable.ValidateBatchManifestRaw(raw); verr != nil {
		var re *ntable.RuleError
		var le *ntable.LimitError
		switch {
		case errors.As(verr, &le):
			return ntable.Receipt{}, refusal("LIMIT", le.Error())
		case errors.As(verr, &re):
			return ntable.Receipt{}, refusal(re.Code, re.Msg)
		}
		return ntable.Receipt{}, fmt.Errorf("%w: %v", ntable.ErrMalformedManifest, verr)
	}
	if err := m.fail("apply " + man.Table + " before"); err != nil {
		return ntable.Receipt{}, fmt.Errorf("%w: %w", ntable.ErrUnknownOutcome, err)
	}
	t, err := m.table(man.Table)
	if err != nil {
		return ntable.Receipt{}, err
	}
	body := string(raw)
	opKey := man.Epoch + ":" + man.OperationID
	if rec, ok := t.ops[opKey]; ok {
		if rec.body != body {
			return ntable.Receipt{}, refusal("OPCONFLICT", "operation "+man.OperationID+" already holds a different request")
		}
		r := rec.receipt
		r.Replay = true
		return r, nil
	}
	if man.Epoch != "0" {
		return ntable.Receipt{}, refusal("STALE", "requested epoch "+man.Epoch+", active 0")
	}
	if man.ExpectedTableRevision != strconv.FormatUint(t.rev, 10) {
		return ntable.Receipt{}, refusal("REVISION", fmt.Sprintf("table revision mismatch: expected %s, observed %d", man.ExpectedTableRevision, t.rev))
	}
	for _, e := range man.Members {
		if err := t.judge(e); err != nil {
			return ntable.Receipt{}, err
		}
	}
	delta := &ntable.BatchDelta{OperationID: man.OperationID, Actor: man.Actor, SelectedCount: len(man.Members)}
	for _, e := range man.Members {
		d, changed := t.commit(e)
		if changed {
			delta.ChangedCount++
		} else if e.Create == nil && e.Move == nil && !e.Remove && len(e.Set) == 0 && len(e.Unset) == 0 {
			delta.GuardCount++
		}
		delta.Members = append(delta.Members, d)
	}
	before := t.rev
	t.rev++
	outcome := "changed"
	if delta.ChangedCount == 0 {
		outcome = "noop"
	}
	m.seq++
	r := ntable.Receipt{ID: fmt.Sprintf("%d-0", m.seq), Epoch: 0, Before: before, After: t.rev, Outcome: outcome, BatchDelta: delta}
	t.ops[opKey] = memOp{body: body, receipt: r}
	if err := m.fail("apply " + man.Table + " after"); err != nil {
		return ntable.Receipt{}, fmt.Errorf("%w: %w", ntable.ErrUnknownOutcome, err)
	}
	return r, nil
}

func (t *memTable) judge(e ntable.BatchMemberEntry) error {
	mm := t.members[e.ID]
	x := e.Expect
	if x != nil && x.Absent {
		if mm != nil {
			return refusal("MEMBEREXISTS", "member "+e.ID+": expected absent, observed a record")
		}
		if !t.owned(e.Create.Row, e.Create.Col) {
			return refusal("NOCOL", "member "+e.ID+": no owned cell "+e.Create.Row+":"+e.Create.Col)
		}
		return nil
	}
	if mm == nil {
		return refusal("NOTMEMBER", "member "+e.ID+": no member record")
	}
	if x != nil {
		if x.Revision != "" && x.Revision != strconv.FormatUint(mm.rev, 10) {
			return refusal("MEMBERREVISION", fmt.Sprintf("member %s: revision expected %s, observed %d", e.ID, x.Revision, mm.rev))
		}
		if x.Place != nil && (!mm.placed || mm.row != x.Place.Row || mm.col != x.Place.Col) {
			return refusal("PLACEGUARD", fmt.Sprintf("member %s: expected place %s:%s, observed %s:%s", e.ID, x.Place.Row, x.Place.Col, mm.row, mm.col))
		}
		for name, g := range x.Fields {
			v, ok := mm.fields[name]
			switch {
			case g.Equals != nil && (!ok || v != *g.Equals),
				g.Absent != nil && ok,
				g.OneOf != nil && (!ok || !containsStr(g.OneOf, v)):
				return refusal("FIELDGUARD", "member "+e.ID+" field "+name)
			}
		}
	}
	if (e.Move != nil || e.Remove) && !mm.placed {
		return refusal("NOTMEMBER", "member "+e.ID+": record without placement")
	}
	if e.Move != nil && !t.owned(e.Move.Row, e.Move.Col) {
		return refusal("NOCOL", "member "+e.ID+": no owned cell "+e.Move.Row+":"+e.Move.Col)
	}
	return nil
}

func containsStr(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func place(mm *memMember) string {
	if mm == nil || !mm.placed {
		return ""
	}
	return mm.row + ":" + mm.col
}

func (t *memTable) commit(e ntable.BatchMemberEntry) (ntable.BatchMemberDelta, bool) {
	mm := t.members[e.ID]
	d := ntable.BatchMemberDelta{ID: e.ID, BeforePlace: place(mm)}
	if mm == nil {
		mm = &memMember{fields: map[string]string{}}
		t.members[e.ID] = mm
	}
	d.BeforeRev = strconv.FormatUint(mm.rev, 10)
	changed := false
	switch {
	case e.Create != nil:
		mm.placed, mm.row, mm.col, mm.score, changed = true, e.Create.Row, e.Create.Col, e.Create.Score, true
	case e.Move != nil:
		if mm.row != e.Move.Row || mm.col != e.Move.Col {
			mm.row, mm.col, changed = e.Move.Row, e.Move.Col, true
		}
		if e.Move.Score != nil && *e.Move.Score != mm.score {
			mm.score, changed = *e.Move.Score, true
		}
	case e.Remove:
		mm.placed, mm.row, mm.col, changed = false, "", "", true
	}
	for k, v := range e.Set {
		if old, ok := mm.fields[k]; !ok || old != v {
			mm.fields[k], changed = v, true
		}
	}
	for _, k := range e.Unset {
		if _, ok := mm.fields[k]; ok {
			delete(mm.fields, k)
			changed = true
		}
	}
	if changed {
		mm.rev++
	}
	d.AfterPlace, d.AfterRev = place(mm), strconv.FormatUint(mm.rev, 10)
	return d, changed
}

func (m *Mem) Create(_ context.Context, t ntable.Table) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["create"]++
	if old, ok := m.tables[t.Name]; ok {
		if ntable.SameDefinition(old.def, t) {
			return nil
		}
		return refusal("EXISTS", "table "+t.Name+" exists with another definition")
	}
	if err := ntable.ValidateColumns(t.Columns); err != nil {
		return err
	}
	def := t
	def.Rows = nil
	m.tables[t.Name] = &memTable{def: def, texts: map[string]map[string]string{}, members: map[string]*memMember{}, ops: map[string]memOp{}}
	m.takeResidue(m.tables[t.Name])
	return nil
}

func (m *Mem) RowsAdd(_ context.Context, table string, rows []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["rows"]++
	t, err := m.table(table)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if !containsStr(t.rows, r) {
			t.rows = append(t.rows, r)
		}
	}
	t.rev++
	return nil
}

func (m *Mem) RowSet(_ context.Context, table, row string, texts map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["rowset"]++
	t, err := m.table(table)
	if err != nil {
		return err
	}
	if !containsStr(t.rows, row) {
		return refusal("NOROW", "no row "+row)
	}
	if t.texts[row] == nil {
		t.texts[row] = map[string]string{}
	}
	for k, v := range texts {
		t.texts[row][k] = v
	}
	t.rev++
	return nil
}

func (m *Mem) ViewSet(_ context.Context, v ntable.View) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range v.Tables {
		if _, err := m.table(t); err != nil {
			return err
		}
	}
	m.views[v.Name] = v
	return nil
}

func (m *Mem) ViewDelete(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.views, name)
	return nil
}

func (m *Mem) DropTable(_ context.Context, table string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keepResidue(table)
	delete(m.tables, table)
	return nil
}

// CheckTable finds a member placed on a row the table does not declare.
func (m *Mem) CheckTable(_ context.Context, table string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return err
	}
	for id, mm := range t.members {
		if mm.placed && !t.owned(mm.row, mm.col) {
			return fmt.Errorf("table %s: %w: %s at %s:%s", table, ntable.ErrDrift, id, mm.row, mm.col)
		}
	}
	return nil
}

func (m *Mem) ReadFence(context.Context) (Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["fence"]++
	if err := m.fail("fence"); err != nil {
		return Fence{}, err
	}
	f := Fence{Gen: m.gen}
	if m.fence != nil {
		op := *m.fence
		f.Pending = &op
	}
	return f, nil
}

func (m *Mem) Acquire(_ context.Context, gen uint64, op OpRecord) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["acquire"]++
	if err := m.fail("acquire"); err != nil {
		return false, err
	}
	if m.fence != nil || m.gen != gen {
		return false, nil
	}
	body, err := json.Marshal(op)
	if err != nil {
		return false, err
	}
	var copyOp OpRecord
	if err := json.Unmarshal(body, &copyOp); err != nil {
		return false, err
	}
	m.fence = &copyOp
	m.gen++
	return true, nil
}

func (m *Mem) Release(_ context.Context, op OpRecord, commit bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["release"]++
	if err := m.fail("release"); err != nil {
		return err
	}
	if m.fence == nil || m.fence.ID != op.ID {
		return nil
	}
	if commit {
		for _, n := range append(append([]sprint.Note{}, op.Notes...), op.Decided...) {
			// every subject stays open; only the note's listing is bounded
			subjects := n.Subjects()
			n = n.Bound()
			m.seq++
			m.inbox = append(m.inbox, memNote{fmt.Sprintf("%d-0", m.seq), n})
			if n.Kind == sprint.Judgment {
				m.notes[n.ID] = n
				for _, s := range subjects {
					m.open[sprint.OpenKey(n.ID, s)] = n.ID
				}
			}
		}
		for _, k := range op.Closes {
			delete(m.open, k)
		}
		for _, s := range op.Streams {
			m.progress[s] = op.At
		}
		if op.CallerOp != "" {
			m.done[op.CallerOp] = op.Result
		}
	}
	m.fence = nil
	return nil
}

func (m *Mem) Done(_ context.Context, callerOp string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.done[callerOp]
	return v, ok, nil
}

func (m *Mem) SetReview(_ context.Context, noteID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.notes[noteID]
	if !ok {
		return fmt.Errorf("no judgment %s; run: nova-sprint inbox", noteID)
	}
	n.Review = at
	m.notes[noteID] = n
	return nil
}

func (m *Mem) Progress(context.Context) (map[string]time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]time.Time{}
	for k, v := range m.progress {
		out[k] = v
	}
	return out, nil
}

// Pending is the operation the fence holds, for tests.
func (m *Mem) Pending() *OpRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fence
}

func (m *Mem) OpenNotes(context.Context) ([]sprint.Open, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []sprint.Open
	for k, nid := range m.open {
		out = append(out, sprint.Open{Key: k, Note: m.notes[nid]})
	}
	sortOpen(out)
	return out, nil
}

func sortOpen(out []sprint.Open) {
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Note.At.Equal(out[j].Note.At) {
			return out[i].Note.At.Before(out[j].Note.At)
		}
		return out[i].Key < out[j].Key
	})
}

func (m *Mem) NotesSince(_ context.Context, after string, max int) ([]sprint.Note, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var notes []sprint.Note
	var ids []string
	past := after == ""
	for _, n := range m.inbox {
		if !past {
			past = n.id == after
			continue
		}
		if len(notes) == max {
			break
		}
		notes = append(notes, n.note)
		ids = append(ids, n.id)
	}
	return notes, ids, nil
}

func (m *Mem) Cursor(context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cursor, nil
}

func (m *Mem) SetCursor(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cursor = id
	return nil
}

func (m *Mem) Coordinator(context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.notes[coordinatorField].What, nil
}

func (m *Mem) SetCoordinator(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notes[coordinatorField] = sprint.Note{ID: coordinatorField, What: name}
	return nil
}

// Record is a member's record as the store holds it, for tests.
func (m *Mem) Record(table, id string) (row, col string, rev uint64, fields map[string]string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tables[table]
	if t == nil || t.members[id] == nil {
		return "", "", 0, nil, false
	}
	mm := t.members[id]
	return mm.row, mm.col, mm.rev, mm.fields, true
}

// Revision is a table's revision, for tests.
func (m *Mem) Revision(table string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := m.tables[table]; t != nil {
		return t.rev
	}
	return 0
}
