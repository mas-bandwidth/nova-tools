//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

const cjsonReadLengthProbe = `
local S=NS.tset
redis.register_function('ns_tset_cjson_read_length_probe', function(keys,args)
  if #keys~=0 or #args~=1 then return 'bad args' end
  return tostring(#S.json.encode({v=args[1]}))
end)
`

func TestReadCJSONStringLengthsMatchLuaEncoder(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.ActivateWithLua(t, cjsonReadLengthProbe)
	for _, value := range []string{
		"<>&/\x7f\u2028\u2029\b\t\n\f\r\\\"",
		strings.Repeat("<", MaxFieldValueBytes),
	} {
		want, err := readCJSONLength(map[string]string{"v": value})
		if err != nil {
			t.Fatal(err)
		}
		gotText, err := fx.Client.FCall(context.Background(), "ns_tset_cjson_read_length_probe", []string{}, value).Text()
		if err != nil {
			t.Fatal(err)
		}
		got, err := strconv.ParseInt(gotText, 10, 64)
		if err != nil || got != want {
			t.Fatalf("CJSON length for %d input bytes: Lua=%q Mem=%d err=%v", len(value), gotText, want, err)
		}
	}
}

func crossEpochReadFixture(t *testing.T) (*tsetFixture, *Mem) {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.AddRow(t, "work", "old-row", 7)
	m := NewMem()
	if err := m.DefineTable(fx.Space, "work", TableDefinition{
		Columns: []string{"c"}, MemberPrefix: fx.Space + "member:work:",
		EpochKey: fx.Space + "sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedRow(fx.Space, "work", "0", "old-row", "7"); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedMember(fx.Space, "work", "0", "old", MemRecord{
		Epoch: "0", Revision: "3", Row: "old-row", Column: "c", Score: "2",
		Fields: map[string]string{"tag": "old"},
	}); err != nil {
		t.Fatal(err)
	}
	fx.Epoch = "1"
	fx.seedEpoch(t)
	ctx := context.Background()
	definition, err := fx.Client.HGetAll(ctx, fixtureDefinitionKey(fx.Space, "work", "0")).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.HSet(ctx, fixtureDefinitionKey(fx.Space, "work", "1"), definition).Err(); err != nil {
		t.Fatal(err)
	}
	fx.AddRow(t, "work", "new-row", 11)
	if err := m.SetActiveEpoch(fx.Space, "1"); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedRow(fx.Space, "work", "1", "new-row", "11"); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedMember(fx.Space, "work", "1", "new", MemRecord{
		Epoch: "1", Revision: "7", Row: "new-row", Column: "c", Score: "5",
		Fields: map[string]string{"tag": "new"},
	}); err != nil {
		t.Fatal(err)
	}
	pipe := fx.Client.Pipeline()
	pipe.HSet(ctx, fixtureRecordKey(fx.Space, "work", "old"), map[string]any{
		"epoch": "0", "revision": "3", "place:work": "old-row:c", "tag": "old",
	})
	pipe.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "old-row", "c"), redis.Z{Score: 2, Member: "old"})
	pipe.HSet(ctx, fixtureRecordKey(fx.Space, "work", "new"), map[string]any{
		"epoch": "1", "revision": "7", "place:work": "new-row:c", "tag": "new",
	})
	pipe.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "1", "new-row", "c"), redis.Z{Score: 5, Member: "new"})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fx.Activate(t)
	assertReadFixtureParity(t, fx, m)
	return fx, m
}

func TestReadCrossEpochIDsMatchLuaRecordNamespace(t *testing.T) {
	t.Parallel()
	fx, m := crossEpochReadFixture(t)
	for _, tc := range []struct {
		request, id, stored Decimal
		row                 string
	}{
		{"1", "old", "0", "old-row"},
		{"0", "new", "1", "new-row"},
	} {
		plan := ReadPlan{Epoch: tc.request, Space: fx.Space, Queries: []ReadQuery{{
			Kind: "ids", Table: "work", IDs: []string{string(tc.id)}, Fields: []string{"tag"},
		}}}
		model, err := m.Read(context.Background(), plan)
		if err != nil {
			t.Fatalf("Mem epoch %s: %v", tc.request, err)
		}
		var server ReadReply
		if err := json.Unmarshal(readLuaRaw(t, fx, plan), &server); err != nil {
			t.Fatalf("Lua epoch %s decode: %v", tc.request, err)
		}
		differentialReadAnswers(t, model, server)
		record := model.Answers[0].Records[0]
		if !record.Exists || record.Epoch != tc.stored || record.Place == nil || record.Place.Row != tc.row {
			t.Fatalf("epoch %s record %s: %+v", tc.request, tc.id, record)
		}
	}
}

func TestReadDoneMissingIdentityEpochMatchesLuaActive(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	m := NewMem()
	if err := m.DefineTable(fx.Space, "work", TableDefinition{
		Columns: []string{"c"}, MemberPrefix: fx.Space + "member:work:",
		EpochKey: fx.Space + "sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatal(err)
	}
	fx.Epoch = "1"
	fx.seedEpoch(t)
	ctx := context.Background()
	definition, err := fx.Client.HGetAll(ctx, fixtureDefinitionKey(fx.Space, "work", "0")).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.HSet(ctx, fixtureDefinitionKey(fx.Space, "work", "1"), definition).Err(); err != nil {
		t.Fatal(err)
	}
	if err := m.SetActiveEpoch(fx.Space, "1"); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.Del(ctx, fx.Space+"sprint:epoch@0", fixtureDefinitionKey(fx.Space, "work", "0")).Err(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	delete(m.spaces[fx.Space].epochs, "0")
	m.mu.Unlock()
	fx.Activate(t)
	plan := ReadPlan{Epoch: "1", Space: fx.Space, Queries: []ReadQuery{
		{Kind: "rows", Table: "work"},
		{Kind: "done", Ops: []DoneIdentity{{Epoch: "0", Op: "old-op", IntentDigest: strings.Repeat("0", 40)}}},
	}}
	beforeMem, err := m.Snapshot(fx.Space)
	if err != nil {
		t.Fatal(err)
	}
	beforeRedis := commitProbeImage(t, fx.Client)
	model, err := m.Read(ctx, plan)
	var memRef *Refusal
	if !errors.As(err, &memRef) || memRef.Code != "EPOCHGONE" ||
		memRef.Detail.ActiveEpoch != "1" || memRef.Detail.QueryIndex == nil || *memRef.Detail.QueryIndex != 1 {
		t.Fatalf("Mem done gone: reply=%+v err=%v", model, err)
	}
	if len(model.Answers) != 0 {
		t.Fatalf("Mem leaked answers: %+v", model)
	}
	raw := readLuaRaw(t, fx, plan)
	var luaRef Refusal
	if err := json.Unmarshal(raw, &luaRef); err != nil || luaRef.Code != "EPOCHGONE" ||
		luaRef.Detail.ActiveEpoch != "1" || luaRef.Detail.QueryIndex == nil || *luaRef.Detail.QueryIndex != 1 {
		t.Fatalf("Lua done gone: raw=%s err=%v", raw, err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope["answers"] != nil {
		t.Fatalf("Lua refusal leaked answers: raw=%s err=%v", raw, err)
	}
	afterMem, err := m.Snapshot(fx.Space)
	if err != nil || !reflect.DeepEqual(beforeMem, afterMem) ||
		!reflect.DeepEqual(beforeRedis, commitProbeImage(t, fx.Client)) {
		t.Fatalf("done gone changed state: Mem err=%v", err)
	}
}

func TestReadRowsCountPreflightMatchesLua(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	m := NewMem()
	if err := m.DefineTable(fx.Space, "work", TableDefinition{
		Columns: []string{"c"}, MemberPrefix: fx.Space + "member:work:",
		EpochKey: fx.Space + "sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const count = 30000 // count*280 exceeds the shared 8 MiB fetched-byte cap.
	values := make([]redis.Z, count)
	for i := range values {
		row := fmt.Sprintf("r%05d", i)
		values[i] = redis.Z{Score: float64(i), Member: row}
		if err := m.SeedRow(fx.Space, "work", "0", row, Decimal(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := fx.Client.ZAdd(ctx, fixtureRowsKey(fx.Space, "work", "0"), values...).Err(); err != nil {
		t.Fatal(err)
	}
	fx.Activate(t)
	plan := ReadPlan{Epoch: "0", Space: fx.Space, Queries: []ReadQuery{{Kind: "rows", Table: "work"}}}
	model, err := m.Read(ctx, plan)
	var memRef *Refusal
	if !errors.As(err, &memRef) || memRef.Code != "BUDGET" || memRef.Detail.Budget != "fetched_bytes" ||
		memRef.Detail.QueryIndex == nil || *memRef.Detail.QueryIndex != 0 || len(model.Answers) != 0 {
		t.Fatalf("Mem rows preflight: reply=%+v err=%v", model, err)
	}
	raw := readLuaRaw(t, fx, plan)
	var luaRef Refusal
	if err := json.Unmarshal(raw, &luaRef); err != nil || luaRef.Code != "BUDGET" ||
		luaRef.Detail.Budget != "fetched_bytes" || luaRef.Detail.QueryIndex == nil || *luaRef.Detail.QueryIndex != 0 {
		t.Fatalf("Lua rows preflight: raw=%s err=%v", raw, err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope["answers"] != nil {
		t.Fatalf("Lua rows refusal leaked answers: raw=%s err=%v", raw, err)
	}
	if n, err := fx.Client.ZCard(ctx, fixtureRowsKey(fx.Space, "work", "0")).Result(); err != nil || n != count {
		t.Fatalf("rows changed on refusal: count=%d err=%v", n, err)
	}
}

func TestReadRowsCJSONLedgerMatchesLua(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	m := NewMem()
	if err := m.DefineTable(fx.Space, "work", TableDefinition{
		Columns: []string{"c"}, MemberPrefix: fx.Space + "member:work:",
		EpochKey: fx.Space + "sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatal(err)
	}
	value := strings.Repeat("<", MaxFieldValueBytes)
	if err := m.SeedMember(fx.Space, "work", "0", "large", MemRecord{
		Epoch: "0", Revision: "1", Fields: map[string]string{"html": value},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := fx.Client.HSet(ctx, fixtureRecordKey(fx.Space, "work", "large"), map[string]any{
		"epoch": "0", "revision": "1", "html": value,
	}).Err(); err != nil {
		t.Fatal(err)
	}
	const rowCount = 29000
	rows := make([]redis.Z, rowCount)
	for i := range rows {
		row := fmt.Sprintf("r%05d", i)
		rows[i] = redis.Z{Score: float64(i), Member: row}
		if err := m.SeedRow(fx.Space, "work", "0", row, Decimal(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := fx.Client.ZAdd(ctx, fixtureRowsKey(fx.Space, "work", "0"), rows...).Err(); err != nil {
		t.Fatal(err)
	}
	fx.Activate(t)
	plan := ReadPlan{Epoch: "0", Space: fx.Space, Queries: []ReadQuery{
		{Kind: "ids", Table: "work", IDs: []string{"large"}, Fields: []string{"html"}},
		{Kind: "rows", Table: "work"},
	}}
	model, err := m.Read(ctx, plan)
	if err != nil {
		t.Fatalf("Mem CJSON ledger refused: %v", err)
	}
	var server ReadReply
	if err := json.Unmarshal(readLuaRaw(t, fx, plan), &server); err != nil {
		t.Fatal(err)
	}
	if len(model.Answers) != 2 || len(model.Answers[1].Rows) != rowCount ||
		len(server.Answers) != 2 || len(server.Answers[1].Rows) != rowCount {
		t.Fatalf("ids/rows answers incomplete: Mem=%d Lua=%d", len(model.Answers), len(server.Answers))
	}
	differentialReadAnswers(t, model, server)
}
