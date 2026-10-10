package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var snapT = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

// stubTable returns a memTable with data in every field, for snapshot tests.
func stubTable() *memTable {
	return &memTable{
		def: ntable.Table{
			Name:     "t-test",
			EpochKey: "e",
			Columns:  []ntable.Column{{Name: "state", Projection: ntable.Count}},
		},
		epochs: map[uint64]*memEpoch{
			0: {
				rows:   []string{"r1", "r2"},
				hidden: map[string]bool{"r2": true},
				texts:  map[string]map[string]string{"r1": {"state": "active"}},
				props:  map[string]string{"stream": "s1"},
			},
		},
		wrote: map[uint64]bool{0: true},
		rev:   2,
		members: map[string]*memMember{
			"s1-1": {epoch: 0, placed: true, row: "r1", col: "state", score: 0.5, rev: 1, fields: map[string]string{"brief": "t"}},
		},
		ops: map[string]memOp{
			"0:op1": {body: "{}", receipt: ntable.Receipt{ID: "1-0", Outcome: "changed"}},
		},
		changes: []memChange{{epoch: 0, before: 1, after: 2, verb: "apply", ids: []string{"s1-1"}}},
	}
}

// stubLog returns a memLog with data in every field.
func stubLog() *memLog {
	return &memLog{
		fence:    &OpRecord{ID: "op1", Verb: "deal", At: snapT},
		gen:      1,
		done:     map[string]string{"caller-1": "ok"},
		progress: map[string]time.Time{"s1": snapT},
		inbox:    []memNote{{id: "1-0", note: sprint.Note{ID: "n1", Kind: sprint.Judgment, Type: sprint.NReadyToAccept, At: snapT}}},
		lines:    []memLine{{id: "1-0", line: sprint.Line{Kind: sprint.LineMove, At: snapT, Epoch: 0, Card: "s1-1", Table: "t-work", To: "s1:ready"}}},
		notes: map[string]sprint.Note{
			"n1": {ID: "n1", Kind: sprint.Judgment, Type: sprint.NReadyToAccept, At: snapT},
		},
		open:   map[string]string{"s1/s1-1": "n1"},
		cursor: "1-0",
		queue:  []sprint.QueuedChange{{Verb: "apply", Actor: "tester"}},
	}
}

// TestMemSnapshotCoverSnapTable pins snapTable: it converts a memTable's
// internal state into a tableSnapshot for JSON serialization. The edge case
// is an empty memTable, which must produce a snapshot with empty maps.
func TestMemSnapshotCoverSnapTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   *memTable
	}{
		{"full", stubTable()},
		{"empty", &memTable{
			def:     ntable.Table{Name: "t-empty", EpochKey: "e"},
			epochs:  map[uint64]*memEpoch{},
			wrote:   map[uint64]bool{},
			members: map[string]*memMember{},
			ops:     map[string]memOp{},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := snapTable(c.in)
			assert.Equal(t, c.in.def, s.Def, "Def matches")
			assert.Equal(t, c.in.rev, s.Rev, "Rev matches")
			assert.Equal(t, c.in.wrote, s.Wrote, "Wrote matches")
			assert.Len(t, s.Epochs, len(c.in.epochs), "Epochs count")
			if ep, ok := s.Epochs[0]; ok {
				require.NotNil(t, ep)
				src := c.in.epochs[0]
				assert.Equal(t, src.rows, ep.Rows)
				assert.Equal(t, src.hidden, ep.Hidden)
				assert.Equal(t, src.texts, ep.Texts)
				assert.Equal(t, src.props, ep.Props)
			}
			assert.Len(t, s.Members, len(c.in.members), "Members count")
			if mm, ok := s.Members["s1-1"]; ok {
				require.NotNil(t, mm)
				src := c.in.members["s1-1"]
				assert.Equal(t, src.epoch, mm.Epoch)
				assert.Equal(t, src.placed, mm.Placed)
				assert.Equal(t, src.row, mm.Row)
				assert.Equal(t, src.col, mm.Col)
				assert.Equal(t, src.score, mm.Score)
				assert.Equal(t, src.rev, mm.Rev)
				assert.Equal(t, src.fields, mm.Fields)
			}
			assert.Len(t, s.Ops, len(c.in.ops), "Ops count")
			if op, ok := s.Ops["0:op1"]; ok {
				assert.Equal(t, c.in.ops["0:op1"].body, op.Body)
				assert.Equal(t, c.in.ops["0:op1"].receipt, op.Receipt)
			}
			assert.Len(t, s.Changes, len(c.in.changes), "Changes count")
			if len(s.Changes) > 0 {
				src := c.in.changes[0]
				assert.Equal(t, src.epoch, s.Changes[0].Epoch)
				assert.Equal(t, src.before, s.Changes[0].Before)
				assert.Equal(t, src.after, s.Changes[0].After)
				assert.Equal(t, src.verb, s.Changes[0].Verb)
				assert.Equal(t, src.ids, s.Changes[0].IDs)
			}
		})
	}
}

// TestMemSnapshotCoverTable pins table(): it restores a tableSnapshot into a
// memTable. The edge case is an empty tableSnapshot, which must produce a
// memTable with initialized maps.
func TestMemSnapshotCoverTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   *tableSnapshot
	}{
		{"full", &tableSnapshot{
			Def: ntable.Table{Name: "t-test", EpochKey: "e", Columns: []ntable.Column{{Name: "state", Projection: ntable.Count}}},
			Epochs: map[uint64]*epochSnapshot{
				0: {Rows: []string{"r1", "r2"}, Hidden: map[string]bool{"r2": true},
					Texts: map[string]map[string]string{"r1": {"state": "active"}},
					Props: map[string]string{"stream": "s1"}},
			},
			Wrote:   map[uint64]bool{0: true},
			Rev:     2,
			Members: map[string]*memberSnapshot{"s1-1": {Epoch: 0, Placed: true, Row: "r1", Col: "state", Score: 0.5, Rev: 1, Fields: map[string]string{"brief": "t"}}},
			Ops:     map[string]opSnapshot{"0:op1": {Body: "{}", Receipt: ntable.Receipt{ID: "1-0", Outcome: "changed"}}},
			Changes: []changeSnapshot{{Epoch: 0, Before: 1, After: 2, Verb: "apply", IDs: []string{"s1-1"}}},
		}},
		{"empty", &tableSnapshot{
			Def:     ntable.Table{Name: "t-empty", EpochKey: "e"},
			Epochs:  map[uint64]*epochSnapshot{},
			Wrote:   map[uint64]bool{},
			Members: map[string]*memberSnapshot{},
			Ops:     map[string]opSnapshot{},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			mt := c.in.table()
			assert.Equal(t, c.in.Def, mt.def)
			assert.Equal(t, c.in.Rev, mt.rev)
			assert.Len(t, mt.epochs, len(c.in.Epochs))
			assert.Len(t, mt.wrote, len(c.in.Wrote))
			assert.Len(t, mt.members, len(c.in.Members))
			assert.Len(t, mt.ops, len(c.in.Ops))
			assert.Len(t, mt.changes, len(c.in.Changes))
			if ep, ok := mt.epochs[0]; ok {
				require.NotNil(t, ep)
				require.NotNil(t, ep.texts, "table() initializes texts to a non-nil map")
				src := c.in.Epochs[0]
				assert.Equal(t, src.Rows, ep.rows)
				assert.Equal(t, src.Hidden, ep.hidden)
				assert.Equal(t, src.Texts, ep.texts)
				assert.Equal(t, src.Props, ep.props)
			}
			if mm, ok := mt.members["s1-1"]; ok {
				require.NotNil(t, mm)
				src := c.in.Members["s1-1"]
				assert.Equal(t, src.Epoch, mm.epoch)
				assert.Equal(t, src.Placed, mm.placed)
				assert.Equal(t, src.Row, mm.row)
				assert.Equal(t, src.Col, mm.col)
				assert.Equal(t, src.Score, mm.score)
				assert.Equal(t, src.Rev, mm.rev)
				assert.Equal(t, src.Fields, mm.fields)
			}
		})
	}
}

// TestMemSnapshotCoverSnapshot pins Snapshot: it serializes a Mem store's
// whole state as one JSON document, version-stamped and with every table,
// dropped residue, view, epoch state, log, kv and seq.
func TestMemSnapshotCoverSnapshot(t *testing.T) {
	t.Parallel()
	m := NewMem()
	m.tables = map[string]*memTable{"t-test": stubTable()}
	m.epochSet = true
	m.epochN = 1
	m.cleared = snapT
	m.owed = true
	m.logs = map[uint64]*memLog{0: stubLog()}
	m.kv = map[string]string{"coordinator": "tester"}
	m.seq = 42

	doc, err := m.Snapshot()
	require.NoError(t, err)

	var s memSnapshot
	require.NoError(t, json.Unmarshal(doc, &s))
	assert.Equal(t, SnapshotVersion, s.Version)
	assert.Equal(t, uint64(1), s.EpochN)
	assert.True(t, s.EpochSet)
	assert.Equal(t, snapT, s.Cleared)
	assert.True(t, s.Owed)
	assert.Equal(t, 42, s.Seq)
	assert.Contains(t, s.KV, "coordinator")
	ts, ok := s.Tables["t-test"]
	require.True(t, ok, "table t-test is in the snapshot")
	assert.Equal(t, uint64(2), ts.Rev)
	mm, ok := ts.Members["s1-1"]
	require.True(t, ok)
	assert.Equal(t, "r1", mm.Row)
	require.Contains(t, s.Logs, uint64(0))
	l := s.Logs[0]
	assert.Equal(t, uint64(1), l.Gen)
	require.NotNil(t, l.Fence)
	assert.Equal(t, "op1", l.Fence.ID)
	assert.Equal(t, "1-0", l.Cursor)
}

// TestMemSnapshotCoverRestore pins Restore: it applies a snapshot document
// to a Mem store, replacing its state. The main path restores a full snapshot;
// the refusals reject documents that are not JSON, have the wrong version, or
// carry unknown fields.
func TestMemSnapshotCoverRestore(t *testing.T) {
	t.Parallel()
	src := NewMem()
	src.tables = map[string]*memTable{"t-test": stubTable()}
	src.epochSet = true
	src.epochN = 1
	src.cleared = snapT
	src.logs = map[uint64]*memLog{0: stubLog()}
	src.kv = map[string]string{"coordinator": "tester"}
	src.seq = 7
	validDoc, err := src.Snapshot()
	require.NoError(t, err)

	cases := []struct {
		name    string
		doc     []byte
		wantErr string
	}{
		{"valid snapshot restores state", validDoc, ""},
		{"invalid JSON is refused", []byte("{not valid json"), "not a twin snapshot"},
		{"wrong version is refused", []byte(`{"version": 99}`), "a twin snapshot of version 99"},
		{"unknown field is refused", []byte(`{"version": 1, "bogus": true}`), "not a twin snapshot"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := NewMem()
			err := m.Restore(c.doc)
			if c.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, uint64(1), m.epochN, "epochN restored")
				assert.True(t, m.epochSet, "epochSet restored")
				assert.Equal(t, snapT, m.cleared, "cleared restored")
				assert.Equal(t, 7, m.seq, "seq restored")
				_, ok := m.tables["t-test"]
				assert.True(t, ok, "table t-test present after restore")
				assert.Contains(t, m.kv, "coordinator", "kv restored")
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), c.wantErr)
			}
		})
	}
}

// TestMemSnapshotCoverRoundTrip pins that Snapshot and Restore are inverses:
// restoring a snapshot and re-snapshotting yields identical bytes, because
// the same state always gives the same JSON.
func TestMemSnapshotCoverRoundTrip(t *testing.T) {
	t.Parallel()
	first := NewMem()
	first.tables = map[string]*memTable{"t-test": stubTable()}
	first.dropped = map[string]*memResidue{
		"t-dropped": {keys: map[string]bool{}, table: stubTable()},
	}
	first.epochSet = true
	first.epochN = 1
	first.cleared = snapT
	first.owed = false
	first.logs = map[uint64]*memLog{0: stubLog()}
	first.kv = map[string]string{"coordinator": "tester", "stuck": "s1-1"}
	first.seq = 42

	doc, err := first.Snapshot()
	require.NoError(t, err)

	second := NewMem()
	require.NoError(t, second.Restore(doc))

	again, err := second.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, doc, again, "Snapshot → Restore → Snapshot is deterministic")
}
