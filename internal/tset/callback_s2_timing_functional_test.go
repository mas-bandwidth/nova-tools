//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

const s2TimingProbeName = "ns_tset_s2_timing_probe"

const s2TimingProbeLua = `
local limit_trap_count=0
redis.register_function('ns_tset_s2_timing_probe',function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=4 then return S.json.encode(S.refuse('ARGS')) end
  local mode,loops=args[3],tonumber(args[4])
  if not loops or loops<0 or loops%1~=0 then return S.json.encode(S.refuse('ARGS')) end
  local escaped_attempts,escaped_ok,calls,record_delta,field_delta=0,0,0,0,0
  local extension={kinds={'boundary'}}
  extension.validate=function(q,index)
    if q.kind~='boundary' then return nil,S.refuse('REQUEST',{query_index=index}) end
    for k in pairs(q) do if k~='kind' then return nil,S.refuse('REQUEST',{query_index=index}) end end
    return true,nil
  end
  extension.read=function(ctx,q,index)
    if mode=='cost' then
      local before_record,before_field=ctx.budget.record,ctx.budget.field
      for _=1,loops do
        local _,err=S.read_record(ctx,'work','card',{'brief'},index)
        if err then return nil,err end
        calls=calls+1
      end
      record_delta=ctx.budget.record-before_record
      field_delta=ctx.budget.field-before_field
    elseif mode=='max_budget' then
      local before_record,before_field=ctx.budget.record,ctx.budget.field
      local _,err=S.charge(ctx,'record',loops)
      if err then return nil,err end
      local _,ferr=S.charge(ctx,'field',loops)
      if ferr then return nil,ferr end
      for _=1,10 do
        local _,r_err=S.read_record(ctx,'work','card',{'brief'},index)
        if r_err then return nil,r_err end
        calls=calls+1
      end
      record_delta=ctx.budget.record-before_record
      field_delta=ctx.budget.field-before_field
    elseif mode=='normal' then
      -- no inner helper loops
    else
      return nil,S.refuse('REQUEST',{query_index=index})
    end
    return {kind='boundary',values=S.array()},nil
  end
  local before=NS.__callback_equal_visits()
  local wire=S.read(args[1],args[2],nil,extension)
  return S.json.encode({reply=S.json.decode(wire),visits=NS.__callback_equal_visits()-before,
    calls=calls,record_delta=record_delta,field_delta=field_delta})
end)
`

type s2TimingResult struct {
	Visits      int `json:"visits"`
	Calls       int `json:"calls"`
	RecordDelta int `json:"record_delta"`
	FieldDelta  int `json:"field_delta"`
	Reply       struct {
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
	} `json:"reply"`
}

type s2BenchFixture struct {
	Client *redis.Client
	Store  *RedisStore
	Space  string
}

func s2TimingActivate(tb testing.TB, fx *tsetFixture, baselineReversal bool) {
	tb.Helper()
	fx.mustInitialize(tb)
	if fx.loaded {
		tb.Fatal("s2 timing probe must load during fixture initialization")
	}
	source := tsetTestSourceWithProbe(tb, fn.TSetStandalone, s2TimingProbeLua, s2TimingProbeName)
	const equalAnchor = "  local function equal_value(a,b,depth)\n"
	const getterAnchor = "  local sealed_fields="
	if strings.Count(source, equalAnchor) != 1 || strings.Count(source, getterAnchor) != 1 {
		tb.Fatal("callback boundary equal_value anchors changed")
	}
	source = strings.Replace(source, equalAnchor,
		"  local callback_equal_visits=0\n"+equalAnchor+
			"    callback_equal_visits=callback_equal_visits+1\n", 1)
	source = strings.Replace(source, getterAnchor,
		"  NS.__callback_equal_visits=function() return callback_equal_visits end\n"+getterAnchor, 1)

	if baselineReversal {
		const target = "local function read_unchanged(ctx,saved)\n    return read_identity_unchanged(ctx,saved)\n  end"
		const reversal = "local function read_unchanged(ctx,saved)\n    if callback_depth>0 then return original_unchanged(ctx) end\n    return read_identity_unchanged(ctx,saved)\n  end"
		if strings.Count(source, target) != 1 {
			tb.Fatal("could not find read_unchanged target for baseline reversal")
		}
		source = strings.Replace(source, target, reversal, 1)
	}

	if err := fx.Client.FunctionLoad(context.Background(), source).Err(); err != nil {
		tb.Fatalf("load s2 timing test library: %v", err)
	}
	tsetRequireProbeLoaded(tb, fx.Client, s2TimingProbeName)
	fx.loaded = true
	fx.flushPendingRows(tb)
	fx.active = true
}

func newS2TimingFixture(tb testing.TB, baselineReversal bool) *s2BenchFixture {
	tb.Helper()
	fx := newTSetFixture(tb)
	fx.Define(tb, "work", "ready")
	fx.Define(tb, "other", "ready")
	fx.AddRow(tb, "work", "w", 0)
	fx.AddRow(tb, "other", "o", 0)
	s2TimingActivate(tb, fx, baselineReversal)
	store := newFixtureRedis(tb, fx.Client)

	step, err := store.Step(context.Background(), Step{
		Epoch: "0",
		Space: fx.Space,
		Entries: []Entry{{
			Kind:   "create",
			Table:  "work",
			To:     "w:ready",
			IDs:    []string{"card"},
			Scores: []string{"1"},
			Set:    map[string]string{"brief": "seed"},
		}},
	})
	if err != nil || step.Status != "ok" || step.Changed != 1 {
		tb.Fatalf("seed public card: reply=%+v err=%v", step, err)
	}

	return &s2BenchFixture{Client: fx.Client, Store: store, Space: fx.Space}
}

func s2BuildRequest(space string, n int, validCells bool) string {
	queries := []map[string]any{{"kind": "boundary"}}
	cells := make([]string, n)
	val := "missing:ready"
	if validCells {
		val = "w:ready"
	}
	for i := range cells {
		cells[i] = val
	}
	queries = append(queries, map[string]any{"kind": "count", "t": "work", "cells": cells})
	encoded, err := json.Marshal(map[string]any{
		"epoch":   "0",
		"space":   space,
		"mode":    "atomic",
		"queries": queries,
	})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func s2Call(tb testing.TB, fx *s2BenchFixture, raw, mode string, loops int) (s2TimingResult, time.Duration) {
	tb.Helper()
	start := time.Now()
	wire, err := fx.Client.FCall(context.Background(), s2TimingProbeName, nil,
		Version, raw, mode, loops).Text()
	elapsed := time.Since(start)
	if err != nil {
		tb.Fatalf("s2 call %s/%d: %v", mode, loops, err)
	}
	var res s2TimingResult
	if err := json.Unmarshal([]byte(wire), &res); err != nil {
		tb.Fatalf("decode s2 result: %v: %s", err, wire)
	}
	return res, elapsed
}

func getRedisMemoryUsage(client *redis.Client) (usedBytes int64, luaBytes int64) {
	info, err := client.Info(context.Background(), "memory").Result()
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, "used_memory:") {
			fmt.Sscanf(line, "used_memory:%d", &usedBytes)
		} else if strings.HasPrefix(line, "used_memory_lua:") {
			fmt.Sscanf(line, "used_memory_lua:%d", &luaBytes)
		}
	}
	return
}

func getRedisCPUUsage(client *redis.Client) (userSec float64, sysSec float64) {
	info, err := client.Info(context.Background(), "cpu").Result()
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, "used_cpu_user:") {
			fmt.Sscanf(line, "used_cpu_user:%f", &userSec)
		} else if strings.HasPrefix(line, "used_cpu_sys:") {
			fmt.Sscanf(line, "used_cpu_sys:%f", &sysSec)
		}
	}
	return
}

// TestS2LargeRequestTimingProfile benchmarks the S2 profile on real Redis 8.10.2
// measuring latency percentiles (P50, P90, P99), comparison visits, and allocations
// across batch sizes N = 100, 500, 1000, 2000 items.
func TestS2LargeRequestTimingProfile(t *testing.T) {
	fx := newS2TimingFixture(t, false)

	sizes := []int{100, 500, 1000, 2000}
	const iterations = 50

	t.Log("==========================================================================================")
	t.Log("S2 LARGE-REQUEST TIMING PROFILE (PR #4816 read_cache_state Isolation on Redis 8.10.2)")
	t.Log("==========================================================================================")
	t.Logf("%-8s | %-10s | %-10s | %-10s | %-10s | %-10s | %-12s | %-10s",
		"Batch(N)", "Req Bytes", "P50 (ms)", "P90 (ms)", "P99 (ms)", "Visits", "Visits/Item", "Scaling")
	t.Log("------------------------------------------------------------------------------------------")

	var prevVisits float64
	var prevN float64

	for _, n := range sizes {
		raw := s2BuildRequest(fx.Space, n, true)
		durations := make([]time.Duration, iterations)
		var lastResult s2TimingResult

		for i := 0; i < iterations; i++ {
			res, dur := s2Call(t, fx, raw, "cost", n)
			durations[i] = dur
			lastResult = res
		}

		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		p50 := durations[iterations*50/100]
		p90 := durations[iterations*90/100]
		p99 := durations[iterations*99/100]

		visits := lastResult.Visits
		visitsPerItem := float64(visits) / float64(n)

		scaling := "1.00x (base)"
		if prevN > 0 {
			growth := float64(visits) / prevVisits
			expectedLinear := float64(n) / prevN
			scaling = fmt.Sprintf("%.2fx (lin: %.2fx)", growth, expectedLinear)
		}
		prevVisits = float64(visits)
		prevN = float64(n)

		t.Logf("%-8d | %-10d | %-10.3f | %-10.3f | %-10.3f | %-10d | %-12.2f | %-10s",
			n, len(raw), float64(p50.Microseconds())/1000.0, float64(p90.Microseconds())/1000.0,
			float64(p99.Microseconds())/1000.0, visits, visitsPerItem, scaling)

		if lastResult.Calls != n || lastResult.RecordDelta != n || lastResult.FieldDelta != n {
			t.Fatalf("expected %d calls and delta, got %+v", n, lastResult)
		}
	}
	t.Log("==========================================================================================")
}

// TestS2BaselineComparisonFullWalkReversal empirically compares PR #4816 against
// the baseline reversal where every helper access performs a full request walk.
func TestS2BaselineComparisonFullWalkReversal(t *testing.T) {
	t.Log("==========================================================================================")
	t.Log("S2 BASELINE COMPARISON: PR #4816 vs FULL-WALK REVERSAL (Quadratic vs Linear)")
	t.Log("==========================================================================================")

	fxRepaired := newS2TimingFixture(t, false)
	fxBaseline := newS2TimingFixture(t, true)

	t.Logf("%-6s | %-14s | %-14s | %-14s | %-14s | %-10s",
		"N", "Repaired Visits", "Baseline Visits", "Repaired (ms)", "Baseline (ms)", "Visit Ratio")
	t.Log("------------------------------------------------------------------------------------------")

	// Compare up to N=500 because full-walk reversal with N=2000 executes millions of nodes
	for _, n := range []int{50, 100, 200, 500} {
		rawRepaired := s2BuildRequest(fxRepaired.Space, n, true)
		rawBaseline := s2BuildRequest(fxBaseline.Space, n, true)

		resRepaired, durRepaired := s2Call(t, fxRepaired, rawRepaired, "cost", n)
		resBaseline, durBaseline := s2Call(t, fxBaseline, rawBaseline, "cost", n)

		ratio := float64(resBaseline.Visits) / float64(resRepaired.Visits)

		t.Logf("%-6d | %-14d | %-14d | %-14.3f | %-14.3f | %-10.2fx",
			n, resRepaired.Visits, resBaseline.Visits,
			float64(durRepaired.Microseconds())/1000.0,
			float64(durBaseline.Microseconds())/1000.0, ratio)

		if resBaseline.Visits <= resRepaired.Visits {
			t.Fatalf("baseline did not exhibit increased visits at N=%d: %d vs %d",
				n, resBaseline.Visits, resRepaired.Visits)
		}
	}
	t.Log("==========================================================================================")
}

// TestS2MaxBudgetStepOverhead measures CPU and Memory overhead of boundary checks under max budget.
func TestS2MaxBudgetStepOverhead(t *testing.T) {
	fx := newS2TimingFixture(t, false)

	raw := s2BuildRequest(fx.Space, 2000, true)

	memBefore, luaBefore := getRedisMemoryUsage(fx.Client)
	cpuUserBefore, cpuSysBefore := getRedisCPUUsage(fx.Client)

	// Exercise max-budget: charge 9990 out of 10000 record budget
	const maxBudgetCharge = 9990
	res, dur := s2Call(t, fx, raw, "max_budget", maxBudgetCharge)

	memAfter, luaAfter := getRedisMemoryUsage(fx.Client)
	cpuUserAfter, cpuSysAfter := getRedisCPUUsage(fx.Client)

	t.Logf("Max-Budget Step Overhead (charge=%d, trailing N=2000):", maxBudgetCharge)
	t.Logf("  Duration: %v", dur)
	t.Logf("  Visits: %d", res.Visits)
	t.Logf("  Record Delta: %d, Field Delta: %d, Calls: %d", res.RecordDelta, res.FieldDelta, res.Calls)
	t.Logf("  Redis Memory Delta: %d bytes (Lua Delta: %d bytes)", memAfter-memBefore, luaAfter-luaBefore)
	t.Logf("  Redis CPU Time: User=%.4fs, Sys=%.4fs", cpuUserAfter-cpuUserBefore, cpuSysAfter-cpuSysBefore)

	if res.Reply.Status != "read" {
		t.Fatalf("expected status=read under max-budget, got %+v", res.Reply)
	}
}

func s2BuildLargePayloadRequest(space string, targetBytes int) string {
	queries := []map[string]any{{"kind": "boundary"}}
	numCells := 20000
	paddingLen := (targetBytes / numCells) - 14
	if paddingLen < 10 {
		paddingLen = 10
	} else if paddingLen > 150 {
		paddingLen = 150
	}
	padding := strings.Repeat("x", paddingLen)
	cells := make([]string, numCells)
	for i := range cells {
		cells[i] = fmt.Sprintf("c%05d_%s:ready", i, padding)
	}
	queries = append(queries, map[string]any{"kind": "count", "t": "work", "cells": cells})
	encoded, err := json.Marshal(map[string]any{
		"epoch":   "0",
		"space":   space,
		"mode":    "atomic",
		"queries": queries,
	})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// TestS2LargePayload2_9MBTiming measures the ~2.9 MiB request payload across k = 0, 1, 4, 10, 40
// cached reads, validating that all requests complete well under the 5-second client timeout
// and that visits remain flat regardless of k (addressing Rowan's finding and Stella's note).
func TestS2LargePayload2_9MBTiming(t *testing.T) {
	fx := newS2TimingFixture(t, false)
	raw := s2BuildLargePayloadRequest(fx.Space, 2900000)

	t.Log("==========================================================================================")
	t.Logf("S2 ~2.9 MiB REQUEST TIMING (Request Size = %d bytes, 20,000 cells)", len(raw))
	t.Log("==========================================================================================")
	t.Logf("%-6s | %-12s | %-14s | %-15s", "k", "Visits", "Duration (ms)", "Under 5s Timeout")
	t.Log("------------------------------------------------------------------------------------------")

	var firstVisits int
	for _, k := range []int{0, 1, 4, 10, 40} {
		mode := "cost"
		if k == 0 {
			mode = "normal"
		}
		res, dur := s2Call(t, fx, raw, mode, k)
		underTimeout := dur < 5*time.Second

		if k == 0 {
			firstVisits = res.Visits
		}

		t.Logf("%-6d | %-12d | %-14.3f | %-15v",
			k, res.Visits, float64(dur.Microseconds())/1000.0, underTimeout)

		if !underTimeout {
			t.Fatalf("k=%d exceeded 5s client timeout: %v", k, dur)
		}
		// Confirm zero visit growth across all k
		if res.Visits != firstVisits {
			t.Fatalf("visits grew with k: k=0 visits=%d, k=%d visits=%d", firstVisits, k, res.Visits)
		}
	}
	t.Log("==========================================================================================")
}

// BenchmarkS2LargeRequestTiming provides the benchmark runnable via:
// go test -tags functional -bench=BenchmarkS2
func BenchmarkS2LargeRequestTiming(b *testing.B) {
	fx := newS2TimingFixture(b, false)

	for _, n := range []int{100, 500, 1000, 2000} {
		raw := s2BuildRequest(fx.Space, n, true)
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = s2Call(b, fx, raw, "cost", n)
			}
		})
	}
}

