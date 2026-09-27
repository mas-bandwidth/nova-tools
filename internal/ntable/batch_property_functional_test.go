//go:build functional

package ntable_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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
