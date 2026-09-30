//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// Functional witnesses for Stella's R1, R2, and R3 contract invariants
// (notes stella-1f9d50b66e3f and stella-54bf705d7a69) executed against
// live Redis 8.10.2 and the Mem twin.

func r1r2r3FunctionalFixture(t *testing.T, space string) (*tsetFixture, *Mem) {
	t.Helper()
	fx := newTSetFixtureSpace(t, fn.TSetStandalone, space)
	fx.Define(t, "work", "cards")
	fx.AddRow(t, "work", "r", 0)
	mem := NewMem()
	if err := mem.DefineTable(fx.Space, "work", TableDefinition{
		Columns:      []string{"cards"},
		MemberPrefix: fx.Space + "member:work:",
		EpochKey:     fx.Space + "sprint:epoch",
		EpochField:   "n",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.SeedRow(fx.Space, "work", "0", "r", "0"); err != nil {
		t.Fatal(err)
	}
	fx.Activate(t)
	settlementParity(t, fx, mem)
	return fx, mem
}

// TestPropertyContractR1ReceiptChangedBoundOver2000Functional verifies that
// a step creating 2,000 members plus 1 table property records changed=2001,
// and that subsequent exact replay and done lookup query succeed without
// refusal or DRIFT in both Lua and Mem.
func TestPropertyContractR1ReceiptChangedBoundOver2000Functional(t *testing.T) {
	t.Parallel()
	fx, mem := r1r2r3FunctionalFixture(t, "{r1-bound}:")
	ctx := context.Background()

	ids := make([]string, MaxMemberCandidates)
	scores := make([]string, MaxMemberCandidates)
	for i := 0; i < MaxMemberCandidates; i++ {
		ids[i] = fmt.Sprintf("card-%04d", i)
		scores[i] = "1"
	}
	propVal := "active"
	op, intent := "op-2000-members-prop", "step with 2000 members and table property"
	step := Step{
		Epoch:  "0",
		Space:  fx.Space,
		Op:     &op,
		Intent: &intent,
		Entries: []Entry{
			{
				Kind:   "create",
				Table:  "work",
				To:     "r:cards",
				IDs:    ids,
				Scores: scores,
			},
			{
				Kind:  "prop",
				Table: "work",
				Name:  "status",
				Value: &propVal,
			},
		},
	}

	// 1. Fresh step commit: both Lua and Mem must agree on changed = 2001.
	fresh := settlementStep(t, fx, mem, step)
	if fresh.Status != "ok" || fresh.Replay || fresh.Changed <= MaxMemberCandidates {
		t.Fatalf("fresh reply = %+v, want status ok, replay false, changed > %d", fresh, MaxMemberCandidates)
	}
	if fresh.Changed != MaxMemberCandidates+1 {
		t.Fatalf("fresh changed = %d, want %d", fresh.Changed, MaxMemberCandidates+1)
	}

	// 2. Done query verification: verify that done slot receipt matches changed = 2001.
	settlementDone(t, fx, mem, "0", op, intent, "ok", "")
	plan := ReadPlan{
		Epoch: "0", Space: fx.Space, Mode: "atomic",
		Queries: []ReadQuery{{
			Kind: "done",
			Ops:  []DoneIdentity{{Epoch: "0", Op: op, IntentDigest: intentDigest(intent)}},
		}},
	}
	for name, read := range map[string]func() (ReadReply, error){
		"Lua": func() (ReadReply, error) { return newFixtureRedis(t, fx.Client).Read(ctx, plan) },
		"Mem": func() (ReadReply, error) { return mem.Read(ctx, plan) },
	} {
		reply, err := read()
		if err != nil || len(reply.Answers) != 1 || len(reply.Answers[0].Done) != 1 {
			t.Fatalf("%s done reply = %+v / %v", name, reply, err)
		}
		slot := reply.Answers[0].Done[0]
		if slot.Receipt == nil || slot.Receipt.Changed != fresh.Changed {
			t.Fatalf("%s done slot receipt changed = %v, want %d", name, slot.Receipt, fresh.Changed)
		}
	}

	// 3. Idempotent replay: exact same request with same op and intent.
	beforeRedis := commitProbeImage(t, fx.Client)
	beforeMem := settlementMemImage(t, mem, fx.Space)
	replay := settlementStep(t, fx, mem, step)
	if !replay.Replay || replay.Status != "ok" || replay.Changed != fresh.Changed {
		t.Fatalf("replay = %+v, want replay true, status ok, changed %d", replay, fresh.Changed)
	}
	settlementUnchanged(t, fx, mem, beforeRedis, beforeMem)

	// 4. Stored raw Redis receipt satisfies independent receipt invariant.
	rawReceipt, err := fx.Client.HGet(ctx, fixtureDoneKey(fx.Space, "0"), op).Result()
	if err != nil {
		t.Fatalf("fetch stored Redis receipt: %v", err)
	}
	if !independentReceipt(rawReceipt, "0", map[string]bool{"0": true}) {
		t.Fatalf("stored receipt failed independentReceipt invariant: %s", rawReceipt)
	}
}

// TestPropertyContractR2TouchedSuccessorEmptinessPreflightFunctional verifies
// that advance into a touched successor property hash checks emptiness:
// (a) advance + equal-property no-op with preseeded x=v on successor refuses DRIFT.
// (b) advance + absent-name propguard with preseeded x=v on successor refuses DRIFT.
// Both refuse BEFORE any mutation and leave whole Redis and Mem images completely unchanged.
// (c) advance into empty successor succeeds and writes properties normally.
func TestPropertyContractR2TouchedSuccessorEmptinessPreflightFunctional(t *testing.T) {
	t.Parallel()

	t.Run("advance_equal_prop_noop_refuses_drift", func(t *testing.T) {
		t.Parallel()
		fx, mem := r1r2r3FunctionalFixture(t, "{r2-equal}:")
		ctx := context.Background()

		// Preseed successor property hash in Redis and Mem for epoch 1: x=v
		successorPropsKey := fixtureTablePrefix(fx.Space, "work", "1") + ":props"
		if err := fx.Client.HSet(ctx, successorPropsKey, "x", "v").Err(); err != nil {
			t.Fatalf("preseed Redis successor property: %v", err)
		}
		if err := mem.SeedProperty(fx.Space, "work", "1", "x", "v"); err != nil {
			t.Fatalf("preseed Mem successor property: %v", err)
		}

		beforeRedis := commitProbeImage(t, fx.Client)
		beforeMem := settlementMemImage(t, mem, fx.Space)

		propVal := "v"
		op, intent := "advance-equal-prop-noop", "advance with equal prop no-op"
		step := Step{
			Epoch:  "0",
			Space:  fx.Space,
			Op:     &op,
			Intent: &intent,
			Entries: []Entry{
				{Kind: "advance", AdvanceFrom: "0"},
				{Kind: "prop", Table: "work", Name: "x", Value: &propVal},
			},
		}

		// Execute on Mem
		_, memErr := mem.Step(ctx, step)
		memRef := requireRefusal(t, memErr, "DRIFT")
		if memRef.Code != "DRIFT" {
			t.Fatalf("Mem refusal = %s, want DRIFT", memRef.Code)
		}

		// Execute on Redis (Lua)
		store := newFixtureRedis(t, fx.Client)
		_, luaErr := store.Step(ctx, step)
		luaRef := requireRefusal(t, luaErr, "DRIFT")
		if luaRef.Code != "DRIFT" {
			t.Fatalf("Lua refusal = %s, want DRIFT", luaRef.Code)
		}

		// Verify whole images remain completely unchanged
		settlementUnchanged(t, fx, mem, beforeRedis, beforeMem)

		// Verify active epoch remains 0 in both Redis and Mem
		activeEpoch, err := fx.Client.HGet(ctx, fx.Space+"sprint:epoch", "n").Result()
		if err != nil || activeEpoch != "0" {
			t.Fatalf("Redis active epoch = %q (err %v), want 0", activeEpoch, err)
		}
		afterMem, err := mem.Snapshot(fx.Space)
		if err != nil || afterMem.ActiveEpoch != "0" {
			t.Fatalf("Mem active epoch = %q, want 0", afterMem.ActiveEpoch)
		}
	})

	t.Run("advance_absent_propguard_refuses_drift", func(t *testing.T) {
		t.Parallel()
		fx, mem := r1r2r3FunctionalFixture(t, "{r2-absent}:")
		ctx := context.Background()

		// Preseed successor property hash in Redis and Mem for epoch 1: x=v
		successorPropsKey := fixtureTablePrefix(fx.Space, "work", "1") + ":props"
		if err := fx.Client.HSet(ctx, successorPropsKey, "x", "v").Err(); err != nil {
			t.Fatalf("preseed Redis successor property: %v", err)
		}
		if err := mem.SeedProperty(fx.Space, "work", "1", "x", "v"); err != nil {
			t.Fatalf("preseed Mem successor property: %v", err)
		}

		beforeRedis := commitProbeImage(t, fx.Client)
		beforeMem := settlementMemImage(t, mem, fx.Space)

		op, intent := "advance-absent-propguard", "advance with absent propguard"
		step := Step{
			Epoch:  "0",
			Space:  fx.Space,
			Op:     &op,
			Intent: &intent,
			Entries: []Entry{
				{Kind: "advance", AdvanceFrom: "0"},
				{Kind: "propguard", Table: "work", Name: "absent_name", Value: nil},
			},
		}

		// Execute on Mem
		_, memErr := mem.Step(ctx, step)
		memRef := requireRefusal(t, memErr, "DRIFT")
		if memRef.Code != "DRIFT" {
			t.Fatalf("Mem refusal = %s, want DRIFT", memRef.Code)
		}

		// Execute on Redis (Lua)
		store := newFixtureRedis(t, fx.Client)
		_, luaErr := store.Step(ctx, step)
		luaRef := requireRefusal(t, luaErr, "DRIFT")
		if luaRef.Code != "DRIFT" {
			t.Fatalf("Lua refusal = %s, want DRIFT", luaRef.Code)
		}

		// Verify whole images remain completely unchanged
		settlementUnchanged(t, fx, mem, beforeRedis, beforeMem)

		// Verify active epoch remains 0 in both Redis and Mem
		activeEpoch, err := fx.Client.HGet(ctx, fx.Space+"sprint:epoch", "n").Result()
		if err != nil || activeEpoch != "0" {
			t.Fatalf("Redis active epoch = %q (err %v), want 0", activeEpoch, err)
		}
		afterMem, err := mem.Snapshot(fx.Space)
		if err != nil || afterMem.ActiveEpoch != "0" {
			t.Fatalf("Mem active epoch = %q, want 0", afterMem.ActiveEpoch)
		}
	})

	t.Run("advance_empty_successor_positive_control", func(t *testing.T) {
		t.Parallel()
		fx, mem := r1r2r3FunctionalFixture(t, "{r2-empty}:")
		ctx := context.Background()

		propVal := "v"
		op, intent := "advance-empty-successor", "advance into clean empty successor"
		step := Step{
			Epoch:  "0",
			Space:  fx.Space,
			Op:     &op,
			Intent: &intent,
			Entries: []Entry{
				{Kind: "advance", AdvanceFrom: "0"},
				{Kind: "prop", Table: "work", Name: "x", Value: &propVal},
			},
		}

		// Execute on both Redis and Mem
		reply := settlementStep(t, fx, mem, step)
		if reply.Status != "ok" || reply.EpochAfter != "1" || reply.Changed != 1 {
			t.Fatalf("advance reply = %+v, want status ok, epoch_after 1, changed 1", reply)
		}

		// Verify property was stored in epoch 1 in both stores
		storedLua, err := fx.Client.HGet(ctx, fixtureTablePrefix(fx.Space, "work", "1")+":props", "x").Result()
		if err != nil || storedLua != "v" {
			t.Fatalf("stored Redis property x = %q (err %v), want v", storedLua, err)
		}
		afterMem, err := mem.Snapshot(fx.Space)
		if err != nil || afterMem.Epochs["1"].Tables["work"].Props["x"] != "v" {
			t.Fatalf("stored Mem property x = %q, want v", afterMem.Epochs["1"].Tables["work"].Props["x"])
		}
	})
}

const r3AccountingProbe = `
local S = NS.tset
local orig_readcmd = S.readcmd
S.readcmd = function(ctx, descriptor, reserve_bytes, probe_kind)
  local val, err = orig_readcmd(ctx, descriptor, reserve_bytes, probe_kind)
  if val and descriptor and descriptor.argv and descriptor.argv[1] == 'TIME' then
    local usec_len = #tostring(val[2])
    if usec_len < 6 then
      ctx.budget.fetched_bytes = ctx.budget.fetched_bytes + (6 - usec_len)
    end
  end
  return val, err
end

redis.register_function('ns_tset_r3_hlen_accounting_witness', function(keys, args)
  if #args ~= 1 then return redis.error_reply('need key') end
  local count = redis.call('HLEN', args[1])
  local fetched = S.payload_bytes(count)
  return cjson.encode({count = count, fetched_bytes = fetched})
end)
`

type r3HLENWitness struct {
	Count        int `json:"count"`
	FetchedBytes int `json:"fetched_bytes"`
}

// TestPropertyContractR3MemLuaProbeAndByteParityFunctional verifies accounting
// parity between Mem and Lua for HLEN property probes:
// 1. For a table with 0 existing properties, HLEN returns 0 -> len("0") = 1 byte.
// 2. For a table with 10 existing properties, HLEN returns 10 -> len("10") = 2 bytes.
// Mem accounts for exactly 1 cell probe and 1 or 2 fetched bytes.
// A deterministic per-command accounting witness confirms Lua's HLEN payload bytes (1 vs 2).
// Normalizing observed TIME payload ensures Lua's fetched bytes difference between 10-prop and
// 0-prop cases deterministically matches Mem's delta (1 byte) without microsecond-width flakiness.
func TestPropertyContractR3MemLuaProbeAndByteParityFunctional(t *testing.T) {
	t.Parallel()

	runCase := func(t *testing.T, space string, preseedCount int) (int, int, int, int, r3HLENWitness) {
		fx := newTSetFixtureSpace(t, fn.TSetStandalone, space)
		fx.Define(t, "work", "cards")
		fx.AddRow(t, "work", "r", 0)
		mem := NewMem()
		if err := mem.DefineTable(fx.Space, "work", TableDefinition{
			Columns:      []string{"cards"},
			MemberPrefix: fx.Space + "member:work:",
			EpochKey:     fx.Space + "sprint:epoch",
			EpochField:   "n",
		}); err != nil {
			t.Fatal(err)
		}
		if err := mem.SeedRow(fx.Space, "work", "0", "r", "0"); err != nil {
			t.Fatal(err)
		}
		fx.ActivateWithLua(t, r3AccountingProbe)
		settlementParity(t, fx, mem)
		ctx := context.Background()

		propsKey := fixtureTablePrefix(fx.Space, "work", "0") + ":props"
		for i := 0; i < preseedCount; i++ {
			pName, pVal := fmt.Sprintf("pre_%02d", i), "v"
			if err := fx.Client.HSet(ctx, propsKey, pName, pVal).Err(); err != nil {
				t.Fatalf("preseed Redis prop %s: %v", pName, err)
			}
			if err := mem.SeedProperty(fx.Space, "work", "0", pName, pVal); err != nil {
				t.Fatalf("preseed Mem prop %s: %v", pName, err)
			}
		}

		// Witness deterministic per-command Lua accounting for HLEN on this key.
		witnessWire, err := fx.Client.FCall(ctx, "ns_tset_r3_hlen_accounting_witness", nil, propsKey).Text()
		if err != nil {
			t.Fatalf("witness HLEN probe: %v", err)
		}
		var witness r3HLENWitness
		if err := json.Unmarshal([]byte(witnessWire), &witness); err != nil {
			t.Fatalf("unmarshal witness: %v", err)
		}

		propVal := "brand_new_val"
		step := Step{
			Epoch: "0", Space: fx.Space,
			Entries: []Entry{
				{Kind: "prop", Table: "work", Name: "brand_new", Value: &propVal},
			},
		}

		// Execute on Mem
		memReply, memErr := mem.Step(ctx, step)
		if memErr != nil || memReply.Status != "ok" {
			t.Fatalf("Mem step failed: reply=%+v err=%v", memReply, memErr)
		}
		var memCounters struct {
			CellProbes   int `json:"cell_probes"`
			FetchedBytes int `json:"raw_fetched_bytes"`
		}
		if err := json.Unmarshal(memReply.Counters, &memCounters); err != nil {
			t.Fatalf("unmarshal Mem counters: %v", err)
		}

		// Execute on Lua (Redis)
		store := newFixtureRedis(t, fx.Client)
		luaReply, luaErr := store.Step(ctx, step)
		if luaErr != nil || luaReply.Status != "ok" {
			t.Fatalf("Lua step failed: reply=%+v err=%v", luaReply, luaErr)
		}
		var luaBudget struct {
			Cell         int `json:"cell"`
			FetchedBytes int `json:"fetched_bytes"`
		}
		if err := json.Unmarshal(luaReply.Counters, &luaBudget); err != nil {
			t.Fatalf("unmarshal Lua counters: %v", err)
		}

		return memCounters.CellProbes, memCounters.FetchedBytes, luaBudget.Cell, luaBudget.FetchedBytes, witness
	}

	memProbes0, memFetched0, luaCell0, luaFetched0, witness0 := runCase(t, "{r3-p00}:", 0)
	memProbes10, memFetched10, luaCell10, luaFetched10, witness10 := runCase(t, "{r3-p10}:", 10)

	// In Mem, exactly 1 cell probe for the HLEN lookup in both cases.
	if memProbes0 != 1 || memProbes10 != 1 {
		t.Fatalf("Mem cell_probes = %d (0 props), %d (10 props), want 1", memProbes0, memProbes10)
	}
	// In Mem, fetched bytes must be 1 for N=0 (len("0")=1) and 2 for N=10 (len("10")=2).
	if memFetched0 != 1 {
		t.Fatalf("Mem raw_fetched_bytes (0 props) = %d, want 1", memFetched0)
	}
	if memFetched10 != 2 {
		t.Fatalf("Mem raw_fetched_bytes (10 props) = %d, want 2", memFetched10)
	}

	// Lua cell count must be identical between 0-prop and 10-prop runs (1 HLEN probe).
	if luaCell0 != luaCell10 {
		t.Fatalf("Lua cell probe count changed: %d vs %d", luaCell0, luaCell10)
	}

	// Deterministic Lua per-command accounting witness:
	// HLEN of 0-property hash returns 0 -> S.payload_bytes(0) = 1.
	// HLEN of 10-property hash returns 10 -> S.payload_bytes(10) = 2.
	if witness0.Count != 0 || witness0.FetchedBytes != 1 {
		t.Fatalf("Lua HLEN witness (0 props) = %+v, want count=0, fetched_bytes=1", witness0)
	}
	if witness10.Count != 10 || witness10.FetchedBytes != 2 {
		t.Fatalf("Lua HLEN witness (10 props) = %+v, want count=10, fetched_bytes=2", witness10)
	}
	witnessDelta := witness10.FetchedBytes - witness0.FetchedBytes
	if witnessDelta != 1 {
		t.Fatalf("Lua HLEN witness delta = %d, want 1", witnessDelta)
	}

	// Lua fetched bytes delta between 10 props and 0 props with normalized TIME
	// payload matches Mem's delta and witness delta (2 - 1 = 1 byte).
	luaDelta := luaFetched10 - luaFetched0
	memDelta := memFetched10 - memFetched0
	if luaDelta != 1 || memDelta != 1 {
		t.Fatalf("HLEN reply byte delta mismatch: Lua=%d Mem=%d, want 1", luaDelta, memDelta)
	}
}
