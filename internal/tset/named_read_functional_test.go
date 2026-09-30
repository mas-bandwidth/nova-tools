//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// readContractFixture seeds one placed member before either runtime is active.
// The 64 KiB field is fetched once, then projected repeatedly by the read
// request so the encoded-reply budget is exhausted without a huge fixture.
func readContractFixture(t *testing.T) (*tsetFixture, *Mem) {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.AddRow(t, "work", "r", 0)
	mem := NewMem()
	if err := mem.DefineTable(fx.Space, "work", TableDefinition{
		Columns: []string{"c"}, MemberPrefix: fx.Space + "member:work:",
		EpochKey: fx.Space + "sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.SeedRow(fx.Space, "work", "0", "r", "0"); err != nil {
		t.Fatal(err)
	}
	blob := strings.Repeat("\x01", MaxFieldValueBytes)
	if err := mem.SeedMember(fx.Space, "work", "0", "existing", MemRecord{
		Epoch: "0", Revision: "1", Row: "r", Column: "c", Score: "1",
		Fields: map[string]string{"blob": blob},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pipe := fx.Client.Pipeline()
	pipe.HSet(ctx, fixtureRecordKey(fx.Space, "work", "existing"), map[string]any{
		"epoch": "0", "revision": "1", "place:work": "r:c", "blob": blob,
	})
	pipe.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "c"),
		redis.Z{Score: 1, Member: "existing"})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed read member: %v", err)
	}
	fx.Activate(t)
	assertReadFixtureParity(t, fx, mem)
	return fx, mem
}

func assertReadFixtureParity(t *testing.T, fx *tsetFixture, mem *Mem) {
	t.Helper()
	model, err := mem.Snapshot(fx.Space)
	if err != nil {
		t.Fatal(err)
	}
	store := fx.SemanticSnapshot(t)
	if !reflect.DeepEqual(model, store) {
		t.Fatalf("read fixture differs before probe: Mem=%#v Lua=%#v", model, store)
	}
}

func readLuaRaw(t *testing.T, fx *tsetFixture, plan ReadPlan) []byte {
	t.Helper()
	encoded, err := EncodeReadPlan(plan)
	if err != nil {
		t.Fatalf("encode read plan: %v", err)
	}
	wire, err := fx.Client.FCallRO(context.Background(), "ns_tset_read", []string{},
		Version, string(encoded)).Result()
	if err != nil {
		t.Fatalf("Lua read returned Redis error: %v", err)
	}
	switch value := wire.(type) {
	case string:
		return []byte(value)
	case []byte:
		return value
	default:
		t.Fatalf("Lua read returned %T, want JSON bulk string", wire)
		return nil
	}
}

func bigReadIDs() []string {
	ids := make([]string, 24)
	for i := range ids {
		ids[i] = "existing"
	}
	return ids
}

func readContractPlan(space string, firstSmall bool) ReadPlan {
	queries := make([]ReadQuery, 0, 2)
	if firstSmall {
		queries = append(queries, ReadQuery{Kind: "ids", Table: "work",
			IDs: []string{"existing"}, Fields: []string{}})
	}
	queries = append(queries, ReadQuery{Kind: "ids", Table: "work",
		IDs: bigReadIDs(), Fields: []string{"blob"}})
	return ReadPlan{Epoch: "0", Space: space, Mode: "atomic", Queries: queries}
}

func checkReadRefusalPair(t *testing.T, fx *tsetFixture, mem *Mem,
	plan ReadPlan, code, budget string, queryIndex *int) {
	t.Helper()
	beforeMem, err := mem.Snapshot(fx.Space)
	if err != nil {
		t.Fatal(err)
	}
	beforeRedis := commitProbeImage(t, fx.Client)
	memReply, memErr := mem.Read(context.Background(), plan)
	var memRef *Refusal
	if !errors.As(memErr, &memRef) || memRef.Code != code {
		t.Fatalf("Mem read: reply=%+v err=%v, want %s", memReply, memErr, code)
	}
	if len(memReply.Answers) != 0 {
		t.Fatalf("Mem refusal leaked %d answers", len(memReply.Answers))
	}
	raw := readLuaRaw(t, fx, plan)
	var luaRef Refusal
	if err := json.Unmarshal(raw, &luaRef); err != nil || luaRef.Code != code || luaRef.Status != "refused" {
		t.Fatalf("Lua read: raw=%q err=%v, want %s", raw, err, code)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	if _, exists := object["answers"]; exists {
		t.Fatalf("Lua refusal leaked answers: %s", raw)
	}
	if memRef.Detail.Budget != budget || luaRef.Detail.Budget != budget {
		t.Fatalf("budget differs: Mem=%+v Lua=%+v", memRef.Detail, luaRef.Detail)
	}
	if (memRef.Detail.QueryIndex == nil) != (queryIndex == nil) ||
		(luaRef.Detail.QueryIndex == nil) != (queryIndex == nil) {
		t.Fatalf("query index presence differs: Mem=%+v Lua=%+v", memRef.Detail, luaRef.Detail)
	}
	if queryIndex != nil && (*memRef.Detail.QueryIndex != *queryIndex ||
		*luaRef.Detail.QueryIndex != *queryIndex) {
		t.Fatalf("query index differs: Mem=%+v Lua=%+v", memRef.Detail, luaRef.Detail)
	}
	afterMem, err := mem.Snapshot(fx.Space)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeMem, afterMem) {
		t.Fatal("read refusal changed Mem whole state")
	}
	if afterRedis := commitProbeImage(t, fx.Client); !reflect.DeepEqual(beforeRedis, afterRedis) {
		t.Fatal("read refusal changed Redis whole-key TYPE/DUMP image")
	}
}

func TestAtomicReadNeverReturnsPartial(t *testing.T) {
	t.Parallel()
	fx, mem := readContractFixture(t)
	plan := readContractPlan(fx.Space, true)
	first := ReadPlan{Epoch: "0", Space: fx.Space, Mode: "atomic",
		Queries: []ReadQuery{plan.Queries[0]}}
	memOK, err := mem.Read(context.Background(), first)
	if err != nil || len(memOK.Answers) != 1 || len(memOK.Answers[0].Records) != 1 ||
		memOK.Answers[0].Records[0].ID != "existing" {
		t.Fatalf("first query alone failed in Mem: reply=%+v err=%v", memOK, err)
	}
	var luaOK ReadReply
	if err := json.Unmarshal(readLuaRaw(t, fx, first), &luaOK); err != nil ||
		luaOK.Status != "read" || len(luaOK.Answers) != 1 ||
		len(luaOK.Answers[0].Records) != 1 ||
		luaOK.Answers[0].Records[0].ID != "existing" {
		t.Fatalf("first query alone failed in Lua: reply=%+v err=%v", luaOK, err)
	}
	index := 1
	checkReadRefusalPair(t, fx, mem, plan, "BUDGET", "encoded_reply", &index)
}

func TestRefuseBUDGET(t *testing.T) {
	t.Parallel()
	fx, mem := readContractFixture(t)
	index := 0
	checkReadRefusalPair(t, fx, mem, readContractPlan(fx.Space, false),
		"BUDGET", "encoded_reply", &index)
}

func TestRefuseEPOCHGONE(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	mem := NewMem()
	if err := mem.DefineTable(fx.Space, "work", TableDefinition{
		Columns: []string{"c"}, MemberPrefix: fx.Space + "member:work:",
		EpochKey: fx.Space + "sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatal(err)
	}
	// Construct the post-advance state during fixture setup, then remove the
	// historical snapshot before loading the read-only runtime.
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
	if err := mem.SetActiveEpoch(fx.Space, "1"); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.Del(ctx, fx.Space+"sprint:epoch@0",
		fixtureDefinitionKey(fx.Space, "work", "0")).Err(); err != nil {
		t.Fatal(err)
	}
	mem.mu.Lock()
	delete(mem.spaces[fx.Space].epochs, "0")
	mem.mu.Unlock()
	fx.Activate(t)
	assertReadFixtureParity(t, fx, mem)
	plan := ReadPlan{Epoch: "0", Space: fx.Space, Mode: "atomic",
		Queries: []ReadQuery{{Kind: "rows", Table: "work"}}}
	checkReadRefusalPair(t, fx, mem, plan, "EPOCHGONE", "", nil)
}
