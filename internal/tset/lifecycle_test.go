package tset

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The lifecycle on the twin (the L1 contract amendment, lifecycle,
// 2026-09-30): define equals the fixture initializer's definitions at epoch
// 0; every refusal is whole; teardown removes the space and keeps its
// receipts.

const lifecycleSpace = "life:"

func lifecycleSpec() DefineSpec {
	return DefineSpec{Space: lifecycleSpace, Build: "b1", View: "sprint", Tables: []TableSpec{
		{Name: "work", Columns: []ColumnSpec{{Name: "ready", Kind: ColumnKindSet}, {Name: "done", Kind: ColumnKindSet}}},
		{Name: "readers", Columns: []ColumnSpec{{Name: "asked", Kind: ColumnKindSet}}},
	}}
}

func lifecycleCode(t *testing.T, err error) string {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("error %v is not a refusal", err)
	}
	return r.Code
}

func TestMemDefineEqualsTheFixtureDefinitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	reply, err := m.Define(ctx, lifecycleSpec())
	if err != nil {
		t.Fatal(err)
	}
	if reply.Status != "ok" || reply.Epoch != "0" || reply.View != "sprint" || !reflect.DeepEqual(reply.Tables, []string{"work", "readers"}) {
		t.Fatalf("define reply %+v", reply)
	}
	want := NewMem()
	for _, d := range []struct {
		t    string
		cols []string
	}{{"work", []string{"ready", "done"}}, {"readers", []string{"asked"}}} {
		if err := want.DefineTable(lifecycleSpace, d.t, TableDefinition{Columns: d.cols,
			MemberPrefix: lifecycleSpace + "member:" + d.t + ":", EpochKey: lifecycleSpace + "sprint:epoch", EpochField: "n"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.Snapshot(lifecycleSpace)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := want.Snapshot(lifecycleSpace)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, fixture) {
		t.Fatalf("define state %+v, fixture state %+v", got, fixture)
	}
	if m.View(lifecycleSpace) != "sprint" {
		t.Fatalf("view %q", m.View(lifecycleSpace))
	}
	receipts := m.LifecycleReceipts(lifecycleSpace)
	if len(receipts) != 1 || receipts[0].Fn != "define" || receipts[0].Build != "b1" || !reflect.DeepEqual(receipts[0].Tables, []string{"work", "readers"}) {
		t.Fatalf("receipts %+v", receipts)
	}
	// The defined space takes a step at epoch 0.
	step := Step{Epoch: "0", Space: lifecycleSpace, Entries: []Entry{{Kind: "rows", Table: "work", Add: []string{"r"}}}}
	if r, err := m.Step(ctx, step); err != nil || r.Status != "ok" {
		t.Fatalf("step on a defined space: %+v %v", r, err)
	}
}

func TestMemDefineRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	many := make([]ColumnSpec, MaxColumns+1)
	for i := range many {
		many[i] = ColumnSpec{Name: "c" + strings.Repeat("x", i), Kind: ColumnKindSet}
	}
	for _, tc := range []struct {
		name string
		edit func(*DefineSpec)
		code string
	}{
		{"empty space", func(s *DefineSpec) { s.Space = "" }, "REQUEST"},
		{"bad view", func(s *DefineSpec) { s.View = "a view" }, "REQUEST"},
		{"no tables", func(s *DefineSpec) { s.Tables = nil }, "REQUEST"},
		{"twice a table", func(s *DefineSpec) { s.Tables[1].Name = "work" }, "REQUEST"},
		{"no columns", func(s *DefineSpec) { s.Tables[0].Columns = nil }, "REQUEST"},
		{"twice a column", func(s *DefineSpec) { s.Tables[0].Columns[1].Name = "ready" }, "REQUEST"},
		{"bad column", func(s *DefineSpec) { s.Tables[0].Columns[0].Name = "a:b" }, "REQUEST"},
		{"five tables", func(s *DefineSpec) {
			for _, n := range []string{"a", "b", "c"} {
				s.Tables = append(s.Tables, TableSpec{Name: n, Columns: []ColumnSpec{{Name: "x", Kind: ColumnKindSet}}})
			}
		}, "LIMIT"},
		{"33 columns", func(s *DefineSpec) { s.Tables[0].Columns = many }, "LIMIT"},
		{"derived kind", func(s *DefineSpec) { s.Tables[0].Columns[0].Kind = "count:ready" }, "CONFIG"},
		{"empty build", func(s *DefineSpec) { s.Build = "" }, "BUILD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NewMem()
			spec := lifecycleSpec()
			tc.edit(&spec)
			_, err := m.Define(ctx, spec)
			if code := lifecycleCode(t, err); code != tc.code {
				t.Fatalf("code %s, want %s", code, tc.code)
			}
			if _, err := m.Snapshot(lifecycleSpace); err == nil || m.View(lifecycleSpace) != "" || len(m.LifecycleReceipts(lifecycleSpace)) != 0 {
				t.Fatal("a refused define left state")
			}
		})
	}
	t.Run("pinned build", func(t *testing.T) {
		t.Parallel()
		m := NewMem()
		m.SetBuild("b2")
		if _, err := m.Define(ctx, lifecycleSpec()); lifecycleCode(t, err) != "BUILD" {
			t.Fatalf("define of another build: %v", err)
		}
	})
	t.Run("exists", func(t *testing.T) {
		t.Parallel()
		m := NewMem()
		if _, err := m.Define(ctx, lifecycleSpec()); err != nil {
			t.Fatal(err)
		}
		before, _ := m.Snapshot(lifecycleSpace)
		if _, err := m.Define(ctx, lifecycleSpec()); lifecycleCode(t, err) != "EXISTS" {
			t.Fatalf("second define: %v", err)
		}
		if after, _ := m.Snapshot(lifecycleSpace); !reflect.DeepEqual(before, after) || len(m.LifecycleReceipts(lifecycleSpace)) != 1 {
			t.Fatal("a refused second define changed the space")
		}
		fixture := NewMem()
		if err := fixture.DefineTable(lifecycleSpace, "work", TableDefinition{Columns: []string{"ready"},
			MemberPrefix: lifecycleSpace + "member:work:", EpochKey: lifecycleSpace + "sprint:epoch", EpochField: "n"}); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.Define(ctx, lifecycleSpec()); lifecycleCode(t, err) != "EXISTS" {
			t.Fatalf("define over the fixture's definitions: %v", err)
		}
	})
}

func TestMemTeardown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	if _, err := m.Teardown(ctx, lifecycleSpace, "sprint"); lifecycleCode(t, err) != "NOSPACE" {
		t.Fatalf("teardown of no space: %v", err)
	}
	if _, err := m.Define(ctx, lifecycleSpec()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Teardown(ctx, lifecycleSpace, "work"); lifecycleCode(t, err) != "CONFIRM" {
		t.Fatalf("teardown under another name: %v", err)
	}
	m.SetRunning(lifecycleSpace, true)
	if _, err := m.Teardown(ctx, lifecycleSpace, "sprint"); lifecycleCode(t, err) != "RUNNING" {
		t.Fatalf("teardown while running: %v", err)
	}
	if _, err := m.Snapshot(lifecycleSpace); err != nil || m.View(lifecycleSpace) != "sprint" {
		t.Fatalf("a refused teardown changed the space: %v", err)
	}
	m.SetRunning(lifecycleSpace, false)
	reply, err := m.Teardown(ctx, lifecycleSpace, "sprint")
	if err != nil || !reply.Done || reply.Calls != 1 {
		t.Fatalf("teardown: %+v %v", reply, err)
	}
	if _, err := m.Snapshot(lifecycleSpace); err == nil || m.View(lifecycleSpace) != "" {
		t.Fatal("teardown left the space")
	}
	if _, err := m.Teardown(ctx, lifecycleSpace, "sprint"); lifecycleCode(t, err) != "NOSPACE" {
		t.Fatalf("second teardown: %v", err)
	}
	var fns []string
	for _, r := range m.LifecycleReceipts(lifecycleSpace) {
		fns = append(fns, r.Fn)
	}
	if !reflect.DeepEqual(fns, []string{"define", "teardown"}) {
		t.Fatalf("receipts %v", fns)
	}
	// A torn-down space is defined again.
	if _, err := m.Define(ctx, lifecycleSpec()); err != nil {
		t.Fatalf("define after teardown: %v", err)
	}
}

func TestLifecycleReceiptsAreBounded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	for i := 0; i < LifecycleReceiptsMax/2+1; i++ {
		if _, err := m.Define(ctx, lifecycleSpec()); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Teardown(ctx, lifecycleSpace, "sprint"); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(m.LifecycleReceipts(lifecycleSpace)); n != LifecycleReceiptsMax {
		t.Fatalf("%d receipts, want the bound %d", n, LifecycleReceiptsMax)
	}
}

func TestEncodeDefineIsTheWire(t *testing.T) {
	t.Parallel()
	raw, err := EncodeDefine(lifecycleSpec())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"space":"life:","build":"b1","view":"sprint","tables":[{"t":"work","columns":[{"name":"ready","kind":"set"},{"name":"done","kind":"set"}]},{"t":"readers","columns":[{"name":"asked","kind":"set"}]}]}`
	if string(raw) != want {
		t.Fatalf("wire %s", raw)
	}
	if _, err := encodeTeardown("", "sprint"); err == nil {
		t.Fatal("teardown of an empty space encoded")
	}
}
