package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

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
	s := &sprint.Snapshot{Now: st.now(), Epoch: st.epoch, Cleared: st.cleared, Prefix: st.Names.Prefix}
	stored := make([]string, len(tables))
	for i, t := range tables {
		stored[i] = st.Names.Table(t)
	}
	shapes, err := st.shapes(ctx, stored)
	if err != nil {
		return nil, err
	}
	tbls, err := st.placedRecords(ctx, tables, shapes)
	if err != nil {
		return nil, err
	}
	for i, t := range tbls {
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

// placedRecords is each table at its shape: its rows, texts and properties, and
// every placed record at the shape's revision, from the load cache where it holds
// the table (loadcache.go), else read whole (the tables read whole share one
// exchange for their cells' member ids) and kept in the cache.
func (st *Store) placedRecords(ctx context.Context, tables []string, shapes []ntable.Table) ([]*sprint.Table, error) {
	lc := st.loadCache()
	tbls := make([]*sprint.Table, len(shapes))
	var whole []ntable.Table
	var wholeAt []int
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
		tbls[i] = t
		if lc != nil {
			hit, err := st.placedFromCache(ctx, lc, t, shape)
			if err != nil {
				return nil, err
			}
			if hit {
				continue
			}
		}
		whole, wholeAt = append(whole, shape), append(wholeAt, i)
	}
	if len(whole) == 0 {
		return tbls, nil
	}
	st.stats().reads.Add(1)
	// each table's mark is read before its records (markOf)
	marks := make([]string, len(whole))
	keep := make([]bool, len(whole))
	for k, shape := range whole {
		if lc == nil {
			break
		}
		var err error
		if marks[k], keep[k], err = st.markOf(ctx, shape); err != nil {
			return nil, err
		}
	}
	ids, err := st.B.CellIDs(ctx, whole)
	if err != nil {
		return nil, err
	}
	for k, shape := range whole {
		t := tbls[wholeAt[k]]
		if err := st.readInto(ctx, t, ids[shape.Name], true); err != nil {
			return nil, err
		}
		if keep[k] {
			lc.keepLoaded(t, shape, marks[k])
		}
	}
	return tbls, nil
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

// The card log index (card-read-speed.w2, docs/SPEC-SPRINT.md section 17, the card
// log index): `card <id>` tells one primary's story from the log lines about it,
// and on 2026-10-04 it read the whole log for them, 240,119 lines (330 MB) at
// the 11:36 PM backup, 3.4 s a call. The tick indexes the log by card as it
// grows (keepLogIndex, from keepWhere): each line's stream id under every name
// a card read finds it by (logKeys), up to a cursor, the last line indexed. A
// card read takes its ids from the index, those lines by id, and the lines after
// the cursor, the log's tail the tick has not indexed yet, never the log from
// its start. Indexing is idempotent: a line indexed twice is one entry, and a
// cursor written back by a racing tick only makes the next read's tail longer.
const (
	keyLogIndex   = "logindex"   // ZSET, score 0, members "<name>\x00<log stream id>"
	keyLogIndexed = "logindexed" // STRING, the stream id of the last line indexed
)

// the index is an epoch's sprint key: teardown removes it with the log
func init() { sprintKeys = append(sprintKeys, keyLogIndex, keyLogIndexed) }

// logIndexPages is how many pages of the log one tick indexes, at most: a log
// that was never indexed (a store from before the index) is caught up a few
// ticks at a time, never in one long tick.
const logIndexPages = 4

// logIndex is a store that keeps the card log index; each call is one exchange.
type logIndex interface {
	// logIndexed is the index's cursor ("" when nothing is indexed).
	logIndexed(ctx context.Context) (string, error)
	// indexLog adds the stream ids under each name and sets the cursor.
	indexLog(ctx context.Context, entries map[string][]string, cursor string) error
	// cardLogIDs is the cursor and the stream ids indexed under the name.
	cardLogIDs(ctx context.Context, name string) (string, []string, error)
	// logAt is the log's lines at the stream ids, with their ids, in the
	// order given; an id with no line is left out.
	logAt(ctx context.Context, ids []string) ([]sprint.Line, []string, error)
}

var (
	_ logIndex = (*Redis)(nil)
	_ logIndex = (*Mem)(nil)
)

// logKeys is every name a line is indexed under: each card it names and each
// dot-prefix of that card's id, and a move line's primary, so the lines a
// card read finds under a primary's id are exactly those sprint.Line.About
// says are about it (a work card <primary>.w<n>, a read card
// <primary>.r<n>.<reader>, found under their primary).
func logKeys(l sprint.Line) []string {
	var out []string
	add := func(k string) {
		if k != "" && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	for _, n := range l.Names() {
		for i := range len(n) {
			if n[i] == '.' {
				add(n[:i])
			}
		}
		add(n)
	}
	if l.Note == nil {
		add(l.Primary)
	}
	return out
}

// keepLogIndex is the tick's index of the log's new lines: the cursor read,
// the lines after it (at most logIndexPages pages), and, when there were any,
// their entries and the new cursor written in one exchange. A store that keeps
// no index is left alone.
func (st *Store) keepLogIndex(ctx context.Context) error {
	ix, ok := st.B.(logIndex)
	if !ok {
		return nil
	}
	cur, err := ix.logIndexed(ctx)
	if err != nil {
		return err
	}
	entries := map[string][]string{}
	last := cur
	for range logIndexPages {
		lines, ids, err := st.B.LogSince(ctx, last, logPage)
		if err != nil {
			return err
		}
		for i := range lines {
			for _, k := range logKeys(lines[i]) {
				entries[k] = append(entries[k], ids[i])
			}
		}
		if len(ids) > 0 {
			last = ids[len(ids)-1]
		}
		if len(ids) < logPage {
			break
		}
	}
	if last == cur {
		return nil
	}
	return ix.indexLog(ctx, entries, last)
}

// CardLog is the log's lines about one primary (sprint.Line.About), in log
// order: from the card log index, its indexed lines read by id and the lines
// after the index's cursor; from the whole log when the store keeps no index
// or nothing of the epoch is indexed yet (no tick has run since it began).
// It is what a card's story is told from (cmd/nova-sprint card).
func (st *Store) CardLog(ctx context.Context, id string) ([]sprint.Line, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	about := func(lines []sprint.Line) []sprint.Line {
		var out []sprint.Line
		for _, l := range lines {
			if l.About(id) {
				out = append(out, l)
			}
		}
		return out
	}
	ix, ok := st.B.(logIndex)
	if !ok {
		lines, err := st.Log(ctx)
		return about(lines), err
	}
	cur, ids, err := ix.cardLogIDs(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur == "" {
		lines, err := st.Log(ctx)
		return about(lines), err
	}
	slices.SortFunc(ids, func(a, b string) int {
		switch {
		case streamIDAfter(a, b):
			return 1
		case streamIDAfter(b, a):
			return -1
		}
		return 0
	})
	ids = slices.Compact(ids)
	out, _, err := ix.logAt(ctx, ids)
	if err != nil {
		return nil, err
	}
	after := cur
	for {
		lines, tail, err := st.B.LogSince(ctx, after, logPage)
		if err != nil {
			return nil, err
		}
		out = append(out, about(lines)...)
		if len(tail) < logPage {
			return out, nil
		}
		after = tail[len(tail)-1]
	}
}

func (r *Redis) logIndexed(ctx context.Context) (string, error) {
	v, err := r.C.Get(ctx, r.key(keyLogIndexed)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

// logIndexChunk is how many members one ZADD of the index carries.
const logIndexChunk = 1000

func (r *Redis) indexLog(ctx context.Context, entries map[string][]string, cursor string) error {
	var members []redis.Z
	for _, k := range slices.Sorted(maps.Keys(entries)) {
		for _, id := range entries[k] {
			members = append(members, redis.Z{Member: k + "\x00" + id})
		}
	}
	_, err := r.C.TxPipelined(ctx, func(p redis.Pipeliner) error {
		for start := 0; start < len(members); start += logIndexChunk {
			p.ZAdd(ctx, r.key(keyLogIndex), members[start:min(start+logIndexChunk, len(members))]...)
		}
		p.Set(ctx, r.key(keyLogIndexed), cursor, 0)
		return nil
	})
	return err
}

func (r *Redis) cardLogIDs(ctx context.Context, name string) (string, []string, error) {
	p := r.C.Pipeline()
	cur := p.Get(ctx, r.key(keyLogIndexed))
	ms := p.ZRangeByLex(ctx, r.key(keyLogIndex), &redis.ZRangeBy{Min: "[" + name + "\x00", Max: "(" + name + "\x01"})
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return "", nil, err
	}
	members, err := ms.Result()
	if err != nil {
		return "", nil, err
	}
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m[len(name)+1:])
	}
	return cur.Val(), ids, nil
}

func (r *Redis) logAt(ctx context.Context, ids []string) ([]sprint.Line, []string, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	p := r.C.Pipeline()
	cmds := make([]*redis.XMessageSliceCmd, len(ids))
	for i, id := range ids {
		cmds[i] = p.XRangeN(ctx, r.key(keyLog), id, id, 1)
	}
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, err
	}
	var lines []sprint.Line
	var out []string
	for _, c := range cmds {
		for _, m := range c.Val() {
			var l sprint.Line
			if s, ok := m.Values["line"].(string); ok && json.Unmarshal([]byte(s), &l) == nil {
				lines = append(lines, l)
				out = append(out, m.ID)
			}
		}
	}
	return lines, out, nil
}

// The stand-in keeps the index among its machine records, under the epoch's
// names (Names.KeyAt), as the store's keys are named: the entries as one JSON
// record of name -> stream ids, and the cursor.
func (m *Mem) logIndexKeys() (string, string) {
	return keyLogIndex + epochSuffix(m.epoch), keyLogIndexed + epochSuffix(m.epoch)
}

func (m *Mem) logIndexed(context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("logindex")
	if err := m.fail("logindex"); err != nil {
		return "", err
	}
	_, cur := m.logIndexKeys()
	return m.kv[cur], nil
}

func (m *Mem) indexLog(_ context.Context, entries map[string][]string, cursor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("logindex")
	if err := m.fail("logindex"); err != nil {
		return err
	}
	ixKey, cur := m.logIndexKeys()
	ix := map[string][]string{}
	if raw, ok := m.kv[ixKey]; ok {
		if err := json.Unmarshal([]byte(raw), &ix); err != nil {
			return err
		}
	}
	for k, ids := range entries {
		for _, id := range ids {
			if !slices.Contains(ix[k], id) {
				ix[k] = append(ix[k], id)
			}
		}
	}
	b, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	if m.kv == nil {
		m.kv = map[string]string{}
	}
	m.kv[ixKey], m.kv[cur] = string(b), cursor
	return nil
}

func (m *Mem) cardLogIDs(_ context.Context, name string) (string, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("logindex")
	if err := m.fail("logindex"); err != nil {
		return "", nil, err
	}
	ixKey, cur := m.logIndexKeys()
	ix := map[string][]string{}
	if raw, ok := m.kv[ixKey]; ok {
		if err := json.Unmarshal([]byte(raw), &ix); err != nil {
			return "", nil, err
		}
	}
	return m.kv[cur], ix[name], nil
}

func (m *Mem) logAt(_ context.Context, ids []string) ([]sprint.Line, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("logat")
	if err := m.fail("logat"); err != nil {
		return nil, nil, err
	}
	at := map[string]sprint.Line{}
	for _, x := range m.log().lines {
		at[x.id] = x.line
	}
	var lines []sprint.Line
	var out []string
	for _, id := range ids {
		if l, ok := at[id]; ok {
			lines = append(lines, l)
			out = append(out, id)
		}
	}
	return lines, out, nil
}
