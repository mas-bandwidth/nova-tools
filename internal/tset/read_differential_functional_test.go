//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func differentialReadFixture(t *testing.T) (*tsetFixture, *Mem) {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.AddRow(t, "work", "r", 7)
	fx.AddRow(t, "work", "later", 9)
	m := NewMem()
	if err := m.DefineTable(fx.Space, "work", TableDefinition{Columns: []string{"c"}, MemberPrefix: fx.Space + "member:work:", EpochKey: fx.Space + "sprint:epoch", EpochField: "n"}); err != nil {
		t.Fatal(err)
	}
	for row, rank := range map[string]Decimal{"r": "7", "later": "9"} {
		if err := m.SeedRow(fx.Space, "work", "0", row, rank); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	pipe := fx.Client.Pipeline()
	seed := []struct {
		id, score, row string
		fields         map[string]string
	}{
		{"a", "1.0", "r", map[string]string{"empty": "", "secret": "a", "tag": "x"}},
		{"b", "1", "r", map[string]string{"empty": "", "secret": "b"}},
		{"c", "2", "later", map[string]string{"tag": "y"}},
		{"parked", "", "", map[string]string{"secret": "parked"}},
	}
	for _, item := range seed {
		member := MemRecord{Epoch: "0", Revision: "1", Row: item.row, Score: item.score, Fields: item.fields}
		if item.row != "" {
			member.Column = "c"
		}
		if err := m.SeedMember(fx.Space, "work", "0", item.id, member); err != nil {
			t.Fatal(err)
		}
		hash := map[string]any{"epoch": "0", "revision": "1"}
		if item.row != "" {
			hash["place:work"] = item.row + ":c"
		}
		for field, value := range item.fields {
			hash[field] = value
		}
		pipe.HSet(ctx, fixtureRecordKey(fx.Space, "work", item.id), hash)
		if item.row != "" {
			score, err := readScore(item.score)
			if err != nil {
				t.Fatal(err)
			}
			pipe.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", item.row, "c"), redis.Z{Score: score, Member: item.id})
		}
	}
	if err := m.SeedZSet(fx.Space, fx.Space+"index:work", map[string]string{"ix-a": "1", "ix-b": "2"}); err != nil {
		t.Fatal(err)
	}
	pipe.ZAdd(ctx, fx.Space+"index:work", redis.Z{Score: 1, Member: "ix-a"}, redis.Z{Score: 2, Member: "ix-b"})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fx.Activate(t)
	return fx, m
}

func differentialReadAnswers(t *testing.T, model, server ReadReply) {
	t.Helper()
	if model.Status != "read" || server.Status != "read" || !model.Complete || !server.Complete || model.Epoch != server.Epoch || model.ActiveEpoch != server.ActiveEpoch || !ValidDecimal(model.TimeMS) || !ValidDecimal(server.TimeMS) {
		t.Fatalf("read envelope differs: Mem=%+v Lua=%+v", model, server)
	}
	var left, right any
	mb, err := json.Marshal(model.Answers)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := json.Marshal(server.Answers)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mb, &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rb, &right); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("read answer mismatch: Mem=%s Lua=%s", mb, rb)
	}
	var mc, sc struct {
		Field   int64 `json:"field"`
		Cell    int64 `json:"cell"`
		Record  int64 `json:"record"`
		RangeID int64 `json:"range_id"`
	}
	if err := json.Unmarshal(model.Counters, &mc); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(server.Counters, &sc); err != nil {
		t.Fatal(err)
	}
	if mc != sc {
		t.Fatalf("logical read work differs: Mem=%+v Lua=%+v", mc, sc)
	}
}

func TestReadDifferentialMixedProjectionAndBounds(t *testing.T) {
	t.Parallel()
	fx, m := differentialReadFixture(t)
	plan := ReadPlan{Epoch: "0", Space: fx.Space, Queries: []ReadQuery{
		{Kind: "range", Table: "work", Cell: "r:c", Min: "(0", Max: "+inf", Limit: 2, Records: true},
		{Kind: "range", Table: "work", Cell: "r:c", Min: "0x1p+0", Max: "+infinity", Limit: 2, Records: true, Fields: []string{}},
		{Kind: "count", Table: "work", Cells: []string{"r:c", "later:c", "r:c"}},
		{Kind: "rcount", Table: "work", Cells: []string{"r:c", "later:c"}, Min: " 0x1p+0", Max: "2"},
		{Kind: "ids", Table: "work", IDs: []string{"missing", "parked", "a"}, Fields: []string{"empty", "absent"}},
		{Kind: "rows", Table: "work"},
		{Kind: "range", Key: fx.Space + "index:work", Min: "-inf", Max: "+inf", Limit: 2},
	}}
	model, me := m.Read(context.Background(), plan)
	server, se := newFixtureRedis(t, fx.Client).Read(context.Background(), plan)
	if me != nil || se != nil {
		t.Fatalf("mixed read: Mem=%v Lua=%v", me, se)
	}
	differentialReadAnswers(t, model, server)
	if got := model.Answers[0].Records[0].Fields; len(got) != 3 || !got["empty"].Present || got["secret"].Value != "a" {
		t.Fatalf("omitted fields did not project all: %#v", got)
	}
	if got := model.Answers[1].Records[0].Fields; len(got) != 0 {
		t.Fatalf("explicit empty projection leaked fields: %#v", got)
	}
}

func TestReadDifferentialExactEmptyScoreBounds(t *testing.T) {
	t.Parallel()
	fx, m := differentialReadFixture(t)
	ctx := context.Background()
	if err := m.SeedMember(fx.Space, "work", "0", "zero", MemRecord{
		Epoch: "0", Revision: "1", Row: "r", Column: "c", Score: "0",
	}); err != nil {
		t.Fatal(err)
	}
	pipe := fx.Client.Pipeline()
	pipe.HSet(ctx, fixtureRecordKey(fx.Space, "work", "zero"), map[string]any{
		"epoch": "0", "revision": "1", "place:work": "r:c",
	})
	pipe.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "c"), redis.Z{Score: 0, Member: "zero"})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	plan := ReadPlan{Epoch: "0", Space: fx.Space, Queries: []ReadQuery{
		{Kind: "range", Table: "work", Cell: "r:c", Min: "", Max: "+inf", Limit: 3},
		{Kind: "range", Table: "work", Cell: "r:c", Min: "(", Max: "+inf", Limit: 3},
		{Kind: "rcount", Table: "work", Cells: []string{"r:c"}, Min: "-inf", Max: ""},
		{Kind: "rcount", Table: "work", Cells: []string{"r:c"}, Min: "-inf", Max: "("},
	}}
	model, me := m.Read(ctx, plan)
	server, se := newFixtureRedis(t, fx.Client).Read(ctx, plan)
	if me != nil || se != nil {
		t.Fatalf("exact empty score bounds: Mem=%v Lua=%v", me, se)
	}
	differentialReadAnswers(t, model, server)
	if !reflect.DeepEqual(model.Answers[0].IDs, []string{"zero", "a", "b"}) ||
		!reflect.DeepEqual(model.Answers[1].IDs, []string{"a", "b"}) ||
		model.Answers[2].Sum != 1 || model.Answers[3].Sum != 0 {
		t.Fatalf("empty-bound inclusion differs: %+v", model.Answers)
	}
}

func TestReadDifferentialRepeatedCellProbeBoundary(t *testing.T) {
	t.Parallel()
	fx, m := differentialReadFixture(t)
	cells := make([]string, 10000)
	for i := range cells {
		cells[i] = "r:c"
	}
	plan := ReadPlan{Epoch: "0", Space: fx.Space, Queries: []ReadQuery{{Kind: "count", Table: "work", Cells: cells}}}
	model, me := m.Read(context.Background(), plan)
	server, se := newFixtureRedis(t, fx.Client).Read(context.Background(), plan)
	if me != nil || se != nil {
		t.Fatalf("10k repeated-cell count: Mem=%v Lua=%v", me, se)
	}
	differentialReadAnswers(t, model, server)
	if len(model.Answers[0].Counts) != 10000 || model.Answers[0].Sum != 20000 {
		t.Fatalf("incomplete count answer: %#v", model.Answers[0])
	}
	cells = make([]string, 20000)
	for i := range cells {
		cells[i] = "r:c"
	}
	plan.Queries[0].Cells = cells
	_, me = m.Read(context.Background(), plan)
	_, se = newFixtureRedis(t, fx.Client).Read(context.Background(), plan)
	var mr, sr *Refusal
	if !errors.As(me, &mr) || !errors.As(se, &sr) || mr.Code != "BUDGET" || sr.Code != "BUDGET" || mr.Detail.Budget != "cell" || sr.Detail.Budget != "cell" {
		t.Fatalf("20k count cap differs: Mem=%v Lua=%v", me, se)
	}
}

func TestReadDifferentialMissingRowProbeAtCellBudget(t *testing.T) {
	t.Parallel()
	fx, m := differentialReadFixture(t)
	for _, tc := range []struct {
		existing int
		code     string
	}{{19994, "NOROW"}, {19995, "BUDGET"}} {
		cells := make([]string, tc.existing+1)
		for i := 0; i < tc.existing; i++ {
			cells[i] = "r:c"
		}
		cells[tc.existing] = "missing:c"
		plan := ReadPlan{Epoch: "0", Space: fx.Space, Queries: []ReadQuery{{Kind: "count", Table: "work", Cells: cells}}}
		model, me := m.Read(context.Background(), plan)
		server, se := newFixtureRedis(t, fx.Client).Read(context.Background(), plan)
		var mr, sr *Refusal
		if !errors.As(me, &mr) || !errors.As(se, &sr) || mr.Code != tc.code || sr.Code != tc.code {
			t.Fatalf("%d existing cells: Mem=%v Lua=%v", tc.existing, me, se)
		}
		if model.Status != "" || server.Status != "" || len(model.Answers) != 0 || len(server.Answers) != 0 {
			t.Fatalf("%d existing cells leaked an answer: Mem=%+v Lua=%+v", tc.existing, model, server)
		}
		if tc.code == "BUDGET" && (mr.Detail.Budget != "cell" || sr.Detail.Budget != "cell" ||
			mr.Detail.Actual == nil || *mr.Detail.Actual != 20001 || sr.Detail.Actual == nil || *sr.Detail.Actual != 20001) {
			t.Fatalf("missing-row probe budget differs: Mem=%+v Lua=%+v", mr.Detail, sr.Detail)
		}
	}
}

func TestReadDifferentialMissingPlacedRowDriftBeforeCellProbe(t *testing.T) {
	t.Parallel()
	fx, m := differentialReadFixture(t)
	ctx := context.Background()
	if err := fx.Client.ZRem(ctx, fixtureRowsKey(fx.Space, "work", "0"), "later").Err(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	delete(m.spaces[fx.Space].epochs["0"].tables["work"].rows, "later")
	m.mu.Unlock()
	cells := make([]string, 19994)
	for i := range cells {
		cells[i] = "r:c"
	}
	plan := ReadPlan{Epoch: "0", Space: fx.Space, Queries: []ReadQuery{
		{Kind: "count", Table: "work", Cells: cells},
		{Kind: "ids", Table: "work", IDs: []string{"c"}, Fields: []string{}},
	}}
	model, me := m.Read(ctx, plan)
	server, se := newFixtureRedis(t, fx.Client).Read(ctx, plan)
	var mr, sr *Refusal
	if !errors.As(me, &mr) || !errors.As(se, &sr) || mr.Code != "DRIFT" || sr.Code != "DRIFT" ||
		mr.Detail.QueryIndex == nil || *mr.Detail.QueryIndex != 1 || sr.Detail.QueryIndex == nil || *sr.Detail.QueryIndex != 1 {
		t.Fatalf("missing placed row at probe cap: Mem=%v Lua=%v", me, se)
	}
	if model.Status != "" || server.Status != "" || len(model.Answers) != 0 || len(server.Answers) != 0 {
		t.Fatalf("DRIFT leaked an earlier count answer: Mem=%+v Lua=%+v", model, server)
	}
}

func TestReadDifferentialRawRangeLongIDRefuses(t *testing.T) {
	t.Parallel()
	fx, m := differentialReadFixture(t)
	key := fx.Space + "index:long-id"
	id := strings.Repeat("x", 257) // Fits the 320-byte fetch reservation but violates the ID contract.
	if err := m.SeedZSet(fx.Space, key, map[string]string{id: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.ZAdd(context.Background(), key, redis.Z{Score: 1, Member: id}).Err(); err != nil {
		t.Fatal(err)
	}
	plan := ReadPlan{Epoch: "0", Space: fx.Space, Queries: []ReadQuery{
		{Kind: "rows", Table: "work"},
		{Kind: "range", Key: key, Min: "-inf", Max: "+inf", Limit: 1},
	}}
	model, me := m.Read(context.Background(), plan)
	server, se := newFixtureRedis(t, fx.Client).Read(context.Background(), plan)
	var mr, sr *Refusal
	if !errors.As(me, &mr) || !errors.As(se, &sr) || mr.Code != "DRIFT" || sr.Code != "DRIFT" {
		t.Fatalf("257-byte raw ID: Mem reply=%+v err=%v, Lua reply=%+v err=%v", model, me, server, se)
	}
	if len(model.Answers) != 0 || len(server.Answers) != 0 {
		t.Fatalf("DRIFT leaked earlier answers: Mem=%+v Lua=%+v", model, server)
	}
}

func TestReadDifferentialTenThousandProjectedRecords(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.AddRow(t, "work", "r", 0)
	m := NewMem()
	if err := m.DefineTable(fx.Space, "work", TableDefinition{Columns: []string{"c"}, MemberPrefix: fx.Space + "member:work:", EpochKey: fx.Space + "sprint:epoch", EpochField: "n"}); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedRow(fx.Space, "work", "0", "r", "0"); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"}
	ids := make([]string, 10000)
	ctx := context.Background()
	pipe := fx.Client.Pipeline()
	for i := range ids {
		id := fmt.Sprintf("member-%05d", i)
		ids[i] = id
		if err := m.SeedMember(fx.Space, "work", "0", id, MemRecord{Epoch: "0", Revision: "1", Row: "r", Column: "c", Score: "1", Fields: fields}); err != nil {
			t.Fatal(err)
		}
		hash := map[string]any{"epoch": "0", "revision": "1", "place:work": "r:c"}
		for field, value := range fields {
			hash[field] = value
		}
		pipe.HSet(ctx, fixtureRecordKey(fx.Space, "work", id), hash)
		pipe.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "c"), redis.Z{Score: 1, Member: id})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fx.Activate(t)
	plan := ReadPlan{Epoch: "0", Space: fx.Space, Queries: []ReadQuery{{Kind: "ids", Table: "work", IDs: ids, Fields: []string{"a", "b", "c", "d", "e"}}}}
	model, me := m.Read(ctx, plan)
	server, se := newFixtureRedis(t, fx.Client).Read(ctx, plan)
	if me != nil || se != nil {
		t.Fatalf("10k projected-record read: Mem=%v Lua=%v", me, se)
	}
	differentialReadAnswers(t, model, server)
	if len(model.Answers[0].Records) != 10000 {
		t.Fatalf("10k read returned %d records", len(model.Answers[0].Records))
	}
}
