package stepbuild

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// class is what an entry counts against in a step.
type class int

const (
	classChange class = iota // create, move, remove: candidates, log lines, record writes
	classGuard               // guard: guard-only members
	classRows                // rows: distinct row pairs
	classNote                // note: notes only
)

// memberKey is a member as a step names it: a stored ID in a table.
type memberKey struct{ table, id string }

// cost is what one member adds to a wire entry and to a step, in every unit a
// bound measures. Sizes are exact; commas between members are the entry's.
type cost struct {
	req   int // its items in the entry's member arrays (ids, scores, revs, each, about)
	line  int // its share of the generated log line
	argv  int // raw bytes of its effective fields: written to its record
	obs   int // field-value observations: distinct names it sets, unsets or reads
	about int // 1 when it carries an about ID
}

// noteState is a costed note.
type noteState struct{ use usage }

// entryState is an entry validated and costed: everything the cut needs to
// place it without looking at a string again.
type entryState struct {
	idx   int
	e     *Entry
	class class
	n     int    // members: ids, or a rows entry's adds then dels
	cost  []cost // one per member

	head   int // wire bytes of the entry with its member arrays empty
	arrays int // member arrays on the wire: the commas between members

	lineHead   int // bytes of the generated line with no member
	lineArrays int // arrays of the generated line: the commas between members
	rawUnset   int // raw bytes of the unset names, written once per member

	tables    []string // distinct: the entry's table and its guards'
	guards    []*entryState
	guardUse  usage       // what the guards add to a step that holds them
	guardKeys []memberKey // the members the guards name

	notes []noteState
}

// wireBytes is the encoded size of members [lo, hi) of the entry as one wire
// entry: the head, the items, and a comma per array between members.
func (es *entryState) wireBytes(lo, hi int) int {
	n := es.head
	for j := lo; j < hi; j++ {
		n += es.cost[j].req
	}
	if hi-lo > 1 {
		n += (hi - lo - 1) * es.arrays
	}
	return n
}

// site is where a check is made: the input entry and its table, and the
// prefix of an attached guard's fields.
type site struct {
	entry  int
	table  string
	prefix string
}

func (s site) input(field, member, reason string) error {
	return &InputError{Entry: s.entry, Field: s.prefix + field, Member: member, Reason: reason}
}

func (s site) limit(bd bound, limit, actual int, member, field string) error {
	return &LimitError{Bound: bd.name, Section: bd.section, Limit: limit, Actual: actual, Entry: s.entry, Table: s.table, Member: member, Field: s.prefix + field, Note: -1}
}

// name checks an identifier: not empty, UTF-8, at most LimitNameBytes.
func (s site) name(field, member, v string) error {
	switch {
	case v == "":
		return s.input(field, member, "is empty")
	case !utf8.ValidString(v):
		return s.input(field, member, "is not UTF-8")
	case len(v) > LimitNameBytes:
		return s.limit(boundName, LimitNameBytes, len(v), member, field)
	}
	return nil
}

// text checks a string that may be empty: UTF-8, at most max bytes.
func (s site) text(field, member, v string, bd bound, max int) error {
	switch {
	case !utf8.ValidString(v):
		return s.input(field, member, "is not UTF-8")
	case len(v) > max:
		return s.limit(bd, max, len(v), member, field)
	}
	return nil
}

// cell checks a cell reference "<row>:<col>", split at its last colon: both
// halves are names.
func (s site) cell(field, v string) error {
	i := strings.LastIndexByte(v, ':')
	if i <= 0 || i == len(v)-1 {
		return s.input(field, "", "is not a cell reference <row>:<col>")
	}
	if err := s.name(field, "", v[:i]); err != nil {
		return err
	}
	return s.name(field, "", v[i+1:])
}

// pair checks one application field: a name and a value.
func (s site) pair(field, member, k, v string) error {
	if err := s.name(field, member, k); err != nil {
		return err
	}
	err := s.text(field, member, v, boundFieldValue, LimitFieldValueBytes)
	if le, ok := err.(*LimitError); ok {
		le.Name = k
	}
	return err
}

// pairOK is pair without the error, for the hot path; a false is followed by
// fault, which names the fault in byte order.
func pairOK(k, v string) bool {
	return k != "" && len(k) <= LimitNameBytes && len(v) <= LimitFieldValueBytes && utf8.ValidString(k) && utf8.ValidString(v)
}

// fault names the first fault of a field object, in key byte order: the one
// error every run reports. It is called once pairOK has found one; should the
// two ever disagree it still refuses, generally.
func (s site) fault(field, member string, m map[string]string) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := s.pair(field, member, k, m[k]); err != nil {
			return err
		}
	}
	return s.input(field, member, "holds a field that is not valid")
}

// names checks an array of field names against the count bound and each name.
func (s site) names(field string, ss []string, bd bound, max int) error {
	if len(ss) > max {
		return s.limit(bd, max, len(ss), "", field)
	}
	for _, v := range ss {
		if err := s.name(field, "", v); err != nil {
			return err
		}
	}
	return nil
}

// prepare validates and costs every entry, in input order, before anything
// is placed: the first fault refuses the build.
func (b *builder) prepare(entries []Entry) ([]*entryState, error) {
	out := make([]*entryState, len(entries))
	for i := range entries {
		es, err := b.costEntry(i, &entries[i])
		if err != nil {
			return nil, err
		}
		out[i] = es
	}
	return out, nil
}

// fieldSet is a set of an entry's optional fields, by kind.
type fieldSet uint16

const (
	fFrom fieldSet = 1 << iota
	fTo
	fScores
	fRevs
	fEach
	fAbout
	fSet
	fUnset
	fBefore
	fMeta
	fGuards
	fNotes
	fRows
)

var fieldNames = []struct {
	f    fieldSet
	name string
}{
	{fFrom, "from"}, {fTo, "to"}, {fScores, "scores"}, {fRevs, "revs"}, {fEach, "each"},
	{fAbout, "about"}, {fSet, "set"}, {fUnset, "unset"}, {fBefore, "before_fields"},
	{fMeta, "meta"}, {fGuards, "guards"}, {fNotes, "notes"}, {fRows, "add/del"},
}

// shape is what a kind requires and allows of an entry (section 3).
type shape struct {
	class    class
	required fieldSet
	allowed  fieldSet
}

var shapes = map[Kind]shape{
	KindCreate: {classChange, fTo | fScores, fTo | fScores | fEach | fAbout | fSet | fBefore | fMeta | fGuards | fNotes},
	KindMove:   {classChange, fFrom, fFrom | fTo | fScores | fRevs | fEach | fAbout | fSet | fUnset | fBefore | fMeta | fGuards | fNotes},
	KindRemove: {classChange, fFrom, fFrom | fRevs | fEach | fAbout | fSet | fUnset | fBefore | fMeta | fGuards | fNotes},
	KindGuard:  {classGuard, fFrom, fFrom | fRevs | fBefore | fNotes},
	KindRows:   {classRows, fRows, fRows | fGuards | fNotes},
	KindNote:   {classNote, fNotes, fNotes},
}

// attachedGuard is what an attached guard may carry: a guard entry, no more.
var attachedGuard = shape{classGuard, fFrom, fFrom | fRevs | fBefore}

// present is which optional fields the entry sets: nil is absent, an empty
// non-nil array or object is present.
func present(e *Entry) fieldSet {
	var f fieldSet
	set := func(on bool, bit fieldSet) {
		if on {
			f |= bit
		}
	}
	set(e.From != "", fFrom)
	set(e.To != "", fTo)
	set(e.Scores != nil, fScores)
	set(e.Revs != nil, fRevs)
	set(e.Each != nil, fEach)
	set(e.About != nil, fAbout)
	set(e.Set != nil, fSet)
	set(e.Unset != nil, fUnset)
	set(e.BeforeFields != nil, fBefore)
	set(e.Meta != nil, fMeta)
	set(len(e.Guards) > 0, fGuards)
	set(len(e.Notes) > 0, fNotes)
	set(e.Add != nil || e.Del != nil, fRows)
	return f
}

// checkShape refuses a field the kind does not take and a field it requires
// that is absent.
func checkShape(st site, e *Entry, sh shape) error {
	have := present(e)
	for _, fn := range fieldNames {
		switch {
		case have&fn.f != 0 && sh.allowed&fn.f == 0:
			return st.input(fn.name, "", "kind "+string(e.Kind)+" does not take it")
		case sh.required&fn.f != 0 && have&fn.f == 0:
			return st.input(fn.name, "", "kind "+string(e.Kind)+" requires it")
		}
	}
	return nil
}

// costEntry validates one entry against its kind and the per-item bounds and
// costs its members, guards and notes.
func (b *builder) costEntry(idx int, e *Entry) (*entryState, error) {
	sh, ok := shapes[e.Kind]
	if !ok {
		return nil, (site{entry: idx}).input("kind", "", "is not a kind of entry")
	}
	st := site{entry: idx, table: e.Table}
	if err := checkShape(st, e, sh); err != nil {
		return nil, err
	}
	es := &entryState{idx: idx, e: e, class: sh.class}
	if es.class != classNote {
		if err := st.name("t", "", e.Table); err != nil {
			return nil, err
		}
		es.tables = []string{e.Table}
	}
	if err := b.costBody(es, st); err != nil {
		return nil, err
	}
	for i := range e.Notes {
		n, err := b.costNote(st, i, &e.Notes[i])
		if err != nil {
			return nil, err
		}
		es.notes = append(es.notes, n)
	}
	return es, nil
}

// costBody costs the members of a change, guard or rows entry, with the
// guards that travel with it.
func (b *builder) costBody(es *entryState, st site) error {
	switch es.class {
	case classRows:
		return b.costRows(es, st)
	case classNote:
		return nil
	}
	seen := make(map[memberKey]struct{}, len(es.e.IDs))
	if err := b.costMembers(es, st, seen); err != nil {
		return err
	}
	return b.costGuards(es, st, seen)
}

// costGuards costs the guards attached to an entry. Guards are never cut, so
// one with more members than an entry may hold is refused; a guard that names
// a member the entry or another guard names is TWICE in every step.
func (b *builder) costGuards(es *entryState, st site, seen map[memberKey]struct{}) error {
	names := map[string]struct{}{es.e.Table: {}}
	for gi := range es.e.Guards {
		g := &es.e.Guards[gi]
		gst := site{entry: es.idx, table: g.Table, prefix: "guards[" + strconv.Itoa(gi) + "]."}
		if g.Kind != KindGuard {
			return gst.input("kind", "", "an attached guard is a guard entry")
		}
		if err := checkShape(gst, g, attachedGuard); err != nil {
			return err
		}
		if err := gst.name("t", "", g.Table); err != nil {
			return err
		}
		gs := &entryState{idx: es.idx, e: g, class: classGuard}
		if err := b.costMembers(gs, gst, seen); err != nil {
			return err
		}
		if gs.n > b.bounds.EntryIDs {
			return gst.limit(boundEntryIDs, b.bounds.EntryIDs, gs.n, "", "ids")
		}
		es.guards = append(es.guards, gs)
		es.guardUse = add(es.guardUse, usage{entries: 1, entryBytes: gs.wireBytes(0, gs.n), guardOnly: gs.n, obs: gs.sumObs()})
		for _, id := range g.IDs {
			es.guardKeys = append(es.guardKeys, memberKey{g.Table, id})
		}
		if _, dup := names[g.Table]; !dup {
			names[g.Table] = struct{}{}
			es.tables = append(es.tables, g.Table)
		}
	}
	return nil
}

func (es *entryState) sumObs() int {
	n := 0
	for i := range es.cost {
		n += es.cost[i].obs
	}
	return n
}

// The generated log line, modelled. The contract leaves the line's encoding
// to Layer 2 and bounds it at 1 MiB and 2,000 IDs; the builder takes the
// stricter reading: the line is the semantic event of section 1.3 written
// out in full as JSON (never the compact form), with every value the store
// supplies at its widest, and every member's effective fields written per
// member, shared fields repeated. A line is one wire entry's members.
const (
	// lineEnvelope opens a line: the sequence, epoch and time Layer 2 adds,
	// each at the widest a decimal uint64 goes; the event follows, and one
	// brace closes.
	lineEnvelope = `{"seq":"18446744073709551615","epoch":"18446744073709551615","at_ms":"18446744073709551615","line":`
	// lineArrayKeys are the member arrays of an event, empty.
	lineArrayKeys = `,"changed_ids":[],"before_scores":[],"after_scores":[],"before_revs":[],"after_revs":[],"field_changes":[]`

	lineScore = `"-1.7976931348623157e+308"` // a score at its widest canonical spelling
	lineRev   = `"18446744073709551615"`     // a revision at its widest

	lineScoreBytes = len(lineScore)
	lineRevBytes   = len(lineRev)

	lineChangeOpen  = `{"set":`   // a field_changes item: the set object,
	lineChangeUnset = `,"unset":` // then the unset array,
	lineChangeClose = `}`         // closed.
	lineArrays      = 6           // member arrays of an event
	lineNoteOpen    = `{"kind":"note","meta":`
)

// lineHead is the generated line's bytes with no member: the envelope, the
// event's kind, table, cells, empty member arrays, about and meta.
func lineHead(e *Entry) int {
	n := len(lineEnvelope) + 1
	n += len(`{"kind":`) + quoted(string(e.Kind)) + len(`,"table":`) + quoted(e.Table)
	if e.From != "" {
		n += len(`,"from":`) + quoted(e.From)
	}
	if e.To != "" {
		n += len(`,"to":`) + quoted(e.To)
	}
	n += len(lineArrayKeys)
	if e.About != nil {
		n += len(`,"about":[]`)
	}
	if e.Meta != nil {
		n += len(`,"meta":`) + objectBytes(e.Meta)
	}
	return n + 1
}

// fieldInfo is a shared field's value as the member costs need it.
type fieldInfo struct{ qv, rawv int }

// shared is what every member of an entry inherits: the set object, the
// names it observes, the unset array.
type shared struct {
	set      map[string]fieldInfo
	setItems int // bytes of the set object's items
	setRaw   int // raw bytes of the names and values
	names    map[string]struct{}
	unsetArr int // encoded bytes of the unset array
	rawUnset int
}

// costShared checks and costs the fields the entry as a whole carries.
func (b *builder) costShared(es *entryState, st site) (shared, error) {
	e := es.e
	sh := shared{unsetArr: 2, set: make(map[string]fieldInfo, len(e.Set))}
	sh.names = make(map[string]struct{}, len(e.Set)+len(e.Unset)+len(e.BeforeFields))
	if len(e.Set) > LimitFieldsPerMember {
		return sh, st.limit(boundFields, LimitFieldsPerMember, len(e.Set), "", "set")
	}
	if err := st.names("unset", e.Unset, boundUnset, LimitUnsetNames); err != nil {
		return sh, err
	}
	if err := st.names("before_fields", e.BeforeFields, boundBeforeNames, LimitBeforeFields); err != nil {
		return sh, err
	}
	for k, v := range e.Set {
		if !pairOK(k, v) {
			return sh, st.fault("set", "", e.Set)
		}
		qv := quoted(v)
		sh.set[k] = fieldInfo{qv, len(v)}
		sh.setItems += quoted(k) + 1 + qv
		sh.setRaw += len(k) + len(v)
		sh.names[k] = struct{}{}
	}
	for _, u := range e.Unset {
		sh.names[u] = struct{}{}
		sh.rawUnset += len(u)
	}
	for _, f := range e.BeforeFields {
		sh.names[f] = struct{}{}
	}
	if e.Unset != nil {
		sh.unsetArr = arrayBytes(e.Unset)
	}
	if err := checkMeta(st, "meta", e.Meta); err != nil {
		return sh, err
	}
	return sh, nil
}

// checkMeta checks a meta object's strings are UTF-8; its size is charged to
// the request and the line, not bounded of its own (section 1.2).
func checkMeta(st site, field string, m map[string]string) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !utf8.ValidString(k) || !utf8.ValidString(m[k]) {
			return st.input(field, "", "is not UTF-8")
		}
	}
	return nil
}

// alignment refuses a member array whose length is not the ids'.
func alignment(st site, e *Entry) error {
	n := len(e.IDs)
	for _, a := range []struct {
		name string
		got  int
		on   bool
	}{
		{"scores", len(e.Scores), e.Scores != nil},
		{"revs", len(e.Revs), e.Revs != nil},
		{"each", len(e.Each), e.Each != nil},
		{"about", len(e.About), e.About != nil},
	} {
		if a.on && a.got != n {
			return st.input(a.name, "", "is not aligned with ids: "+strconv.Itoa(a.got)+" for "+strconv.Itoa(n))
		}
	}
	return nil
}

// costMembers validates the members of a change or guard entry and costs
// each, recording its (table, id) in seen: a member named twice, in the
// entry or with its guards, is refused (section 3, TWICE).
func (b *builder) costMembers(es *entryState, st site, seen map[memberKey]struct{}) error {
	e := es.e
	n := len(e.IDs)
	if n == 0 {
		return st.input("ids", "", "names no member")
	}
	if err := alignment(st, e); err != nil {
		return err
	}
	if err := checkCells(st, e); err != nil {
		return err
	}
	sh, err := b.costShared(es, st)
	if err != nil {
		return err
	}
	es.n, es.cost, es.rawUnset = n, make([]cost, n), sh.rawUnset
	for j := 0; j < n; j++ {
		id := e.IDs[j]
		if err := st.name("ids", id, id); err != nil {
			return err
		}
		key := memberKey{e.Table, id}
		if _, dup := seen[key]; dup {
			return st.input("ids", id, "is named twice in the entry and its guards")
		}
		seen[key] = struct{}{}
		c, err := b.costMember(es, st, &sh, j)
		if err != nil {
			return err
		}
		es.cost[j] = c
	}
	es.arrays = 1 + flag(e.Scores != nil) + flag(e.Revs != nil) + flag(e.Each != nil) + flag(e.About != nil)
	es.head = count(func(w sink) { emitMember(w, headView(e)) })
	if es.class == classChange {
		es.lineHead = lineHead(e)
		es.lineArrays = lineArrays + flag(e.About != nil)
	}
	return nil
}

// checkCells checks the cell references of an entry.
func checkCells(st site, e *Entry) error {
	if e.From != "" {
		if err := st.cell("from", e.From); err != nil {
			return err
		}
	}
	if e.To != "" {
		return st.cell("to", e.To)
	}
	return nil
}

func flag(on bool) int {
	if on {
		return 1
	}
	return 0
}

// headView is the entry with its member arrays emptied, and nothing else
// changed: what its wire form costs before any member.
func headView(e *Entry) *Entry {
	v := *e
	v.IDs = v.IDs[:0]
	if v.Scores != nil {
		v.Scores = v.Scores[:0]
	}
	if v.Revs != nil {
		v.Revs = v.Revs[:0]
	}
	if v.Each != nil {
		v.Each = v.Each[:0]
	}
	if v.About != nil {
		v.About = v.About[:0]
	}
	return &v
}

// costMember costs member j: its items in the member arrays, its share of
// the generated line, the raw bytes of its effective fields and the field
// observations it makes.
func (b *builder) costMember(es *entryState, st site, sh *shared, j int) (cost, error) {
	e, id := es.e, es.e.IDs[j]
	qid := quoted(id)
	c := cost{req: qid}
	after := lineScoreBytes
	if e.Scores != nil {
		s := e.Scores[j]
		if s == "" || !utf8.ValidString(s) {
			return c, st.input("scores", id, "is empty or not UTF-8")
		}
		qs := quoted(s)
		c.req += qs
		if qs > after {
			after = qs
		}
	}
	if e.Revs != nil {
		if !canonicalUint(e.Revs[j]) {
			return c, st.input("revs", id, "is not a canonical decimal string")
		}
		c.req += quoted(e.Revs[j])
	}
	aboutBytes := 0
	if e.About != nil {
		if err := st.name("about", id, e.About[j]); err != nil {
			return c, err
		}
		aboutBytes = quoted(e.About[j])
		c.req += aboutBytes
		c.about = 1
	}
	eff, err := b.effective(es, st, sh, j, &c)
	if err != nil {
		return c, err
	}
	if es.class == classChange {
		c.line = qid + lineScoreBytes + after + 2*lineRevBytes + aboutBytes +
			len(lineChangeOpen) + eff + len(lineChangeUnset) + sh.unsetArr + len(lineChangeClose)
	} else {
		c.obs = len(sh.names)
	}
	return c, nil
}

// effective costs the fields member j sets: the shared set with the member's
// own each object laid over it. It adds the each object's request bytes, the
// raw record bytes and the observations to c, and returns the encoded size
// of the effective set object, which its log line carries.
func (b *builder) effective(es *entryState, st site, sh *shared, j int, c *cost) (int, error) {
	items, itemBytes, raw, extra := len(sh.set), sh.setItems, sh.setRaw, 0
	if es.e.Each != nil {
		m := es.e.Each[j]
		id := es.e.IDs[j]
		eachItems := 0
		for k, v := range m {
			if !pairOK(k, v) {
				return 0, st.fault("each", id, m)
			}
			qk, qv := quoted(k), quoted(v)
			eachItems += qk + 1 + qv
			if old, ok := sh.set[k]; ok {
				itemBytes += qv - old.qv
				raw += len(v) - old.rawv
			} else {
				items++
				itemBytes += qk + 1 + qv
				raw += len(k) + len(v)
			}
			if _, ok := sh.names[k]; !ok {
				extra++
			}
		}
		c.req += 2 + eachItems + max(len(m)-1, 0)
	}
	if items > LimitFieldsPerMember {
		return 0, st.limit(boundFields, LimitFieldsPerMember, items, es.e.IDs[j], "each")
	}
	if es.class == classChange {
		c.argv = raw
		c.obs = len(sh.names) + extra
	}
	return 2 + itemBytes + max(items-1, 0), nil
}

// costRows checks and costs a rows entry: its members are its add names then
// its del names. A row named for adding and for deleting in one entry is
// refused (section 3, ROWCONFLICT); the names are row names, without control
// characters.
func (b *builder) costRows(es *entryState, st site) error {
	e := es.e
	es.n = len(e.Add) + len(e.Del)
	if es.n == 0 {
		return st.input("add/del", "", "names no row")
	}
	es.cost = make([]cost, es.n)
	adds := make(map[string]struct{}, len(e.Add))
	for j := 0; j < es.n; j++ {
		row, field := rowAt(e, j)
		if err := st.name(field, "", row); err != nil {
			return err
		}
		if strings.IndexFunc(row, unicode.IsControl) >= 0 {
			return st.input(field, "", "has a control character")
		}
		if j < len(e.Add) {
			adds[row] = struct{}{}
		} else if _, clash := adds[row]; clash {
			return st.input("del", "", "names a row the entry adds")
		}
		es.cost[j].req = quoted(row)
	}
	es.head = count(func(w sink) { emitRows(w, &Entry{Kind: KindRows, Table: e.Table}) })
	return b.costGuards(es, st, make(map[memberKey]struct{}))
}

// rowAt is row j of a rows entry's members, and the field it is in.
func rowAt(e *Entry, j int) (string, string) {
	if j < len(e.Add) {
		return e.Add[j], "add"
	}
	return e.Del[j-len(e.Add)], "del"
}

// costNote checks and costs note i of an entry: its meta and about IDs. A
// note is not cut, so one over the about bound, or whose line is over the
// line bound, is refused.
func (b *builder) costNote(st site, i int, n *Note) (noteState, error) {
	fail := func(bd bound, limit, actual int) error {
		return &LimitError{Bound: bd.name, Section: bd.section, Limit: limit, Actual: actual, Entry: st.entry, Table: st.table, Field: "notes", Note: i}
	}
	if err := checkMeta(st, "notes", n.Meta); err != nil {
		return noteState{}, err
	}
	if len(n.About) > b.bounds.AboutIDs {
		return noteState{}, fail(boundAbout, b.bounds.AboutIDs, len(n.About))
	}
	for _, a := range n.About {
		if err := st.name("notes", "", a); err != nil {
			if le, ok := err.(*LimitError); ok {
				le.Note = i
			}
			return noteState{}, err
		}
	}
	line := len(lineEnvelope) + 1 + len(lineNoteOpen) + objectBytes(n.Meta) + 1
	if line > b.bounds.LineBytes {
		return noteState{}, fail(boundLineBytes, b.bounds.LineBytes, line)
	}
	sz := count(func(w sink) { emitNote(w, n) })
	return noteState{use: usage{notes: 1, noteBytes: sz, about: len(n.About), argv: line}}, nil
}
