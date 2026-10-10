package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// A Mem is saved to one JSON document and loaded from it again: the file the
// command's `--redis mem:<file>` twin keeps between one verb and the next (for
// learning and tests, never a fleet's store). Snapshot and Restore are the
// whole of it: every field of the store's state, nothing of its test hooks
// (Fail, MaxWrite, Calls, LogWait, the per-epoch touch counts), so a restored
// store answers every call as the store it was taken from did.

// SnapshotVersion names the document's shape; a document of another version
// is refused, never guessed at.
const SnapshotVersion = 1

type memSnapshot struct {
	Version  int                       `json:"version"`
	Tables   map[string]*tableSnapshot `json:"tables"`
	Dropped  map[string]*residueSnap   `json:"dropped,omitempty"`
	Views    map[string]ntable.View    `json:"views,omitempty"`
	EpochSet bool                      `json:"epoch_set"`
	EpochN   uint64                    `json:"epoch"`
	Cleared  time.Time                 `json:"cleared"`
	Owed     bool                      `json:"owed"`
	Logs     map[uint64]*logSnapshot   `json:"logs,omitempty"`
	KV       map[string]string         `json:"kv,omitempty"`
	Seq      int                       `json:"seq"`
}

type tableSnapshot struct {
	Def     ntable.Table               `json:"def"`
	Epochs  map[uint64]*epochSnapshot  `json:"epochs,omitempty"`
	Wrote   map[uint64]bool            `json:"wrote,omitempty"`
	Rev     uint64                     `json:"rev"`
	Members map[string]*memberSnapshot `json:"members,omitempty"`
	Ops     map[string]opSnapshot      `json:"ops,omitempty"`
	// Changes is the table's change stream, which a read twin catches up
	// from (TableChanges): a restored store answers it as it did.
	Changes []changeSnapshot `json:"changes,omitempty"`
}

type epochSnapshot struct {
	Rows   []string                     `json:"rows,omitempty"`
	Hidden map[string]bool              `json:"hidden,omitempty"`
	Texts  map[string]map[string]string `json:"texts,omitempty"`
	Props  map[string]string            `json:"props,omitempty"`
}

type memberSnapshot struct {
	Epoch  uint64            `json:"epoch"`
	Placed bool              `json:"placed"`
	Row    string            `json:"row,omitempty"`
	Col    string            `json:"col,omitempty"`
	Score  float64           `json:"score"`
	Rev    uint64            `json:"rev"`
	Fields map[string]string `json:"fields,omitempty"`
}

type changeSnapshot struct {
	Epoch  uint64   `json:"epoch"`
	Before uint64   `json:"before"`
	After  uint64   `json:"after"`
	Verb   string   `json:"verb"`
	IDs    []string `json:"ids,omitempty"`
}

type opSnapshot struct {
	Body    string         `json:"body"`
	Receipt ntable.Receipt `json:"receipt"`
}

type residueSnap struct {
	Keys  map[string]bool `json:"keys,omitempty"`
	Table *tableSnapshot  `json:"table"`
}

type logSnapshot struct {
	Fence    *OpRecord              `json:"fence,omitempty"`
	Gen      uint64                 `json:"gen"`
	Done     map[string]string      `json:"done,omitempty"`
	Progress map[string]time.Time   `json:"progress,omitempty"`
	Inbox    []noteSnapshot         `json:"inbox,omitempty"`
	Lines    []lineSnapshot         `json:"lines,omitempty"`
	Notes    map[string]sprint.Note `json:"notes,omitempty"`
	Aliases  map[string]string      `json:"aliases,omitempty"`  // alias (j<n>) -> note id (sprint.Alias)
	Answered map[string]string      `json:"answered,omitempty"` // judgment id -> who answered it
	Open     map[string]string      `json:"open,omitempty"`
	Cursor   string                 `json:"cursor,omitempty"`
	// Queue is the work table's queue: the changes a verb queued that the
	// next tick's pump applies (sprint.QueueOf).
	Queue []sprint.QueuedChange `json:"queue,omitempty"`
}

type noteSnapshot struct {
	ID   string      `json:"id"`
	Note sprint.Note `json:"note"`
}

type lineSnapshot struct {
	ID   string      `json:"id"`
	Line sprint.Line `json:"line"`
}

func snapTable(t *memTable) *tableSnapshot {
	s := &tableSnapshot{Def: t.def, Rev: t.rev, Epochs: map[uint64]*epochSnapshot{}, Wrote: map[uint64]bool{}, Members: map[string]*memberSnapshot{}, Ops: map[string]opSnapshot{}}
	for e, ep := range t.epochs {
		s.Epochs[e] = &epochSnapshot{Rows: ep.rows, Hidden: ep.hidden, Texts: ep.texts, Props: ep.props}
	}
	maps.Copy(s.Wrote, t.wrote)
	for id, m := range t.members {
		s.Members[id] = &memberSnapshot{Epoch: m.epoch, Placed: m.placed, Row: m.row, Col: m.col, Score: m.score, Rev: m.rev, Fields: m.fields}
	}
	for id, o := range t.ops {
		s.Ops[id] = opSnapshot{Body: o.body, Receipt: o.receipt}
	}
	for _, c := range t.changes {
		s.Changes = append(s.Changes, changeSnapshot{Epoch: c.epoch, Before: c.before, After: c.after, Verb: c.verb, IDs: c.ids})
	}
	return s
}

func (s *tableSnapshot) table() *memTable {
	t := &memTable{def: s.Def, rev: s.Rev, epochs: map[uint64]*memEpoch{}, wrote: map[uint64]bool{}, members: map[string]*memMember{}, ops: map[string]memOp{}}
	for e, ep := range s.Epochs {
		me := &memEpoch{rows: ep.Rows, hidden: ep.Hidden, texts: ep.Texts, props: ep.Props}
		if me.texts == nil {
			me.texts = map[string]map[string]string{}
		}
		t.epochs[e] = me
	}
	maps.Copy(t.wrote, s.Wrote)
	for id, m := range s.Members {
		t.members[id] = &memMember{epoch: m.Epoch, placed: m.Placed, row: m.Row, col: m.Col, score: m.Score, rev: m.Rev, fields: m.Fields}
	}
	for id, o := range s.Ops {
		t.ops[id] = memOp{body: o.Body, receipt: o.Receipt}
	}
	for _, c := range s.Changes {
		t.changes = append(t.changes, memChange{epoch: c.Epoch, before: c.Before, after: c.After, verb: c.Verb, ids: c.IDs})
	}
	return t
}

// Snapshot is the store's whole state as one JSON document. The same state
// always gives the same bytes (maps are written in key order), so a caller
// can tell by comparing two snapshots whether a verb changed anything.
func (m *Mem) Snapshot() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := memSnapshot{Version: SnapshotVersion, Tables: map[string]*tableSnapshot{}, Dropped: map[string]*residueSnap{}, Views: m.views,
		EpochSet: m.epochSet, EpochN: m.epochN, Cleared: m.cleared, Owed: m.owed, Logs: map[uint64]*logSnapshot{}, KV: m.kv, Seq: m.seq}
	for n, t := range m.tables {
		s.Tables[n] = snapTable(t)
	}
	for n, r := range m.dropped {
		s.Dropped[n] = &residueSnap{Keys: r.keys, Table: snapTable(r.table)}
	}
	for e, l := range m.logs {
		ls := &logSnapshot{Fence: l.fence, Gen: l.gen, Done: l.done, Progress: l.progress, Notes: l.notes, Aliases: l.aliases, Answered: l.answered, Open: l.open, Cursor: l.cursor, Queue: l.queue}
		for _, n := range l.inbox {
			ls.Inbox = append(ls.Inbox, noteSnapshot{ID: n.id, Note: n.note})
		}
		for _, ln := range l.lines {
			ls.Lines = append(ls.Lines, lineSnapshot{ID: ln.id, Line: ln.line})
		}
		s.Logs[e] = ls
	}
	return json.MarshalIndent(s, "", " ")
}

// Restore replaces the store's state with a snapshot's. A document that is
// not a snapshot of this version is refused and the store is left as it was.
func (m *Mem) Restore(doc []byte) error {
	var s memSnapshot
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return fmt.Errorf("not a twin snapshot: %w", err)
	}
	if s.Version != SnapshotVersion {
		return fmt.Errorf("a twin snapshot of version %d; this build reads version %d", s.Version, SnapshotVersion)
	}
	tables, dropped, logs := map[string]*memTable{}, map[string]*memResidue{}, map[uint64]*memLog{}
	for n, t := range s.Tables {
		tables[n] = t.table()
	}
	for n, r := range s.Dropped {
		keys := r.Keys
		if keys == nil {
			keys = map[string]bool{}
		}
		dropped[n] = &memResidue{keys: keys, table: r.Table.table()}
	}
	for e, l := range s.Logs {
		ml := &memLog{fence: l.Fence, gen: l.Gen, done: l.Done, progress: l.Progress, notes: l.Notes, aliases: l.Aliases, answered: l.Answered, open: l.Open, cursor: l.Cursor, queue: l.Queue}
		if ml.done == nil {
			ml.done = map[string]string{}
		}
		if ml.progress == nil {
			ml.progress = map[string]time.Time{}
		}
		if ml.notes == nil {
			ml.notes = map[string]sprint.Note{}
		}
		if ml.open == nil {
			ml.open = map[string]string{}
		}
		for _, n := range l.Inbox {
			ml.inbox = append(ml.inbox, memNote{n.ID, n.Note})
		}
		for _, ln := range l.Lines {
			ml.lines = append(ml.lines, memLine{ln.ID, ln.Line})
		}
		logs[e] = ml
	}
	views, kv := s.Views, s.KV
	if views == nil {
		views = map[string]ntable.View{}
	}
	if kv == nil {
		kv = map[string]string{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tables, m.dropped, m.views, m.logs, m.kv = tables, dropped, views, logs, kv
	m.epochSet, m.epochN, m.cleared, m.owed, m.seq = s.EpochSet, s.EpochN, s.Cleared, s.Owed, s.Seq
	return nil
}
