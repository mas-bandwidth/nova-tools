package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "unsafe"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
)

// errMoved is a read that saw a table change between its exchanges.
var errMoved = errors.New("a table changed while it was read")

// movedError is errMoved in a load, naming the table.
type movedError struct{ table string }

func (e *movedError) Unwrap() error { return errMoved }

func (e *movedError) Error() string { return "table " + e.table + " changed while it was read" }

// Load is the set read of a step: the tables' shapes (one exchange), their
// cells' member ids (one exchange), every member's place, score, revision and
// fields (one read set per ntable.LimitReadSetMembers members), and the open
// judgments. A table's read sets must all see the revision its shape saw, or
// the read is taken again, after a jittered wait, up to LoadTries reads and
// RetryBudget asleep: the snapshot is one consistent state of each table.
// extras names records to read as well (unplaced ones included), by table.
func (st *Store) Load(ctx context.Context, tables []string, extras func(*sprint.Snapshot) map[string][]string) (*sprint.Snapshot, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	var last *movedError
	r := st.retry(ctx)
	for r.next(LoadTries) {
		s, err := st.loadOnce(ctx, tables, extras)
		var moved *movedError
		if errors.As(err, &moved) {
			last = moved
			continue
		}
		return s, err
	}
	return nil, fmt.Errorf("the tables are busy: table %s kept changing while it was read, %d reads in %s; nothing was changed; run the verb again",
		last.table, r.tries, r.slept().Round(time.Millisecond))
}

func (st *Store) loadOnce(ctx context.Context, tables []string, extras func(*sprint.Snapshot) map[string][]string) (*sprint.Snapshot, error) {
	s := &sprint.Snapshot{Now: st.now(), Epoch: st.epoch, Cleared: st.cleared}
	stored := make([]string, len(tables))
	for i, t := range tables {
		stored[i] = st.Names.Table(t)
	}
	shapes, err := st.shapes(ctx, stored)
	if err != nil {
		return nil, err
	}
	if len(tables) > 0 {
		st.stats().reads.Add(1)
	}
	ids, err := st.B.CellIDs(ctx, shapes)
	if err != nil {
		return nil, err
	}
	for i, shape := range shapes {
		if st.pinned && shape.Epoch != st.epoch {
			return nil, errCleared
		}
		t := sprint.NewTable(tables[i])
		t.Epoch, t.Revision = shape.Epoch, shape.Revision
		t.SetProps(shape.Props)
		for _, r := range shape.Rows {
			t.SetRows(append(t.Rows(), r.Key))
			if r.Hidden {
				t.SetHidden(r.Key)
			}
			if len(r.Texts) > 0 {
				t.Texts[r.Key] = r.Texts
			}
		}
		if err := st.readInto(ctx, t, ids[shape.Name], true); err != nil {
			return nil, err
		}
		switch tables[i] {
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
	s.Actor = st.Actor
	if s.Coordinator, err = st.B.Coordinator(ctx); err != nil {
		return nil, err
	}
	if s.SeatGeneration, err = st.seatGeneration(ctx); err != nil {
		return nil, err
	}
	if extras != nil {
		for table, want := range extras(s) {
			t := s.T(table)
			var missing []string
			for _, id := range want {
				if t.Card(id) == nil {
					missing = append(missing, id)
				}
			}
			if err := st.readInto(ctx, t, st.sids(missing), false); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

// HeldBack is sprint.HeldBack over the work table's waiting column, read
// alone (workColumn). A table with nothing waiting is read no further than its
// shape.
func (st *Store) HeldBack(ctx context.Context) (int, error) {
	t, err := st.workColumn(ctx, sprint.Waiting)
	if err != nil || t == nil {
		return 0, err
	}
	return sprint.HeldBack(&sprint.Snapshot{Work: t}), nil
}

// LandedAt is the landed stamps of the work table's landed column, read alone
// (workColumn), for the landing rate of the ETA (sprint.LandingRate); a card
// with no stamp is left out.
func (st *Store) LandedAt(ctx context.Context) ([]time.Time, error) {
	t, err := st.workColumn(ctx, sprint.Landed)
	if err != nil || t == nil {
		return nil, err
	}
	var out []time.Time
	for _, c := range t.Column(sprint.Landed) {
		if at, err := time.Parse(time.RFC3339, c.F("landed")); err == nil {
			out = append(out, at)
		}
	}
	return out, nil
}

// workColumn is the work table with the cards of one column read: its shape,
// the ids of that column's cells, and their records, taken again as Load takes
// a read that saw the table move; nil when the column is empty.
func (st *Store) workColumn(ctx context.Context, col string) (*sprint.Table, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	r := st.retry(ctx)
	for r.next(LoadTries) {
		t, err := st.workColumnOnce(ctx, col)
		if !errors.Is(err, errMoved) {
			return t, err
		}
	}
	return nil, fmt.Errorf("the tables are busy: the work table kept changing while its %s cards were read, %d reads in %s", col, r.tries, r.slept().Round(time.Millisecond))
}

func (st *Store) workColumnOnce(ctx context.Context, col string) (*sprint.Table, error) {
	shapes, err := st.shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil {
		return nil, err
	}
	shape := shapes[0]
	if st.pinned && shape.Epoch != st.epoch {
		return nil, errCleared
	}
	j := shape.Column(col)
	n := int64(0)
	for _, row := range shape.Rows {
		if j >= 0 && j < len(row.Cells) {
			n += row.Cells[j].Count
		}
	}
	if n == 0 {
		return nil, nil
	}
	// only the column's cells are read: every other set column is read as
	// text, which has no cell ids
	shape.Columns = slices.Clone(shape.Columns)
	for k := range shape.Columns {
		if k != j && shape.Columns[k].HasSet() {
			shape.Columns[k].Projection = ntable.Text
		}
	}
	ids, err := st.B.CellIDs(ctx, []ntable.Table{shape})
	if err != nil {
		return nil, err
	}
	t := sprint.NewTable(sprint.Work)
	t.Epoch, t.Revision = shape.Epoch, shape.Revision
	for _, row := range shape.Rows {
		t.SetRows(append(t.Rows(), row.Key))
	}
	if err := st.readInto(ctx, t, ids[shape.Name], true); err != nil {
		return nil, err
	}
	return t, nil
}

// shapes reads the tables' shapes in one exchange. Reading an earlier epoch,
// a table the table layer holds no definition of at that epoch (NOTABLE: it
// keeps one only from the epoch's first write) had no write at it, and is
// empty at it, when the table itself is there.
func (st *Store) shapes(ctx context.Context, stored []string) ([]ntable.Table, error) {
	out, err := st.B.Shapes(ctx, stored)
	if err == nil || !st.old || refusalCode(err) != "NOTABLE" {
		return out, err
	}
	live, lerr := st.root.Shapes(ctx, stored)
	if lerr != nil {
		return nil, err
	}
	out = make([]ntable.Table, len(stored))
	for i, name := range stored {
		one, err := st.B.Shapes(ctx, []string{name})
		switch {
		case err == nil:
			out[i] = one[0]
		case refusalCode(err) == "NOTABLE":
			empty := live[i]
			empty.Rows, empty.Epoch, empty.Revision = nil, st.epoch, 0
			out[i] = empty
		default:
			return nil, err
		}
	}
	return out, nil
}

// readInto reads ids into t in read sets, each of which must see t's revision.
func (st *Store) readInto(ctx context.Context, t *sprint.Table, ids []string, placed bool) error {
	for start := 0; start < len(ids); start += ntable.LimitReadSetMembers {
		end := min(start+ntable.LimitReadSetMembers, len(ids))
		res, err := st.readSet(ctx, st.Names.Table(t.Name), ids[start:end])
		if err != nil {
			return err
		}
		if res.Revision != t.Revision || (placed && len(res.Missing) > 0) {
			return &movedError{table: st.Names.Table(t.Name)}
		}
		for _, m := range res.Members {
			if placed && !m.Placed {
				return &movedError{table: st.Names.Table(t.Name)}
			}
			c := &sprint.Card{ID: sprint.CardID(m.ID), Score: m.Score, Rev: m.Revision, Fields: m.Fields}
			if m.Placed {
				c.Row, c.Col = m.Row, m.Col
			}
			t.Put(c)
		}
	}
	return nil
}

// readSet is one read set of a table, its records counted (stats.go).
func (st *Store) readSet(ctx context.Context, table string, ids []string) (ntable.ReadSetResult, error) {
	res, err := st.B.ReadSet(ctx, table, ids)
	st.stats().rows.Add(int64(len(res.Members)))
	return res, err
}

// A card read (docs/SPEC-SPRINT.md, card) must not walk the epoch's log or the
// hold set. The tick keeps two hashes outside the counted exchanges
// (keepWhere, TestTheWhereCountsTripsArePinned): the log lines of each primary,
// and, when the work table has moved, that card's hold, needs and needed-by.
// One field is one card. The read takes that field, the lines written after
// the index, and the card's own rows.
//
// cardlog and cardfast are sprint keys, one hash an epoch. TeardownKeys names
// every sprintKeys entry, so a teardown deletes them. The mem stand-in keeps
// the fields off m.kv: Keys lists every machine record, and a key teardown
// does not name would be left behind.

const (
	cardLogKind  = "cardlog"
	cardFastKind = "cardfast"
)

func init() {
	sprintKeys = append(sprintKeys, cardLogKind, cardFastKind)
}

func cardLogCursor(epoch uint64) string { return cardLogical(cardLogKind, epoch, "cursor") }

func cardLogKey(epoch uint64, id string) string {
	return cardLogical(cardLogKind, epoch, "id:"+id)
}

func cardFastRev(epoch uint64) string { return cardLogical(cardFastKind, epoch, "rev") }

func cardFastKey(epoch uint64, id string) string {
	return cardLogical(cardFastKind, epoch, "id:"+id)
}

func cardLogical(kind string, epoch uint64, field string) string {
	return kind + ":" + strconv.FormatUint(epoch, 10) + ":" + field
}

// cardField parses a cardLogical name. The field may itself contain colons
// (id:<primary>).
func cardField(name string) (kind string, epoch uint64, field string, ok bool) {
	kind, rest, found := strings.Cut(name, ":")
	if !found || (kind != cardLogKind && kind != cardFastKind) {
		return "", 0, "", false
	}
	es, field, found := strings.Cut(rest, ":")
	if !found || field == "" {
		return "", 0, "", false
	}
	epoch, err := strconv.ParseUint(es, 10, 64)
	if err != nil {
		return "", 0, "", false
	}
	return kind, epoch, field, true
}

// cardLogLine is one indexed log line with the stream id that placed it, so a
// retry of the same delta does not store it twice.
type cardLogLine struct {
	ID   string      `json:"id"`
	Line sprint.Line `json:"line"`
}

// cardFast is one primary's hold, needs and needed-by at a work-table revision.
type cardFast struct {
	Needs    []sprint.NeedState `json:"needs,omitempty"`
	NeededBy []string           `json:"needed_by,omitempty"`
	Hold     sprint.Hold        `json:"hold"`
}

// cardHeld is the pointer sprint.newHeld returns. This file does not read its
// fields; cardHeldHold is that value's hold method.
type cardHeld struct{ _ [0]byte }

// cardHoldOnce is one reading of the no-stall rule. sprint.Holder calls
// newHeld again for every id, and a tick that counted the whole table would
// repeat the next tick that many times.
//
//go:linkname cardHoldOnce github.com/mas-bandwidth/nova-tools/internal/sprint.newHeld
func cardHoldOnce(h sprint.HeldState, now time.Time) *cardHeld

//go:linkname cardHeldHold github.com/mas-bandwidth/nova-tools/internal/sprint.(*held).hold
func cardHeldHold(c *cardHeld, id string) sprint.Hold

// quietGet reads a machine record without counting the exchange on *Mem.
// keepWhere's idle tick is pinned at two counted exchanges (the work shape and
// the where record); the index and the hold cache ride along uncounted.
// A card hash field is not a machine record (cardHashGet).
func (st *Store) quietGet(ctx context.Context, name string) (string, bool, error) {
	if kind, epoch, field, ok := cardField(name); ok {
		return st.cardHashGet(ctx, kind, epoch, field)
	}
	switch b := st.B.(type) {
	case *Mem:
		b.mu.Lock()
		defer b.mu.Unlock()
		v, ok := b.kv[name]
		return v, ok, nil
	case *Redis:
		v, err := b.C.Get(ctx, b.Names.Key(name)).Result()
		if errors.Is(err, redis.Nil) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return v, true, nil
	default:
		kv, err := st.kv()
		if err != nil {
			return "", false, err
		}
		return kv.GetKey(ctx, name)
	}
}

// quietSet writes one machine record the same way quietGet reads.
func (st *Store) quietSet(ctx context.Context, name, value string) error {
	return st.quietPuts(ctx, map[string]string{name: value})
}

// quietPuts writes machine records. On *Mem it is one uncounted update. On
// *Redis it is a pipeline per quietPutChunk keys. A wrapper that is neither
// falls back to counted SetKey. Card hash fields go to the epoch hash, not
// to the machine records.
func (st *Store) quietPuts(ctx context.Context, kvs map[string]string) error {
	if len(kvs) == 0 {
		return nil
	}
	rest := map[string]string{}
	var hashed []cardPut
	for k, v := range kvs {
		if kind, epoch, field, ok := cardField(k); ok {
			hashed = append(hashed, cardPut{kind, epoch, field, v})
			continue
		}
		rest[k] = v
	}
	if err := st.putCardFields(ctx, hashed); err != nil {
		return err
	}
	if len(rest) == 0 {
		return nil
	}
	switch b := st.B.(type) {
	case *Mem:
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.kv == nil {
			b.kv = map[string]string{}
		}
		for k, v := range rest {
			b.kv[k] = v
		}
		return nil
	case *Redis:
		keys := make([]string, 0, len(rest))
		for k := range rest {
			keys = append(keys, k)
		}
		for start := 0; start < len(keys); start += quietPutChunk {
			end := min(start+quietPutChunk, len(keys))
			_, err := b.C.Pipelined(ctx, func(p redis.Pipeliner) error {
				for _, k := range keys[start:end] {
					p.Set(ctx, b.Names.Key(k), rest[k], 0)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	default:
		kv, err := st.kv()
		if err != nil {
			return err
		}
		for k, v := range rest {
			if err := kv.SetKey(ctx, k, v); err != nil {
				return err
			}
		}
		return nil
	}
}

// cardPut is one field of an epoch's card hash.
type cardPut struct {
	kind  string
	epoch uint64
	field string
	val   string
}

// cardBag is the mem stand-in's card hashes. It is not m.kv, so Keys does not
// list it and a teardown leaves nothing behind. Keyed by the stand-in's state,
// which every epoch view of that stand-in shares.
type cardBag struct {
	mu sync.Mutex
	kv map[string]string
}

var cardBags sync.Map // *memState -> *cardBag

func (m *memState) cardBag() *cardBag {
	if v, ok := cardBags.Load(m); ok {
		return v.(*cardBag)
	}
	b := &cardBag{kv: map[string]string{}}
	actual, _ := cardBags.LoadOrStore(m, b)
	return actual.(*cardBag)
}

// memStateOf is the mem stand-in behind b, including a test probe that embeds
// *Mem. Nil when b is not that stand-in.
func memStateOf(b Backend) *memState {
	if m, ok := b.(*Mem); ok && m != nil {
		return m.memState
	}
	v := reflect.ValueOf(b)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() {
		return nil
	}
	e := v.Elem()
	if e.Kind() != reflect.Struct {
		return nil
	}
	memType := reflect.TypeOf((*Mem)(nil))
	for i := 0; i < e.NumField(); i++ {
		f := e.Field(i)
		if !f.CanInterface() || f.Type() != memType {
			continue
		}
		if m, ok := f.Interface().(*Mem); ok && m != nil {
			return m.memState
		}
	}
	return nil
}

func (st *Store) cardHashGet(ctx context.Context, kind string, epoch uint64, field string) (string, bool, error) {
	if ms := memStateOf(st.B); ms != nil {
		b := ms.cardBag()
		b.mu.Lock()
		defer b.mu.Unlock()
		v, ok := b.kv[cardLogical(kind, epoch, field)]
		return v, ok, nil
	}
	if r, ok := st.B.(*Redis); ok {
		v, err := r.C.HGet(ctx, r.Names.KeyAt(kind, epoch), field).Result()
		if errors.Is(err, redis.Nil) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return v, true, nil
	}
	kv, err := st.kv()
	if err != nil {
		return "", false, err
	}
	return kv.GetKey(ctx, cardLogical(kind, epoch, field))
}

func (st *Store) putCardFields(ctx context.Context, puts []cardPut) error {
	if len(puts) == 0 {
		return nil
	}
	if ms := memStateOf(st.B); ms != nil {
		b := ms.cardBag()
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, p := range puts {
			b.kv[cardLogical(p.kind, p.epoch, p.field)] = p.val
		}
		return nil
	}
	if r, ok := st.B.(*Redis); ok {
		grouped := map[string][]any{}
		var order []string
		for _, p := range puts {
			k := r.Names.KeyAt(p.kind, p.epoch)
			if _, seen := grouped[k]; !seen {
				order = append(order, k)
			}
			grouped[k] = append(grouped[k], p.field, p.val)
		}
		_, err := r.C.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for _, k := range order {
				vals := grouped[k]
				for start := 0; start < len(vals); start += quietPutChunk * 2 {
					end := min(start+quietPutChunk*2, len(vals))
					pipe.HSet(ctx, k, vals[start:end]...)
				}
			}
			return nil
		})
		return err
	}
	kv, err := st.kv()
	if err != nil {
		return err
	}
	for _, p := range puts {
		if err := kv.SetKey(ctx, cardLogical(p.kind, p.epoch, p.field), p.val); err != nil {
			return err
		}
	}
	return nil
}

// quietRoutes copies the routes onto the snapshot without a counted read.
// *Mem holds them on the stand-in; calling routes would trip Fail("routes")
// and a tick is pinned at one such read (TestATickReadsTheRoutesOnce).
func (st *Store) quietRoutes(ctx context.Context, snap *sprint.Snapshot) {
	if snap == nil || snap.Routes != nil {
		return
	}
	if ms := memStateOf(st.B); ms != nil {
		ms.mu.Lock()
		routes := make([]sprint.Route, len(ms.routes))
		copy(routes, ms.routes)
		tiers := map[string][]string{}
		for t, a := range ms.tiers {
			tiers[t] = append([]string(nil), a...)
		}
		bars := ms.bars
		ms.mu.Unlock()
		sort.Slice(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
		RouteSet{Routes: routes, Tiers: tiers, Bars: bars}.into(snap)
		return
	}
	if _, ok := st.B.(*Redis); ok {
		if set, err := st.routes(ctx); err == nil {
			set.into(snap)
		}
	}
}

// quietPutChunk is how many records one Redis pipeline writes.
const quietPutChunk = 128

// quietReaderStates fills the snapshot's reader states from the beat records
// without a counted exchange (ReaderStates' getKeys would count).
func (st *Store) quietReaderStates(ctx context.Context, s *sprint.Snapshot) {
	if s == nil || s.Readers == nil || s.ReaderStates != nil {
		return
	}
	out := map[string]string{}
	for _, r := range s.Readers.Rows() {
		var b sprint.Beat
		var hold readerHold
		if raw, ok, err := st.quietGet(ctx, readerBeatKey(r)); err == nil && ok {
			_ = json.Unmarshal([]byte(raw), &b)
		}
		if raw, ok, err := st.quietGet(ctx, readerAwayKey(r)); err == nil && ok {
			_ = json.Unmarshal([]byte(raw), &hold)
		}
		out[r] = sprint.ReaderState(hold.Away, b, s.Now)
		if hold.Retired {
			out[r] = sprint.ReaderRetired
		}
		if hold.Held {
			out[r] = sprint.ReaderHeld
		}
	}
	s.ReaderStates = out
}

// linePrimaries is every primary a log line is about. A work or read card's
// id indexes under its primary, which is what Line.About matches.
func linePrimaries(l sprint.Line) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name == "" {
			return
		}
		p := name
		if i := strings.IndexByte(name, '.'); i > 0 {
			p = name[:i]
		}
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, n := range l.Names() {
		add(n)
	}
	if l.Note == nil {
		add(l.Primary)
	}
	return out
}

// logSince is the log's lines after the stream id ("" from the start), in
// order, with their stream ids.
func (st *Store) logSince(ctx context.Context, after string) ([]sprint.Line, []string, error) {
	var lines []sprint.Line
	var ids []string
	for {
		ls, is, err := st.B.LogSince(ctx, after, logPage)
		if err != nil {
			return nil, nil, err
		}
		lines = append(lines, ls...)
		ids = append(ids, is...)
		if len(is) < logPage {
			return lines, ids, nil
		}
		after = is[len(is)-1]
	}
}

// indexLog brings the per-card log index up to the lines it reads. The cursor
// is written after the card keys, so a retry that lost the cursor replaces
// from the start and a retry that kept it merges by stream id. Caught up, it
// reads the tail id and the cursor and writes nothing.
func (st *Store) indexLog(ctx context.Context) error {
	tail, _, err := st.B.Tails(ctx)
	if err != nil {
		return err
	}
	key := cardLogCursor(st.epoch)
	cursor, has, err := st.quietGet(ctx, key)
	if err != nil {
		return err
	}
	if has && cursor == tail {
		return nil
	}
	if tail == "" {
		return nil
	}
	lines, ids, err := st.logSince(ctx, cursor)
	if err != nil {
		return fmt.Errorf("card log: %w", err)
	}
	// A cursor that is not in this log (the mem stand-in's hash outlives a
	// teardown that deleted the log) is not a delta. Index from the start.
	if cursor != "" && len(ids) == 0 {
		cursor = ""
		lines, ids, err = st.logSince(ctx, "")
		if err != nil {
			return fmt.Errorf("card log: %w", err)
		}
	}
	if len(ids) == 0 {
		if cursor != tail {
			return st.quietSet(ctx, key, tail)
		}
		return nil
	}
	grouped := map[string][]cardLogLine{}
	var order []string
	for i, l := range lines {
		for _, p := range linePrimaries(l) {
			if _, ok := grouped[p]; !ok {
				order = append(order, p)
			}
			grouped[p] = append(grouped[p], cardLogLine{ID: ids[i], Line: l})
		}
	}
	puts := map[string]string{}
	for _, p := range order {
		merged := grouped[p]
		if cursor != "" {
			raw, ok, err := st.quietGet(ctx, cardLogKey(st.epoch, p))
			if err != nil {
				return err
			}
			if ok {
				var have []cardLogLine
				if json.Unmarshal([]byte(raw), &have) == nil {
					merged = mergeCardLog(have, grouped[p])
				}
			}
		}
		b, err := json.Marshal(merged)
		if err != nil {
			return err
		}
		puts[cardLogKey(st.epoch, p)] = string(b)
	}
	if err := st.quietPuts(ctx, puts); err != nil {
		return fmt.Errorf("card log: %w", err)
	}
	if err := st.quietSet(ctx, key, ids[len(ids)-1]); err != nil {
		return fmt.Errorf("card log: %w", err)
	}
	return nil
}

func mergeCardLog(have, add []cardLogLine) []cardLogLine {
	seen := map[string]bool{}
	for _, x := range have {
		seen[x.ID] = true
	}
	for _, x := range add {
		if x.ID == "" || seen[x.ID] {
			continue
		}
		seen[x.ID] = true
		have = append(have, x)
	}
	return have
}

// logOf is one primary's log lines: the index, plus lines written after the
// cursor. With no index yet (no tick has kept one) it reads the epoch's log
// from the start. It does not write the index; the tick does.
func (st *Store) logOf(ctx context.Context, id string) ([]sprint.Line, error) {
	tail, _, err := st.B.Tails(ctx)
	if err != nil {
		return nil, err
	}
	cursor, has, err := st.quietGet(ctx, cardLogCursor(st.epoch))
	if err != nil {
		return nil, err
	}
	if !has {
		lines, _, err := st.logSince(ctx, "")
		if err != nil {
			return nil, err
		}
		return linesAbout(lines, id), nil
	}
	var stored []cardLogLine
	if raw, ok, err := st.quietGet(ctx, cardLogKey(st.epoch, id)); err != nil {
		return nil, err
	} else if ok && json.Unmarshal([]byte(raw), &stored) != nil {
		stored = nil
	}
	out := make([]sprint.Line, 0, len(stored))
	for _, x := range stored {
		out = append(out, x.Line)
	}
	if cursor == tail || tail == "" {
		return out, nil
	}
	delta, ids, err := st.logSince(ctx, cursor)
	if err != nil {
		return nil, err
	}
	// The cursor is not in this log. The stored lines are from another life
	// of the epoch; read the log that is there.
	if len(ids) == 0 {
		lines, _, err := st.logSince(ctx, "")
		if err != nil {
			return nil, err
		}
		return linesAbout(lines, id), nil
	}
	out = append(out, linesAbout(delta, id)...)
	return out, nil
}

func linesAbout(lines []sprint.Line, id string) []sprint.Line {
	var out []sprint.Line
	for _, l := range lines {
		if l.About(id) {
			out = append(out, l)
		}
	}
	return out
}

// keepCardFast stores each primary's needs, needed-by and hold from a snapshot
// that already holds every card, and the work revision those were counted at,
// last, so a read that sees the revision sees every card. One reading of the
// no-stall rule covers the table. m is the machine keepWhere was given.
func (st *Store) keepCardFast(ctx context.Context, snap *sprint.Snapshot, m Machine) error {
	if snap == nil || snap.Work == nil {
		return nil
	}
	cards := snap.Work.Cards()
	fast := make(map[string]cardFast, len(cards))
	for _, c := range cards {
		needs, neededBy := sprint.NeedsOf(snap, c.ID)
		fast[c.ID] = cardFast{Needs: needs, NeededBy: neededBy}
	}
	st.quietReaderStates(ctx, snap)
	st.quietRoutes(ctx, snap)
	now := snap.Now
	if now.IsZero() {
		now = st.now()
	}
	var hb Heartbeat
	if raw, ok, err := st.quietGet(ctx, keyHeartbeat); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &hb)
	}
	h := sprint.HeldState{Snap: snap, Running: m.Running(), Stopped: m.StoppedBetween, Grace: st.grace(), LastTick: hb.At}
	once := cardHoldOnce(h, now)
	puts := make(map[string]string, len(fast))
	for id, f := range fast {
		f.Hold = cardHeldHold(once, id)
		b, err := json.Marshal(f)
		if err != nil {
			return err
		}
		puts[cardFastKey(st.epoch, id)] = string(b)
	}
	if err := st.quietPuts(ctx, puts); err != nil {
		return fmt.Errorf("card hold: %w", err)
	}
	return st.quietSet(ctx, cardFastRev(st.epoch), strconv.FormatUint(snap.Work.Revision, 10))
}

// cardRecords reads one primary and its work, read and merge cards by identity,
// and the judgments open on it. It does not read the work table whole, and it
// does not fill needs: the caller has them from the tick's cache, or from CardOf.
func (st *Store) cardRecords(ctx context.Context, id string) (CardInfo, error) {
	var v CardInfo
	rs, err := st.readSet(ctx, st.Names.Table(sprint.Work), []string{st.sid(id)})
	if err != nil {
		return v, err
	}
	m, ok := rs.Member(st.sid(id))
	if !ok {
		return v, nil
	}
	v.Primary = card(m)
	attempts := v.Primary.Int("attempt")
	if attempts > 0 {
		var ids []string
		for k := 1; k <= attempts; k++ {
			ids = append(ids, sprint.WorkCardID(id, k))
		}
		if v.Work, err = st.records(ctx, sprint.Fleet, ids); err != nil {
			return v, err
		}
		shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Readers)})
		if err != nil {
			return v, err
		}
		ids = nil
		if len(shapes) > 0 {
			for k := 1; k <= attempts; k++ {
				for _, r := range shapes[0].Rows {
					ids = append(ids, sprint.ReadCardID(id, k, r.Key))
				}
			}
		}
		if len(ids) > 0 {
			if v.Reads, err = st.records(ctx, sprint.Readers, ids); err != nil {
				return v, err
			}
		}
	}
	ms, err := st.records(ctx, sprint.Merge, []string{id})
	if err != nil {
		return v, err
	}
	if len(ms) == 1 {
		v.Merge = ms[0]
	}
	open, err := st.B.OpenNotes(ctx)
	if err != nil {
		return v, err
	}
	open, _ = sprint.SplitOpen(open)
	for _, o := range open {
		if o.Subject() == id || contains(o.Note.Primaries, id) {
			v.Open = append(v.Open, o)
		}
	}
	return v, nil
}

// cardOrCached is the primary's records, needs and hold. The tick's cache wins
// when it was counted at this work revision; otherwise CardOf and Held, which
// read the tables whole.
func (st *Store) cardOrCached(ctx context.Context, id string, rev uint64) (CardInfo, *sprint.Hold, error) {
	rawRev, ok, err := st.quietGet(ctx, cardFastRev(st.epoch))
	if err != nil {
		return CardInfo{}, nil, err
	}
	if ok && rawRev == strconv.FormatUint(rev, 10) {
		raw, found, err := st.quietGet(ctx, cardFastKey(st.epoch, id))
		if err != nil {
			return CardInfo{}, nil, err
		}
		if !found {
			return CardInfo{}, nil, nil
		}
		var fast cardFast
		if json.Unmarshal([]byte(raw), &fast) == nil {
			info, err := st.cardRecords(ctx, id)
			if err != nil || info.Primary == nil {
				return info, nil, err
			}
			info.Needs, info.NeededBy = fast.Needs, fast.NeededBy
			h := fast.Hold
			return info, &h, nil
		}
	}
	info, err := st.CardOf(ctx, id)
	if err != nil || info.Primary == nil || st.old {
		return info, nil, err
	}
	hd, err := st.Held(ctx, id)
	if err != nil {
		return info, nil, nil
	}
	return info, &hd, nil
}

// ReadCard is one primary for card: its rows, its hold and its own log lines.
// A historical epoch (--at-epoch) carries no hold. A pending operation holds
// every card, as Held does, and the rows are still only that card's.
func (st *Store) ReadCard(ctx context.Context, id string) (CardInfo, *sprint.Hold, []sprint.Line, error) {
	var zero CardInfo
	st, err := st.pin(ctx)
	if err != nil {
		return zero, nil, nil, err
	}
	lines, err := st.logOf(ctx, id)
	if err != nil {
		return zero, nil, nil, err
	}
	shapes, err := st.shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil {
		return zero, nil, nil, err
	}
	if len(shapes) == 0 {
		return zero, nil, lines, nil
	}
	rev := shapes[0].Revision
	if !st.old {
		f, err := st.B.ReadFence(ctx)
		if err != nil {
			return zero, nil, nil, err
		}
		if f.Pending != nil {
			info, _, err := st.cardOrCached(ctx, id, rev)
			if err != nil {
				return zero, nil, nil, err
			}
			h := sprint.Hold{ID: id, Place: id, Why: "operation " + f.Pending.ID + " (" + f.Pending.Verb + ") is pending: the tables are a partial state of it; run: nova-sprint repair"}
			return info, &h, lines, nil
		}
	}
	info, hold, err := st.cardOrCached(ctx, id, rev)
	if st.old {
		hold = nil
	}
	return info, hold, lines, err
}
