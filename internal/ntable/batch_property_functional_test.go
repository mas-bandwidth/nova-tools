//go:build functional

package ntable_test

import (
	"context"
	"errors"
	"fmt"
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

func TestBatchApplyPropertyAndReceiptReplay(t *testing.T) {
	t.Parallel()
	rapidKeepsTheTreeClean(t)
	_, c := live(t)
	ctx := context.Background()

	const tableName = "t_prop"
	rows := []string{"r1", "r2"}
	cols := []string{"a", "b", "c"}
	membersPool := []string{"m1", "m2", "m3", "m4"}
	fieldsPool := []string{"role", "tier"}
	valuesPool := []string{"worker", "lead", "gold", "silver"}

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

	rapid.Check(t, func(rt *rapid.T) {
		if err := c.FlushAll(ctx).Err(); err != nil {
			rt.Fatalf("flushall: %v", err)
		}

		def := propDefinition(tableName)
		if err := ntable.Create(ctx, c, def, time.Now()); err != nil {
			rt.Fatalf("create %s: %v", tableName, err)
		}
		for _, r := range rows {
			if _, err := ntable.RowAdd(ctx, c, tableName, r, ntable.RowSpec{}); err != nil {
				rt.Fatalf("rowAdd %s %s: %v", tableName, r, err)
			}
		}

		revStr, err := c.HGet(ctx, ntable.DefKey(tableName)+":revision", "n").Result()
		if err != nil {
			rt.Fatalf("get table revision: %v", err)
		}
		initRev, err := strconv.ParseUint(revStr, 10, 64)
		if err != nil {
			rt.Fatalf("parse initial revision: %v", err)
		}

		oracle := newBatchOracle(tableName, rows, cols, initRev)
		stepCount := rapid.IntRange(8, 16).Draw(rt, "steps")

		for step := 0; step < stepCount; step++ {
			opID := fmt.Sprintf("op-step-%d-%d", step, rapid.IntRange(1000, 999999).Draw(rt, "op_rand"))

			expectedRev := strconv.FormatUint(oracle.table.revision, 10)
			if rapid.IntRange(0, 7).Draw(rt, "stale_table_rev") == 0 {
				expectedRev = strconv.FormatUint(oracle.table.revision+10, 10)
			}

			targetTable := tableName
			if rapid.IntRange(0, 19).Draw(rt, "bad_table") == 0 {
				targetTable = "nonexistent_table"
			}

			numMembers := rapid.IntRange(1, 3).Draw(rt, "num_members")
			batchMembers := make([]ntable.BatchMemberEntry, numMembers)

			hasDup := numMembers > 1 && rapid.IntRange(0, 7).Draw(rt, "has_duplicate") == 0
			var firstID string

			for i := 0; i < numMembers; i++ {
				var mID string
				if i > 0 && hasDup && firstID != "" {
					mID = firstID
				} else {
					mID = rapid.SampledFrom(membersPool).Draw(rt, fmt.Sprintf("member_id_%d", i))
					if i == 0 {
						firstID = mID
					}
				}

				memState := oracle.members[mID]
				isPlaced := memState != nil && memState.placed

				entry := ntable.BatchMemberEntry{ID: mID}

				if !isPlaced {
					opChoice := rapid.IntRange(0, 3).Draw(rt, fmt.Sprintf("unplaced_op_%d", i))
					switch opChoice {
					case 0, 1:
						targetRow := rapid.SampledFrom(rows).Draw(rt, "create_row")
						if rapid.IntRange(0, 19).Draw(rt, "bad_row") == 0 {
							targetRow = "bad_row"
						}
						targetCol := rapid.SampledFrom(cols).Draw(rt, "create_col")
						score := float64(rapid.IntRange(1, 100).Draw(rt, "create_score"))

						entry.Create = &ntable.MemberCreateOp{
							Row:   targetRow,
							Col:   targetCol,
							Score: score,
						}
						if rapid.IntRange(0, 7).Draw(rt, "bad_expect_create") == 0 {
							entry.Expect = &ntable.MemberExpect{Absent: false, Revision: "1"}
						} else {
							entry.Expect = &ntable.MemberExpect{Absent: true}
						}
						if rapid.Bool().Draw(rt, "create_set_field") {
							f := rapid.SampledFrom(fieldsPool).Draw(rt, "field_k")
							v := rapid.SampledFrom(valuesPool).Draw(rt, "field_v")
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
					opChoice := rapid.IntRange(0, 4).Draw(rt, fmt.Sprintf("placed_op_%d", i))
					switch opChoice {
					case 0:
						dstRow := rapid.SampledFrom(rows).Draw(rt, "move_row")
						dstCol := rapid.SampledFrom(cols).Draw(rt, "move_col")
						var newScore *float64
						if rapid.Bool().Draw(rt, "new_score") {
							sc := float64(rapid.IntRange(1, 100).Draw(rt, "move_score"))
							newScore = &sc
						}
						entry.Move = &ntable.MemberMoveOp{
							Row:   dstRow,
							Col:   dstCol,
							Score: newScore,
						}
						expRev := strconv.FormatUint(memState.revision, 10)
						if rapid.IntRange(0, 7).Draw(rt, "stale_mem_rev") == 0 {
							expRev = strconv.FormatUint(memState.revision+5, 10)
						}
						expPlace := &ntable.PlaceExpect{Row: memState.row, Col: memState.col}
						if rapid.IntRange(0, 7).Draw(rt, "drift_place") == 0 {
							expPlace = &ntable.PlaceExpect{Row: "bad_r", Col: "bad_c"}
						}
						entry.Expect = &ntable.MemberExpect{
							Revision: expRev,
							Place:    expPlace,
						}
					case 1:
						entry.Remove = true
						expRev := strconv.FormatUint(memState.revision, 10)
						if rapid.IntRange(0, 7).Draw(rt, "stale_rem_rev") == 0 {
							expRev = strconv.FormatUint(memState.revision+5, 10)
						}
						entry.Expect = &ntable.MemberExpect{Revision: expRev}
					case 2:
						f := rapid.SampledFrom(fieldsPool).Draw(rt, "set_f")
						v := rapid.SampledFrom(valuesPool).Draw(rt, "set_v")
						entry.Set = map[string]string{f: v}

						if len(memState.fields) > 0 && rapid.Bool().Draw(rt, "guard_field") {
							var existingF, existingV string
							for ef, ev := range memState.fields {
								existingF, existingV = ef, ev
								break
							}
							if rapid.IntRange(0, 3).Draw(rt, "guard_bad") == 0 {
								wrong := existingV + "_wrong"
								entry.Expect = &ntable.MemberExpect{
									Fields: map[string]ntable.FieldGuard{
										existingF: {Equals: &wrong},
									},
								}
							} else {
								entry.Expect = &ntable.MemberExpect{
									Fields: map[string]ntable.FieldGuard{
										existingF: {Equals: &existingV},
									},
								}
							}
						} else {
							entry.Expect = &ntable.MemberExpect{Revision: strconv.FormatUint(memState.revision, 10)}
						}
					case 3:
						entry.Create = &ntable.MemberCreateOp{Row: "r1", Col: "a", Score: 10}
						entry.Expect = &ntable.MemberExpect{Absent: true}
					case 4:
						// Guard only: no mutations, outcome can be noop
						entry.Expect = &ntable.MemberExpect{
							Revision: strconv.FormatUint(memState.revision, 10),
							Place:    &ntable.PlaceExpect{Row: memState.row, Col: memState.col},
						}
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
				rt.Fatalf("dumpStore before: %v", err)
			}

			rcpt, err := ntable.ApplyBatch(ctx, c, manifest)

			if !wantVerdict.ok() {
				tally.refused++
				if err == nil {
					rt.Fatalf("step %d: wanted refusal %s, got success receipt %+v", step, wantVerdict, rcpt)
				}
				if !strings.Contains(err.Error(), "changed=no") {
					rt.Fatalf("step %d: refusal missing changed=no: %v", step, err)
				}
				afterSnap, err := dumpStore(ctx, c)
				if err != nil {
					rt.Fatalf("dumpStore after: %v", err)
				}
				if diff := diffSnapshots(beforeSnap, afterSnap); diff != "" {
					rt.Fatalf("step %d: refused batch changed store state: %s (err: %v)", step, diff, err)
				}
			} else {
				tally.accepted++
				if err != nil {
					rt.Fatalf("step %d: expected success, got error: %v", step, err)
				}

				wantOutcome := "noop"
				if willChange {
					wantOutcome = "changed"
				}
				if rcpt.Outcome != wantOutcome {
					rt.Fatalf("step %d: expected outcome %q, got %q", step, wantOutcome, rcpt.Outcome)
				}
				if rcpt.Before != oracle.table.revision {
					rt.Fatalf("step %d: expected rev before %d, got %d", step, oracle.table.revision, rcpt.Before)
				}
				if rcpt.After != oracle.table.revision+1 {
					rt.Fatalf("step %d: expected rev after %d, got %d", step, oracle.table.revision+1, rcpt.After)
				}

				oracle.apply(manifest)

				rs, err := ntable.ReadSetMembers(ctx, c, oracle.table.name, membersPool)
				if err != nil {
					rt.Fatalf("step %d: ReadSetMembers: %v", step, err)
				}
				if rs.Revision != oracle.table.revision {
					rt.Fatalf("step %d: ReadSet revision %d != oracle %d", step, rs.Revision, oracle.table.revision)
				}
				for _, mid := range membersPool {
					mState := oracle.members[mid]
					if mState == nil || !mState.exists {
						if !rs.IsMissing(mid) {
							rt.Fatalf("step %d: member %s should be missing, but found in ReadSet", step, mid)
						}
					} else {
						rm, found := rs.Member(mid)
						if !found {
							rt.Fatalf("step %d: member %s should exist in ReadSet, but not found", step, mid)
						}
						if rm.Placed != mState.placed {
							rt.Fatalf("step %d: member %s placed=%v, oracle placed=%v", step, mid, rm.Placed, mState.placed)
						}
						if mState.placed {
							if rm.Row != mState.row || rm.Col != mState.col {
								rt.Fatalf("step %d: member %s place=%s:%s, oracle=%s:%s", step, mid, rm.Row, rm.Col, mState.row, mState.col)
							}
							if rm.Score != mState.score {
								rt.Fatalf("step %d: member %s score=%g, oracle=%g", step, mid, rm.Score, mState.score)
							}
						}
						if rm.Revision != mState.revision {
							rt.Fatalf("step %d: member %s revision=%d, oracle=%d", step, mid, rm.Revision, mState.revision)
						}
						for fk, fv := range mState.fields {
							if rm.Fields[fk] != fv {
								rt.Fatalf("step %d: member %s field %s=%q, oracle=%q", step, mid, fk, rm.Fields[fk], fv)
							}
						}
					}
				}

				// Receipt Replay 1: Exact replay
				tally.replays++
				replaySnapBefore, err := dumpStore(ctx, c)
				if err != nil {
					rt.Fatalf("replay dumpStore before: %v", err)
				}
				replayRcpt, err := ntable.ApplyBatch(ctx, c, manifest)
				if err != nil {
					rt.Fatalf("step %d: exact replay failed: %v", step, err)
				}
				if replayRcpt.ID != rcpt.ID || replayRcpt.Outcome != rcpt.Outcome ||
					replayRcpt.Before != rcpt.Before || replayRcpt.After != rcpt.After {
					rt.Fatalf("step %d: replay receipt mismatch: got %+v, original %+v", step, replayRcpt, rcpt)
				}
				replaySnapAfter, err := dumpStore(ctx, c)
				if err != nil {
					rt.Fatalf("replay dumpStore after: %v", err)
				}
				if diff := diffSnapshots(replaySnapBefore, replaySnapAfter); diff != "" {
					rt.Fatalf("step %d: exact replay mutated store: %s", step, diff)
				}

				// Receipt Replay 2: Conflicting payload with identical operation ID
				tally.conflicts++
				conflictManifest := manifest
				conflictManifest.Actor = manifest.Actor + "-conflict"
				conflictSnapBefore, err := dumpStore(ctx, c)
				if err != nil {
					rt.Fatalf("conflict dumpStore before: %v", err)
				}
				_, conflictErr := ntable.ApplyBatch(ctx, c, conflictManifest)
				if conflictErr == nil {
					rt.Fatalf("step %d: conflicting replay unexpectedly succeeded", step)
				}
				if !errors.Is(conflictErr, ntable.ErrOpConflict) {
					rt.Fatalf("step %d: expected ErrOpConflict, got: %v", step, conflictErr)
				}
				if !strings.Contains(conflictErr.Error(), "changed=no") {
					rt.Fatalf("step %d: conflict error missing changed=no: %v", step, conflictErr)
				}
				conflictSnapAfter, err := dumpStore(ctx, c)
				if err != nil {
					rt.Fatalf("conflict dumpStore after: %v", err)
				}
				if diff := diffSnapshots(conflictSnapBefore, conflictSnapAfter); diff != "" {
					rt.Fatalf("step %d: conflicting replay mutated store: %s", step, diff)
				}
			}
		}
	})
}
