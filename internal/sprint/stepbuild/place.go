package stepbuild

// usage is what a step holds, or what a piece adds to it, in every unit a
// bound of section 6 measures. Every field is additive, so a piece's usage is
// added to a step's to ask whether the two together are still inside.
type usage struct {
	entryBytes int // encoded bytes of the wire entries, commas between them not counted
	entries    int // wire entries, attached guards included
	tables     int // distinct tables; in a piece's usage, the tables it adds
	candidates int // members changed by create, move and remove entries
	guardOnly  int // members named by guard entries
	rowPairs   int // distinct (table,row) pairs of rows entries
	notes      int
	noteBytes  int // encoded bytes of the notes, commas not counted
	about      int // about IDs of member entries and notes, before dedup
	obs        int // field-value observations
	argv       int // planned argv bytes that can be counted from the request
}

func add(a, b usage) usage {
	return usage{
		entryBytes: a.entryBytes + b.entryBytes,
		entries:    a.entries + b.entries,
		tables:     a.tables + b.tables,
		candidates: a.candidates + b.candidates,
		guardOnly:  a.guardOnly + b.guardOnly,
		rowPairs:   a.rowPairs + b.rowPairs,
		notes:      a.notes + b.notes,
		noteBytes:  a.noteBytes + b.noteBytes,
		about:      a.about + b.about,
		obs:        a.obs + b.obs,
		argv:       a.argv + b.argv,
	}
}

// notesKey is the bytes the notes array adds to a request before its first
// note: the key, the brackets and the comma ahead of it.
const notesKey = len(`,"notes":[]`)

// stepState is the step being filled.
type stepState struct {
	step     Step
	hdr      int // bytes of the request with no entry and no note
	u        usage
	tables   map[string]struct{}
	seen     map[memberKey]struct{} // members the step names, per table
	rows     map[memberKey]bool     // rows the step names: true when added
	attached map[int]struct{}       // input entries whose guards the step holds
}

func (s *stepState) empty() bool { return len(s.step.Entries) == 0 && len(s.step.Notes) == 0 }

// bytes is the exact size of the request with the usage u: the header, the
// entries and the commas between them, the notes array.
func (s *stepState) bytes(u usage) int {
	n := s.hdr + u.entryBytes
	if u.entries > 1 {
		n += u.entries - 1
	}
	if u.notes > 0 {
		n += notesKey + u.noteBytes + u.notes - 1
	}
	return n
}

// over is the first bound the step would break holding d as well, in the
// order of section 6's table, or nil.
func (s *stepState) over(d usage, bd Bounds) *breach {
	t := add(s.u, d)
	if n := s.bytes(t); n > bd.RequestBytes {
		return breachOf(boundRequest, bd.RequestBytes, n)
	}
	for _, c := range []struct {
		b          bound
		limit, use int
	}{
		{boundEntries, bd.Entries, t.entries},
		{boundTables, bd.Tables, t.tables},
		{boundCandidates, bd.Candidates, t.candidates},
		{boundGuardOnly, bd.GuardOnly, t.guardOnly},
		{boundRowPairs, bd.RowPairs, t.rowPairs},
		{boundNotes, bd.Notes, t.notes},
		{boundAbout, bd.AboutIDs, t.about},
		{boundObserved, bd.FieldObservations, t.obs},
		{boundArgv, bd.PlannedArgvBytes, t.argv},
	} {
		if c.use > c.limit {
			return breachOf(c.b, c.limit, c.use)
		}
	}
	return nil
}

// builder is one build.
type builder struct {
	cfg    Config
	bounds Bounds
	steps  []Step
	cur    *stepState // nil when no step is open
	at     Cursor     // what the input has reached
	ops    map[string]struct{}
}

// newBuilder checks the header and resolves the bounds.
func newBuilder(cfg Config) (*builder, error) {
	bd, err := cfg.Bounds.resolve()
	if err != nil {
		return nil, err
	}
	hd := site{entry: -1}
	if !canonicalUint(cfg.Epoch) {
		return nil, hd.input("epoch", "", "is not a canonical decimal string")
	}
	if err := checkHeader(hd, cfg.Header); err != nil {
		return nil, err
	}
	return &builder{cfg: cfg, bounds: bd, ops: map[string]struct{}{}}, nil
}

// reservedKeys are the request members the builder writes itself.
var reservedKeys = map[string]bool{"epoch": true, "op": true, "intent": true, "result": true, "entries": true, "notes": true}

// checkHeader refuses a header member whose key is empty, over long, not
// UTF-8, reserved or repeated, or whose value is not a name.
func checkHeader(hd site, header []Member) error {
	seen := map[string]bool{}
	for _, m := range header {
		if err := hd.name("header key", "", m.Key); err != nil {
			return err
		}
		if reservedKeys[m.Key] || seen[m.Key] {
			return hd.input("header key", "", "is a member the request already has")
		}
		seen[m.Key] = true
		if err := hd.name("header value", "", m.Value); err != nil {
			return err
		}
	}
	return nil
}

// canonicalUint says s is a decimal uint64 as section 4 spells one: 0 or no
// leading zero, digits only, at most 18446744073709551615.
func canonicalUint(s string) bool {
	const top = "18446744073709551615"
	if s == "" || len(s) > len(top) || (s[0] == '0' && s != "0") {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) < len(top) || s <= top
}

// ident asks the caller for step part's identity and checks it: an op and an
// intent together, both within their bounds, the op new to this build.
func (b *builder) ident(part int) (Ident, error) {
	if b.cfg.Ident == nil {
		return Ident{}, nil
	}
	id := b.cfg.Ident(part)
	hd := site{entry: -1}
	if err := hd.text("result", "", id.Result, boundResult, LimitResultBytes); err != nil {
		return id, err
	}
	if (id.Op == "") != (id.Intent == "") {
		return id, hd.input("op", "", "and intent are both set or both empty")
	}
	if id.Op == "" {
		return id, nil
	}
	if err := hd.name("op", "", id.Op); err != nil {
		return id, err
	}
	if err := hd.text("intent", "", id.Intent, boundIntent, LimitIntentBytes); err != nil {
		return id, err
	}
	if _, dup := b.ops[id.Op]; dup {
		return id, hd.input("op", "", "is the op of an earlier step: the two would replay as one")
	}
	b.ops[id.Op] = struct{}{}
	return id, nil
}

// ensureOpen opens the next step when none is open.
func (b *builder) ensureOpen() error {
	if b.cur != nil {
		return nil
	}
	id, err := b.ident(len(b.steps) + 1)
	if err != nil {
		return err
	}
	s := &stepState{
		step:     Step{Epoch: b.cfg.Epoch, Header: b.cfg.Header, Ident: id},
		tables:   map[string]struct{}{},
		seen:     map[memberKey]struct{}{},
		rows:     map[memberKey]bool{},
		attached: map[int]struct{}{},
	}
	s.hdr = count(func(w sink) { emitRequest(w, &s.step) })
	if id.Op != "" {
		s.u.argv = LimitReceiptBytes
	}
	b.cur = s
	return nil
}

// close ends the open step, if it holds anything, at the input's position.
func (b *builder) close() {
	s := b.cur
	b.cur = nil
	if s == nil || s.empty() {
		return
	}
	s.step.Bytes = s.bytes(s.u)
	s.step.Cursor = b.at
	b.steps = append(b.steps, s.step)
}

// finish numbers the steps.
func (b *builder) finish() []Step {
	for i := range b.steps {
		b.steps[i].Part, b.steps[i].Parts = i+1, len(b.steps)
	}
	return b.steps
}

// place puts an entry's members, then its notes, into steps, in order.
func (b *builder) place(es *entryState) error {
	if es.class == classNote {
		b.at = Cursor{Entry: es.idx}
	} else if err := b.placeMembers(es); err != nil {
		return err
	}
	return b.placeNotes(es)
}

// placeMembers cuts the entry's members into wire entries, each as long as
// the bounds let it be, and closes the step whenever nothing more fits. A
// step that holds nothing and still takes no member refuses the build.
func (b *builder) placeMembers(es *entryState) error {
	for pos := 0; pos < es.n; {
		if err := b.ensureOpen(); err != nil {
			return err
		}
		k, d, br := b.fit(es, pos)
		if k == 0 {
			if b.cur.empty() {
				return b.refuse(es, pos, br)
			}
			b.close()
			continue
		}
		b.cur.commit(es, pos, pos+k, d)
		b.at = cursorAfter(es, pos+k)
		pos += k
	}
	return nil
}

// fit is how many members from pos the next wire entry of the open step can
// take, with the usage of that wire entry, and the bound that stopped it.
func (b *builder) fit(es *entryState, pos int) (int, usage, *breach) {
	if es.class == classRows {
		return b.fitRows(es, pos)
	}
	return b.fitMembers(es, pos)
}

// headUsage is what a new wire entry of es costs before its first member: the
// entry itself and, when the step does not hold them yet, its guards. The
// breach is nil when it fits.
func (b *builder) headUsage(es *entryState) (usage, *breach) {
	s := b.cur
	d := usage{entries: 1, entryBytes: es.head}
	if es.class == classChange {
		d.argv = es.lineHead
	}
	for _, t := range es.tables {
		if _, in := s.tables[t]; !in {
			d.tables++
		}
	}
	if _, held := s.attached[es.idx]; !held && len(es.guards) > 0 {
		d = add(d, es.guardUse)
		for _, k := range es.guardKeys {
			if _, in := s.seen[k]; in {
				return d, repeated()
			}
		}
	}
	return d, s.over(d, b.bounds)
}

// fitMembers takes members of a change or guard entry from pos while every
// bound of the step and of the wire entry (its IDs, its generated line) holds
// and no member is one the step already names.
func (b *builder) fitMembers(es *entryState, pos int) (int, usage, *breach) {
	s := b.cur
	d, br := b.headUsage(es)
	if br != nil {
		return 0, d, br
	}
	line, k := es.lineHead, 0
	for j := pos; j < es.n; j++ {
		c := es.cost[j]
		nd, nl := d, line
		nd.entryBytes += c.req
		nd.obs += c.obs
		nd.about += c.about
		if k > 0 {
			nd.entryBytes += es.arrays
		}
		if es.class == classChange {
			nd.candidates++
			nl += c.line
			if k > 0 {
				nl += es.lineArrays
			}
			nd.argv += c.argv + es.rawUnset + (nl - line)
		} else {
			nd.guardOnly++
		}
		if br := s.over(nd, b.bounds); br != nil {
			return k, d, br
		}
		if br := b.wireBreach(es, k+1, nl); br != nil {
			return k, d, br
		}
		if _, in := s.seen[memberKey{es.e.Table, es.e.IDs[j]}]; in {
			return k, d, repeated()
		}
		d, line, k = nd, nl, k+1
	}
	return k, d, nil
}

// wireBreach is the bound a wire entry of ids members and a generated line of
// line bytes would break by itself, or nil.
func (b *builder) wireBreach(es *entryState, ids, line int) *breach {
	switch {
	case ids > b.bounds.EntryIDs:
		return breachOf(boundEntryIDs, b.bounds.EntryIDs, ids)
	case es.class != classChange:
		return nil
	case ids > b.bounds.LineIDs:
		return breachOf(boundLineIDs, b.bounds.LineIDs, ids)
	case line > b.bounds.LineBytes:
		return breachOf(boundLineBytes, b.bounds.LineBytes, line)
	}
	return nil
}

// arrayKey is the bytes a rows entry's add or del array costs before its
// first row: the comma, the key, the brackets.
const arrayKey = len(`,"add":[]`)

// fitRows takes rows of a rows entry from pos while the step stays inside
// its bounds, a row is not added in one entry and deleted in another of the
// step (ROWCONFLICT), and the distinct (table,row) pairs stay under the row
// bound. A row the step already names in the same direction costs nothing
// new (section 3: repeated same-direction names deduplicate).
func (b *builder) fitRows(es *entryState, pos int) (int, usage, *breach) {
	s := b.cur
	d, br := b.headUsage(es)
	if br != nil {
		return 0, d, br
	}
	local := map[string]struct{}{}
	adds, dels, k := 0, 0, 0
	for j := pos; j < es.n; j++ {
		row, _ := rowAt(es.e, j)
		isAdd := j < len(es.e.Add)
		key := memberKey{es.e.Table, row}
		nd := d
		if dir, in := s.rows[key]; in && dir != isAdd {
			return k, d, repeated()
		}
		_, held := s.rows[key]
		if _, in := local[row]; !held && !in {
			nd.rowPairs++
		}
		nd.entryBytes += es.cost[j].req
		n := &dels
		if isAdd {
			n = &adds
		}
		if *n == 0 {
			nd.entryBytes += arrayKey
		} else {
			nd.entryBytes++
		}
		if br := s.over(nd, b.bounds); br != nil {
			return k, d, br
		}
		*n++
		local[row] = struct{}{}
		d, k = nd, k+1
	}
	return k, d, nil
}

// commit puts members [lo, hi) of es into the open step as one wire entry,
// ahead of it the entry's guards when the step does not hold them yet, and
// records them as the step's.
func (s *stepState) commit(es *entryState, lo, hi int, d usage) {
	if _, held := s.attached[es.idx]; !held && len(es.guards) > 0 {
		for _, g := range es.guards {
			s.step.Entries = append(s.step.Entries, Placed{Entry: *g.e, Source: es.idx, Guard: true})
		}
		for _, k := range es.guardKeys {
			s.seen[k] = struct{}{}
		}
		s.attached[es.idx] = struct{}{}
	}
	s.step.Entries = append(s.step.Entries, Placed{Entry: es.part(lo, hi), Source: es.idx})
	for _, t := range es.tables {
		s.tables[t] = struct{}{}
	}
	for j := lo; j < hi; j++ {
		if es.class == classRows {
			row, _ := rowAt(es.e, j)
			s.rows[memberKey{es.e.Table, row}] = j < len(es.e.Add)
		} else {
			s.seen[memberKey{es.e.Table, es.e.IDs[j]}] = struct{}{}
		}
	}
	s.u = add(s.u, d)
	s.u.tables = len(s.tables)
}

// part is members [lo, hi) of the entry as one wire entry: the member arrays
// cut to the range, sharing the input's backing arrays with their capacity
// clipped; the shared fields whole; no guards and no notes.
func (es *entryState) part(lo, hi int) Entry {
	e := *es.e
	e.Guards, e.Notes = nil, nil
	if es.class == classRows {
		na := len(es.e.Add)
		e.Add, e.Del = nil, nil
		if lo < na {
			e.Add = es.e.Add[lo:min(hi, na):min(hi, na)]
		}
		if hi > na {
			e.Del = es.e.Del[max(lo, na)-na : hi-na : hi-na]
		}
		return e
	}
	e.IDs = e.IDs[lo:hi:hi]
	if e.Scores != nil {
		e.Scores = e.Scores[lo:hi:hi]
	}
	if e.Revs != nil {
		e.Revs = e.Revs[lo:hi:hi]
	}
	if e.Each != nil {
		e.Each = e.Each[lo:hi:hi]
	}
	if e.About != nil {
		e.About = e.About[lo:hi:hi]
	}
	return e
}

// cursorAfter is the input's position once members [0, done) of es are
// placed: the last member's table and ID.
func cursorAfter(es *entryState, done int) Cursor {
	id := ""
	if es.class == classRows {
		id, _ = rowAt(es.e, done-1)
	} else {
		id = es.e.IDs[done-1]
	}
	return Cursor{Entry: es.idx, Done: done, Table: es.e.Table, ID: id}
}

// placeNotes puts the entry's notes into steps after its members, each in
// the open step when it fits and in a step of its own when it does not.
func (b *builder) placeNotes(es *entryState) error {
	for i := range es.notes {
		for {
			if err := b.ensureOpen(); err != nil {
				return err
			}
			br := b.cur.over(es.notes[i].use, b.bounds)
			if br == nil {
				break
			}
			if b.cur.empty() {
				return &LimitError{Bound: br.bound.name, Section: br.bound.section, Limit: br.limit, Actual: br.actual, Entry: es.idx, Table: es.e.Table, Field: "notes", Note: i}
			}
			b.close()
		}
		s := b.cur
		s.step.Notes = append(s.step.Notes, PlacedNote{Note: es.e.Notes[i], Source: es.idx})
		s.u = add(s.u, es.notes[i].use)
		b.at.Notes = i + 1
	}
	return nil
}

// refuse is the build's refusal for member pos of es, which no step, however
// empty, can hold: the member's own fields, with the entry's shared ones and
// its guards, break the bound br.
func (b *builder) refuse(es *entryState, pos int, br *breach) error {
	member := ""
	if es.class == classRows {
		member, _ = rowAt(es.e, pos)
	} else {
		member = es.e.IDs[pos]
	}
	return &LimitError{Bound: br.bound.name, Section: br.bound.section, Limit: br.limit, Actual: br.actual, Entry: es.idx, Table: es.e.Table, Member: member, Note: -1}
}
