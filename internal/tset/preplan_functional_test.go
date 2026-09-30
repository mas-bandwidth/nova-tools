//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// This callback is appended only to each test's private FUNCTION LOAD source.
// It exercises the public composition seam with an enclosing Lua planner; it
// does not add an upper-layer callback to the production library.
const preplanProbeLua = `
redis.register_function('ns_tset_preplan_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local mode=args[3]
  local ctx,err=S.open(args[1],args[2])
  if err then return S.json.encode(err) end
  if ctx.replay then return S.json.encode(S.refuse('REQUEST')) end
  local aliases_before=ctx.notes==ctx.request.notes
  local function counters()
    return {field=ctx.budget.field,record=ctx.budget.record,
      cell=ctx.budget.cell,store_commands=ctx.budget.store_commands,
      fetched_bytes=ctx.budget.fetched_bytes}
  end
  local first,second,third,first_count,second_count,third_count
  local before_new
  if mode=='cache' then
    first,err=S.before(ctx,'work',{'existing'},{'state'})
    if err then return S.json.encode(err) end
    first_count=counters()
    second,err=S.before(ctx,'work',{'existing'},{'state','label'})
    if err then return S.json.encode(err) end
    second_count=counters()
    third,err=S.before(ctx,'work',{'existing'},{'state','label'})
    if err then return S.json.encode(err) end
    third_count=counters()
    ctx.request.entries[#ctx.request.entries+1]=S.json.decode(
      '{"kind":"guard","t":"work","from":"r:c","ids":["existing"],"before_fields":["state","label"]}')
  elseif mode=='append' or mode=='malformed' or mode=='unnamed_note' then
    first,err=S.before(ctx,'work',{'existing'},{'state'})
    if err then return S.json.encode(err) end
    before_new,err=S.before(ctx,'aux',{'new'},{'state'})
    if err then return S.json.encode(err) end
    ctx.request.entries[#ctx.request.entries+1]=S.json.decode(
      '{"kind":"rows","t":"aux","add":["fresh"]}')
    ctx.request.entries[#ctx.request.entries+1]=S.json.decode(
      '{"kind":"create","t":"aux","to":"fresh:c","ids":["new"],"scores":["1"],"set":{"state":"created"},"about":["primary-new"]}')
    if mode=='malformed' then
      -- The earlier appended entries would write if the whole request were
      -- not revalidated after preplanning. This third entry has no scores.
      ctx.request.entries[#ctx.request.entries+1]=S.json.decode(
        '{"kind":"create","t":"work","to":"r:c","ids":["bad"],"about":["primary-bad"]}')
    else
      ctx.request.entries[#ctx.request.entries+1]=S.json.decode(
        '{"kind":"move","t":"work","from":"r:c","ids":["existing"],"scores":["2"],"set":{"state":"after"},"about":["primary-old"]}')
      ctx.notes[#ctx.notes+1]=S.json.decode(
        '{"line":{"kind":"note","meta":{"reason":"preplan"}},"about":["primary-old"]}')
    end
  elseif string.sub(mode,1,6)=='alias_' then
    if mode=='alias_append' or mode=='alias_overflow' or mode=='alias_invalid' then
      local count=mode=='alias_overflow' and 101 or 1
      for i=1,count do
        local note=S.json.decode('{"line":{"kind":"note","meta":{}},"about":[]}')
        if mode=='alias_invalid' then note.line.kind='invalid' end
        ctx.notes[#ctx.notes+1]=note
      end
    elseif mode=='alias_replace_ctx' then ctx.notes=S.array()
    elseif mode=='alias_replace_request' then ctx.request.notes=S.array()
    else return S.json.encode(S.refuse('REQUEST')) end
  elseif mode=='boundary' then
    -- The raw request is just below 4 MiB. Its implicit empty notes array
    -- exists for the preplanner but is omitted only from the size check.
  elseif mode=='combined_bytes' then
    first,err=S.before(ctx,'work',{'existing'},{'state'})
    if err then return S.json.encode(err) end
    local added=S.json.decode(
      '{"kind":"create","t":"work","to":"r:c","ids":["large"],"scores":["2"],"about":["primary-large"],"set":{}}')
    for i=1,64 do added.set['f'..i]=string.rep('x',65536) end
    ctx.request.entries[#ctx.request.entries+1]=added
  elseif string.sub(mode,1,9)=='identity_' then
    first,err=S.before(ctx,'work',{'existing'},{'state'})
    if err then return S.json.encode(err) end
    ctx.request.entries[#ctx.request.entries+1]=S.json.decode(
      '{"kind":"rows","t":"work","add":["identity-added"]}')
    local field=string.sub(mode,10)
    if field=='space' then ctx.request.space=ctx.space..'changed'
    elseif field=='epoch' then ctx.request.epoch='1'
    elseif field=='op' then ctx.request.op='changed-op'
    elseif field=='intent' then ctx.request.intent='changed-intent'
    elseif field=='result' then ctx.request.result='changed-result'
    elseif field=='introduce' then
      ctx.request.op='introduced-op'
      ctx.request.intent='introduced-intent'
    else return S.json.encode(S.refuse('REQUEST')) end
  elseif string.sub(mode,1,8)=='advance_' then
    first,err=S.before(ctx,'work',{'existing'},{'state'})
    if err then return S.json.encode(err) end
    if mode=='advance_append' then
      ctx.request.entries[#ctx.request.entries+1]=S.json.decode('{"kind":"advance","from":"0"}')
    elseif mode=='advance_remove' then
      ctx.request.entries=S.array()
    elseif mode=='advance_alter' then
      ctx.request.entries[1].from='1'
    else return S.json.encode(S.refuse('REQUEST')) end
  else
    return S.json.encode(S.refuse('REQUEST'))
  end
  local original_bytes=#ctx.raw_request
  local effective_bytes=#S.json.encode(ctx.request)
  local aliases_after=ctx.notes==ctx.request.notes
  local loaded=0
  for _ in pairs(ctx.defs) do loaded=loaded+1 end
  local table_plan;table_plan,err=S.plan(ctx)
  if err then return S.json.encode({status='refused',code=err.code,
    phase='plan',loaded_defs=loaded,original_bytes=original_bytes,
    effective_bytes=effective_bytes,budget=err.detail.budget,
    actual=err.detail.actual,limit=err.detail.limit,
    aliases_before=aliases_before,aliases_after=aliases_after,
    note_count=#ctx.notes,
    first=first and first.existing or cjson.null,
    before_new=before_new and before_new.new or cjson.null}) end
  local planned_count=counters()
  if mode=='alias_append' or mode=='boundary' then
    return S.json.encode({status='planned',original_bytes=original_bytes,
      effective_bytes=effective_bytes,aliases_before=aliases_before,
      aliases_after=aliases_after,note_count=#ctx.notes,
      input_notes=ctx.budget.input_notes,planned_commands=#table_plan.commands})
  end
  local log_plan={commands={},first_seq='0',last_seq='0',line_count=0,about_appends=0}
  if ctx.profile~='l1_only' then
    if not NS.tlog then return S.json.encode(S.refuse('REQUEST')) end
    log_plan,err=NS.tlog.plan(ctx,table_plan)
    if err then return S.json.encode(err) end
  end
  local commit;commit,err=S.prepare(ctx,table_plan,log_plan,{})
  if err then return S.json.encode(err) end
  local committed=S.commit(commit)
  return S.json.encode({status='probe',reply=S.json.decode(committed),
    first=first and first.existing or cjson.null,
    second=second and second.existing or cjson.null,
    third=third and third.existing or cjson.null,
    before_new=before_new and before_new.new or cjson.null,
    first_count=first_count,second_count=second_count,third_count=third_count,
    planned_count=planned_count,loaded_defs=loaded,rows=table_plan.rows,
    note_count=#ctx.notes,aliases_before=aliases_before,aliases_after=aliases_after})
end)
`

type preplanProbeCounter struct {
	Field         int `json:"field"`
	Record        int `json:"record"`
	Cell          int `json:"cell"`
	StoreCommands int `json:"store_commands"`
	FetchedBytes  int `json:"fetched_bytes"`
}

type preplanBeforeRecord struct {
	Exists bool `json:"exists"`
	Fields map[string]struct {
		Present bool   `json:"present"`
		Value   string `json:"value"`
	} `json:"fields"`
}

type preplanProbeReply struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Phase  string `json:"phase"`
	Reply  struct {
		Status          string `json:"status"`
		Changed         int    `json:"changed"`
		Guarded         int    `json:"guarded"`
		Lines           int    `json:"lines"`
		ChangedPerEntry []int  `json:"changed_per_entry"`
	} `json:"reply"`
	First     preplanBeforeRecord `json:"first"`
	Second    preplanBeforeRecord `json:"second"`
	Third     preplanBeforeRecord `json:"third"`
	BeforeNew struct {
		Exists bool `json:"exists"`
	} `json:"before_new"`
	FirstCount      preplanProbeCounter `json:"first_count"`
	SecondCount     preplanProbeCounter `json:"second_count"`
	ThirdCount      preplanProbeCounter `json:"third_count"`
	PlannedCount    preplanProbeCounter `json:"planned_count"`
	LoadedDefs      int                 `json:"loaded_defs"`
	OriginalBytes   int                 `json:"original_bytes"`
	EffectiveBytes  int                 `json:"effective_bytes"`
	Budget          string              `json:"budget"`
	Actual          int                 `json:"actual"`
	Limit           int                 `json:"limit"`
	AliasesBefore   bool                `json:"aliases_before"`
	AliasesAfter    bool                `json:"aliases_after"`
	InputNotes      int                 `json:"input_notes"`
	PlannedCommands int                 `json:"planned_commands"`
	Rows            []struct {
		Table string `json:"table"`
		Added []struct {
			Row  string `json:"row"`
			Rank string `json:"rank"`
		} `json:"added"`
	} `json:"rows"`
	NoteCount int `json:"note_count"`
}

func preplanProbeCall(t *testing.T, fx *tsetFixture, mode string) preplanProbeReply {
	t.Helper()
	raw := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[]}`, fx.Space)
	return preplanProbeCallRaw(t, fx, mode, raw)
}

func preplanProbeCallRaw(t *testing.T, fx *tsetFixture, mode, raw string) preplanProbeReply {
	t.Helper()
	value, err := fx.Client.FCall(context.Background(), "ns_tset_preplan_probe", []string{}, Version, raw, mode).Result()
	if err != nil {
		t.Fatalf("preplan FCALL: %v", err)
	}
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("preplan FCALL returned %T, want JSON bulk string", value)
	}
	var reply preplanProbeReply
	if err := json.Unmarshal([]byte(encoded), &reply); err != nil {
		t.Fatalf("preplan reply %q: %v", encoded, err)
	}
	return reply
}

func seedPreplanMember(t *testing.T, fx *tsetFixture) {
	t.Helper()
	raw := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"create","t":"work","to":"r:c","ids":["existing"],"scores":["1"],"set":{"state":"old","label":"seed"},"about":["primary-old"]}]}`, fx.Space)
	value, err := fx.Step(raw)
	if err != nil {
		t.Fatalf("seed member FCALL: %v", err)
	}
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("seed member FCALL returned %T", value)
	}
	var reply struct{ Status, Code string }
	if err := json.Unmarshal([]byte(encoded), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Status != "ok" {
		t.Fatalf("seed member: %s", encoded)
	}
}

func TestPreplanBeforeCacheReuse(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.AddRow(t, "work", "r", 0)
	fx.ActivateWithLua(t, preplanProbeLua)
	seedPreplanMember(t, fx)
	before := commitProbeImage(t, fx.Client)
	reply := preplanProbeCall(t, fx, "cache")
	if reply.Status != "probe" || reply.Reply.Status != "ok" {
		t.Fatalf("preplan cache reply status=%q code=%q commit=%q", reply.Status, reply.Code, reply.Reply.Status)
	}
	if !reply.First.Exists || !reply.First.Fields["state"].Present || reply.First.Fields["state"].Value != "old" {
		t.Errorf("first before observation = %+v", reply.First)
	}
	if !reply.Second.Fields["label"].Present || reply.Second.Fields["label"].Value != "seed" ||
		!reflect.DeepEqual(reply.Second, reply.Third) {
		t.Errorf("expanded/repeated before observations second=%+v third=%+v", reply.Second, reply.Third)
	}
	if reply.LoadedDefs != 1 || reply.FirstCount.Record != 1 || reply.FirstCount.Field != 1 ||
		reply.SecondCount.Record != 1 || reply.SecondCount.Field != 2 {
		t.Errorf("preplan counters first=%+v second=%+v defs=%d", reply.FirstCount, reply.SecondCount, reply.LoadedDefs)
	}
	if reply.ThirdCount != reply.SecondCount {
		t.Errorf("identical second projection reread store: second=%+v third=%+v", reply.SecondCount, reply.ThirdCount)
	}
	// S.before observed the record, its indicated source cell and the row
	// score. The subsequent guard plan needs no additional Redis read at all.
	if reply.PlannedCount != reply.ThirdCount {
		t.Errorf("member plan reread cached record/cell/row: third=%+v planned=%+v", reply.ThirdCount, reply.PlannedCount)
	}
	if reply.Reply.Changed != 0 || reply.Reply.Guarded != 1 || !reflect.DeepEqual(reply.Reply.ChangedPerEntry, []int{0}) {
		t.Errorf("guard result = %+v", reply.Reply)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("cache-only preplan changed the store")
	}
}

func TestPreplanAppendedEntriesAndNotes(t *testing.T) {
	t.Parallel()
	t.Run("composed rows members and note", func(t *testing.T) {
		t.Parallel()
		fx := newComposedTSetFixture(t)
		fx.Define(t, "work", "c")
		fx.Define(t, "aux", "c")
		fx.AddRow(t, "work", "r", 0)
		fx.ActivateWithLua(t, preplanProbeLua)
		seedPreplanMember(t, fx)
		raw := fmt.Sprintf(`{"epoch":"0","space":%q,"op":"preplan-op","intent":"append-note","entries":[]}`, fx.Space)
		reply := preplanProbeCallRaw(t, fx, "append", raw)
		if reply.Status != "probe" || reply.Reply.Status != "ok" {
			t.Fatalf("preplan append reply status=%q code=%q commit=%q", reply.Status, reply.Code, reply.Reply.Status)
		}
		if !reply.First.Exists || reply.First.Fields["state"].Value != "old" || reply.BeforeNew.Exists || reply.LoadedDefs != 2 {
			t.Errorf("before state/lazy definitions: first=%+v new=%+v defs=%d", reply.First, reply.BeforeNew, reply.LoadedDefs)
		}
		if reply.Reply.Changed != 2 || reply.Reply.Lines != 4 || reply.NoteCount != 1 ||
			!reflect.DeepEqual(reply.Reply.ChangedPerEntry, []int{0, 1, 1}) {
			t.Errorf("composed append result = %+v, notes=%d", reply.Reply, reply.NoteCount)
		}
		if len(reply.Rows) != 1 || reply.Rows[0].Table != "aux" || len(reply.Rows[0].Added) != 1 ||
			reply.Rows[0].Added[0].Row != "fresh" || reply.Rows[0].Added[0].Rank != "0" {
			t.Errorf("normalized row event = %+v", reply.Rows)
		}
		ctx := context.Background()
		if score, err := fx.Client.ZScore(ctx, fixtureRowsKey(fx.Space, "aux", "0"), "fresh").Result(); err != nil || score != 0 {
			t.Errorf("appended row rank = %v, err=%v", score, err)
		}
		if score, err := fx.Client.ZScore(ctx, fixtureCellKey(fx.Space, "aux", "0", "fresh", "c"), "new").Result(); err != nil || score != 1 {
			t.Errorf("appended member score = %v, err=%v", score, err)
		}
		if state, err := fx.Client.HGet(ctx, fixtureRecordKey(fx.Space, "work", "existing"), "state").Result(); err != nil || state != "after" {
			t.Errorf("appended move state = %q, err=%v", state, err)
		}
		lines, err := fx.Client.XRange(ctx, fixtureLogKey(fx.Space, "0"), "-", "+").Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != 5 { // one seed create, then four preplan events
			t.Errorf("log lines = %d, want seed plus four preplan events", len(lines))
		}
		var noteSeen, rowSeen bool
		for i, line := range lines {
			encoded, ok := line.Values["d"].(string)
			if !ok {
				t.Fatalf("log line %d has no JSON body: %+v", i, line.Values)
			}
			var body struct {
				Kind  string `json:"k"`
				Table string `json:"tbl"`
				Add   []struct {
					Row  string `json:"row"`
					Rank string `json:"rank"`
				} `json:"add"`
				About []string          `json:"about"`
				Meta  map[string]string `json:"meta"`
			}
			if err := json.Unmarshal([]byte(encoded), &body); err != nil {
				t.Fatalf("log line %d body %q: %v", i, encoded, err)
			}
			if body.Kind == "n" {
				noteSeen = true
				if i != len(lines)-1 || !reflect.DeepEqual(body.About, []string{"primary-old"}) ||
					body.Meta["reason"] != "preplan" {
					t.Errorf("appended note out of order or incomplete: index=%d body=%+v", i, body)
				}
			}
			if body.Kind == "w" {
				rowSeen = true
				if i != 1 || body.Table != "aux" || len(body.Add) != 1 ||
					body.Add[0].Row != "fresh" || body.Add[0].Rank != "0" {
					t.Errorf("appended row event out of order or incomplete: index=%d body=%+v", i, body)
				}
			}
		}
		if !noteSeen {
			t.Error("composed log lacks appended note")
		}
		if !rowSeen {
			t.Error("composed log lacks appended row event")
		}
	})

	t.Run("malformed append is atomic", func(t *testing.T) {
		t.Parallel()
		fx := newComposedTSetFixture(t)
		fx.Define(t, "work", "c")
		fx.Define(t, "aux", "c")
		fx.AddRow(t, "work", "r", 0)
		fx.ActivateWithLua(t, preplanProbeLua)
		seedPreplanMember(t, fx)
		before := commitProbeImage(t, fx.Client)
		reply := preplanProbeCall(t, fx, "malformed")
		if reply.Status != "refused" || reply.Code != "REQUEST" || reply.Phase != "plan" ||
			!reply.First.Exists || reply.BeforeNew.Exists || reply.LoadedDefs != 2 {
			t.Fatalf("malformed append did not refuse after preplan: %+v", reply)
		}
		if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
			t.Error("malformed appended request changed the store")
		}
	})
}

func TestPreplanDerivedNoteWithoutOpPlans(t *testing.T) {
	t.Parallel()
	fx := newPreplanNoteIdentityFixture(t)
	before := commitProbeImage(t, fx.Client)
	raw := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[]}`, fx.Space)
	reply := preplanNoteIdentityCall(t, fx, raw, "valid")
	if reply.Status != "planned" || reply.Code != "" || reply.OriginalCount != 0 ||
		!reply.Implicit || !reply.Alias || reply.NoteCount != 1 || reply.InputNotes != 1 {
		t.Fatalf("op-less step did not plan its real-state-derived note: %+v", reply)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("plan-only derived note changed the store")
	}
}

func TestPreplanNotesAlias(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code string
		notes      int
		aliasAfter bool
	}{
		{name: "append", notes: 1, aliasAfter: true},
		{name: "overflow", code: "LIMIT", notes: 101, aliasAfter: true},
		{name: "invalid", code: "REQUEST", notes: 1, aliasAfter: true},
		{name: "replace_ctx", code: "REQUEST", aliasAfter: false},
		{name: "replace_request", code: "REQUEST", aliasAfter: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixture(t)
			fx.ActivateWithLua(t, preplanProbeLua)
			before := commitProbeImage(t, fx.Client)
			raw := fmt.Sprintf(`{"epoch":"0","space":%q,"op":"alias-op","intent":"alias-check","entries":[]}`, fx.Space)
			reply := preplanProbeCallRaw(t, fx, "alias_"+tc.name, raw)
			if !reply.AliasesBefore || reply.AliasesAfter != tc.aliasAfter || reply.NoteCount != tc.notes {
				t.Errorf("%s alias/count observation = %+v", tc.name, reply)
			}
			if tc.code == "" {
				if reply.Status != "planned" || reply.InputNotes != 1 || reply.PlannedCommands != 0 {
					t.Errorf("in-place derived note was not planned: %+v", reply)
				}
			} else if reply.Status != "refused" || reply.Code != tc.code || reply.Phase != "plan" {
				t.Errorf("%s note mutation refusal = %+v, want %s", tc.name, reply, tc.code)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Errorf("%s note probe changed the store", tc.name)
			}
		})
	}
}

func TestPreplanImplicitEmptyNotesByteBoundary(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.AddRow(t, "work", "r", 0)
	fx.ActivateWithLua(t, preplanProbeLua)
	raw := preplanBoundaryRequest(t, fx.Space)
	before := commitProbeImage(t, fx.Client)
	reply := preplanProbeCallRaw(t, fx, "boundary", raw)
	if reply.Status != "planned" || !reply.AliasesBefore || !reply.AliasesAfter ||
		reply.NoteCount != 0 || reply.InputNotes != 0 || reply.OriginalBytes != 4194300 ||
		reply.EffectiveBytes <= 4194304 || reply.PlannedCommands == 0 {
		t.Fatalf("implicit empty notes changed the byte-boundary plan: %+v", reply)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("byte-boundary plan-only probe changed the store")
	}
}

func preplanBoundaryRequest(t *testing.T, space string) string {
	t.Helper()
	const target = 4194300
	fields := make(map[string]string, 64)
	for i := 0; i < 64; i++ {
		fields[fmt.Sprintf("f%02d", i)] = strings.Repeat("x", 65500)
	}
	request := map[string]any{
		"epoch": "0", "space": space,
		"entries": []any{map[string]any{
			"kind": "create", "t": "work", "to": "r:c",
			"ids": []string{"large"}, "scores": []string{"1"}, "set": fields,
		}},
	}
	encode := func() []byte {
		t.Helper()
		b, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	delta := target - len(encode())
	if delta < 0 || delta > 64*36 {
		t.Fatalf("boundary fixture cannot distribute %d bytes across 64 fields", delta)
	}
	for i := 0; i < 64 && delta > 0; i++ {
		n := delta
		if n > 36 {
			n = 36
		}
		key := fmt.Sprintf("f%02d", i)
		fields[key] += strings.Repeat("x", n)
		delta -= n
	}
	raw := encode()
	if len(raw) != target {
		t.Fatalf("boundary request bytes = %d, want %d", len(raw), target)
	}
	return string(raw)
}

func TestPreplanCombinedByteLimit(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.AddRow(t, "work", "r", 0)
	fx.ActivateWithLua(t, preplanProbeLua)
	seedPreplanMember(t, fx)
	before := commitProbeImage(t, fx.Client)
	reply := preplanProbeCall(t, fx, "combined_bytes")
	if reply.Status != "refused" || reply.Code != "LIMIT" || reply.Phase != "plan" ||
		!reply.First.Exists || reply.First.Fields["state"].Value != "old" ||
		reply.OriginalBytes >= 4194304 || reply.EffectiveBytes <= 4194304 ||
		reply.Budget != "request_bytes" || reply.Actual <= 4194304 ||
		reply.Actual >= reply.EffectiveBytes || reply.Limit != 4194304 {
		t.Fatalf("combined request byte cap did not refuse after preplan: %+v", reply)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("over-limit derived request changed the store")
	}
}

func TestPreplanIdentityImmutable(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"space", "epoch", "op", "intent", "result", "introduce"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixture(t)
			fx.Define(t, "work", "c")
			fx.AddRow(t, "work", "r", 0)
			fx.ActivateWithLua(t, preplanProbeLua)
			seedPreplanMember(t, fx)
			before := commitProbeImage(t, fx.Client)
			raw := fmt.Sprintf(`{"epoch":"0","space":%q,"op":"identity-op","intent":"identity-intent","result":"identity-result","entries":[]}`, fx.Space)
			if field == "introduce" {
				raw = fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[]}`, fx.Space)
			}
			reply := preplanProbeCallRaw(t, fx, "identity_"+field, raw)
			if reply.Status != "refused" || reply.Code != "REQUEST" || reply.Phase != "plan" ||
				!reply.First.Exists || reply.First.Fields["state"].Value != "old" || reply.LoadedDefs != 1 {
				t.Fatalf("%s identity mutation did not refuse after preplan: %+v", field, reply)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Errorf("%s identity mutation changed the store", field)
			}
		})
	}
}

func TestPreplanCannotIntroduceAdvance(t *testing.T) {
	t.Parallel()
	for _, mutation := range []string{"append", "remove", "alter"} {
		t.Run(mutation, func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixture(t)
			fx.Define(t, "work", "c")
			fx.AddRow(t, "work", "r", 0)
			fx.ActivateWithLua(t, preplanProbeLua)
			seedPreplanMember(t, fx)
			before := commitProbeImage(t, fx.Client)
			entries := `[]`
			if mutation != "append" {
				entries = `[{"kind":"advance","from":"0"}]`
			}
			raw := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":%s}`, fx.Space, entries)
			reply := preplanProbeCallRaw(t, fx, "advance_"+mutation, raw)
			if reply.Status != "refused" || reply.Code != "REQUEST" || reply.Phase != "plan" ||
				!reply.First.Exists || reply.First.Fields["state"].Value != "old" || reply.LoadedDefs != 1 {
				t.Fatalf("%s advance mutation did not refuse after preplan: %+v", mutation, reply)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Errorf("%s advance mutation changed the store", mutation)
			}
		})
	}
}
