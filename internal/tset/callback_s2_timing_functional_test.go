//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

const s2TimingProbeName = "ns_tset_s2_timing_probe"
const s2GCProbeName = "ns_tset_s2_gc"

// s2PaddedRow has length 138 (<= 256 byte cap('name') limit).
// When combined with ":ready" (144 chars), each cell JSON entry is 147 bytes,
// yielding an exact ~2.94 MiB request (2,940,113 bytes) across 20,000 cells.
const s2PaddedRow = "row_pad_0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"

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
redis.register_function{
  function_name='ns_tset_s2_gc',
  callback=function(keys,args)
    collectgarbage('collect')
    return 'collected'
  end,
  flags={'no-writes'}
}
`

type s2Reply struct {
	Status      string            `json:"status"`
	Code        string            `json:"code,omitempty"`
	Message     string            `json:"message,omitempty"`
	Detail      RefusalDetail     `json:"detail,omitempty"`
	Epoch       Decimal           `json:"epoch,omitempty"`
	ActiveEpoch Decimal           `json:"active_epoch,omitempty"`
	TimeMS      Decimal           `json:"time_ms,omitempty"`
	Answers     []ReadAnswer      `json:"answers,omitempty"`
	Complete    bool              `json:"complete,omitempty"`
	Counters    map[string]uint64 `json:"counters,omitempty"`
}

type s2TimingResult struct {
	Visits      int     `json:"visits"`
	Calls       int     `json:"calls"`
	RecordDelta int     `json:"record_delta"`
	FieldDelta  int     `json:"field_delta"`
	Reply       s2Reply `json:"reply"`
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
	tsetRequireProbeLoaded(tb, fx.Client, s2GCProbeName)
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
	fx.AddRow(tb, "work", s2PaddedRow, 1)
	fx.AddRow(tb, "other", "o", 0)
	s2TimingActivate(tb, fx, baselineReversal)
	store := newFixtureRedis(tb, fx.Client)

	step, err := store.Step(context.Background(), Step{
		Epoch: "0",
		Space: fx.Space,
		Entries: []Entry{
			{
				Kind:   "create",
				Table:  "work",
				To:     "w:ready",
				IDs:    []string{"card"},
				Scores: []string{"1"},
				Set:    map[string]string{"brief": "seed"},
			},
			{
				Kind:   "create",
				Table:  "work",
				To:     s2PaddedRow + ":ready",
				IDs:    []string{"card_pad"},
				Scores: []string{"1"},
				Set:    map[string]string{"brief": "seed_pad"},
			},
		},
	})
	if err != nil || step.Status != "ok" || step.Changed != 2 {
		tb.Fatalf("seed public card: reply=%+v err=%v", step, err)
	}

	ctx := context.Background()
	if err := fx.Client.ConfigSet(ctx, "slowlog-log-slower-than", "0").Err(); err != nil {
		tb.Fatalf("config set slowlog-log-slower-than 0: %v", err)
	}
	if err := fx.Client.ConfigSet(ctx, "slowlog-max-len", "1024").Err(); err != nil {
		tb.Fatalf("config set slowlog-max-len 1024: %v", err)
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

func s2Call(tb testing.TB, fx *s2BenchFixture, raw, mode string, loops int) (s2TimingResult, time.Duration, time.Duration) {
	tb.Helper()
	ctx := context.Background()
	_, isBench := tb.(*testing.B)
	if !isBench {
		if err := fx.Client.SlowLogReset(ctx).Err(); err != nil {
			tb.Fatalf("slowlog reset: %v", err)
		}
	}
	start := time.Now()
	wire, err := fx.Client.FCall(ctx, s2TimingProbeName, nil,
		Version, raw, mode, loops).Text()
	elapsed := time.Since(start)
	if err != nil {
		tb.Fatalf("s2 call %s/%d: %v", mode, loops, err)
	}
	var serverDur time.Duration
	if !isBench {
		entries, err := fx.Client.SlowLogGet(ctx, 1).Result()
		if err != nil {
			tb.Fatalf("slowlog get: %v", err)
		}
		if len(entries) == 0 {
			tb.Fatalf("slowlog empty after s2 call %s/%d", mode, loops)
		}
		serverDur = entries[0].Duration
		if len(entries[0].Args) < 2 || !strings.EqualFold(entries[0].Args[0], "fcall") || entries[0].Args[1] != s2TimingProbeName {
			tb.Fatalf("unexpected slowlog entry: %+v", entries[0])
		}
		if serverDur <= 0 {
			tb.Fatalf("non-positive server duration %v", serverDur)
		}
	}
	var res s2TimingResult
	if err := json.Unmarshal([]byte(wire), &res); err != nil {
		tb.Fatalf("decode s2 result: %v: %s", err, wire)
	}
	return res, elapsed, serverDur
}

func s2ForceFunctionGC(ctx context.Context, client *redis.Client) error {
	val, err := client.FCallRo(ctx, s2GCProbeName, nil).Result()
	if err != nil {
		return fmt.Errorf("force functions vm gc: %w", err)
	}
	if val != "collected" {
		return fmt.Errorf("unexpected gc result: %v", val)
	}
	return nil
}

func getRedisMemoryUsage(ctx context.Context, client *redis.Client) (usedBytes int64, luaBytes int64, err error) {
	info, err := client.Info(ctx, "memory").Result()
	if err != nil {
		return 0, 0, fmt.Errorf("info memory: %w", err)
	}
	var foundUsed, foundLua bool
	for _, line := range strings.Split(info, "\r\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "used_memory:") {
			if _, scanErr := fmt.Sscanf(line, "used_memory:%d", &usedBytes); scanErr == nil {
				foundUsed = true
			}
		} else if strings.HasPrefix(line, "used_memory_lua:") {
			if _, scanErr := fmt.Sscanf(line, "used_memory_lua:%d", &luaBytes); scanErr == nil {
				foundLua = true
			}
		}
	}
	if !foundUsed {
		return 0, 0, errors.New("used_memory metric not found in INFO memory")
	}
	if !foundLua {
		return 0, 0, errors.New("used_memory_lua metric not found in INFO memory")
	}
	return usedBytes, luaBytes, nil
}

func getRedisCPUUsage(ctx context.Context, client *redis.Client) (userSec float64, sysSec float64, err error) {
	info, err := client.Info(ctx, "cpu").Result()
	if err != nil {
		return 0, 0, fmt.Errorf("info cpu: %w", err)
	}
	var foundUser, foundSys bool
	for _, line := range strings.Split(info, "\r\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "used_cpu_user:") {
			if _, scanErr := fmt.Sscanf(line, "used_cpu_user:%f", &userSec); scanErr == nil {
				foundUser = true
			}
		} else if strings.HasPrefix(line, "used_cpu_sys:") {
			if _, scanErr := fmt.Sscanf(line, "used_cpu_sys:%f", &sysSec); scanErr == nil {
				foundSys = true
			}
		}
	}
	if !foundUser {
		return 0, 0, errors.New("used_cpu_user metric not found in INFO cpu")
	}
	if !foundSys {
		return 0, 0, errors.New("used_cpu_sys metric not found in INFO cpu")
	}
	return userSec, sysSec, nil
}

// TestS2LargeRequestTimingProfile benchmarks the S2 profile on real Redis 8.10.2
// measuring latency percentiles (P50, P90, P99) for both client wall time and
// server-side SLOWLOG execution duration, comparison visits, and assertions
// across batch sizes N = 100, 500, 1000, 2000 items.
// Every single iteration across all 50 samples is asserted for semantic validity.
func TestS2LargeRequestTimingProfile(t *testing.T) {
	fx := newS2TimingFixture(t, false)

	sizes := []int{100, 500, 1000, 2000}
	const iterations = 50

	t.Log("====================================================================================================================")
	t.Log("S2 LARGE-REQUEST TIMING PROFILE (PR #4816 read_cache_state Isolation on Redis 8.10.2)")
	t.Log("====================================================================================================================")
	t.Logf("%-8s | %-10s | %-10s | %-10s | %-10s | %-10s | %-10s | %-10s | %-8s | %-10s | %-10s",
		"Batch(N)", "Req Bytes", "Wall P50", "Wall P90", "Wall P99", "Serv P50", "Serv P90", "Serv P99", "Visits", "Vis/Item", "Scaling")
	t.Log("--------------------------------------------------------------------------------------------------------------------")

	var prevVisits float64
	var prevN float64

	for _, n := range sizes {
		raw := s2BuildRequest(fx.Space, n, true)
		wallDurations := make([]time.Duration, iterations)
		serverDurations := make([]time.Duration, iterations)
		var lastResult s2TimingResult

		for i := 0; i < iterations; i++ {
			res, wallDur, serverDur := s2Call(t, fx, raw, "cost", n)
			wallDurations[i] = wallDur
			serverDurations[i] = serverDur
			lastResult = res

			// Assert every single response of all 50 iterations:
			if res.Reply.Status != "read" {
				t.Fatalf("iteration %d (N=%d): expected status=read, got %q (code=%q)", i, n, res.Reply.Status, res.Reply.Code)
			}
			if res.Reply.Code != "" {
				t.Fatalf("iteration %d (N=%d): expected empty code on read, got %q", i, n, res.Reply.Code)
			}
			if len(res.Reply.Answers) != 2 {
				t.Fatalf("iteration %d (N=%d): expected 2 answers, got %d", i, n, len(res.Reply.Answers))
			}
			if res.Reply.Answers[0].Kind != "boundary" {
				t.Fatalf("iteration %d (N=%d): answer 0 kind=%q, want boundary", i, n, res.Reply.Answers[0].Kind)
			}
			if res.Reply.Answers[1].Kind != "count" || len(res.Reply.Answers[1].Counts) != n || res.Reply.Answers[1].Sum != uint64(n) {
				t.Fatalf("iteration %d (N=%d): answer 1 count mismatch: kind=%q counts_len=%d sum=%d, want kind=count counts_len=%d sum=%d",
					i, n, res.Reply.Answers[1].Kind, len(res.Reply.Answers[1].Counts), res.Reply.Answers[1].Sum, n, n)
			}
			for ci, cnt := range res.Reply.Answers[1].Counts {
				if cnt != 1 {
					t.Fatalf("iteration %d (N=%d): count[%d]=%d, want 1", i, n, ci, cnt)
				}
			}
			if res.Calls != n || res.RecordDelta != n || res.FieldDelta != n {
				t.Fatalf("iteration %d (N=%d): expected %d calls and deltas, got calls=%d record_delta=%d field_delta=%d",
					i, n, n, res.Calls, res.RecordDelta, res.FieldDelta)
			}
			if serverDur <= 0 {
				t.Fatalf("iteration %d (N=%d): invalid server duration: %v", i, n, serverDur)
			}
			if serverDur > wallDur+100*time.Millisecond {
				t.Fatalf("iteration %d (N=%d): server duration %v exceeded client wall time %v", i, n, serverDur, wallDur)
			}
		}

		sort.Slice(wallDurations, func(i, j int) bool { return wallDurations[i] < wallDurations[j] })
		sort.Slice(serverDurations, func(i, j int) bool { return serverDurations[i] < serverDurations[j] })
		p50Wall := wallDurations[iterations*50/100]
		p90Wall := wallDurations[iterations*90/100]
		p99Wall := wallDurations[iterations*99/100]
		p50Serv := serverDurations[iterations*50/100]
		p90Serv := serverDurations[iterations*90/100]
		p99Serv := serverDurations[iterations*99/100]

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

		t.Logf("%-8d | %-10d | %-10.3f | %-10.3f | %-10.3f | %-10.3f | %-10.3f | %-10.3f | %-8d | %-10.2f | %-10s",
			n, len(raw),
			float64(p50Wall.Microseconds())/1000.0, float64(p90Wall.Microseconds())/1000.0, float64(p99Wall.Microseconds())/1000.0,
			float64(p50Serv.Microseconds())/1000.0, float64(p90Serv.Microseconds())/1000.0, float64(p99Serv.Microseconds())/1000.0,
			visits, visitsPerItem, scaling)
	}
	t.Log("====================================================================================================================")
}

// TestS2BaselineComparisonFullWalkReversal empirically compares PR #4816 against
// the baseline reversal where every helper access performs a full request walk.
func TestS2BaselineComparisonFullWalkReversal(t *testing.T) {
	t.Log("====================================================================================================================")
	t.Log("S2 BASELINE COMPARISON: PR #4816 vs FULL-WALK REVERSAL (Quadratic vs Linear)")
	t.Log("====================================================================================================================")

	fxRepaired := newS2TimingFixture(t, false)
	fxBaseline := newS2TimingFixture(t, true)

	t.Logf("%-6s | %-12s | %-12s | %-12s | %-12s | %-12s | %-12s | %-10s",
		"N", "Rep Visits", "Base Visits", "Rep Wall(ms)", "Base Wall(ms)", "Rep Serv(ms)", "Base Serv(ms)", "Visit Ratio")
	t.Log("--------------------------------------------------------------------------------------------------------------------")

	// Compare up to N=500 because full-walk reversal with N=2000 executes millions of nodes
	for _, n := range []int{50, 100, 200, 500} {
		rawRepaired := s2BuildRequest(fxRepaired.Space, n, true)
		rawBaseline := s2BuildRequest(fxBaseline.Space, n, true)

		resRepaired, wallRepaired, servRepaired := s2Call(t, fxRepaired, rawRepaired, "cost", n)
		resBaseline, wallBaseline, servBaseline := s2Call(t, fxBaseline, rawBaseline, "cost", n)

		if resRepaired.Reply.Status != "read" {
			t.Fatalf("repaired N=%d: expected status=read, got %q", n, resRepaired.Reply.Status)
		}
		if resBaseline.Reply.Status != "read" {
			t.Fatalf("baseline N=%d: expected status=read, got %q", n, resBaseline.Reply.Status)
		}

		ratio := float64(resBaseline.Visits) / float64(resRepaired.Visits)

		t.Logf("%-6d | %-12d | %-12d | %-12.3f | %-12.3f | %-12.3f | %-12.3f | %-10.2fx",
			n, resRepaired.Visits, resBaseline.Visits,
			float64(wallRepaired.Microseconds())/1000.0,
			float64(wallBaseline.Microseconds())/1000.0,
			float64(servRepaired.Microseconds())/1000.0,
			float64(servBaseline.Microseconds())/1000.0,
			ratio)

		if resBaseline.Visits <= resRepaired.Visits {
			t.Fatalf("baseline did not exhibit increased visits at N=%d: %d vs %d",
				n, resBaseline.Visits, resRepaired.Visits)
		}
	}
	t.Log("====================================================================================================================")
}

// TestS2MaxBudgetStepOverhead measures CPU and Memory overhead of boundary checks under max budget,
// forcing Functions VM garbage collection before and after to verify zero retained Lua heap leak,
// and asserting whole-store image invariance via commitProbeImage.
func TestS2MaxBudgetStepOverhead(t *testing.T) {
	fx := newS2TimingFixture(t, false)
	ctx := context.Background()

	raw := s2BuildRequest(fx.Space, 2000, true)

	// Assert whole store image invariance before and after
	imageBefore := commitProbeImage(t, fx.Client)

	// Force Functions VM GC before baseline memory measurement
	if err := s2ForceFunctionGC(ctx, fx.Client); err != nil {
		t.Fatalf("pre-run GC: %v", err)
	}

	memBefore, luaBefore, err := getRedisMemoryUsage(ctx, fx.Client)
	if err != nil {
		t.Fatalf("get memory before: %v", err)
	}
	if memBefore <= 0 || luaBefore <= 0 {
		t.Fatalf("invalid initial memory metrics: used=%d lua=%d", memBefore, luaBefore)
	}

	cpuUserBefore, cpuSysBefore, err := getRedisCPUUsage(ctx, fx.Client)
	if err != nil {
		t.Fatalf("get cpu before: %v", err)
	}
	if cpuUserBefore < 0 || cpuSysBefore < 0 {
		t.Fatalf("invalid initial cpu metrics: user=%.4f sys=%.4f", cpuUserBefore, cpuSysBefore)
	}

	// Exercise max-budget: charge 9990 out of 10000 record budget
	const maxBudgetCharge = 9990
	res, wallDur, serverDur := s2Call(t, fx, raw, "max_budget", maxBudgetCharge)

	// Force Functions VM GC after run to verify zero retained heap leakage
	if err := s2ForceFunctionGC(ctx, fx.Client); err != nil {
		t.Fatalf("post-run GC: %v", err)
	}

	memAfter, luaAfter, err := getRedisMemoryUsage(ctx, fx.Client)
	if err != nil {
		t.Fatalf("get memory after: %v", err)
	}
	cpuUserAfter, cpuSysAfter, err := getRedisCPUUsage(ctx, fx.Client)
	if err != nil {
		t.Fatalf("get cpu after: %v", err)
	}

	t.Logf("Max-Budget Step Overhead (charge=%d, trailing N=2000):", maxBudgetCharge)
	t.Logf("  Wall Duration: %v, Server SLOWLOG: %v", wallDur, serverDur)
	t.Logf("  Visits: %d", res.Visits)
	t.Logf("  Record Delta: %d, Field Delta: %d, Calls: %d", res.RecordDelta, res.FieldDelta, res.Calls)
	t.Logf("  Redis Memory Delta: %d bytes (Lua Delta: %d bytes)", memAfter-memBefore, luaAfter-luaBefore)
	t.Logf("  Redis CPU Time: User=%.4fs, Sys=%.4fs", cpuUserAfter-cpuUserBefore, cpuSysAfter-cpuSysBefore)

	// Assertions
	if res.Reply.Status != "read" {
		t.Fatalf("expected status=read under max-budget, got %+v", res.Reply)
	}
	if res.Reply.Code != "" {
		t.Fatalf("expected empty code on successful read, got %q", res.Reply.Code)
	}
	if res.Calls != 10 || res.RecordDelta != 10000 || res.FieldDelta != 10000 {
		t.Fatalf("expected 10 calls, 10000 record delta, 10000 field delta, got calls=%d rec=%d fld=%d",
			res.Calls, res.RecordDelta, res.FieldDelta)
	}
	// Assert zero retained Lua heap delta post-GC
	if luaDelta := luaAfter - luaBefore; luaDelta != 0 {
		t.Fatalf("retained Lua heap delta after GC must be 0, got %d bytes (before=%d after=%d)",
			luaDelta, luaBefore, luaAfter)
	}

	imageAfter := commitProbeImage(t, fx.Client)
	if !reflect.DeepEqual(imageBefore, imageAfter) {
		t.Fatal("whole store image was modified during max-budget read execution")
	}
}

func s2BuildLargePayloadRequest(space string, targetBytes int) string {
	queries := []map[string]any{{"kind": "boundary"}}
	numCells := 19990
	cellName := s2PaddedRow + ":ready"
	cells := make([]string, numCells)
	for i := range cells {
		cells[i] = cellName
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
// cached reads, asserting reply.status == "read", zero reply refusal, valid count answers across
// 19,990 cells, matched SLOWLOG duration under 5s, flat comparison visits, and whole-store image
// equality before and after.
func TestS2LargePayload2_9MBTiming(t *testing.T) {
	fx := newS2TimingFixture(t, false)
	raw := s2BuildLargePayloadRequest(fx.Space, 2940000)

	// Whole-store image before any calls
	imageBefore := commitProbeImage(t, fx.Client)

	t.Log("========================================================================================================")
	t.Logf("S2 ~2.9 MiB REQUEST TIMING (Request Size = %d bytes, 19,990 cells)", len(raw))
	t.Log("========================================================================================================")
	t.Logf("%-6s | %-12s | %-15s | %-18s | %-10s | %-12s",
		"k", "Visits", "Wall Time (ms)", "Server SLOWLOG (ms)", "Calls", "Under 5s")
	t.Log("--------------------------------------------------------------------------------------------------------")

	var firstVisits int
	for _, k := range []int{0, 1, 4, 10, 40} {
		mode := "cost"
		if k == 0 {
			mode = "normal"
		}
		res, wallDur, serverDur := s2Call(t, fx, raw, mode, k)
		underTimeout := wallDur < 5*time.Second

		if k == 0 {
			firstVisits = res.Visits
		}

		t.Logf("%-6d | %-12d | %-15.3f | %-18.3f | %-10d | %-12v",
			k, res.Visits,
			float64(wallDur.Microseconds())/1000.0,
			float64(serverDur.Microseconds())/1000.0,
			res.Calls, underTimeout)

		// Assertions per k:
		if !underTimeout {
			t.Fatalf("k=%d exceeded 5s client timeout: wall=%v server=%v", k, wallDur, serverDur)
		}
		if res.Reply.Status != "read" {
			act, lim := int64(-1), int64(-1)
			if res.Reply.Detail.Actual != nil {
				act = *res.Reply.Detail.Actual
			}
			if res.Reply.Detail.Limit != nil {
				lim = *res.Reply.Detail.Limit
			}
			t.Fatalf("k=%d: expected reply.status=read, got %q (code=%q, message=%q, budget=%s actual=%d limit=%d)",
				k, res.Reply.Status, res.Reply.Code, res.Reply.Message, res.Reply.Detail.Budget, act, lim)
		}
		if res.Reply.Code != "" {
			t.Fatalf("k=%d: expected empty reply.code, got %q", k, res.Reply.Code)
		}
		if len(res.Reply.Answers) != 2 {
			t.Fatalf("k=%d: expected 2 answers, got %d", k, len(res.Reply.Answers))
		}
		if res.Reply.Answers[0].Kind != "boundary" {
			t.Fatalf("k=%d: answer 0 kind=%q, want boundary", k, res.Reply.Answers[0].Kind)
		}
		if res.Reply.Answers[1].Kind != "count" {
			t.Fatalf("k=%d: answer 1 kind=%q, want count", k, res.Reply.Answers[1].Kind)
		}
		if len(res.Reply.Answers[1].Counts) != 19990 || res.Reply.Answers[1].Sum != 19990 {
			t.Fatalf("k=%d: count mismatch: counts_len=%d sum=%d, want 19990/19990",
				k, len(res.Reply.Answers[1].Counts), res.Reply.Answers[1].Sum)
		}
		if res.Calls != k {
			t.Fatalf("k=%d: expected %d actual calls, got %d", k, k, res.Calls)
		}
		if res.RecordDelta != k || res.FieldDelta != k {
			t.Fatalf("k=%d: expected delta %d, got record_delta=%d field_delta=%d",
				k, k, res.RecordDelta, res.FieldDelta)
		}
		if serverDur <= 0 {
			t.Fatalf("k=%d: non-positive server duration %v", k, serverDur)
		}

		// Confirm zero visit growth across all k
		if res.Visits != firstVisits {
			t.Fatalf("visits grew with k: k=0 visits=%d, k=%d visits=%d", firstVisits, k, res.Visits)
		}
	}
	t.Log("========================================================================================================")

	// Whole-store image invariance after all calls
	imageAfter := commitProbeImage(t, fx.Client)
	if !reflect.DeepEqual(imageBefore, imageAfter) {
		t.Fatal("whole store image was modified during ~2.9 MiB large-payload tests")
	}
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
				_, _, _ = s2Call(b, fx, raw, "cost", n)
			}
		})
	}
}
