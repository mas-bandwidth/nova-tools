//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func newRowsetFunctionalHarness(t *testing.T, actual []RowRank) *namedStateHarness {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c", "d")
	if len(actual) != 0 {
		seed := make([]redis.Z, len(actual))
		for i, row := range actual {
			rank, err := strconv.ParseInt(string(row.Rank), 10, 64)
			if err != nil {
				t.Fatalf("seed rank %q: %v", row.Rank, err)
			}
			seed[i] = redis.Z{Score: float64(rank), Member: row.Row}
		}
		if err := fx.Client.ZAdd(context.Background(), fixtureRowsKey(fx.Space, "work", "0"), seed...).Err(); err != nil {
			t.Fatalf("seed Redis rowset: %v", err)
		}
	}
	mem := newRowsetMem(t, fx.Space, actual)
	fx.Activate(t)
	h := &namedStateHarness{fx: fx, mem: mem}
	h.snapshot(t)
	return h
}

// Use FCALL directly for refusals. NewRedis.Step performs its own validation,
// which would otherwise let a client-side LIMIT masquerade as a Lua refusal.
func rowsetFunctionalRefuse(t *testing.T, h *namedStateHarness, step Step, code string) RefusalDetail {
	t.Helper()
	before := h.snapshot(t)
	image := commitProbeImage(t, h.fx.Client)
	_, memErr := h.mem.Step(context.Background(), step)
	memRef := requireRefusal(t, memErr, code)
	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal rowset request: %v", err)
	}
	wire, err := h.fx.Step(string(raw))
	if err != nil {
		t.Fatalf("Lua rowset FCALL: %v", err)
	}
	encoded, ok := wire.(string)
	if !ok {
		t.Fatalf("Lua rowset reply type %T, want JSON string", wire)
	}
	var luaRef Refusal
	if err := json.Unmarshal([]byte(encoded), &luaRef); err != nil {
		t.Fatalf("decode Lua rowset refusal %q: %v", encoded, err)
	}
	requireRefusal(t, &luaRef, code)
	if code == "ROWSET" && len(memRef.Detail.Rows) == 0 {
		var wireShape struct {
			Detail struct {
				Rows json.RawMessage `json:"rows"`
			} `json:"detail"`
		}
		if err := json.Unmarshal([]byte(encoded), &wireShape); err != nil || string(wireShape.Detail.Rows) != "[]" {
			t.Fatalf("unnamed ROWSET detail must encode rows:[]: %s (err=%v)", encoded, err)
		}
	}
	if !reflect.DeepEqual(comparableDetail(memRef.Detail), comparableDetail(luaRef.Detail)) {
		t.Fatalf("%s detail differs: Mem=%+v Lua=%+v", code, memRef.Detail, luaRef.Detail)
	}
	if after := h.snapshot(t); !reflect.DeepEqual(before, after) {
		t.Fatalf("%s changed semantic state: %s", code, compareJSON("state", before, after))
	}
	if after := commitProbeImage(t, h.fx.Client); !reflect.DeepEqual(image, after) {
		t.Fatalf("%s changed the Redis whole-key image", code)
	}
	return memRef.Detail
}

func TestRefuseROWSET(t *testing.T) {
	t.Parallel()
	base := rowsetRanks(2)
	for _, tc := range []struct {
		name     string
		actual   []RowRank
		expected []RowRank
		wantRow  string
	}{
		{name: "extra stored row", actual: base, expected: base[:1]},
		{name: "missing stored row", actual: base[:1], expected: base},
		{name: "equal-cardinality replacement", actual: base,
			expected: []RowRank{base[0], {Row: "replacement", Rank: "1"}}, wantRow: "replacement"},
		{name: "changed rank", actual: base,
			expected: []RowRank{{Row: base[0].Row, Rank: "7"}, base[1]}, wantRow: base[0].Row},
		{name: "empty expectation races row addition", actual: base[:1], expected: []RowRank{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRowsetFunctionalHarness(t, tc.actual)
			detail := rowsetFunctionalRefuse(t, h, rowsetAdvance(h.fx.Space, tc.expected, nil), "ROWSET")
			if detail.EntryIndex == nil || *detail.EntryIndex != 0 ||
				detail.Table != "work" || detail.ActiveEpoch != "0" {
				t.Fatalf("ROWSET detail=%+v, want entry 0, work, active epoch 0", detail)
			}
			if tc.wantRow == "" {
				if len(detail.Rows) != 0 {
					t.Fatalf("cardinality ROWSET detail rows=%v, want empty", detail.Rows)
				}
			} else if !reflect.DeepEqual(detail.Rows, []string{tc.wantRow}) {
				t.Fatalf("named ROWSET detail rows=%v, want [%s]", detail.Rows, tc.wantRow)
			}
		})
	}
}

func TestRowsetGuardsRequestEpochAcrossAdvance(t *testing.T) {
	t.Parallel()
	t.Run("reordered exact old rows and successor restoration", func(t *testing.T) {
		old := rowsetRanks(2)
		h := newRowsetFunctionalHarness(t, old)
		beforeOld := stateWork(t, h.snapshot(t), "0").Rows
		step := rowsetAdvance(h.fx.Space, []RowRank{old[1], old[0]}, []string{"restored"})
		reply := h.apply(t, step)
		if reply.EpochBefore != "0" || reply.EpochAfter != "1" || reply.Changed != 0 ||
			reply.Guarded != 0 || !reflect.DeepEqual(reply.ChangedPerEntry, []int{0, 0, 0}) {
			t.Fatalf("exact old-epoch guard reply=%+v", reply)
		}
		snapshot := h.snapshot(t)
		if !reflect.DeepEqual(stateWork(t, snapshot, "0").Rows, beforeOld) ||
			!reflect.DeepEqual(stateWork(t, snapshot, "1").Rows, map[string]Decimal{"restored": "0"}) {
			t.Fatalf("old/new row topology after guarded advance: %+v", snapshot)
		}
	})
	t.Run("empty old set guards empty successor", func(t *testing.T) {
		h := newRowsetFunctionalHarness(t, nil)
		reply := h.apply(t, rowsetAdvance(h.fx.Space, []RowRank{}, nil))
		if reply.EpochAfter != "1" || !reflect.DeepEqual(reply.ChangedPerEntry, []int{0, 0}) ||
			len(stateWork(t, h.snapshot(t), "1").Rows) != 0 {
			t.Fatalf("empty rowset advance reply=%+v", reply)
		}
	})
	t.Run("successor restoration cannot satisfy old epoch guard", func(t *testing.T) {
		h := newRowsetFunctionalHarness(t, rowsetRanks(1))
		wrong := []RowRank{{Row: "future", Rank: "0"}}
		detail := rowsetFunctionalRefuse(t, h, rowsetAdvance(h.fx.Space, wrong, []string{"future"}), "ROWSET")
		if !reflect.DeepEqual(detail.Rows, []string{"future"}) || detail.ActiveEpoch != "0" {
			t.Fatalf("wrong epoch rowset detail=%+v", detail)
		}
	})
	t.Run("receipt replay bypasses changed dynamic expectation", func(t *testing.T) {
		h := newRowsetFunctionalHarness(t, rowsetRanks(1))
		op, intent := "advance-rowset", "stable intent"
		original := rowsetAdvance(h.fx.Space, rowsetRanks(1), []string{"r0000"})
		original.Op, original.Intent, original.Result = &op, &intent, "recorded"
		fresh := h.apply(t, original)
		if fresh.Replay || fresh.Result != "recorded" || fresh.EpochAfter != "1" {
			t.Fatalf("fresh named rowset advance=%+v", fresh)
		}
		changed := rowsetAdvance(h.fx.Space, []RowRank{{Row: "future", Rank: "0"}}, []string{"future"})
		changed.Op, changed.Intent, changed.Result = &op, &intent, "replanned"
		replay := h.unchanged(t, changed)
		if !replay.Replay || replay.Result != "recorded" || replay.EpochBefore != "0" ||
			replay.EpochAfter != "1" || replay.Changed != fresh.Changed {
			t.Fatalf("changed rowset failed original receipt replay: %+v", replay)
		}
	})
	for _, count := range []int{604, 1024} {
		t.Run("pair union "+strconv.Itoa(count), func(t *testing.T) {
			rows := rowsetRanks(count)
			h := newRowsetFunctionalHarness(t, rows)
			reply := h.apply(t, rowsetAdvance(h.fx.Space, rows, rowsetNames(rows)))
			if reply.EpochAfter != "1" || reply.Changed != 0 || reply.Guarded != 0 ||
				!reflect.DeepEqual(reply.ChangedPerEntry, []int{0, 0, 0}) ||
				len(stateWork(t, h.snapshot(t), "1").Rows) != count {
				t.Fatalf("%d distinct guarded/restored pairs reply=%+v", count, reply)
			}
		})
	}
	t.Run("1025 distinct pairs refuse before any write", func(t *testing.T) {
		rows := rowsetRanks(1024)
		h := newRowsetFunctionalHarness(t, rows)
		step := rowsetAdvance(h.fx.Space, rows, rowsetNames(rows))
		step.Entries = append(step.Entries, Entry{Kind: "rows", Table: "work", Add: []string{"extra"}})
		rowsetFunctionalRefuse(t, h, step, "LIMIT")
	})
	t.Run("writer uses bounded point row reads", func(t *testing.T) {
		fx := observationFixture(t, 4, 0, false, "c")
		step := rowsetAdvance(fx.Space, rowsetRanks(4), nil)
		// An advance must retain its named replay identity in the raw probe
		// request. observationCall builds an anonymous entries-only step.
		raw, err := EncodeStep(step)
		if err != nil {
			t.Fatalf("encode named rowset observation: %v", err)
		}
		value, err := fx.Client.FCall(context.Background(), "ns_tset_observation_probe", []string{}, Version, string(raw)).Result()
		if err != nil {
			t.Fatalf("named rowset observation FCALL: %v", err)
		}
		encoded, ok := value.(string)
		if !ok {
			t.Fatalf("named rowset observation reply type %T", value)
		}
		var trace observationTrace
		if err := json.Unmarshal([]byte(encoded), &trace); err != nil {
			t.Fatalf("decode named rowset observation %q: %v", encoded, err)
		}
		if trace.Status != "trace" {
			t.Fatalf("named rowset observation status=%q code=%q: %s", trace.Status, trace.Code, encoded)
		}
		rowsKey := fixtureRowsKey(fx.Space, "work", "0")
		zcard, zmscore := false, false
		for _, argv := range trace.Trace {
			if len(argv) == 0 {
				t.Fatal("empty planner read argv")
			}
			command := strings.ToUpper(argv[0])
			if command == "ZRANGE" || command == "ZSCAN" || command == "SCAN" || command == "KEYS" {
				t.Fatalf("rowset planner used broad enumeration: %v", argv)
			}
			if len(argv) > 1 && argv[1] == rowsKey {
				zcard = zcard || command == "ZCARD"
				zmscore = zmscore || command == "ZMSCORE"
			}
		}
		if !zcard || !zmscore {
			t.Fatalf("rowset missed bounded ZCARD/ZMSCORE reads on %q: %v", rowsKey, trace.Trace)
		}
	})
}

// This test-only callback simulates an enclosing preplanner changing the
// request after S.open has pinned the rowset prefix and original advance.
// It stops at S.plan; a rejected plan must not perform a new store read.
const rowsetPrefixProbeLua = `
redis.register_function('ns_tset_rowset_prefix_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local ctx,err=S.open(args[1],args[2])
  if err then return S.json.encode(err) end
  if ctx.replay then return S.json.encode(S.refuse('REQUEST')) end
  local mode=args[3]
  local entries=ctx.request.entries
  if mode=='rank' then
    entries[1].rows[1].rank='7'
  elseif mode=='remove' then
    table.remove(entries,1)
  elseif mode=='reorder' then
    entries[1].rows[1],entries[1].rows[2]=entries[1].rows[2],entries[1].rows[1]
  elseif mode=='insert_guard' then
    table.insert(entries,2,{kind='guard',t='work',from='r0000:c',ids={'missing'}})
  elseif mode=='shift_advance' then
    table.insert(entries,2,{kind='rowset',t='other',rows=S.array()})
  else
    return S.json.encode(S.refuse('REQUEST'))
  end
  local opened=ctx.budget.store_commands
  local reads=0
  local original=S.readcmd
  S.readcmd=function(...)
    reads=reads+1
    return original(...)
  end
  local ok,plan,problem=pcall(S.plan,ctx)
  S.readcmd=original
  if not ok then return S.json.encode({status='error',message=tostring(plan)}) end
  if plan then return S.json.encode({status='planned',reads=reads}) end
  return S.json.encode({status=problem.status,code=problem.code,
    reads=reads,charged=ctx.budget.store_commands-opened})
end)
`

func TestRowsetPreplanPrefixImmutable(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c", "d")
	fx.Define(t, "other", "c")
	fx.AddRow(t, "work", "r0000", 0)
	fx.AddRow(t, "work", "r0001", 1)
	fx.ActivateWithLua(t, rowsetPrefixProbeLua)
	step := rowsetAdvance(fx.Space, rowsetRanks(2), []string{"restored"})
	raw, err := EncodeStep(step)
	if err != nil {
		t.Fatalf("encode original rowset advance: %v", err)
	}
	before := commitProbeImage(t, fx.Client)
	for _, mode := range []string{"rank", "remove", "reorder", "insert_guard", "shift_advance"} {
		t.Run(mode, func(t *testing.T) {
			value, err := fx.Client.FCall(context.Background(), "ns_tset_rowset_prefix_probe", []string{},
				Version, string(raw), mode).Result()
			if err != nil {
				t.Fatalf("prefix probe FCALL: %v", err)
			}
			encoded, ok := value.(string)
			if !ok {
				t.Fatalf("prefix probe reply type %T", value)
			}
			var result struct {
				Status  string `json:"status"`
				Code    string `json:"code"`
				Reads   int    `json:"reads"`
				Charged int    `json:"charged"`
			}
			if err := json.Unmarshal([]byte(encoded), &result); err != nil {
				t.Fatalf("decode prefix probe %q: %v", encoded, err)
			}
			if result.Status != "refused" || result.Code != "REQUEST" ||
				result.Reads != 0 || result.Charged != 0 {
				t.Fatalf("%s mutation escaped pinned prefix/advance: %s", mode, encoded)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Fatalf("%s preplan mutation changed Redis whole-key image", mode)
			}
		})
	}
}
