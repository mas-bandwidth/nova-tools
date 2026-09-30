//go:build functional

package tset

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// The caller deliberately discards a successful response to model an unknown
// transport outcome. Every write and read still runs through the real Lua
// function and the independent Mem implementation.
func TestOutcomeUnknownFenceSettles(t *testing.T) {
	t.Parallel()
	for _, first := range []string{"original", "fence"} {
		t.Run(first+" wins before and after advance", func(t *testing.T) {
			fx, mem := settlementFixture(t)
			op, intent := "part", "stable semantic intent"
			original := Step{Epoch: "0", Space: fx.Space, Op: &op, Intent: &intent,
				Result: "applied", Entries: []Entry{{Kind: "create", Table: "work", To: "r:cards",
					IDs: []string{"card"}, Scores: []string{"1"}}}}
			fence := Step{Epoch: "0", Space: fx.Space, Op: &op, Intent: &intent,
				Fence: true, Entries: []Entry{}, Notes: []Note{}}
			winning := original
			status, result := "ok", "applied"
			if first == "fence" {
				winning, status, result = fence, "fenced", ""
			}

			// The response is lost to the modeled resume caller. Its receipt is
			// the authoritative result, including when a fence won first.
			fresh := settlementStep(t, fx, mem, winning)
			if fresh.Status != status || fresh.Replay || fresh.Result != result {
				t.Fatalf("first %s reply = %+v", first, fresh)
			}
			_, applied := settlementMemImage(t, mem, fx.Space).Epochs["0"].Tables["work"].Records["card"]
			if applied != (first == "original") {
				t.Fatalf("first %s left card applied=%t", first, applied)
			}
			settlementDone(t, fx, mem, "0", op, intent, status, result)
			before := commitProbeImage(t, fx.Client)
			beforeMem := settlementMemImage(t, mem, fx.Space)
			loser := fence
			if first == "fence" {
				loser = original
			}
			for _, resend := range []Step{winning, loser} {
				replay := settlementStep(t, fx, mem, resend)
				if !replay.Replay || replay.Status != status || replay.Result != result {
					t.Fatalf("same-identity resend after %s won = %+v", first, replay)
				}
				settlementUnchanged(t, fx, mem, before, beforeMem)
			}
			// In the fence-first case, the first resend is crash-after-fence
			// recovery: no original was sent and the fresh fence reply was lost.
			settlementAdvance(t, fx, mem)
			settlementDone(t, fx, mem, "1", op, intent, status, result)
			before = commitProbeImage(t, fx.Client)
			beforeMem = settlementMemImage(t, mem, fx.Space)
			for _, resend := range []Step{loser, winning} {
				replay := settlementStep(t, fx, mem, resend)
				if !replay.Replay || replay.Status != status || replay.Result != result ||
					replay.EpochBefore != "0" || replay.EpochAfter != "0" {
					t.Fatalf("same-identity resend after advance = %+v", replay)
				}
				settlementUnchanged(t, fx, mem, before, beforeMem)
			}
			settlementDone(t, fx, mem, "1", op, intent, status, result)
		})
	}
}

func TestEpochGoneCarriesActive(t *testing.T) {
	t.Parallel()
	fx, mem := settlementFixture(t)
	op, intent := "historical", "done identity in retained epoch"
	settlementStep(t, fx, mem, Step{Epoch: "0", Space: fx.Space, Op: &op, Intent: &intent,
		Entries: []Entry{}})
	settlementAdvance(t, fx, mem)

	// This is a controlled fault in the isolated fixture, after a real
	// advance: the epoch-0 marker is gone while its done hash remains.
	if err := fx.Client.Del(context.Background(), fx.Space+"sprint:epoch@0").Err(); err != nil {
		t.Fatal(err)
	}
	mem.mu.Lock()
	delete(mem.spaces[fx.Space].epochs, "0")
	mem.mu.Unlock()
	before := commitProbeImage(t, fx.Client)
	beforeMem := settlementMemImage(t, mem, fx.Space)
	plan := ReadPlan{Epoch: "1", Space: fx.Space, Mode: "atomic", Queries: []ReadQuery{{
		Kind: "done", Ops: []DoneIdentity{{Epoch: "0", Op: op, IntentDigest: intentDigest(intent)}},
	}}}
	for name, read := range map[string]func() (ReadReply, error){
		"Lua": func() (ReadReply, error) { return newFixtureRedis(t, fx.Client).Read(context.Background(), plan) },
		"Mem": func() (ReadReply, error) { return mem.Read(context.Background(), plan) },
	} {
		reply, err := read()
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != "EPOCHGONE" ||
			refusal.Detail.ActiveEpoch != "1" || len(reply.Answers) != 0 {
			t.Fatalf("%s done in gone epoch = %+v / %v, want EPOCHGONE active_epoch=1 and no answers", name, reply, err)
		}
	}
	settlementUnchanged(t, fx, mem, before, beforeMem)
}

func settlementFixture(t *testing.T) (*tsetFixture, *Mem) {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "cards")
	fx.AddRow(t, "work", "r", 0)
	mem := NewMem()
	if err := mem.DefineTable(fx.Space, "work", TableDefinition{
		Columns: []string{"cards"}, MemberPrefix: fx.Space + "member:work:",
		EpochKey: fx.Space + "sprint:epoch", EpochField: "n",
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

func settlementAdvance(t *testing.T, fx *tsetFixture, mem *Mem) {
	t.Helper()
	op, intent := "advance:0", "retain receipt settlement through epoch one"
	reply := settlementStep(t, fx, mem, Step{Epoch: "0", Space: fx.Space,
		Op: &op, Intent: &intent, Entries: []Entry{
			{Kind: "advance", AdvanceFrom: "0"},
			{Kind: "rows", Table: "work", Add: []string{"r"}},
		}})
	if reply.Status != "ok" || reply.EpochAfter != "1" {
		t.Fatalf("named settlement advance = %+v", reply)
	}
}

func settlementMemImage(t *testing.T, mem *Mem, space string) MemSnapshot {
	t.Helper()
	image, err := mem.Snapshot(space)
	if err != nil {
		t.Fatal(err)
	}
	return image
}

func settlementParity(t *testing.T, fx *tsetFixture, mem *Mem) {
	t.Helper()
	model := settlementMemImage(t, mem, fx.Space)
	store := fx.SemanticSnapshot(t)
	if !reflect.DeepEqual(model, store) {
		t.Fatalf("Lua/Mem semantic states differ: Lua=%#v Mem=%#v", store, model)
	}
}

func settlementStep(t *testing.T, fx *tsetFixture, mem *Mem, step Step) Reply {
	t.Helper()
	model, modelErr := mem.Step(context.Background(), step)
	lua, luaErr := newFixtureRedis(t, fx.Client).Step(context.Background(), step)
	if modelErr != nil || luaErr != nil || model.Status != lua.Status ||
		model.Replay != lua.Replay || model.EpochBefore != lua.EpochBefore ||
		model.EpochAfter != lua.EpochAfter || model.Changed != lua.Changed ||
		model.FirstSeq != lua.FirstSeq || model.LastSeq != lua.LastSeq ||
		model.Result != lua.Result {
		t.Fatalf("Lua/Mem step differs: Lua=%+v/%v Mem=%+v/%v", lua, luaErr, model, modelErr)
	}
	settlementParity(t, fx, mem)
	return lua
}

func settlementDone(t *testing.T, fx *tsetFixture, mem *Mem, readEpoch Decimal,
	op, intent, status, result string) {
	t.Helper()
	plan := ReadPlan{Epoch: readEpoch, Space: fx.Space, Mode: "atomic", Queries: []ReadQuery{{
		Kind: "done", Ops: []DoneIdentity{{Epoch: "0", Op: op, IntentDigest: intentDigest(intent)}},
	}}}
	before := commitProbeImage(t, fx.Client)
	beforeMem := settlementMemImage(t, mem, fx.Space)
	for name, read := range map[string]func() (ReadReply, error){
		"Lua": func() (ReadReply, error) { return newFixtureRedis(t, fx.Client).Read(context.Background(), plan) },
		"Mem": func() (ReadReply, error) { return mem.Read(context.Background(), plan) },
	} {
		reply, err := read()
		if err != nil || len(reply.Answers) != 1 || len(reply.Answers[0].Done) != 1 {
			t.Fatalf("%s done reply = %+v / %v", name, reply, err)
		}
		slot := reply.Answers[0].Done[0]
		if slot.Status != "match" || slot.IntentDigest != intentDigest(intent) ||
			slot.Receipt == nil || slot.Receipt.Status != status || slot.Receipt.Result != result ||
			slot.Receipt.EpochBefore != "0" || slot.Receipt.EpochAfter != "0" {
			t.Fatalf("%s done slot = %+v, want saved %s receipt", name, slot, status)
		}
		if status == "fenced" && (slot.Receipt.Changed != 0 ||
			slot.Receipt.FirstSeq != "0" || slot.Receipt.LastSeq != "0") {
			t.Fatalf("%s fenced done slot has effects: %+v", name, slot)
		}
	}
	settlementUnchanged(t, fx, mem, before, beforeMem)
}

func settlementUnchanged(t *testing.T, fx *tsetFixture, mem *Mem,
	before map[string]commitProbeKey, beforeMem MemSnapshot) {
	t.Helper()
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatal("read or replay changed the whole Redis TYPE/DUMP image")
	}
	if after := settlementMemImage(t, mem, fx.Space); !reflect.DeepEqual(beforeMem, after) {
		t.Fatal("read or replay changed the Mem snapshot")
	}
}

// TestReceiptChangedBoundOver2000WithProperties verifies that a step creating
// 2,000 members plus 1 or more table properties records changed > 2000 in its
// receipt and allows both exact replay and done query without refusal or DRIFT
// (addressing receipt poisoning when changed was bounded by member_candidates).
func TestReceiptChangedBoundOver2000WithProperties(t *testing.T) {
	t.Parallel()
	fx, mem := settlementFixture(t)
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
	fresh := settlementStep(t, fx, mem, step)
	if fresh.Status != "ok" || fresh.Replay || fresh.Changed <= MaxMemberCandidates {
		t.Fatalf("fresh reply = %+v, want status ok, replay false, changed > %d", fresh, MaxMemberCandidates)
	}
	if fresh.Changed != MaxMemberCandidates+1 {
		t.Fatalf("fresh changed = %d, want %d", fresh.Changed, MaxMemberCandidates+1)
	}

	settlementDone(t, fx, mem, "0", op, intent, "ok", "")

	// Direct query to verify the done slot receipt has the expected changed count.
	plan := ReadPlan{Epoch: "0", Space: fx.Space, Mode: "atomic", Queries: []ReadQuery{{
		Kind: "done", Ops: []DoneIdentity{{Epoch: "0", Op: op, IntentDigest: intentDigest(intent)}},
	}}}
	for name, read := range map[string]func() (ReadReply, error){
		"Lua": func() (ReadReply, error) { return newFixtureRedis(t, fx.Client).Read(context.Background(), plan) },
		"Mem": func() (ReadReply, error) { return mem.Read(context.Background(), plan) },
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

	before := commitProbeImage(t, fx.Client)
	beforeMem := settlementMemImage(t, mem, fx.Space)
	replay := settlementStep(t, fx, mem, step)
	if !replay.Replay || replay.Status != "ok" || replay.Changed != fresh.Changed {
		t.Fatalf("replay = %+v, want replay true, status ok, changed %d", replay, fresh.Changed)
	}
	settlementUnchanged(t, fx, mem, before, beforeMem)

	rawReceipt, err := fx.Client.HGet(context.Background(), fixtureDoneKey(fx.Space, "0"), op).Result()
	if err != nil {
		t.Fatalf("fetch stored Redis receipt: %v", err)
	}
	if !independentReceipt(rawReceipt, "0", map[string]bool{"0": true}) {
		t.Fatalf("stored receipt failed independentReceipt invariant: %s", rawReceipt)
	}
}
