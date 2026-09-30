//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// These are the ten traces from L1 contract section 1.6. Each refusal is sent
// through the registered function, after a successful composed-profile seed.
// The snapshot includes the entire throwaway server, rather than a selection
// of keys the writer is expected to touch.
var commitProbeTables = []string{"work", "merge", "fleet", "reader"}

type commitProbeKey struct {
	Type string
	Dump string
}

func commitProbeImage(t *testing.T, c *redis.Client) map[string]commitProbeKey {
	t.Helper()
	ctx := context.Background()
	keys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	image := make(map[string]commitProbeKey, len(keys))
	for _, key := range keys {
		kind, err := c.Type(ctx, key).Result()
		if err != nil {
			t.Fatalf("TYPE %q: %v", key, err)
		}
		value, err := c.Dump(ctx, key).Result()
		if err != nil {
			t.Fatalf("DUMP %q: %v", key, err)
		}
		image[key] = commitProbeKey{Type: kind, Dump: value}
	}
	return image
}

func commitProbeNoKeys(t *testing.T, c *redis.Client, keys []string) {
	t.Helper()
	ctx := context.Background()
	pipe := c.Pipeline()
	answers := make([]*redis.IntCmd, len(keys))
	for i, key := range keys {
		answers[i] = pipe.Exists(ctx, key)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("EXISTS pipeline: %v", err)
	}
	for i, key := range keys {
		n, err := answers[i].Result()
		if err != nil {
			t.Fatalf("EXISTS %q: %v", key, err)
		}
		if n != 0 {
			t.Errorf("prospective key %q exists", key)
		}
	}
}

func commitProbeRawCall(t *testing.T, c *redis.Client, raw string) struct {
	Status string `json:"status"`
	Code   string `json:"code"`
} {
	t.Helper()
	value, err := c.FCall(context.Background(), "ns_tset_step", []string{}, "tset/1", raw).Result()
	if err != nil {
		t.Fatalf("raw FCALL returned a Redis error: %v", err)
	}
	var encoded []byte
	switch v := value.(type) {
	case string:
		encoded = []byte(v)
	case []byte:
		encoded = v
	default:
		t.Fatalf("raw FCALL returned %T, want JSON bulk string", value)
	}
	var reply struct {
		Status string `json:"status"`
		Code   string `json:"code"`
	}
	if err := json.Unmarshal(encoded, &reply); err != nil {
		t.Fatalf("raw FCALL reply %q is not JSON: %v", encoded, err)
	}
	return reply
}

func commitProbeRequest(t *testing.T, space, op string, entries []map[string]any) string {
	t.Helper()
	req := map[string]any{"epoch": "0", "space": space, "entries": entries}
	if op != "" {
		req["op"] = op
		req["intent"] = "commit-probe/" + op
		req["result"] = "commit probe"
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func commitProbeSetup(t *testing.T, space string, c *redis.Client,
	define func(*testing.T, string, ...string), addRow func(*testing.T, string, string, int64),
	activate func(*testing.T)) {
	t.Helper()
	for _, table := range commitProbeTables {
		define(t, table, "ready", "done")
		addRow(t, table, "r", 0)
	}
	activate(t)
	entries := make([]map[string]any, 0, len(commitProbeTables))
	for _, table := range commitProbeTables {
		entries = append(entries, map[string]any{
			"kind": "create", "t": table, "to": "r:ready",
			"ids": []string{"base-" + table}, "scores": []string{"1"},
			"about": []string{"seed-" + table},
			"set":   map[string]string{"seed": "present"},
		})
	}
	reply := commitProbeRawCall(t, c, commitProbeRequest(t, space, "", entries))
	if reply.Status != "ok" {
		t.Fatalf("valid four-table seed refused: status=%q code=%q", reply.Status, reply.Code)
	}
	for _, table := range commitProbeTables {
		key := fixtureHistoryKey(space, "0", "seed-"+table)
		if n, err := c.Exists(context.Background(), key).Result(); err != nil || n != 1 {
			t.Fatalf("seed history %q absent (exists=%d, err=%v)", key, n, err)
		}
	}
	if key := fixtureLogKey(space, "0"); c.Exists(context.Background(), key).Val() != 1 {
		t.Fatalf("seed log %q absent", key)
	}
}

func commitProbeMove(table string, rev string) map[string]any {
	return map[string]any{
		"kind": "move", "t": table, "from": "r:ready", "to": "r:done",
		"ids": []string{"base-" + table}, "revs": []string{rev},
		"about": []string{"change-" + table},
		"set":   map[string]string{"probe": "changed"},
	}
}

func commitProbeMoves() []map[string]any {
	entries := make([]map[string]any, 0, len(commitProbeTables))
	for _, table := range commitProbeTables {
		entries = append(entries, commitProbeMove(table, "1"))
	}
	return entries
}

func commitProbeNewKeys(space string, entries []map[string]any) []string {
	keys := []string{fixtureDoneKey(space, "0")}
	for _, entry := range entries {
		table, _ := entry["t"].(string)
		if to, ok := entry["to"].(string); ok {
			parts := strings.SplitN(to, ":", 2)
			if len(parts) == 2 {
				keys = append(keys, fixtureCellKey(space, table, "0", parts[0], parts[1]))
			}
		}
		if ids, ok := entry["ids"].([]string); ok && entry["kind"] == "create" {
			for _, id := range ids {
				keys = append(keys, fixtureRecordKey(space, table, id))
			}
		}
		if about, ok := entry["about"].([]string); ok {
			for _, id := range about {
				keys = append(keys, fixtureHistoryKey(space, "0", id))
			}
		}
	}
	return keys
}

func commitProbeRefusal(t *testing.T, c *redis.Client, space, code string, entries []map[string]any, newKeys []string) {
	t.Helper()
	if newKeys == nil {
		newKeys = commitProbeNewKeys(space, entries)
	}
	commitProbeNoKeys(t, c, newKeys)
	before := commitProbeImage(t, c)
	reply := commitProbeRawCall(t, c, commitProbeRequest(t, space, "refused-operation", entries))
	if reply.Status != "refused" || reply.Code != code {
		t.Errorf("want refused %s; got status=%q code=%q", code, reply.Status, reply.Code)
	}
	after := commitProbeImage(t, c)
	if !reflect.DeepEqual(before, after) {
		for key, value := range before {
			if after[key] != value {
				t.Errorf("existing key changed or disappeared: %q", key)
			}
		}
		for key := range after {
			if _, ok := before[key]; !ok {
				t.Errorf("new key appeared: %q", key)
			}
		}
	}
	commitProbeNoKeys(t, c, newKeys)
}

func TestCommitProbeRevisionSecondTable(t *testing.T) {
	t.Parallel()
	fx := newComposedTSetFixture(t)
	commitProbeSetup(t, fx.Space, fx.Client, fx.Define, fx.AddRow, fx.Activate)
	entries := commitProbeMoves()
	entries[1]["revs"] = []string{"0"}
	commitProbeRefusal(t, fx.Client, fx.Space, "REVISION", entries, nil)
}

func TestCommitProbeRecordWrongType(t *testing.T) {
	t.Parallel()
	fx := newComposedTSetFixture(t)
	commitProbeSetup(t, fx.Space, fx.Client, fx.Define, fx.AddRow, fx.Activate)
	key := fixtureRecordKey(fx.Space, "reader", "base-reader")
	if err := fx.Client.Del(context.Background(), key).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.Set(context.Background(), key, "wrong type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	commitProbeRefusal(t, fx.Client, fx.Space, "WRONGTYPE", commitProbeMoves(), nil)
}

func TestCommitProbeDestinationWrongType(t *testing.T) {
	t.Parallel()
	fx := newComposedTSetFixture(t)
	commitProbeSetup(t, fx.Space, fx.Client, fx.Define, fx.AddRow, fx.Activate)
	key := fixtureCellKey(fx.Space, "reader", "0", "r", "done")
	if err := fx.Client.Set(context.Background(), key, "wrong type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	newKeys := []string{fixtureDoneKey(fx.Space, "0")}
	for _, table := range commitProbeTables[:3] {
		newKeys = append(newKeys, fixtureCellKey(fx.Space, table, "0", "r", "done"), fixtureHistoryKey(fx.Space, "0", "change-"+table))
	}
	newKeys = append(newKeys, fixtureHistoryKey(fx.Space, "0", "change-reader"))
	commitProbeRefusal(t, fx.Client, fx.Space, "WRONGTYPE", commitProbeMoves(), newKeys)
}

func TestCommitProbeHistoryWrongType(t *testing.T) {
	t.Parallel()
	fx := newComposedTSetFixture(t)
	commitProbeSetup(t, fx.Space, fx.Client, fx.Define, fx.AddRow, fx.Activate)
	key := fixtureHistoryKey(fx.Space, "0", "change-reader")
	if err := fx.Client.Set(context.Background(), key, "wrong type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	entries := commitProbeMoves()
	newKeys := []string{fixtureDoneKey(fx.Space, "0")}
	for _, table := range commitProbeTables[:3] {
		newKeys = append(newKeys, fixtureCellKey(fx.Space, table, "0", "r", "done"), fixtureHistoryKey(fx.Space, "0", "change-"+table))
	}
	newKeys = append(newKeys, fixtureCellKey(fx.Space, "reader", "0", "r", "done"))
	commitProbeRefusal(t, fx.Client, fx.Space, "WRONGTYPE", entries, newKeys)
}

func TestCommitProbeCandidateOverflow(t *testing.T) {
	t.Parallel()
	fx := newComposedTSetFixture(t)
	commitProbeSetup(t, fx.Space, fx.Client, fx.Define, fx.AddRow, fx.Activate)
	entries := make([]map[string]any, 0, 4)
	newKeys := []string{fixtureDoneKey(fx.Space, "0")}
	for i, table := range commitProbeTables {
		count := 1000
		if i == 3 {
			count = 1001
		}
		ids, scores, about := make([]string, count), make([]string, count), make([]string, count)
		for j := range ids {
			ids[j] = fmt.Sprintf("candidate-%s-%04d", table, j)
			scores[j] = "1"
			about[j] = "bulk-" + table
			newKeys = append(newKeys, fixtureRecordKey(fx.Space, table, ids[j]))
		}
		newKeys = append(newKeys, fixtureHistoryKey(fx.Space, "0", "bulk-"+table))
		entries = append(entries, map[string]any{"kind": "create", "t": table, "to": "r:done", "ids": ids, "scores": scores, "about": about})
		newKeys = append(newKeys, fixtureCellKey(fx.Space, table, "0", "r", "done"))
	}
	commitProbeRefusal(t, fx.Client, fx.Space, "LIMIT", entries, newKeys)
}

func TestCommitProbeUnset8000(t *testing.T) {
	t.Parallel()
	fx := newComposedTSetFixture(t)
	commitProbeSetup(t, fx.Space, fx.Client, fx.Define, fx.AddRow, fx.Activate)
	entries := commitProbeMoves()
	unset := make([]string, 8000)
	for i := range unset {
		unset[i] = fmt.Sprintf("field-%04d", i)
	}
	entries[3]["unset"] = unset
	commitProbeRefusal(t, fx.Client, fx.Space, "LIMIT", entries, nil)
}

func TestCommitProbeRowsAcrossEntries(t *testing.T) {
	t.Parallel()
	fx := newComposedTSetFixture(t)
	commitProbeSetup(t, fx.Space, fx.Client, fx.Define, fx.AddRow, fx.Activate)
	entries := []map[string]any{commitProbeMove("reader", "1")}
	for batch := 0; batch < 40; batch++ {
		rows := make([]string, 100)
		for i := range rows {
			rows[i] = fmt.Sprintf("fleet-%02d-%02d", batch, i)
		}
		entries = append(entries, map[string]any{"kind": "rows", "t": "fleet", "add": rows})
	}
	newKeys := []string{fixtureDoneKey(fx.Space, "0"), fixtureCellKey(fx.Space, "reader", "0", "r", "done"), fixtureHistoryKey(fx.Space, "0", "change-reader")}
	commitProbeRefusal(t, fx.Client, fx.Space, "LIMIT", entries, newKeys)
	for batch := 0; batch < 40; batch++ {
		for i := 0; i < 100; i++ {
			row := fmt.Sprintf("fleet-%02d-%02d", batch, i)
			if score, err := fx.Client.ZScore(context.Background(), fixtureRowsKey(fx.Space, "fleet", "0"), row).Result(); err != redis.Nil {
				t.Errorf("row %s unexpectedly added: score=%v err=%v", row, score, err)
			}
		}
	}
}

func commitProbeBadScore(t *testing.T, score string) {
	t.Helper()
	fx := newComposedTSetFixture(t)
	commitProbeSetup(t, fx.Space, fx.Client, fx.Define, fx.AddRow, fx.Activate)
	entries := commitProbeMoves()[:3]
	entries = append(entries, map[string]any{
		"kind": "create", "t": "reader", "to": "r:done",
		"ids": []string{"bad-score-reader"}, "scores": []string{score},
		"about": []string{"bad-score-about"},
	})
	commitProbeRefusal(t, fx.Client, fx.Space, "REQUEST", entries, nil)
}

func TestCommitProbeScoreLeadingSpace(t *testing.T) {
	t.Parallel()
	commitProbeBadScore(t, " 1")
}

func TestCommitProbeScoreTrailingSpace(t *testing.T) {
	t.Parallel()
	commitProbeBadScore(t, "1 ")
}

func TestCommitProbeScoreUnderflow(t *testing.T) {
	t.Parallel()
	commitProbeBadScore(t, "1e-400")
}

// The callback below exists only in this test's private Redis library. It
// supplies descriptors to the production planner/validator/executor; it does
// not duplicate their rules. The first command is always valid and would
// create a key if a later descriptor escaped preflight.
const commitDescriptorProbeSource = `
redis.register_function('ns_tset_descriptor_probe', function(keys,args)
  local S=NS.tset
  local which=args[1]
  local ctx=S.context({space='probe:',epoch='0',entries={}},'step')
  ctx.active_epoch='0'; ctx.write_epoch='0'; ctx.now_ms='0'
  local table_plan={commands={},changed=0,guarded=0,changed_per_entry=S.array()}
  local log_plan={commands={},first_seq='0',last_seq='0',line_count=0}
  -- These trusted-composition probes isolate S.prepare's command ceiling from
  -- S.writecmd's earlier staging ceiling. No accepted large plan is committed.
  local count=string.match(which,'^prepare_commands_(%d+)$')
  if count then
    local key='probe:count-key'
    local descriptor={argv={'HSET',key,'f','v'},
      access={{key=key,kind='hash',mode='write'}}}
    for i=1,tonumber(count) do table_plan.commands[i]=descriptor end
    local plan,err=S.prepare(ctx,table_plan,log_plan,{})
    if err then return S.json.encode(err) end
    return S.json.encode({status='ok',prepared=#plan.commands,
      planned_argv_bytes=ctx.budget.planned_argv_bytes,late_argv_count=#descriptor.argv})
  end
  local early='probe:early:'..which
  local d,err=S.command(ctx,'HSET',early,'hash',{'field','value'})
  if err then return S.json.encode(err) end
  table_plan.commands[1]=d
  local key='probe:late:'..which
  local command,kind,fields
  if which=='unsupported' then command='INCR'; kind='string'; fields={}
  elseif which=='hset_short' then command='HSET'; kind='hash'; fields={'field'}
  elseif which=='hdel_empty' then command='HDEL'; kind='hash'; fields={}
  elseif which=='hdel_oversized' then
    command='HDEL'; kind='hash'; fields={}
    for i=1,1001 do fields[i]='field'..i end
  elseif which=='zadd_bad_score' then command='ZADD'; kind='zset'; fields={' 1','member'}
  elseif which=='zrem_empty' then command='ZREM'; kind='zset'; fields={}
  elseif which=='rpush_empty' then command='RPUSH'; kind='list'; fields={}
  elseif which=='xadd_bad_id' then command='XADD'; kind='stream'; fields={'bogus','field','value'}
  elseif which=='set_missing' then command='SET'; kind='string'; fields={}
  elseif which=='alias' then command='ZADD'; kind='zset'; key=early; fields={'1','member'}
  elseif which=='valid_hset' then command='HSET'; kind='hash'; fields={'field','value'}
  elseif which=='valid_hdel' then command='HDEL'; kind='hash'; fields={'field'}
  elseif which=='valid_zadd' then command='ZADD'; kind='zset'; fields={'1','member'}
  elseif which=='valid_zrem' then command='ZREM'; kind='zset'; fields={'member'}
  elseif which=='valid_rpush' then command='RPUSH'; kind='list'; fields={'value'}
  elseif which=='valid_xadd' then command='XADD'; kind='stream'; fields={'1-0','field','value'}
  elseif which=='valid_set' then command='SET'; kind='string'; fields={'value'}
  elseif string.sub(which,1,8)=='prepare_' then
    local operation,collection,size=string.match(which,'^prepare_([a-z]+)_([a-z]+)_(%d+)$')
    size=tonumber(size)
    if collection=='pairs' and (operation=='hset' or operation=='zadd') then
      command=string.upper(operation);kind=operation=='hset' and 'hash' or 'zset';fields={}
      for i=1,size do
        fields[#fields+1]=operation=='zadd' and '1' or 'field'
        fields[#fields+1]=operation=='zadd' and 'member' or 'value'
      end
    elseif collection=='fields' and operation=='hdel' then
      command='HDEL';kind='hash';fields={}
      for i=1,size do fields[i]='field' end
    elseif collection=='members' and operation=='zrem' then
      command='ZREM';kind='zset';fields={}
      for i=1,size do fields[i]='member' end
    elseif collection=='items' and operation=='rpush' then
      command='RPUSH';kind='list';fields={}
      for i=1,size do fields[i]='value' end
    elseif collection=='pairs' and operation=='xadd' then
      command='XADD';kind='stream';fields={'1-0'}
      for i=1,size do fields[#fields+1]='field';fields[#fields+1]='value' end
    elseif operation=='argv' and collection=='count' then
      command='HDEL';kind='hash';fields={}
      for i=1,size-2 do fields[i]='field' end
    else return S.json.encode(S.refuse('REQUEST')) end
    local argv={command,key}
    for _,v in ipairs(fields) do argv[#argv+1]=v end
    -- Raw descriptor construction is intentional: this checks prepare's
    -- independent validation of descriptors supplied by trusted planners.
    log_plan.commands[1]={argv=argv,access={{key=key,kind=kind,mode='write'}}}
    local plan,err=S.prepare(ctx,table_plan,log_plan,{})
    if err then return S.json.encode(err) end
    return S.json.encode({status='ok',prepared=#plan.commands,
      planned_argv_bytes=ctx.budget.planned_argv_bytes,late_argv_count=#argv})
  else return S.json.encode(S.refuse('REQUEST')) end
  d,err=S.command(ctx,command,key,kind,fields)
  if err then return S.json.encode(err) end
  log_plan.commands[1]=d
  local plan;plan,err=S.prepare(ctx,table_plan,log_plan,{})
  if err then return S.json.encode(err) end
  return S.commit(plan)
end)
`

func commitDescriptorServer(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t), MaxRetries: -1, ReadTimeout: 30 * time.Second})
	t.Cleanup(func() { _ = c.Close() })
	source := tsetTestSourceWithProbe(t, fn.TSetStandalone, commitDescriptorProbeSource, "ns_tset_descriptor_probe")
	if err := c.FunctionLoad(context.Background(), source).Err(); err != nil {
		t.Fatalf("load descriptor probe library: %v", err)
	}
	tsetRequireProbeLoaded(t, c, "ns_tset_descriptor_probe")
	return c
}

type commitDescriptorReply struct {
	Status           string `json:"status"`
	Code             string `json:"code"`
	Prepared         int    `json:"prepared"`
	PlannedArgvBytes int    `json:"planned_argv_bytes"`
	LateArgvCount    int    `json:"late_argv_count"`
	Detail           struct {
		Budget string `json:"budget"`
		Actual int    `json:"actual"`
		Limit  int    `json:"limit"`
	} `json:"detail"`
}

func commitDescriptorCall(t *testing.T, c *redis.Client, which string) commitDescriptorReply {
	t.Helper()
	value, err := c.FCall(context.Background(), "ns_tset_descriptor_probe", []string{}, which).Result()
	if err != nil {
		t.Fatalf("descriptor FCALL %s: %v", which, err)
	}
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("descriptor FCALL %s returned %T", which, value)
	}
	var reply commitDescriptorReply
	if err := json.Unmarshal([]byte(encoded), &reply); err != nil {
		t.Fatalf("descriptor FCALL %s replied %q: %v", which, encoded, err)
	}
	return reply
}

func TestCommitCommandRegistry(t *testing.T) {
	t.Parallel()
	c := commitDescriptorServer(t)
	refusals := []struct{ name, code string }{
		{"unsupported", "REQUEST"},
		{"hset_short", "REQUEST"},
		{"hdel_empty", "REQUEST"},
		{"hdel_oversized", "LIMIT"},
		{"zadd_bad_score", "REQUEST"},
		{"zrem_empty", "REQUEST"},
		{"rpush_empty", "REQUEST"},
		{"xadd_bad_id", "LOGID"},
		{"set_missing", "REQUEST"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			before := commitProbeImage(t, c)
			reply := commitDescriptorCall(t, c, tc.name)
			if reply.Status != "refused" || reply.Code != tc.code {
				t.Errorf("want refused %s; got status=%q code=%q", tc.code, reply.Status, reply.Code)
			}
			if after := commitProbeImage(t, c); !reflect.DeepEqual(before, after) {
				t.Error("late descriptor refusal changed the whole store")
			}
			commitProbeNoKeys(t, c, []string{"probe:early:" + tc.name, "probe:late:" + tc.name})
		})
	}
	for _, name := range []string{"valid_hset", "valid_hdel", "valid_zadd", "valid_zrem", "valid_rpush", "valid_xadd", "valid_set"} {
		t.Run(name, func(t *testing.T) {
			reply := commitDescriptorCall(t, c, name)
			if reply.Status != "ok" {
				t.Fatalf("valid descriptor %s refused: %s", name, reply.Code)
			}
			if n, err := c.Exists(context.Background(), "probe:early:"+name).Result(); err != nil || n != 1 {
				t.Fatalf("accepted descriptor did not commit its earlier command: exists=%d err=%v", n, err)
			}
		})
	}
	// These cases call S.prepare through a test-only trusted-composition
	// callback. An accepted result proves validation of the complete plan; it
	// does not execute 65,536 writes or measure their latency.
	for _, tc := range []struct {
		name          string
		prepared      int
		lateArgvCount int
		code          string
		budget        string
		actual        int
		limit         int
	}{
		{name: "prepare_commands_65535", prepared: 65535, lateArgvCount: 4},
		{name: "prepare_commands_65536", prepared: 65536, lateArgvCount: 4},
		{name: "prepare_commands_65537", code: "LIMIT", budget: "commands", actual: 65537, limit: 65536},
		{name: "prepare_hset_pairs_1000", prepared: 2, lateArgvCount: 2002},
		{name: "prepare_hset_pairs_1001", code: "LIMIT", budget: "argv", actual: 2004, limit: 2002},
		{name: "prepare_zadd_pairs_1000", prepared: 2, lateArgvCount: 2002},
		{name: "prepare_zadd_pairs_1001", code: "LIMIT", budget: "argv", actual: 2004, limit: 2002},
		{name: "prepare_hdel_fields_1000", prepared: 2, lateArgvCount: 1002},
		{name: "prepare_hdel_fields_1001", code: "LIMIT"},
		{name: "prepare_zrem_members_1000", prepared: 2, lateArgvCount: 1002},
		{name: "prepare_zrem_members_1001", code: "LIMIT"},
		{name: "prepare_rpush_items_1000", prepared: 2, lateArgvCount: 1002},
		{name: "prepare_rpush_items_1001", code: "LIMIT"},
		{name: "prepare_xadd_pairs_999", prepared: 2, lateArgvCount: 2001},
		{name: "prepare_xadd_pairs_1000", code: "LIMIT", budget: "argv", actual: 2003, limit: 2002},
		{name: "prepare_argv_count_2003", code: "LIMIT", budget: "argv", actual: 2003, limit: 2002},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prospective := []string{"probe:early:" + tc.name, "probe:late:" + tc.name}
			if strings.HasPrefix(tc.name, "prepare_commands_") {
				prospective = []string{"probe:count-key"}
			}
			commitProbeNoKeys(t, c, prospective)
			before := commitProbeImage(t, c)
			reply := commitDescriptorCall(t, c, tc.name)
			if tc.code != "" {
				if reply.Status != "refused" || reply.Code != tc.code {
					t.Errorf("prepare-only late descriptor: want refused %s; got status=%q code=%q", tc.code, reply.Status, reply.Code)
				}
				if tc.budget != "" && (reply.Detail.Budget != tc.budget || reply.Detail.Actual != tc.actual || reply.Detail.Limit != tc.limit) {
					t.Errorf("prepare-only limit detail: want %s %d/%d; got %+v", tc.budget, tc.actual, tc.limit, reply.Detail)
				}
			} else if reply.Status != "ok" || reply.Prepared != tc.prepared || reply.LateArgvCount != tc.lateArgvCount || reply.PlannedArgvBytes <= 0 || reply.PlannedArgvBytes > 8*1024*1024 {
				t.Errorf("prepare-only descriptor: want %d validated commands below argv byte ceiling; got %+v", tc.prepared, reply)
			}
			if after := commitProbeImage(t, c); !reflect.DeepEqual(before, after) {
				t.Error("descriptor preparation changed the whole store")
			}
			commitProbeNoKeys(t, c, prospective)
		})
	}
}

func TestPlannedKeyTypeAliases(t *testing.T) {
	t.Parallel()
	c := commitDescriptorServer(t)
	before := commitProbeImage(t, c)
	reply := commitDescriptorCall(t, c, "alias")
	if reply.Status != "refused" || reply.Code != "WRONGTYPE" {
		t.Errorf("cross-plan hash then zset alias: want WRONGTYPE; got status=%q code=%q", reply.Status, reply.Code)
	}
	if after := commitProbeImage(t, c); !reflect.DeepEqual(before, after) {
		t.Error("cross-plan key type alias changed the whole store")
	}
	commitProbeNoKeys(t, c, []string{"probe:early:alias"})
}
