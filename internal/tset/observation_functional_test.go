//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
)

// The callback observes the actual Lua planner through its checked read seam.
// It is added only to this test's isolated Function library and never commits.
const observationProbeLua = `
redis.register_function('ns_tset_observation_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=2 then return S.json.encode(S.refuse('ARGS')) end
  local original=S.readcmd
  local open_trace=S.array()
  S.readcmd=function(c,d,reserve,kind)
    local argv=S.array()
    for i,v in ipairs(d.argv) do argv[i]=v end
    open_trace[#open_trace+1]=argv
    return original(c,d,reserve,kind)
  end
  local opened_ok,ctx,err=pcall(S.open,args[1],args[2])
  S.readcmd=original
  if not opened_ok then error(ctx,0) end
  if err then return S.json.encode(err) end
  if ctx.replay then return S.json.encode(S.refuse('REQUEST')) end
  local opened=ctx.budget.store_commands
  local trace=S.array()
  S.readcmd=function(c,d,reserve,kind)
    local argv=S.array()
    for i,v in ipairs(d.argv) do argv[i]=v end
    trace[#trace+1]=argv
    return original(c,d,reserve,kind)
  end
  local ok,plan,problem=pcall(S.plan,ctx)
  S.readcmd=original
  if not ok then error(plan,0) end
  if problem then return S.json.encode(problem) end
  return S.json.encode({status='trace',trace=trace,open_trace=open_trace,
    open_commands=opened,plan_store_commands=ctx.budget.store_commands-opened,
    field=ctx.budget.field,record=ctx.budget.record,cell=ctx.budget.cell,
    fetched_bytes=ctx.budget.fetched_bytes,changed=plan.changed,
    guarded=plan.guarded,planned_commands=ctx.budget.planned_commands})
end)
`

type observationTrace struct {
	Status            string     `json:"status"`
	Code              string     `json:"code"`
	Trace             [][]string `json:"trace"`
	OpenTrace         [][]string `json:"open_trace"`
	OpenCommands      int        `json:"open_commands"`
	PlanStoreCommands int        `json:"plan_store_commands"`
	Field             int        `json:"field"`
	Record            int        `json:"record"`
	Cell              int        `json:"cell"`
	FetchedBytes      int        `json:"fetched_bytes"`
	Changed           int        `json:"changed"`
	Guarded           int        `json:"guarded"`
	PlannedCommands   int        `json:"planned_commands"`
}

func observationFixture(t *testing.T, rowCount, members int, foreignCopies bool, columns ...string) *tsetFixture {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", columns...)
	rows := make([]redis.Z, rowCount)
	for i := 0; i < rowCount; i++ {
		rows[i] = redis.Z{Score: float64(i), Member: fmt.Sprintf("r%04d", i)}
	}
	if err := fx.Client.ZAdd(context.Background(), fixtureRowsKey(fx.Space, "work", "0"), rows...).Err(); err != nil {
		t.Fatalf("seed %d observation rows: %v", rowCount, err)
	}
	if members > 0 {
		ctx := context.Background()
		pipe := fx.Client.Pipeline()
		zs := make([]redis.Z, members)
		for i := 0; i < members; i++ {
			id := fmt.Sprintf("id%04d", i)
			pipe.HSet(ctx, fixtureRecordKey(fx.Space, "work", id), map[string]any{
				"epoch": "0", "revision": "1", "place:work": "r0000:c",
				"f1": "one", "f2": "two",
			})
			zs[i] = redis.Z{Score: 1, Member: id}
		}
		pipe.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r0000", "c"), zs...)
		if foreignCopies {
			// Deliberate out-of-band corruption remains inside the fixture's
			// declared initialization interval, before the writer is loaded.
			pipe.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r0000", "d"),
				redis.Z{Score: 1, Member: "id0000"})
			pipe.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r0001", "e"),
				redis.Z{Score: 1, Member: "id0000"})
		}
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatalf("seed %d observation members: %v", members, err)
		}
	}
	fx.ActivateWithLua(t, observationProbeLua)
	return fx
}

func observationRequest(t *testing.T, space string, entries []Entry) string {
	t.Helper()
	if entries == nil {
		entries = []Entry{}
	}
	raw, err := EncodeStep(Step{Epoch: "0", Space: space, Entries: entries})
	if err != nil {
		t.Fatalf("encode observation request: %v", err)
	}
	return string(raw)
}

func observationCall(t *testing.T, fx *tsetFixture, entries []Entry) observationTrace {
	t.Helper()
	raw := observationRequest(t, fx.Space, entries)
	value, err := fx.Client.FCall(context.Background(), "ns_tset_observation_probe", []string{}, Version, raw).Result()
	if err != nil {
		t.Fatalf("observation FCALL: %v", err)
	}
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("observation FCALL returned %T", value)
	}
	var result observationTrace
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		t.Fatalf("observation reply %q: %v", encoded, err)
	}
	if result.Status != "trace" {
		t.Fatalf("observation result status=%q code=%q: %s", result.Status, result.Code, encoded)
	}
	return result
}

func observationGuard(ids []string, fields ...string) Entry {
	return Entry{Kind: "guard", Table: "work", From: "r0000:c", IDs: ids, BeforeFields: fields}
}

func TestPointPlacementNoSiblingScan(t *testing.T) {
	t.Parallel()
	fx := observationFixture(t, 2, 1, true, "c", "d", "e")
	// A raw out-of-band duplicate is deliberately outside OnePlace's supported
	// writer guarantee. A point guard must still read only its indicated cell.
	trace := observationCall(t, fx, []Entry{observationGuard([]string{"id0000"})})
	if trace.Guarded != 1 || trace.Changed != 0 {
		t.Fatalf("point guard result = %+v", trace)
	}
	source := fixtureCellKey(fx.Space, "work", "0", "r0000", "c")
	forbidden := map[string]bool{
		fixtureCellKey(fx.Space, "work", "0", "r0000", "d"): true,
		fixtureCellKey(fx.Space, "work", "0", "r0001", "e"): true,
	}
	seenRecord, seenSource := false, false
	for _, argv := range trace.Trace {
		if len(argv) < 2 {
			t.Fatalf("invalid traced argv %v", argv)
		}
		command := strings.ToUpper(argv[0])
		key := argv[1]
		if forbidden[key] {
			t.Errorf("point planner read sibling cell: %v", argv)
		}
		if command == "SCAN" || command == "KEYS" || command == "ZRANGE" || command == "ZSCAN" || command == "HGETALL" && key == fixtureRecordKey(fx.Space, "work", "id0000") {
			t.Errorf("point planner used broad scan: %v", argv)
		}
		if key == fixtureRecordKey(fx.Space, "work", "id0000") {
			seenRecord = true
		}
		if command == "ZSCORE" && key == source && len(argv) == 3 && argv[2] == "id0000" {
			seenSource = true
		}
	}
	if !seenRecord || !seenSource {
		t.Errorf("point record/source observations missing: trace=%v", trace.Trace)
	}
}

func TestNoHiddenPlacementScan(t *testing.T) {
	t.Parallel()
	var small, large observationTrace
	for _, size := range []int{10, 1000} {
		size := size
		t.Run(fmt.Sprintf("rows_%d", size), func(t *testing.T) {
			t.Parallel()
			fx := observationFixture(t, size, 1, false, "c", "d")
			got := observationCall(t, fx, []Entry{observationGuard([]string{"id0000"})})
			if got.Guarded != 1 || got.Record != 1 {
				t.Fatalf("fixed-member guard = %+v", got)
			}
			if size == 10 {
				small = got
			} else {
				large = got
			}
		})
	}
	// The subtests above run in parallel. Their comparison is done by the
	// parent after t.Run returns and the Go test runner has joined them.
	t.Cleanup(func() {
		if !reflect.DeepEqual(small.Trace, large.Trace) || small.PlanStoreCommands != large.PlanStoreCommands ||
			small.Record != large.Record || small.Field != large.Field || small.Cell != large.Cell ||
			small.FetchedBytes != large.FetchedBytes {
			t.Errorf("fixed member work grew with rows: 10=%+v 1000=%+v", small, large)
		}
	})
}

func TestCommandsPerMember(t *testing.T) {
	t.Parallel()
	fx := observationFixture(t, 10, 4, false, "c")
	ids := []string{"id0000", "id0001", "id0002", "id0003"}
	fixed := observationCall(t, fx, nil)
	one := observationCall(t, fx, []Entry{observationGuard(ids[:1])})
	four := observationCall(t, fx, []Entry{observationGuard(ids)})
	twoEntries := observationCall(t, fx, []Entry{observationGuard(ids[:2]), observationGuard(ids[2:])})
	fields := observationCall(t, fx, []Entry{observationGuard(ids, "f1", "f2")})
	if one.OpenCommands != four.OpenCommands || four.OpenCommands != twoEntries.OpenCommands ||
		twoEntries.OpenCommands != fields.OpenCommands {
		t.Errorf("open table cost varies with members/entries/fields: %d %d %d %d %d",
			fixed.OpenCommands, one.OpenCommands, four.OpenCommands, twoEntries.OpenCommands, fields.OpenCommands)
	}
	// Opening the first request that names work loads that table definition
	// once. Its HLEN/HGETALL pair costs four checked store commands; neither
	// is a per-member or per-entry planner observation.
	tableKey := fx.Space + "table:work"
	var definitionProbes []string
	for _, argv := range one.OpenTrace {
		if len(argv) >= 2 && argv[1] == tableKey {
			definitionProbes = append(definitionProbes, argv[0])
		}
	}
	if one.OpenCommands-fixed.OpenCommands != 4 ||
		len(one.OpenTrace)-len(fixed.OpenTrace) != 2 ||
		!reflect.DeepEqual(definitionProbes, []string{"HLEN", "HGETALL"}) {
		t.Errorf("first-table open cost: empty=%d/%v table=%d/%v definition probes=%v",
			fixed.OpenCommands, fixed.OpenTrace, one.OpenCommands, one.OpenTrace, definitionProbes)
	}
	memberTerm := four.PlanStoreCommands - one.PlanStoreCommands
	entryTerm := twoEntries.PlanStoreCommands - four.PlanStoreCommands
	fieldTerm := fields.PlanStoreCommands - four.PlanStoreCommands
	perMember := float64(memberTerm) / 3
	firstEntry := float64(one.PlanStoreCommands-fixed.PlanStoreCommands) - perMember
	if one.Record != 1 || four.Record != 4 || fields.Record != 4 || fields.Field != 8 || four.Field != 0 {
		t.Errorf("record/field accounting one=%+v four=%+v fields=%+v", one, four, fields)
	}
	if memberTerm <= 0 || memberTerm > 24 || firstEntry < 0 || entryTerm < 0 || entryTerm > 4 || fieldTerm <= 0 || fieldTerm > 8 {
		t.Errorf("unbounded/nonlinear read terms: 3 members=%d second entry=%d 8 fields=%d", memberTerm, entryTerm, fieldTerm)
	}
	if four.PlanStoreCommands >= one.PlanStoreCommands*4 {
		t.Errorf("four-member work multiplied one-member fixed overhead: one=%d four=%d", one.PlanStoreCommands, four.PlanStoreCommands)
	}
	t.Logf("planner terms: empty-open=%d table-open=%d empty-plan fixed=%d first-entry=%.2f per-member=%.2f second-entry=%d field-probe commands=%d for %d field observations; readcmd calls=%d/%d/%d/%d/%d",
		fixed.OpenCommands, one.OpenCommands, fixed.PlanStoreCommands, firstEntry, perMember, entryTerm, fieldTerm, fields.Field,
		len(fixed.Trace), len(one.Trace), len(four.Trace), len(twoEntries.Trace), len(fields.Trace))
}

type observationCommandHook struct {
	mu       sync.Mutex
	commands []string
}

func (h *observationCommandHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *observationCommandHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.mu.Lock()
		h.commands = append(h.commands, cmd.Name())
		h.mu.Unlock()
		return next(ctx, cmd)
	}
}
func (h *observationCommandHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.mu.Lock()
		for _, cmd := range cmds {
			h.commands = append(h.commands, cmd.Name())
		}
		h.mu.Unlock()
		return next(ctx, cmds)
	}
}
func (h *observationCommandHook) reset() { h.mu.Lock(); h.commands = nil; h.mu.Unlock() }
func (h *observationCommandHook) snapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.commands...)
}

func TestNoPerCardStoreLoop(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.AddRow(t, "work", "r", 0)
	fx.Activate(t)
	client := redis.NewClient(&redis.Options{Addr: fx.Client.Options().Addr, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	hook := &observationCommandHook{}
	client.AddHook(hook)
	store := NewRedis(client)
	ids, scores, about := make([]string, 100), make([]string, 100), make([]string, 100)
	for i := range ids {
		ids[i] = fmt.Sprintf("id%04d", i)
		scores[i] = "1"
		about[i] = fmt.Sprintf("primary%04d", i)
	}
	seed, err := store.Step(context.Background(), Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{
		Kind: "create", Table: "work", To: "r:c", IDs: ids, Scores: scores, About: about,
	}}})
	if err != nil || seed.Changed != len(ids) {
		t.Fatalf("seed 100 set members: reply=%+v err=%v", seed, err)
	}
	for _, n := range []int{1, 100} {
		hook.reset()
		reply, err := store.Step(context.Background(), Step{Epoch: "0", Space: fx.Space,
			Entries: []Entry{observationGuardForClient(ids[:n])}})
		if err != nil || reply.Guarded != n {
			t.Fatalf("guard %d members: reply=%+v err=%v", n, reply, err)
		}
		if got := hook.snapshot(); !reflect.DeepEqual(got, []string{"fcall"}) {
			t.Errorf("guard %d members issued %v, want one FCALL", n, got)
		}
	}
}

func observationGuardForClient(ids []string) Entry {
	return Entry{Kind: "guard", Table: "work", From: "r:c", IDs: ids}
}
