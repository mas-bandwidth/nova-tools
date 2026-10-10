package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// A restore is proved at one of two levels, and a check says which it made
// (docs/SPEC-SPRINT.md, store-snapshot-verb). Integrity is the file: the RDB's
// header, version and CRC-64 (RDBTwin), or the twin document's shape; nothing
// of the sprint was read back. Semantic is the sprint: the dump was loaded into
// an isolated store and its sprint state (ReadState) is the source's, part for
// part. An integrity pass is never reported as a semantic one.
const (
	RestoreIntegrity = "integrity"
	RestoreSemantic  = "semantic"
)

// SprintState is a store's logical sprint state, one canonical value per part:
// the epoch, the coordinator and the machine; each table's complete definition,
// epoch, revision, properties and rows; every record each table has held (its place, score,
// revision and fields: the ids, heads, attempts, dependencies, decisions and
// costs the cards carry); and, at the sprint's epoch, the fence and the work
// table's queue, each stream's progress, the open judgments and who answered
// each judgment, the aliases, the notifications and the log, the log's cursor
// and every caller's recorded result. A pending fence includes the full replay
// operation, not just its identifier. Two stores hold the same sprint when
// their states have the same parts with the same values.
type SprintState struct {
	Parts map[string]string `json:"parts"`
}

// DoneLister lists every caller's recorded result at the backend's epoch: the
// results a retry of a caller's operation id returns (Backend.Done reads one).
// A backend that cannot list them cannot be proved restored.
type DoneLister interface {
	DoneAll(ctx context.Context) (map[string]string, error)
}

// statePage is how many lines of the log or of the inbox one read of the state
// takes.
const statePage = 1000

// ReadState reads the sprint state of the store b holds under names.
func ReadState(ctx context.Context, b Backend, names sprint.Names) (SprintState, error) {
	s := SprintState{Parts: map[string]string{}}
	put := func(part string, v any) error {
		if str, ok := v.(string); ok {
			s.Parts[part] = str
			return nil
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("%s: %w", part, err)
		}
		s.Parts[part] = string(raw)
		return nil
	}
	es, err := b.Epoch(ctx)
	if err != nil {
		return s, fmt.Errorf("the epoch: %w", err)
	}
	_ = put("epoch", es) // ignored: a struct of numbers, a time and a flag always marshals
	at := b.AtEpoch(es.N, false)
	coord, err := at.Coordinator(ctx)
	if err != nil {
		return s, fmt.Errorf("the coordinator: %w", err)
	}
	s.Parts["coordinator"] = coord
	mach, _, err := (&Store{B: b, Names: names}).Machine(ctx)
	if err != nil {
		return s, fmt.Errorf("the machine: %w", err)
	}
	if err := put("machine", mach); err != nil {
		return s, err
	}
	for _, name := range All {
		if err := readTableState(ctx, at, names, name, es.N, put); err != nil {
			return s, err
		}
	}
	fence, err := at.ReadFence(ctx)
	if err != nil {
		return s, fmt.Errorf("the fence: %w", err)
	}
	if err := put("fence", fence); err != nil {
		return s, err
	}
	queue, err := at.QueueRead(ctx)
	if err != nil {
		return s, fmt.Errorf("the queue: %w", err)
	}
	if err := put("queue", queue); err != nil {
		return s, err
	}
	progress, err := at.Progress(ctx)
	if err != nil {
		return s, fmt.Errorf("the progress: %w", err)
	}
	for stream, t := range progress {
		_ = put("progress "+stream, t) // ignored: a time always marshals
	}
	open, err := at.OpenNotes(ctx)
	if err != nil {
		return s, fmt.Errorf("the open judgments: %w", err)
	}
	for _, o := range open {
		if err := put("open "+o.Key, o.Note); err != nil {
			return s, err
		}
	}
	var judged, aliases []string
	for after := ""; ; {
		notes, ids, err := at.NotesSince(ctx, after, statePage)
		if err != nil {
			return s, fmt.Errorf("the notifications: %w", err)
		}
		for i, n := range notes {
			if err := put("note "+ids[i], n); err != nil {
				return s, err
			}
			if n.Kind == sprint.Judgment {
				judged = append(judged, n.ID)
			}
			if n.Alias != "" {
				aliases = append(aliases, n.Alias)
			}
		}
		if len(ids) < statePage {
			break
		}
		after = ids[len(ids)-1]
	}
	answered, err := at.Answered(ctx, judged)
	if err != nil {
		return s, fmt.Errorf("the answers: %w", err)
	}
	for id, who := range answered {
		s.Parts["answered "+id] = who
	}
	named, err := at.Aliases(ctx, aliases)
	if err != nil {
		return s, fmt.Errorf("the aliases: %w", err)
	}
	for a, id := range named {
		s.Parts["alias "+a] = id
	}
	for after := ""; ; {
		lines, ids, err := at.LogSince(ctx, after, statePage)
		if err != nil {
			return s, fmt.Errorf("the log: %w", err)
		}
		for i, l := range lines {
			if err := put("log "+ids[i], l); err != nil {
				return s, err
			}
		}
		if len(ids) < statePage {
			break
		}
		after = ids[len(ids)-1]
	}
	cursor, err := at.Cursor(ctx)
	if err != nil {
		return s, fmt.Errorf("the cursor: %w", err)
	}
	s.Parts["cursor"] = cursor
	dl, ok := at.(DoneLister)
	if !ok {
		return s, errors.New("this store cannot list the callers' recorded results, so its restore cannot be compared")
	}
	done, err := dl.DoneAll(ctx)
	if err != nil {
		return s, fmt.Errorf("the callers' recorded results: %w", err)
	}
	for op, res := range done {
		s.Parts["done "+op] = res
	}
	return s, nil
}

// readTableState puts one table's parts: its shape and its rows, and every
// record it has held, read in read sets at epoch, the sprint's, and a record
// of an older epoch as the store holds it. A table the store cannot read is
// one part, its refusal.
func readTableState(ctx context.Context, b Backend, names sprint.Names, logical string, epoch uint64, put func(string, any) error) error {
	stored := names.Table(logical)
	shapes, err := b.Shapes(ctx, []string{stored})
	if err != nil {
		// a store that holds no such table (one never initialised) is that
		// part's state: a restore of it must answer the same
		return put("table "+logical, "unreadable: "+err.Error())
	}
	if len(shapes) != 1 {
		return fmt.Errorf("table %s: the store answered %d shapes for one", logical, len(shapes))
	}
	shape := shapes[0]
	definition := shape
	definition.Rows = nil // rows have their own canonical parts below
	if err := put("table "+logical, definition); err != nil {
		return err
	}
	for i, r := range shape.Rows {
		if err := put(fmt.Sprintf("row %s %s", logical, r.Key), map[string]any{"at": i, "hidden": r.Hidden, "texts": r.Texts}); err != nil {
			return err
		}
	}
	ids, err := b.RecordIDs(ctx, stored)
	if err != nil {
		return fmt.Errorf("table %s: its records: %w", logical, err)
	}
	// The change log every epoch shares names the records of every epoch. A
	// record an older epoch left is the store's as much as any other, and the
	// table refuses to read it at the active epoch (MEMBEREPOCH): it is read
	// as the store holds it (RecordReader), never refused and never left out.
	// Its stored id says its epoch (sprint.StoredID); a record whose id does
	// not, and that the read set still refuses, is read the same way.
	var current, older []string
	for _, id := range ids {
		if e, ok := storedEpoch(id); ok && e != epoch {
			older = append(older, id)
		} else {
			current = append(current, id)
		}
	}
	for start := 0; start < len(current); start += ntable.LimitReadSetMembers {
		end := min(start+ntable.LimitReadSetMembers, len(current))
		members, refused, err := readSetOrEach(ctx, b, stored, current[start:end])
		if err != nil {
			return fmt.Errorf("table %s: %w", logical, err)
		}
		older = append(older, refused...)
		for _, m := range members {
			at := "-"
			if m.Placed {
				at = m.Row + ":" + m.Col
			}
			if err := put(fmt.Sprintf("record %s %s", logical, m.ID), map[string]any{"at": at, "score": m.Score, "revision": m.Revision}); err != nil {
				return err
			}
			for f, v := range m.Fields {
				if err := put(fmt.Sprintf("record %s %s field %s", logical, m.ID, f), v); err != nil {
					return err
				}
			}
		}
	}
	if len(older) == 0 {
		return nil
	}
	rr, ok := b.(RecordReader)
	if !ok {
		return fmt.Errorf("table %s: %d record(s) of an older epoch, and this store cannot read a record as it holds it", logical, len(older))
	}
	sort.Strings(older)
	for _, id := range older {
		raw, found, err := rr.RecordRaw(ctx, stored, names.RecordKey(logical, id), id)
		if err != nil {
			return fmt.Errorf("table %s: the record %s of an older epoch: %w", logical, id, err)
		}
		if !found {
			continue // as a read set leaves a missing member out
		}
		if err := put(fmt.Sprintf("record %s %s", logical, id), map[string]any{"older": raw}); err != nil {
			return err
		}
	}
	return nil
}

// RecordReader reads one record as the store holds it, whatever its epoch and
// without the table's epoch check: the record key's fields on a Redis, the
// record's every value on a twin. It is how a sprint's state reads a record an
// older epoch left (readTableState); ok is false when there is none.
type RecordReader interface {
	RecordRaw(ctx context.Context, table, key, id string) (map[string]string, bool, error)
}

// storedEpoch is the epoch a stored id carries after its last '~'
// (sprint.StoredID), and false for an id that carries none.
func storedEpoch(id string) (uint64, bool) {
	i := strings.LastIndexByte(id, '~')
	if i < 0 || i == len(id)-1 || strings.Trim(id[i+1:], "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.ParseUint(id[i+1:], 10, 64)
	return n, err == nil
}

// readSetOrEach reads ids in one read set; when the read set refuses a member
// of another epoch, it reads each id alone, and returns the refused ones for
// the caller to read as the store holds them.
func readSetOrEach(ctx context.Context, b Backend, table string, ids []string) ([]ntable.ReadSetMember, []string, error) {
	res, err := b.ReadSet(ctx, table, ids)
	if err == nil {
		return res.Members, nil, nil
	}
	if refusalCode(err) != "MEMBEREPOCH" {
		return nil, nil, err
	}
	var members []ntable.ReadSetMember
	var refused []string
	for _, id := range ids {
		one, err := b.ReadSet(ctx, table, []string{id})
		switch {
		case refusalCode(err) == "MEMBEREPOCH":
			refused = append(refused, id)
		case err != nil:
			return nil, nil, err
		default:
			members = append(members, one.Members...)
		}
	}
	return members, refused, nil
}

// Diff is every part where restored differs from s, the source, sorted: a part
// either holds that the other does not, or a part both hold with other values.
func (s SprintState) Diff(restored SprintState) []string {
	var out []string
	for part, want := range s.Parts {
		if got, ok := restored.Parts[part]; !ok || got != want {
			out = append(out, part)
		}
	}
	for part := range restored.Parts {
		if _, ok := s.Parts[part]; !ok {
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}

// diffText names the parts that differ (the first 24, and how many more).
// Values remain private: restore comparison runs before a backup's secret scan.
func diffText(parts []string) string {
	if len(parts) > 24 {
		return fmt.Sprintf("%s and %d more", strings.Join(parts[:24], ", "), len(parts)-24)
	}
	return strings.Join(parts, ", ")
}

// StateSource is a snapshot source that can say what the store's sprint state was
// at its save: read once the save has written, so a source whose store takes
// writes while it saves (a Redis between its BGSAVE and the read) can disagree
// with its own dump, and the check fails rather than passes.
type StateSource interface {
	SnapshotSource
	State(ctx context.Context) (SprintState, error)
}

// StateTwin loads a dump into an isolated store that no fleet writes to, and
// reads its sprint state.
type StateTwin interface {
	SnapshotTwin
	LoadState(ctx context.Context, dump []byte) (SprintState, error)
}

// RestoreLevel is the level a snapshot from src restored into twin is proved
// at: semantic when the source can say its state and the twin can load the
// dump's, integrity otherwise.
func RestoreLevel(src SnapshotSource, twin SnapshotTwin) string {
	_, ok := src.(StateSource)
	_, ok2 := twin.(StateTwin)
	if ok && ok2 {
		return RestoreSemantic
	}
	return RestoreIntegrity
}

// SemanticRestore loads dump into the twin and compares the sprint state it
// holds with want, the source's: nil only when they are the same in every part.
// The error names the parts that differ.
func SemanticRestore(ctx context.Context, want SprintState, twin StateTwin, dump []byte) error {
	got, err := twin.LoadState(ctx, dump)
	if err != nil {
		return fmt.Errorf("the dump does not load into an isolated store whose sprint can be read: %w", err)
	}
	if d := want.Diff(got); len(d) > 0 {
		return fmt.Errorf("the restored sprint is not the store's in %d part(s): %s", len(d), diffText(d))
	}
	return nil
}

// State is the in-memory store's sprint state now.
func (s MemSource) State(ctx context.Context) (SprintState, error) {
	return ReadState(ctx, s.M, s.Names)
}

// LoadState restores the document into a new Mem and reads its sprint state
// under the twin's names.
func (t MemTwin) LoadState(ctx context.Context, doc []byte) (SprintState, error) {
	m := NewMem()
	if err := m.Restore(doc); err != nil {
		return SprintState{}, err
	}
	return ReadState(ctx, m, t.Names)
}

// DoneAll is every caller's recorded result at the pinned epoch.
func (m *Mem) DoneAll(context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := maps.Clone(m.log().done)
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

// DoneAll is every caller's recorded result at the pinned epoch, in one read.
func (r *Redis) DoneAll(ctx context.Context) (map[string]string, error) {
	return r.C.HGetAll(ctx, r.key(keyDone)).Result()
}

// RecordRaw is the record key's every field, in one read, whatever its epoch.
func (r *Redis) RecordRaw(ctx context.Context, _, key, _ string) (map[string]string, bool, error) {
	h, err := r.C.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, false, err
	}
	return h, len(h) > 0, nil
}

// RecordRaw is the twin's record of id in table, whatever its epoch: its
// epoch, place, score, revision and fields.
func (m *Mem) RecordRaw(_ context.Context, table, _, id string) (map[string]string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return nil, false, err
	}
	mm := t.members[id]
	if mm == nil {
		return nil, false, nil
	}
	out := map[string]string{
		"epoch":    strconv.FormatUint(mm.epoch, 10),
		"revision": strconv.FormatUint(mm.rev, 10),
	}
	if mm.placed {
		out["place"] = mm.row + ":" + mm.col
		out["score"] = strconv.FormatFloat(mm.score, 'g', -1, 64)
	}
	for f, v := range mm.fields {
		out["field:"+f] = v
	}
	return out, true, nil
}

// RecordRaw reads through a read-only backend.
func (r readOnly) RecordRaw(ctx context.Context, table, key, id string) (map[string]string, bool, error) {
	rr, ok := r.b.(RecordReader)
	if !ok {
		return nil, false, errors.New("this store cannot read a record as it holds it")
	}
	return rr.RecordRaw(ctx, table, key, id)
}
