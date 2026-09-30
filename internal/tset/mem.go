package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Mem is an independent, atomic model of the table store.  A step plans against
// a private copy of one namespace; only a completely successful plan replaces it.
type Mem struct {
	mu     sync.Mutex
	spaces map[string]*memNamespace
	now    func() time.Time
}

type TableDefinition struct {
	Columns      []string
	MemberPrefix string
	EpochKey     string
	EpochField   string
}

// MemRecord is used to initialize fixtures and to inspect model snapshots.
// An empty Row and Column mean that the member is retained without a placement.
type MemRecord struct {
	Epoch    Decimal
	Revision Decimal
	Row      string
	Column   string
	Score    string
	Fields   map[string]string
}

type MemSnapshot struct {
	ActiveEpoch Decimal
	Engine      string
	Definitions map[string]TableDefinition
	Epochs      map[Decimal]MemEpochSnapshot
	Receipts    map[Decimal]map[string]MemReceiptSnapshot
	ZSets       map[string]map[string]string
}

type MemReceiptSnapshot struct {
	IntentDigest string
	Status       string
	EpochBefore  Decimal
	EpochAfter   Decimal
	FirstSeq     Decimal
	LastSeq      Decimal
	Changed      int
	Result       string
}

type MemEpochSnapshot struct {
	Tables map[string]MemTableSnapshot
}

// Props is nil for a table with no property at that epoch (amendment
// 2026-09-30, property, section 1: a hash per table per epoch).
type MemTableSnapshot struct {
	Rows    map[string]Decimal
	Cells   map[string]map[string]map[string]string
	Records map[string]MemRecord
	Props   map[string]string
}

type memNamespace struct {
	version         uint64 // changes on every supported mutation or successful publication
	active          Decimal
	engine          string
	epochKey        string
	epochField      string
	defs            map[string]*memTableDef
	definitionOrder []string
	epochs          map[Decimal]*memEpoch
	recordEpoch     map[string]map[string]Decimal // table, ID -> retained owner epoch
	receipts        map[Decimal]map[string]memReceipt
	zsets           map[string]map[string]string
}

type memTableDef struct {
	name         string
	memberPrefix string
	epochKey     string
	epochField   string
	columns      map[string]bool
	columnOrder  []string
}

type memEpoch struct {
	tables map[string]*memTableEpoch
}

type memTableEpoch struct {
	rows    map[string]Decimal
	cells   map[string]map[string]map[string]string // row, column, stored ID, score
	records map[string]*memRecord
	props   map[string]string // amendment 2026-09-30 (property): name -> value at this epoch
}

type memRecord struct {
	epoch    Decimal
	revision Decimal
	place    *memPlace
	score    string
	fields   map[string]string
}

type memPlace struct {
	row string
	col string
}

func NewMem() *Mem {
	return &Mem{spaces: make(map[string]*memNamespace), now: time.Now}
}

func (m *Mem) SetClock(now func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now == nil {
		m.now = time.Now
	} else {
		m.now = now
	}
}

// SetEngine injects a stored engine marker fault into a fixture. Production
// Mem namespaces start with the tset/1 marker.
func (m *Mem) SetEngine(space, engine string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.spaces[space]
	if s == nil {
		return memRefusal("CONFIG", RefusalDetail{})
	}
	s.engine = engine
	s.version++
	return nil
}

func (m *Mem) space(name string) *memNamespace {
	if m.spaces == nil {
		m.spaces = make(map[string]*memNamespace)
	}
	s := m.spaces[name]
	if s == nil {
		s = &memNamespace{
			active: "0", engine: Version, defs: make(map[string]*memTableDef), definitionOrder: make([]string, 0),
			epochs:      map[Decimal]*memEpoch{"0": {tables: make(map[string]*memTableEpoch)}},
			recordEpoch: make(map[string]map[string]Decimal),
			receipts:    make(map[Decimal]map[string]memReceipt),
			zsets:       make(map[string]map[string]string),
		}
		m.spaces[name] = s
	}
	return s
}

func newMemTableEpoch() *memTableEpoch {
	return &memTableEpoch{rows: make(map[string]Decimal), cells: make(map[string]map[string]map[string]string), records: make(map[string]*memRecord), props: make(map[string]string)}
}

func validMemName(s string) bool {
	return len(s) > 0 && len(s) <= 256 && utf8.ValidString(s)
}

func validMemRow(s string) bool {
	if !validMemName(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func memPrefixesOverlap(a, b string) bool { return strings.HasPrefix(a, b) || strings.HasPrefix(b, a) }

// validateMemConfig rechecks the stored definition view on every step. A
// fixture may corrupt it after initialization, just as a Redis hash can drift.
// This runs before receipt lookup, matching the protocol's admission order.
func validateMemConfig(space string, s *memNamespace) error {
	if len(s.defs) > 4 {
		return memRefusal("CONFIG", RefusalDetail{})
	}
	names := make([]string, 0, len(s.defs))
	for name := range s.defs {
		names = append(names, name)
	}
	sort.Strings(names)
	prefixes := make([]string, 0, len(names))
	for _, name := range names {
		def := s.defs[name]
		if def == nil || !validMemColumn(name) || len(def.columns) == 0 || len(def.columns) > 32 ||
			def.epochKey != s.epochKey || def.epochField != s.epochField ||
			!strings.HasPrefix(def.memberPrefix, space) ||
			memPrefixesOverlap(def.memberPrefix, space+"sprint:") ||
			memPrefixesOverlap(def.memberPrefix, space+"table:") ||
			memPrefixesOverlap(def.memberPrefix, space+"tables") ||
			memPrefixesOverlap(def.memberPrefix, def.epochKey) {
			return memRefusal("CONFIG", RefusalDetail{Table: name})
		}
		for _, prefix := range prefixes {
			if memPrefixesOverlap(prefix, def.memberPrefix) {
				return memRefusal("CONFIG", RefusalDetail{Table: name})
			}
		}
		prefixes = append(prefixes, def.memberPrefix)
	}
	return nil
}

// DefineTable installs a bounded definition before model steps.  All tables in
// one namespace share the same epoch key, and their member-key namespaces are disjoint.
func (m *Mem) DefineTable(space, table string, def TableDefinition) error {
	if !validMemName(space) || !validMemColumn(table) || len(def.Columns) == 0 || len(def.Columns) > 32 ||
		def.MemberPrefix == "" || def.EpochKey == "" || def.EpochField == "" {
		return memRefusal("CONFIG", RefusalDetail{Table: table})
	}
	if !strings.HasPrefix(def.MemberPrefix, space) || !strings.HasPrefix(def.EpochKey, space) ||
		memPrefixesOverlap(def.MemberPrefix, space+"sprint:") ||
		memPrefixesOverlap(def.MemberPrefix, space+"table:") ||
		memPrefixesOverlap(def.MemberPrefix, space+"tables") ||
		memPrefixesOverlap(def.MemberPrefix, def.EpochKey) {
		return memRefusal("CONFIG", RefusalDetail{Table: table})
	}
	cols := make(map[string]bool, len(def.Columns))
	for _, col := range def.Columns {
		if !validMemColumn(col) || cols[col] {
			return memRefusal("CONFIG", RefusalDetail{Table: table})
		}
		cols[col] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.space(space)
	if len(s.defs) >= 4 || s.defs[table] != nil {
		return memRefusal("CONFIG", RefusalDetail{Table: table})
	}
	for _, old := range s.defs {
		if old.epochKey != def.EpochKey || old.epochField != def.EpochField || memPrefixesOverlap(old.memberPrefix, def.MemberPrefix) {
			return memRefusal("CONFIG", RefusalDetail{Table: table})
		}
	}
	s.defs[table] = &memTableDef{name: table, memberPrefix: def.MemberPrefix, epochKey: def.EpochKey,
		epochField: def.EpochField, columns: cols, columnOrder: append([]string(nil), def.Columns...)}
	s.recordEpoch[table] = make(map[string]Decimal)
	if len(s.definitionOrder) == 0 {
		s.epochKey, s.epochField = def.EpochKey, def.EpochField
	}
	s.definitionOrder = append(s.definitionOrder, table)
	for _, epoch := range s.epochs {
		epoch.tables[table] = newMemTableEpoch()
	}
	s.version++
	return nil
}

func validMemColumn(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i == 0 && !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

// SetActiveEpoch is a fixture initializer. It also creates an empty snapshot
// for a fresh epoch; ordinary steps advance the active epoch themselves.
func (m *Mem) SetActiveEpoch(space string, epoch Decimal) error {
	if !memValidDecimal(epoch) {
		return memRefusal("REQUEST", RefusalDetail{})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.space(space)
	s.active = epoch
	if s.epochs[epoch] == nil {
		e := &memEpoch{tables: make(map[string]*memTableEpoch)}
		for table := range s.defs {
			e.tables[table] = newMemTableEpoch()
		}
		s.epochs[epoch] = e
	}
	s.version++
	return nil
}

func (m *Mem) SeedRow(space, table string, epoch Decimal, row string, rank Decimal) error {
	if !validMemRow(row) || !memValidDecimal(epoch) || !memValidDecimal(rank) || memCompareDecimal(rank, "9007199254740991") > 0 {
		return memRefusal("REQUEST", RefusalDetail{Table: table, Rows: []string{row}})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.spaces[space]
	if s == nil {
		return memRefusal("CONFIG", RefusalDetail{Table: table})
	}
	e := s.epochs[epoch]
	if e == nil || s.defs[table] == nil || e.tables[table] == nil {
		return memRefusal("CONFIG", RefusalDetail{Table: table})
	}
	e.tables[table].rows[row] = rank
	s.version++
	return nil
}

func (m *Mem) SeedMember(space, table string, epoch Decimal, id string, record MemRecord) error {
	if !validMemName(id) || !memValidDecimal(epoch) || !memValidDecimal(record.Revision) || record.Epoch != epoch ||
		(record.Row == "") != (record.Column == "") {
		return memRefusal("REQUEST", RefusalDetail{Table: table, IDs: []string{id}})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.spaces[space]
	if s == nil {
		return memRefusal("CONFIG", RefusalDetail{Table: table})
	}
	e := s.epochs[epoch]
	if e == nil || s.defs[table] == nil || e.tables[table] == nil {
		return memRefusal("CONFIG", RefusalDetail{Table: table})
	}
	if _, _, exists := s.recordTable(table, id); exists {
		return memRefusal("EXISTS", RefusalDetail{Table: table, IDs: []string{id}})
	}
	r := &memRecord{epoch: epoch, revision: record.Revision, score: record.Score, fields: cloneFields(record.Fields)}
	if record.Row != "" {
		if e.tables[table].rows[record.Row] == "" || !s.defs[table].columns[record.Column] {
			return memRefusal("PLACE", RefusalDetail{Table: table, IDs: []string{id}})
		}
		score, err := parseMemScore(record.Score)
		if err != nil {
			return err
		}
		r.score = memScoreText(score)
		r.place = &memPlace{row: record.Row, col: record.Column}
		ensureMemCell(e.tables[table], record.Row, record.Column)[id] = r.score
	}
	e.tables[table].records[id] = r
	s.recordEpoch[table][id] = epoch
	s.version++
	return nil
}

// recordTable resolves the single member namespace to its retained table
// snapshot. Callers hold m.mu; the index is published with the cloned namespace.
func (s *memNamespace) recordTable(table, id string) (*memTableEpoch, Decimal, bool) {
	epoch, ok := s.recordEpoch[table][id]
	if !ok {
		return nil, "", false
	}
	e := s.epochs[epoch]
	if e == nil || e.tables[table] == nil || e.tables[table].records[id] == nil {
		return nil, "", false
	}
	return e.tables[table], epoch, true
}

func (m *Mem) SeedZSet(space, key string, members map[string]string) error {
	if !validMemName(space) || !strings.HasPrefix(key, space) {
		return memRefusal("REQUEST", RefusalDetail{})
	}
	copyMembers := make(map[string]string, len(members))
	for id, score := range members {
		f, err := parseMemScore(score)
		if err != nil {
			return err
		}
		copyMembers[id] = memScoreText(f)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.space(space)
	s.zsets[key] = copyMembers
	s.version++
	return nil
}

// CorruptCell deliberately changes one fixture cell without changing its
// record. It exists to prove that point guards refuse detectable store drift.
func (m *Mem) CorruptCell(space, table string, epoch Decimal, row, col, id, score string) error {
	f, err := parseMemScore(score)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.spaces[space]
	if s == nil || s.epochs[epoch] == nil || s.epochs[epoch].tables[table] == nil ||
		s.epochs[epoch].tables[table].cells[row][col] == nil {
		return memRefusal("CONFIG", RefusalDetail{Table: table})
	}
	s.epochs[epoch].tables[table].cells[row][col][id] = memScoreText(f)
	s.version++
	return nil
}

func (m *Mem) Snapshot(space string) (MemSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.spaces[space]
	if s == nil {
		return MemSnapshot{}, memRefusal("CONFIG", RefusalDetail{})
	}
	out := MemSnapshot{ActiveEpoch: s.active, Engine: s.engine, Definitions: make(map[string]TableDefinition, len(s.defs)),
		Epochs:   make(map[Decimal]MemEpochSnapshot, len(s.epochs)),
		Receipts: make(map[Decimal]map[string]MemReceiptSnapshot, len(s.receipts)),
		ZSets:    make(map[string]map[string]string, len(s.zsets))}
	for name, def := range s.defs {
		columns := make([]string, 0, len(def.columns))
		for col := range def.columns {
			columns = append(columns, col)
		}
		sort.Strings(columns)
		out.Definitions[name] = TableDefinition{Columns: columns, MemberPrefix: def.memberPrefix, EpochKey: def.epochKey, EpochField: def.epochField}
	}
	for epoch, receipts := range s.receipts {
		out.Receipts[epoch] = make(map[string]MemReceiptSnapshot, len(receipts))
		for op, r := range receipts {
			out.Receipts[epoch][op] = MemReceiptSnapshot{IntentDigest: r.IntentDigest, Status: r.Status,
				EpochBefore: r.EpochBefore, EpochAfter: r.EpochAfter, FirstSeq: r.FirstSeq,
				LastSeq: r.LastSeq, Changed: r.Changed, Result: r.Result}
		}
	}
	for key, members := range s.zsets {
		out.ZSets[key] = make(map[string]string, len(members))
		for id, score := range members {
			out.ZSets[key][id] = score
		}
	}
	for epoch, e := range s.epochs {
		es := MemEpochSnapshot{Tables: make(map[string]MemTableSnapshot, len(e.tables))}
		for name, t := range e.tables {
			ts := MemTableSnapshot{Rows: make(map[string]Decimal, len(t.rows)), Cells: cloneMemCells(t.cells), Records: make(map[string]MemRecord, len(t.records))}
			for row, rank := range t.rows {
				ts.Rows[row] = rank
			}
			for id, r := range t.records {
				rr := MemRecord{Epoch: r.epoch, Revision: r.revision, Score: r.score, Fields: cloneFields(r.fields)}
				if r.place != nil {
					rr.Row, rr.Column = r.place.row, r.place.col
				}
				ts.Records[id] = rr
			}
			if len(t.props) != 0 {
				ts.Props = cloneFields(t.props)
			}
			es.Tables[name] = ts
		}
		out.Epochs[epoch] = es
	}
	return out, nil
}

func cloneFields(fields map[string]string) map[string]string {
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		out[k] = v
	}
	return out
}

func cloneMemCells(cells map[string]map[string]map[string]string) map[string]map[string]map[string]string {
	out := make(map[string]map[string]map[string]string, len(cells))
	for row, columns := range cells {
		out[row] = make(map[string]map[string]string, len(columns))
		for col, members := range columns {
			out[row][col] = make(map[string]string, len(members))
			for id, score := range members {
				out[row][col][id] = score
			}
		}
	}
	return out
}

func cloneMemNamespace(s *memNamespace) *memNamespace {
	out := &memNamespace{version: s.version, active: s.active, engine: s.engine, epochKey: s.epochKey, epochField: s.epochField,
		defs: make(map[string]*memTableDef, len(s.defs)), definitionOrder: append([]string(nil), s.definitionOrder...), epochs: make(map[Decimal]*memEpoch, len(s.epochs)),
		recordEpoch: make(map[string]map[string]Decimal, len(s.recordEpoch)),
		receipts:    make(map[Decimal]map[string]memReceipt, len(s.receipts)), zsets: make(map[string]map[string]string, len(s.zsets))}
	for name, def := range s.defs {
		copyDef := *def
		copyDef.columnOrder = append([]string(nil), def.columnOrder...)
		copyDef.columns = make(map[string]bool, len(def.columns))
		for column, enabled := range def.columns {
			copyDef.columns[column] = enabled
		}
		out.defs[name] = &copyDef
	}
	for key, members := range s.zsets {
		out.zsets[key] = make(map[string]string, len(members))
		for id, score := range members {
			out.zsets[key][id] = score
		}
	}
	for table, ids := range s.recordEpoch {
		out.recordEpoch[table] = make(map[string]Decimal, len(ids))
		for id, epoch := range ids {
			out.recordEpoch[table][id] = epoch
		}
	}
	for epoch, e := range s.epochs {
		ne := &memEpoch{tables: make(map[string]*memTableEpoch, len(e.tables))}
		for table, t := range e.tables {
			nt := &memTableEpoch{rows: make(map[string]Decimal, len(t.rows)), cells: cloneMemCells(t.cells), records: make(map[string]*memRecord, len(t.records)), props: cloneFields(t.props)}
			for row, rank := range t.rows {
				nt.rows[row] = rank
			}
			for id, r := range t.records {
				nr := *r
				nr.fields = cloneFields(r.fields)
				if r.place != nil {
					p := *r.place
					nr.place = &p
				}
				nt.records[id] = &nr
			}
			ne.tables[table] = nt
		}
		out.epochs[epoch] = ne
	}
	for epoch, receipts := range s.receipts {
		out.receipts[epoch] = make(map[string]memReceipt, len(receipts))
		for op, receipt := range receipts {
			out.receipts[epoch][op] = receipt
		}
	}
	return out
}

// rawDefinition and rawEpochMarker expose the exact bounded hash payloads
// fetched by the Redis planner. Their field names and values are included in
// the Mem read budget, while the public snapshot stays semantic.
func (s *memNamespace) rawDefinition(table string) map[string]string {
	def := s.defs[table]
	if def == nil {
		return nil
	}
	raw := map[string]string{
		"engine": "tset/1", "order": strings.Join(def.columnOrder, ","),
		"member_prefix": def.memberPrefix, "epoch_key": def.epochKey, "epoch_field": def.epochField,
	}
	for _, col := range def.columnOrder {
		raw["col:"+col] = "set"
	}
	return raw
}

func (s *memNamespace) rawEpochMarker(epoch Decimal) map[string]string {
	names := s.definitionOrder
	if names == nil {
		names = []string{}
	}
	encoded, _ := json.Marshal(names)
	return map[string]string{"engine": "tset/1", "n": string(epoch), "tables": string(encoded)}
}

func ensureMemCell(t *memTableEpoch, row, col string) map[string]string {
	if t.cells[row] == nil {
		t.cells[row] = make(map[string]map[string]string)
	}
	if t.cells[row][col] == nil {
		t.cells[row][col] = make(map[string]string)
	}
	return t.cells[row][col]
}

func memRefusal(code string, detail RefusalDetail) error {
	return &Refusal{Status: "refused", Code: code, Detail: detail, Message: code + "; nothing was changed"}
}

func memValidDecimal(d Decimal) bool {
	s := string(d)
	if s == "0" {
		return true
	}
	if s == "" || s[0] < '1' || s[0] > '9' || len(s) > 20 {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) < 20 || s <= "18446744073709551615"
}

func memCompareDecimal(a, b Decimal) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(string(a), string(b))
}

func memNextDecimal(d Decimal) (Decimal, bool) {
	if !memValidDecimal(d) || d == "18446744073709551615" {
		return "", false
	}
	b := []byte(d)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < '9' {
			b[i]++
			return Decimal(b), true
		}
		b[i] = '0'
	}
	return Decimal("1" + string(b)), true
}

// memPrepared owns the candidate state and reply privately. Public MemPlan
// fields are observations, never instructions to Commit. A replay only carries
// its captured reply; publishing a clone would move state for no write.
type memPrepared struct {
	owner       *Mem
	space       string
	base        *memNamespace
	baseVersion uint64
	next        *memNamespace
	reply       Reply
	publish     bool
	used        bool
}

func (m *Mem) validateStepInput(step Step) error {
	if _, err := EncodeStep(step); err != nil {
		if refusal, ok := err.(*Refusal); ok && refusal.Code == "ADVANCE" {
			m.mu.Lock()
			if space := m.spaces[step.Space]; space != nil {
				refusal.Detail.ActiveEpoch = space.active
			}
			m.mu.Unlock()
		}
		return err
	}
	return nil
}

// prepareLocked performs exactly the same admission, receipt lookup, planning,
// and receipt-size check as Step. The caller holds m.mu throughout.
func (m *Mem) prepareLocked(ctx context.Context, step Step) (*memPrepared, error) {
	s := m.spaces[step.Space]
	if s == nil {
		return nil, memRefusal("CONFIG", RefusalDetail{})
	}
	if s.engine != Version {
		return nil, memRefusal("ENGINE", RefusalDetail{})
	}
	if err := validateMemConfig(step.Space, s); err != nil {
		return nil, err
	}
	for i, entry := range step.Entries {
		if entry.Kind != "advance" && s.defs[entry.Table] == nil {
			return nil, memRefusal("NOTABLE", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table})
		}
	}
	if reply, found, err := m.checkReceipt(s, step); found || err != nil {
		if err != nil {
			return nil, err
		}
		return &memPrepared{owner: m, space: step.Space, base: s,
			baseVersion: s.version, reply: reply}, nil
	}
	if c := memCompareDecimal(step.Epoch, s.active); c < 0 {
		return nil, memEpochRefusal("STALE", s.active)
	} else if c > 0 {
		return nil, memEpochRefusal("EPOCHAHEAD", s.active)
	}
	next := cloneMemNamespace(s)
	var reply Reply
	var err error
	if step.Fence {
		reply, err = m.planFence(step)
	} else {
		reply, err = m.planStep(ctx, s, next, step)
	}
	if err != nil {
		return nil, err
	}
	if err := m.saveReceipt(next, step, reply); err != nil {
		return nil, err
	}
	return &memPrepared{owner: m, space: step.Space, base: s,
		baseVersion: s.version, next: next, reply: reply, publish: true}, nil
}

func memAsRefusal(err error) *Refusal {
	if refusal, ok := err.(*Refusal); ok {
		return refusal
	}
	return NewRefusal("DRIFT", RefusalDetail{})
}

// Plan is the twin's nonwriting first phase. It returns a caller-editable
// observation detached from the private candidate state and captured reply.
func (m *Mem) Plan(step Step) (*MemPlan, *Refusal) {
	if err := m.validateStepInput(step); err != nil {
		return nil, memAsRefusal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prepared, err := m.prepareLocked(context.Background(), step)
	if err != nil {
		return nil, memAsRefusal(err)
	}
	plan := cloneMemPlan(prepared.reply.MemPlan)
	if plan == nil {
		plan = &MemPlan{}
	}
	plan.Replay = prepared.reply.Replay
	plan.prepared = prepared
	return plan, nil
}

// Commit publishes the exact candidate captured by Plan. Stale plans cannot
// overwrite an intervening fixture mutation or another committed step.
func (m *Mem) Commit(plan *MemPlan) (Reply, *Refusal) {
	if plan == nil || plan.prepared == nil || plan.prepared.owner != m {
		return Reply{}, NewRefusal("REQUEST", RefusalDetail{})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.commitLocked(plan.prepared)
}

func (m *Mem) commitLocked(prepared *memPrepared) (Reply, *Refusal) {
	if prepared.used {
		return Reply{}, NewRefusal("REQUEST", RefusalDetail{})
	}
	current := m.spaces[prepared.space]
	if current != prepared.base || current == nil || current.version != prepared.baseVersion {
		prepared.used = true
		detail := RefusalDetail{}
		if current != nil {
			detail.ActiveEpoch = current.active
		}
		return Reply{}, NewRefusal("REVISION", detail)
	}
	prepared.used = true
	if prepared.publish {
		prepared.next.version = prepared.baseVersion + 1
		m.spaces[prepared.space] = prepared.next
	}
	return prepared.reply, nil
}

func (m *Mem) Step(ctx context.Context, step Step) (Reply, error) {
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	if err := m.validateStepInput(step); err != nil {
		return Reply{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prepared, err := m.prepareLocked(ctx, step)
	if err != nil {
		return Reply{}, err
	}
	reply, refusal := m.commitLocked(prepared)
	if refusal != nil {
		return Reply{}, refusal
	}
	return reply, nil
}

func (m *Mem) Steps(ctx context.Context, steps []Step) ([]StepResult, error) {
	// Match the transport boundary: malformed encoding aborts before any step.
	raw := make([][]byte, len(steps))
	for i, step := range steps {
		encoded, err := EncodeStep(step)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i, err)
		}
		raw[i] = encoded
	}
	if err := ctx.Err(); err != nil && len(steps) != 0 {
		return nil, err
	}
	out := make([]StepResult, len(steps))
	for i := range out {
		out[i].RawRequest = append([]byte(nil), raw[i]...)
	}
	for i, step := range steps {
		if err := ctx.Err(); err != nil {
			for j := i; j < len(out); j++ {
				out[j].Err = err
			}
			break
		}
		out[i].Reply, out[i].Err = m.Step(ctx, step)
	}
	return out, nil
}

func memEpochRefusal(code string, active Decimal) error {
	r := memRefusal(code, RefusalDetail{ActiveEpoch: active})
	var refusal *Refusal
	if errors.As(r, &refusal) {
		refusal.Message = fmt.Sprintf("%s (active_epoch=%s); nothing was changed", code, active)
	}
	return r
}
