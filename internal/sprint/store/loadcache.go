package store

import (
	"context"
	"errors"
	"maps"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// The load cache (docs/SPEC-SPRINT.md section 14, store-trips-pipelinedb-bb).
// On 2026-10-10 the server read the fleet table whole about 56 times a second
// (ns_table_read_set, 25 to 30 ms each, 62% of the store's one thread): every take,
// view and friends row read loaded the table's every record, and most of those are
// the finished cards of its ok and failed cells, which grow with every landing. A
// process keeps, for each table it loads, the placed records the last whole read
// found, at the revision they were read at, with the mark of the change event that
// left that revision (TableMarker: its stream id). A later load of the table reads
// its shape and the change stream from the shape's revision back to the cache's
// (one exchange); the cache is used only when the event that left the cache's
// revision is still the one it was read at (the same mark), and then the records
// the writes since changed are read again at the shape's revision, as the tick's
// twin catches up (twin.go catchUp). Read whole, and kept: a table the cache does
// not hold at its epoch, one whose stream does not chain between the two
// revisions, one whose kept records do not add up to the shape's counts, and one
// whose store is behind the cache or whose event at the cache's revision is
// another (the store lost writes: an AOF everysec restart, an RDB or a backup
// restore, at the same epoch with a lower revision, and may have written others
// since, up to and past the cache's revision; the entry is dropped). So a load gives
// the records a whole read of the same revision gives (tla/LoadCache.tla,
// LoadIsSnapshot, with the store's rollback), and costs the shape, one read of the
// stream and the changed records instead of the table.
//
// Every load gets its own copy of the records (each card and its fields): nothing a
// caller does to its snapshot reaches the cache or another caller's snapshot.

// TableMarker is a store whose change stream marks each event (its stream id):
// TableChangesMarked is TableChanges with the marks of the events that left
// revisions from and to ("" for revision 0, the epoch's empty table).
type TableMarker interface {
	TableChangesMarked(ctx context.Context, table string, from, to uint64) (ids []string, fromMark, toMark string, ok bool, err error)
}

// LoadCache is a process's kept table records of one store, by stored table name.
// The zero value is empty; its methods are safe from many goroutines.
type LoadCache struct {
	mu     sync.Mutex
	tables map[string]*cachedTable
}

// cachedTable is one table's kept placed records, at t.Revision of epoch, and the
// mark of the event that left that revision.
type cachedTable struct {
	mu    sync.Mutex
	epoch uint64
	mark  string
	t     *sprint.Table // nil: not held
}

// NewLoadCache is an empty load cache.
func NewLoadCache() *LoadCache { return &LoadCache{} }

// ShareLoadCache has the store's loads keep and reuse the table records in lc (a
// process's one cache of a store); nil is none: every load reads its tables whole.
func (st *Store) ShareLoadCache(lc *LoadCache) {
	lazyMu.Lock()
	st.lc = lc
	lazyMu.Unlock()
}

// loadCache is the store's load cache; nil when it has none, reads an earlier epoch
// (its tables are another epoch's), or its store keeps no marked change stream.
func (st *Store) loadCache() *LoadCache {
	lazyMu.Lock()
	lc := st.lc
	lazyMu.Unlock()
	if lc == nil || st.old {
		return nil
	}
	if _, ok := st.B.(TableMarker); !ok {
		return nil
	}
	return lc
}

// table is the cache's entry of a stored table, made on first use.
func (lc *LoadCache) table(name string) *cachedTable {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if lc.tables == nil {
		lc.tables = map[string]*cachedTable{}
	}
	ct := lc.tables[name]
	if ct == nil {
		ct = &cachedTable{}
		lc.tables[name] = ct
	}
	return ct
}

// placedFromCache puts into t (its shape already set) the placed records of the
// table at shape's revision from the load cache, caught up from the change stream:
// true when it did, false when the table is to be read whole (then keepLoaded keeps
// that read). A record that moved while the changes were read is a movedError, and
// the caller reads again.
func (st *Store) placedFromCache(ctx context.Context, lc *LoadCache, t *sprint.Table, shape ntable.Table) (bool, error) {
	ct := lc.table(shape.Name)
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if ct.t == nil || ct.epoch != shape.Epoch {
		return false, nil
	}
	if ct.t.Revision > shape.Revision {
		// the store is behind the cache: it lost writes (a restart, a restore), or
		// another load read a later shape first; either way the entry is dropped and
		// the table read whole
		ct.t = nil
		return false, nil
	}
	ok, err := st.catchUpCached(ctx, ct, shape)
	if err != nil || !ok {
		return false, err
	}
	for _, c := range ct.t.Cards() {
		t.Put(copyCard(c))
	}
	return true, nil
}

// catchUpCached brings ct to shape's revision: the change stream read back from
// shape's revision to ct's, its event at ct's revision checked to be the one ct was
// read at (its mark), and the records the writes between named read again at
// shape's revision and put in (placed) or taken out (on no cell, or gone). Nothing of
// ct changes until every read is done; false (the entry dropped) when the stream does
// not chain between the two revisions, the mark is another, or the result does not
// add up to the shape's counts.
func (st *Store) catchUpCached(ctx context.Context, ct *cachedTable, shape ntable.Table) (bool, error) {
	tm := st.B.(TableMarker) // loadCache holds only a store that has one
	ids, fromMark, toMark, ok, err := tm.TableChangesMarked(ctx, shape.Name, ct.t.Revision, shape.Revision)
	var grant *GrantError
	var gap *GapError
	switch {
	case errors.As(err, &grant), errors.As(err, &gap):
		ok, err = false, nil
	case err != nil:
		return false, err
	}
	if !ok || fromMark != ct.mark {
		// a gap, or the event that left the cache's revision is another one: the
		// store is not the history the cache was read from
		ct.t = nil
		return false, nil
	}
	seen := map[string]bool{}
	var cards []string
	for _, id := range ids {
		if c := sprint.CardID(id); !seen[c] {
			seen[c] = true
			cards = append(cards, c)
		}
	}
	if len(cards) > 0 {
		found := sprint.NewTable(ct.t.Name)
		found.Revision = shape.Revision
		if err := st.readInto(ctx, found, st.sids(cards), false); err != nil {
			return false, err
		}
		for _, id := range cards {
			if c := found.Card(id); c.Placed() {
				ct.t.Put(c)
			} else {
				ct.t.Drop(id)
			}
		}
	}
	ct.t.Revision, ct.mark = shape.Revision, toMark
	if why := countsAgree(ct.t, shape, shape.Epoch); why != "" {
		st.stats().mismatch.Add(1)
		st.stats().note("the load cache read a table whole: " + why)
		ct.t = nil
		return false, nil
	}
	return true, nil
}

// markOf is the mark of the event that left the table at shape's revision, read
// before the table is read whole: a store that lost writes between gives the
// records a newer history than the mark, and the next use of the entry, finding
// another event at its revision, drops it. ok is false when the stream does not
// give it: the read is then not kept.
func (st *Store) markOf(ctx context.Context, shape ntable.Table) (string, bool, error) {
	_, _, mark, ok, err := st.B.(TableMarker).TableChangesMarked(ctx, shape.Name, shape.Revision, shape.Revision)
	var grant *GrantError
	var gap *GapError
	if errors.As(err, &grant) || errors.As(err, &gap) {
		return "", false, nil
	}
	return mark, ok && err == nil, err
}

// keepLoaded keeps the placed records of t, read whole at shape's revision, in the
// load cache with mark (markOf), unless the cache holds the table at a later
// revision of the same epoch already (its own mark is checked when it is used).
func (lc *LoadCache) keepLoaded(t *sprint.Table, shape ntable.Table, mark string) {
	ct := lc.table(shape.Name)
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if ct.t != nil && ct.epoch == shape.Epoch && ct.t.Revision > shape.Revision {
		return
	}
	kept := sprint.NewTable(t.Name)
	kept.Revision = shape.Revision
	for _, c := range t.Cards() {
		if c.Placed() {
			kept.Put(copyCard(c))
		}
	}
	ct.epoch, ct.mark, ct.t = shape.Epoch, mark, kept
}

// copyCard is a copy of the card that shares nothing with it.
func copyCard(c *sprint.Card) *sprint.Card {
	out := *c
	out.Fields = maps.Clone(c.Fields)
	if out.Fields == nil {
		out.Fields = map[string]string{}
	}
	return &out
}
