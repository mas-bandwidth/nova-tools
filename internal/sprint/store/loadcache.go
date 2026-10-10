package store

import (
	"context"
	"errors"
	"maps"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The load cache (docs/SPEC-SPRINT.md section 14, The server, "The load cache").
// On 2026-10-10 the server read the fleet table whole about 56 times a second
// (ns_table_read_set, 25 to 30 ms each, 62% of the store's one thread): every take,
// view and friends row read loaded the table's every record, and most of those are
// the finished cards of its ok and failed cells, which grow with every landing. A
// process keeps, for each table it loads, the placed records the last whole read
// found, at the revision they were read at; a later load of the table reads its
// shape, and when the revision moved brings the kept records up to it from the
// table's change stream (TableChanger: the records the writes since changed, read
// again), as the tick's twin does (twin.go catchUp). A table whose stream does not
// account for every revision between, whose kept records do not add up to the
// shape's counts, or that the cache does not hold at its epoch is read whole, and
// that read is kept. So a load gives the records a whole read of the same revision
// gives (tla/DirtyTickRead.tla, Catchup and TwinIsTheStore: the change stream names
// every record a write changed), and costs the shape and the changed records
// instead of the table.
//
// Every load gets its own copy of the records (each card and its fields): nothing a
// caller does to its snapshot reaches the cache or another caller's snapshot.

// LoadCache is a process's kept table records of one store, by stored table name.
// The zero value is empty; its methods are safe from many goroutines.
type LoadCache struct {
	mu     sync.Mutex
	tables map[string]*cachedTable
}

// cachedTable is one table's kept placed records, at t.Revision of epoch.
type cachedTable struct {
	mu    sync.Mutex
	epoch uint64
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
// (its tables are another epoch's), or its store keeps no change stream.
func (st *Store) loadCache() *LoadCache {
	lazyMu.Lock()
	lc := st.lc
	lazyMu.Unlock()
	if lc == nil || st.old {
		return nil
	}
	if _, ok := st.B.(TableChanger); !ok {
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
	if ct.t == nil || ct.epoch != shape.Epoch || ct.t.Revision > shape.Revision {
		return false, nil
	}
	if ct.t.Revision < shape.Revision {
		ok, err := st.catchUpCached(ctx, ct, shape)
		if err != nil || !ok {
			return false, err
		}
	}
	for _, c := range ct.t.Cards() {
		t.Put(copyCard(c))
	}
	return true, nil
}

// catchUpCached brings ct to shape's revision: the records the writes between
// changed, named by the change stream, read again at shape's revision and put in
// (placed) or taken out (on no cell, or gone). Nothing of ct changes until every
// read is done; false (the table is dropped from the cache) when the stream does not
// account for every revision between or the result does not add up to the shape's
// counts.
func (st *Store) catchUpCached(ctx context.Context, ct *cachedTable, shape ntable.Table) (bool, error) {
	tc := st.B.(TableChanger) // loadCache holds only a store that has one
	ids, ok, err := tc.TableChanges(ctx, shape.Name, ct.t.Revision, shape.Revision)
	var grant *GrantError
	var gap *GapError
	switch {
	case errors.As(err, &grant), errors.As(err, &gap):
		ok, err = false, nil
	case err != nil:
		return false, err
	}
	if !ok {
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
	ct.t.Revision = shape.Revision
	if why := countsAgree(ct.t, shape, shape.Epoch); why != "" {
		st.stats().mismatch.Add(1)
		st.stats().note("the load cache read a table whole: " + why)
		ct.t = nil
		return false, nil
	}
	return true, nil
}

// keepLoaded keeps the placed records of t, read whole at shape's revision, in the
// load cache, unless the cache holds the table at a later revision already.
func (lc *LoadCache) keepLoaded(t *sprint.Table, shape ntable.Table) {
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
	ct.epoch, ct.t = shape.Epoch, kept
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
