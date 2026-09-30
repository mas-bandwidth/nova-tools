//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"github.com/redis/go-redis/v9"
)

// This is a test-only AL5 callback. It deliberately tries each route that
// could evade the common read budget; production extensions use the checked
// helpers in their normal way.
const al5GuardrailProbeLua = `
redis.register_function('ns_tset_al5_guardrail_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local mode=args[3]
  local extension={kinds={'al5guard'}}
  extension.validate=function(q,index)
    if q.kind~='al5guard' then return nil,S.refuse('REQUEST',{query_index=index}) end
    for k in pairs(q) do
      if k~='kind' then return nil,S.refuse('REQUEST',{query_index=index}) end
    end
    return true,nil
  end
  extension.read=function(ctx,q,index)
    local member=ctx.space..'member:work:card'
    local vector=ctx.space..'sprint:vector'
    if mode=='member_hmget' or mode=='custom_member' then
      if mode=='custom_member' then member=ctx.space..'records:work:card' end
      local _,err=S.read_probe(ctx,{'HMGET',member,'state'},member,'hash',65536)
      return nil,err
    elseif mode=='vector' then
      local start=ctx.budget.cell
      local _,err=S.read_probe(ctx,{'ZMSCORE',vector,'a','b','c','d'},vector,'zset',64)
      if err then return nil,err end
      return {kind='al5guard',cell_delta=ctx.budget.cell-start},nil
    elseif mode=='hash_vector' then
      local key=ctx.space..'sprint:probe'
      local start=ctx.budget.cell
      local _,err=S.read_probe(ctx,{'HMGET',key,'a','b','c'},key,'hash',64)
      if err then return nil,err end
      return {kind='al5guard',cell_delta=ctx.budget.cell-start},nil
    elseif mode=='vector_boundary' then
      local _,err=S.charge(ctx,'cell',S.limits.cell-ctx.budget.cell-1)
      if err then return nil,err end
      _,err=S.read_probe(ctx,{'ZMSCORE',vector,'a','b'},vector,'zset',32)
      return nil,err
    elseif mode=='direct_record' then
      local _,err=S.readcmd(ctx,{argv={'HLEN',member},
        access={{key=member,kind='hash',mode='read'}}},32,'record')
      return nil,err
    elseif mode=='direct_field' then
      local key=ctx.space..'sprint:probe'
      local _,err=S.readcmd(ctx,{argv={'HMGET',key,'a','b'},
        access={{key=key,kind='hash',mode='read'}}},64,'field')
      return nil,err
    elseif mode=='direct_collection' then
      local key=ctx.space..'sprint:vector'
      local _,err=S.readcmd(ctx,{argv={'ZRANGE',key,'0','-1'},
        access={{key=key,kind='zset',mode='read'}}},1024,'cell')
      return nil,err
    elseif mode=='before_repeat' then
      local _,err=S.before(ctx,'work',{'card'},{'state'})
      if err then return nil,err end
      _,err=S.before(ctx,'work',{'card'},{'state'})
      if err then return nil,err end
      return {kind='al5guard',record=ctx.budget.record,field=ctx.budget.field},nil
    elseif mode=='before_cap' then
      local ids={}
      for i=1,5000 do ids[i]='card' end
      local _,err=S.before(ctx,'work',ids,{})
      if err then return nil,err end
      ids[5001]='card'
      _,err=S.before(ctx,'work',ids,{})
      return nil,err
    elseif mode=='budget_reset' then
      local _,err=S.charge(ctx,'cell',100)
      if err then return nil,err end
      ctx.budget.cell=0
      local key=ctx.space..'sprint:vector'
      _,err=S.read_probe(ctx,{'EXISTS',key},key,'any',16)
      return nil,err
    elseif mode=='emit_reset' then
      local _,err=S.emit_read_item(ctx,{payload=string.rep('x',1024)},index)
      if err then return nil,err end
      ctx.read_encoded=0
      _,err=S.emit_read_item(ctx,{payload=string.rep('y',1024)},index)
      return nil,err
    elseif mode=='fake_context' then
      local fake={}
      for k,v in pairs(ctx) do fake[k]=v end
      fake.budget={cell=0,fetched_bytes=0,store_commands=0}
      local key=ctx.space..'sprint:vector'
      local _,err=S.read_probe(fake,{'EXISTS',key},key,'any',16)
      return nil,err
    elseif mode=='epochgone' then
      return nil,S.refuse('EPOCHGONE',{query_index=index})
    end
    return nil,S.refuse('REQUEST',{query_index=index})
  end
  return S.read(args[1],args[2],nil,extension)
end)
`

type al5GuardrailReply struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Detail struct {
		Budget      string `json:"budget"`
		Actual      int    `json:"actual"`
		QueryIndex  *int   `json:"query_index"`
		ActiveEpoch string `json:"active_epoch"`
	} `json:"detail"`
	Counters struct {
		Cell   int `json:"cell"`
		Record int `json:"record"`
		Field  int `json:"field"`
	} `json:"counters"`
	Answers []json.RawMessage `json:"answers"`
}

func al5GuardrailCall(t *testing.T, fx *tsetFixture, mode string) al5GuardrailReply {
	t.Helper()
	raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"al5guard"}]`)
	value, err := fx.Client.FCall(context.Background(), "ns_tset_al5_guardrail_probe",
		[]string{}, Version, raw, mode).Result()
	if err != nil {
		t.Fatalf("AL5 guardrail %s: %v", mode, err)
	}
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("AL5 guardrail %s returned %T", mode, value)
	}
	var reply al5GuardrailReply
	if err := json.Unmarshal([]byte(encoded), &reply); err != nil {
		t.Fatalf("decode AL5 guardrail %s: %v: %s", mode, err, encoded)
	}
	return reply
}

func TestAL5CallbackCannotEscapeReadBudget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode, code, budget string
	}{
		{mode: "member_hmget", code: "CONFIG"},
		{mode: "custom_member", code: "CONFIG"},
		{mode: "vector_boundary", code: "BUDGET", budget: "cell"},
		{mode: "direct_record", code: "CONFIG"},
		{mode: "direct_field", code: "CONFIG"},
		{mode: "direct_collection", code: "CONFIG"},
		{mode: "before_cap", code: "BUDGET", budget: "record"},
		{mode: "budget_reset", code: "CONFIG"},
		{mode: "emit_reset", code: "CONFIG"},
		{mode: "fake_context", code: "CONFIG"},
		{mode: "epochgone", code: "EPOCHGONE"},
		{mode: "vector"},
		{mode: "hash_vector"},
		{mode: "before_repeat"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixture(t)
			fx.Define(t, "work", "c")
			if tc.mode == "custom_member" {
				for _, key := range []string{fx.Space + "table:work", fixtureDefinitionKey(fx.Space, "work", "0")} {
					if err := fx.Client.HSet(context.Background(), key, "member_prefix", fx.Space+"records:work:").Err(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := fx.Client.HSet(context.Background(),
				fixtureRecordKey(fx.Space, "work", "card"),
				"epoch", "0", "revision", "1", "state", "ready").Err(); err != nil {
				t.Fatal(err)
			}
			if tc.mode == "custom_member" {
				if err := fx.Client.HSet(context.Background(), fx.Space+"records:work:card",
					"epoch", "0", "revision", "1", "state", "ready").Err(); err != nil {
					t.Fatal(err)
				}
			}
			fx.ActivateWithLua(t, al5GuardrailProbeLua)
			before := commitProbeImage(t, fx.Client)
			statsBefore := readExtensionCommandStats(t, fx.Client)
			reply := al5GuardrailCall(t, fx, tc.mode)
			statsAfter := readExtensionCommandStats(t, fx.Client)
			if tc.mode == "member_hmget" || tc.mode == "custom_member" {
				if got := readExtensionExecutedCalls(t, statsAfter, "HMGET") - readExtensionExecutedCalls(t, statsBefore, "HMGET"); got != 0 {
					t.Errorf("member record probe fetched %d HMGET vectors", got)
				}
			}
			if tc.code != "" {
				if reply.Status != "refused" || reply.Code != tc.code ||
					reply.Detail.Budget != tc.budget || len(reply.Answers) != 0 {
					t.Fatalf("%s escaped/refused incorrectly: %+v", tc.mode, reply)
				}
				if tc.mode == "epochgone" && reply.Detail.ActiveEpoch != "0" {
					t.Errorf("callback EPOCHGONE omitted observed active epoch: %+v", reply.Detail)
				}
			} else {
				if reply.Status != "read" || len(reply.Answers) != 1 {
					t.Fatalf("%s lost complete answer: %+v", tc.mode, reply)
				}
				if tc.mode == "vector" {
					var answer struct {
						CellDelta int `json:"cell_delta"`
					}
					if err := json.Unmarshal(reply.Answers[0], &answer); err != nil || answer.CellDelta != 4 {
						t.Errorf("vector did not charge four named members: answer=%+v err=%v", answer, err)
					}
				}
				if tc.mode == "hash_vector" {
					var answer struct {
						CellDelta int `json:"cell_delta"`
					}
					if err := json.Unmarshal(reply.Answers[0], &answer); err != nil || answer.CellDelta != 3 {
						t.Errorf("HMGET did not charge three named fields: answer=%+v err=%v", answer, err)
					}
				}
				if tc.mode == "before_repeat" && (reply.Counters.Record != 2 || reply.Counters.Field != 2) {
					t.Errorf("direct S.before did not charge repeated observations: %+v", reply.Counters)
				}
			}
			if tc.mode == "direct_record" || tc.mode == "direct_field" || tc.mode == "direct_collection" || tc.mode == "fake_context" {
				command := map[string]string{"direct_record": "HLEN", "direct_field": "HMGET", "direct_collection": "ZRANGE", "fake_context": "EXISTS"}[tc.mode]
				if got := readExtensionExecutedCalls(t, statsAfter, command) - readExtensionExecutedCalls(t, statsBefore, command); got != 0 {
					t.Errorf("%s executed forbidden %s %d times", tc.mode, command, got)
				}
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Error("read guardrail changed the whole Redis image")
			}
		})
	}
}

func TestAL5RowsPreflightBeforePageFetch(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	ctx := context.Background()
	key := fixtureRowsKey(fx.Space, "work", "0")
	for start := 0; start < 29960; start += 1000 {
		end := start + 1000
		if end > 29960 {
			end = 29960
		}
		members := make([]redis.Z, 0, end-start)
		for i := start; i < end; i++ {
			members = append(members, redis.Z{Score: float64(i), Member: fmt.Sprintf("r%05d", i)})
		}
		if err := fx.Client.ZAdd(ctx, key, members...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	fx.Activate(t)
	before := commitProbeImage(t, fx.Client)
	statsBefore := readExtensionCommandStats(t, fx.Client)
	raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"rows","t":"work"}]`)
	value, err := fx.Client.FCallRO(ctx, "ns_tset_read", []string{}, Version, raw).Result()
	if err != nil {
		t.Fatal(err)
	}
	var reply al5GuardrailReply
	if err := json.Unmarshal([]byte(value.(string)), &reply); err != nil {
		t.Fatal(err)
	}
	statsAfter := readExtensionCommandStats(t, fx.Client)
	if reply.Status != "refused" || reply.Code != "BUDGET" ||
		(reply.Detail.Budget != "fetched_bytes" && reply.Detail.Budget != "encoded_reply") || len(reply.Answers) != 0 {
		t.Errorf("large row catalog did not refuse before paging: %+v", reply)
	}
	if got := readExtensionExecutedCalls(t, statsAfter, "ZCARD") - readExtensionExecutedCalls(t, statsBefore, "ZCARD"); got != 1 {
		t.Errorf("rows preflight issued %d ZCARD commands, want one", got)
	}
	if got := readExtensionExecutedCalls(t, statsAfter, "ZRANGE") - readExtensionExecutedCalls(t, statsBefore, "ZRANGE"); got != 0 {
		t.Errorf("rows preflight fetched %d pages", got)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("rows refusal changed the whole Redis image")
	}
}

func TestAL5MissingLogReaderRefusesBeforeContext(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.Activate(t)
	before := commitProbeImage(t, fx.Client)
	statsBefore := readExtensionCommandStats(t, fx.Client)
	raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"last"}]`)
	value, err := fx.Client.FCallRO(context.Background(), "ns_tset_read", []string{}, Version, raw).Result()
	if err != nil {
		t.Fatal(err)
	}
	var reply al5GuardrailReply
	if err := json.Unmarshal([]byte(value.(string)), &reply); err != nil {
		t.Fatal(err)
	}
	statsAfter := readExtensionCommandStats(t, fx.Client)
	if reply.Status != "refused" || reply.Code != "REQUEST" || reply.Detail.QueryIndex == nil ||
		*reply.Detail.QueryIndex != 0 || len(reply.Answers) != 0 {
		t.Errorf("missing log reader did not statically refuse: %+v", reply)
	}
	for _, command := range []string{"TIME", "ZRANGE", "HGET"} {
		if got := readExtensionExecutedCalls(t, statsAfter, command) - readExtensionExecutedCalls(t, statsBefore, command); got != 0 {
			t.Errorf("missing log reader executed %s %d times", command, got)
		}
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("missing-log refusal changed the whole Redis image")
	}
}

func TestAL5WholeRecordUsesOneLengthAndDirectFetch(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fields := map[string]any{"epoch": "0", "revision": "1"}
	for i := 0; i < 5; i++ {
		fields["f"+strconv.Itoa(i)] = "v" + strconv.Itoa(i)
	}
	if err := fx.Client.HSet(context.Background(), fixtureRecordKey(fx.Space, "work", "card"), fields).Err(); err != nil {
		t.Fatal(err)
	}
	fx.Activate(t)
	before := commitProbeImage(t, fx.Client)
	statsBefore := readExtensionCommandStats(t, fx.Client)
	raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"ids","t":"work","ids":["card"]}]`)
	value, err := fx.Client.FCallRO(context.Background(), "ns_tset_read", []string{}, Version, raw).Result()
	if err != nil {
		t.Fatal(err)
	}
	var reply al5GuardrailReply
	if err := json.Unmarshal([]byte(value.(string)), &reply); err != nil {
		t.Fatal(err)
	}
	statsAfter := readExtensionCommandStats(t, fx.Client)
	if reply.Status != "read" || reply.Counters.Record != 1 || reply.Counters.Field != 5 {
		t.Errorf("whole record answer/charges: %+v", reply)
	}
	if got := readExtensionExecutedCalls(t, statsAfter, "HGETALL") - readExtensionExecutedCalls(t, statsBefore, "HGETALL"); got != 2 {
		t.Errorf("whole record issued %d HGETALL, want definition and direct record", got)
	}
	if got := readExtensionExecutedCalls(t, statsAfter, "HLEN") - readExtensionExecutedCalls(t, statsBefore, "HLEN"); got != 2 {
		t.Errorf("whole record issued %d HLEN, want definition and one record length", got)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("whole-record read changed the whole Redis image")
	}
}

// This callback names its table dynamically, so retained open_state has no
// top-level table name to preload. It tests that callback-visible definitions
// cannot replace the retained snapshot used by the point reader.
const al5DefinitionSealProbeLua = `
redis.register_function('ns_tset_al5_definition_seal_probe',function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local mode=args[3]
  local extension={kinds={'defseal'}}
  extension.validate=function(q,index)
    if not S.is_object(q) or q.kind~='defseal' then
      return nil,S.refuse('REQUEST',{query_index=index})
    end
    for key in pairs(q) do
      if key~='kind' then return nil,S.refuse('REQUEST',{query_index=index}) end
    end
    return true,nil
  end
  extension.read=function(ctx,q,index)
    if mode=='public_load' then
      local _,err=S.load_defs(ctx,'work')
      return nil,err
    end
    if mode=='injected_def' then
      ctx.defs.work={member_prefix=ctx.space..'member:work_live:'}
    elseif mode=='returned_def' then
      local def,err=S.ensure_read_table(ctx,'work',index)
      if err then return nil,err end
      def.member_prefix=ctx.space..'member:work_live:'
      def.raw.member_prefix=def.member_prefix
    elseif mode=='forged_definition_key' then
      ctx.definition_key=function() return ctx.space..'table:work:1:definition' end
    elseif mode=='forged_placement_keys' then
      ctx.rows_key=function() return ctx.space..'table:work:1:rows' end
      ctx.cell_key=function() return ctx.space..'table:work:1:cell:r:c' end
    end
    local record,err=S.read_record(ctx,'work','card',{'state'},index)
    if err then return nil,err end
    if mode=='post_read_poison' then
      ctx.defs.work.member_prefix=ctx.space..'member:work_live:'
    end
    return {kind='defseal',value=record.fields.state.value,score=record.score},nil
  end
  return S.read(args[1],args[2],nil,extension)
end)
`

func al5DefinitionSealCall(t *testing.T, fx *tsetFixture, epoch, mode string) al5GuardrailReply {
	t.Helper()
	raw := readExtensionRaw(fx.Space, epoch, "atomic", `[{"kind":"defseal"}]`)
	wire, err := fx.Client.FCall(context.Background(), "ns_tset_al5_definition_seal_probe",
		nil, Version, raw, mode).Text()
	if err != nil {
		t.Fatalf("definition seal %s: %v", mode, err)
	}
	var reply al5GuardrailReply
	if err := json.Unmarshal([]byte(wire), &reply); err != nil {
		t.Fatalf("decode definition seal %s: %v", mode, err)
	}
	return reply
}

func TestAL5RetainedDefinitionCannotUseLivePrefix(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	ctx := context.Background()
	if err := fx.Client.HSet(ctx, fixtureRecordKey(fx.Space, "work", "card"),
		"epoch", "0", "revision", "1", "place:work", "r:c", "state", "retained").Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.ZAdd(ctx, fixtureRowsKey(fx.Space, "work", "0"),
		redis.Z{Score: 0, Member: "r"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "c"),
		redis.Z{Score: 1, Member: "card"}).Err(); err != nil {
		t.Fatal(err)
	}
	fx.Epoch = "1"
	fx.seedEpoch(t)
	if err := fx.Client.HSet(ctx, fx.Space+"table:work", "member_prefix",
		fx.Space+"member:work_live:").Err(); err != nil {
		t.Fatal(err)
	}
	current, err := fx.Client.HGetAll(ctx, fx.Space+"table:work").Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.HSet(ctx, fixtureDefinitionKey(fx.Space, "work", "1"), current).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.ZAdd(ctx, fixtureRowsKey(fx.Space, "work", "1"),
		redis.Z{Score: 0, Member: "r"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "1", "r", "c"),
		redis.Z{Score: 9, Member: "card"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.HSet(ctx, fx.Space+"member:work_live:card",
		"epoch", "1", "revision", "1", "state", "live").Err(); err != nil {
		t.Fatal(err)
	}
	fx.ActivateWithLua(t, al5DefinitionSealProbeLua)
	before := commitProbeImage(t, fx.Client)
	for _, mode := range []string{"public_load", "injected_def", "post_read_poison",
		"returned_def", "forged_definition_key", "forged_placement_keys"} {
		statsBefore := readExtensionCommandStats(t, fx.Client)
		reply := al5DefinitionSealCall(t, fx, "0", mode)
		statsAfter := readExtensionCommandStats(t, fx.Client)
		if mode == "returned_def" {
			var answer struct {
				Value string `json:"value"`
				Score string `json:"score"`
			}
			if reply.Status != "read" || len(reply.Answers) != 1 ||
				json.Unmarshal(reply.Answers[0], &answer) != nil ||
				answer.Value != "retained" || answer.Score != "1" {
				t.Errorf("returned definition poisoned retained record: %+v answer=%+v", reply, answer)
			}
		} else {
			if reply.Status != "refused" || reply.Code != "CONFIG" || len(reply.Answers) != 0 {
				t.Errorf("%s bypassed retained definition: %+v", mode, reply)
			}
			if mode != "post_read_poison" {
				for _, command := range []string{"HLEN", "HGETALL", "HMGET"} {
					if got := readExtensionExecutedDelta(t, statsBefore, statsAfter, command); got != 0 {
						t.Errorf("%s executed %s %d times before refusal", mode, command, got)
					}
				}
			}
		}
		if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s changed Redis image", mode)
		}
	}
}

func TestAL5ActiveDynamicDefinitionStillLoads(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	if err := fx.Client.HSet(context.Background(), fixtureRecordKey(fx.Space, "work", "card"),
		"epoch", "0", "revision", "1", "state", "active").Err(); err != nil {
		t.Fatal(err)
	}
	fx.ActivateWithLua(t, al5DefinitionSealProbeLua)
	before := commitProbeImage(t, fx.Client)
	reply := al5DefinitionSealCall(t, fx, "0", "active_dynamic")
	var answer struct {
		Value string `json:"value"`
	}
	if reply.Status != "read" || len(reply.Answers) != 1 ||
		json.Unmarshal(reply.Answers[0], &answer) != nil || answer.Value != "active" {
		t.Errorf("active callback discovery failed: %+v answer=%+v", reply, answer)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("active dynamic read changed Redis image")
	}
}

// The callback can mutate the public context and returned projections. Those
// values must never become the read invocation's cached store observations.
const al5PrivateCacheProbeLua = `
redis.register_function('ns_tset_al5_private_cache_probe',function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local mode=args[3]
  local extension={kinds={'cacheguard'}}
  extension.validate=function(q,index)
    if not S.is_object(q) or q.kind~='cacheguard' then
      return nil,S.refuse('REQUEST',{query_index=index})
    end
    for key in pairs(q) do
      if key~='kind' then return nil,S.refuse('REQUEST',{query_index=index}) end
    end
    return true,nil
  end
  extension.read=function(ctx,q,index)
    if mode=='inject_cell' then
      ctx.row_scores={[ctx.space..'table:work:rows']={missing='99'}}
      return {kind='cacheguard'},nil
    end
    if mode=='inject' then
      ctx.before={work={card={exists=true,epoch='0',revision='1',
        place={row='r',col='c'},score='99',fields={state={present=true,value='fake'}}}}}
      ctx.row_scores={[ctx.space..'table:work:rows']={r='99'}}
    end
    local cell_start=ctx.budget.cell
    local found,err=S.before(ctx,'work',{'card'},{'state'})
    if err then return nil,err end
    if mode=='refuse' then return nil,S.refuse('CONFIG',{query_index=index}) end
    if mode=='throw' then error('intentional cache callback failure') end
    local first=found.card
    if mode=='detach_before' then
      first.fields.state.value='fake'
      first.place.row='other'
      first.score='99'
      found,err=S.before(ctx,'work',{'card'},{'state'})
      if err then return nil,err end
      first=found.card
    elseif mode=='detach_whole' then
      local whole,names
      whole,names,err=S.before_whole(ctx,'work','card')
      if err then return nil,err end
      whole.fields.state.value='fake'
      whole.place.row='other'
      whole.score='99'
      names[1]='fake'
      whole.whole_names[1]='fake'
      whole,names,err=S.before_whole(ctx,'work','card')
      if err then return nil,err end
      if names[1]~='state' or whole.whole_names[1]~='state' then
        return nil,S.refuse('CONFIG',{query_index=index})
      end
      first=whole
    elseif mode=='shared_probe' then
      found,err=S.before(ctx,'work',{'card'},{'state'})
      if err then return nil,err end
      first=found.card
    end
    return {kind='cacheguard',value=first.fields.state.value,
      score=first.score,row=first.place.row,
      record=ctx.budget.record,field=ctx.budget.field,
      cell_delta=ctx.budget.cell-cell_start},nil
  end
  return S.read(args[1],args[2],nil,extension)
end)
`

func al5PrivateCacheCall(t *testing.T, fx *tsetFixture, mode string, sharedRow bool) al5GuardrailReply {
	t.Helper()
	queries := `[{"kind":"cacheguard"}]`
	if sharedRow {
		queries = `[{"kind":"count","t":"work","cells":["r:c"]},{"kind":"cacheguard"}]`
	} else if mode == "inject_cell" {
		queries = `[{"kind":"cacheguard"},{"kind":"count","t":"work","cells":["missing:c"]}]`
	}
	raw := readExtensionRaw(fx.Space, "0", "atomic", queries)
	wire, err := fx.Client.FCall(context.Background(), "ns_tset_al5_private_cache_probe",
		nil, Version, raw, mode).Text()
	if err != nil {
		t.Fatalf("private-cache %s: %v", mode, err)
	}
	var reply al5GuardrailReply
	if err := json.Unmarshal([]byte(wire), &reply); err != nil {
		t.Fatalf("decode private-cache %s: %v", mode, err)
	}
	return reply
}

func TestAL5PrivateReadCachesResistPublicMutation(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	ctx := context.Background()
	if err := fx.Client.HSet(ctx, fixtureRecordKey(fx.Space, "work", "card"),
		"epoch", "0", "revision", "1", "place:work", "r:c", "state", "ready").Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.ZAdd(ctx, fixtureRowsKey(fx.Space, "work", "0"),
		redis.Z{Score: 0, Member: "r"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "c"),
		redis.Z{Score: 1, Member: "card"}).Err(); err != nil {
		t.Fatal(err)
	}
	fx.ActivateWithLua(t, al5PrivateCacheProbeLua)
	before := commitProbeImage(t, fx.Client)
	for _, tc := range []struct {
		mode      string
		sharedRow bool
		answers   int
		zscores   int
		cellDelta int
		records   int
		fields    int
	}{
		// Callback-only plans load work lazily: definition HLEN/HGETALL and
		// placement row/member ZSCORE each charge one cell observation.
		{mode: "inject", answers: 1, zscores: 2, cellDelta: 4, records: 1, fields: 1},
		{mode: "detach_before", answers: 1, zscores: 2, cellDelta: 4, records: 2, fields: 2},
		{mode: "detach_whole", answers: 1, zscores: 2, cellDelta: 4, records: 3, fields: 3},
		{mode: "shared_probe", sharedRow: true, answers: 2, zscores: 2, cellDelta: 1, records: 2, fields: 2},
		{mode: "inject_cell", answers: 0, zscores: 1},
		{mode: "refuse", answers: 0, zscores: 2},
		{mode: "inject", answers: 1, zscores: 2, cellDelta: 4, records: 1, fields: 1},
		{mode: "throw", answers: 0, zscores: 2},
		{mode: "inject", answers: 1, zscores: 2, cellDelta: 4, records: 1, fields: 1},
	} {
		statsBefore := readExtensionCommandStats(t, fx.Client)
		reply := al5PrivateCacheCall(t, fx, tc.mode, tc.sharedRow)
		statsAfter := readExtensionCommandStats(t, fx.Client)
		if tc.mode == "throw" || tc.mode == "refuse" || tc.mode == "inject_cell" {
			code := "CONFIG"
			if tc.mode == "inject_cell" {
				code = "NOROW"
			}
			if reply.Status != "refused" || reply.Code != code || len(reply.Answers) != 0 {
				t.Errorf("%s did not refuse atomically: %+v", tc.mode, reply)
			}
		} else {
			var answer struct {
				Value     string `json:"value"`
				Score     string `json:"score"`
				Row       string `json:"row"`
				Record    int    `json:"record"`
				Field     int    `json:"field"`
				CellDelta int    `json:"cell_delta"`
			}
			if reply.Status != "read" || len(reply.Answers) != tc.answers ||
				json.Unmarshal(reply.Answers[len(reply.Answers)-1], &answer) != nil ||
				answer.Value != "ready" || answer.Score != "1" || answer.Row != "r" ||
				answer.Record != tc.records || answer.Field != tc.fields ||
				answer.CellDelta != tc.cellDelta ||
				reply.Counters.Record != tc.records || reply.Counters.Field != tc.fields {
				t.Errorf("%s reused mutable cache or lost charges: %+v answer=%+v", tc.mode, reply, answer)
			}
		}
		if got := readExtensionExecutedDelta(t, statsBefore, statsAfter, "ZSCORE"); got != int64(tc.zscores) {
			t.Errorf("%s ran %d row/member ZSCORE commands, want %d", tc.mode, got, tc.zscores)
		}
		if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s changed Redis image", tc.mode)
		}
	}
}
