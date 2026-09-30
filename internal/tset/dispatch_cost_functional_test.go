//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// The counter exists only in the test library. It counts recursive equal_value
// nodes rather than elapsed time, so a full request walk at each dispatch is a
// deterministic regression even on a fast Redis host.
const dispatchCostProbeLua = `
local S=NS.tset
local read_line_raw
S.bind_log_helpers(256,function(_,raw)
  read_line_raw=raw
  return function() end
end)
redis.register_function('ns_tset_dispatch_cost_probe',function(keys,args)
  if #keys~=0 or #args~=4 then return S.json.encode(S.refuse('ARGS')) end
  local mode,loops=args[3],tonumber(args[4])
  if (mode~='builtin' and mode~='line') or not loops or loops<0 or loops>40 or loops%1~=0 then
    return S.json.encode(S.refuse('ARGS'))
  end
  local calls,log_delta=0,0
  local function trusted_reader(ctx,q)
    local before=ctx.budget.log_commands or 0
    for _=1,loops do
      local batch,err,short=read_line_raw(ctx,'7',ctx.query_index)
      if err then return nil,err end
      if short or not batch or #batch~=1 then return nil,S.refuse('DRIFT') end
      calls=calls+1
    end
    log_delta=(ctx.budget.log_commands or 0)-before
    return {kind='last',count=calls},nil
  end
  local before=NS.__dispatch_equal_visits()
  local wire=S.read(args[1],args[2],mode=='line' and trusted_reader or nil)
  return S.json.encode({reply=S.json.decode(wire),equal_visits=NS.__dispatch_equal_visits()-before,
    calls=calls,log_delta=log_delta})
end)
`

const (
	dispatchCostProbeName     = "ns_tset_dispatch_cost_probe"
	dispatchCostTrailingCells = 4096
	dispatchCostManyQueries   = 40
)

type dispatchCostResult struct {
	EqualVisits int `json:"equal_visits"`
	Calls       int `json:"calls"`
	LogDelta    int `json:"log_delta"`
	Reply       struct {
		Status string `json:"status"`
		Code   string `json:"code"`
		Detail struct {
			QueryIndex *int `json:"query_index"`
		} `json:"detail"`
		Answers []struct {
			Kind  string            `json:"kind"`
			Rows  []json.RawMessage `json:"rows"`
			Count int               `json:"count"`
		} `json:"answers"`
		Counters map[string]int `json:"counters"`
	} `json:"reply"`
}

func dispatchCostActivate(t *testing.T, fx *tsetFixture) {
	t.Helper()
	fx.mustInitialize(t)
	if fx.loaded {
		t.Fatal("dispatch cost probe must load during fixture initialization")
	}
	source := tsetTestSourceWithProbe(t, fn.TSetStandalone, dispatchCostProbeLua, dispatchCostProbeName)
	const equalAnchor = "  local function equal_value(a,b,depth)\n"
	const getterAnchor = "  local sealed_fields="
	if strings.Count(source, equalAnchor) != 1 || strings.Count(source, getterAnchor) != 1 {
		t.Fatal("tset equal_value instrumentation anchors changed")
	}
	source = strings.Replace(source, equalAnchor,
		"  local dispatch_equal_visits=0\n"+equalAnchor+
			"    dispatch_equal_visits=dispatch_equal_visits+1\n", 1)
	source = strings.Replace(source, getterAnchor,
		"  NS.__dispatch_equal_visits=function() return dispatch_equal_visits end\n"+getterAnchor, 1)
	if err := fx.Client.FunctionLoad(context.Background(), source).Err(); err != nil {
		t.Fatalf("load instrumented test-only tset profile: %v", err)
	}
	tsetRequireProbeLoaded(t, fx.Client, dispatchCostProbeName)
	fx.loaded = true
	fx.flushPendingRows(t)
	fx.active = true
}

func dispatchCostRaw(t *testing.T, space string, rows int, lines, trailing bool) string {
	t.Helper()
	queries := make([]map[string]any, 0, rows+2)
	if lines {
		queries = append(queries, map[string]any{"kind": "last"})
	} else {
		for range rows {
			queries = append(queries, map[string]any{"kind": "rows", "t": "work"})
		}
	}
	if trailing {
		cells := make([]string, dispatchCostTrailingCells)
		for i := range cells {
			cells[i] = "missing:ready"
		}
		queries = append(queries, map[string]any{"kind": "count", "t": "work", "cells": cells})
	}
	encoded, err := json.Marshal(map[string]any{"epoch": "0", "space": space,
		"mode": "atomic", "queries": queries})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func dispatchCostCall(t *testing.T, fx *tsetFixture, raw, mode string, loops int) dispatchCostResult {
	t.Helper()
	wire, err := fx.Client.FCall(context.Background(), dispatchCostProbeName, nil,
		Version, raw, mode, loops).Text()
	if err != nil {
		t.Fatalf("dispatch cost %s/%d: %v", mode, loops, err)
	}
	var got dispatchCostResult
	if err := json.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatalf("decode dispatch cost %s/%d: %v: %s", mode, loops, err, wire)
	}
	return got
}

func dispatchCostRequireNOROW(t *testing.T, got dispatchCostResult, index int) {
	t.Helper()
	if got.Reply.Status != "refused" || got.Reply.Code != "NOROW" ||
		got.Reply.Detail.QueryIndex == nil || *got.Reply.Detail.QueryIndex != index {
		t.Fatalf("trailing valid count did not reach missing row at query %d: %+v", index, got)
	}
}

func TestTrustedReadDispatchDoesNotWalkFullRequestPerQuery(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "ready")
	seedL1LineCapabilityStream(t, fx.Client, fixtureLogKey(fx.Space, "0"), "7-0", "ok")
	dispatchCostActivate(t, fx)

	// This trailing count is valid and large, but its first cell names a row
	// that is absent. NOROW at the last query proves validation and every
	// preceding cheap query ran; the 4096 cells do not trigger 4096 ZCARDs.
	shortRaw := dispatchCostRaw(t, fx.Space, 1, false, true)
	manyRaw := dispatchCostRaw(t, fx.Space, dispatchCostManyQueries, false, true)
	lineRaw := dispatchCostRaw(t, fx.Space, 0, true, true)
	for label, raw := range map[string]string{"short": shortRaw, "many": manyRaw, "line": lineRaw} {
		if len(raw) < 32*1024 || len(raw) >= 4*1024*1024 {
			t.Fatalf("%s request size %d does not exercise a large valid read", label, len(raw))
		}
	}
	const maxLegitimateVisitGrowth = dispatchCostTrailingCells / 2

	rowsOnly := dispatchCostCall(t, fx,
		dispatchCostRaw(t, fx.Space, dispatchCostManyQueries, false, false), "builtin", 0)
	if rowsOnly.Reply.Status != "read" || len(rowsOnly.Reply.Answers) != dispatchCostManyQueries ||
		rowsOnly.Reply.Counters["store_commands"] <= 0 {
		t.Fatalf("cheap built-in rows queries did not execute: %+v", rowsOnly)
	}
	for i, answer := range rowsOnly.Reply.Answers {
		if answer.Kind != "rows" || len(answer.Rows) != 0 {
			t.Fatalf("rows answer %d = %+v, want empty rows", i, answer)
		}
	}
	one := dispatchCostCall(t, fx, shortRaw, "builtin", 0)
	many := dispatchCostCall(t, fx, manyRaw, "builtin", 0)
	dispatchCostRequireNOROW(t, one, 1)
	dispatchCostRequireNOROW(t, many, dispatchCostManyQueries)
	if one.EqualVisits <= 0 || many.EqualVisits <= 0 {
		t.Fatalf("equal_value counter did not observe the baseline request check: one=%+v many=%+v", one, many)
	}
	// A single extra full traversal must visit at least the 4096 trailing
	// cell strings. Legitimate work for 39 additional small rows queries adds
	// only their own shallow nodes to the initial validation/check.
	if growth := many.EqualVisits - one.EqualVisits; growth >= maxLegitimateVisitGrowth {
		t.Errorf("%d extra built-in dispatches revisited large request: visits %d -> %d (growth %d, limit %d)",
			dispatchCostManyQueries-1, one.EqualVisits, many.EqualVisits, growth, maxLegitimateVisitGrowth)
	}

	lineOnly := dispatchCostCall(t, fx,
		dispatchCostRaw(t, fx.Space, 0, true, false), "line", dispatchCostManyQueries)
	if lineOnly.Reply.Status != "read" || len(lineOnly.Reply.Answers) != 1 ||
		lineOnly.Reply.Answers[0].Kind != "last" ||
		lineOnly.Reply.Answers[0].Count != dispatchCostManyQueries ||
		lineOnly.Calls != dispatchCostManyQueries || lineOnly.LogDelta != dispatchCostManyQueries ||
		lineOnly.Reply.Counters["log_commands"] != dispatchCostManyQueries {
		t.Fatalf("trusted log_reader did not execute/account for %d exact lines: %+v",
			dispatchCostManyQueries, lineOnly)
	}
	idleLine := dispatchCostCall(t, fx, lineRaw, "line", 0)
	busyLine := dispatchCostCall(t, fx, lineRaw, "line", dispatchCostManyQueries)
	dispatchCostRequireNOROW(t, idleLine, 1)
	dispatchCostRequireNOROW(t, busyLine, 1)
	if idleLine.EqualVisits <= 0 || busyLine.EqualVisits <= 0 {
		t.Fatalf("equal_value counter did not observe the baseline request check: idle=%+v busy=%+v", idleLine, busyLine)
	}
	if idleLine.Calls != 0 || idleLine.LogDelta != 0 ||
		busyLine.Calls != dispatchCostManyQueries || busyLine.LogDelta != dispatchCostManyQueries {
		t.Fatalf("trusted exact-line work was skipped: idle=%+v busy=%+v", idleLine, busyLine)
	}
	if growth := busyLine.EqualVisits - idleLine.EqualVisits; growth >= maxLegitimateVisitGrowth {
		t.Errorf("%d trusted exact-line reads revisited large request: visits %d -> %d (growth %d, limit %d)",
			dispatchCostManyQueries, idleLine.EqualVisits, busyLine.EqualVisits, growth, maxLegitimateVisitGrowth)
	}
	t.Logf("read dispatch cost: request bytes builtin %d/%d, line %d; equal_value visits builtin %d/%d, line %d/%d; exact-line calls and log commands %d/%d",
		len(shortRaw), len(manyRaw), len(lineRaw), one.EqualVisits, many.EqualVisits,
		idleLine.EqualVisits, busyLine.EqualVisits, busyLine.Calls, busyLine.LogDelta)
}
