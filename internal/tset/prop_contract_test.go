package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Unit witnesses for table properties on the wire and the Mem twin (L1
// contract amendment 2026-09-30, property, sections 1, 2, 3 and 5). The Lua
// writer and reader are held to the same outcomes in prop_functional_test.go.

func propValue(s string) *string { return &s }

func propMem(t *testing.T) (*Mem, string) {
	t.Helper()
	const space = "prop:"
	m := NewMem()
	requireFixture(t, m.DefineTable(space, "fleet", TableDefinition{
		Columns: []string{"ready", "busy"}, MemberPrefix: space + "member:",
		EpochKey: space + "epoch", EpochField: "n",
	}))
	return m, space
}

func propStep(space string, epoch Decimal, entries ...Entry) Step {
	return Step{Epoch: epoch, Space: space, Entries: entries}
}

func propSet(name, value string) Entry {
	return Entry{Kind: "prop", Table: "fleet", Name: name, Value: propValue(value)}
}

func propGuard(name string, value *string) Entry {
	return Entry{Kind: "propguard", Table: "fleet", Name: name, Value: value}
}

func propMemRead(t *testing.T, m *Mem, space string, epoch Decimal, names []string) map[string]string {
	t.Helper()
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: epoch, Space: space,
		Queries: []ReadQuery{{Kind: "props", Table: "fleet", Names: names}}})
	if err != nil {
		t.Fatalf("props read: %v", err)
	}
	if len(reply.Answers) != 1 || reply.Answers[0].Kind != "props" || reply.Answers[0].Props == nil {
		t.Fatalf("props answer = %+v", reply.Answers)
	}
	return reply.Answers[0].Props
}

func TestPropWireRoundTrip(t *testing.T) {
	t.Parallel()
	step := Step{Epoch: "0", Space: "prop:", Entries: []Entry{
		propSet("deal_index", "7"), propSet("empty", ""),
		propGuard("deal_index", propValue("6")), propGuard("gone", nil), propGuard("empty", propValue("")),
	}}
	raw, err := EncodeStep(step)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `{"kind":"propguard","name":"gone","t":"fleet"}`) {
		t.Fatalf("absent-form propguard carries a value: %s", raw)
	}
	back, err := DecodeStep(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, step) {
		t.Fatalf("round trip: got %+v want %+v", back, step)
	}
	if back.Entries[3].Value != nil || back.Entries[4].Value == nil || *back.Entries[4].Value != "" {
		t.Fatal("absent and empty propguard values are not distinct")
	}
	for _, bad := range []string{
		`{"epoch":"0","space":"prop:","entries":[{"kind":"prop","t":"fleet","name":"x","value":null}]}`,
		`{"epoch":"0","space":"prop:","entries":[{"kind":"prop","t":"fleet","name":"x","value":1}]}`,
		`{"epoch":"0","space":"prop:","entries":[{"kind":"propguard","t":"fleet","name":"x","ids":["a"]}]}`,
		`{"epoch":"0","space":"prop:","entries":[{"kind":"create","t":"fleet","to":"r:ready","ids":["a"],"scores":["1"],"name":"x"}]}`,
	} {
		if _, err := DecodeStep([]byte(bad)); err == nil {
			t.Errorf("decoded malformed request %s", bad)
		}
	}
	plan := ReadPlan{Epoch: "0", Space: "prop:", Queries: []ReadQuery{
		{Kind: "props", Table: "fleet"}, {Kind: "props", Table: "fleet", Names: []string{}},
		{Kind: "props", Table: "fleet", Names: []string{"deal_index"}},
	}}
	rawPlan, err := EncodeReadPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	backPlan, err := DecodeReadPlan(rawPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backPlan, plan) || backPlan.Queries[0].Names != nil || backPlan.Queries[1].Names == nil {
		t.Fatalf("read plan round trip lost the omitted/empty distinction: %+v", backPlan)
	}
	answer, err := json.Marshal(ReadAnswer{Kind: "props"})
	if err != nil || string(answer) != `{"kind":"props","props":{}}` {
		t.Fatalf("empty props answer = %s, %v", answer, err)
	}
}

func TestPropWireValidation(t *testing.T) {
	t.Parallel()
	many := make([]Entry, 0, MaxPropEntries+1)
	for i := 0; i <= MaxPropEntries; i++ {
		many = append(many, propGuard(fmt.Sprintf("p%02d", i), nil))
	}
	for _, tc := range []struct {
		name  string
		entry []Entry
		code  string
	}{
		{"bad name", []Entry{propSet("a b", "1")}, "REQUEST"},
		{"empty name", []Entry{propSet("", "1")}, "REQUEST"},
		{"long name", []Entry{propSet(strings.Repeat("n", MaxIdentifierBytes+1), "1")}, "LIMIT"},
		{"prop without value", []Entry{propGuard("x", nil)}, ""},
		{"prop value required", []Entry{{Kind: "prop", Table: "fleet", Name: "x"}}, "REQUEST"},
		{"long value", []Entry{propSet("x", strings.Repeat("v", MaxFieldValueBytes+1))}, "LIMIT"},
		{"largest value", []Entry{propSet("x", strings.Repeat("v", MaxFieldValueBytes))}, ""},
		{"name on a member entry", []Entry{{Kind: "rows", Table: "fleet", Add: []string{"r"}, Name: "x"}}, "REQUEST"},
		{"twice", []Entry{propSet("x", "1"), propSet("x", "2")}, "TWICE"},
		{"guard beside prop", []Entry{propGuard("x", nil), propSet("x", "1"), propGuard("x", nil)}, ""},
		{"65 entries", many, "LIMIT"},
		{"64 entries", many[:MaxPropEntries], ""},
	} {
		_, err := EncodeStep(Step{Epoch: "0", Space: "prop:", Entries: tc.entry})
		if tc.code == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		refusal := requireRefusal(t, err, tc.code)
		if tc.code == "TWICE" && (refusal.Detail.EntryIndex == nil || *refusal.Detail.EntryIndex != 1 ||
			refusal.Detail.Table != "fleet" || refusal.Detail.Name != "x") {
			t.Errorf("%s detail = %+v", tc.name, refusal.Detail)
		}
	}
	names := make([]string, MaxPropsPerTable+1)
	for i := range names {
		names[i] = fmt.Sprintf("p%02d", i)
	}
	for _, tc := range []struct {
		name  string
		names []string
		code  string
	}{
		{"65 names", names, "LIMIT"},
		{"64 names", names[:MaxPropsPerTable], ""},
		{"duplicate", []string{"a", "a"}, "REQUEST"},
		{"bad name", []string{"a:b"}, "REQUEST"},
	} {
		_, err := EncodeReadPlan(ReadPlan{Epoch: "0", Space: "prop:", Queries: []ReadQuery{{Kind: "props", Table: "fleet", Names: tc.names}}})
		if tc.code == "" {
			if err != nil {
				t.Errorf("read %s: %v", tc.name, err)
			}
			continue
		}
		requireRefusal(t, err, tc.code)
	}
}

func TestMemPropWriteGuardAndRead(t *testing.T) {
	t.Parallel()
	m, space := propMem(t)
	ctx := context.Background()
	reply, err := m.Step(ctx, propStep(space, "0", propSet("deal_index", "3"), propSet("empty", ""), propGuard("absent", nil)))
	if err != nil {
		t.Fatal(err)
	}
	if reply.Changed != 2 || !reflect.DeepEqual(reply.ChangedPerEntry, []int{1, 1, 0}) || reply.Lines != 0 {
		t.Fatalf("reply = %+v", reply)
	}
	if got := reply.MemPlan.Entries[0].Prop; got == nil || got.Present {
		t.Fatalf("plan pre-state = %+v, want absent", got)
	}
	if got := propMemRead(t, m, space, "0", nil); !reflect.DeepEqual(got, map[string]string{"deal_index": "3", "empty": ""}) {
		t.Fatalf("props = %v", got)
	}
	if got := propMemRead(t, m, space, "0", []string{"deal_index", "absent"}); !reflect.DeepEqual(got, map[string]string{"deal_index": "3"}) {
		t.Fatalf("named props = %v", got)
	}
	if got := propMemRead(t, m, space, "0", []string{}); len(got) != 0 {
		t.Fatalf("empty names answered %v", got)
	}
	// Equal value: a no-op. The guard reads the pre-state even after a write
	// on the same pair in the same step.
	reply, err = m.Step(ctx, propStep(space, "0", propSet("deal_index", "3"), propSet("empty", "x"), propGuard("empty", propValue(""))))
	if err != nil || reply.Changed != 1 || !reflect.DeepEqual(reply.ChangedPerEntry, []int{0, 1, 0}) {
		t.Fatalf("no-op and guarded write: %+v %v", reply, err)
	}
	before := refusalSnapshot(t, m, space)
	for _, guard := range []Entry{propGuard("deal_index", nil), propGuard("deal_index", propValue("4")),
		propGuard("absent", propValue("")), propGuard("empty", nil)} {
		_, err := m.Step(ctx, propStep(space, "0", propSet("deal_index", "9"), guard))
		refusal := requireRefusal(t, err, "PROPGUARD")
		if refusal.Detail.EntryIndex == nil || *refusal.Detail.EntryIndex != 1 || refusal.Detail.Table != "fleet" || refusal.Detail.Name != guard.Name {
			t.Fatalf("PROPGUARD detail = %+v", refusal.Detail)
		}
		if encoded, _ := json.Marshal(refusal); strings.Contains(string(encoded), `"4"`) {
			t.Fatalf("PROPGUARD echoed a value: %s", encoded)
		}
	}
	if after := refusalSnapshot(t, m, space); !reflect.DeepEqual(before, after) {
		t.Fatal("PROPGUARD changed the model")
	}
}

func TestMemPropLimitAdvanceAndReplay(t *testing.T) {
	t.Parallel()
	m, space := propMem(t)
	ctx := context.Background()
	entries := make([]Entry, MaxPropsPerTable)
	for i := range entries {
		entries[i] = propSet(fmt.Sprintf("p%02d", i), "v")
	}
	if reply, err := m.Step(ctx, propStep(space, "0", entries...)); err != nil || reply.Changed != MaxPropsPerTable {
		t.Fatalf("64 properties: %+v %v", reply, err)
	}
	_, err := m.Step(ctx, propStep(space, "0", propSet("p00", "w"), propSet("extra", "v")))
	refusal := requireRefusal(t, err, "LIMIT")
	if refusal.Detail.Budget != "properties" || *refusal.Detail.EntryIndex != 1 || *refusal.Detail.Limit != 64 || *refusal.Detail.Actual != 65 {
		t.Fatalf("LIMIT detail = %+v", refusal.Detail)
	}
	if _, err := m.Step(ctx, propStep(space, "0", propSet("p00", "w"))); err != nil {
		t.Fatalf("rewrite at the limit: %v", err)
	}
	op, intent := "advance-1", "advance with a property"
	step := propStep(space, "0", Entry{Kind: "advance", AdvanceFrom: "0"}, propGuard("p00", nil), propSet("deal_index", "1"))
	step.Op, step.Intent = &op, &intent
	reply, err := m.Step(ctx, step)
	if err != nil || reply.EpochAfter != "1" || !reflect.DeepEqual(reply.ChangedPerEntry, []int{0, 0, 1}) {
		t.Fatalf("advance: %+v %v", reply, err)
	}
	if got := propMemRead(t, m, space, "1", nil); !reflect.DeepEqual(got, map[string]string{"deal_index": "1"}) {
		t.Fatalf("new epoch props = %v", got)
	}
	if got := propMemRead(t, m, space, "0", []string{"p00", "deal_index"}); !reflect.DeepEqual(got, map[string]string{"p00": "w"}) {
		t.Fatalf("old epoch props = %v", got)
	}
	before := refusalSnapshot(t, m, space)
	replay, err := m.Step(ctx, step)
	if err != nil || !replay.Replay || replay.Changed != 1 || replay.EpochAfter != "1" {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if after := refusalSnapshot(t, m, space); !reflect.DeepEqual(before, after) {
		t.Fatal("replay changed the model")
	}
}

func TestMemPropAdvanceRefusesPrepopulatedSuccessor(t *testing.T) {
	t.Parallel()
	m, space := propMem(t)
	ctx := context.Background()

	if _, err := m.Step(ctx, propStep(space, "0", propSet("deal_index", "3"))); err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	ns := m.spaces[space]
	e1 := ns.epochs["1"]
	if e1 == nil {
		e1 = &memEpoch{tables: make(map[string]*memTableEpoch)}
		ns.epochs["1"] = e1
	}
	if e1.tables["fleet"] == nil {
		e1.tables["fleet"] = newMemTableEpoch()
	}
	e1.tables["fleet"].props["x"] = "v"
	m.mu.Unlock()

	before := refusalSnapshot(t, m, space)
	op, intent := "adv-equal", "advance with prepopulated epoch 1"
	step := propStep(space, "0", Entry{Kind: "advance", AdvanceFrom: "0"}, propSet("x", "v"))
	step.Op, step.Intent = &op, &intent
	_, err := m.Step(ctx, step)
	refusal := requireRefusal(t, err, "DRIFT")
	if refusal.Detail.EntryIndex == nil || *refusal.Detail.EntryIndex != 0 {
		t.Fatalf("DRIFT detail = %+v", refusal.Detail)
	}
	if after := refusalSnapshot(t, m, space); !reflect.DeepEqual(before, after) {
		t.Fatal("advance refusal changed the model")
	}
}

func TestMemPropCellProbeLimitBoundary(t *testing.T) {
	t.Parallel()
	preEpoch := &memEpoch{tables: map[string]*memTableEpoch{"fleet": newMemTableEpoch()}}
	workEpoch := &memEpoch{tables: map[string]*memTableEpoch{"fleet": newMemTableEpoch()}}
	entry := propSet("deal_index", "1")

	for i := 0; i < 5; i++ {
		preEpoch.tables["fleet"].props[fmt.Sprintf("p%d", i)] = "v"
	}

	// Boundary 1: cellProbes exactly reaches 20,000 (19,999 + 1 from HLEN).
	b := &memWorkBudget{cellProbes: 19999, fetchedBytes: 100}
	counts := make(map[string]int)
	observed := make(map[string]bool)
	_, didChange, err := memPlanProp(preEpoch, workEpoch, entry, 0, counts, observed, b)
	if err != nil {
		t.Fatalf("at 20000 cell probes: %v", err)
	}
	if !didChange || b.cellProbes != 20000 {
		t.Fatalf("cellProbes = %d, want 20000", b.cellProbes)
	}
	if b.fetchedBytes != 101 { // 100 + len("5") = 101
		t.Fatalf("fetchedBytes = %d, want 101", b.fetchedBytes)
	}

	// Boundary 2: cellProbes exceeds 20,000 (20,000 + 1 = 20,001).
	b = &memWorkBudget{cellProbes: 20000, fetchedBytes: 100}
	counts = make(map[string]int)
	observed = make(map[string]bool)
	_, didChange, err = memPlanProp(preEpoch, workEpoch, entry, 0, counts, observed, b)
	if err == nil {
		t.Fatal("expected LIMIT refusal at 20001 cell probes, got nil")
	}
	refusal := requireRefusal(t, err, "LIMIT")
	if refusal.Detail.Budget != "cell_probes" {
		t.Fatalf("refusal budget = %q, want cell_probes", refusal.Detail.Budget)
	}

	// Boundary 3: raw_fetched_bytes exceeds 8 MiB (8<<20).
	b = &memWorkBudget{cellProbes: 0, fetchedBytes: 8 << 20}
	counts = make(map[string]int)
	observed = make(map[string]bool)
	_, didChange, err = memPlanProp(preEpoch, workEpoch, entry, 0, counts, observed, b)
	if err == nil {
		t.Fatal("expected LIMIT refusal when fetchedBytes exceeds 8<<20, got nil")
	}
	refusal = requireRefusal(t, err, "LIMIT")
	if refusal.Detail.Budget != "raw_fetched_bytes" {
		t.Fatalf("refusal budget = %q, want raw_fetched_bytes", refusal.Detail.Budget)
	}
}

