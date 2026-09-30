//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// This callback exists only in the test library. The equal_value counter
// counts recursive nodes, not elapsed time, and is read after each request.
const callbackBoundaryProbeLua = `
local limit_trap_count=0
redis.register_function('ns_tset_callback_boundary_probe',function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=4 then return S.json.encode(S.refuse('ARGS')) end
  local mode,loops=args[3],tonumber(args[4])
  if not loops or loops<0 or loops>40 or loops%1~=0 then return S.json.encode(S.refuse('ARGS')) end
  if mode=='limit_meter' then
    local _=S.limits.__callback_boundary_missing_cap
    local visits=limit_trap_count
    limit_trap_count=0
    return S.json.encode({trap_count=visits})
  end
  local escaped_attempts,escaped_ok,calls,record_delta,field_delta,direct_code=0,0,0,0,0,nil
  local extension={kinds={'boundary'}}
  extension.validate=function(q,index)
    if q.kind~='boundary' then return nil,S.refuse('REQUEST',{query_index=index}) end
    for k in pairs(q) do if k~='kind' then return nil,S.refuse('REQUEST',{query_index=index}) end end
    return true,nil
  end
  extension.read=function(ctx,q,index)
    local marker={__metatable=false}
    if mode=='root' or mode=='root_false' or mode=='root_spoof' then
      -- The old answer() writes read_encoded after S.read_callback returns.
      -- A __newindex hook then ran with callback_depth=0 and could read and
      -- mutate the next validated query. The attempted read itself is counted.
      ctx.read_encoded=nil
      local function escape(tab,key,value)
        if key=='read_encoded' then
          escaped_attempts=escaped_attempts+1
          local target=ctx.space..'table:work'
          local _,err=S.readcmd(ctx,{argv={'TYPE',target},
            access={{key=target,kind='any',mode='read'}}},16,'metadata')
          if not err then escaped_ok=escaped_ok+1 end
          ctx.request.queries[2].t='other'
        end
        rawset(tab,key,value)
      end
      if mode=='root' then marker={__newindex=escape}
      elseif mode=='root_spoof' then marker={__newindex=escape,__metatable={__is_cjson_array=true}}
      else marker={__newindex=escape,__metatable=false} end
      setmetatable(ctx,marker)
    elseif mode=='budget' then setmetatable(ctx.budget,marker)
    elseif mode=='request_deep' then setmetatable(ctx.request.queries[1],marker)
    elseif mode=='cache' then setmetatable(ctx.before,marker)
    elseif mode=='read_emit' then setmetatable(ctx.read_emit,marker)
    elseif mode=='original' then
      local detached=ctx.original_rowsets
      setmetatable(detached,marker)
      ctx.original_rowsets={}
    elseif mode=='table_key' then ctx.before[setmetatable({},marker)]='bad'
    elseif mode=='cycle' then ctx.before.cycle=ctx.before
    elseif mode=='limits' then
      setmetatable(ctx.limits,{__metatable=false,__index=function()
        limit_trap_count=limit_trap_count+1
        return nil
      end})
    elseif mode=='result' then
      return setmetatable({kind='boundary',values=S.array()},marker),nil
    elseif mode=='result_spoof' then
      return setmetatable({kind='boundary',values=S.array()},
        {__metatable={__is_cjson_array=true},__newindex=function() end}),nil
    elseif mode=='error' then
      return nil,setmetatable(S.refuse('EPOCHGONE',{query_index=index}),marker)
    elseif mode=='thrown_object' then
      error(setmetatable({message='callback threw'},marker))
    elseif mode=='direct' then
      local target=ctx.space..'table:work'
      local _,err=S.readcmd(ctx,{argv={'TYPE',target},
        access={{key=target,kind='any',mode='read'}}},16,'metadata')
      direct_code=err and err.code or 'OK'
    elseif mode=='cost' then
      local before_record,before_field=ctx.budget.record,ctx.budget.field
      for _=1,loops do
        local _,err=S.read_record(ctx,'work','card',{'brief'},index)
        if err then return nil,err end
        calls=calls+1
      end
      record_delta=ctx.budget.record-before_record
      field_delta=ctx.budget.field-before_field
    elseif mode~='normal' then return nil,S.refuse('REQUEST',{query_index=index}) end
    return {kind='boundary',values=S.array()},nil
  end
  local before=NS.__callback_equal_visits()
  local wire=S.read(args[1],args[2],nil,extension)
  return S.json.encode({reply=S.json.decode(wire),visits=NS.__callback_equal_visits()-before,
    escaped_attempts=escaped_attempts,escaped_ok=escaped_ok,calls=calls,
    record_delta=record_delta,field_delta=field_delta,direct_code=direct_code})
end)
`

const (
	callbackBoundaryName          = "ns_tset_callback_boundary_probe"
	callbackBoundaryTrailingCells = 4096
)

type callbackBoundaryResult struct {
	Visits          int    `json:"visits"`
	EscapedAttempts int    `json:"escaped_attempts"`
	EscapedOK       int    `json:"escaped_ok"`
	Calls           int    `json:"calls"`
	RecordDelta     int    `json:"record_delta"`
	FieldDelta      int    `json:"field_delta"`
	DirectCode      string `json:"direct_code"`
	TrapCount       int    `json:"trap_count"`
	Reply           struct {
		Status string `json:"status"`
		Code   string `json:"code"`
		Detail struct {
			QueryIndex *int `json:"query_index"`
		} `json:"detail"`
		Answers []struct {
			Kind string `json:"kind"`
			Rows []struct {
				Row string `json:"row"`
			} `json:"rows"`
		} `json:"answers"`
		Counters map[string]int `json:"counters"`
	} `json:"reply"`
}

func callbackBoundaryActivate(t *testing.T, fx *tsetFixture) {
	t.Helper()
	fx.mustInitialize(t)
	if fx.loaded {
		t.Fatal("callback boundary probe must load during fixture initialization")
	}
	source := tsetTestSourceWithProbe(t, fn.TSetStandalone, callbackBoundaryProbeLua, callbackBoundaryName)
	const equalAnchor = "  local function equal_value(a,b,depth)\n"
	const getterAnchor = "  local sealed_fields="
	if strings.Count(source, equalAnchor) != 1 || strings.Count(source, getterAnchor) != 1 {
		t.Fatal("callback boundary equal_value anchors changed")
	}
	source = strings.Replace(source, equalAnchor,
		"  local callback_equal_visits=0\n"+equalAnchor+
			"    callback_equal_visits=callback_equal_visits+1\n", 1)
	source = strings.Replace(source, getterAnchor,
		"  NS.__callback_equal_visits=function() return callback_equal_visits end\n"+getterAnchor, 1)
	if err := fx.Client.FunctionLoad(context.Background(), source).Err(); err != nil {
		t.Fatalf("load callback boundary test library: %v", err)
	}
	tsetRequireProbeLoaded(t, fx.Client, callbackBoundaryName)
	fx.loaded = true
	fx.flushPendingRows(t)
	fx.active = true
}

func callbackBoundaryFixture(t *testing.T) *tsetFixture {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "ready")
	fx.Define(t, "other", "ready")
	fx.AddRow(t, "work", "w", 0)
	fx.AddRow(t, "other", "o", 0)
	callbackBoundaryActivate(t, fx)
	store := newFixtureRedis(t, fx.Client)
	step, err := store.Step(context.Background(), Step{Epoch: Decimal(fx.Epoch), Space: fx.Space,
		Entries: []Entry{{Kind: "create", Table: "work", To: "w:ready",
			IDs: []string{"card"}, Scores: []string{"1"}, Set: map[string]string{"brief": "seed"}}}})
	if err != nil || step.Status != "ok" || step.Changed != 1 {
		t.Fatalf("seed public card: reply=%+v err=%v", step, err)
	}
	return fx
}

func callbackBoundaryRaw(t *testing.T, space string, trailing bool) string {
	t.Helper()
	queries := []map[string]any{{"kind": "boundary"}}
	if trailing {
		cells := make([]string, callbackBoundaryTrailingCells)
		for i := range cells {
			cells[i] = "missing:ready"
		}
		queries = append(queries, map[string]any{"kind": "count", "t": "work", "cells": cells})
	} else {
		queries = append(queries, map[string]any{"kind": "rows", "t": "work"})
	}
	encoded, err := json.Marshal(map[string]any{"epoch": "0", "space": space,
		"mode": "atomic", "queries": queries})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func callbackBoundaryCall(t *testing.T, fx *tsetFixture, raw, mode string, loops int) callbackBoundaryResult {
	t.Helper()
	wire, err := fx.Client.FCall(context.Background(), callbackBoundaryName, nil,
		Version, raw, mode, loops).Text()
	if err != nil {
		t.Fatalf("callback boundary %s/%d: %v", mode, loops, err)
	}
	var got callbackBoundaryResult
	if err := json.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatalf("decode callback boundary %s/%d: %v: %s", mode, loops, err, wire)
	}
	return got
}

func callbackBoundaryRequireRecovered(t *testing.T, fx *tsetFixture, raw string) {
	t.Helper()
	ordinaryRaw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"rows","t":"work"}]`)
	wire, err := fx.Client.FCall(context.Background(), "ns_tset_read", nil, Version, ordinaryRaw).Text()
	if err != nil {
		t.Fatalf("ordinary read after rejected callback: %v", err)
	}
	var ordinary callbackBoundaryResult
	if err := json.Unmarshal([]byte(`{"reply":`+wire+`}`), &ordinary); err != nil {
		t.Fatalf("decode ordinary recovery read: %v: %s", err, wire)
	}
	if ordinary.Reply.Status != "read" || len(ordinary.Reply.Answers) != 1 ||
		ordinary.Reply.Answers[0].Kind != "rows" || len(ordinary.Reply.Answers[0].Rows) != 1 ||
		ordinary.Reply.Answers[0].Rows[0].Row != "w" {
		t.Fatalf("ordinary read did not recover original work row: %+v", ordinary.Reply)
	}
	direct := callbackBoundaryCall(t, fx, raw, "direct", 0)
	if direct.Reply.Status != "read" || direct.DirectCode != "CONFIG" ||
		direct.EscapedAttempts != 0 || direct.EscapedOK != 0 {
		t.Fatalf("direct callback read escaped after rejected callback: %+v", direct)
	}
}

func TestCallbackBoundaryRejectsMetatableEscape(t *testing.T) {
	t.Parallel()
	fx := callbackBoundaryFixture(t)
	raw := callbackBoundaryRaw(t, fx.Space, false)
	for _, mode := range []string{"root", "root_false", "root_spoof", "budget",
		"request_deep", "cache", "read_emit", "original", "table_key", "cycle",
		"result", "result_spoof", "error", "thrown_object", "limits"} {
		t.Run(mode, func(t *testing.T) {
			before := commitProbeImage(t, fx.Client)
			got := callbackBoundaryCall(t, fx, raw, mode, 0)
			if got.Reply.Status != "refused" || got.Reply.Code != "CONFIG" ||
				len(got.Reply.Answers) != 0 || got.EscapedAttempts != 0 || got.EscapedOK != 0 {
				t.Fatalf("metatable %s crossed callback boundary or read store: %+v", mode, got)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Fatalf("metatable %s changed Redis state on refusal: before=%v after=%v", mode, before, after)
			}
			callbackBoundaryRequireRecovered(t, fx, raw)
			if mode == "limits" {
				meter := callbackBoundaryCall(t, fx, raw, "limit_meter", 0)
				if meter.TrapCount != 0 {
					t.Fatalf("rejected limits metatable leaked into global caps: %+v", meter)
				}
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Fatalf("metatable %s recovery changed Redis state: before=%v after=%v", mode, before, after)
			}
		})
	}
	control := callbackBoundaryCall(t, fx, raw, "normal", 0)
	if control.Reply.Status != "read" || len(control.Reply.Answers) != 2 ||
		control.Reply.Answers[0].Kind != "boundary" || control.Reply.Answers[1].Kind != "rows" ||
		len(control.Reply.Answers[1].Rows) != 1 || control.Reply.Answers[1].Rows[0].Row != "w" {
		t.Fatalf("valid marked request/result arrays rejected: %+v", control)
	}
}

func TestCallbackCheckedReadsDoNotWalkWholeRequestPerCall(t *testing.T) {
	t.Parallel()
	fx := callbackBoundaryFixture(t)
	raw := callbackBoundaryRaw(t, fx.Space, true)
	if len(raw) < 32*1024 || len(raw) >= 4*1024*1024 {
		t.Fatalf("trailing valid count request has unexpected size %d", len(raw))
	}
	one := callbackBoundaryCall(t, fx, raw, "cost", 1)
	many := callbackBoundaryCall(t, fx, raw, "cost", 40)
	for _, tc := range []struct {
		name  string
		got   callbackBoundaryResult
		loops int
	}{{"one", one, 1}, {"many", many, 40}} {
		if tc.got.Reply.Status != "refused" || tc.got.Reply.Code != "NOROW" ||
			tc.got.Reply.Detail.QueryIndex == nil || *tc.got.Reply.Detail.QueryIndex != 1 ||
			tc.got.Calls != tc.loops || tc.got.RecordDelta != tc.loops ||
			tc.got.FieldDelta != tc.loops || tc.got.Visits <= 0 {
			t.Fatalf("%s callback did not execute/charge and reach trailing NOROW: %+v", tc.name, tc.got)
		}
	}
	// One extra whole-request walk visits at least 4096 trailing cell strings.
	// The 39 additional cached record projections need only shallow checks;
	// a 2048-node allowance discriminates old per-call original_unchanged.
	if growth := many.Visits - one.Visits; growth < 0 || growth >= callbackBoundaryTrailingCells/2 {
		t.Fatalf("39 cached callback reads revisited whole request: visits %d -> %d (growth %d)",
			one.Visits, many.Visits, growth)
	}
	t.Logf("callback cost: request bytes=%d, calls=%d/%d, record=%d/%d, field=%d/%d, equal_value visits=%d/%d",
		len(raw), one.Calls, many.Calls, one.RecordDelta, many.RecordDelta,
		one.FieldDelta, many.FieldDelta, one.Visits, many.Visits)
}
