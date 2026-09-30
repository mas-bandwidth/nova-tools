//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// This enclosing planner exists only in private fixture source. Invalid
// mutations must be refused by the production seam before any write or receipt.
const originalSealProbeLua = `
redis.register_function('ns_tset_original_seal_probe',function(keys,args)
  local S=NS.tset
  local ctx,err=S.open(args[1],args[2]);if err then return S.json.encode(err) end
  local mode=args[3]
  local function rebind()
    ctx.op='rebound';ctx.request.op='rebound'
    ctx.intent='rebound-intent';ctx.request.intent='rebound-intent'
    ctx.intent_digest=redis.sha1hex('rebound-intent')
  end
  local function append_derived()
    ctx.request.entries[#ctx.request.entries+1]=S.json.decode(
      '{"kind":"create","t":"work","to":"r:c","ids":["new"],"scores":["2"]}')
    ctx.notes[#ctx.notes+1]=S.json.decode(
      '{"line":{"kind":"note","meta":{"source":"derived"}},"about":[]}')
  end
  if mode=='drop' then table.remove(ctx.request.entries,1)
  elseif mode=='replace' then
    local entries=S.array();entries[1]=ctx.request.entries[2];ctx.request.entries=entries
  elseif mode=='alter_guard' then ctx.request.entries[1].revs[1]='1'
  elseif mode=='alter_set' then ctx.request.entries[1].set.state='changed'
  elseif mode=='alter_meta_kind' then ctx.request.entries[1].meta=S.array()
  elseif mode=='alter_note' then ctx.notes[1].line.meta.source='changed'
  elseif mode=='drop_note' then table.remove(ctx.notes,1)
  elseif mode=='rebind' or mode=='fence_rebind' then rebind()
  elseif mode=='op_only' then ctx.op='rebound';ctx.request.op='rebound'
  elseif mode=='intent_only' then ctx.intent='rebound-intent';ctx.request.intent='rebound-intent'
  elseif mode=='epoch' then ctx.request.epoch='1';ctx.request_epoch='1';ctx.write_epoch='1'
  elseif mode=='write_epoch' then ctx.write_epoch='1'
  elseif mode=='space' then ctx.space=ctx.space..'other:';ctx.request.space=ctx.space
  elseif mode=='result' then ctx.result='other';ctx.request.result='other'
  elseif mode=='raw' then ctx.raw_request='{}';ctx.request_hash=redis.sha1hex('{}')
  elseif mode=='active' then ctx.active_epoch='1'
  elseif mode=='original_count' then ctx.original_note_count=0
  elseif mode=='append' or mode=='mutate_derived_after_plan' or
      mode=='mutate_derived_note_after_plan' then append_derived()
  elseif mode=='append_after_plan' or mode=='append_note_after_plan' or
      mode=='misaligned_changed_per_entry' or mode=='prepare_op_only' or
      mode=='prepare_rebind' or mode=='baseline' or mode=='no_plan' then
    -- These cases mutate after S.plan or deliberately omit S.plan.
  else return S.json.encode(S.refuse('REQUEST'))
  end
  local prepared
  if mode=='fence_rebind' then
    prepared,err=S.fence_prepare(ctx)
  elseif mode=='no_plan' then
    local changed=S.array()
    for i=1,#ctx.request.entries do changed[i]=0 end
    local plan={commands=S.array(),changed=0,guarded=0,changed_per_entry=changed}
    local log={commands=S.array(),first_seq='0',last_seq='0',line_count=0,about_appends=0}
    prepared,err=S.prepare(ctx,plan,log,{})
  else
    local plan;plan,err=S.plan(ctx);if err then return S.json.encode(err) end
    if mode=='prepare_rebind' then rebind() end
    if mode=='prepare_op_only' then ctx.op='rebound';ctx.request.op='rebound' end
    if mode=='append_after_plan' then
      ctx.request.entries[#ctx.request.entries+1]=S.json.decode(
        '{"kind":"create","t":"work","to":"r:c","ids":["late"],"scores":["3"]}')
    elseif mode=='append_note_after_plan' then
      ctx.notes[#ctx.notes+1]=S.json.decode(
        '{"line":{"kind":"note","meta":{"source":"late"}},"about":[]}')
    elseif mode=='mutate_derived_after_plan' then
      ctx.request.entries[#ctx.request.entries].ids[1]='other'
    elseif mode=='mutate_derived_note_after_plan' then
      ctx.notes[#ctx.notes].line.meta.source='other'
    elseif mode=='misaligned_changed_per_entry' then
      table.remove(plan.changed_per_entry)
    end
    local log={commands={},first_seq='0',last_seq='0',line_count=0,about_appends=0}
    prepared,err=S.prepare(ctx,plan,log,{})
  end
  if err then return S.json.encode(err) end
  return S.commit(prepared)
end)
`

func originalSealFixture(t *testing.T) *tsetFixture {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.ActivateWithLua(t, originalSealProbeLua)
	raw := fmt.Sprintf(`{"space":%q,"epoch":"0","entries":[{"kind":"rows","t":"work","add":["r"]},{"kind":"create","t":"work","to":"r:c","ids":["existing"],"scores":["1"],"set":{"state":"live"}}]}`, fx.Space)
	value, err := fx.Step(raw)
	if err != nil {
		t.Fatal(err)
	}
	var reply struct{ Status string }
	if err := json.Unmarshal([]byte(fmt.Sprint(value)), &reply); err != nil || reply.Status != "ok" {
		t.Fatalf("bootstrap = %v, decode error %v", value, err)
	}
	return fx
}

type originalSealReply struct {
	Status          string `json:"status"`
	Code            string `json:"code"`
	Changed         int    `json:"changed"`
	ChangedPerEntry []int  `json:"changed_per_entry"`
}

func originalSealCallFull(t *testing.T, fx *tsetFixture, raw, mode string) originalSealReply {
	t.Helper()
	wire, err := fx.Client.FCall(context.Background(), "ns_tset_original_seal_probe", []string{}, Version, raw, mode).Text()
	if err != nil {
		t.Fatal(err)
	}
	var reply originalSealReply
	if err := json.Unmarshal([]byte(wire), &reply); err != nil {
		t.Fatalf("reply %q: %v", wire, err)
	}
	return reply
}

func originalSealCall(t *testing.T, fx *tsetFixture, raw, mode string) (string, string) {
	t.Helper()
	reply := originalSealCallFull(t, fx, raw, mode)
	return reply.Status, reply.Code
}

func TestPreplanOriginalCallerPrefixSeal(t *testing.T) {
	t.Parallel()
	fx := originalSealFixture(t)
	stale := fmt.Sprintf(`{"space":%q,"epoch":"0","op":"original","intent":"stable","entries":[{"kind":"guard","t":"work","from":"r:c","ids":["existing"],"revs":["9"]},{"kind":"create","t":"work","to":"r:c","ids":["new"],"scores":["2"]}]}`, fx.Space)
	create := fmt.Sprintf(`{"space":%q,"epoch":"0","op":"original","intent":"stable","entries":[{"kind":"create","t":"work","to":"r:c","ids":["new"],"scores":["2"],"set":{"state":"caller"},"meta":{}}],"notes":[{"line":{"kind":"note","meta":{"source":"caller"}},"about":[]}]}`, fx.Space)
	for _, tc := range []struct{ mode, raw, code string }{
		{"baseline", stale, "REVISION"}, {"drop", stale, "REQUEST"},
		{"replace", stale, "REQUEST"}, {"alter_guard", stale, "REQUEST"},
		{"alter_set", create, "REQUEST"}, {"alter_meta_kind", create, "REQUEST"},
		{"alter_note", create, "REQUEST"}, {"drop_note", create, "REQUEST"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			before := commitProbeImage(t, fx.Client)
			status, code := originalSealCall(t, fx, tc.raw, tc.mode)
			if status != "refused" || code != tc.code {
				t.Fatalf("got %s/%s, want refused/%s", status, code, tc.code)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Fatal("original-prefix refusal changed the complete Redis image")
			}
		})
	}
}

func TestPreplanPrivateIdentitySeal(t *testing.T) {
	t.Parallel()
	fx := originalSealFixture(t)
	raw := fmt.Sprintf(`{"space":%q,"epoch":"0","op":"original","intent":"stable","entries":[{"kind":"create","t":"work","to":"r:c","ids":["new"],"scores":["2"]}],"notes":[{"line":{"kind":"note","meta":{}},"about":[]}]}`, fx.Space)
	for _, mode := range []string{"rebind", "op_only", "intent_only", "epoch", "write_epoch", "space", "result", "raw", "active", "original_count", "prepare_rebind", "prepare_op_only", "fence_rebind"} {
		t.Run(mode, func(t *testing.T) {
			input := raw
			if mode == "fence_rebind" {
				input = fmt.Sprintf(`{"space":%q,"epoch":"0","op":"original","intent":"stable","fence":true,"entries":[]}`, fx.Space)
			}
			before := commitProbeImage(t, fx.Client)
			status, code := originalSealCall(t, fx, input, mode)
			if status != "refused" || code != "REQUEST" {
				t.Fatalf("got %s/%s, want refused/REQUEST", status, code)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Fatal("private-identity refusal changed the complete Redis image")
			}
			for _, op := range []string{"original", "rebound"} {
				if fx.Client.HExists(context.Background(), fixtureDoneKey(fx.Space, "0"), op).Val() {
					t.Fatalf("private-identity refusal persisted %q receipt", op)
				}
			}
		})
	}
}

func TestPreplanCombinedPlanSeal(t *testing.T) {
	t.Parallel()
	fx := originalSealFixture(t)
	raw := fmt.Sprintf(`{"space":%q,"epoch":"0","op":"original","intent":"stable","entries":[{"kind":"guard","t":"work","from":"r:c","ids":["existing"],"revs":["1"]}],"notes":[{"line":{"kind":"note","meta":{"source":"caller"}},"about":[]}]}`, fx.Space)
	for _, mode := range []string{"append_after_plan", "append_note_after_plan", "mutate_derived_after_plan", "mutate_derived_note_after_plan", "misaligned_changed_per_entry", "no_plan"} {
		t.Run(mode, func(t *testing.T) {
			before := commitProbeImage(t, fx.Client)
			status, code := originalSealCall(t, fx, raw, mode)
			if status != "refused" || code != "REQUEST" {
				t.Fatalf("post-plan %s = %s/%s, want refused/REQUEST", mode, status, code)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Fatalf("post-plan %s changed the complete Redis image", mode)
			}
			for _, op := range []string{"original", "rebound"} {
				if fx.Client.HExists(context.Background(), fixtureDoneKey(fx.Space, "0"), op).Val() {
					t.Fatalf("post-plan %s persisted %q receipt", mode, op)
				}
			}
			for _, id := range []string{"new", "late", "other"} {
				if fx.Client.Exists(context.Background(), fixtureRecordKey(fx.Space, "work", id)).Val() != 0 {
					t.Fatalf("post-plan %s wrote member %q", mode, id)
				}
			}
		})
	}
}

func TestPreplanOriginalSealAllowsDerivedSuffix(t *testing.T) {
	t.Parallel()
	fx := originalSealFixture(t)
	raw := fmt.Sprintf(`{"space":%q,"epoch":"0","op":"original","intent":"stable","entries":[{"kind":"guard","t":"work","from":"r:c","ids":["existing"],"revs":["1"]}],"notes":[{"line":{"kind":"note","meta":{"source":"caller"}},"about":[]}]}`, fx.Space)
	reply := originalSealCallFull(t, fx, raw, "append")
	if reply.Status != "ok" || reply.Code != "" || reply.Changed != 1 ||
		!reflect.DeepEqual(reply.ChangedPerEntry, []int{0, 1}) {
		t.Fatalf("derived append and plan alignment = %+v", reply)
	}
	ctx := context.Background()
	if n := fx.Client.Exists(ctx, fixtureRecordKey(fx.Space, "work", "new")).Val(); n != 1 {
		t.Fatal("derived create did not commit")
	}
	if !fx.Client.HExists(ctx, fixtureDoneKey(fx.Space, "0"), "original").Val() || fx.Client.HExists(ctx, fixtureDoneKey(fx.Space, "0"), "rebound").Val() {
		t.Fatal("receipt did not preserve the original operation identity")
	}
}

// Read callbacks have no append phase. Even a mutation after producing a
// plausible answer must discard the complete answer set atomically.
const originalReadSealProbeLua = `
redis.register_function('ns_tset_original_read_seal_probe',function(keys,args)
  local S=NS.tset
  local calls=0
  local extension={kinds={'sealprobe'},
    validate=function(q,index)
      if q.kind~='sealprobe' or type(q.label)~='string' then return nil,S.refuse('REQUEST') end
      return true,nil
    end,
    read=function(ctx,q,index)
      calls=calls+1
      if args[3]=='nested_context' then
        local fabricated,problem=S.context(ctx.request,'read')
        if fabricated or not problem or problem.code~='CONFIG' then error('callback rebound context') end
        local released;released,problem=S.release_context(ctx)
        if released or not problem or problem.code~='CONFIG' then error('callback released context') end
      elseif args[3]=='current' then q.label='changed'
      elseif args[3]=='later' then ctx.request.queries[index+2].fields=S.array()
      elseif args[3]=='append' then
        ctx.request.queries[#ctx.request.queries+1]=S.json.decode('{"kind":"sealprobe","label":"derived"}')
      elseif args[3]=='replace_context' then
        ctx.request=S.json.decode(S.json.encode(ctx.request))
        q.label='changed-on-original-request'
      elseif args[3]=='mode' then ctx.request.mode='page'
      end
      return {kind='sealprobe',label=q.label},nil
    end}
  local reply=S.json.decode(S.read(args[1],args[2],nil,extension))
  return S.json.encode({reply=reply,calls=calls})
end)
`

func TestReadCallbackOriginalQuerySeal(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.ActivateWithLua(t, originalReadSealProbeLua)
	raw := fmt.Sprintf(`{"space":%q,"epoch":"0","mode":"atomic","queries":[{"kind":"ids","t":"work","ids":["absent"],"fields":[]},{"kind":"sealprobe","label":"caller"},{"kind":"ids","t":"work","ids":["later"],"fields":["state"]}]}`, fx.Space)
	for _, mode := range []string{"current", "later", "append", "replace_context", "mode", "nested_context", "unchanged"} {
		t.Run(mode, func(t *testing.T) {
			before := commitProbeImage(t, fx.Client)
			wire, err := fx.Client.FCall(context.Background(), "ns_tset_original_read_seal_probe", []string{}, Version, raw, mode).Text()
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Reply struct {
					Status, Code string
					Answers      []json.RawMessage
				}
				Calls int
			}
			if err := json.Unmarshal([]byte(wire), &got); err != nil {
				t.Fatal(err)
			}
			if got.Calls != 1 {
				t.Fatalf("callback calls = %d, want 1", got.Calls)
			}
			if mode == "unchanged" || mode == "nested_context" {
				if got.Reply.Status != "read" || len(got.Reply.Answers) != 3 {
					t.Fatalf("unchanged read = %s", wire)
				}
			} else if got.Reply.Status != "refused" || got.Reply.Code != "CONFIG" || len(got.Reply.Answers) != 0 {
				t.Fatalf("mutated read must refuse without earlier answers: %s", wire)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Fatal("read query mutation changed the complete Redis image")
			}
		})
	}
}

// Keep one test-only reference after each scope to prove that cleanup revokes
// the seal, rather than merely relying on Lua garbage collection.
const contextLifecycleProbeLua = `
redis.register_function('ns_tset_context_lifecycle_probe',function(keys,args)
  local S=NS.tset
  local held,nested_ran,old_refused,released=false,false,false,false
  local function body()
    local ctx,err=S.open(args[1],args[2]);if err then return S.json.encode(err) end
    held=ctx
    if args[3]=='throw' then error('lifecycle deliberate failure') end
    if args[3]=='replacement' then
      local newer;newer,err=S.open(args[1],args[2]);if err then return S.json.encode(err) end
      local old,problem=S.plan(ctx)
      old_refused=not old and problem and problem.code=='REQUEST'
      held=newer
      released,err=S.release_context(newer)
      if err then return S.json.encode(err) end
      return S.json.encode({status='released'})
    end
    if args[3]=='nested' then
      local inner=S.run_context(function() nested_ran=true;return 'unexpected' end)
      if S.json.decode(inner).code~='CONFIG' then error('nested scope was admitted') end
    end
    local plan;plan,err=S.plan(ctx);if err then return S.json.encode(err) end
    local prepared;prepared,err=S.prepare(ctx,plan,
      {commands={},first_seq='0',last_seq='0',line_count=0,about_appends=0},{})
    if err then return S.json.encode(err) end
    return S.commit(prepared)
  end
  local ok,result=pcall(S.run_context,body)
  local stale,problem=S.plan(held)
  return S.json.encode({outcome=ok and S.json.decode(result) or {status='thrown'},
    stale=not stale and problem and problem.code or 'accepted',
    nested_ran=nested_ran,old_refused=old_refused,released=released})
end)
`

func TestPrivateContextLifecycle(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.ActivateWithLua(t, contextLifecycleProbeLua)
	good := fmt.Sprintf(`{"space":%q,"epoch":"0","entries":[]}`, fx.Space)
	bad := fmt.Sprintf(`{"space":%q,"epoch":"0","entries":[{"kind":"guard","t":"work","from":"missing:c","ids":["absent"]}]}`, fx.Space)
	for _, mode := range []string{"success", "refusal", "throw", "replacement", "nested", "success"} {
		t.Run(mode, func(t *testing.T) {
			input := good
			want := "ok"
			switch mode {
			case "refusal":
				input, want = bad, "refused"
			case "throw":
				want = "thrown"
			case "replacement":
				want = "released"
			}
			before := commitProbeImage(t, fx.Client)
			wire, err := fx.Client.FCall(context.Background(), "ns_tset_context_lifecycle_probe", []string{}, Version, input, mode).Text()
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Outcome    struct{ Status string }
				Stale      string
				NestedRan  bool `json:"nested_ran"`
				OldRefused bool `json:"old_refused"`
				Released   bool
			}
			if err := json.Unmarshal([]byte(wire), &got); err != nil {
				t.Fatal(err)
			}
			if got.Outcome.Status != want || got.Stale != "REQUEST" || got.NestedRan ||
				(mode == "replacement" && (!got.OldRefused || !got.Released)) {
				t.Fatalf("lifecycle %s: %s", mode, wire)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Fatal("context lifecycle probe changed the complete Redis image")
			}
		})
	}
}
