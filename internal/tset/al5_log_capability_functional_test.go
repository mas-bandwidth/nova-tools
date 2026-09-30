//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

// This standalone, test-only library initializer holds the same narrow L1
// capabilities that the reviewed composed log fragment binds. It exercises
// authority and raw XRANGE accounting, not L2 cache, decode, or projection.
const l1LineCapabilityProbeLua = `
local S=NS.tset
local authorize_line,read_line_raw
local function expected_capability_refusal(err,index)
  if err then return err end
  return S.refuse('DRIFT',{query_index=index,capability_probe='accepted_invalid_authority'})
end
S.bind_log_helpers(256,function(authorize,raw)
  authorize_line,read_line_raw=authorize,raw
  return function() end
end)
local function denied_capability(ctx,seq,index,probe_index)
  local _,err=authorize_line(ctx,seq,probe_index)
  if type(err)~='table' or err.status~='refused' or err.code~='CONFIG' then
    return expected_capability_refusal(nil,index)
  end
  _,err=read_line_raw(ctx,seq,probe_index)
  if type(err)~='table' or err.status~='refused' or err.code~='CONFIG' then
    return expected_capability_refusal(nil,index)
  end
  return err
end
local prior_ctx
redis.register_function('ns_tset_l1_line_cap_probe',function(keys,args)
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local mode=args[3]
  local extension={kinds={'linecap'}}
  extension.validate=function(q,index)
    if not S.is_object(q) or q.kind~='linecap' then
      return nil,S.refuse('REQUEST',{query_index=index})
    end
    for key in pairs(q) do
      if key~='kind' then return nil,S.refuse('REQUEST',{query_index=index}) end
    end
    return true,nil
  end
  extension.read=function(ctx,q,index)
    local seq='7'
    if mode=='old_context' then
      if not prior_ctx then return nil,expected_capability_refusal(nil,index) end
      return nil,denied_capability(prior_ctx,seq,index,index)
    end
    if mode=='fake_context' or mode=='zero_budget_copy' then
      local fake={}
      for k,v in pairs(ctx) do fake[k]=v end
      if mode=='zero_budget_copy' then
        fake.budget={fetched_bytes=0,cell=0,store_commands=0}
      end
      return nil,denied_capability(fake,seq,index,index)
    end
    if mode=='bad_zero' then seq='0'
    elseif mode=='bad_leading' then seq='07'
    elseif mode=='bad_over' then seq='9007199254740992'
    elseif mode=='bad_text' then seq='bad'
    elseif mode=='absent' then seq='8' end
    if mode=='wrong_index' then
      return nil,denied_capability(ctx,seq,index,index+1)
    end
    if mode=='mutated_index' then
      local original=ctx.query_index
      ctx.query_index=index+1
      local err=denied_capability(ctx,seq,index,index)
      ctx.query_index=original
      return nil,err
    end
    if mode=='mutated_request' then
      local original=ctx.request.epoch
      ctx.request.epoch='1'
      local err=denied_capability(ctx,seq,index,index)
      ctx.request.epoch=original
      return nil,err
    end
    if mode=='forged_key' then
      ctx.log_key=function() return 'outside:sprint:log@0' end
    end
    if mode=='injected_public_cache' then
      ctx.log_lines={['7']={{'7-0',{'d','forged'}}}}
    end
    if mode=='short_reservation' then
      ctx.budget.fetched_bytes=S.limits.fetched_bytes-255
    elseif mode=='exact_fit' or mode=='public_exact_fit' then
      ctx.budget.fetched_bytes=S.limits.fetched_bytes-256
    elseif mode=='public_limit' then
      ctx.budget.fetched_bytes=S.limits.fetched_bytes-255
    end
    if mode=='cell_exhausted' then
      local _,err=S.charge(ctx,'cell',S.limits.cell-ctx.budget.cell)
      if err then return nil,err end
    end
    if mode=='nonfinite_fetched' or mode=='nonfinite_other' then
      local unit=mode=='nonfinite_fetched' and 'fetched_bytes' or 'field'
      local prior=ctx.budget[unit]
      ctx.budget[unit]=math.huge
      local _,err,short=read_line_raw(ctx,seq,index)
      ctx.budget[unit]=prior
      if short then return {kind='linecap',short=true},nil end
      return nil,expected_capability_refusal(err,index)
    end
    if mode=='direct_readcmd' or mode=='direct_rd' then
      local key=ctx.space..'sprint:log@'..ctx.request_epoch
      local _,err
      if mode=='direct_readcmd' then
        _,err=S.readcmd(ctx,{argv={'XRANGE',key,'7-0','7-0','COUNT','1'},
          access={{key=key,kind='stream',mode='read'}}},256,'log')
      else
        _,err=S.rd(ctx,{'XRANGE',key,'7-0','7-0','COUNT','1'},'stream',256,'log')
      end
      return nil,expected_capability_refusal(err,index)
    end
    if mode=='throw' then error('line-capability callback throw') end
    local nested_code=''
    if mode=='nested_read' then
      nested_code=S.json.decode(S.read(args[1],args[2],nil,extension)).code
    elseif mode=='nested_context' then
      local _,err=S.context(ctx.request,'read')
      nested_code=err and err.code or ''
    elseif mode=='nested_release' then
      local _,err=S.release_context(ctx)
      nested_code=err and err.code or ''
    end
    local key,authority_error=authorize_line(ctx,seq,index)
    if authority_error then return nil,authority_error end
    if key~=ctx.space..'sprint:log@'..ctx.request_epoch then
      return nil,S.refuse('CONFIG',{query_index=index})
    end
    local cells,stores,fetched=ctx.budget.cell,ctx.budget.store_commands,ctx.budget.fetched_bytes
    local saved_limit
    if mode=='public_limit' or mode=='public_exact_fit' then
      saved_limit=S.limits.fetched_bytes
      S.limits.fetched_bytes=saved_limit+512
    end
    local batch,err,short=read_line_raw(ctx,seq,index,0,'ignored override')
    if saved_limit then S.limits.fetched_bytes=saved_limit end
    if err then return nil,err end
    if mode=='remember' then prior_ctx=ctx end
    local entry=batch and batch[1]
    return {kind='linecap',count=batch and #batch or 0,
      id=entry and entry[1] or '',data=entry and entry[2] and entry[2][2] or '',
      short=short,cell_delta=ctx.budget.cell-cells,
      store_delta=ctx.budget.store_commands-stores,
      fetched_delta=ctx.budget.fetched_bytes-fetched,
      nested_code=nested_code,
      binder_hidden=S.bind_log_helpers==nil and S.bind_read_helpers==nil},nil
  end
  return S.read(args[1],args[2],nil,extension)
end)
`

type l1LineCapabilityReply struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Detail struct {
		QueryIndex *int   `json:"query_index"`
		Budget     string `json:"budget"`
	} `json:"detail"`
	Answers []struct {
		Kind         string `json:"kind"`
		Count        int    `json:"count"`
		ID           string `json:"id"`
		Data         string `json:"data"`
		Short        bool   `json:"short"`
		CellDelta    int    `json:"cell_delta"`
		StoreDelta   int    `json:"store_delta"`
		FetchedDelta int    `json:"fetched_delta"`
		NestedCode   string `json:"nested_code"`
		BinderHidden bool   `json:"binder_hidden"`
	} `json:"answers"`
}

func l1LineCapabilityCall(t *testing.T, fx *tsetFixture, epoch, mode string) l1LineCapabilityReply {
	t.Helper()
	raw := readExtensionRaw(fx.Space, epoch, "atomic", `[{"kind":"linecap"}]`)
	wire, err := fx.Client.FCall(context.Background(), "ns_tset_l1_line_cap_probe",
		nil, Version, raw, mode).Text()
	if err != nil {
		t.Fatalf("line capability %s: %v", mode, err)
	}
	var reply l1LineCapabilityReply
	if err := json.Unmarshal([]byte(wire), &reply); err != nil {
		t.Fatalf("decode line capability %s: %v: %s", mode, err, wire)
	}
	return reply
}

func seedL1LineCapabilityStream(t *testing.T, client *redis.Client, key, id, data string) {
	t.Helper()
	if err := client.XAdd(context.Background(), &redis.XAddArgs{
		Stream: key, ID: id, Values: map[string]any{"d": data},
	}).Err(); err != nil {
		t.Fatalf("seed %s %s: %v", key, id, err)
	}
}

func TestL1ExactLineCapabilityAuthorityAndAccounting(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.ActivateWithLua(t, l1LineCapabilityProbeLua)
	key := fx.Space + "sprint:log@0"
	seedL1LineCapabilityStream(t, fx.Client, key, "7-0", "inside")
	seedL1LineCapabilityStream(t, fx.Client, key, "7-1", "decoy")
	seedL1LineCapabilityStream(t, fx.Client, "outside:sprint:log@0", "7-0", "outside")
	before := commitProbeImage(t, fx.Client)
	for _, tc := range []struct {
		mode, code, budget, data, nested string
		count, xrange                    int64
		short                            bool
	}{
		{mode: "source", data: "inside", count: 1, xrange: 1},
		{mode: "remember", data: "inside", count: 1, xrange: 1},
		{mode: "old_context", code: "CONFIG"},
		{mode: "fake_context", code: "CONFIG"},
		{mode: "zero_budget_copy", code: "CONFIG"},
		{mode: "forged_key", data: "inside", count: 1, xrange: 1},
		{mode: "injected_public_cache", data: "inside", count: 1, xrange: 1},
		{mode: "wrong_index", code: "CONFIG"},
		{mode: "mutated_index", code: "CONFIG"},
		{mode: "mutated_request", code: "CONFIG"},
		{mode: "bad_zero", code: "REQUEST"},
		{mode: "bad_leading", code: "REQUEST"},
		{mode: "bad_over", code: "REQUEST"},
		{mode: "bad_text", code: "REQUEST"},
		{mode: "cell_exhausted", code: "BUDGET", budget: "cell"},
		{mode: "nonfinite_fetched", code: "CONFIG"},
		{mode: "nonfinite_other", code: "CONFIG"},
		{mode: "short_reservation", short: true},
		{mode: "exact_fit", data: "inside", count: 1, xrange: 1},
		{mode: "public_limit", short: true},
		{mode: "public_exact_fit", data: "inside", count: 1, xrange: 1},
		{mode: "direct_readcmd", code: "CONFIG"},
		{mode: "direct_rd", code: "CONFIG"},
		{mode: "nested_read", data: "inside", nested: "CONFIG", count: 1, xrange: 1},
		{mode: "nested_context", data: "inside", nested: "CONFIG", count: 1, xrange: 1},
		{mode: "nested_release", data: "inside", nested: "CONFIG", count: 1, xrange: 1},
		{mode: "throw", code: "CONFIG"},
		{mode: "absent", count: 0, xrange: 1},
		{mode: "source", data: "inside", count: 1, xrange: 1},
	} {
		statsBefore := readExtensionCommandStats(t, fx.Client)
		reply := l1LineCapabilityCall(t, fx, "0", tc.mode)
		statsAfter := readExtensionCommandStats(t, fx.Client)
		if got := readExtensionExecutedDelta(t, statsBefore, statsAfter, "XRANGE"); got != tc.xrange {
			t.Errorf("%s executed %d XRANGE, want %d", tc.mode, got, tc.xrange)
		}
		if tc.code != "" {
			if reply.Status != "refused" || reply.Code != tc.code ||
				reply.Detail.Budget != tc.budget || len(reply.Answers) != 0 ||
				reply.Detail.QueryIndex == nil || *reply.Detail.QueryIndex != 0 {
				t.Errorf("%s refusal = %+v, want %s without answers", tc.mode, reply, tc.code)
			}
		} else if reply.Status != "read" || len(reply.Answers) != 1 ||
			reply.Answers[0].Count != int(tc.count) || reply.Answers[0].Data != tc.data ||
			reply.Answers[0].Short != tc.short || reply.Answers[0].NestedCode != tc.nested ||
			!reply.Answers[0].BinderHidden {
			t.Errorf("%s answer = %+v", tc.mode, reply)
		} else if tc.xrange == 1 &&
			(reply.Answers[0].CellDelta != 1 || reply.Answers[0].StoreDelta != 2 ||
				(tc.count == 0 && reply.Answers[0].FetchedDelta != 0) ||
				(tc.count > 0 && (reply.Answers[0].FetchedDelta <= 0 ||
					reply.Answers[0].FetchedDelta > 256))) {
			t.Errorf("%s raw XRANGE cost = %+v", tc.mode, reply.Answers[0])
		} else if tc.short && (reply.Answers[0].CellDelta != 0 ||
			reply.Answers[0].StoreDelta != 0 || reply.Answers[0].FetchedDelta != 0) {
			t.Errorf("%s short read performed work: %+v", tc.mode, reply.Answers[0])
		}
		if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s changed Redis image", tc.mode)
		}
	}
}

func TestL1ExactLineCapabilityUsesSealedRequestedEpoch(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Epoch = "1"
	fx.seedEpoch(t)
	fx.ActivateWithLua(t, l1LineCapabilityProbeLua)
	seedL1LineCapabilityStream(t, fx.Client, fx.Space+"sprint:log@0", "7-0", "retained")
	seedL1LineCapabilityStream(t, fx.Client, fx.Space+"sprint:log@1", "7-0", "active")
	before := commitProbeImage(t, fx.Client)
	answer := l1LineCapabilityCall(t, fx, "0", "source")
	if answer.Status != "read" || len(answer.Answers) != 1 ||
		answer.Answers[0].Data != "retained" {
		t.Errorf("requested-epoch exact line = %+v", answer)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("requested-epoch read changed Redis image")
	}
}

func TestL1ExactLineCapabilitySequentialQueries(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.ActivateWithLua(t, l1LineCapabilityProbeLua)
	seedL1LineCapabilityStream(t, fx.Client, fx.Space+"sprint:log@0", "7-0", "inside")
	raw := readExtensionRaw(fx.Space, "0", "atomic",
		`[{"kind":"linecap"},{"kind":"linecap"}]`)
	statsBefore := readExtensionCommandStats(t, fx.Client)
	wire, err := fx.Client.FCall(context.Background(), "ns_tset_l1_line_cap_probe",
		nil, Version, raw, "source").Text()
	if err != nil {
		t.Fatal(err)
	}
	statsAfter := readExtensionCommandStats(t, fx.Client)
	var reply l1LineCapabilityReply
	if err := json.Unmarshal([]byte(wire), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Status != "read" || len(reply.Answers) != 2 {
		t.Fatalf("sequential read = %+v", reply)
	}
	for i, answer := range reply.Answers {
		if answer.Data != "inside" || answer.ID != "7-0" || answer.CellDelta != 1 {
			t.Errorf("query %d lost private index authority: %+v", i, answer)
		}
	}
	if got := readExtensionExecutedDelta(t, statsBefore, statsAfter, "XRANGE"); got != 2 {
		t.Errorf("two authorized queries executed %d XRANGE", got)
	}
}

func TestL1ExactLineCapabilityDoesNotFallForward(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.ActivateWithLua(t, l1LineCapabilityProbeLua)
	// A loose range starting at 7-0 would return 7-1. Exact 7-0 is absent.
	seedL1LineCapabilityStream(t, fx.Client, fx.Space+"sprint:log@0", "7-1", "later")
	statsBefore := readExtensionCommandStats(t, fx.Client)
	reply := l1LineCapabilityCall(t, fx, "0", "source")
	statsAfter := readExtensionCommandStats(t, fx.Client)
	if reply.Status != "read" || len(reply.Answers) != 1 || reply.Answers[0].Count != 0 {
		t.Errorf("missing exact sequence fell forward: %+v", reply)
	}
	if got := readExtensionExecutedDelta(t, statsBefore, statsAfter, "XRANGE"); got != 1 {
		t.Errorf("exact missing sequence executed %d XRANGE, want one", got)
	}
}

func TestL1ExactLineCapabilityWrongTypeRefusesAtomically(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.ActivateWithLua(t, l1LineCapabilityProbeLua)
	if err := fx.Client.Set(context.Background(), fx.Space+"sprint:log@0", "wrong type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	before := commitProbeImage(t, fx.Client)
	statsBefore := readExtensionCommandStats(t, fx.Client)
	reply := l1LineCapabilityCall(t, fx, "0", "source")
	statsAfter := readExtensionCommandStats(t, fx.Client)
	if reply.Status != "refused" || reply.Code != "WRONGTYPE" || len(reply.Answers) != 0 {
		t.Errorf("wrong-type exact line = %+v", reply)
	}
	if got := readExtensionExecutedDelta(t, statsBefore, statsAfter, "XRANGE"); got != 1 {
		t.Errorf("wrong-type exact line executed %d XRANGE, want one", got)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("wrong-type refusal changed Redis image")
	}
}

func TestL1ExactLineCapabilityACLRefusesBeforeXRANGE(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.ActivateWithLua(t, l1LineCapabilityProbeLua)
	ctx := context.Background()
	key := fx.Space + "sprint:log@0"
	seedL1LineCapabilityStream(t, fx.Client, key, "7-0", "inside")
	const user = "tset_line_no_xrange"
	if err := fx.Client.Do(ctx, "ACL", "SETUSER", user, "reset", "on", ">tset-test-pass",
		"~*", "+@all", "-xrange").Err(); err != nil {
		t.Fatalf("set line reader ACL: %v", err)
	}
	limited := redis.NewClient(&redis.Options{
		Addr: fx.Client.Options().Addr, Username: user,
		Password: "tset-test-pass", MaxRetries: -1,
	})
	t.Cleanup(func() { _ = limited.Close() })
	if err := limited.Ping(ctx).Err(); err != nil {
		t.Fatalf("line reader ACL cannot connect: %v", err)
	}
	if err := limited.XRange(ctx, key, "7-0", "7-0").Err(); err == nil ||
		!strings.Contains(strings.ToUpper(err.Error()), "NOPERM") {
		t.Fatalf("restricted XRANGE = %v, want Redis NOPERM", err)
	}
	before := commitProbeImage(t, fx.Client)
	statsBefore := readExtensionCommandStats(t, fx.Client)
	raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"linecap"}]`)
	wire, err := limited.FCall(ctx, "ns_tset_l1_line_cap_probe", nil, Version, raw, "source").Text()
	if err != nil {
		t.Fatalf("restricted line capability: %v", err)
	}
	statsAfter := readExtensionCommandStats(t, fx.Client)
	var reply l1LineCapabilityReply
	if err := json.Unmarshal([]byte(wire), &reply); err != nil {
		t.Fatalf("decode restricted line capability: %v", err)
	}
	if reply.Status != "refused" || reply.Code != "NOPERM" || len(reply.Answers) != 0 ||
		reply.Detail.QueryIndex == nil || *reply.Detail.QueryIndex != 0 {
		t.Errorf("restricted exact line = %+v, want NOPERM with no answers", reply)
	}
	if got := readExtensionExecutedDelta(t, statsBefore, statsAfter, "XRANGE"); got != 0 {
		t.Errorf("ACL-denied exact line executed %d XRANGE commands", got)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("ACL-denied exact line changed Redis image")
	}
}

const l1LateLineBindProbeLua = `
local S=NS.tset
local captured=S.bind_log_helpers
redis.register_function('ns_tset_l1_late_line_bind_probe',function(keys,args)
  if #keys~=0 or #args~=2 then return S.json.encode(S.refuse('ARGS')) end
  local encoded=S.read(args[1],args[2],nil)
  local ok=pcall(captured,256,function() return function() end end)
  return S.json.encode({reply=S.json.decode(encoded),late_ok=ok,
    binder_hidden=S.bind_log_helpers==nil})
end)
`

func TestL1LineCapabilityCannotBindAfterReadScope(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.ActivateWithLua(t, l1LateLineBindProbeLua)
	raw := readExtensionRaw(fx.Space, "0", "atomic",
		`[{"kind":"done","ops":[{"epoch":"0","op":"none","intent_digest":"0000000000000000000000000000000000000000"}]}]`)
	wire, err := fx.Client.FCall(context.Background(), "ns_tset_l1_late_line_bind_probe",
		nil, Version, raw).Text()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Reply struct {
			Status string `json:"status"`
		} `json:"reply"`
		LateOK       bool `json:"late_ok"`
		BinderHidden bool `json:"binder_hidden"`
	}
	if err := json.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatal(err)
	}
	if got.Reply.Status != "read" || got.LateOK || !got.BinderHidden {
		t.Errorf("late line binding was accepted: %+v", got)
	}
}

func TestL1LineCapabilityRejectsInvalidLoadTimeReservations(t *testing.T) {
	t.Parallel()
	const validFactory = "function() return function() end end"
	const reservationGuard = "attempt to call local 'require_valid_log_reservation' (a boolean value)"
	for _, tc := range []struct {
		name, binding, diagnostic string
	}{
		{name: "zero", binding: "S.bind_log_helpers(0," + validFactory + ")", diagnostic: reservationGuard},
		{name: "negative", binding: "S.bind_log_helpers(-1," + validFactory + ")", diagnostic: reservationGuard},
		{name: "fraction", binding: "S.bind_log_helpers(0.5," + validFactory + ")", diagnostic: reservationGuard},
		{name: "positive_infinity", binding: "S.bind_log_helpers(1/0," + validFactory + ")", diagnostic: reservationGuard},
		{name: "nan", binding: "S.bind_log_helpers(0/0," + validFactory + ")", diagnostic: reservationGuard},
		{name: "over_cap", binding: "S.bind_log_helpers(8388609," + validFactory + ")", diagnostic: reservationGuard},
		{name: "numeric_string", binding: `S.bind_log_helpers("256",` + validFactory + ")", diagnostic: reservationGuard},
		{name: "noncallable_factory", binding: "S.bind_log_helpers(256,1)", diagnostic: "attempt to call local 'factory' (a number value)"},
		{name: "noncallable_cleanup", binding: "S.bind_log_helpers(256,function() return 1 end)", diagnostic: "attempt to call local 'cleanup' (a number value)"},
		{name: "second_bind", binding: "local captured=S.bind_log_helpers\n" +
			"captured(256," + validFactory + ")\n" +
			"captured(256," + validFactory + ")",
			diagnostic: "attempt to call local 'require_open_log_binding' (a boolean value)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixture(t)
			extra := `
local S=NS.tset
` + tc.binding + `
redis.register_function('ns_tset_l1_invalid_line_bind_probe',function() return 'unexpected' end)
`
			source := tsetTestSourceWithProbe(t, fn.TSetStandalone, extra,
				"ns_tset_l1_invalid_line_bind_probe")
			if err := fx.Client.FunctionLoad(context.Background(), source).Err(); err == nil ||
				!strings.Contains(err.Error(), tc.diagnostic) {
				t.Errorf("invalid line binding %s returned %v, want binder diagnostic %q", tc.name, err, tc.diagnostic)
			}
			libraries, err := fx.Client.FunctionList(context.Background(),
				redis.FunctionListQuery{LibraryNamePattern: fn.Library}).Result()
			if err != nil || len(libraries) != 0 {
				t.Errorf("failed line binding installed a library for %s: %+v err=%v", tc.name, libraries, err)
			}
		})
	}
}
