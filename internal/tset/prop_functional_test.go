//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Table properties on the Lua writer and reader beside the Mem twin (L1
// contract amendment 2026-09-30, property, section 5). Every step goes to
// both; replies, refusal details and complete semantic snapshots must agree,
// and every read answer and its logical read work must agree.

// propBoth applies one step to the twin and the store and returns the twin's
// reply (with its plan observation) or the shared refusal.
func propBoth(t *testing.T, h *compareHarness, step Step) (Reply, *Refusal) {
	t.Helper()
	ctx := context.Background()
	memReply, memErr := h.mem.Step(ctx, step)
	luaReply, luaErr := h.rdb.Step(ctx, step)
	memRef, memIsRef := compareRefusal(memErr)
	luaRef, luaIsRef := compareRefusal(luaErr)
	if memErr != nil && !memIsRef || luaErr != nil && !luaIsRef || memIsRef != luaIsRef {
		t.Fatalf("outcomes differ: Mem=%v Lua=%v", memErr, luaErr)
	}
	if memIsRef {
		if !reflect.DeepEqual(memRef, luaRef) {
			t.Fatal(compareJSON("refusal", memRef, luaRef))
		}
	} else {
		mem, lua := memReply, luaReply
		mem.MemPlan, mem.Counters, lua.Counters = nil, nil, nil
		if !reflect.DeepEqual(mem, lua) {
			t.Fatal(compareJSON("reply", mem, lua))
		}
	}
	afterMem, err := h.mem.Snapshot(h.fx.Space)
	if err != nil {
		t.Fatal(err)
	}
	afterLua := h.fx.SemanticSnapshot(t)
	if !reflect.DeepEqual(afterMem, afterLua) {
		t.Fatal(compareJSON("state", afterMem, afterLua))
	}
	h.beforeMem, h.beforeLua = afterMem, afterLua
	return memReply, memRef
}

// propRawLua sends a step to the Lua writer without Go client validation, so
// a static refusal is the writer's own, and compares it with the twin's.
func propRawLua(t *testing.T, h *compareHarness, step Step, code string) (*Refusal, string) {
	t.Helper()
	_, memErr := h.mem.Step(context.Background(), step)
	memRef := requireRefusal(t, memErr, code)
	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := h.fx.Step(string(raw))
	if err != nil {
		t.Fatalf("Lua %s: %v", code, err)
	}
	text, ok := wire.(string)
	if !ok {
		t.Fatalf("Lua reply type %T", wire)
	}
	var luaRef Refusal
	if err := json.Unmarshal([]byte(text), &luaRef); err != nil {
		t.Fatal(err)
	}
	requireRefusal(t, &luaRef, code)
	if md, ld := comparableDetail(memRef.Detail), comparableDetail(luaRef.Detail); !reflect.DeepEqual(md, ld) {
		t.Fatalf("%s detail: Mem=%+v Lua=%+v", code, md, ld)
	}
	return memRef, text
}

func propRead(t *testing.T, h *compareHarness, epoch Decimal, names []string) map[string]string {
	t.Helper()
	plan := ReadPlan{Epoch: epoch, Space: h.fx.Space, Queries: []ReadQuery{{Kind: "props", Table: compareTable, Names: names}}}
	model, me := h.mem.Read(context.Background(), plan)
	server, se := h.rdb.Read(context.Background(), plan)
	if me != nil || se != nil {
		t.Fatalf("props read: Mem=%v Lua=%v", me, se)
	}
	differentialReadAnswers(t, model, server)
	return model.Answers[0].Props
}

func propUnchanged(t *testing.T, h *compareHarness, step Step, code string) *Refusal {
	t.Helper()
	before, image := h.beforeMem, commitProbeImage(t, h.fx.Client)
	_, refusal := propBoth(t, h, step)
	if refusal == nil || refusal.Code != code {
		t.Fatalf("refusal = %+v, want %s", refusal, code)
	}
	if !reflect.DeepEqual(before, h.beforeMem) {
		t.Fatalf("%s changed state", code)
	}
	if !reflect.DeepEqual(image, commitProbeImage(t, h.fx.Client)) {
		t.Fatalf("%s changed the Redis key image", code)
	}
	return refusal
}

func cardsProp(name, value string) Entry {
	return Entry{Kind: "prop", Table: compareTable, Name: name, Value: propValue(value)}
}

func cardsPropGuard(name string, value *string) Entry {
	return Entry{Kind: "propguard", Table: compareTable, Name: name, Value: value}
}

func cardsStep(h *compareHarness, entries ...Entry) Step {
	return Step{Epoch: h.beforeMem.ActiveEpoch, Space: h.fx.Space, Entries: entries}
}

func TestPropWriteAndRead(t *testing.T) {
	t.Parallel()
	h := newCompareHarness(t)
	reply, _ := propBoth(t, h, cardsStep(h, cardsProp("deal_index", "3"), cardsProp("empty", ""), cardsPropGuard("absent", nil)))
	if reply.Changed != 2 || !reflect.DeepEqual(reply.ChangedPerEntry, []int{1, 1, 0}) {
		t.Fatalf("reply = %+v", reply)
	}
	if got := h.beforeLua.Epochs["0"].Tables[compareTable].Props; !reflect.DeepEqual(got, map[string]string{"deal_index": "3", "empty": ""}) {
		t.Fatalf("stored properties = %v", got)
	}
	if got := propRead(t, h, "0", nil); !reflect.DeepEqual(got, map[string]string{"deal_index": "3", "empty": ""}) {
		t.Fatalf("props = %v", got)
	}
	if got := propRead(t, h, "0", []string{"absent", "deal_index"}); !reflect.DeepEqual(got, map[string]string{"deal_index": "3"}) {
		t.Fatalf("named props = %v", got)
	}
	if got := propRead(t, h, "0", []string{}); len(got) != 0 {
		t.Fatalf("empty names answered %v", got)
	}
	// A property sits beside the members of the same step.
	reply, _ = propBoth(t, h, cardsStep(h, Entry{Kind: "rows", Table: compareTable, Add: []string{"r0"}},
		Entry{Kind: "create", Table: compareTable, To: "r0:ready", IDs: []string{"m0"}, Scores: []string{"1"}},
		cardsProp("deal_index", "4")))
	if reply.ChangedPerEntry[2] != 1 {
		t.Fatalf("reply = %+v", reply)
	}
	if got := propRead(t, h, "0", []string{"deal_index"}); got["deal_index"] != "4" {
		t.Fatalf("props = %v", got)
	}
}

func TestPropNoopUnchanged(t *testing.T) {
	t.Parallel()
	h := newCompareHarness(t)
	propBoth(t, h, cardsStep(h, cardsProp("deal_index", "3"), cardsProp("empty", "")))
	before, image := h.beforeMem, commitProbeImage(t, h.fx.Client)
	reply, _ := propBoth(t, h, cardsStep(h, cardsProp("deal_index", "3"), cardsProp("empty", "")))
	if reply.Changed != 0 || !reflect.DeepEqual(reply.ChangedPerEntry, []int{0, 0}) {
		t.Fatalf("no-op reply = %+v", reply)
	}
	if !reflect.DeepEqual(before, h.beforeMem) || !reflect.DeepEqual(image, commitProbeImage(t, h.fx.Client)) {
		t.Fatal("an equal value changed the store")
	}
}

func TestRefusePROPGUARD(t *testing.T) {
	t.Parallel()
	h := newCompareHarness(t)
	propBoth(t, h, cardsStep(h, cardsProp("deal_index", "secret-3"), cardsProp("empty", "")))
	for _, tc := range []struct {
		name  string
		guard Entry
		code  string
	}{
		{"present and equal", cardsPropGuard("deal_index", propValue("secret-3")), ""},
		{"present and different", cardsPropGuard("deal_index", propValue("secret-4")), "PROPGUARD"},
		{"present where absent", cardsPropGuard("absent", propValue("")), "PROPGUARD"},
		{"absent and absent", cardsPropGuard("absent", nil), ""},
		{"absent where present", cardsPropGuard("deal_index", nil), "PROPGUARD"},
		{"absent where empty", cardsPropGuard("empty", nil), "PROPGUARD"},
		{"empty and empty", cardsPropGuard("empty", propValue("")), ""},
	} {
		step := cardsStep(h, cardsProp("other", tc.name), tc.guard)
		if tc.code == "" {
			if reply, refusal := propBoth(t, h, step); refusal != nil || reply.ChangedPerEntry[1] != 0 {
				t.Fatalf("%s: %+v %+v", tc.name, reply, refusal)
			}
			continue
		}
		refusal := propUnchanged(t, h, step, "PROPGUARD")
		if refusal.Detail.EntryIndex == nil || *refusal.Detail.EntryIndex != 1 || refusal.Detail.Table != compareTable || refusal.Detail.Name != tc.guard.Name {
			t.Fatalf("%s detail = %+v", tc.name, refusal.Detail)
		}
		if _, text := propRawLua(t, h, step, "PROPGUARD"); strings.Contains(text, "secret") {
			t.Fatalf("%s: the refusal echoed a value: %s", tc.name, text)
		}
	}
	// The guard reads the pre-state: a write before it in the step is unseen.
	reply, refusal := propBoth(t, h, cardsStep(h, cardsProp("deal_index", "secret-5"), cardsPropGuard("deal_index", propValue("secret-3"))))
	if refusal != nil || !reflect.DeepEqual(reply.ChangedPerEntry, []int{1, 0}) {
		t.Fatalf("guard after write: %+v %+v", reply, refusal)
	}
}

func TestPropTwiceRefused(t *testing.T) {
	t.Parallel()
	h := newCompareHarness(t)
	step := cardsStep(h, cardsPropGuard("x", nil), cardsProp("x", "1"), cardsProp("x", "2"))
	if _, err := EncodeStep(step); err == nil {
		t.Fatal("the client encoded a second prop on one pair")
	}
	image := commitProbeImage(t, h.fx.Client)
	refusal, _ := propRawLua(t, h, step, "TWICE")
	if refusal.Detail.EntryIndex == nil || *refusal.Detail.EntryIndex != 2 || refusal.Detail.Table != compareTable || refusal.Detail.Name != "x" {
		t.Fatalf("TWICE detail = %+v", refusal.Detail)
	}
	if !reflect.DeepEqual(image, commitProbeImage(t, h.fx.Client)) {
		t.Fatal("TWICE changed the Redis key image")
	}
	// One prop on each of two pairs, and a guard beside a prop, are legal.
	if _, refusal := propBoth(t, h, cardsStep(h, cardsPropGuard("x", nil), cardsProp("x", "1"), cardsProp("y", "1"))); refusal != nil {
		t.Fatalf("legal step refused %+v", refusal)
	}
}

func TestPropLimit(t *testing.T) {
	t.Parallel()
	h := newCompareHarness(t)
	entries := make([]Entry, 0, MaxPropEntries+1)
	for i := 0; i < MaxPropsPerTable; i++ {
		entries = append(entries, cardsProp(fmt.Sprintf("p%02d", i), "v"))
	}
	if reply, _ := propBoth(t, h, cardsStep(h, entries...)); reply.Changed != MaxPropsPerTable {
		t.Fatalf("64 properties: %+v", reply)
	}
	refusal := propUnchanged(t, h, cardsStep(h, cardsProp("p00", "w"), cardsProp("extra", "v")), "LIMIT")
	if refusal.Detail.EntryIndex == nil || *refusal.Detail.EntryIndex != 1 || refusal.Detail.Budget != "properties" ||
		refusal.Detail.Limit == nil || *refusal.Detail.Limit != 64 || refusal.Detail.Actual == nil || *refusal.Detail.Actual != 65 {
		t.Fatalf("LIMIT detail = %+v", refusal.Detail)
	}
	if reply, _ := propBoth(t, h, cardsStep(h, cardsProp("p00", "w"))); reply.Changed != 1 {
		t.Fatalf("rewrite at the limit: %+v", reply)
	}
	if got := propRead(t, h, "0", nil); len(got) != MaxPropsPerTable || got["p00"] != "w" {
		t.Fatalf("props = %d", len(got))
	}
	// A step holds at most 64 prop and propguard entries.
	entries = append(entries, cardsPropGuard("p00", nil))
	image := commitProbeImage(t, h.fx.Client)
	propRawLua(t, h, cardsStep(h, entries...), "LIMIT")
	if !reflect.DeepEqual(image, commitProbeImage(t, h.fx.Client)) {
		t.Fatal("the entry LIMIT changed the Redis key image")
	}
}

func TestPropAdvanceStartsEmpty(t *testing.T) {
	t.Parallel()
	h := newCompareHarness(t)
	propBoth(t, h, cardsStep(h, cardsProp("deal_index", "3")))
	step := cardsStep(h, Entry{Kind: "advance", AdvanceFrom: "0"}, cardsPropGuard("deal_index", nil), cardsProp("ask_index", "1"))
	op, intent := "advance-props", "advance: the new epoch starts with no property"
	step.Op, step.Intent = &op, &intent
	reply, refusal := propBoth(t, h, step)
	if refusal != nil || reply.EpochAfter != "1" || !reflect.DeepEqual(reply.ChangedPerEntry, []int{0, 0, 1}) {
		t.Fatalf("advance: %+v %+v", reply, refusal)
	}
	if got := propRead(t, h, "1", nil); !reflect.DeepEqual(got, map[string]string{"ask_index": "1"}) {
		t.Fatalf("new epoch props = %v", got)
	}
	if got := propRead(t, h, "0", nil); !reflect.DeepEqual(got, map[string]string{"deal_index": "3"}) {
		t.Fatalf("old epoch props = %v", got)
	}
	if got := propRead(t, h, "0", []string{"ask_index", "deal_index"}); !reflect.DeepEqual(got, map[string]string{"deal_index": "3"}) {
		t.Fatalf("old epoch named props = %v", got)
	}
}

func TestPropAdvanceRefusesPrepopulatedSuccessorHash(t *testing.T) {
	t.Parallel()
	h := newCompareHarness(t)
	ctx := context.Background()

	propBoth(t, h, cardsStep(h, cardsProp("deal_index", "3")))
	succKey := h.fx.Space + "table:" + compareTable + ":1:props"

	// Case 1: Prepopulated successor hash with equal value -> advance with prop deal_index=3
	// Before the fix, this equal-value no-op bypassed advance emptiness preflight and advanced.
	if err := h.fx.Client.HSet(ctx, succKey, "deal_index", "3").Err(); err != nil {
		t.Fatal(err)
	}
	image := commitProbeImage(t, h.fx.Client)
	step := cardsStep(h, Entry{Kind: "advance", AdvanceFrom: "0"}, cardsProp("deal_index", "3"))
	op, intent := "adv-equal", "advance with equal-value prop"
	step.Op, step.Intent = &op, &intent
	_, err := h.rdb.Step(ctx, step)
	requireRefusal(t, err, "DRIFT")
	if !reflect.DeepEqual(image, commitProbeImage(t, h.fx.Client)) {
		t.Fatal("prepopulated successor hash refusal changed Redis whole-key image")
	}

	// Case 2: Prepopulated successor hash with x=v -> advance with absent guard other=nil
	// Stronger: advance plus absent propguard for another name must also refuse DRIFT.
	if err := h.fx.Client.Del(ctx, succKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := h.fx.Client.HSet(ctx, succKey, "x", "v").Err(); err != nil {
		t.Fatal(err)
	}
	image = commitProbeImage(t, h.fx.Client)
	step = cardsStep(h, Entry{Kind: "advance", AdvanceFrom: "0"}, cardsPropGuard("other", nil))
	op, intent = "adv-guard", "advance with absent guard"
	step.Op, step.Intent = &op, &intent
	_, err = h.rdb.Step(ctx, step)
	requireRefusal(t, err, "DRIFT")
	if !reflect.DeepEqual(image, commitProbeImage(t, h.fx.Client)) {
		t.Fatal("prepopulated successor hash absent-guard refusal changed Redis whole-key image")
	}

	// Case 3: Prepopulated successor hash with wrong type (string) -> advance with prop or propguard
	// Successor key of wrong type must refuse DRIFT, not WRONGTYPE.
	if err := h.fx.Client.Del(ctx, succKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := h.fx.Client.Set(ctx, succKey, "not-a-hash", 0).Err(); err != nil {
		t.Fatal(err)
	}
	image = commitProbeImage(t, h.fx.Client)
	step = cardsStep(h, Entry{Kind: "advance", AdvanceFrom: "0"}, cardsProp("deal_index", "3"))
	op, intent = "adv-wrongtype", "advance with wrongtype successor props"
	step.Op, step.Intent = &op, &intent
	_, err = h.rdb.Step(ctx, step)
	requireRefusal(t, err, "DRIFT")
	if !reflect.DeepEqual(image, commitProbeImage(t, h.fx.Client)) {
		t.Fatal("prepopulated successor wrong-type refusal changed Redis whole-key image")
	}
}

func TestPropReplay(t *testing.T) {
	t.Parallel()
	h := newCompareHarness(t)
	step := cardsStep(h, cardsPropGuard("deal_index", nil), cardsProp("deal_index", "1"))
	op, intent := "deal-1", "deal: index 1"
	step.Op, step.Intent = &op, &intent
	reply, _ := propBoth(t, h, step)
	if reply.Changed != 1 || reply.Replay {
		t.Fatalf("fresh reply = %+v", reply)
	}
	// The guard would now fail; the receipt answers first with the recorded reply.
	before, image := h.beforeMem, commitProbeImage(t, h.fx.Client)
	replay, refusal := propBoth(t, h, step)
	if refusal != nil || !replay.Replay || replay.Changed != 1 || replay.EpochAfter != "0" {
		t.Fatalf("replay = %+v %+v", replay, refusal)
	}
	if !reflect.DeepEqual(before, h.beforeMem) || !reflect.DeepEqual(image, commitProbeImage(t, h.fx.Client)) {
		t.Fatal("replay changed the store")
	}
	if got := propRead(t, h, "0", nil); !reflect.DeepEqual(got, map[string]string{"deal_index": "1"}) {
		t.Fatalf("props = %v", got)
	}
}

// In the composed profile a prop change writes no log line (amendment
// 2026-09-30, section 2). This witness skips until the Layer 2 fragment is
// present in the tree.
func TestPropComposedWritesNoLogLine(t *testing.T) {
	t.Parallel()
	fx := newComposedTSetFixture(t)
	fx.Define(t, compareTable, "ready", "busy")
	fx.Activate(t)
	store := newFixtureRedis(t, fx.Client)
	reply, err := store.Step(context.Background(), Step{Epoch: "0", Space: fx.Space, Entries: []Entry{cardsProp("deal_index", "1")}})
	if err != nil || reply.Changed != 1 || reply.Lines != 0 || reply.LastSeq != "0" {
		t.Fatalf("composed prop: %+v %v", reply, err)
	}
	if n, err := fx.Client.XLen(context.Background(), fixtureLogKey(fx.Space, "0")).Result(); err != nil || n != 0 {
		t.Fatalf("log length = %d, %v", n, err)
	}
}
