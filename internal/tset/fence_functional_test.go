//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// The hook source is installed only in an L1 fixture. It provides a synthetic
// L2 boundary and changes the profile name solely to make an accidental route
// into L.plan observable. It does not claim real composed integration, whose
// L2 fragment has separate gates.
const fenceTripwireLua = `
redis.register_function('ns_tset_fence_tripwire', function(keys,args)
  if #keys~=0 or #args~=1 or args[1]~='arm' then return 'ARGS' end
  local S=NS.tset
  S.before=function(...) error('fence test: preplan hook called') end
  S.plan=function(...) error('fence test: table planner called') end
  NS.tlog={plan=function(...) error('fence test: synthetic Layer 2 planner called') end}
  S.profile='test-composed'
  return 'armed'
end)
`

// This private callback is an L1-only probe for the original-fence invariant.
// It changes the decoded request after S.open but deliberately never commits a
// returned plan. A refusal must happen before the marker change can cause any
// additional planner reads. It is not a production composition callback.
const fenceMutationProbeLua = `
redis.register_function('ns_tset_fence_mutation_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local ctx,err=S.open(args[1],args[2])
  if err then return S.json.encode(err) end
  if ctx.replay then return S.json.encode(S.refuse('REQUEST')) end
  local before=ctx.budget.store_commands
  local mode=args[3]
  if mode=='add' then ctx.request.fence=true
  elseif mode=='remove' then ctx.request.fence=nil
  elseif mode=='false' then ctx.request.fence=false
  elseif mode=='null' then ctx.request.fence=cjson.null
  else return S.json.encode(S.refuse('REQUEST')) end
  local plan,problem
  if mode=='add' then plan,problem=S.plan(ctx) else plan,problem=S.fence_prepare(ctx) end
  if problem then return S.json.encode({status=problem.status,code=problem.code,
    before=before,after=ctx.budget.store_commands,planned=plan~=nil}) end
  return S.json.encode({status='planned',before=before,after=ctx.budget.store_commands,
    planned=plan~=nil})
end)
`

// This private L1 fixture models an enclosing dispatcher that elects the
// fence helper immediately after open/replay. The changed profile and synthetic
// tlog tripwire prove that the helper did not enter any preplanner. It does not
// prove integration with the real Layer 2 library, which has its own gates.
const fenceEnclosingProbeLua = `
redis.register_function('ns_tset_fence_enclosing_probe', function(keys,args)
  if #keys==0 and #args==1 and args[1]=='arm' then
    local S=NS.tset
    S.before=function(...) error('fence test: enclosing preplan hook called') end
    S.plan=function(...) error('fence test: enclosing table planner called') end
    NS.tlog={plan=function(...) error('fence test: enclosing synthetic Layer 2 planner called') end}
    S.profile='test-composed'
    return 'armed'
  end
  local S=NS.tset
  if #keys~=0 or #args~=2 then return S.json.encode(S.refuse('ARGS')) end
  local ctx,err=S.open(args[1],args[2])
  if err then return S.json.encode(err) end
  if ctx.replay then return S.json.encode(ctx.replay) end
  local commit
  commit,err=S.fence_prepare(ctx)
  if err then return S.json.encode(err) end
  return S.commit(commit)
end)
`

func TestFenceFunctional(t *testing.T) {
	t.Parallel()
	t.Run("winning fence bypasses planners and writes only receipt", func(t *testing.T) {
		fx := newTSetFixture(t)
		fx.Define(t, "work", "cards")
		fx.AddRow(t, "work", "r", 0)
		fx.ActivateWithLua(t, fenceTripwireLua)
		fenceArmPlannerTrap(t, fx, "ns_tset_fence_tripwire")

		op, intent := "fence-win", "fence functional winning identity"
		request := fenceRequest(fx.Space, op, intent)
		before := commitProbeImage(t, fx.Client)
		fresh := fenceCall(t, fx, request)
		requireFenceFreshReply(t, fresh)

		doneKey := fixtureDoneKey(fx.Space, "0")
		afterFresh := commitProbeImage(t, fx.Client)
		requireFenceOnlyReceiptWrite(t, before, afterFresh, doneKey)
		requireFenceReceipt(t, fx, doneKey, op, intent)

		replay := fenceCall(t, fx, request)
		requireFenceCompactReplay(t, replay, "fenced")
		afterReplay := commitProbeImage(t, fx.Client)
		if !reflect.DeepEqual(afterFresh, afterReplay) {
			t.Fatal("fenced replay changed the Redis key image")
		}

		lateOriginal := map[string]any{
			"epoch": "0", "space": fx.Space, "op": op, "intent": intent,
			"entries": []any{map[string]any{
				"kind": "create", "t": "work", "to": "r:cards",
				"ids": []string{"late"}, "scores": []string{"1"}, "about": []string{"primary"},
			}},
		}
		late := fenceCall(t, fx, lateOriginal)
		requireFenceCompactReplay(t, late, "fenced")
		afterLate := commitProbeImage(t, fx.Client)
		if !reflect.DeepEqual(afterFresh, afterLate) {
			t.Fatal("late original identity changed state after winning fence")
		}
	})

	t.Run("prior ok receipt wins over fence request", func(t *testing.T) {
		fx := newTSetFixture(t)
		fx.Define(t, "work", "cards")

		op, intent := "ordinary-empty", "ordinary named empty identity"
		ordinary := map[string]any{
			"epoch": "0", "space": fx.Space, "op": op, "intent": intent,
			"result": "ordinary result", "entries": []any{},
		}
		fresh := fenceCall(t, fx, ordinary)
		requireOrdinaryEmptyOK(t, fresh)

		before := commitProbeImage(t, fx.Client)
		fencedRequest := fenceRequest(fx.Space, op, intent)
		replay := fenceCall(t, fx, fencedRequest)
		requireFenceCompactReplay(t, replay, "ok")
		if got := fenceString(t, replay, "result"); got != "ordinary result" {
			t.Fatalf("saved ordinary result = %q, want %q", got, "ordinary result")
		}
		after := commitProbeImage(t, fx.Client)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("fence request changed an existing ok receipt")
		}
	})

	t.Run("enclosing helper bypasses every preplanner in an L1 probe", func(t *testing.T) {
		fx := newTSetFixture(t)
		fx.Define(t, "work", "cards")
		fx.AddRow(t, "work", "r", 0)
		fx.ActivateWithLua(t, fenceEnclosingProbeLua)
		fenceArmPlannerTrap(t, fx, "ns_tset_fence_enclosing_probe")

		op, intent := "fence-enclosed", "enclosing fence helper identity"
		request := fenceRequest(fx.Space, op, intent)
		before := commitProbeImage(t, fx.Client)
		fresh := fenceEnclosingCall(t, fx, request)
		requireFenceFreshReply(t, fresh)
		afterFresh := commitProbeImage(t, fx.Client)
		doneKey := fixtureDoneKey(fx.Space, "0")
		requireFenceOnlyReceiptWrite(t, before, afterFresh, doneKey)
		requireFenceReceipt(t, fx, doneKey, op, intent)

		replay := fenceEnclosingCall(t, fx, request)
		requireFenceCompactReplay(t, replay, "fenced")
		if afterReplay := commitProbeImage(t, fx.Client); !reflect.DeepEqual(afterFresh, afterReplay) {
			t.Fatal("enclosing fence replay changed the Redis key image")
		}
	})

	t.Run("original fence marker cannot be changed after open", func(t *testing.T) {
		for _, mode := range []string{"add", "remove", "false", "null"} {
			t.Run(mode, func(t *testing.T) {
				fx := newTSetFixture(t)
				fx.Define(t, "work", "cards")
				fx.AddRow(t, "work", "r", 0)
				fx.ActivateWithLua(t, fenceMutationProbeLua)

				var request map[string]any
				if mode == "add" {
					request = map[string]any{
						"epoch": "0", "space": fx.Space, "op": "ordinary-marker", "intent": "ordinary marker mutation",
						"entries": []any{}, "notes": []any{}, "result": "",
					}
				} else {
					request = fenceRequest(fx.Space, "fence-marker-"+mode, "fence marker mutation "+mode)
				}
				before := commitProbeImage(t, fx.Client)
				reply := fenceMutationCall(t, fx, request, mode)
				if reply.Status != "refused" || reply.Code != "REQUEST" || reply.Planned || reply.Before != reply.After {
					t.Fatalf("marker mutation %q = %+v, want REQUEST before further planner reads", mode, reply)
				}
				if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
					t.Fatalf("marker mutation %q changed the Redis key image", mode)
				}
			})
		}
	})
}

// The fixture flushes queued rows through the public writer during Activate.
// Arm fatal hooks only after that setup finishes, before the fence under test.
func fenceArmPlannerTrap(t *testing.T, fx *tsetFixture, function string) {
	t.Helper()
	result, err := fx.Client.FCall(context.Background(), function, []string{}, "arm").Text()
	if err != nil || result != "armed" {
		t.Fatalf("arm fence planner trap %s: result=%q err=%v", function, result, err)
	}
}

func fenceRequest(space, op, intent string) map[string]any {
	return map[string]any{
		"epoch": "0", "space": space, "op": op, "intent": intent,
		"entries": []any{}, "notes": []any{}, "result": "", "fence": true,
	}
}

func fenceCall(t *testing.T, fx *tsetFixture, request map[string]any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	value, err := fx.Step(string(raw))
	if err != nil {
		t.Fatalf("fence FCALL: %v", err)
	}
	var encoded []byte
	switch v := value.(type) {
	case string:
		encoded = []byte(v)
	case []byte:
		encoded = v
	default:
		t.Fatalf("fence FCALL returned %T, want JSON bulk string", value)
	}
	var reply map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &reply); err != nil {
		t.Fatalf("fence reply %q: %v", encoded, err)
	}
	return reply
}

func fenceEnclosingCall(t *testing.T, fx *tsetFixture, request map[string]any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	value, err := fx.Client.FCall(context.Background(), "ns_tset_fence_enclosing_probe", []string{}, Version, string(raw)).Result()
	if err != nil {
		t.Fatalf("enclosing fence FCALL: %v", err)
	}
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("enclosing fence FCALL returned %T, want JSON bulk string", value)
	}
	var reply map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &reply); err != nil {
		t.Fatalf("enclosing fence reply %q: %v", encoded, err)
	}
	return reply
}

type fenceMutationReply struct {
	Status  string `json:"status"`
	Code    string `json:"code"`
	Before  int    `json:"before"`
	After   int    `json:"after"`
	Planned bool   `json:"planned"`
}

func fenceMutationCall(t *testing.T, fx *tsetFixture, request map[string]any, mode string) fenceMutationReply {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	value, err := fx.Client.FCall(context.Background(), "ns_tset_fence_mutation_probe", []string{}, Version, string(raw), mode).Result()
	if err != nil {
		t.Fatalf("fence mutation FCALL: %v", err)
	}
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("fence mutation FCALL returned %T, want JSON bulk string", value)
	}
	var reply fenceMutationReply
	if err := json.Unmarshal([]byte(encoded), &reply); err != nil {
		t.Fatalf("fence mutation reply %q: %v", encoded, err)
	}
	return reply
}

func requireFenceFreshReply(t *testing.T, reply map[string]json.RawMessage) {
	t.Helper()
	requireFenceKeys(t, reply, "changed", "changed_per_entry", "counters", "epoch_after", "epoch_before",
		"first_seq", "guarded", "last_seq", "lines", "replay", "result", "status")
	if got := fenceString(t, reply, "status"); got != "fenced" {
		t.Fatalf("fresh fence status = %q, want fenced", got)
	}
	if fenceBool(t, reply, "replay") {
		t.Fatal("fresh fence marked replay")
	}
	if got := fenceString(t, reply, "epoch_before"); got != "0" || fenceString(t, reply, "epoch_after") != "0" ||
		fenceNumber(t, reply, "changed") != 0 || fenceNumber(t, reply, "guarded") != 0 ||
		fenceNumber(t, reply, "lines") != 0 || fenceString(t, reply, "first_seq") != "0" ||
		fenceString(t, reply, "last_seq") != "0" || fenceString(t, reply, "result") != "" {
		t.Fatalf("fresh fence shape has effects: %s", fenceJSON(t, reply))
	}
	var changed []any
	if err := json.Unmarshal(reply["changed_per_entry"], &changed); err != nil || len(changed) != 0 {
		t.Fatalf("fresh fence changed_per_entry = %s, want []", reply["changed_per_entry"])
	}
}

func requireFenceCompactReplay(t *testing.T, reply map[string]json.RawMessage, status string) {
	t.Helper()
	requireFenceKeys(t, reply, "changed", "epoch_after", "epoch_before", "first_seq", "last_seq", "replay", "result", "status")
	if got := fenceString(t, reply, "status"); got != status || !fenceBool(t, reply, "replay") {
		t.Fatalf("compact replay = %s, want status=%q replay=true", fenceJSON(t, reply), status)
	}
	if status == "fenced" && (fenceString(t, reply, "epoch_before") != "0" ||
		fenceString(t, reply, "epoch_after") != "0" || fenceNumber(t, reply, "changed") != 0 ||
		fenceString(t, reply, "first_seq") != "0" || fenceString(t, reply, "last_seq") != "0" ||
		fenceString(t, reply, "result") != "") {
		t.Fatalf("fenced compact replay has effects: %s", fenceJSON(t, reply))
	}
}

func requireOrdinaryEmptyOK(t *testing.T, reply map[string]json.RawMessage) {
	t.Helper()
	requireFenceKeys(t, reply, "changed", "changed_per_entry", "counters", "epoch_after", "epoch_before",
		"first_seq", "guarded", "last_seq", "lines", "replay", "result", "status")
	if got := fenceString(t, reply, "status"); got != "ok" || fenceBool(t, reply, "replay") ||
		fenceString(t, reply, "result") != "ordinary result" {
		t.Fatalf("ordinary named empty reply = %s", fenceJSON(t, reply))
	}
}

func requireFenceOnlyReceiptWrite(t *testing.T, before, after map[string]commitProbeKey, doneKey string) {
	t.Helper()
	if _, exists := before[doneKey]; exists {
		t.Fatalf("fixture unexpectedly already has done key %q", doneKey)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("fence key count before=%d after=%d, want one new receipt key", len(before), len(after))
	}
	for key, prior := range before {
		if got, exists := after[key]; !exists || got != prior {
			t.Fatalf("fence changed non-receipt key %q: before=%+v after=%+v", key, prior, got)
		}
	}
	if got, exists := after[doneKey]; !exists || got.Type != "hash" {
		t.Fatalf("fence done key = %+v exists=%v, want hash", got, exists)
	}
}

func requireFenceReceipt(t *testing.T, fx *tsetFixture, doneKey, op, intent string) {
	t.Helper()
	raw, err := fx.Client.HGet(context.Background(), doneKey, op).Result()
	if err != nil {
		t.Fatalf("read fenced receipt: %v", err)
	}
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		t.Fatalf("decode fenced receipt: %v", err)
	}
	requireFenceKeys(t, receipt, "changed", "epoch_after", "epoch_before", "first_seq", "intent_digest", "last_seq", "result", "status")
	if fenceString(t, receipt, "intent_digest") != intentDigest(intent) ||
		fenceString(t, receipt, "status") != "fenced" || fenceString(t, receipt, "epoch_before") != "0" ||
		fenceString(t, receipt, "epoch_after") != "0" || fenceString(t, receipt, "first_seq") != "0" ||
		fenceString(t, receipt, "last_seq") != "0" || fenceNumber(t, receipt, "changed") != 0 ||
		fenceString(t, receipt, "result") != "" {
		t.Fatalf("fenced receipt for op %q = %s", op, fenceJSON(t, receipt))
	}
}

func requireFenceKeys(t *testing.T, reply map[string]json.RawMessage, keys ...string) {
	t.Helper()
	if len(reply) != len(keys) {
		t.Fatalf("reply keys = %v, want %v", fenceKeySet(reply), keys)
	}
	for _, key := range keys {
		if _, ok := reply[key]; !ok {
			t.Fatalf("reply keys = %v, missing %q", fenceKeySet(reply), key)
		}
	}
}

func fenceString(t *testing.T, reply map[string]json.RawMessage, key string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(reply[key], &value); err != nil {
		t.Fatalf("reply %q = %s, want string: %v", key, reply[key], err)
	}
	return value
}

func fenceNumber(t *testing.T, reply map[string]json.RawMessage, key string) int {
	t.Helper()
	var value int
	if err := json.Unmarshal(reply[key], &value); err != nil {
		t.Fatalf("reply %q = %s, want integer: %v", key, reply[key], err)
	}
	return value
}

func fenceBool(t *testing.T, reply map[string]json.RawMessage, key string) bool {
	t.Helper()
	var value bool
	if err := json.Unmarshal(reply[key], &value); err != nil {
		t.Fatalf("reply %q = %s, want bool: %v", key, reply[key], err)
	}
	return value
}

func fenceKeySet(reply map[string]json.RawMessage) map[string]bool {
	keys := make(map[string]bool, len(reply))
	for key := range reply {
		keys[key] = true
	}
	return keys
}

func fenceJSON(t *testing.T, reply map[string]json.RawMessage) string {
	t.Helper()
	encoded, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
