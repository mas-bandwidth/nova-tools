//go:build functional

package ntable_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"pgregory.net/rapid"
)

// The current-contract batch extension reuses the independent state oracle and
// its complete-store comparisons. The original single-member harness remains
// unchanged so its file can still be transplanted to the pinned old baseline.
func TestTableMemberBatchesAgainstModel(t *testing.T) {
	t.Parallel()
	rapidKeepsTheTreeClean(t)
	_, c := live(t)
	ctx := context.Background()
	tally := contractTally{gaps: map[string]bool{}}
	batchCounts := [3][2]int{}
	defer func() {
		t.Logf("mixed single/batch actions=%d (ok=%d refused=%d), batches add=%v remove=%v move=%v [accepted refused]", tally.steps, tally.ok, tally.refused, batchCounts[0], batchCounts[1], batchCounts[2])
		for op, counts := range batchCounts {
			if counts[0] == 0 || counts[1] == 0 {
				t.Errorf("batch generator missed acceptance/refusal for %s: %v; this run is insufficient coverage", []string{"add", "remove", "move"}[op], counts)
			}
		}
		if len(tally.gapOrder) > 0 {
			t.Errorf("contract gaps: %v", tally.gapOrder)
		}
	}()
	present := storeHasFunction(t, ctx, c, checkFn)
	rapid.Check(t, func(rt *rapid.T) {
		if err := c.FlushAll(ctx).Err(); err != nil {
			rt.Fatal(err)
		}
		h := &harness{t: rt, ctx: ctx, c: c, m: newModel(true), tally: &tally, checkPresent: present}
		actions := map[string]func(*rapid.T){
			"": h.verify, "create": h.create, "rowAdd": h.rowAdd, "rowDel": h.rowDel,
			"bindKeepSubset": h.bindKeepSubset, "clear": h.clear, "drop": h.drop,
			"cellAdd": h.cellAdd, "cellRemove": h.cellRemove, "cellMove": h.cellMove,
			"batchAdd":    func(*rapid.T) { h.memberBatch(0, &batchCounts) },
			"batchRemove": func(*rapid.T) { h.memberBatch(1, &batchCounts) },
			"batchMove":   func(*rapid.T) { h.memberBatch(2, &batchCounts) },
		}
		for round := 0; round < repeatRounds; round++ {
			rt.Repeat(actions)
		}
	})
}

// A batch draws two or three IDs, including duplicates. Validate every member
// against the pre-call model before updating anything. This is an oracle over
// member identities/sets, not a replay of Redis staging commands.
func (h *harness) memberBatch(op int, counts *[3][2]int) {
	tn := h.table()
	row, from := h.row(tn), h.cellColumn()
	to := from
	if op == 2 {
		// Draw some moves from real multi-member cells. Uniform independent
		// row/column/member draws almost exclusively refuse and can miss all
		// successful batch moves even across a hundred complete sequences.
		type cell struct{ table, row, col string }
		var eligible []cell
		for _, table := range propTables {
			for _, r := range h.m.rowsInOrder(table) {
				for _, col := range propColumns {
					if h.m.cellWrite(table, r, col).ok() && len(h.m.membersAt(ntable.CellKey(table, r, col))) >= 2 {
						eligible = append(eligible, cell{table, r, col})
					}
				}
			}
		}
		if len(eligible) > 0 && rapid.IntRange(0, 3).Draw(h.t, "move from group") != 0 {
			cell := rapid.SampledFrom(eligible).Draw(h.t, "group cell")
			tn, row, from = cell.table, cell.row, cell.col
		}
		to = h.cellColumn()
	}
	fromKey, toKey := ntable.CellKey(tn, row, from), ntable.CellKey(tn, row, to)
	live := h.m.membersAt(fromKey)
	if op == 0 {
		// Include both fresh IDs and already-placed IDs. The latter and an
		// occasional duplicate deliberately exercise late refusal atomicity.
		live = nil
		for _, m := range propMembers {
			if _, placed := h.m.placeOf(m, tn); !placed {
				live = append(live, m)
			}
		}
	}
	nMembers := rapid.IntRange(2, 3).Draw(h.t, "batch size")
	members := make([]string, nMembers)
	if len(live) >= 2 && rapid.IntRange(0, 3).Draw(h.t, "distinct live batch") != 0 {
		pool := append([]string(nil), live...)
		if nMembers > len(pool) {
			nMembers = len(pool)
			members = members[:nMembers]
		}
		for i := range members {
			j := rapid.IntRange(0, len(pool)-1).Draw(h.t, "batch member index")
			members[i] = pool[j]
			pool = append(pool[:j], pool[j+1:]...)
		}
	} else {
		for i := range members {
			members[i] = h.member(live)
		}
	}
	score := h.score()
	action := fmt.Sprintf("batch%s %s %s %s->%s %v score=%g", []string{"Add", "Remove", "Move"}[op], tn, row, from, to, members, score)
	want := h.m.cellWrite(tn, row, from)
	if want.ok() && op == 2 {
		want = h.m.cellWrite(tn, row, to)
	}
	changes := false
	if want.ok() {
		seen := map[string]bool{}
		for _, member := range members {
			if seen[member] {
				want = refused("twice")
				break
			}
			seen[member] = true
			p, placed := h.m.placeOf(member, tn)
			switch op {
			case 0:
				if placed {
					want = refused("placed")
				} else {
					changes = true
				}
			case 1:
				if placed && p == (place{row, from}) {
					changes = true
				}
			case 2:
				if !placed || p != (place{row, from}) {
					want = refused("notmember")
				} else if from != to {
					changes = true
				}
			}
			if !want.ok() {
				break
			}
		}
	}
	if want.ok() && !changes {
		want = verdict{kind: "unchanged"}
	}
	before := h.before(want)
	var n int64
	var err error
	switch op {
	case 0:
		n, err = ntable.CellsAdd(h.ctx, h.c, tn, row, from, score, members)
	case 1:
		n, err = ntable.CellsRemove(h.ctx, h.c, tn, row, from, members)
	case 2:
		n, err = ntable.CellsMove(h.ctx, h.c, tn, row, from, to, members)
	}
	// Duplicate list entries are a new refusal kind; the old baseline's shared
	// classifier deliberately need not know the list API.
	if want.kind == "twice" {
		if err == nil || !batchDuplicate(err) {
			h.fail("%s: wanted duplicate refusal, got %v", action, err)
		}
		if diff := diffSnapshots(before, h.snapshot()); diff != "" {
			h.fail("%s: refused batch changed %s", action, diff)
		}
		h.tally.steps++
		h.tally.refused++
	} else {
		h.expect(action, want, err, before)
	}
	if !want.ok() && want.kind != "unchanged" {
		counts[op][1]++
		h.m.note("%s -> %s", action, outcome(want))
		return
	}
	counts[op][0]++
	for _, member := range members {
		switch op {
		case 0:
			if h.m.phys[fromKey] == nil {
				h.m.phys[fromKey] = zset{}
			}
			h.m.phys[fromKey][member] = score
			h.m.setPlace(member, tn, place{row, from})
		case 1:
			if p, placed := h.m.placeOf(member, tn); placed && p == (place{row, from}) {
				delete(h.m.phys[fromKey], member)
				delete(h.m.place[member], tn)
			}
		case 2:
			// Capture each old score before the move, including a self-move.
			oldScore := h.m.phys[fromKey][member]
			delete(h.m.phys[fromKey], member)
			if h.m.phys[toKey] == nil {
				h.m.phys[toKey] = zset{}
			}
			h.m.phys[toKey][member] = oldScore
			h.m.setPlace(member, tn, place{row, to})
		}
	}
	if len(h.m.phys[fromKey]) == 0 {
		delete(h.m.phys, fromKey)
	}
	if n != int64(len(h.m.phys[toKey])) {
		h.fail("%s: count=%d, model=%s", action, n, h.m.phys[toKey])
	}
	// Exactly one revision and one receipt for the accepted list, even when
	// some or all removals are absent, or a move keeps the same destination.
	h.m.mutated(tn)
	h.m.note("%s -> %s", action, outcome(want))
}

func batchDuplicate(err error) bool { return strings.Contains(err.Error(), "TWICE") }

type batchMemberState struct {
	id       string
	exists   bool
	placed   bool
	row      string
	col      string
	score    float64
	revision uint64
	fields   map[string]string
}

type batchTableState struct {
	name     string
	present  bool
	revision uint64
	rows     []string
	columns  []string
	cells    map[string]map[string]float64
}

type batchOracle struct {
	table   *batchTableState
	members map[string]*batchMemberState
}

func newBatchOracle(tableName string, rows, cols []string, rev uint64) *batchOracle {
	cells := make(map[string]map[string]float64)
	for _, r := range rows {
		for _, c := range cols {
			cells[r+":"+c] = make(map[string]float64)
		}
	}
	return &batchOracle{
		table: &batchTableState{
			name:     tableName,
			present:  true,
			revision: rev,
			rows:     append([]string(nil), rows...),
			columns:  append([]string(nil), cols...),
			cells:    cells,
		},
		members: make(map[string]*batchMemberState),
	}
}

func (o *batchOracle) memberHasChange(entry ntable.BatchMemberEntry, mem *batchMemberState) bool {
	if entry.Create != nil {
		return true
	}
	if entry.Remove {
		return true
	}
	fieldsChanged := false
	for f, val := range entry.Set {
		if curVal, ok := mem.fields[f]; !ok || curVal != val {
			fieldsChanged = true
			break
		}
	}
	if !fieldsChanged {
		for _, f := range entry.Unset {
			if _, ok := mem.fields[f]; ok {
				fieldsChanged = true
				break
			}
		}
	}
	if entry.Move != nil {
		placeChanged := (entry.Move.Row != mem.row || entry.Move.Col != mem.col)
		scoreChanged := false
		if entry.Move.Score != nil && *entry.Move.Score != mem.score {
			scoreChanged = true
		}
		return placeChanged || scoreChanged || fieldsChanged
	}
	return fieldsChanged
}

func (o *batchOracle) predict(manifest ntable.BatchManifest) (verdict, bool) {
	if manifest.Table != o.table.name || !o.table.present {
		return refused("notable"), false
	}
	if manifest.ExpectedTableRevision != strconv.FormatUint(o.table.revision, 10) {
		return refused("revision"), false
	}
	seen := make(map[string]bool)
	for _, entry := range manifest.Members {
		if seen[entry.ID] {
			return refused("twice"), false
		}
		seen[entry.ID] = true
	}

	for _, entry := range manifest.Members {
		mem := o.members[entry.ID]
		if mem == nil {
			mem = &batchMemberState{id: entry.ID, fields: make(map[string]string)}
		}
		exp := entry.Expect
		if exp == nil {
			return refused("manifest"), false
		}
		if exp.Absent {
			if mem.exists || mem.placed {
				return refused("memberexists"), false
			}
		} else {
			if !mem.exists {
				return refused("notmember"), false
			}
			if exp.Revision != "" && exp.Revision != strconv.FormatUint(mem.revision, 10) {
				return refused("memberrevision"), false
			}
			if exp.Place != nil {
				if !mem.placed || mem.row != exp.Place.Row || mem.col != exp.Place.Col {
					return refused("drift"), false
				}
			}
			if exp.Fields != nil {
				for f, g := range exp.Fields {
					if g.Equals != nil {
						if actual, ok := mem.fields[f]; !ok || actual != *g.Equals {
							return refused("fieldguard"), false
						}
					} else if g.Absent != nil && *g.Absent {
						if _, ok := mem.fields[f]; ok {
							return refused("fieldguard"), false
						}
					} else if len(g.OneOf) > 0 {
						actual, ok := mem.fields[f]
						if !ok || !slices.Contains(g.OneOf, actual) {
							return refused("fieldguard"), false
						}
					}
				}
			}
		}

		if len(entry.Set) > 0 && len(entry.Unset) > 0 {
			for f := range entry.Set {
				if slices.Contains(entry.Unset, f) {
					return refused("mutation"), false
				}
			}
		}

		if entry.Create != nil {
			if entry.Move != nil || entry.Remove {
				return refused("mutation"), false
			}
			if exp.Revision != "" || exp.Place != nil || exp.Fields != nil || !exp.Absent {
				return refused("mutation"), false
			}
			if mem.exists || mem.placed {
				return refused("memberexists"), false
			}
			if !slices.Contains(o.table.rows, entry.Create.Row) {
				return refused("norow"), false
			}
			if !slices.Contains(o.table.columns, entry.Create.Col) {
				return refused("nocol"), false
			}
		} else if entry.Move != nil {
			if entry.Remove {
				return refused("mutation"), false
			}
			if !mem.placed {
				return refused("notmember"), false
			}
			if !slices.Contains(o.table.rows, entry.Move.Row) {
				return refused("norow"), false
			}
			if !slices.Contains(o.table.columns, entry.Move.Col) {
				return refused("nocol"), false
			}
		} else if entry.Remove {
			if !mem.placed {
				return refused("notmember"), false
			}
		} else {
			hasFields := len(entry.Set) > 0 || len(entry.Unset) > 0
			if hasFields && !mem.exists {
				return refused("notmember"), false
			}
		}
	}

	hasRealChanges := false
	for _, entry := range manifest.Members {
		mem := o.members[entry.ID]
		if mem == nil {
			mem = &batchMemberState{id: entry.ID, fields: make(map[string]string)}
		}
		if o.memberHasChange(entry, mem) {
			hasRealChanges = true
			break
		}
	}

	return succeeds, hasRealChanges
}

func (o *batchOracle) apply(manifest ntable.BatchManifest) {
	o.table.revision++
	for _, entry := range manifest.Members {
		mem := o.members[entry.ID]
		if mem == nil {
			mem = &batchMemberState{id: entry.ID, fields: make(map[string]string)}
			o.members[entry.ID] = mem
		}
		changed := o.memberHasChange(entry, mem)

		if entry.Create != nil {
			mem.exists = true
			mem.placed = true
			mem.row = entry.Create.Row
			mem.col = entry.Create.Col
			mem.score = entry.Create.Score
			mem.revision = 1
			cellKey := mem.row + ":" + mem.col
			if o.table.cells[cellKey] == nil {
				o.table.cells[cellKey] = make(map[string]float64)
			}
			o.table.cells[cellKey][mem.id] = mem.score
		} else if entry.Move != nil {
			oldCellKey := mem.row + ":" + mem.col
			delete(o.table.cells[oldCellKey], mem.id)
			mem.row = entry.Move.Row
			mem.col = entry.Move.Col
			if entry.Move.Score != nil {
				mem.score = *entry.Move.Score
			}
			newCellKey := mem.row + ":" + mem.col
			if o.table.cells[newCellKey] == nil {
				o.table.cells[newCellKey] = make(map[string]float64)
			}
			o.table.cells[newCellKey][mem.id] = mem.score
			if changed {
				mem.revision++
			}
		} else if entry.Remove {
			oldCellKey := mem.row + ":" + mem.col
			delete(o.table.cells[oldCellKey], mem.id)
			mem.placed = false
			mem.row = ""
			mem.col = ""
			mem.score = 0
			mem.revision++
		} else {
			if changed {
				mem.revision++
			}
		}

		if len(entry.Set) > 0 {
			for k, v := range entry.Set {
				mem.fields[k] = v
			}
		}
		if len(entry.Unset) > 0 {
			for _, k := range entry.Unset {
				delete(mem.fields, k)
			}
		}
	}
}

func (o *batchOracle) predictDelta(manifest ntable.BatchManifest) ntable.BatchDelta {
	guardCount := 0
	changedCount := 0
	memberDeltas := make([]ntable.BatchMemberDelta, len(manifest.Members))

	for i, entry := range manifest.Members {
		mem := o.members[entry.ID]
		if mem == nil {
			mem = &batchMemberState{id: entry.ID, fields: make(map[string]string)}
		}

		hasMutation := (entry.Create != nil || entry.Move != nil || entry.Remove || len(entry.Set) > 0 || len(entry.Unset) > 0)
		if !hasMutation {
			guardCount++
		}

		isChange := o.memberHasChange(entry, mem)
		if isChange {
			changedCount++
		}

		md := ntable.BatchMemberDelta{
			ID:          entry.ID,
			FieldsSet:   entry.Set,
			FieldsUnset: entry.Unset,
			Fields:      make(map[string]ntable.FieldChange),
		}
		if md.FieldsSet == nil {
			md.FieldsSet = map[string]string{}
		}
		if md.FieldsUnset == nil {
			md.FieldsUnset = []string{}
		}

		for f, v := range entry.Set {
			var before *string
			if curV, ok := mem.fields[f]; ok {
				curVCopy := curV
				before = &curVCopy
			}
			vCopy := v
			md.Fields[f] = ntable.FieldChange{Before: before, After: &vCopy}
		}
		for _, f := range entry.Unset {
			var before *string
			if curV, ok := mem.fields[f]; ok {
				curVCopy := curV
				before = &curVCopy
			}
			md.Fields[f] = ntable.FieldChange{Before: before, After: nil}
		}

		if entry.Create != nil {
			md.BeforePlace = ""
			md.AfterPlace = entry.Create.Row + ":" + entry.Create.Col
			md.BeforeScore = nil
			sc := entry.Create.Score
			md.AfterScore = &sc
			md.BeforeRev = "0"
			md.AfterRev = "1"
		} else if entry.Move != nil {
			md.BeforePlace = mem.row + ":" + mem.col
			md.AfterPlace = entry.Move.Row + ":" + entry.Move.Col
			bScore := mem.score
			md.BeforeScore = &bScore
			if entry.Move.Score != nil {
				aScore := *entry.Move.Score
				md.AfterScore = &aScore
			} else {
				md.AfterScore = &bScore
			}
			md.BeforeRev = strconv.FormatUint(mem.revision, 10)
			if isChange {
				md.AfterRev = strconv.FormatUint(mem.revision+1, 10)
			} else {
				md.AfterRev = md.BeforeRev
			}
		} else if entry.Remove {
			md.BeforePlace = mem.row + ":" + mem.col
			md.AfterPlace = ""
			bScore := mem.score
			md.BeforeScore = &bScore
			md.AfterScore = nil
			md.BeforeRev = strconv.FormatUint(mem.revision, 10)
			md.AfterRev = strconv.FormatUint(mem.revision+1, 10)
		} else {
			if mem.placed {
				md.BeforePlace = mem.row + ":" + mem.col
				md.AfterPlace = md.BeforePlace
				bScore := mem.score
				md.BeforeScore = &bScore
				md.AfterScore = &bScore
			} else {
				md.BeforePlace = ""
				md.AfterPlace = ""
				md.BeforeScore = nil
				md.AfterScore = nil
			}
			if mem.exists {
				md.BeforeRev = strconv.FormatUint(mem.revision, 10)
			} else {
				md.BeforeRev = "0"
			}
			if isChange {
				md.AfterRev = strconv.FormatUint(mem.revision+1, 10)
			} else {
				md.AfterRev = md.BeforeRev
			}
		}
		memberDeltas[i] = md
	}

	return ntable.BatchDelta{
		OperationID:   manifest.OperationID,
		Actor:         manifest.Actor,
		SelectedCount: len(manifest.Members),
		GuardCount:    guardCount,
		ChangedCount:  changedCount,
		Members:       memberDeltas,
	}
}

func (o *batchOracle) verifyPhysical(t *testing.T, ctx context.Context, c *redis.Client) {
	t.Helper()
	for cellKey, expectedMembers := range o.table.cells {
		parts := strings.Split(cellKey, ":")
		r, col := parts[0], parts[1]
		redisKey := "table:" + o.table.name + ":cell:" + r + ":" + col
		zs, err := c.ZRangeWithScores(ctx, redisKey, 0, -1).Result()
		if err != nil {
			t.Fatalf("verifyPhysical: ZRangeWithScores %s: %v", redisKey, err)
		}
		if len(zs) != len(expectedMembers) {
			t.Fatalf("verifyPhysical: cell %s member count mismatch: got %d, want %d", cellKey, len(zs), len(expectedMembers))
		}
		for _, z := range zs {
			memID, ok := z.Member.(string)
			if !ok {
				t.Fatalf("verifyPhysical: cell %s member not string: %v", cellKey, z.Member)
			}
			expScore, found := expectedMembers[memID]
			if !found {
				t.Fatalf("verifyPhysical: cell %s unexpected extra member %s", cellKey, memID)
			}
			if z.Score != expScore {
				t.Fatalf("verifyPhysical: cell %s member %s score mismatch: got %g, want %g", cellKey, memID, z.Score, expScore)
			}
		}
	}

	for mid, m := range o.members {
		h, err := c.HGetAll(ctx, ntable.MemberKey(mid)).Result()
		if err != nil {
			t.Fatalf("verifyPhysical: HGetAll %s: %v", mid, err)
		}
		if !m.exists {
			if len(h) != 0 {
				t.Fatalf("verifyPhysical: member %s expected not to exist, got hash %v", mid, h)
			}
			continue
		}
		if len(h) == 0 {
			t.Fatalf("verifyPhysical: member %s expected to exist, but hash is empty", mid)
		}
		expectedRev := strconv.FormatUint(m.revision, 10)
		if h["revision"] != expectedRev {
			t.Fatalf("verifyPhysical: member %s revision mismatch: got %s, want %s", mid, h["revision"], expectedRev)
		}
		if m.exists {
			if h["epoch"] != "0" && h["epoch"] != "" {
				t.Fatalf("verifyPhysical: member %s epoch unexpected: %q", mid, h["epoch"])
			}
		}
		placeKey := "place:" + o.table.name
		if m.placed {
			expectedPlace := m.row + ":" + m.col
			if h[placeKey] != expectedPlace {
				t.Fatalf("verifyPhysical: member %s place mismatch: got %s, want %s", mid, h[placeKey], expectedPlace)
			}
			cellKey := m.row + ":" + m.col
			if expScore, ok := o.table.cells[cellKey][mid]; !ok || expScore != m.score {
				t.Fatalf("verifyPhysical: member %s score in cell %s mismatch: got %g, want %g", mid, cellKey, expScore, m.score)
			}
		} else {
			if _, ok := h[placeKey]; ok {
				t.Fatalf("verifyPhysical: member %s unplaced but has place field %s", mid, h[placeKey])
			}
		}
		for k, v := range m.fields {
			actualVal, exists := h[k]
			if !exists {
				t.Fatalf("verifyPhysical: member %s field %s expected to exist with value %q, but missing from hash", mid, k, v)
			}
			if actualVal != v {
				t.Fatalf("verifyPhysical: member %s field %s mismatch: got %q, want %q", mid, k, actualVal, v)
			}
		}
		for k := range h {
			if k == "revision" || k == "epoch" {
				continue
			}
			if k == placeKey {
				if !m.placed {
					t.Fatalf("verifyPhysical: member %s unexpected place field: %s", mid, h[k])
				}
				continue
			}
			if strings.HasPrefix(k, "place:") {
				t.Fatalf("verifyPhysical: member %s unexpected foreign place field %s=%s", mid, k, h[k])
			}
			if _, ok := m.fields[k]; !ok {
				t.Fatalf("verifyPhysical: member %s unexpected extra field %s=%s", mid, k, h[k])
			}
		}
	}

	keys, err := c.Keys(ctx, "table:"+o.table.name+":*").Result()
	if err != nil {
		t.Fatalf("verifyPhysical: KEYS table:%s:*: %v", o.table.name, err)
	}
	for _, k := range keys {
		suffix := strings.TrimPrefix(k, "table:"+o.table.name+":")
		switch {
		case suffix == "definition" || suffix == "revision" || suffix == "identity" || suffix == "rows" || suffix == "changes" || suffix == "events":
		case strings.HasPrefix(suffix, "row:"):
		case strings.HasPrefix(suffix, "cell:"):
			cell := strings.TrimPrefix(suffix, "cell:")
			if _, ok := o.table.cells[cell]; !ok {
				t.Fatalf("verifyPhysical: unexpected cell key %s in store", k)
			}
		case strings.HasPrefix(suffix, "op:"):
		default:
			t.Fatalf("verifyPhysical: unexpected table key in store: %s", k)
		}
	}
}

func dumpStore(ctx context.Context, c *redis.Client) (map[string]string, error) {
	keys, err := c.Keys(ctx, "table:*").Result()
	if err != nil {
		return nil, err
	}
	keys = append(keys, ntable.Registry, extKey)
	pipe := c.Pipeline()
	cmds := make([]*redis.StringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.Dump(ctx, k)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := make(map[string]string)
	for i, k := range keys {
		v, err := cmds[i].Result()
		if err == nil {
			out[k] = v
		}
	}
	return out, nil
}

func generateExpect(memState *batchMemberState, rnd *rand.Rand, fieldsPool, valuesPool []string) *ntable.MemberExpect {
	expRev := strconv.FormatUint(memState.revision, 10)
	if rnd.Intn(10) == 0 {
		expRev = strconv.FormatUint(memState.revision+5, 10)
	}
	exp := &ntable.MemberExpect{
		Revision: expRev,
	}
	if memState.placed && rnd.Intn(2) == 0 {
		exp.Place = &ntable.PlaceExpect{Row: memState.row, Col: memState.col}
	}
	guardChoice := rnd.Intn(4)
	f := fieldsPool[rnd.Intn(len(fieldsPool))]
	trueVal := true
	switch guardChoice {
	case 0:
		exp.Fields = map[string]ntable.FieldGuard{
			f: {Absent: &trueVal},
		}
	case 1:
		if curVal, ok := memState.fields[f]; ok && rnd.Intn(4) != 0 {
			exp.Fields = map[string]ntable.FieldGuard{
				f: {OneOf: []string{curVal, "bogus1", "bogus2"}},
			}
		} else {
			exp.Fields = map[string]ntable.FieldGuard{
				f: {OneOf: []string{"bogus1", "bogus2"}},
			}
		}
	case 2:
		if curVal, ok := memState.fields[f]; ok {
			if rnd.Intn(4) == 0 {
				wrong := curVal + "_wrong"
				exp.Fields = map[string]ntable.FieldGuard{
					f: {Equals: &wrong},
				}
			} else {
				vCopy := curVal
				exp.Fields = map[string]ntable.FieldGuard{
					f: {Equals: &vCopy},
				}
			}
		} else {
			val := valuesPool[rnd.Intn(len(valuesPool))]
			exp.Fields = map[string]ntable.FieldGuard{
				f: {Equals: &val},
			}
		}
	case 3:
		// No field guard, only revision / place
	}
	return exp
}

func TestBatchApplyPropertyAndReceiptReplay(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()

	rows := []string{"r1", "r2"}
	cols := []string{"a", "b", "c"}
	membersPool := []string{"m1", "m2", "m3", "m4"}
	fieldsPool := []string{"role", "tier", "dept"}
	valuesPool := []string{"worker", "lead", "gold", "silver", "sales", ""}

	seeds := []int64{101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 114, 115, 116}

	tally := struct {
		accepted, refused, replays, conflicts int
	}{}

	defer func() {
		t.Logf("batch property test summary: accepted=%d refused=%d replays=%d conflicts=%d",
			tally.accepted, tally.refused, tally.replays, tally.conflicts)
		if tally.accepted == 0 || tally.refused == 0 || tally.replays == 0 || tally.conflicts == 0 {
			t.Errorf("insufficient coverage in batch property test: %+v", tally)
		}
	}()

	for _, seed := range seeds {
		if err := c.FlushAll(ctx).Err(); err != nil {
			t.Fatalf("flushall: %v", err)
		}

		rnd := rand.New(rand.NewSource(seed))
		tableName := fmt.Sprintf("t_prop_%d", seed)

		def := propDefinition(tableName)
		if err := ntable.Create(ctx, c, def, time.Now()); err != nil {
			t.Fatalf("create %s: %v", tableName, err)
		}
		for _, r := range rows {
			if _, err := ntable.RowAdd(ctx, c, tableName, r, ntable.RowSpec{}); err != nil {
				t.Fatalf("rowAdd %s %s: %v", tableName, r, err)
			}
		}

		revStr, err := c.HGet(ctx, ntable.DefKey(tableName)+":revision", "n").Result()
		if err != nil {
			t.Fatalf("get table revision: %v", err)
		}
		initRev, err := strconv.ParseUint(revStr, 10, 64)
		if err != nil {
			t.Fatalf("parse initial revision: %v", err)
		}

		oracle := newBatchOracle(tableName, rows, cols, initRev)

		for step := 0; step < 128; step++ {
			opID := fmt.Sprintf("op-s%d-step-%d-%d", seed, step, 1000+rnd.Intn(999000))

			expectedRev := strconv.FormatUint(oracle.table.revision, 10)
			if rnd.Intn(8) == 0 {
				expectedRev = strconv.FormatUint(oracle.table.revision+10, 10)
			}

			targetTable := tableName
			if rnd.Intn(20) == 0 {
				targetTable = "nonexistent_table"
			}

			numMembers := 1 + rnd.Intn(3)
			batchMembers := make([]ntable.BatchMemberEntry, numMembers)

			hasDup := numMembers > 1 && rnd.Intn(8) == 0
			var firstID string

			for i := 0; i < numMembers; i++ {
				var mID string
				if i > 0 && hasDup && firstID != "" {
					mID = firstID
				} else {
					mID = membersPool[rnd.Intn(len(membersPool))]
					if i == 0 {
						firstID = mID
					}
				}

				memState := oracle.members[mID]
				isPlaced := memState != nil && memState.placed

				entry := ntable.BatchMemberEntry{ID: mID}

				if !isPlaced {
					opChoice := rnd.Intn(4)
					switch opChoice {
					case 0, 1:
						targetRow := rows[rnd.Intn(len(rows))]
						if rnd.Intn(20) == 0 {
							targetRow = "bad_row"
						}
						targetCol := cols[rnd.Intn(len(cols))]
						score := float64(1 + rnd.Intn(100))

						entry.Create = &ntable.MemberCreateOp{
							Row:   targetRow,
							Col:   targetCol,
							Score: score,
						}
						if rnd.Intn(8) == 0 {
							entry.Expect = &ntable.MemberExpect{Absent: false, Revision: "1"}
						} else {
							entry.Expect = &ntable.MemberExpect{Absent: true}
						}
						if rnd.Intn(2) == 0 {
							f := fieldsPool[rnd.Intn(len(fieldsPool))]
							v := valuesPool[rnd.Intn(len(valuesPool))]
							entry.Set = map[string]string{f: v}
						}
					case 2:
						entry.Move = &ntable.MemberMoveOp{Row: "r1", Col: "a"}
						entry.Expect = &ntable.MemberExpect{Absent: false}
					case 3:
						entry.Remove = true
						entry.Expect = &ntable.MemberExpect{Absent: false}
					}
				} else {
					opChoice := rnd.Intn(6)
					switch opChoice {
					case 0:
						dstRow := rows[rnd.Intn(len(rows))]
						dstCol := cols[rnd.Intn(len(cols))]
						var newScore *float64
						if rnd.Intn(2) == 0 {
							sc := float64(1 + rnd.Intn(100))
							newScore = &sc
						}
						entry.Move = &ntable.MemberMoveOp{
							Row:   dstRow,
							Col:   dstCol,
							Score: newScore,
						}
						expRev := strconv.FormatUint(memState.revision, 10)
						if rnd.Intn(8) == 0 {
							expRev = strconv.FormatUint(memState.revision+5, 10)
						}
						expPlace := &ntable.PlaceExpect{Row: memState.row, Col: memState.col}
						if rnd.Intn(8) == 0 {
							expPlace = &ntable.PlaceExpect{Row: "bad_r", Col: "bad_c"}
						}
						entry.Expect = &ntable.MemberExpect{
							Revision: expRev,
							Place:    expPlace,
						}
					case 1:
						entry.Remove = true
						expRev := strconv.FormatUint(memState.revision, 10)
						if rnd.Intn(8) == 0 {
							expRev = strconv.FormatUint(memState.revision+5, 10)
						}
						entry.Expect = &ntable.MemberExpect{Revision: expRev}
					case 2:
						f := fieldsPool[rnd.Intn(len(fieldsPool))]
						v := valuesPool[rnd.Intn(len(valuesPool))]
						entry.Set = map[string]string{f: v}
						entry.Expect = generateExpect(memState, rnd, fieldsPool, valuesPool)
					case 3:
						f := fieldsPool[rnd.Intn(len(fieldsPool))]
						entry.Unset = []string{f}
						entry.Expect = generateExpect(memState, rnd, fieldsPool, valuesPool)
					case 4:
						entry.Expect = generateExpect(memState, rnd, fieldsPool, valuesPool)
					case 5:
						entry.Create = &ntable.MemberCreateOp{Row: "r1", Col: "a", Score: 10}
						entry.Expect = &ntable.MemberExpect{Absent: true}
					}
				}

				batchMembers[i] = entry
			}

			manifest := ntable.BatchManifest{
				Schema:                1,
				Table:                 targetTable,
				Epoch:                 "0",
				ExpectedTableRevision: expectedRev,
				OperationID:           opID,
				Actor:                 "agent-proptest",
				Members:               batchMembers,
			}

			wantVerdict, willChange := oracle.predict(manifest)

			beforeSnap, err := dumpStore(ctx, c)
			if err != nil {
				t.Fatalf("dumpStore before: %v", err)
			}

			rcpt, err := ntable.ApplyBatch(ctx, c, manifest)

			if !wantVerdict.ok() {
				tally.refused++
				if err == nil {
					t.Fatalf("seed %d step %d: wanted refusal %s, got success receipt %+v", seed, step, wantVerdict, rcpt)
				}
				if !strings.Contains(err.Error(), "changed=no") {
					t.Fatalf("seed %d step %d: refusal missing changed=no: %v", seed, step, err)
				}
				afterSnap, err := dumpStore(ctx, c)
				if err != nil {
					t.Fatalf("dumpStore after: %v", err)
				}
				if diff := diffSnapshots(beforeSnap, afterSnap); diff != "" {
					t.Fatalf("seed %d step %d: refused batch changed store state: %s (err: %v)", seed, step, diff, err)
				}
			} else {
				tally.accepted++
				if err != nil {
					t.Fatalf("seed %d step %d: expected success, got error: %v", seed, step, err)
				}

				wantOutcome := "noop"
				if willChange {
					wantOutcome = "changed"
				}
				if rcpt.Outcome != wantOutcome {
					t.Fatalf("seed %d step %d: expected outcome %q, got %q", seed, step, wantOutcome, rcpt.Outcome)
				}
				if rcpt.Before != oracle.table.revision {
					t.Fatalf("seed %d step %d: expected rev before %d, got %d", seed, step, oracle.table.revision, rcpt.Before)
				}
				if rcpt.After != oracle.table.revision+1 {
					t.Fatalf("seed %d step %d: expected rev after %d, got %d", seed, step, oracle.table.revision+1, rcpt.After)
				}

				predDelta := oracle.predictDelta(manifest)
				if rcpt.BatchDelta == nil {
					t.Fatalf("missing BatchDelta")
				}
				if rcpt.BatchDelta.SelectedCount != predDelta.SelectedCount {
					t.Fatalf("SelectedCount mismatch: got %d, want %d", rcpt.BatchDelta.SelectedCount, predDelta.SelectedCount)
				}
				if rcpt.BatchDelta.GuardCount != predDelta.GuardCount {
					t.Fatalf("GuardCount mismatch: got %d, want %d", rcpt.BatchDelta.GuardCount, predDelta.GuardCount)
				}
				if rcpt.BatchDelta.ChangedCount != predDelta.ChangedCount {
					t.Fatalf("ChangedCount mismatch: got %d, want %d", rcpt.BatchDelta.ChangedCount, predDelta.ChangedCount)
				}
				if len(rcpt.BatchDelta.Members) != len(predDelta.Members) {
					t.Fatalf("seed %d step %d: members delta count mismatch: got %d, want %d", seed, step, len(rcpt.BatchDelta.Members), len(predDelta.Members))
				}
				for idx := range rcpt.BatchDelta.Members {
					gotM := rcpt.BatchDelta.Members[idx]
					wantM := predDelta.Members[idx]
					if gotM.ID != wantM.ID {
						t.Fatalf("seed %d step %d member %d: id got %s, want %s", seed, step, idx, gotM.ID, wantM.ID)
					}
					if gotM.BeforeRev != wantM.BeforeRev || gotM.AfterRev != wantM.AfterRev {
						t.Fatalf("seed %d step %d member %s: rev got %s->%s, want %s->%s", seed, step, gotM.ID, gotM.BeforeRev, gotM.AfterRev, wantM.BeforeRev, wantM.AfterRev)
					}
					if gotM.BeforePlace != wantM.BeforePlace || gotM.AfterPlace != wantM.AfterPlace {
						t.Fatalf("seed %d step %d member %s: place got %s->%s, want %s->%s", seed, step, gotM.ID, gotM.BeforePlace, gotM.AfterPlace, wantM.BeforePlace, wantM.AfterPlace)
					}
					if (gotM.BeforeScore == nil) != (wantM.BeforeScore == nil) || (gotM.BeforeScore != nil && *gotM.BeforeScore != *wantM.BeforeScore) {
						t.Fatalf("seed %d step %d member %s: score before got %v, want %v", seed, step, gotM.ID, gotM.BeforeScore, wantM.BeforeScore)
					}
					if (gotM.AfterScore == nil) != (wantM.AfterScore == nil) || (gotM.AfterScore != nil && *gotM.AfterScore != *wantM.AfterScore) {
						t.Fatalf("seed %d step %d member %s: score after got %v, want %v", seed, step, gotM.ID, gotM.AfterScore, wantM.AfterScore)
					}
					if len(gotM.Fields) != len(wantM.Fields) {
						t.Fatalf("seed %d step %d member %s: fields count mismatch: got %d, want %d", seed, step, gotM.ID, len(gotM.Fields), len(wantM.Fields))
					}
					for f, wantCh := range wantM.Fields {
						gotCh, ok := gotM.Fields[f]
						if !ok {
							t.Fatalf("seed %d step %d member %s: field %s missing in delta", seed, step, gotM.ID, f)
						}
						if (gotCh.Before == nil) != (wantCh.Before == nil) || (gotCh.Before != nil && *gotCh.Before != *wantCh.Before) {
							t.Fatalf("seed %d step %d member %s field %s before mismatch: got %v, want %v", seed, step, gotM.ID, f, gotCh.Before, wantCh.Before)
						}
						if (gotCh.After == nil) != (wantCh.After == nil) || (gotCh.After != nil && *gotCh.After != *wantCh.After) {
							t.Fatalf("seed %d step %d member %s field %s after mismatch: got %v, want %v", seed, step, gotM.ID, f, gotCh.After, wantCh.After)
						}
					}
				}

				oracle.apply(manifest)
				oracle.verifyPhysical(t, ctx, c)

				rs, err := ntable.ReadSetMembers(ctx, c, oracle.table.name, membersPool)
				if err != nil {
					t.Fatalf("seed %d step %d: ReadSetMembers: %v", seed, step, err)
				}
				if rs.Revision != oracle.table.revision {
					t.Fatalf("seed %d step %d: ReadSet revision %d != oracle %d", seed, step, rs.Revision, oracle.table.revision)
				}
				for _, mid := range membersPool {
					mState := oracle.members[mid]
					if mState == nil || !mState.exists {
						if !rs.IsMissing(mid) {
							t.Fatalf("seed %d step %d: member %s should be missing, but found in ReadSet", seed, step, mid)
						}
					} else {
						rm, found := rs.Member(mid)
						if !found {
							t.Fatalf("seed %d step %d: member %s should exist in ReadSet, but not found", seed, step, mid)
						}
						if rm.Placed != mState.placed {
							t.Fatalf("seed %d step %d: member %s placed=%v, oracle placed=%v", seed, step, mid, rm.Placed, mState.placed)
						}
						if mState.placed {
							if rm.Row != mState.row || rm.Col != mState.col {
								t.Fatalf("seed %d step %d: member %s place=%s:%s, oracle=%s:%s", seed, step, mid, rm.Row, rm.Col, mState.row, mState.col)
							}
							if rm.Score != mState.score {
								t.Fatalf("seed %d step %d: member %s score=%g, oracle=%g", seed, step, mid, rm.Score, mState.score)
							}
						}
						if rm.Revision != mState.revision {
							t.Fatalf("seed %d step %d: member %s revision=%d, oracle=%d", seed, step, mid, rm.Revision, mState.revision)
						}
						for fk, fv := range mState.fields {
							if rm.Fields[fk] != fv {
								t.Fatalf("seed %d step %d: member %s field %s=%q, oracle=%q", seed, step, mid, fk, rm.Fields[fk], fv)
							}
						}
					}
				}

				// Receipt Replay 1: Exact replay
				tally.replays++
				replaySnapBefore, err := dumpStore(ctx, c)
				if err != nil {
					t.Fatalf("replay dumpStore before: %v", err)
				}
				replayRcpt, err := ntable.ApplyBatch(ctx, c, manifest)
				if err != nil {
					t.Fatalf("seed %d step %d: exact replay failed: %v", seed, step, err)
				}
				if replayRcpt.ID != rcpt.ID || replayRcpt.Outcome != rcpt.Outcome ||
					replayRcpt.Before != rcpt.Before || replayRcpt.After != rcpt.After {
					t.Fatalf("seed %d step %d: replay receipt mismatch: got %+v, original %+v", seed, step, replayRcpt, rcpt)
				}
				replaySnapAfter, err := dumpStore(ctx, c)
				if err != nil {
					t.Fatalf("replay dumpStore after: %v", err)
				}
				if diff := diffSnapshots(replaySnapBefore, replaySnapAfter); diff != "" {
					t.Fatalf("seed %d step %d: exact replay mutated store: %s", seed, step, diff)
				}

				// Receipt Replay 2: Conflicting payload with identical operation ID
				tally.conflicts++
				conflictManifest := manifest
				conflictManifest.Actor = manifest.Actor + "-conflict"
				conflictSnapBefore, err := dumpStore(ctx, c)
				if err != nil {
					t.Fatalf("conflict dumpStore before: %v", err)
				}
				_, conflictErr := ntable.ApplyBatch(ctx, c, conflictManifest)
				if conflictErr == nil {
					t.Fatalf("seed %d step %d: conflicting replay unexpectedly succeeded", seed, step)
				}
				if !errors.Is(conflictErr, ntable.ErrOpConflict) {
					t.Fatalf("seed %d step %d: expected ErrOpConflict, got: %v", seed, step, conflictErr)
				}
				if !strings.Contains(conflictErr.Error(), "changed=no") {
					t.Fatalf("seed %d step %d: conflict error missing changed=no: %v", seed, step, conflictErr)
				}
				conflictSnapAfter, err := dumpStore(ctx, c)
				if err != nil {
					t.Fatalf("conflict dumpStore after: %v", err)
				}
				if diff := diffSnapshots(conflictSnapBefore, conflictSnapAfter); diff != "" {
					t.Fatalf("seed %d step %d: conflicting replay mutated store: %s", seed, step, diff)
				}
			}
		}
	}
}

func TestBatchPropertyNMaxAndLimits(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()

	const tableName = "t_limits"
	_ = c.FlushAll(ctx).Err()
	def := propDefinition(tableName)
	if err := ntable.Create(ctx, c, def, time.Now()); err != nil {
		t.Fatalf("create %s: %v", tableName, err)
	}
	if _, err := ntable.RowAdd(ctx, c, tableName, "r1", ntable.RowSpec{}); err != nil {
		t.Fatalf("rowAdd %s: %v", tableName, err)
	}

	revStr, err := c.HGet(ctx, ntable.DefKey(tableName)+":revision", "n").Result()
	if err != nil {
		t.Fatalf("get table revision: %v", err)
	}
	curRev, err := strconv.ParseUint(revStr, 10, 64)
	if err != nil {
		t.Fatalf("parse revision: %v", err)
	}

	// 1. 129 syntactic mutations -> MUST BE REFUSED with LIMIT and changed=no, 0 store changes
	mut129 := make([]ntable.BatchMemberEntry, 129)
	for i := 0; i < 129; i++ {
		mut129[i] = ntable.BatchMemberEntry{
			ID:     fmt.Sprintf("mem_mut_129_%d", i),
			Create: &ntable.MemberCreateOp{Row: "r1", Col: "a", Score: float64(i)},
			Expect: &ntable.MemberExpect{Absent: true},
		}
	}
	manifest129 := ntable.BatchManifest{
		Schema:                1,
		Table:                 tableName,
		Epoch:                 "0",
		ExpectedTableRevision: strconv.FormatUint(curRev, 10),
		OperationID:           "op-mut-129",
		Actor:                 "tester",
		Members:               mut129,
	}

	snapBefore129, err := dumpStore(ctx, c)
	if err != nil {
		t.Fatalf("dumpStore: %v", err)
	}
	rcpt129, err129 := ntable.ApplyBatch(ctx, c, manifest129)
	if err129 == nil {
		t.Fatalf("expected refusal for 129 mutations, got receipt: %+v", rcpt129)
	}
	if !strings.Contains(strings.ToLower(err129.Error()), "limit") {
		t.Fatalf("expected limit in err, got: %v", err129)
	}
	if !strings.Contains(err129.Error(), "changed=no") {
		t.Fatalf("expected changed=no in err, got: %v", err129)
	}
	snapAfter129, err := dumpStore(ctx, c)
	if err != nil {
		t.Fatalf("dumpStore: %v", err)
	}
	if diff := diffSnapshots(snapBefore129, snapAfter129); diff != "" {
		t.Fatalf("129 mutations mutated store: %s", diff)
	}

	// 2. 128 syntactic mutations -> MUST BE ACCEPTED (Nmax mutations)
	mut128 := make([]ntable.BatchMemberEntry, 128)
	for i := 0; i < 128; i++ {
		mut128[i] = ntable.BatchMemberEntry{
			ID:     fmt.Sprintf("mem_mut_128_%d", i),
			Create: &ntable.MemberCreateOp{Row: "r1", Col: "a", Score: float64(i)},
			Expect: &ntable.MemberExpect{Absent: true},
		}
	}
	manifest128 := ntable.BatchManifest{
		Schema:                1,
		Table:                 tableName,
		Epoch:                 "0",
		ExpectedTableRevision: strconv.FormatUint(curRev, 10),
		OperationID:           "op-mut-128",
		Actor:                 "tester",
		Members:               mut128,
	}
	rcpt128, err128 := ntable.ApplyBatch(ctx, c, manifest128)
	if err128 != nil {
		t.Fatalf("128 mutations rejected: %v", err128)
	}
	if rcpt128.Outcome != "changed" {
		t.Fatalf("expected outcome 'changed', got: %s", rcpt128.Outcome)
	}
	curRev = rcpt128.After

	// 3. 1,025 guard-only entries -> MUST BE REFUSED with LIMIT and changed=no, 0 store changes
	guard1025 := make([]ntable.BatchMemberEntry, 1025)
	for i := 0; i < 1025; i++ {
		guard1025[i] = ntable.BatchMemberEntry{
			ID:     fmt.Sprintf("mem_guard_1025_%d", i),
			Expect: &ntable.MemberExpect{Absent: true},
		}
	}
	manifestGuard1025 := ntable.BatchManifest{
		Schema:                1,
		Table:                 tableName,
		Epoch:                 "0",
		ExpectedTableRevision: strconv.FormatUint(curRev, 10),
		OperationID:           "op-guard-1025",
		Actor:                 "tester",
		Members:               guard1025,
	}
	snapBeforeGuard1025, err := dumpStore(ctx, c)
	if err != nil {
		t.Fatalf("dumpStore: %v", err)
	}
	rcptGuard1025, errGuard1025 := ntable.ApplyBatch(ctx, c, manifestGuard1025)
	if errGuard1025 == nil {
		t.Fatalf("expected refusal for 1025 guards, got receipt: %+v", rcptGuard1025)
	}
	if !strings.Contains(strings.ToLower(errGuard1025.Error()), "limit") {
		t.Fatalf("expected limit in err, got: %v", errGuard1025)
	}
	if !strings.Contains(errGuard1025.Error(), "changed=no") {
		t.Fatalf("expected changed=no in err, got: %v", errGuard1025)
	}
	snapAfterGuard1025, err := dumpStore(ctx, c)
	if err != nil {
		t.Fatalf("dumpStore: %v", err)
	}
	if diff := diffSnapshots(snapBeforeGuard1025, snapAfterGuard1025); diff != "" {
		t.Fatalf("1025 guards mutated store: %s", diff)
	}

	// 4. 1,024 guard-only entries -> MUST BE ACCEPTED (Nmax guards, outcome="noop")
	guard1024 := make([]ntable.BatchMemberEntry, 1024)
	for i := 0; i < 1024; i++ {
		guard1024[i] = ntable.BatchMemberEntry{
			ID:     fmt.Sprintf("mem_guard_1024_%d", i),
			Expect: &ntable.MemberExpect{Absent: true},
		}
	}
	manifestGuard1024 := ntable.BatchManifest{
		Schema:                1,
		Table:                 tableName,
		Epoch:                 "0",
		ExpectedTableRevision: strconv.FormatUint(curRev, 10),
		OperationID:           "op-guard-1024",
		Actor:                 "tester",
		Members:               guard1024,
	}
	rcptGuard1024, errGuard1024 := ntable.ApplyBatch(ctx, c, manifestGuard1024)
	if errGuard1024 != nil {
		t.Fatalf("1024 guards rejected: %v", errGuard1024)
	}
	if rcptGuard1024.Outcome != "noop" {
		t.Fatalf("expected outcome 'noop', got: %s", rcptGuard1024.Outcome)
	}
}

func TestBatchReceiptDrivenStateReplay(t *testing.T) {
	t.Parallel()
	_, clientA := live(t)
	_, clientB := live(t)
	ctx := context.Background()

	const targetTable = "demo"
	rows := []string{"r1", "r2"}
	cols := []string{"a", "b", "c"}
	membersPool := []string{"m1", "m2", "m3", "m4", "m5"}

	_ = clientA.FlushAll(ctx).Err()
	defA := propDefinition(targetTable)
	if err := ntable.Create(ctx, clientA, defA, time.Now()); err != nil {
		t.Fatalf("create %s on clientA: %v", targetTable, err)
	}
	for _, r := range rows {
		if _, err := ntable.RowAdd(ctx, clientA, targetTable, r, ntable.RowSpec{}); err != nil {
			t.Fatalf("rowAdd %s %s on clientA: %v", targetTable, r, err)
		}
	}

	_ = clientB.FlushAll(ctx).Err()
	defB := propDefinition(targetTable)
	if err := ntable.Create(ctx, clientB, defB, time.Now()); err != nil {
		t.Fatalf("create %s on clientB: %v", targetTable, err)
	}
	for _, r := range rows {
		if _, err := ntable.RowAdd(ctx, clientB, targetTable, r, ntable.RowSpec{}); err != nil {
			t.Fatalf("rowAdd %s %s on clientB: %v", targetTable, r, err)
		}
	}

	replayDelta := func(delta *ntable.BatchDelta, targetTable string, rev uint64) {
		t.Helper()
		if err := clientB.HSet(ctx, ntable.DefKey(targetTable)+":revision", "n", rev).Err(); err != nil {
			t.Fatalf("replay revision: %v", err)
		}
		for _, md := range delta.Members {
			memKey := ntable.MemberKey(md.ID)
			curHash, err := clientB.HGetAll(ctx, memKey).Result()
			if err != nil {
				t.Fatalf("replayDelta: clientB HGetAll %s: %v", md.ID, err)
			}

			// Validate BeforeRev
			curRev := curHash["revision"]
			if curRev == "" {
				curRev = "0"
			}
			expectedBeforeRev := md.BeforeRev
			if expectedBeforeRev == "" {
				expectedBeforeRev = "0"
			}
			if curRev != expectedBeforeRev {
				t.Fatalf("replayDelta: member %s before rev mismatch: got %s, want %s", md.ID, curRev, expectedBeforeRev)
			}

			// Validate BeforePlace
			placeKey := "place:" + targetTable
			curPlace := curHash[placeKey]
			if curPlace != md.BeforePlace {
				t.Fatalf("replayDelta: member %s before place mismatch: got %q, want %q", md.ID, curPlace, md.BeforePlace)
			}

			// Validate BeforeScore
			if md.BeforePlace != "" {
				parts := strings.Split(md.BeforePlace, ":")
				cellKey := fmt.Sprintf("table:%s:cell:%s:%s", targetTable, parts[0], parts[1])
				score, err := clientB.ZScore(ctx, cellKey, md.ID).Result()
				if err != nil {
					t.Fatalf("replayDelta: member %s missing from before cell %s: %v", md.ID, cellKey, err)
				}
				if md.BeforeScore != nil && score != *md.BeforeScore {
					t.Fatalf("replayDelta: member %s before score mismatch in %s: got %g, want %g", md.ID, cellKey, score, *md.BeforeScore)
				}
			} else {
				if md.BeforeScore != nil {
					t.Fatalf("replayDelta: member %s has BeforeScore %g but BeforePlace is empty", md.ID, *md.BeforeScore)
				}
			}

			// Validate md.Fields[f].Before
			for f, ch := range md.Fields {
				actualVal, exists := curHash[f]
				if ch.Before == nil {
					if exists {
						t.Fatalf("replayDelta: member %s field %s expected absent, got %q", md.ID, f, actualVal)
					}
				} else {
					if !exists {
						t.Fatalf("replayDelta: member %s field %s expected %q, but missing", md.ID, f, *ch.Before)
					}
					if actualVal != *ch.Before {
						t.Fatalf("replayDelta: member %s field %s before mismatch: got %q, want %q", md.ID, f, actualVal, *ch.Before)
					}
				}
			}

			// Apply md to clientB
			if md.BeforeRev == "0" || md.BeforeRev == "" {
				if err := clientB.HSet(ctx, memKey, "epoch", "0").Err(); err != nil {
					t.Fatalf("replay member epoch: %v", err)
				}
			}
			if md.AfterRev != "" {
				if err := clientB.HSet(ctx, memKey, "revision", md.AfterRev).Err(); err != nil {
					t.Fatalf("replay member revision: %v", err)
				}
			}
			if md.BeforePlace != "" && md.BeforePlace != md.AfterPlace {
				oldParts := strings.Split(md.BeforePlace, ":")
				oldKey := fmt.Sprintf("table:%s:cell:%s:%s", targetTable, oldParts[0], oldParts[1])
				if err := clientB.ZRem(ctx, oldKey, md.ID).Err(); err != nil {
					t.Fatalf("replay cell zrem: %v", err)
				}
			}
			if md.AfterPlace != "" {
				newParts := strings.Split(md.AfterPlace, ":")
				newKey := fmt.Sprintf("table:%s:cell:%s:%s", targetTable, newParts[0], newParts[1])
				var sc float64
				if md.AfterScore != nil {
					sc = *md.AfterScore
				}
				if err := clientB.ZAdd(ctx, newKey, redis.Z{Score: sc, Member: md.ID}).Err(); err != nil {
					t.Fatalf("replay cell zadd: %v", err)
				}
				if err := clientB.HSet(ctx, memKey, "place:"+targetTable, md.AfterPlace).Err(); err != nil {
					t.Fatalf("replay member place: %v", err)
				}
			} else if md.BeforePlace != "" {
				if err := clientB.HDel(ctx, memKey, "place:"+targetTable).Err(); err != nil {
					t.Fatalf("replay member hdel place: %v", err)
				}
			}
			for f, ch := range md.Fields {
				if ch.After != nil {
					if err := clientB.HSet(ctx, memKey, f, *ch.After).Err(); err != nil {
						t.Fatalf("replay field hset: %v", err)
					}
				} else {
					if err := clientB.HDel(ctx, memKey, f).Err(); err != nil {
						t.Fatalf("replay field hdel: %v", err)
					}
				}
			}
		}

		opKey := "table:" + targetTable + ":op:" + delta.OperationID
		clientB.HSet(ctx, opKey, "digest", delta.Digest, "actor", delta.Actor, "rev", rev)
		clientB.XAdd(ctx, &redis.XAddArgs{
			Stream: "table:" + targetTable + ":events",
			Values: map[string]any{"op": delta.OperationID, "rev": rev, "digest": delta.Digest},
		})
	}

	verifyTablesMatch := func() {
		t.Helper()
		revA, err := clientA.HGet(ctx, ntable.DefKey(targetTable)+":revision", "n").Result()
		if err != nil {
			t.Fatalf("get rev A: %v", err)
		}
		revB, err := clientB.HGet(ctx, ntable.DefKey(targetTable)+":revision", "n").Result()
		if err != nil {
			t.Fatalf("get rev B: %v", err)
		}
		if revA != revB {
			t.Fatalf("table revision mismatch: A=%s, B=%s", revA, revB)
		}

		for _, r := range rows {
			for _, col := range cols {
				cellKey := fmt.Sprintf("table:%s:cell:%s:%s", targetTable, r, col)
				zsA, err := clientA.ZRangeWithScores(ctx, cellKey, 0, -1).Result()
				if err != nil {
					t.Fatalf("zrange A: %v", err)
				}
				zsB, err := clientB.ZRangeWithScores(ctx, cellKey, 0, -1).Result()
				if err != nil {
					t.Fatalf("zrange B: %v", err)
				}
				if len(zsA) != len(zsB) {
					t.Fatalf("cell %s:%s member count mismatch: A has %d, B has %d", r, col, len(zsA), len(zsB))
				}
				mapA := make(map[string]float64)
				for _, z := range zsA {
					mapA[z.Member.(string)] = z.Score
				}
				for _, z := range zsB {
					mid := z.Member.(string)
					expScore, ok := mapA[mid]
					if !ok {
						t.Fatalf("cell %s:%s B has unexpected member %s", r, col, mid)
					}
					if expScore != z.Score {
						t.Fatalf("cell %s:%s member %s score mismatch: A=%g, B=%g", r, col, mid, expScore, z.Score)
					}
				}
			}
		}

		rsA, err := ntable.ReadSetMembers(ctx, clientA, targetTable, membersPool)
		if err != nil {
			t.Fatalf("ReadSetMembers A: %v", err)
		}
		rsB, err := ntable.ReadSetMembers(ctx, clientB, targetTable, membersPool)
		if err != nil {
			t.Fatalf("ReadSetMembers B: %v", err)
		}
		if rsA.Revision != rsB.Revision {
			t.Fatalf("ReadSet revision mismatch: A=%d, B=%d", rsA.Revision, rsB.Revision)
		}
		for _, mid := range membersPool {
			if rsA.IsMissing(mid) {
				if !rsB.IsMissing(mid) {
					t.Fatalf("member %s missing in A but present in B", mid)
				}
			} else {
				if rsB.IsMissing(mid) {
					t.Fatalf("member %s present in A but missing in B", mid)
				}
				rmA, _ := rsA.Member(mid)
				rmB, _ := rsB.Member(mid)
				if rmA.Placed != rmB.Placed {
					t.Fatalf("member %s placed mismatch: A=%v, B=%v", mid, rmA.Placed, rmB.Placed)
				}
				if rmA.Placed {
					if rmA.Row != rmB.Row || rmA.Col != rmB.Col {
						t.Fatalf("member %s place mismatch: A=%s:%s, B=%s:%s", mid, rmA.Row, rmA.Col, rmB.Row, rmB.Col)
					}
					if rmA.Score != rmB.Score {
						t.Fatalf("member %s score mismatch: A=%g, B=%g", mid, rmA.Score, rmB.Score)
					}
				}
				if rmA.Revision != rmB.Revision {
					t.Fatalf("member %s revision mismatch: A=%d, B=%d", mid, rmA.Revision, rmB.Revision)
				}
				if len(rmA.Fields) != len(rmB.Fields) {
					t.Fatalf("member %s fields length mismatch: A=%v, B=%v", mid, rmA.Fields, rmB.Fields)
				}
				for fk, fv := range rmA.Fields {
					if rmB.Fields[fk] != fv {
						t.Fatalf("member %s field %s mismatch: A=%s, B=%s", mid, fk, fv, rmB.Fields[fk])
					}
				}
			}
		}

		for _, mid := range membersPool {
			hA, err := clientA.HGetAll(ctx, ntable.MemberKey(mid)).Result()
			if err != nil {
				t.Fatalf("HGetAll clientA %s: %v", mid, err)
			}
			hB, err := clientB.HGetAll(ctx, ntable.MemberKey(mid)).Result()
			if err != nil {
				t.Fatalf("HGetAll clientB %s: %v", mid, err)
			}
			if len(hA) != len(hB) {
				t.Fatalf("member %s hash len mismatch: A=%v, B=%v", mid, hA, hB)
			}
			for k, v := range hA {
				if hB[k] != v {
					t.Fatalf("member %s hash key %s mismatch: A=%q, B=%q", mid, k, v, hB[k])
				}
			}
		}
	}

	// Batch 1: Create members m1, m2, m3
	curRevA, _ := strconv.ParseUint(clientA.HGet(ctx, ntable.DefKey(targetTable)+":revision", "n").Val(), 10, 64)
	manifest1 := ntable.BatchManifest{
		Schema:                1,
		Table:                 targetTable,
		Epoch:                 "0",
		ExpectedTableRevision: strconv.FormatUint(curRevA, 10),
		OperationID:           "replay-op-1",
		Actor:                 "replay-tester",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Create: &ntable.MemberCreateOp{Row: "r1", Col: "a", Score: 10},
				Expect: &ntable.MemberExpect{Absent: true},
				Set:    map[string]string{"role": "worker", "tier": "silver"},
			},
			{
				ID:     "m2",
				Create: &ntable.MemberCreateOp{Row: "r1", Col: "b", Score: 20},
				Expect: &ntable.MemberExpect{Absent: true},
				Set:    map[string]string{"role": "lead", "tier": "gold"},
			},
			{
				ID:     "m3",
				Create: &ntable.MemberCreateOp{Row: "r2", Col: "c", Score: 30},
				Expect: &ntable.MemberExpect{Absent: true},
				Set:    map[string]string{"role": "staff"},
			},
		},
	}
	rcpt1, err := ntable.ApplyBatch(ctx, clientA, manifest1)
	if err != nil {
		t.Fatalf("batch 1 failed: %v", err)
	}
	if rcpt1.BatchDelta == nil {
		t.Fatalf("batch 1 missing BatchDelta")
	}
	replayDelta(rcpt1.BatchDelta, targetTable, rcpt1.After)
	verifyTablesMatch()

	// Batch 2: Move m1, modify fields of m2, move m3
	score15 := 15.0
	manifest2 := ntable.BatchManifest{
		Schema:                1,
		Table:                 targetTable,
		Epoch:                 "0",
		ExpectedTableRevision: strconv.FormatUint(rcpt1.After, 10),
		OperationID:           "replay-op-2",
		Actor:                 "replay-tester",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Move:   &ntable.MemberMoveOp{Row: "r2", Col: "b", Score: &score15},
				Expect: &ntable.MemberExpect{Revision: "1"},
				Set:    map[string]string{"tier": "platinum"},
				Unset:  []string{"role"},
			},
			{
				ID:     "m2",
				Expect: &ntable.MemberExpect{Revision: "1"},
				Set:    map[string]string{"tier": "diamond"},
				Unset:  []string{"role"},
			},
			{
				ID:     "m3",
				Move:   &ntable.MemberMoveOp{Row: "r1", Col: "a"},
				Expect: &ntable.MemberExpect{Revision: "1"},
			},
		},
	}
	rcpt2, err := ntable.ApplyBatch(ctx, clientA, manifest2)
	if err != nil {
		t.Fatalf("batch 2 failed: %v", err)
	}
	if rcpt2.BatchDelta == nil {
		t.Fatalf("batch 2 missing BatchDelta")
	}
	replayDelta(rcpt2.BatchDelta, targetTable, rcpt2.After)
	verifyTablesMatch()

	// Batch 3: Remove m1, move m2, create m4
	score50 := 50.0
	manifest3 := ntable.BatchManifest{
		Schema:                1,
		Table:                 targetTable,
		Epoch:                 "0",
		ExpectedTableRevision: strconv.FormatUint(rcpt2.After, 10),
		OperationID:           "replay-op-3",
		Actor:                 "replay-tester",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Remove: true,
				Expect: &ntable.MemberExpect{Revision: "2"},
			},
			{
				ID:     "m2",
				Move:   &ntable.MemberMoveOp{Row: "r2", Col: "a", Score: &score50},
				Expect: &ntable.MemberExpect{Revision: "2"},
			},
			{
				ID:     "m4",
				Create: &ntable.MemberCreateOp{Row: "r1", Col: "c", Score: 40},
				Expect: &ntable.MemberExpect{Absent: true},
				Set:    map[string]string{"role": "intern"},
			},
		},
	}
	rcpt3, err := ntable.ApplyBatch(ctx, clientA, manifest3)
	if err != nil {
		t.Fatalf("batch 3 failed: %v", err)
	}
	if rcpt3.BatchDelta == nil {
		t.Fatalf("batch 3 missing BatchDelta")
	}
	replayDelta(rcpt3.BatchDelta, targetTable, rcpt3.After)
	verifyTablesMatch()
}
