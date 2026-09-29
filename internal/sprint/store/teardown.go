package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// sprintKeys are the deployment's keys outside its tables, under its prefix
// (Names.Key): the fence and its generation, the notification stream, the
// judgments, the open subjects, the coordinator's cursor, the streams'
// progress and the callers' results.
var sprintKeys = []string{keyFence, keyGen, keyInbox, keyNotes, keyOpen, keyCursor, keyProgress, keyDone}

// residueSuffixes are the keys of a table the table layer's drop keeps: its
// identity, revision, definition record and change log; and its operation
// records, which a drop by this build removes and one by an older build
// keeps.
var residueSuffixes = []string{":identity", ":revision", ":definition", ":changes", ":ops"}

// TeardownKeys is every key a deployment leaves after its tables are dropped
// and its view deleted, by exact name: for each of the four tables, what the
// table layer's drop keeps and the member records of the ids it held (ids,
// by logical table); then the sprint's own keys.
func TeardownKeys(names sprint.Names, ids map[string][]string) []string {
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
	for _, k := range sprintKeys {
		keys = append(keys, names.Key(k))
	}
	return keys
}

// Teardown drops the four tables and the view, then deletes every key the
// deployment created that the drops keep, by exact name (TeardownKeys): the
// member records of every id each table's change log names, read before the
// drop, the tables' residue and the sprint's keys. Nothing is found by a scan
// of the store. It is the count of keys deleted.
func (st *Store) Teardown(ctx context.Context) (int, error) {
	ids := map[string][]string{}
	for _, t := range All {
		got, err := st.B.RecordIDs(ctx, st.Names.Table(t))
		if err != nil {
			return 0, fmt.Errorf("the records of %s: %w", st.Names.Table(t), err)
		}
		ids[t] = got
	}
	_ = st.B.ViewDelete(ctx, st.Names.View())
	for _, t := range All {
		if err := st.B.DropTable(ctx, st.Names.Table(t)); err != nil && refusalCode(err) != "NOTABLE" {
			return 0, err
		}
	}
	return st.B.DeleteKeys(ctx, TeardownKeys(st.Names, ids))
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
// keys not yet deleted, and its member records, unplaced, under its member
// prefix.
type memResidue struct {
	keys    map[string]bool
	prefix  string
	members map[string]*memMember
	rev     uint64
}

// keepResidue keeps a dropped table's residue as a drop in the store does.
// The caller holds m.mu.
func (m *Mem) keepResidue(table string) {
	t := m.tables[table]
	if t == nil {
		return
	}
	r := &memResidue{keys: map[string]bool{}, prefix: memberPrefix(t.def), members: t.members, rev: t.rev}
	for _, s := range residueSuffixes[:4] {
		r.keys[ntable.DefKey(table)+s] = true
	}
	for _, mm := range t.members {
		mm.placed, mm.row, mm.col = false, "", ""
	}
	m.dropped[table] = r
}

// takeResidue gives a table created again under a dropped name what its drop
// kept, as the store does: its records, unplaced, and its revision. The
// caller holds m.mu.
func (m *Mem) takeResidue(t *memTable) {
	r := m.dropped[t.def.Name]
	if r == nil {
		return
	}
	t.members, t.rev = r.members, r.rev
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
		members = r.members
	}
	var out []string
	for id := range members {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// DeleteKeys deletes the keys named, as Keys names them: member records, a
// dropped table's residue, views and the sprint's keys (a key ending in
// sprint:<name>; the stand-in holds one deployment's). A key it does not hold
// is not counted.
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

func (m *Mem) deleteKey(k string) bool {
	for _, t := range m.tables {
		if id, ok := strings.CutPrefix(k, memberPrefix(t.def)); ok && t.members[id] != nil {
			delete(t.members, id)
			return true
		}
	}
	for name, r := range m.dropped {
		deleted := r.keys[k]
		delete(r.keys, k)
		if id, ok := strings.CutPrefix(k, r.prefix); ok && r.members[id] != nil {
			delete(r.members, id)
			deleted = true
		}
		if len(r.keys) == 0 && len(r.members) == 0 {
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
	for _, s := range sprintKeys {
		if strings.HasSuffix(k, "sprint:"+s) && m.sprintKey(s, true) {
			return true
		}
	}
	return false
}

// sprintKey says the sprint key named s exists, as the store would hold it,
// and deletes it when del is set.
func (m *Mem) sprintKey(s string, del bool) bool {
	var held bool
	switch s {
	case keyFence:
		held = m.fence != nil
		if del {
			m.fence = nil
		}
	case keyGen:
		held = m.gen != 0
		if del {
			m.gen = 0
		}
	case keyInbox:
		held = len(m.inbox) > 0
		if del {
			m.inbox = nil
		}
	case keyNotes:
		held = len(m.notes) > 0
		if del {
			m.notes = map[string]sprint.Note{}
		}
	case keyOpen:
		held = len(m.open) > 0
		if del {
			m.open = map[string]string{}
		}
	case keyCursor:
		held = m.cursor != ""
		if del {
			m.cursor = ""
		}
	case keyProgress:
		held = len(m.progress) > 0
		if del {
			m.progress = map[string]time.Time{}
		}
	case keyDone:
		held = len(m.done) > 0
		if del {
			m.done = map[string]string{}
		}
	}
	return held
}

// Keys is every key the stand-in holds, named as the store would name them
// for a deployment under names: each table's definition, residue, rows, cells
// and member records; each dropped table's residue and records; the views;
// the sets of tables and views; and the sprint's keys. For tests.
func (m *Mem) Keys(names sprint.Names) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for name, t := range m.tables {
		def := ntable.DefKey(name)
		keys = append(keys, def)
		for _, s := range residueSuffixes[:4] {
			keys = append(keys, def+s)
		}
		if len(t.ops) > 0 {
			keys = append(keys, def+":ops")
		}
		if len(t.rows) > 0 {
			keys = append(keys, ntable.RowsKey(name))
		}
		for _, row := range t.rows {
			keys = append(keys, ntable.RowKey(name, row))
		}
		cells := map[string]bool{}
		for id, mm := range t.members {
			keys = append(keys, memberPrefix(t.def)+id)
			if mm.placed {
				cells[ntable.CellKey(name, mm.row, mm.col)] = true
			}
		}
		for c := range cells {
			keys = append(keys, c)
		}
	}
	if len(m.tables) > 0 {
		keys = append(keys, "tables")
	}
	for _, r := range m.dropped {
		for k := range r.keys {
			keys = append(keys, k)
		}
		for id := range r.members {
			keys = append(keys, r.prefix+id)
		}
	}
	for v := range m.views {
		keys = append(keys, "view:"+v)
	}
	if len(m.views) > 0 {
		keys = append(keys, "views")
	}
	for _, s := range sprintKeys {
		if m.sprintKey(s, false) {
			keys = append(keys, names.Key(s))
		}
	}
	sort.Strings(keys)
	return keys
}
