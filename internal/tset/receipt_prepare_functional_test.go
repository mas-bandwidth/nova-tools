//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The callback calls the production planners and S.prepare, then reports the
// frozen command list without S.commit. It makes the receipt ordering visible
// while leaving the isolated Redis image unchanged.
const receiptOrderProbeLua = `
redis.register_function('ns_tset_receipt_order_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=2 then return S.json.encode(S.refuse('ARGS')) end
  local ctx,err=S.open(args[1],args[2]);if err then return S.json.encode(err) end
  if ctx.replay then return S.json.encode(S.refuse('REQUEST')) end
  local table_plan;table_plan,err=S.plan(ctx);if err then return S.json.encode(err) end
  local log_plan={commands={},first_seq='0',last_seq='0',line_count=0,about_appends=0}
  if ctx.profile~='l1_only' then
    log_plan,err=NS.tlog.plan(ctx,table_plan);if err then return S.json.encode(err) end
  end
  local prepared;prepared,err=S.prepare(ctx,table_plan,log_plan,{})
  if err then return S.json.encode(err) end
  local commands=S.array()
  for i,argv in ipairs(prepared.commands) do
    commands[i]={verb=argv[1],key=argv[2],field=argv[3]}
  end
  return S.json.encode({status='prepared',commands=commands})
end)
`

type receiptOrderProbeReply struct {
	Status   string `json:"status"`
	Code     string `json:"code"`
	Commands []struct {
		Verb  string `json:"verb"`
		Key   string `json:"key"`
		Field string `json:"field"`
	} `json:"commands"`
}

func TestCompositionReceiptLast(t *testing.T) {
	t.Parallel()
	receiptOrderWitness(t, false)
}

// This gate deliberately loads the actual composed profile. It remains a
// separate red integration gate until the real Layer-2 fragment is present;
// the L1-only result above is not evidence for composed execution.
func TestCompositionReceiptLastComposed(t *testing.T) {
	t.Parallel()
	receiptOrderWitness(t, true)
}

func receiptOrderWitness(t *testing.T, composed bool) {
	t.Helper()
	var fx *tsetFixture
	if composed {
		fx = newComposedTSetFixture(t)
	} else {
		fx = newTSetFixture(t)
	}
	fx.Define(t, "work", "cards")
	fx.AddRow(t, "work", "r", 0)
	fx.ActivateWithLua(t, receiptOrderProbeLua)
	op, intent := "receipt-order", "create card for receipt order"
	request := map[string]any{
		"epoch": "0", "space": fx.Space, "op": op, "intent": intent,
		"entries": []any{map[string]any{"kind": "create", "t": "work", "to": "r:cards",
			"ids": []string{"card"}, "scores": []string{"1"}, "about": []string{"primary"}}},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	before := commitProbeImage(t, fx.Client)
	wire, err := fx.Client.FCall(context.Background(), "ns_tset_receipt_order_probe", []string{},
		Version, string(raw)).Text()
	if err != nil {
		t.Fatal(err)
	}
	var got receiptOrderProbeReply
	if err := json.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatalf("receipt order probe %q: %v", wire, err)
	}
	if got.Status != "prepared" || len(got.Commands) < 2 {
		t.Fatalf("receipt order probe = %+v, want table effects then receipt", got)
	}
	doneKey := fixtureDoneKey(fx.Space, "0")
	last := got.Commands[len(got.Commands)-1]
	if last.Verb != "HSET" || last.Key != doneKey || last.Field != op {
		t.Fatalf("last frozen command = %+v, want HSET %q %q", last, doneKey, op)
	}
	tableEffect, logEffect := false, false
	for i, command := range got.Commands[:len(got.Commands)-1] {
		if command.Key == doneKey {
			t.Fatalf("receipt write appeared at command %d before final command", i)
		}
		if strings.HasPrefix(command.Key, fx.Space+"table:work") ||
			strings.HasPrefix(command.Key, fx.Space+"member:work:") {
			tableEffect = true
		}
		if command.Verb == "XADD" {
			logEffect = true
		}
	}
	if !tableEffect || (composed && !logEffect) {
		t.Fatalf("frozen commands lack table effect or composed log effect: %+v", got.Commands)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatal("prepare-only command-order probe changed the whole Redis TYPE/DUMP image")
	}
}

const receiptCapProbeLua = `
redis.register_function('ns_tset_receipt_cap_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=2 then return S.json.encode(S.refuse('ARGS')) end
  local ctx,err=S.open(args[1],args[2]);if err then return S.json.encode(err) end
  if ctx.replay then return S.json.encode(S.refuse('REQUEST')) end
  local reply={status='ok',epoch_before=ctx.request_epoch,epoch_after=ctx.request_epoch,
    first_seq='0',last_seq='0',changed=0,result=''}
  local base;base,err=S.receipt_prepare(ctx,reply)
  if err then return S.json.encode(err) end
  local base_bytes=#base.argv[4]
  reply.result=string.rep('x',32768-base_bytes)
  local exact;exact,err=S.receipt_prepare(ctx,reply)
  if err then return S.json.encode(err) end
  local exact_bytes=#exact.argv[4]
  reply.result=reply.result..'x'
  local oversized;oversized,err=S.receipt_prepare(ctx,reply)
  return S.json.encode({status='probed',base=base_bytes,exact=exact_bytes,
    oversized_prepared=oversized~=nil,oversized_code=err and err.code or ''})
end)
`

func TestReceiptSizeBoundary(t *testing.T) {
	t.Parallel()
	t.Run("private production prepare accepts 32768 and refuses 32769 before write", func(t *testing.T) {
		fx := newTSetFixture(t)
		fx.Define(t, "work", "cards")
		fx.ActivateWithLua(t, receiptCapProbeLua)
		request := `{"epoch":"0","space":"` + fx.Space + `","op":"cap","intent":"stable","entries":[]}`
		before := commitProbeImage(t, fx.Client)
		wire, err := fx.Client.FCall(context.Background(), "ns_tset_receipt_cap_probe", []string{},
			Version, request).Text()
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Status            string `json:"status"`
			Base              int    `json:"base"`
			Exact             int    `json:"exact"`
			OversizedPrepared bool   `json:"oversized_prepared"`
			OversizedCode     string `json:"oversized_code"`
		}
		if err := json.Unmarshal([]byte(wire), &got); err != nil {
			t.Fatalf("decode receipt cap probe %q: %v", wire, err)
		}
		if got.Status != "probed" || got.Base <= 0 || got.Exact != 32768 ||
			got.OversizedPrepared || got.OversizedCode != "LIMIT" {
			t.Fatalf("receipt cap probe = %+v, want exact cap then LIMIT", got)
		}
		if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
			t.Fatal("receipt cap prepare probe changed the whole Redis TYPE/DUMP image")
		}
	})

	t.Run("maximum public result persists below receipt cap", func(t *testing.T) {
		fx := newTSetFixture(t)
		fx.Define(t, "work", "cards")
		fx.Activate(t)
		op, intent := "public-max", "maximum public caller result"
		result := strings.Repeat("\x01", MaxResultBytes)
		step := Step{Epoch: "0", Space: fx.Space, Op: &op, Intent: &intent,
			Result: result, Entries: []Entry{}}
		reply, err := newFixtureRedis(t, fx.Client).Step(context.Background(), step)
		if err != nil || reply.Status != "ok" {
			t.Fatalf("maximum public result step = %+v / %v", reply, err)
		}
		stored, err := fx.Client.HGet(context.Background(), fixtureDoneKey(fx.Space, "0"), op).Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) > 32768 {
			t.Fatalf("maximum valid public receipt = %d bytes, cap 32768", len(stored))
		}
		var compact struct {
			Status string `json:"status"`
			Result string `json:"result"`
		}
		if err := json.Unmarshal([]byte(stored), &compact); err != nil || compact.Status != "ok" ||
			compact.Result != result {
			t.Fatalf("maximum public receipt content = %+v / %v", compact, err)
		}
	})
}
