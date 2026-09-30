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
