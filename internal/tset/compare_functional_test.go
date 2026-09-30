//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// This is deliberately an L1-only gate. The standalone fixture has no L2 log
// planner; a composed run needs its own gate once that planner is installed.
const (
	compareTable = "cards"
	compareIDs   = 32
	stepsPerSeed = 5000
	// A counterexample must reach the test output even when replaying it is
	// expensive. These are deterministic ceilings on reduction work per seed.
	maxShrinkAttempts    = 24
	maxShrinkReplaySteps = 4096
)

type compareAction struct {
	Step       Step   `json:"step"`
	Label      string `json:"label"`
	WantCode   string `json:"want_code,omitempty"`
	WantNoop   bool   `json:"want_noop,omitempty"`
	CheckImage bool   `json:"check_image,omitempty"`
}

type compareFailure struct {
	Index  int
	Kind   string
	Detail string
}

type compareHarness struct {
	t         *testing.T
	fx        *tsetFixture
	mem       *Mem
	rdb       Store
	beforeMem MemSnapshot
	beforeLua MemSnapshot
}

func TestL1OnlyMemLuaTenThousandRandomSteps(t *testing.T) {
	t.Parallel()
	for _, seed := range []int64{20260929, 31415926} {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seed))
			h := newCompareHarness(t)
			trace := make([]compareAction, 0, stepsPerSeed)
			coverage := make(map[string]int)
			var lastNamed *compareAction
			for i := 0; i < stepsPerSeed; i++ {
				before, err := h.mem.Snapshot(h.fx.Space)
				if err != nil {
					t.Fatalf("seed=%d step=%d model snapshot: %v", seed, i, err)
				}
				action := nextCompareAction(rng, before, h.fx.Space, seed, i, lastNamed)
				action.CheckImage = action.WantCode != "" && i%101 == 0
				trace = append(trace, action)
				coverage[action.Label]++
				if failure := h.apply(i, action); failure != nil {
					original := *failure
					wantKind := failure.Kind
					failingAction := trace[failure.Index]
					encodedAction, encodeErr := json.Marshal(failingAction)
					if encodeErr != nil {
						encodedAction = []byte(fmt.Sprintf("%+v", failingAction))
					}
					// testing.T holds Log output until the subtest finishes. A
					// timeout during reduction must not conceal this original
					// mismatch, so write it to stderr before any replay/reset.
					_, _ = fmt.Fprintf(os.Stderr, "L1_COMPARE_ORIGINAL seed=%d step=%d kind=%q action=%s detail=%q\n",
						seed, original.Index, original.Kind, encodedAction, original.Detail)
					if len(trace) > maxShrinkReplaySteps {
						t.Fatalf("L1-only Mem/Lua mismatch: seed=%d step=%d kind=%s detail=%s\nreduction skipped: original %d actions exceed %d replay-step budget\noriginal failing sequence:\n%s",
							seed, original.Index, original.Kind, original.Detail, len(trace), maxShrinkReplaySteps, compareActionTrace(trace))
					}
					attempts, replaySteps := 0, 0
					budgetExhausted, reproducedOriginal := false, false
					reproduces := func(candidate []compareAction) bool {
						if attempts >= maxShrinkAttempts || replaySteps+len(candidate) > maxShrinkReplaySteps {
							budgetExhausted = true
							return false
						}
						attempts++
						replaySteps += len(candidate) // upper bound even if replay fails early
						h.reset()
						for j, item := range candidate {
							if got := h.apply(j, item); got != nil {
								// Keep the original failing action in the reduced
								// trace; another action's reply mismatch is a
								// different failure even if its broad kind agrees.
								matches := got.Kind == wantKind && reflect.DeepEqual(item, failingAction)
								if attempts == 1 {
									reproducedOriginal = matches
								}
								return matches
							}
						}
						return false
					}
					short, global := shrinkFailingSequence(trace, reproduces)
					if !reproducedOriginal {
						t.Fatalf("L1-only Mem/Lua mismatch did not reproduce: seed=%d step=%d kind=%s detail=%s\noriginal trace:\n%s",
							seed, original.Index, original.Kind, original.Detail, compareActionTrace(trace))
					}
					label := "one-minimal by deletion; global shortest not proved"
					if budgetExhausted {
						label = "bounded failing subsequence; minimality not proved"
					} else if global {
						label = "globally shortest subsequence"
					}
					t.Fatalf("L1-only Mem/Lua mismatch: seed=%d step=%d kind=%s detail=%s\n%s (%d of %d actions; %d replay attempts, at most %d replayed actions):\n%s",
						seed, original.Index, original.Kind, original.Detail, label, len(short), len(trace), attempts, replaySteps, compareActionTrace(short))
				}
				if action.Step.Op != nil && action.Label != "replay" {
					copy := action
					lastNamed = &copy
				}
			}
			for _, label := range []string{
				"create", "move and fields", "same-cell no-op", "stale revision",
				"remove with fields", "cell count guard", "score range guard",
				"remove final occupant and row", "advance and restore rows", "replay",
			} {
				if coverage[label] == 0 {
					t.Fatalf("seed=%d omitted required action class %q; coverage=%v", seed, label, coverage)
				}
			}
			t.Logf("L1-only Mem/Lua parity: seed=%d steps=%d coverage=%v", seed, stepsPerSeed, coverage)
		})
	}
}

func newCompareHarness(t *testing.T) *compareHarness {
	t.Helper()
	fx := newTSetFixture(t)
	h := &compareHarness{t: t, fx: fx, rdb: NewRedis(fx.Client)}
	h.configure()
	return h
}

func (h *compareHarness) configure() {
	h.t.Helper()
	h.fx.Define(h.t, compareTable, "ready", "busy")
	h.fx.Activate(h.t)
	h.mem = NewMem()
	err := h.mem.DefineTable(h.fx.Space, compareTable, TableDefinition{
		Columns: []string{"ready", "busy"}, MemberPrefix: h.fx.Space + "member:" + compareTable + ":",
		EpochKey: h.fx.Space + "sprint:epoch", EpochField: "n",
	})
	if err != nil {
		h.t.Fatalf("configure Mem: %v", err)
	}
	h.beforeMem, err = h.mem.Snapshot(h.fx.Space)
	if err != nil {
		h.t.Fatalf("initial model snapshot: %v", err)
	}
	h.beforeLua = h.fx.SemanticSnapshot(h.t)
}

func (h *compareHarness) reset() {
	h.t.Helper()
	h.fx.Reset(h.t)
	h.configure()
}

func (h *compareHarness) apply(index int, action compareAction) *compareFailure {
	h.t.Helper()
	space := h.fx.Space
	beforeMem, beforeLua := h.beforeMem, h.beforeLua
	if !reflect.DeepEqual(beforeMem, beforeLua) {
		return &compareFailure{index, "state", compareJSON("before", beforeMem, beforeLua)}
	}
	// Periodic whole-key images supplement the complete semantic snapshot.
	// Every refusal, including an unexpected one, checks the latter for writes.
	var beforeImage any
	if action.CheckImage {
		beforeImage = commitProbeImage(h.t, h.fx.Client)
	}
	memReply, memErr := h.mem.Step(context.Background(), action.Step)
	luaReply, luaErr := h.rdb.Step(context.Background(), action.Step)
	memRef, memIsRef := compareRefusal(memErr)
	luaRef, luaIsRef := compareRefusal(luaErr)
	if (memErr != nil && !memIsRef) || (luaErr != nil && !luaIsRef) {
		return &compareFailure{index, "transport", fmt.Sprintf("action=%s mem=%v lua=%v", action.Label, memErr, luaErr)}
	}
	if memIsRef != luaIsRef {
		return &compareFailure{index, "reply", fmt.Sprintf("action=%s mem=%v lua=%v", action.Label, memErr, luaErr)}
	}
	if memIsRef {
		if !reflect.DeepEqual(memRef, luaRef) {
			return &compareFailure{index, "reply", compareJSON(action.Label, memRef, luaRef)}
		}
		if action.WantCode == "" {
			return &compareFailure{index, "unexpected-refusal", fmt.Sprintf("action=%s code=%s; wanted success", action.Label, memRef.Code)}
		}
		if action.WantCode != "" && (memRef.Code != action.WantCode || luaRef.Code != action.WantCode) {
			return &compareFailure{index, "expected-code", fmt.Sprintf("action=%s want=%s mem=%s lua=%s", action.Label, action.WantCode, memRef.Code, luaRef.Code)}
		}
	} else {
		memReply.Counters, luaReply.Counters = nil, nil // implementation work counts differ; outcomes must not.
		if !reflect.DeepEqual(memReply, luaReply) {
			return &compareFailure{index, "reply", compareJSON(action.Label, memReply, luaReply)}
		}
		if action.WantCode != "" {
			return &compareFailure{index, "expected-code", fmt.Sprintf("action=%s succeeded; wanted %s", action.Label, action.WantCode)}
		}
		if action.WantNoop && action.Label != "replay" && memReply.Changed != 0 {
			return &compareFailure{index, "no-op-reply", fmt.Sprintf("action=%s changed=%d; wanted zero", action.Label, memReply.Changed)}
		}
	}
	afterMem, err := h.mem.Snapshot(space)
	if err != nil {
		return &compareFailure{index, "harness", fmt.Sprintf("model after: %v", err)}
	}
	afterLua := h.fx.SemanticSnapshot(h.t)
	if !reflect.DeepEqual(afterMem, afterLua) {
		return &compareFailure{index, "state", compareJSON(action.Label, afterMem, afterLua)}
	}
	if memIsRef || action.WantNoop {
		if !reflect.DeepEqual(beforeMem, afterMem) || !reflect.DeepEqual(beforeLua, afterLua) {
			return &compareFailure{index, "unchanged", fmt.Sprintf("action=%s refusal=%v no-op=%v\n%s", action.Label, memIsRef, action.WantNoop, compareJSON("state", beforeLua, afterLua))}
		}
	}
	if beforeImage != nil && !reflect.DeepEqual(beforeImage, commitProbeImage(h.t, h.fx.Client)) {
		return &compareFailure{index, "raw-unchanged", fmt.Sprintf("action=%s code=%s changed Redis key image", action.Label, action.WantCode)}
	}
	h.beforeMem, h.beforeLua = afterMem, afterLua
	return nil
}

func compareRefusal(err error) (*Refusal, bool) {
	if err == nil {
		return nil, false
	}
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		return nil, false
	}
	// The prose can vary between engines. Empty detail arrays have one JSON
	// representation whether a Go producer holds nil or an allocated slice.
	// Normalize only that representation difference; retain every named field,
	// including active_epoch, IDs, rows, cells and optional index presence.
	return &Refusal{Status: refusal.Status, Code: refusal.Code,
		Detail: comparableDetail(refusal.Detail)}, true
}

func compareJSON(label string, left, right any) string {
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return fmt.Sprintf("%s mem=%s lua=%s", label, a, b)
}

func nextCompareAction(r *rand.Rand, snapshot MemSnapshot, space string, seed int64, index int, replay *compareAction) compareAction {
	epoch := snapshot.ActiveEpoch
	base := Step{Epoch: epoch, Space: space}
	// Every generated request keeps its original space and epoch in the trace,
	// so a shrink replay cannot silently rewrite a stale request.
	if index == 0 {
		base.Entries = []Entry{{Kind: "rows", Table: compareTable, Add: []string{"r0", "r1"}}}
		return compareAction{Step: base, Label: "seed rows"}
	}
	if index == 1 {
		base.Entries = []Entry{{Kind: "create", Table: compareTable, To: "r0:ready", IDs: []string{"e0m00", "e0m01"}, Scores: []string{"0", "1"}, Set: map[string]string{"status": "open", "empty": ""}}}
		return compareAction{Step: base, Label: "seed members"}
	}
	if index == 2 {
		base.Entries = []Entry{{Kind: "rows", Table: compareTable, Add: []string{"r2"}}, {Kind: "create", Table: compareTable, To: "r2:busy", IDs: []string{"e0m02"}, Scores: []string{"2"}}}
		return compareAction{Step: base, Label: "add row and create"}
	}
	if index == 3 {
		base.Entries = []Entry{{Kind: "rows", Table: compareTable, Del: []string{"r2"}}, {Kind: "remove", Table: compareTable, From: "r2:busy", IDs: []string{"e0m02"}}}
		return compareAction{Step: base, Label: "remove final occupant and row"}
	}
	if index == stepsPerSeed/2 {
		base.Entries = []Entry{{Kind: "advance", AdvanceFrom: epoch}, {Kind: "rows", Table: compareTable, Add: []string{"r0", "r1"}}}
		name := fmt.Sprintf("advance-%d-%d", seed, index)
		base.Op, base.Intent = &name, &name
		return compareAction{Step: base, Label: "advance and restore rows"}
	}
	if replay != nil && r.Intn(29) == 0 {
		copy := *replay
		copy.Label = "replay"
		copy.WantCode = ""
		copy.WantNoop = true
		return copy
	}
	table := snapshot.Epochs[epoch].Tables[compareTable]
	rows := sortedRowNames(table.Rows)
	placed := placedRecords(table)
	if len(rows) == 0 {
		base.Entries = []Entry{{Kind: "rows", Table: compareTable, Add: []string{"r0"}}}
		return compareAction{Step: base, Label: "restore a row"}
	}
	var action compareAction
	switch r.Intn(12) {
	case 0, 1:
		id, ok := unusedCompareID(table, epoch, r.Intn(compareIDs))
		if !ok {
			id = fmt.Sprintf("e%sm%02d", epoch, r.Intn(compareIDs))
			base.Entries = []Entry{{Kind: "create", Table: compareTable, To: rows[r.Intn(len(rows))] + ":ready", IDs: []string{id}, Scores: []string{"1"}}}
			action = compareAction{Step: base, Label: "create existing", WantCode: "EXISTS"}
			break
		}
		base.Entries = []Entry{{Kind: "create", Table: compareTable, To: rows[r.Intn(len(rows))] + ":ready", IDs: []string{id}, Scores: []string{[]string{"0", "1", "1.0", "-0", "2.5"}[r.Intn(5)]}, Set: map[string]string{"status": "open", "empty": ""}}}
		action = compareAction{Step: base, Label: "create"}
	case 2, 3, 4:
		if len(placed) == 0 {
			base.Entries = []Entry{{Kind: "rows", Table: compareTable, Add: []string{"r0"}}}
			return compareAction{Step: base, Label: "keep row available"}
		}
		member := placed[r.Intn(len(placed))]
		from := member.rec.Row + ":" + member.rec.Column
		to := rows[r.Intn(len(rows))] + ":" + []string{"ready", "busy"}[r.Intn(2)]
		e := Entry{Kind: "move", Table: compareTable, From: from, To: to, IDs: []string{member.id}}
		if r.Intn(2) == 0 {
			e.Scores = []string{[]string{"0", "1", "1.0", "2.5", "10"}[r.Intn(5)]}
		}
		if r.Intn(3) == 0 {
			e.Set = map[string]string{"status": []string{"open", "waiting", "done"}[r.Intn(3)]}
		}
		if r.Intn(5) == 0 {
			e.Each = []map[string]string{{"status": "override", "empty": ""}}
		}
		// FIELDOVERLAP is tested separately. A generated valid move may
		// unset empty only when its per-ID overlay does not also set it.
		if r.Intn(7) == 0 && e.Each == nil {
			e.Unset = []string{"empty"}
		}
		base.Entries = []Entry{e}
		action = compareAction{Step: base, Label: "move and fields"}
	case 5:
		if len(placed) == 0 {
			base.Entries = []Entry{{Kind: "guard", Table: compareTable, From: rows[0] + ":ready", IDs: []string{"never-created"}}}
			action = compareAction{Step: base, Label: "missing guard", WantCode: "MISSING"}
			break
		}
		member := placed[r.Intn(len(placed))]
		base.Entries = []Entry{{Kind: "move", Table: compareTable, From: member.rec.Row + ":" + member.rec.Column, To: member.rec.Row + ":" + member.rec.Column, IDs: []string{member.id}, Scores: []string{member.rec.Score}}}
		action = compareAction{Step: base, Label: "same-cell no-op", WantNoop: true}
	case 6:
		if len(placed) == 0 {
			base.Entries = []Entry{{Kind: "guard", Table: compareTable, From: rows[0] + ":ready", IDs: []string{"never-created"}}}
			action = compareAction{Step: base, Label: "missing guard", WantCode: "MISSING"}
			break
		}
		member := placed[r.Intn(len(placed))]
		base.Entries = []Entry{{Kind: "guard", Table: compareTable, From: member.rec.Row + ":" + member.rec.Column, IDs: []string{member.id}, Revs: []Decimal{"0"}}}
		action = compareAction{Step: base, Label: "stale revision", WantCode: "REVISION"}
	case 7:
		if len(placed) == 0 {
			base.Entries = []Entry{{Kind: "rows", Table: compareTable, Add: []string{"r1"}}}
			action = compareAction{Step: base, Label: "row add"}
			break
		}
		member := placed[r.Intn(len(placed))]
		base.Entries = []Entry{{Kind: "remove", Table: compareTable, From: member.rec.Row + ":" + member.rec.Column, IDs: []string{member.id}, Set: map[string]string{"status": "removed"}}}
		action = compareAction{Step: base, Label: "remove with fields"}
	case 8:
		row := []string{"r0", "r1", "r2", "r3"}[r.Intn(4)]
		base.Entries = []Entry{{Kind: "rows", Table: compareTable, Add: []string{row}}}
		action = compareAction{Step: base, Label: "row add or duplicate"}
	case 9:
		row := rows[r.Intn(len(rows))]
		base.Entries = []Entry{{Kind: "rows", Table: compareTable, Del: []string{row}}}
		occupied := rowOccupants(table, row)
		if len(occupied) > 0 && r.Intn(2) == 0 {
			for _, col := range []string{"ready", "busy"} {
				if ids := occupied[col]; len(ids) > 0 {
					base.Entries = append(base.Entries, Entry{Kind: "remove", Table: compareTable, From: row + ":" + col, IDs: ids})
				}
			}
			action = compareAction{Step: base, Label: "delete after final occupants removed"}
		} else if len(occupied) > 0 {
			action = compareAction{Step: base, Label: "delete occupied row", WantCode: "OCCUPIED"}
		} else {
			action = compareAction{Step: base, Label: "delete empty row"}
		}
	case 10:
		row := rows[r.Intn(len(rows))]
		col := []string{"ready", "busy"}[r.Intn(2)]
		count := uint64(len(table.Cells[row][col]))
		max := count
		if count > 0 && r.Intn(2) == 0 {
			max = 0
		}
		base.Entries = []Entry{{Kind: "count", Table: compareTable, Cells: []string{row + ":" + col}, CountMax: []uint64{max}}}
		action = compareAction{Step: base, Label: "cell count guard", WantNoop: max >= count}
		if max < count {
			action.WantCode = "CELLFULL"
		}
	case 11:
		row := rows[r.Intn(len(rows))]
		col := []string{"ready", "busy"}[r.Intn(2)]
		count := uint64(len(table.Cells[row][col]))
		atMost := count
		if count > 0 && r.Intn(2) == 0 {
			atMost = 0
		}
		base.Entries = []Entry{{Kind: "rcount", Table: compareTable, Cells: []string{row + ":" + col}, ScoreMin: "-inf", ScoreMax: "+inf", AtMost: &atMost}}
		action = compareAction{Step: base, Label: "score range guard", WantNoop: atMost >= count}
		if atMost < count {
			action.WantCode = "RANGECOUNT"
		}
	}
	if action.Step.Space == "" {
		action.Step = base
	}
	if index%67 == 0 && action.WantCode == "" && !action.WantNoop {
		name := fmt.Sprintf("op-%d-%d", seed, index)
		intent := fmt.Sprintf("random-step/%d/%d/%s", seed, index, action.Label)
		action.Step.Op, action.Step.Intent = &name, &intent
		action.Step.Result = fmt.Sprintf("result-%d", index)
	}
	return action
}

type placedCompareRecord struct {
	id  string
	rec MemRecord
}

func placedRecords(table MemTableSnapshot) []placedCompareRecord {
	ids := make([]string, 0, len(table.Records))
	for id, rec := range table.Records {
		if rec.Row != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]placedCompareRecord, 0, len(ids))
	for _, id := range ids {
		out = append(out, placedCompareRecord{id, table.Records[id]})
	}
	return out
}

func sortedRowNames(rows map[string]Decimal) []string {
	out := make([]string, 0, len(rows))
	for row := range rows {
		out = append(out, row)
	}
	sort.Strings(out)
	return out
}

func unusedCompareID(table MemTableSnapshot, epoch Decimal, start int) (string, bool) {
	for i := 0; i < compareIDs; i++ {
		id := fmt.Sprintf("e%sm%02d", epoch, (start+i)%compareIDs)
		if _, exists := table.Records[id]; !exists {
			return id, true
		}
	}
	return "", false
}

func rowOccupants(table MemTableSnapshot, row string) map[string][]string {
	out := make(map[string][]string)
	for id, rec := range table.Records {
		if rec.Row == row {
			out[rec.Column] = append(out[rec.Column], id)
		}
	}
	for col := range out {
		sort.Strings(out[col])
	}
	return out
}

// A deterministic, human-readable action dump is useful with the seed when
// a replay from CI needs to be reproduced locally.
func compareActionTrace(actions []compareAction) string {
	var b strings.Builder
	for i, action := range actions {
		encoded, _ := json.Marshal(action)
		fmt.Fprintf(&b, "%d %s\n", i, encoded)
	}
	return b.String()
}
