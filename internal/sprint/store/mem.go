package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	*memState
	epoch uint64 // the epoch this view is pinned to: its sprint keys and writes
	old   bool   // reads of the tables read the pinned epoch, not the active one
}

type memState struct {
	mu      sync.Mutex
	tables  map[string]*memTable
	dropped map[string]*memResidue // what a drop keeps of a table, as the table layer's does
	views   map[string]ntable.View
	// the sprint's epoch hash (Names.EpochKey): held once it has been written
	epochSet bool
	epochN   uint64
	cleared  time.Time
	owed     bool
	logs     map[uint64]*memLog // the sprint's keys, per epoch
	kv       map[string]string  // the machine's records (tick.go)
	seq      int
	touched  map[uint64]int // store calls that named each epoch, for tests
	Fail     func(point string) error
	// MaxWrite, when above zero, is the largest operation record the store
	// takes in one write, as a store's bulk length bound: a larger one is
	// not written and the write fails as the connection closing does.
	MaxWrite int
	// Calls counts store exchanges by kind.
	Calls map[string]int
	// LogWait, when set, is how WaitLog waits when the log holds no line
	// after its cursor: it is handed the time the wait may take and returns
	// when it has passed (a test's clock steps by it, or appends a line). Nil
	// waits on the wall clock for a line or the time, whichever comes first.
	LogWait func(d time.Duration)
	logged  chan struct{}       // closed, and replaced, by every commit that appends to a log
	routes  []sprint.Route      // the model tiers' routes (routes.go)
	tiers   map[string][]string // the tiers' route arrays (routes.go)
}

// memLog is one epoch's sprint keys.
type memLog struct {
	fence    *OpRecord
	gen      uint64
	done     map[string]string
	progress map[string]time.Time
	inbox    []memNote
	lines    []memLine // the log
	notes    map[string]sprint.Note
	open     map[string]string
	cursor   string
	queue    []sprint.QueuedChange // the work table's queue, oldest first
}

type memNote struct {
	id   string
	note sprint.Note
}

type memLine struct {
	id   string
	line sprint.Line
}

type memTable struct {
	def     ntable.Table
	epochs  map[uint64]*memEpoch // rows and text cells, per epoch
	wrote   map[uint64]bool      // the epochs written: the table layer's definition snapshots
	rev     uint64
	members map[string]*memMember
	ops     map[string]memOp
	// changes is the table's change stream: each write's revisions, epoch,
	// verb and the records it named (TableChanges).
	changes []memChange
}

// memChange is one event of a table's change stream.
type memChange struct {
	epoch, before, after uint64
	verb                 string
	ids                  []string
}

// memEpoch is a table's rows, text cells and properties at one epoch.
type memEpoch struct {
	rows  []string
	texts map[string]map[string]string
	props map[string]string // the table's properties (docs/SPEC-NOVA-TABLE.md)
}

type memMember struct {
	epoch    uint64 // the epoch the record belongs to
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
	return &Mem{memState: &memState{tables: map[string]*memTable{}, dropped: map[string]*memResidue{}, views: map[string]ntable.View{},
		logs: map[uint64]*memLog{}, kv: map[string]string{}, Calls: map[string]int{}, touched: map[uint64]int{}}}
}

// AtEpoch is the store pinned to an epoch.
func (m *Mem) AtEpoch(epoch uint64, old bool) Backend {
	return &Mem{memState: m.memState, epoch: epoch, old: old}
}

// touch counts a store call naming the epoch. The caller holds m.mu.
func (m *Mem) touch(e uint64) { m.touched[e]++ }

// Touched is how many store calls named the epoch: its sprint keys, a read of
// it as it was, or a write at it. For tests.
func (m *Mem) Touched(e uint64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.touched[e]
}

// readable refuses a read of an earlier epoch as the table layer does: an
// epoch it holds no definition of (none is kept until the epoch's first
// write) is NOTABLE, and one after the active epoch is EPOCHAHEAD. The caller
// holds m.mu.
func (m *Mem) readable(t *memTable) error {
	if !m.old {
		return nil
	}
	m.touch(m.epoch)
	a := m.active(t)
	switch {
	case m.epoch > a:
		return refusal("EPOCHAHEAD", fmt.Sprintf("requested epoch %d, active %d", m.epoch, a))
	case m.epoch != a && !t.wrote[m.epoch]:
		return refusal("NOTABLE", fmt.Sprintf("no such table %s at epoch %d", t.def.Name, m.epoch))
	}
	return nil
}

// log is the pinned epoch's sprint keys. The caller holds m.mu.
func (m *Mem) log() *memLog {
	m.touch(m.epoch)
	l := m.logs[m.epoch]
	if l == nil {
		l = &memLog{done: map[string]string{}, progress: map[string]time.Time{}, notes: map[string]sprint.Note{}, open: map[string]string{}}
		m.logs[m.epoch] = l
	}
	return l
}

// active is a table's active epoch: the sprint's, when it is bound to it.
func (m *Mem) active(t *memTable) uint64 {
	if t.def.EpochKey == "" {
		return 0
	}
	return m.epochN
}

// read is the epoch a read of the table sees.
func (m *Mem) read(t *memTable) uint64 {
	if m.old {
		return m.epoch
	}
	return m.active(t)
}

func (t *memTable) at(e uint64) *memEpoch {
	ep := t.epochs[e]
	if ep == nil {
		ep = &memEpoch{texts: map[string]map[string]string{}}
		t.epochs[e] = ep
	}
	return ep
}

func (m *Mem) Epoch(context.Context) (EpochState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return EpochState{N: m.epochN, Cleared: m.cleared, Owed: m.owed}, nil
}

func (m *Mem) AdvanceEpoch(_ context.Context, from uint64, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("advance"); err != nil {
		return false, err
	}
	if m.epochN != from {
		return false, nil
	}
	m.epochSet, m.epochN, m.cleared, m.owed = true, from+1, at, true
	return true, nil
}

func (m *Mem) SettleEpoch(_ context.Context, n uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.epochN == n {
		m.owed = false
	}
	return nil
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
		if err := m.readable(t); err != nil {
			return nil, err
		}
		e := m.read(t)
		s := t.def
		s.Revision, s.Epoch = t.rev, e
		s.Rows = nil
		ep := t.at(e)
		s.Props = nil
		if len(ep.props) > 0 {
			s.Props = make(map[string]string, len(ep.props))
			for k, v := range ep.props {
				s.Props[k] = v
			}
		}
		for _, r := range ep.rows {
			row := ntable.NewRow(s, r)
			row.Texts = map[string]string{}
			for k, v := range ep.texts[r] {
				row.Texts[k] = v
			}
			for j, c := range s.Columns {
				if c.HasSet() {
					for _, mm := range t.members {
						if mm.epoch == e && mm.placed && mm.row == r && mm.col == c.Name {
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
			if mm.epoch == s.Epoch && mm.placed && rows[mm.row] {
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
	if err := m.fail("readset " + table); err != nil {
		return ntable.ReadSetResult{}, err
	}
	if len(ids) == 0 || len(ids) > ntable.LimitReadSetMembers {
		return ntable.ReadSetResult{}, refusal("LIMIT", fmt.Sprintf("read set members: bound %d, observed %d", ntable.LimitReadSetMembers, len(ids)))
	}
	t, err := m.table(table)
	if err != nil {
		return ntable.ReadSetResult{}, err
	}
	if err := m.readable(t); err != nil {
		return ntable.ReadSetResult{}, err
	}
	e := m.read(t)
	res := ntable.ReadSetResult{Table: table, Revision: t.rev, Epoch: e}
	for _, id := range ids {
		mm := t.members[id]
		if mm != nil && mm.epoch != e {
			return ntable.ReadSetResult{}, refusal("MEMBEREPOCH", fmt.Sprintf("member %s belongs to epoch %d, the read is at %d", id, mm.epoch, e))
		}
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

func (t *memTable) owned(e uint64, row, col string) bool {
	if !containsStr(t.at(e).rows, row) {
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
	active := m.active(t)
	req, perr := strconv.ParseUint(man.Epoch, 10, 64)
	if perr == nil {
		m.touch(req)
	}
	if perr != nil || req < active {
		return ntable.Receipt{}, refusal("STALE", fmt.Sprintf("requested epoch %s, active %d", man.Epoch, active))
	} else if req > active {
		return ntable.Receipt{}, refusal("EPOCHAHEAD", fmt.Sprintf("requested epoch %s, active %d", man.Epoch, active))
	}
	if man.ExpectedTableRevision != strconv.FormatUint(t.rev, 10) {
		return ntable.Receipt{}, refusal("REVISION", fmt.Sprintf("table revision mismatch: expected %s, observed %d", man.ExpectedTableRevision, t.rev))
	}
	props := t.at(active).props
	for name, v := range man.PropExpect {
		if cur, ok := props[name]; !ok || cur != v {
			return ntable.Receipt{}, refusal("PROPGUARD", "table "+man.Table+" property "+name)
		}
	}
	for _, name := range man.PropAbsent {
		if _, ok := props[name]; ok {
			return ntable.Receipt{}, refusal("PROPGUARD", "table "+man.Table+" property "+name)
		}
	}
	changedProps := map[string]string{}
	held := len(props)
	for name, v := range man.Props {
		if cur, ok := props[name]; !ok || cur != v {
			if !ok {
				held++
			}
			changedProps[name] = v
		}
	}
	if held > ntable.LimitTableProps {
		return ntable.Receipt{}, refusal("LIMIT", fmt.Sprintf("properties per table: bound %d, observed %d", ntable.LimitTableProps, held))
	}
	for _, e := range man.Members {
		if err := t.judge(active, e); err != nil {
			return ntable.Receipt{}, err
		}
	}
	delta := &ntable.BatchDelta{OperationID: man.OperationID, Actor: man.Actor, SelectedCount: len(man.Members)}
	if len(changedProps) > 0 {
		ep := t.at(active)
		if ep.props == nil {
			ep.props = map[string]string{}
		}
		for name, v := range changedProps {
			ep.props[name] = v
		}
		delta.Props = changedProps
	}
	for _, e := range man.Members {
		d, changed := t.commit(active, e)
		if changed {
			delta.ChangedCount++
		} else if e.Create == nil && e.Move == nil && !e.Remove && len(e.Set) == 0 && len(e.Unset) == 0 {
			delta.GuardCount++
		}
		delta.Members = append(delta.Members, d)
	}
	before := t.rev
	t.rev++
	t.wrote[active] = true
	ids := make([]string, len(man.Members))
	for i, e := range man.Members {
		ids[i] = e.ID
	}
	t.changes = append(t.changes, memChange{epoch: active, before: before, after: t.rev, verb: "apply", ids: ids})
	outcome := "changed"
	if delta.ChangedCount == 0 && len(changedProps) == 0 {
		outcome = "noop"
	}
	m.seq++
	r := ntable.Receipt{ID: fmt.Sprintf("%d-0", m.seq), Epoch: active, Before: before, After: t.rev, Outcome: outcome, BatchDelta: delta}
	t.ops[opKey] = memOp{body: body, receipt: r}
	if err := m.fail("apply " + man.Table + " after"); err != nil {
		return ntable.Receipt{}, fmt.Errorf("%w: %w", ntable.ErrUnknownOutcome, err)
	}
	return r, nil
}

func (t *memTable) judge(active uint64, e ntable.BatchMemberEntry) error {
	mm := t.members[e.ID]
	if mm != nil && mm.epoch != active {
		return refusal("MEMBEREPOCH", fmt.Sprintf("member %s belongs to epoch %d, the active epoch is %d", e.ID, mm.epoch, active))
	}
	x := e.Expect
	if x != nil && x.Absent {
		if mm != nil {
			return refusal("MEMBEREXISTS", "member "+e.ID+": expected absent, observed a record")
		}
		if !t.owned(active, e.Create.Row, e.Create.Col) {
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
	if e.Move != nil && !t.owned(active, e.Move.Row, e.Move.Col) {
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

func (t *memTable) commit(active uint64, e ntable.BatchMemberEntry) (ntable.BatchMemberDelta, bool) {
	mm := t.members[e.ID]
	d := ntable.BatchMemberDelta{ID: e.ID, BeforePlace: place(mm)}
	if mm == nil {
		mm = &memMember{epoch: active, fields: map[string]string{}}
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
	if mm.placed {
		// the score as the store's receipt gives it: parsed, and its text
		score, text := mm.score, strconv.FormatFloat(mm.score, 'f', -1, 64)
		d.AfterScore, d.AfterScoreText = &score, &text
	}
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
	mt := &memTable{def: def, epochs: map[uint64]*memEpoch{}, wrote: map[uint64]bool{}, members: map[string]*memMember{}, ops: map[string]memOp{}}
	m.tables[t.Name] = mt
	m.takeResidue(mt)
	mt.wrote[m.active(mt)] = true
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
	if err := m.writeEpoch(t); err != nil {
		return err
	}
	ep := t.at(m.active(t))
	for _, r := range rows {
		if !containsStr(ep.rows, r) {
			ep.rows = append(ep.rows, r)
		}
	}
	t.rev++
	t.wrote[m.active(t)] = true
	t.changes = append(t.changes, memChange{epoch: m.active(t), before: t.rev - 1, after: t.rev, verb: "rows_add"})
	return nil
}

// RowsDel removes rows and unplaces the cards in them, as the table layer's row
// delete does, under RowsAdd's epoch check.
func (m *Mem) RowsDel(_ context.Context, table string, rows []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["rowsdel"]++
	t, err := m.table(table)
	if err != nil {
		return err
	}
	if err := m.writeEpoch(t); err != nil {
		return err
	}
	ep := t.at(m.active(t))
	for _, r := range rows {
		i := slices.Index(ep.rows, r)
		if i < 0 {
			continue
		}
		ep.rows = slices.Delete(ep.rows, i, i+1)
		delete(ep.texts, r)
		for _, mm := range t.members {
			if mm.placed && mm.epoch == m.active(t) && mm.row == r {
				mm.placed, mm.row, mm.col = false, "", ""
				mm.rev++
			}
		}
	}
	t.rev++
	t.wrote[m.active(t)] = true
	t.changes = append(t.changes, memChange{epoch: m.active(t), before: t.rev - 1, after: t.rev, verb: "row_del"})
	return nil
}

// writeEpoch refuses a write pinned to an epoch that is not the table's
// active one, as the table layer refuses a stale epoch.
func (m *Mem) writeEpoch(t *memTable) error {
	m.touch(m.epoch)
	if a := m.active(t); m.epoch != a {
		code := "STALE"
		if m.epoch > a {
			code = "EPOCHAHEAD"
		}
		return refusal(code, fmt.Sprintf("requested epoch %d, active %d", m.epoch, a))
	}
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
	if err := m.writeEpoch(t); err != nil {
		return err
	}
	ep := t.at(m.active(t))
	if !containsStr(ep.rows, row) {
		return refusal("NOROW", "no row "+row)
	}
	if ep.texts[row] == nil {
		ep.texts[row] = map[string]string{}
	}
	for k, v := range texts {
		ep.texts[row][k] = v
	}
	t.rev++
	t.wrote[m.active(t)] = true
	t.changes = append(t.changes, memChange{epoch: m.active(t), before: t.rev - 1, after: t.rev, verb: "row_set"})
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
	// A view set leaves the view's state as it is, as the table layer's does.
	v.State = m.views[v.Name].State
	m.views[v.Name] = v
	return nil
}

// View is a stored view as the store holds it, and whether it is there.
func (m *Mem) View(name string) (ntable.View, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.views[name]
	return v, ok
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
		if mm.epoch == m.active(t) && mm.placed && !t.owned(mm.epoch, mm.row, mm.col) {
			return fmt.Errorf("table %s: %w: %s at %s:%s", table, ntable.ErrDrift, id, mm.row, mm.col)
		}
	}
	return nil
}

func (m *Mem) ReadFence(context.Context) (Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	m.Calls["fence"]++
	if err := m.fail("fence"); err != nil {
		return Fence{}, err
	}
	f := Fence{Gen: l.gen, Queued: len(l.queue)}
	if raw, ok := m.kv[keyMachine]; ok {
		var mc Machine
		f.Running = json.Unmarshal([]byte(raw), &mc) == nil && mc.Running()
	}
	f.Stuck = m.kv[keyStuck]
	if l.fence != nil {
		op := *l.fence
		f.Pending = &op
	}
	return f, nil
}

func (m *Mem) Acquire(_ context.Context, gen uint64, op OpRecord) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	m.Calls["acquire"]++
	if err := m.fail("acquire"); err != nil {
		return false, err
	}
	if l.fence != nil || l.gen != gen || m.epoch != m.epochN {
		return false, nil
	}
	body, err := json.Marshal(op)
	if err != nil {
		return false, err
	}
	if m.MaxWrite > 0 && len(body) > m.MaxWrite {
		return false, errors.New("write tcp: write: broken pipe")
	}
	var copyOp OpRecord
	if err := json.Unmarshal(body, &copyOp); err != nil {
		return false, err
	}
	l.fence = &copyOp
	l.gen++
	return true, nil
}

func (m *Mem) Release(_ context.Context, op OpRecord, commit bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	m.Calls["release"]++
	if err := m.fail("release"); err != nil {
		return err
	}
	if l.fence == nil || l.fence.ID != op.ID {
		return nil
	}
	if commit {
		if op.Drain > 0 {
			l.queue = append([]sprint.QueuedChange(nil), l.queue[min(op.Drain, len(l.queue)):]...)
		}
		l.queue = append(l.queue, op.Queue...)
		for _, line := range op.Log {
			m.seq++
			l.lines = append(l.lines, memLine{fmt.Sprintf("%d-0", m.seq), line})
		}
		for _, n := range append(append([]sprint.Note{}, op.Notes...), op.Decided...) {
			// every subject stays open; only the note's listing is bounded
			subjects := n.Subjects()
			n = n.Bound()
			m.seq++
			l.lines = append(l.lines, memLine{fmt.Sprintf("%d-0", m.seq), sprint.NoteLine(n, op.ID)})
			m.seq++
			l.inbox = append(l.inbox, memNote{fmt.Sprintf("%d-0", m.seq), n})
			if n.Kind == sprint.Judgment || n.Kind == sprint.Acknowledged {
				l.notes[n.ID] = n
				for _, s := range subjects {
					l.open[sprint.OpenKey(n.ID, s)] = n.ID
				}
			}
		}
		for _, n := range op.Updates {
			n = n.Bound()
			l.notes[n.ID] = n
			line := sprint.NoteLine(n, op.ID)
			line.Verb = "updated"
			m.seq++
			l.lines = append(l.lines, memLine{fmt.Sprintf("%d-0", m.seq), line})
		}
		for _, k := range op.Closes {
			delete(l.open, k)
		}
		m.wakeLog()
		if op.Stuck != "" {
			delete(m.kv, keyStuck)
		}
		for _, s := range op.Streams {
			l.progress[s] = op.At
		}
		if op.CallerOp != "" {
			l.done[op.CallerOp] = op.Result
		}
	}
	l.fence = nil
	return nil
}

// QueueRead is the pinned epoch's work-table queue.
func (m *Mem) QueueRead(context.Context) ([]sprint.QueuedChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["queue"]++
	if err := m.fail("queue"); err != nil {
		return nil, err
	}
	return append([]sprint.QueuedChange(nil), m.log().queue...), nil
}

func (m *Mem) Done(_ context.Context, callerOp string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("done")
	l := m.log()
	v, ok := l.done[callerOp]
	return v, ok, nil
}

func (m *Mem) DoneBefore(_ context.Context, callerOp string, before uint64) (uint64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for e := before; e > 0; e-- {
		m.touch(e - 1)
		if l := m.logs[e-1]; l != nil {
			if _, ok := l.done[callerOp]; ok {
				return e - 1, true, nil
			}
		}
	}
	return 0, false, nil
}

func (m *Mem) SetReview(_ context.Context, noteID string, at, set time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	n, ok := l.notes[noteID]
	if !ok {
		return fmt.Errorf("no judgment %s; run: nova-sprint inbox", noteID)
	}
	n.Review, n.ReviewSet = at, set
	l.notes[noteID] = n
	return nil
}

func (m *Mem) Progress(context.Context) (map[string]time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	out := map[string]time.Time{}
	for k, v := range l.progress {
		out[k] = v
	}
	return out, nil
}

// Pending is the operation the fence holds, for tests.
func (m *Mem) Pending() *OpRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	return l.fence
}

func (m *Mem) OpenNotes(context.Context) ([]sprint.Open, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("open")
	l := m.log()
	var out []sprint.Open
	for k, nid := range l.open {
		out = append(out, sprint.Open{Key: k, Note: l.notes[nid]})
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
	l := m.log()
	var notes []sprint.Note
	var ids []string
	past := after == ""
	for _, n := range l.inbox {
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

func (m *Mem) Tails(context.Context) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	var lg, in string
	if n := len(l.lines); n > 0 {
		lg = l.lines[n-1].id
	}
	if n := len(l.inbox); n > 0 {
		in = l.inbox[n-1].id
	}
	return lg, in, nil
}

func (m *Mem) LogSince(_ context.Context, after string, max int) ([]sprint.Line, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	var lines []sprint.Line
	var ids []string
	past := after == ""
	for _, x := range l.lines {
		if !past {
			past = x.id == after
			continue
		}
		if len(lines) == max {
			break
		}
		lines = append(lines, x.line)
		ids = append(ids, x.id)
	}
	return lines, ids, nil
}

func (m *Mem) Cursor(context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	return l.cursor, nil
}

func (m *Mem) SetCursor(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	l.cursor = id
	return nil
}

func (m *Mem) Coordinator(context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("coord")
	return m.kv[keyCoordinator], nil
}

func (m *Mem) SetCoordinator(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.kv == nil {
		m.kv = map[string]string{}
	}
	m.kv[keyCoordinator] = name
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

// TableChanges is the records the table's writes between two revisions
// named, from its change stream, as the table layer's (twin.go).
func (m *Mem) TableChanges(_ context.Context, table string, from, to uint64) ([]string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("changes")
	t := m.tables[table]
	if t == nil || to < from {
		return nil, false, nil
	}
	active := m.active(t)
	need := to
	var ids []string
	for i := len(t.changes) - 1; i >= 0 && need > from; i-- {
		c := t.changes[i]
		if c.epoch != active || c.after > to {
			continue
		}
		if c.after != need {
			return nil, false, nil
		}
		ids = append(ids, c.ids...)
		need = c.before
	}
	return ids, need == from, nil
}

var _ TableChanger = (*Mem)(nil)

// count counts one exchange of the kind (Calls), for a kind that did not count
// itself before; the lock is held.
func (m *Mem) count(kind string) {
	if m.Calls != nil {
		m.Calls[kind]++
	}
}

// Trips is the exchanges made so far, every kind (Calls): what a store's round
// trips are to the tick's cost (stats.go).
func (m *Mem) Trips() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.Calls {
		n += c
	}
	return int64(n)
}

var _ Tripper = (*Mem)(nil)
