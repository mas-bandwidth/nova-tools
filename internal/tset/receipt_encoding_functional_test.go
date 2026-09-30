//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// The target Redis runtime decides the actual cjson receipt bytes. These
// characters distinguish its encoding from Go's encoding/json defaults, so a
// source-only approximation cannot make the admission counters pass this test.
func TestReceiptEncodingBudgetParity(t *testing.T) {
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
	fx.Activate(t)
	ctx := context.Background()
	store := NewRedis(fx.Client)
	op, intent := "encoding-proof", "stable encoding intent"
	result := "<>&/\u2028\u2029\x7f\x01é日"
	step := Step{Epoch: "0", Space: fx.Space, Op: &op, Intent: &intent,
		Result: result, Entries: []Entry{}}
	memReply, err := mem.Step(ctx, step)
	if err != nil {
		t.Fatalf("Mem receipt step: %v", err)
	}
	luaReply, err := store.Step(ctx, step)
	if err != nil {
		t.Fatalf("Redis receipt step: %v", err)
	}
	if memReply.Status != "ok" || luaReply.Status != "ok" ||
		memReply.Result != result || luaReply.Result != result {
		t.Fatalf("receipt replies differ: Mem=%+v Lua=%+v", memReply, luaReply)
	}
	saved := mem.space(fx.Space).receipts["0"][op]
	modelBytes := encodeMemReceipt(saved)
	redisBytes, err := fx.Client.HGet(ctx, fixtureDoneKey(fx.Space, "0"), op).Bytes()
	if err != nil {
		t.Fatalf("fetch actual Redis receipt: %v", err)
	}
	if len(modelBytes) != len(redisBytes) {
		t.Fatalf("encoded receipt length: Mem=%d Redis=%d", len(modelBytes), len(redisBytes))
	}
	var redisReceipt memReceipt
	if err := json.Unmarshal(redisBytes, &redisReceipt); err != nil ||
		!reflect.DeepEqual(redisReceipt, saved) {
		t.Fatalf("persisted receipt content differs: Redis=%+v Mem=%+v err=%v",
			redisReceipt, saved, err)
	}
	var memWork, luaWork struct {
		PlannedCommands int `json:"planned_commands"`
		PlannedBytes    int `json:"planned_argv_bytes"`
	}
	if err := json.Unmarshal(memReply.Counters, &memWork); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(luaReply.Counters, &luaWork); err != nil {
		t.Fatal(err)
	}
	wantPlannedBytes := len("HSET") + len(fixtureDoneKey(fx.Space, "0")) +
		len(op) + len(redisBytes)
	if memWork.PlannedCommands != 1 || luaWork.PlannedCommands != 1 ||
		memWork.PlannedBytes != wantPlannedBytes || luaWork.PlannedBytes != wantPlannedBytes {
		t.Fatalf("receipt-only HSET budget: Mem=%+v Lua=%+v want one command/%d bytes",
			memWork, luaWork, wantPlannedBytes)
	}

	readFetched := func(storeRead func(ReadPlan) (ReadReply, error), identity DoneIdentity, wantStatus string) int {
		t.Helper()
		plan := ReadPlan{Epoch: "0", Space: fx.Space, Mode: "atomic",
			Queries: []ReadQuery{{Kind: "done", Ops: []DoneIdentity{identity}}}}
		reply, err := storeRead(plan)
		if err != nil || reply.Status != "read" || len(reply.Answers) != 1 ||
			len(reply.Answers[0].Done) != 1 {
			t.Fatalf("done read for %s: reply=%+v err=%v", identity.Op, reply, err)
		}
		slot := reply.Answers[0].Done[0]
		if slot.Status != wantStatus ||
			(wantStatus == "match" && (slot.Receipt == nil || slot.Receipt.Result != result)) {
			t.Fatalf("done slot for %s: %+v, want %s with original result", identity.Op, slot, wantStatus)
		}
		var work struct {
			FetchedBytes int `json:"fetched_bytes"`
		}
		if err := json.Unmarshal(reply.Counters, &work); err != nil {
			t.Fatal(err)
		}
		return work.FetchedBytes
	}
	memRead := func(plan ReadPlan) (ReadReply, error) { return mem.Read(ctx, plan) }
	luaRead := func(plan ReadPlan) (ReadReply, error) { return store.Read(ctx, plan) }
	matching := DoneIdentity{Epoch: "0", Op: op, IntentDigest: intentDigest(intent)}
	absent := DoneIdentity{Epoch: "0", Op: "encoding-absent", IntentDigest: intentDigest(intent)}
	memDelta := readFetched(memRead, matching, "match") - readFetched(memRead, absent, "absent")
	luaDelta := readFetched(luaRead, matching, "match") - readFetched(luaRead, absent, "absent")
	if memDelta != len(modelBytes) || luaDelta != len(redisBytes) {
		t.Fatalf("done fetched-byte delta: Mem=%d Lua=%d receipt=%d",
			memDelta, luaDelta, len(redisBytes))
	}
}
