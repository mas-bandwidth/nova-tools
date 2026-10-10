package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
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
	// LogLines counts the log lines LogSince has returned: a read that walks the log whole
	// shows here, where an in-memory page is one exchange however long the log is.
	LogLines int
	// LogWait, when set, is how WaitLog waits when the log holds no line
	// after its cursor: it is handed the time the wait may take and returns
	// when it has passed (a test's clock steps by it, or appends a line). Nil
	// waits on the wall clock for a line or the time, whichever comes first.
	LogWait func(d time.Duration)
	logged  chan struct{}       // closed, and replaced, by every commit that appends to a log
	routes  []sprint.Route      // the model tiers' routes (routes.go)
	tiers   map[string][]string // the tiers' route arrays (routes.go)
	bars    Bars                // the sprint row's nova-decide bars (routes.go)
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
	aliases  map[string]string // alias (j<n>) -> note id (sprint.Alias)
	answered map[string]string // judgment id -> who answered it, as it closed
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
	// mark is the event's own id, unique among every Mem's events (memMarks): a
	// store that lost its last writes and wrote others since gives a revision a new
	// event, as a Redis stream gives it a new id (TableChangesMarked)
	mark uint64
}

// memMarks numbers every Mem's change events.
var memMarks atomic.Uint64

// memEpoch is a table's rows, text cells and properties at one epoch.
type memEpoch struct {
	rows   []string
	hidden map[string]bool // the rows hidden (RowsHide)
	texts  map[string]map[string]string
	props  map[string]string // the table's properties (docs/SPEC-NOVA-TABLE.md)
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
		l = &memLog{done: map[string]string{}, progress: map[string]time.Time{}, notes: map[string]sprint.Note{}, aliases: map[string]string{}, open: map[string]string{}}
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
			maps.Copy(s.Props, ep.props)
		}
		for _, r := range ep.rows {
			row := ntable.NewRow(s, r)
			row.Hidden = ep.hidden[r]
			row.Texts = map[string]string{}
			maps.Copy(row.Texts, ep.texts[r])
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
		// a column the shape projects as text has no cell ids, as the store's
		// own cells read gives none (Redis.CellIDs)
		text := map[string]bool{}
		for _, c := range s.Columns {
			if !c.HasSet() {
				text[c.Name] = true
			}
		}
		for id, mm := range t.members {
			if mm.epoch == s.Epoch && mm.placed && rows[mm.row] && !text[mm.col] {
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
		maps.Copy(f, mm.fields)
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
	if !slices.Contains(t.at(e).rows, row) {
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
	body := string(raw)
	opKey := man.Epoch + ":" + man.OperationID
	t, err := m.table(man.Table)
	if err != nil {
		return ntable.Receipt{}, err
	}
	// Twin replay fidelity (security#78 finding 7): an operation already
	// recorded under this id replays or conflicts on its recorded bytes, before
	// any newer validator would refuse the re-sent request. This mirrors the real
	// store's op-record lookup in internal/nsprint/fn/lua/table.lua.
	if rec, ok := t.ops[opKey]; ok {
		if rec.body != body {
			return ntable.Receipt{}, refusal("OPCONFLICT", "operation "+man.OperationID+" already holds a different request")
		}
		r := rec.receipt
		r.Replay = true
		return r, nil
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
		maps.Copy(ep.props, changedProps)
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
	t.changes = append(t.changes, memChange{mark: memMarks.Add(1), epoch: active, before: before, after: t.rev, verb: "apply", ids: ids})
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
		if e.Create != nil && !t.owned(active, e.Create.Row, e.Create.Col) {
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
				g.OneOf != nil && (!ok || !slices.Contains(g.OneOf, v)):
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
		if !slices.Contains(ep.rows, r) {
			ep.rows = append(ep.rows, r)
		}
	}
	t.rev++
	t.wrote[m.active(t)] = true
	t.changes = append(t.changes, memChange{mark: memMarks.Add(1), epoch: m.active(t), before: t.rev - 1, after: t.rev, verb: "rows_add"})
	return nil
}

// RowsHide marks rows of the table hidden, as the table layer's row hide does, under
// RowsAdd's epoch check; a row the table does not have is skipped.
func (m *Mem) RowsHide(_ context.Context, table string, rows []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["rowshide"]++
	t, err := m.table(table)
	if err != nil {
		return err
	}
	if err := m.writeEpoch(t); err != nil {
		return err
	}
	ep := t.at(m.active(t))
	for _, r := range rows {
		if slices.Contains(ep.rows, r) {
			if ep.hidden == nil {
				ep.hidden = map[string]bool{}
			}
			ep.hidden[r] = true
		}
	}
	t.rev++
	t.wrote[m.active(t)] = true
	t.changes = append(t.changes, memChange{mark: memMarks.Add(1), epoch: m.active(t), before: t.rev - 1, after: t.rev, verb: "rows_hide"})
	return nil
}

// RowsShow draws hidden rows of the table again, as the table layer's row show
// does, under RowsAdd's epoch check; a row the table does not have is skipped.
func (m *Mem) RowsShow(_ context.Context, table string, rows []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["rowsshow"]++
	t, err := m.table(table)
	if err != nil {
		return err
	}
	if err := m.writeEpoch(t); err != nil {
		return err
	}
	ep := t.at(m.active(t))
	for _, r := range rows {
		delete(ep.hidden, r)
	}
	t.rev++
	t.wrote[m.active(t)] = true
	t.changes = append(t.changes, memChange{mark: memMarks.Add(1), epoch: m.active(t), before: t.rev - 1, after: t.rev, verb: "rows_show"})
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
	m.rowsDel(t, rows)
	return nil
}

// RowsDelIf removes each guard's row only while its record is on no cell at
// the guard's revision, checked and removed under the one lock, as RowsDel does.
func (m *Mem) RowsDelIf(_ context.Context, table string, guards []RowGuard) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["rowsdelif"]++
	t, err := m.table(table)
	if err != nil {
		return nil, err
	}
	if err := m.writeEpoch(t); err != nil {
		return nil, err
	}
	var rows []string
	for _, g := range guards {
		if mm := t.members[g.ID]; mm != nil && !mm.placed && mm.rev == g.Rev {
			rows = append(rows, g.Row)
		}
	}
	if len(rows) > 0 {
		m.rowsDel(t, rows)
	}
	return rows, nil
}

// KeysDelIf deletes each guard's keys only while its record is on no cell at the
// guard's revision and its row is not in the table, under the one lock.
func (m *Mem) KeysDelIf(_ context.Context, table string, guards []RowGuard) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["keysdelif"]++
	t, err := m.table(table)
	if err != nil {
		return nil, err
	}
	rows := t.at(m.active(t)).rows
	var out []string
	for _, g := range guards {
		if mm := t.members[g.ID]; mm == nil || mm.placed || mm.rev != g.Rev || slices.Contains(rows, g.Row) {
			continue
		}
		for _, k := range g.Keys {
			m.deleteKey(k)
		}
		out = append(out, g.Row)
	}
	return out, nil
}

// rowsDel is the row delete itself, under m.mu and the epoch check.
func (m *Mem) rowsDel(t *memTable, rows []string) {
	ep := t.at(m.active(t))
	// unplaced is the members the delete took off the table: the change names
	// them, as the table layer's change stream does (a twin catching up reads
	// them again)
	var unplaced []string
	for _, r := range rows {
		i := slices.Index(ep.rows, r)
		if i < 0 {
			continue
		}
		ep.rows = slices.Delete(ep.rows, i, i+1)
		delete(ep.texts, r)
		delete(ep.hidden, r)
		for id, mm := range t.members {
			if mm.placed && mm.epoch == m.active(t) && mm.row == r {
				mm.placed, mm.row, mm.col = false, "", ""
				mm.rev++
				unplaced = append(unplaced, id)
			}
		}
	}
	slices.Sort(unplaced)
	t.rev++
	t.wrote[m.active(t)] = true
	t.changes = append(t.changes, memChange{mark: memMarks.Add(1), epoch: m.active(t), before: t.rev - 1, after: t.rev, verb: "row_del", ids: unplaced})
}

// writeEpoch refuses a write pinned to an epoch that is not the table's
// active one, as the table layer refuses a stale epoch.
// Place puts a record of the active epoch that is on no cell back into an owned
// cell, as the table layer's cell add does; a record placed already, of another
// epoch, or absent is refused.
func (m *Mem) Place(_ context.Context, table, row, col, id string, score float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["place"]++
	t, err := m.table(table)
	if err != nil {
		return err
	}
	if err := m.writeEpoch(t); err != nil {
		return err
	}
	mm := t.members[id]
	switch {
	case mm == nil:
		return refusal("NOTMEMBER", "member "+id+": no member record")
	case mm.epoch != m.active(t):
		return refusal("MEMBEREPOCH", fmt.Sprintf("member %s belongs to epoch %d, the active epoch is %d", id, mm.epoch, m.active(t)))
	case mm.placed:
		return refusal("MEMBEREXISTS", "member "+id+": placed at "+place(mm))
	case !t.owned(m.active(t), row, col):
		return refusal("NOCOL", "member "+id+": no owned cell "+row+":"+col)
	}
	mm.placed, mm.row, mm.col, mm.score = true, row, col, score
	mm.rev++
	t.rev++
	t.wrote[m.active(t)] = true
	t.changes = append(t.changes, memChange{mark: memMarks.Add(1), epoch: m.active(t), before: t.rev - 1, after: t.rev, verb: "cell_add", ids: []string{id}})
	return nil
}

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
	if !slices.Contains(ep.rows, row) {
		return refusal("NOROW", "no row "+row)
	}
	// as the store's ns_table_row_set: a column the table lacks, or one that is no
	// text column, is refused
	for k := range texts {
		j := t.def.Column(k)
		if j < 0 {
			return refusal("NOCOL", "row "+row+": no column "+k)
		}
		if t.def.Columns[j].Projection != ntable.Text {
			return refusal("NOTTEXT", "row "+row+": column "+k+" is not text")
		}
	}
	if ep.texts[row] == nil {
		ep.texts[row] = map[string]string{}
	}
	maps.Copy(ep.texts[row], texts)
	t.rev++
	t.wrote[m.active(t)] = true
	t.changes = append(t.changes, memChange{mark: memMarks.Add(1), epoch: m.active(t), before: t.rev - 1, after: t.rev, verb: "row_set"})
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
		if json.Unmarshal([]byte(raw), &mc) == nil {
			f.Running = mc.Running()
			f.RunSeq = mc.RunSeq
			f.StopRevoked = mc.stopRevoked()
			f.StopIssued = mc.StopIssued
			f.StopDebt = mc.StopDebt
		}
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
			if n.Kind == sprint.Judgment || n.Kind == sprint.Acknowledged {
				// the note's alias: its place among the epoch's, under the fence (sprint.Alias)
				if l.aliases == nil {
					l.aliases = map[string]string{} // a log loaded from a file written before aliases
				}
				n.Alias = sprint.Alias(len(l.aliases) + 1)
				l.aliases[n.Alias] = n.ID
			}
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
		if l.answered == nil {
			l.answered = map[string]string{}
		}
		maps.Copy(l.answered, answeredBy(op))
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
		if op.Seat != nil {
			rec, err := seatRecord(op.Seat)
			if err != nil {
				return err
			}
			if m.kv == nil {
				m.kv = map[string]string{}
			}
			m.kv[keyCoordinator], m.kv[keySeat] = op.Seat.Holder, rec
		}
		// a clear first, so an observation written by the same commit stands
		for _, f := range op.HealthClear {
			delete(m.kv, friendHealthKey(f))
		}
		if op.Health != nil {
			rec, err := json.Marshal(op.Health.Health)
			if err != nil {
				return err
			}
			if m.kv == nil {
				m.kv = map[string]string{}
			}
			m.kv[friendHealthKey(op.Health.Friend)] = string(rec)
		}
		if len(op.CloseTimers) > 0 {
			if m.kv == nil {
				m.kv = map[string]string{}
			}
			if raw := m.kv[keyTimers]; raw != "" {
				var ts sprint.Timers
				if err := json.Unmarshal([]byte(raw), &ts); err == nil {
					closing := map[string]bool{}
					for _, id := range op.CloseTimers {
						closing[id] = true
					}
					var rem []sprint.Timer
					for _, tm := range ts.Open {
						if !closing[tm.ID] {
							rem = append(rem, tm)
						}
					}
					ts.Open = rem
					rec, err := json.Marshal(ts)
					if err != nil {
						return err
					}
					m.kv[keyTimers] = string(rec)
				}
			}
		} else if op.Timers != nil {
			rec, err := json.Marshal(op.Timers)
			if err != nil {
				return err
			}
			if m.kv == nil {
				m.kv = map[string]string{}
			}
			m.kv[keyTimers] = string(rec)
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
	maps.Copy(out, l.progress)
	return out, nil
}

// Pending is the operation the fence holds, for tests.
func (m *Mem) Pending() *OpRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	return l.fence
}

func (m *Mem) Answered(_ context.Context, ids []string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("answered")
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if who, ok := m.log().answered[id]; ok {
			out[id] = who
		}
	}
	return out, nil
}

func (m *Mem) Aliases(_ context.Context, aliases []string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("aliases")
	out := make(map[string]string, len(aliases))
	for _, a := range aliases {
		if id, ok := m.log().aliases[a]; ok {
			out[a] = id
		}
	}
	return out, nil
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
	m.LogLines += len(lines)
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

// SetCoordinator writes the coordinator key: init's alone (Store.InitSeat);
// the seat's steps write it in Release with the record. Mem keeps no expiring
// keys: the server's record stays until written again, judged by its time.
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

// TableChangesMarked is TableChanges with the marks of the events that left
// revisions from and to (loadcache.go); revision 0 is left by no event, its mark "".
func (m *Mem) TableChangesMarked(_ context.Context, table string, from, to uint64) ([]string, string, string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("changes")
	t := m.tables[table]
	if t == nil || to < from {
		return nil, "", "", false, nil
	}
	mark := func(c memChange) string { return strconv.FormatUint(c.mark, 10) }
	active := m.active(t)
	need := to
	var ids []string
	toMark, fromMark := "", ""
	chained := false
	for i := len(t.changes) - 1; i >= 0; i-- {
		c := t.changes[i]
		if c.epoch != active || c.after > to {
			continue
		}
		if c.after != need {
			return nil, "", "", false, nil
		}
		if c.after == to {
			toMark = mark(c)
		}
		if need == from {
			fromMark, chained = mark(c), true
			break
		}
		ids = append(ids, c.ids...)
		need = c.before
		if need == from && from == 0 {
			chained = true
			break
		}
	}
	if !chained && !(from == 0 && to == 0) {
		return nil, "", "", false, nil
	}
	return ids, fromMark, toMark, true, nil
}

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

// RowsOrder puts the named rows first, in that order, the rest after them as
// they stood: the table layer's row order, under RowsAdd's epoch check.
func (m *Mem) RowsOrder(_ context.Context, table string, rows []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["roworder"]++
	t, err := m.table(table)
	if err != nil {
		return err
	}
	if err := m.writeEpoch(t); err != nil {
		return err
	}
	ep := t.at(m.active(t))
	var order []string
	for _, r := range rows {
		if slices.Contains(ep.rows, r) && !slices.Contains(order, r) {
			order = append(order, r)
		}
	}
	for _, r := range ep.rows {
		if !slices.Contains(order, r) {
			order = append(order, r)
		}
	}
	ep.rows = order
	t.rev++
	t.wrote[m.active(t)] = true
	t.changes = append(t.changes, memChange{mark: memMarks.Add(1), epoch: m.active(t), before: t.rev - 1, after: t.rev, verb: "row_order"})
	return nil
}

var _ RowsOrderer = (*Mem)(nil)
