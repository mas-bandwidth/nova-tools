package store

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// sprintKeys are the deployment's keys outside its tables, under its prefix
// (Names.Key): the fence and its generation, the notification stream, the
// judgments, the open subjects, the coordinator's cursor, the streams'
// progress and the callers' results. Each epoch has its own.
var sprintKeys = []string{keyFence, keyGen, keyInbox, keyLog, keyNotes, keyOpen, keyCursor, keyProgress, keyDone, keyQueue}

// machineKeys are the machine's records and the people's goals: one for the
// whole sprint, under its prefix, never per epoch, so a clear keeps them.
var machineKeys = []string{keyMachine, keyHeartbeat, keyStuck, keyCoordinator, keyGoals, keyStrangers, keyTickEnd, keyRules, keyFriends}

// residueSuffixes are the keys of a table the table layer's drop keeps: its
// revision, definition record and change log; and its operation records,
// which a drop by this build removes and one by an older build keeps. The
// identity hash is not residue: drop --definition removes it.
var residueSuffixes = []string{":revision", ":definition", ":changes", ":ops"}

// Epochs is what teardown names of a sprint's epochs: the last (active) one,
// and each earlier one's shape as it was, whose rows, text cells and owned
// cells the drop of the active epoch keeps.
type Epochs struct {
	Last uint64
	Old  map[uint64][]ntable.Table
	// Beating is every machine that may have a beat record: the fleet's
	// members of every epoch, and the unknown machines that beat.
	Beating []string
	// Readers is every reader of the readers table of every epoch: each may
	// have a beat record and a hold (readers.go).
	Readers []string
	// Friends is every friend of the roster: each may have a beat record
	// (friends.go).
	Friends []string
}

// TeardownKeys is every key a deployment leaves after its tables are dropped
// and its view deleted, by exact name: for each of the four tables, what the
// table layer's drop keeps, the rows, text cells and owned cells of every
// earlier epoch, and the member records of the ids it held (ids, by logical
// table, as the table layer holds them); then the sprint's own keys of every
// epoch and the sprint's epoch.
func TeardownKeys(names sprint.Names, ids map[string][]string, epochs Epochs) []string {
	var keys []string
	for _, t := range All {
		stored := names.Table(t)
		for _, s := range residueSuffixes {
			keys = append(keys, ntable.DefKey(stored)+s)
		}
		for _, id := range ids[t] {
			keys = append(keys, names.MemberPrefix(t)+id)
		}
	}
	for _, e := range slices.Sorted(maps.Keys(epochs.Old)) {
		for _, shape := range epochs.Old[e] {
			keys = append(keys, ntable.RowsKeyAt(shape.Name, e), ntable.PropsKeyAt(shape.Name, e))
			for _, row := range shape.Rows {
				keys = append(keys, ntable.RowKeyAt(shape.Name, row.Key, e))
				for _, c := range shape.Columns {
					if c.HasSet() {
						keys = append(keys, ntable.CellKeyAt(shape.Name, row.Key, c.Name, e))
					}
				}
			}
		}
	}
	for e := uint64(1); e <= epochs.Last; e++ {
		// the definition snapshot the table layer keeps of every epoch after
		// the first, the active one's too
		for _, t := range All {
			keys = append(keys, ntable.EpochPrefix(names.Table(t), e)+":definition")
		}
	}
	for e := uint64(0); e <= epochs.Last; e++ {
		for _, k := range sprintKeys {
			keys = append(keys, names.KeyAt(k, e))
		}
	}
	for _, k := range machineKeys {
		keys = append(keys, names.Key(k))
	}
	for _, m := range epochs.Beating {
		keys = append(keys, names.Key(beatKey(m)))
	}
	for _, r := range epochs.Readers {
		keys = append(keys, names.Key(readerBeatKey(r)), names.Key(readerAwayKey(r)))
	}
	for _, f := range epochs.Friends {
		keys = append(keys, names.Key(friendBeatKey(f)))
	}
	return append(keys, names.EpochKey())
}

// Teardown drops the four tables and the view, then deletes every key the
// deployment created that the drops keep, by exact name (TeardownKeys): the
// member records of every id each table's change log names, read before the
// drop, every earlier epoch's rows and cells, the tables' residue, the
// sprint's keys of every epoch and its epoch. Nothing is found by a scan of the
// store. It is the count of keys deleted.
func (st *Store) Teardown(ctx context.Context) (int, error) {
	es, err := st.B.Epoch(ctx)
	if err != nil {
		return 0, err
	}
	ids := map[string][]string{}
	for _, t := range All {
		got, err := st.B.RecordIDs(ctx, st.Names.Table(t))
		if err != nil {
			return 0, fmt.Errorf("the records of %s: %w", st.Names.Table(t), err)
		}
		ids[t] = got
	}
	epochs := Epochs{Last: es.N, Old: map[uint64][]ntable.Table{}}
	names := make([]string, len(All))
	for i, t := range All {
		names[i] = st.Names.Table(t)
	}
	for e := uint64(0); e < es.N; e++ {
		// An epoch that had no write is empty at it (shapes).
		shapes, err := st.At(e).shapes(ctx, names)
		if err != nil {
			return 0, fmt.Errorf("epoch %d of the sprint: %w", e, err)
		}
		epochs.Old[e] = shapes
	}
	beating := map[string]bool{}
	reading := map[string]bool{}
	current, err := st.B.AtEpoch(es.N, false).Shapes(ctx, []string{st.Names.Table(sprint.Fleet), st.Names.Table(sprint.Readers)})
	if err != nil && refusalCode(err) != "NOTABLE" {
		return 0, err
	}
	for _, shapes := range append([][]ntable.Table{current}, mapValues(epochs.Old)...) {
		for _, sh := range shapes {
			var into map[string]bool
			switch sh.Name {
			case st.Names.Table(sprint.Fleet):
				into = beating
			case st.Names.Table(sprint.Readers):
				into = reading
			default:
				continue
			}
			for _, r := range sh.Rows {
				into[r.Key] = true
			}
		}
	}
	if s, err := st.strangers(ctx); err == nil {
		for m := range s {
			beating[m] = true
		}
	}
	epochs.Beating = slices.Sorted(maps.Keys(beating))
	epochs.Readers = slices.Sorted(maps.Keys(reading))
	epochs.Friends = st.friendNames(ctx)
	_ = st.B.ViewDelete(ctx, st.Names.View())
	for _, t := range All {
		if err := st.B.AtEpoch(es.N, false).DropTable(ctx, st.Names.Table(t)); err != nil && refusalCode(err) != "NOTABLE" {
			return 0, err
		}
	}
	return st.B.DeleteKeys(ctx, TeardownKeys(st.Names, ids, epochs))
}

// recordIDsPage is how many change events one exchange of RecordIDs reads.
const recordIDsPage = 1000

// RecordIDs reads the table's change log (ntable.ChangesKey), recordIDsPage
// events an exchange, and returns every member id its events name: every
// record the table created, since a create is a change of place.
func (r *Redis) RecordIDs(ctx context.Context, table string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	start := "-"
	for {
		msgs, err := r.C.XRangeN(ctx, ntable.ChangesKey(table), start, "+", recordIDsPage).Result()
		if err != nil {
			return nil, err
		}
		for _, msg := range msgs {
			s, _ := msg.Values["members"].(string)
			if s == "" || s == "[]" || s == "{}" {
				continue
			}
			var ms []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(s), &ms); err != nil {
				return nil, fmt.Errorf("change %s of %s: its members are unreadable: %w", msg.ID, table, err)
			}
			for _, m := range ms {
				if m.ID != "" && !seen[m.ID] {
					seen[m.ID] = true
					out = append(out, m.ID)
				}
			}
		}
		if len(msgs) < recordIDsPage {
			sort.Strings(out)
			return out, nil
		}
		start = "(" + msgs[len(msgs)-1].ID
	}
}

// deleteChunk is how many keys one DEL names.
const deleteChunk = 512

// DeleteKeys deletes exactly the named keys, in one pipelined exchange.
func (r *Redis) DeleteKeys(ctx context.Context, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	pipe := r.C.Pipeline()
	for start := 0; start < len(keys); start += deleteChunk {
		pipe.Del(ctx, keys[start:min(start+deleteChunk, len(keys))]...)
	}
	cmds, err := pipe.Exec(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range cmds {
		if ic, ok := c.(interface{ Val() int64 }); ok {
			n += int(ic.Val())
		}
	}
	return n, nil
}

// memResidue is what the table layer's drop keeps of a table: the residue
// keys not yet deleted, and the table itself: its member records, unplaced.
type memResidue struct {
	keys  map[string]bool
	table *memTable
}

// keepResidue keeps a dropped table's residue as a drop in the store does:
// the rows and cells of every epoch go, the members unplaced. The caller
// holds m.mu.
func (m *Mem) keepResidue(table string) {
	t := m.tables[table]
	if t == nil {
		return
	}
	r := &memResidue{keys: map[string]bool{}, table: t}
	for _, s := range residueSuffixes[:3] {
		r.keys[ntable.DefKey(table)+s] = true
	}
	for _, mm := range t.members {
		mm.placed, mm.row, mm.col = false, "", ""
	}
	clear(t.epochs)
	m.dropped[table] = r
}

// takeResidue gives a table created again under a dropped name what its drop
// kept, as the store does: its records and its revision.
// The caller holds m.mu.
func (m *Mem) takeResidue(t *memTable) {
	r := m.dropped[t.def.Name]
	if r == nil {
		return
	}
	t.members, t.rev, t.epochs, t.wrote = r.table.members, r.table.rev, r.table.epochs, r.table.wrote
	delete(m.dropped, t.def.Name)
}

func memberPrefix(def ntable.Table) string {
	if def.MemberPrefix == "" {
		return "table::member:"
	}
	return def.MemberPrefix
}

// RecordIDs is every member id the table, or its residue, holds a record of.
func (m *Mem) RecordIDs(_ context.Context, table string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var members map[string]*memMember
	if t := m.tables[table]; t != nil {
		members = t.members
	} else if r := m.dropped[table]; r != nil {
		members = r.table.members
	}
	return slices.Sorted(maps.Keys(members)), nil
}

// DeleteKeys deletes the keys named, as Keys names them. A key it does not
// hold is not counted.
func (m *Mem) DeleteKeys(_ context.Context, keys []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, k := range keys {
		if m.deleteKey(k) {
			n++
		}
	}
	return n, nil
}

// tableKeys is every key of a table's rows, text cells, owned cells and
// member records, live or kept by a drop, mapped to how to delete it. The
// caller holds m.mu.
func (m *Mem) tableKeys(name string, t *memTable) map[string]func() {
	out := map[string]func(){}
	for e, ep := range t.epochs {
		if len(ep.props) > 0 {
			ep := ep
			out[ntable.PropsKeyAt(name, e)] = func() { ep.props = nil }
		}
		if len(ep.rows) == 0 {
			continue
		}
		ep := ep
		out[ntable.RowsKeyAt(name, e)] = func() { ep.rows = nil }
		for _, row := range ep.rows {
			row := row
			out[ntable.RowKeyAt(name, row, e)] = func() { delete(ep.texts, row) }
		}
	}
	for id, mm := range t.members {
		id, mm := id, mm
		out[memberPrefix(t.def)+id] = func() { delete(t.members, id) }
		if mm.placed {
			out[ntable.CellKeyAt(name, mm.row, mm.col, mm.epoch)] = func() {
				for _, x := range t.members {
					if x.placed && x.epoch == mm.epoch && x.row == mm.row && x.col == mm.col {
						x.placed, x.row, x.col = false, "", ""
					}
				}
			}
		}
	}
	return out
}

func (m *Mem) deleteKey(k string) bool {
	for name, t := range m.tables {
		if del, ok := m.tableKeys(name, t)[k]; ok {
			del()
			return true
		}
	}
	for name, r := range m.dropped {
		deleted := r.keys[k]
		delete(r.keys, k)
		if del, ok := m.tableKeys(name, r.table)[k]; ok {
			del()
			deleted = true
		}
		if len(r.keys) == 0 && len(m.tableKeys(name, r.table)) == 0 {
			delete(m.dropped, name)
		}
		if deleted {
			return true
		}
	}
	if v, ok := strings.CutPrefix(k, "view:"); ok {
		if _, held := m.views[v]; held {
			delete(m.views, v)
			return true
		}
	}
	if strings.HasSuffix(k, "sprint:epoch") && m.epochSet {
		m.epochSet, m.epochN, m.cleared, m.owed = false, 0, time.Time{}, false
		return true
	}
	for e, l := range m.logs {
		for _, s := range sprintKeys {
			if strings.HasSuffix(k, "sprint:"+s+epochSuffix(e)) && sprintKey(l, s, true) {
				return true
			}
		}
	}
	for s := range m.kv {
		if strings.HasSuffix(k, "sprint:"+s) {
			delete(m.kv, s)
			return true
		}
	}
	return false
}

func epochSuffix(e uint64) string {
	if e == 0 {
		return ""
	}
	return "@" + strconv.FormatUint(e, 10)
}

// sprintKey says the sprint key named s exists in an epoch's log, as the
// store would hold it, and deletes it when del is set.
func sprintKey(l *memLog, s string, del bool) bool {
	var held bool
	switch s {
	case keyFence:
		held = l.fence != nil
		if del {
			l.fence = nil
		}
	case keyGen:
		held = l.gen != 0
		if del {
			l.gen = 0
		}
	case keyInbox:
		held = len(l.inbox) > 0
		if del {
			l.inbox = nil
		}
	case keyLog:
		held = len(l.lines) > 0
		if del {
			l.lines = nil
		}
	case keyNotes:
		held = len(l.notes) > 0
		if del {
			l.notes = map[string]sprint.Note{}
		}
	case keyOpen:
		held = len(l.open) > 0
		if del {
			l.open = map[string]string{}
		}
	case keyCursor:
		held = l.cursor != ""
		if del {
			l.cursor = ""
		}
	case keyQueue:
		held = len(l.queue) > 0
		if del {
			l.queue = nil
		}
	case keyProgress:
		held = len(l.progress) > 0
		if del {
			l.progress = map[string]time.Time{}
		}
	case keyDone:
		held = len(l.done) > 0
		if del {
			l.done = map[string]string{}
		}
	}
	return held
}

// Keys is every key the stand-in holds, named as the store would name them
// for a deployment under names: each table's definition and residue, its rows,
// text cells, owned cells and member records of every epoch; each dropped
// table's residue and what its drop kept; the views; the sets of tables and
// views; and the sprint's keys of every epoch and its epoch. For tests.
func (m *Mem) Keys(names sprint.Names) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for name, t := range m.tables {
		def := ntable.DefKey(name)
		keys = append(keys, def, ntable.IdentityKey(name))
		for _, s := range residueSuffixes[:3] {
			keys = append(keys, def+s)
		}
		if len(t.ops) > 0 {
			keys = append(keys, def+":ops")
		}
		keys = slices.AppendSeq(keys, maps.Keys(m.tableKeys(name, t)))
	}
	if len(m.tables) > 0 {
		keys = append(keys, "tables")
	}
	for name, r := range m.dropped {
		keys = slices.AppendSeq(keys, maps.Keys(r.keys))
		keys = slices.AppendSeq(keys, maps.Keys(m.tableKeys(name, r.table)))
	}
	for v := range m.views {
		keys = append(keys, "view:"+v)
	}
	if len(m.views) > 0 {
		keys = append(keys, "views")
	}
	if m.epochSet {
		keys = append(keys, names.EpochKey())
	}
	for e, l := range m.logs {
		for _, s := range sprintKeys {
			if sprintKey(l, s, false) {
				keys = append(keys, names.KeyAt(s, e))
			}
		}
	}
	for s := range m.kv {
		keys = append(keys, names.Key(s))
	}
	sort.Strings(keys)
	return keys
}

func mapValues(m map[uint64][]ntable.Table) [][]ntable.Table {
	var out [][]ntable.Table
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
