//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// The test-only extension uses the confirmed AL5 read seam. It owns no Sprint
// state or Layer 2 callback; related and newclockkind are inert registered kinds.
const readExtensionProbeLua = `
redis.register_function('ns_tset_read_extension_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local mode=args[3]
  local trace=S.array()
  local original=S.readcmd
  S.readcmd=function(ctx,d,reserve,kind)
    local argv=S.array()
    for i,v in ipairs(d.argv) do argv[i]=v end
    trace[#trace+1]=argv
    return original(ctx,d,reserve,kind)
  end
  local extension={kinds={'related'}}
  extension.validate=function(q,index)
    local function invalid() return nil,S.refuse('REQUEST',{query_index=index}) end
    if q.kind=='newclockkind' then
      for key in pairs(q) do if key~='kind' then return invalid() end end
      return true,nil
    end
    if q.kind~='related' or type(q.fields)~='table' or not S.is_array(q.fields)
      or #q.fields>128 then return invalid() end
    for _,field in ipairs(q.fields) do if not S.name(field) then return invalid() end end
    if q.tables~=nil then
      if q.table~=nil or q.id~=nil or not S.is_array(q.tables)
        or #q.tables<1 or #q.tables>5 then return invalid() end
      for _,t in ipairs(q.tables) do if not S.name(t) then return invalid() end end
    elseif type(q.table)~='string' or not S.name(q.table)
      or type(q.id)~='string' or not S.name(q.id) then return invalid() end
    for key in pairs(q) do
      if key~='kind' and key~='table' and key~='id' and key~='fields'
        and key~='tables' and key~='exhaust' then return invalid() end
    end
    if q.exhaust~=nil and type(q.exhaust)~='boolean' then return invalid() end
    return true,nil
  end
  extension.read=function(ctx,q,index)
    if q.kind=='newclockkind' then
      return {kind='newclockkind',index=index,time_ms=ctx.now_ms},nil
    end
    if q.tables then
      for _,t in ipairs(q.tables) do
        local _,err=S.ensure_read_table(ctx,t,index)
        if err then return nil,err end
      end
      return {kind='related',index=index,tables=#q.tables},nil
    end
    if q.exhaust then
      local _,err=S.charge(ctx,'record',10000)
      if err then return nil,err end
    end
    local record,err=S.read_record(ctx,q.table,q.id,q.fields,index)
    if err then return nil,err end
    return {kind='related',index=index,time_ms=ctx.now_ms,
      seen_record=ctx.budget.record,seen_field=ctx.budget.field,
      definition_prefix=ctx.defs[q.table].member_prefix,record=record},nil
  end
  if mode=='duplicate' then extension.kinds={'related','related'}
  elseif mode=='collision_l1' then extension.kinds={'ids'}
  elseif mode=='collision_l2' then extension.kinds={'lines'}
  elseif mode=='newclockkind' then extension.kinds={'newclockkind'}
  elseif mode=='malformed_kind' then extension.kinds={'bad kind'}
  elseif mode=='oversized_registry' then
    extension.kinds={}
    for i=1,1025 do extension.kinds[i]='kind'..i end
  elseif mode=='sparse' then extension.kinds={[1]='related',[3]='front'}
  elseif mode=='missing_validate' then extension.validate=nil
  elseif mode=='missing_read' then extension.read=nil end
  local ok,encoded=pcall(S.read,args[1],args[2],nil,extension)
  S.readcmd=original
  if not ok then error(encoded,0) end
  return S.json.encode({reply=S.json.decode(encoded),trace=trace})
end)
`

// This second private callback exercises the checked AL5 helpers without a
// Sprint implementation. It never calls raw Redis from its read extension.
const checkedReadProbeLua = `
redis.register_function('ns_tset_checked_read_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local mode=args[3]
  local trace=S.array()
  local original=S.readcmd
  S.readcmd=function(ctx,d,reserve,kind)
    local argv=S.array()
    for i,v in ipairs(d.argv) do argv[i]=v end
    trace[#trace+1]=argv
    return original(ctx,d,reserve,kind)
  end
  local emit_refused=false
  local extension={kinds={'newclockkind'},
    validate=function(q,index)
      if q.kind~='newclockkind' or q.mode~=nil then
        return nil,S.refuse('REQUEST',{query_index=index})
      end
      for key in pairs(q) do if key~='kind' then
        return nil,S.refuse('REQUEST',{query_index=index})
      end end
      return true,nil
    end,
    read=function(ctx,q,index)
      local key=ctx.space..'probe:absent'
      if mode=='invalid_time' then
        local _,err=S.read_probe(ctx,{'TIME'},key,'any',16)
        if err then return nil,err end
      elseif mode=='invalid_exists' then
        local _,err=S.read_probe(ctx,{'EXISTS',key,key},key,'any',16)
        if err then return nil,err end
      elseif mode=='cell_budget' then
        local _,err=S.charge(ctx,'cell',S.limits.cell-ctx.budget.cell)
        if err then return nil,err end
        _,err=S.read_probe(ctx,{'EXISTS',key},key,'any',16)
        if err then return nil,err end
      elseif mode=='structural_any' then
        local start=ctx.budget.cell
        local typ,err=S.read_probe(ctx,{'TYPE',key},key,'any',16)
        if err then return nil,err end
        local exists;exists,err=S.read_probe(ctx,{'EXISTS',key},key,'any',16)
        if err then return nil,err end
        return {kind='newclockkind',cell_delta=ctx.budget.cell-start,
          type_value=typ,exists=exists},nil
      elseif mode=='unbounded_zrange' then
        local range_key=ctx.space..'index:good'
        local _,err=S.read_probe(ctx,{'ZRANGE',range_key,'0','-1'},
          range_key,'zset',8388608)
        if err then return nil,err end
      elseif mode=='unbounded_hgetall' then
        local record_key=ctx.space..'member:work:absent'
        local _,err=S.read_probe(ctx,{'HGETALL',record_key},
          record_key,'hash',49152)
        if err then return nil,err end
      elseif mode=='range_head' or mode=='range_bad_lookahead' then
        local range_key=ctx.space..'index:'..(mode=='range_head' and 'good' or 'bad')
        local head,err=S.read_range_head(ctx,range_key,{min='0',max='4'},2)
        if err then return nil,err end
        return {kind='newclockkind',head=head,range_id=ctx.budget.range_id},nil
      elseif mode=='emit_near_cap' or mode=='emit_no_double_charge' then
        -- One bounded generated item avoids a large seeded store or many
        -- allocations while testing the real encoded output boundary.
        local size=mode=='emit_near_cap' and 8388100 or 4300000
        local item={payload=string.rep('x',size)}
        local _,err=S.emit_read_item(ctx,item,index)
        if err then emit_refused=true;return nil,err end
        return {kind='newclockkind',item=item},nil
      elseif mode=='final_exact_over' then
        -- An additive numeric test counter keeps the callback's budget seal
        -- valid while its long name bloats only the final reply envelope.
        -- The small answer passes its exact per-query close first.
        ctx.budget[string.rep('x',8388600)]=1
        return {kind='newclockkind'},nil
      end
      return nil,S.refuse('REQUEST',{query_index=index})
    end}
  local ok,encoded=pcall(S.read,args[1],args[2],nil,extension)
  S.readcmd=original
  if not ok then error(encoded,0) end
  return S.json.encode({reply=S.json.decode(encoded),trace=trace,
    emit_refused=emit_refused})
end)
`

type readExtensionProbeReply struct {
	Reply struct {
		Status  string            `json:"status"`
		Code    string            `json:"code"`
		TimeMS  string            `json:"time_ms"`
		Answers []json.RawMessage `json:"answers"`
		Detail  struct {
			Budget      string `json:"budget"`
			Actual      int    `json:"actual"`
			Limit       int    `json:"limit"`
			QueryIndex  *int   `json:"query_index"`
			ActiveEpoch string `json:"active_epoch"`
		} `json:"detail"`
		Counters struct {
			Record  int `json:"record"`
			Field   int `json:"field"`
			Cell    int `json:"cell"`
			RangeID int `json:"range_id"`
		} `json:"counters"`
	} `json:"reply"`
	Trace       [][]string `json:"trace"`
	EmitRefused bool       `json:"emit_refused"`
}

func readExtensionCall(t *testing.T, fx *tsetFixture, raw, mode string) readExtensionProbeReply {
	return readExtensionCallNamed(t, fx, "ns_tset_read_extension_probe", raw, mode)
}

func readExtensionCallNamed(t *testing.T, fx *tsetFixture, name, raw, mode string) readExtensionProbeReply {
	t.Helper()
	// Large boundary replies use a separate client on this fixture's private
	// server, with transport retries disabled and a bounded longer read wait.
	c := redis.NewClient(&redis.Options{Addr: fx.Client.Options().Addr, MaxRetries: -1,
		ReadTimeout: 30 * time.Second})
	t.Cleanup(func() { _ = c.Close() })
	value, err := c.FCall(context.Background(), name, []string{}, Version, raw, mode).Result()
	if err != nil {
		t.Fatalf("AL5 probe FCALL %s: %v", mode, err)
	}
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("AL5 probe FCALL %s returned %T", mode, value)
	}
	var reply readExtensionProbeReply
	if err := json.Unmarshal([]byte(encoded), &reply); err != nil {
		t.Fatalf("AL5 probe reply %q: %v", encoded, err)
	}
	return reply
}

func checkedReadFixture(t *testing.T) *tsetFixture {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	ctx := context.Background()
	for _, tc := range []struct {
		key string
		ids []string
	}{
		{key: fx.Space + "index:good", ids: []string{"first", "second", "third"}},
		{key: fx.Space + "index:bad", ids: []string{"first", "second", strings.Repeat("x", 257)}},
	} {
		members := make([]redis.Z, len(tc.ids))
		for i, id := range tc.ids {
			members[i] = redis.Z{Score: float64(i + 1), Member: id}
		}
		if err := fx.Client.ZAdd(ctx, tc.key, members...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	fx.ActivateWithLua(t, checkedReadProbeLua)
	return fx
}

// The trace hook observes calls through the public S.readcmd entry point.
// AL5 checked helpers use the private trusted closure, so commandstats deltas
// are the authoritative observation for all executed commands.
func readExtensionCommandStats(t *testing.T, c *redis.Client) string {
	t.Helper()
	info, err := c.Info(context.Background(), "commandstats").Result()
	if err != nil {
		t.Fatalf("read private Redis commandstats: %v", err)
	}
	return info
}

func readExtensionExecutedCalls(t *testing.T, info, command string) int64 {
	t.Helper()
	prefix := "cmdstat_" + strings.ToLower(command) + ":"
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		for _, field := range strings.Split(strings.TrimPrefix(line, prefix), ",") {
			if strings.HasPrefix(field, "calls=") {
				calls, err := strconv.ParseInt(strings.TrimPrefix(field, "calls="), 10, 64)
				if err != nil {
					t.Fatalf("parse %s commandstats %q: %v", command, field, err)
				}
				return calls
			}
		}
		t.Fatalf("%s commandstats lacks calls field: %q", command, line)
	}
	return 0
}

func readExtensionExecutedDelta(t *testing.T, before, after, command string) int64 {
	t.Helper()
	return readExtensionExecutedCalls(t, after, command) - readExtensionExecutedCalls(t, before, command)
}

func readExtensionFixture(t *testing.T) *tsetFixture {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	if err := fx.Client.HSet(context.Background(), fixtureRecordKey(fx.Space, "work", "card"),
		map[string]any{"epoch": "0", "revision": "1", "state": "ready"}).Err(); err != nil {
		t.Fatal(err)
	}
	fx.ActivateWithLua(t, readExtensionProbeLua)
	return fx
}

func readExtensionRaw(space, epoch, mode, queries string) string {
	return fmt.Sprintf(`{"epoch":%q,"space":%q,"mode":%q,"queries":%s}`, epoch, space, mode, queries)
}

func TestSprintReadPureValidationBeforeStoreAccess(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, mode, readMode, queries, code string
	}{
		{name: "late_invalid_query", readMode: "atomic", queries: `[{"kind":"ids","t":"work","ids":["card"],"fields":["state"]},{"kind":"related","table":"work","id":"card"}]`, code: "REQUEST"},
		{name: "page_extension", readMode: "page", queries: `[{"kind":"related","table":"work","id":"card","fields":[]}]`, code: "REQUEST"},
		{name: "unregistered_valid_query", readMode: "atomic", queries: `[{"kind":"newclockkind"}]`, code: "REQUEST"},
		{name: "duplicate_registry", mode: "duplicate", readMode: "atomic", queries: `[{"kind":"related","table":"work","id":"card","fields":[]}]`, code: "CONFIG"},
		{name: "l1_kind_collision", mode: "collision_l1", readMode: "atomic", queries: `[{"kind":"related","table":"work","id":"card","fields":[]}]`, code: "CONFIG"},
		{name: "l2_kind_collision", mode: "collision_l2", readMode: "atomic", queries: `[{"kind":"related","table":"work","id":"card","fields":[]}]`, code: "CONFIG"},
		{name: "malformed_kind", mode: "malformed_kind", readMode: "atomic", queries: `[{"kind":"related","table":"work","id":"card","fields":[]}]`, code: "CONFIG"},
		{name: "oversized_registry", mode: "oversized_registry", readMode: "atomic", queries: `[{"kind":"related","table":"work","id":"card","fields":[]}]`, code: "CONFIG"},
		{name: "sparse_registry", mode: "sparse", readMode: "atomic", queries: `[{"kind":"related","table":"work","id":"card","fields":[]}]`, code: "CONFIG"},
		{name: "missing_validate", mode: "missing_validate", readMode: "atomic", queries: `[{"kind":"related","table":"work","id":"card","fields":[]}]`, code: "CONFIG"},
		{name: "missing_read", mode: "missing_read", readMode: "atomic", queries: `[{"kind":"related","table":"work","id":"card","fields":[]}]`, code: "CONFIG"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := readExtensionFixture(t)
			before := commitProbeImage(t, fx.Client)
			beforeStats := readExtensionCommandStats(t, fx.Client)
			result := readExtensionCall(t, fx, readExtensionRaw(fx.Space, "0", tc.readMode, tc.queries), tc.mode)
			afterStats := readExtensionCommandStats(t, fx.Client)
			if result.Reply.Status != "refused" || result.Reply.Code != tc.code {
				t.Errorf("want %s before store access; got status=%q code=%q", tc.code, result.Reply.Status, result.Reply.Code)
			}
			for _, command := range []string{"TIME", "HGET", "HGETALL", "HLEN", "HMGET", "TYPE", "EXISTS", "ZRANGE"} {
				if got := readExtensionExecutedDelta(t, beforeStats, afterStats, command); got != 0 {
					t.Errorf("validation refusal executed %s %d times before refusal", command, got)
				}
			}
			if len(result.Reply.Answers) != 0 {
				t.Errorf("validation refusal leaked partial answers: %s", result.Reply.Answers)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Error("validation refusal changed the whole store")
			}
		})
	}
}

func TestSprintReadNewClockKind(t *testing.T) {
	t.Parallel()
	fx := readExtensionFixture(t)
	before := commitProbeImage(t, fx.Client)
	beforeStats := readExtensionCommandStats(t, fx.Client)
	queries := `[{"kind":"newclockkind"}]`
	result := readExtensionCall(t, fx, readExtensionRaw(fx.Space, "0", "atomic", queries), "newclockkind")
	afterStats := readExtensionCommandStats(t, fx.Client)
	if result.Reply.Status != "read" || len(result.Reply.Answers) != 1 || result.Reply.TimeMS == "" {
		t.Fatalf("newly registered kind did not dispatch: %+v", result.Reply)
	}
	var answer struct {
		Kind   string `json:"kind"`
		Index  int    `json:"index"`
		TimeMS string `json:"time_ms"`
	}
	if err := json.Unmarshal(result.Reply.Answers[0], &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Kind != "newclockkind" || answer.Index != 0 || answer.TimeMS != result.Reply.TimeMS {
		t.Errorf("registered callback did not receive the common clock and index: %+v", answer)
	}
	timeCalls := int(readExtensionExecutedDelta(t, beforeStats, afterStats, "TIME"))
	if timeCalls != 1 {
		t.Errorf("new clock kind used %d TIME calls, want one: %v", timeCalls, result.Trace)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("new clock kind changed the whole store")
	}
}

func TestSprintCompositeReadSharedSnapshotAndBudget(t *testing.T) {
	t.Parallel()
	fx := readExtensionFixture(t)
	before := commitProbeImage(t, fx.Client)
	beforeStats := readExtensionCommandStats(t, fx.Client)
	queries := `[{"kind":"related","table":"work","id":"card","fields":["state"]},` +
		`{"kind":"ids","t":"work","ids":["card"],"fields":["state"]},` +
		`{"kind":"related","table":"work","id":"card","fields":["state"]}]`
	result := readExtensionCall(t, fx, readExtensionRaw(fx.Space, "0", "atomic", queries), "normal")
	afterStats := readExtensionCommandStats(t, fx.Client)
	if result.Reply.Status != "read" || len(result.Reply.Answers) != 3 || result.Reply.TimeMS == "" {
		t.Fatalf("mixed AL5 read lost ordered complete reply: %+v", result.Reply)
	}
	var first, middle, third struct {
		Kind       string `json:"kind"`
		Index      int    `json:"index"`
		TimeMS     string `json:"time_ms"`
		SeenRecord int    `json:"seen_record"`
		SeenField  int    `json:"seen_field"`
	}
	if err := json.Unmarshal(result.Reply.Answers[0], &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result.Reply.Answers[1], &middle); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result.Reply.Answers[2], &third); err != nil {
		t.Fatal(err)
	}
	if first.Kind != "related" || first.Index != 0 || first.SeenRecord != 1 || first.SeenField != 1 ||
		middle.Kind != "ids" || third.Kind != "related" || third.Index != 2 ||
		third.SeenRecord != 3 || third.SeenField != 3 ||
		first.TimeMS != result.Reply.TimeMS || third.TimeMS != result.Reply.TimeMS {
		t.Errorf("mixed query order or shared context changed: first=%+v middle=%+v third=%+v", first, middle, third)
	}
	if result.Reply.Counters.Record != 3 || result.Reply.Counters.Field != 3 {
		t.Errorf("repeated ID/field occurrences not charged: %+v", result.Reply.Counters)
	}
	timeCalls := int(readExtensionExecutedDelta(t, beforeStats, afterStats, "TIME"))
	metadataReads := int(readExtensionExecutedDelta(t, beforeStats, afterStats, "HLEN"))
	fieldReads := int(readExtensionExecutedDelta(t, beforeStats, afterStats, "HMGET"))
	if timeCalls != 1 || metadataReads != 2 || fieldReads != 2 {
		t.Errorf("one TIME, one definition HLEN, one record HLEN, and two HMGET fetches expected; TIME=%d HLEN=%d HMGET=%d trace=%v", timeCalls, metadataReads, fieldReads, result.Trace)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("mixed read changed the whole store")
	}
	budgetQueries := `[{"kind":"related","table":"work","id":"card","fields":["state"]},` +
		`{"kind":"related","table":"work","id":"card","fields":["state"],"exhaust":true}]`
	budget := readExtensionCall(t, fx, readExtensionRaw(fx.Space, "0", "atomic", budgetQueries), "normal")
	if budget.Reply.Status != "refused" || budget.Reply.Code != "BUDGET" || len(budget.Reply.Answers) != 0 ||
		budget.Reply.Detail.Budget != "record" || budget.Reply.Detail.Actual != 10001 || budget.Reply.Detail.Limit != 10000 {
		t.Errorf("shared read budget refusal must be atomic: %+v", budget.Reply)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("shared read budget refusal changed the whole store")
	}
}

func TestSprintReadRequestedEpochDefinitions(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	ctx := context.Background()
	if err := fx.Client.HSet(ctx, fixtureRecordKey(fx.Space, "work", "old"),
		map[string]any{"epoch": "0", "revision": "1", "legacy": "past"}).Err(); err != nil {
		t.Fatal(err)
	}
	fx.Epoch = "1"
	fx.seedEpoch(t)
	current := fx.Space + "table:work"
	if err := fx.Client.HDel(ctx, current, "col:c").Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.HSet(ctx, current, map[string]any{
		"order": "d", "col:d": "set", "member_prefix": fx.Space + "member:work_new:",
	}).Err(); err != nil {
		t.Fatal(err)
	}
	activeDefinition, err := fx.Client.HGetAll(ctx, current).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.HSet(ctx, fixtureDefinitionKey(fx.Space, "work", "1"), activeDefinition).Err(); err != nil {
		t.Fatal(err)
	}
	fx.ActivateWithLua(t, readExtensionProbeLua)
	before := commitProbeImage(t, fx.Client)
	queries := `[{"kind":"related","table":"work","id":"old","fields":["legacy"]}]`
	result := readExtensionCall(t, fx, readExtensionRaw(fx.Space, "0", "atomic", queries), "normal")
	if result.Reply.Status != "read" || len(result.Reply.Answers) != 1 {
		t.Fatalf("retained composite read: %+v", result.Reply)
	}
	var answer struct {
		DefinitionPrefix string `json:"definition_prefix"`
		Record           struct {
			Exists bool `json:"exists"`
			Fields map[string]struct {
				Present bool   `json:"present"`
				Value   string `json:"value"`
			} `json:"fields"`
		} `json:"record"`
	}
	if err := json.Unmarshal(result.Reply.Answers[0], &answer); err != nil {
		t.Fatal(err)
	}
	if answer.DefinitionPrefix != fx.Space+"member:work:" || !answer.Record.Exists ||
		!answer.Record.Fields["legacy"].Present || answer.Record.Fields["legacy"].Value != "past" {
		t.Errorf("callback-discovered retained table used active definition: %+v", answer)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("retained composite read changed the whole store")
	}
}

func TestSprintReadDynamicFourTableLimit(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	for _, name := range []string{"one", "two", "three", "four"} {
		fx.Define(t, name, "c")
	}
	fx.ActivateWithLua(t, readExtensionProbeLua)
	before := commitProbeImage(t, fx.Client)
	queries := `[{"kind":"related","tables":["one","two","three","four","fifth"],"fields":[]}]`
	result := readExtensionCall(t, fx, readExtensionRaw(fx.Space, "0", "atomic", queries), "normal")
	if result.Reply.Status != "refused" || result.Reply.Code != "LIMIT" ||
		result.Reply.Detail.Budget != "tables" || result.Reply.Detail.Actual != 5 || result.Reply.Detail.Limit != 4 ||
		len(result.Reply.Answers) != 0 {
		t.Errorf("dynamic fifth table did not meet common limit: %+v", result.Reply)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("fifth-table refusal changed the whole store")
	}
}

func TestSprintReadCheckedProbe(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode, code, budget string
	}{
		{mode: "invalid_time", code: "REQUEST"},
		{mode: "invalid_exists", code: "REQUEST"},
		{mode: "cell_budget", code: "BUDGET", budget: "cell"},
		{mode: "unbounded_zrange", code: "REQUEST"},
		{mode: "unbounded_hgetall", code: "REQUEST"},
		{mode: "structural_any"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			fx := checkedReadFixture(t)
			before := commitProbeImage(t, fx.Client)
			beforeStats := readExtensionCommandStats(t, fx.Client)
			raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"newclockkind"}]`)
			result := readExtensionCallNamed(t, fx, "ns_tset_checked_read_probe", raw, tc.mode)
			afterStats := readExtensionCommandStats(t, fx.Client)
			for _, command := range []string{"TIME", "TYPE", "EXISTS", "ZRANGE", "HGET", "HLEN", "HGETALL"} {
				want := int64(0)
				switch {
				case command == "TIME":
					want = 1
				case command == "HGET":
					// S.open_state always checks the engine and reads the active epoch.
					want = 2
					if tc.mode == "structural_any" {
						want++ // Generic nonstructural probes first load the epoch catalog.
					}
				case tc.mode == "structural_any" && (command == "TYPE" || command == "EXISTS"):
					want = 1
				case tc.mode == "structural_any" &&
					(command == "HLEN" || command == "HGETALL"):
					want = 1 // The catalog definition is loaded and checked once.
				}
				got := readExtensionExecutedCalls(t, afterStats, command) -
					readExtensionExecutedCalls(t, beforeStats, command)
				if got != want {
					t.Errorf("%s executed %s %d times, want %d (trace records attempts): %v", tc.mode, command, got, want, result.Trace)
				}
			}
			timeCalls := int(readExtensionExecutedDelta(t, beforeStats, afterStats, "TIME"))
			typeCalls := int(readExtensionExecutedDelta(t, beforeStats, afterStats, "TYPE"))
			existsCalls := int(readExtensionExecutedDelta(t, beforeStats, afterStats, "EXISTS"))
			collectionCalls := int(readExtensionExecutedDelta(t, beforeStats, afterStats, "ZRANGE") +
				readExtensionExecutedDelta(t, beforeStats, afterStats, "HGETALL"))
			if timeCalls != 1 {
				t.Errorf("common read sampled TIME %d times: %v", timeCalls, result.Trace)
			}
			if tc.code != "" {
				if result.Reply.Status != "refused" || result.Reply.Code != tc.code ||
					result.Reply.Detail.Budget != tc.budget || len(result.Reply.Answers) != 0 {
					t.Errorf("checked probe refusal: %+v", result.Reply)
				}
				if tc.mode == "cell_budget" {
					if existsCalls != 0 || typeCalls != 0 {
						t.Errorf("cell-budget refusal executed its target probe: TYPE=%d EXISTS=%d", typeCalls, existsCalls)
					}
				} else if typeCalls != 0 || existsCalls != 0 || collectionCalls != 0 {
					t.Errorf("invalid descriptor reached Redis: TYPE=%d EXISTS=%d collections=%d", typeCalls, existsCalls, collectionCalls)
				}
			} else {
				if result.Reply.Status != "read" || len(result.Reply.Answers) != 1 ||
					typeCalls != 1 || existsCalls != 1 {
					t.Errorf("TYPE/EXISTS any probes were not checked once each: reply=%+v trace=%v", result.Reply, result.Trace)
				}
				var answer struct {
					CellDelta int `json:"cell_delta"`
				}
				if len(result.Reply.Answers) == 1 {
					if err := json.Unmarshal(result.Reply.Answers[0], &answer); err != nil {
						t.Fatal(err)
					}
					if answer.CellDelta != 5 {
						t.Errorf("two probes plus catalog/definition admission charged %d cell slots, want 5", answer.CellDelta)
					}
				}
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Error("checked probe changed the whole store")
			}
		})
	}
}

func TestSprintReadRangeHeadLookahead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode string
		bad  bool
	}{
		{mode: "range_head"},
		{mode: "range_bad_lookahead", bad: true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			fx := checkedReadFixture(t)
			before := commitProbeImage(t, fx.Client)
			beforeStats := readExtensionCommandStats(t, fx.Client)
			raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"newclockkind"}]`)
			result := readExtensionCallNamed(t, fx, "ns_tset_checked_read_probe", raw, tc.mode)
			afterStats := readExtensionCommandStats(t, fx.Client)
			if got := readExtensionExecutedDelta(t, beforeStats, afterStats, "ZRANGE"); got != 1 {
				t.Errorf("range-head callback executed ZRANGE %d times, want one; public trace=%v", got, result.Trace)
			}
			if tc.bad {
				if result.Reply.Status != "refused" || result.Reply.Code != "DRIFT" ||
					len(result.Reply.Answers) != 0 {
					t.Errorf("invalid lookahead was not checked before output: %+v", result.Reply)
				}
			} else {
				if result.Reply.Status != "read" || len(result.Reply.Answers) != 1 ||
					result.Reply.Counters.RangeID != 2 {
					t.Fatalf("bounded range head: %+v", result.Reply)
				}
				var answer struct {
					Head struct {
						IDs     []string `json:"ids"`
						Scores  []string `json:"scores"`
						HasMore bool     `json:"has_more"`
					} `json:"head"`
					RangeID int `json:"range_id"`
				}
				if err := json.Unmarshal(result.Reply.Answers[0], &answer); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(answer.Head.IDs, []string{"first", "second"}) ||
					!reflect.DeepEqual(answer.Head.Scores, []string{"1", "2"}) ||
					!answer.Head.HasMore || answer.RangeID != 2 {
					t.Errorf("lookahead leaked or returned-ID charge wrong: %+v", answer)
				}
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Error("range head changed the whole store")
			}
		})
	}
}

func TestSprintReadEmitterBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode       string
		wantRefuse bool
	}{
		{mode: "emit_near_cap", wantRefuse: true},
		{mode: "emit_no_double_charge"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			fx := checkedReadFixture(t)
			before := commitProbeImage(t, fx.Client)
			raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"newclockkind"}]`)
			result := readExtensionCallNamed(t, fx, "ns_tset_checked_read_probe", raw, tc.mode)
			if tc.wantRefuse {
				if result.Reply.Status != "refused" || result.Reply.Code != "BUDGET" ||
					result.Reply.Detail.Budget != "encoded_reply" || !result.EmitRefused ||
					len(result.Reply.Answers) != 0 {
					t.Errorf("near-cap item was not refused before append: %+v", result)
				}
			} else {
				answerBytes := 0
				if len(result.Reply.Answers) == 1 {
					answerBytes = len(result.Reply.Answers[0])
				}
				if result.Reply.Status != "read" || result.EmitRefused ||
					len(result.Reply.Answers) != 1 || answerBytes < 4300000 || answerBytes > 4300100 {
					t.Errorf("emitted item was double charged at exact answer close: status=%q code=%q bytes=%d", result.Reply.Status, result.Reply.Code, answerBytes)
				}
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Error("emission probe changed the whole store")
			}
		})
	}
}

func TestSprintReadFinalExactLimit(t *testing.T) {
	t.Parallel()
	fx := checkedReadFixture(t)
	before := commitProbeImage(t, fx.Client)
	raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"newclockkind"}]`)
	result := readExtensionCallNamed(t, fx, "ns_tset_checked_read_probe", raw, "final_exact_over")
	if result.Reply.Status != "refused" || result.Reply.Code != "BUDGET" ||
		result.Reply.Detail.Budget != "encoded_reply" ||
		result.Reply.Detail.Actual <= result.Reply.Detail.Limit ||
		result.Reply.Detail.Limit != 8*1024*1024 ||
		result.Reply.Detail.QueryIndex == nil || *result.Reply.Detail.QueryIndex != 0 ||
		result.EmitRefused || len(result.Reply.Answers) != 0 {
		t.Errorf("exact final serialization did not refuse an oversized complete answer at query 0: %+v", result)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("exact final reply refusal changed the whole store")
	}
}
