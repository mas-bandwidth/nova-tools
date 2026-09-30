//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/redis/go-redis/v9"
)

// This private L1 callback derives notes from an actual member observation.
// The ordinary acceptance mode runs the complete L1 path through prepare and
// commit with an empty log plan; it does not claim a real L2 note line.
const preplanNoteIdentityProbeLua = `
redis.register_function('ns_tset_note_identity_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local mode=args[3]
  local open_reads=0
  local original=S.readcmd
  S.readcmd=function(...)
    open_reads=open_reads+1
    return original(...)
  end
  local ctx,err=S.open(args[1],args[2])
  S.readcmd=original
  if err then return S.json.encode({status=err.status,code=err.code,open_reads=open_reads}) end
  if ctx.replay then return S.json.encode({status='unexpected_replay'}) end
  if mode=='open_only' then return S.json.encode({status='opened',open_reads=open_reads}) end
  local original_count=ctx.original_note_count
  local implicit=ctx.notes_implicit
  local observed
  if mode~='fence_append' then
    observed,err=S.before(ctx,'work',{'p'},{'state'})
    if err then return S.json.encode(err) end
    if not observed.p or not observed.p.exists or
        not observed.p.fields.state.present or observed.p.fields.state.value~='live' then
      return S.json.encode({status='wrong_observation'})
    end
  end
  local function append_note(i, about_count, blob)
    local about=S.array()
    for j=1,about_count do about[j]='p' end
    ctx.notes[#ctx.notes+1]={line={kind='note',
      meta={observed=observed.p.fields.state.value,ordinal=i,blob=blob}},about=about}
  end
  if mode=='valid' or mode=='invalid_shape' or mode=='commit_opless' then
    append_note(1,1,'')
    if mode=='invalid_shape' then ctx.notes[1].line.kind='invalid' end
  elseif mode=='notes_100' or mode=='notes_101' then
    local n=mode=='notes_100' and 100 or 101
    for i=1,n do append_note(i,1,'') end
  elseif mode=='about_4000' or mode=='about_4001' then
    for i=1,100 do append_note(i,40+(mode=='about_4001' and i==100 and 1 or 0),'') end
  elseif mode=='distinct_2000' or mode=='distinct_2001' then
    local about=S.array()
    local n=mode=='distinct_2000' and 2000 or 2001
    for i=1,n do about[i]=string.format('id%04d',i) end
    ctx.notes[1]={line={kind='note',meta={observed=observed.p.fields.state.value}},about=about}
  elseif mode=='effective_bytes' then
    for i=1,80 do append_note(i,1,string.rep('x',53000)) end
  elseif mode=='fence_append' then
    ctx.notes[#ctx.notes+1]={line={kind='note',meta={source='forbidden-fence'}},about=S.array()}
  else
    return S.json.encode(S.refuse('REQUEST'))
  end
  local effective=#S.json.encode(ctx.request)
  local before=ctx.budget.store_commands
  local reads=0
  S.readcmd=function(...)
    reads=reads+1
    return original(...)
  end
  local plan,problem
  if mode=='fence_append' then plan,problem=S.fence_prepare(ctx)
  else plan,problem=S.plan(ctx) end
  S.readcmd=original
  local response={status=problem and problem.status or 'planned',
    code=problem and problem.code or cjson.null,
    budget=problem and problem.detail.budget or cjson.null,
    original_count=original_count,implicit=implicit,
    alias=ctx.notes==ctx.request.notes,note_count=#ctx.notes,
    input_notes=ctx.budget.input_notes or -1,
    original_bytes=#ctx.raw_request,effective_bytes=effective,
    plan_reads=reads,plan_charged=ctx.budget.store_commands-before,
    planned_commands=plan and #plan.commands or -1}
  if mode=='commit_opless' and not problem then
    local log_plan={commands={},first_seq='0',last_seq='0',line_count=0,about_appends=0}
    local prepared;prepared,problem=S.prepare(ctx,plan,log_plan,{})
    if problem then
      response.status=problem.status
      response.code=problem.code
      return S.json.encode(response)
    end
    response.committed=S.json.decode(S.commit(prepared))
    response.status='committed'
  end
  return S.json.encode(response)
end)
`

type preplanNoteIdentityReply struct {
	Status          string `json:"status"`
	Code            string `json:"code"`
	Budget          string `json:"budget"`
	OriginalCount   int    `json:"original_count"`
	Implicit        bool   `json:"implicit"`
	Alias           bool   `json:"alias"`
	NoteCount       int    `json:"note_count"`
	InputNotes      int    `json:"input_notes"`
	OriginalBytes   int    `json:"original_bytes"`
	EffectiveBytes  int    `json:"effective_bytes"`
	OpenReads       int    `json:"open_reads"`
	PlanReads       int    `json:"plan_reads"`
	PlanCharged     int    `json:"plan_charged"`
	PlannedCommands int    `json:"planned_commands"`
	Committed       *Reply `json:"committed"`
}

func newPreplanNoteIdentityFixture(t *testing.T) *tsetFixture {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.AddRow(t, "work", "r", 0)
	ctx := context.Background()
	pipe := fx.Client.Pipeline()
	pipe.HSet(ctx, fixtureRecordKey(fx.Space, "work", "p"), map[string]any{
		"epoch": "0", "revision": "1", "place:work": "r:c", "state": "live",
	})
	pipe.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "c"),
		redis.Z{Score: 1, Member: "p"})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed real preplan observation: %v", err)
	}
	fx.ActivateWithLua(t, preplanNoteIdentityProbeLua)
	return fx
}

func preplanNoteIdentityCall(t *testing.T, fx *tsetFixture, raw, mode string) preplanNoteIdentityReply {
	t.Helper()
	value, err := fx.Client.FCall(context.Background(), "ns_tset_note_identity_probe", []string{},
		Version, raw, mode).Result()
	if err != nil {
		t.Fatalf("note-identity FCALL %s: %v", mode, err)
	}
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("note-identity FCALL %s returned %T", mode, value)
	}
	var reply preplanNoteIdentityReply
	if err := json.Unmarshal([]byte(encoded), &reply); err != nil {
		t.Fatalf("decode note-identity %s reply %q: %v", mode, encoded, err)
	}
	return reply
}

func TestPreplanNoteIdentityScope(t *testing.T) {
	t.Parallel()
	fx := newPreplanNoteIdentityFixture(t)
	before := commitProbeImage(t, fx.Client)
	callerNote := `{"line":{"kind":"note","meta":{"source":"caller"}},"about":["p"]}`
	for _, tc := range []struct{ name, raw string }{
		{"ordinary", fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[],"notes":[%s]}`, fx.Space, callerNote)},
		{"fence", fmt.Sprintf(`{"epoch":"0","space":%q,"op":"fence-op","intent":"stable","fence":true,"entries":[],"notes":[%s]}`, fx.Space, callerNote)},
	} {
		t.Run(tc.name+" caller note rejected before store", func(t *testing.T) {
			_, decodeErr := DecodeStep([]byte(tc.raw))
			requireRefusal(t, decodeErr, "REQUEST")
			opened := preplanNoteIdentityCall(t, fx, tc.raw, "open_only")
			if opened.Status != "refused" || opened.Code != "REQUEST" || opened.OpenReads != 0 {
				t.Fatalf("caller note reached store opening: %+v", opened)
			}
			wire, err := fx.Step(tc.raw)
			if err != nil {
				t.Fatalf("public caller-note FCALL: %v", err)
			}
			encoded, ok := wire.(string)
			if !ok {
				t.Fatalf("public caller-note reply type %T", wire)
			}
			var refusal Refusal
			if err := json.Unmarshal([]byte(encoded), &refusal); err != nil {
				t.Fatal(err)
			}
			requireRefusal(t, &refusal, "REQUEST")
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Fatal("caller-supplied note changed the Redis key image")
			}
		})
	}
	for _, tc := range []struct {
		name       string
		mode       string
		wantStatus string
		wantCode   string
		wantNotes  int
		implicit   bool
	}{
		{name: "implicit empty original accepts derived note", mode: "valid", wantStatus: "planned", wantNotes: 1, implicit: true},
		{name: "explicit empty original accepts derived note", mode: "valid", wantStatus: "planned", wantNotes: 1},
		{name: "100 derived notes accepted", mode: "notes_100", wantStatus: "planned", wantNotes: 100, implicit: true},
		{name: "101 derived notes limited", mode: "notes_101", wantStatus: "refused", wantCode: "LIMIT", wantNotes: 101, implicit: true},
		{name: "4000 about occurrences accepted", mode: "about_4000", wantStatus: "planned", wantNotes: 100, implicit: true},
		{name: "4001 about occurrences limited", mode: "about_4001", wantStatus: "refused", wantCode: "LIMIT", wantNotes: 100, implicit: true},
		{name: "2000 distinct about IDs accepted", mode: "distinct_2000", wantStatus: "planned", wantNotes: 1, implicit: true},
		{name: "2001 distinct about IDs limited", mode: "distinct_2001", wantStatus: "refused", wantCode: "LIMIT", wantNotes: 1, implicit: true},
		{name: "invalid derived note shape refused", mode: "invalid_shape", wantStatus: "refused", wantCode: "REQUEST", wantNotes: 1, implicit: true},
		{name: "effective bytes still limited", mode: "effective_bytes", wantStatus: "refused", wantCode: "LIMIT", wantNotes: 80, implicit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[]}`, fx.Space)
			if !tc.implicit {
				raw = fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[],"notes":[]}`, fx.Space)
			}
			reply := preplanNoteIdentityCall(t, fx, raw, tc.mode)
			if reply.Status != tc.wantStatus || reply.Code != tc.wantCode ||
				reply.OriginalCount != 0 || reply.Implicit != tc.implicit || !reply.Alias ||
				reply.NoteCount != tc.wantNotes || reply.OriginalBytes != len(raw) ||
				reply.EffectiveBytes <= reply.OriginalBytes {
				t.Fatalf("derived-note scope/shape %s: %+v", tc.mode, reply)
			}
			if tc.wantStatus == "planned" && reply.InputNotes != tc.wantNotes {
				t.Fatalf("derived notes omitted from effective input count: %+v", reply)
			}
			if tc.wantStatus == "refused" && (reply.PlanReads != 0 || reply.PlanCharged != 0) {
				t.Fatalf("invalid derived notes caused plan store IO: %+v", reply)
			}
			if tc.mode == "effective_bytes" {
				if reply.EffectiveBytes <= MaxWriteRequestBytes || reply.Budget != "request_bytes" {
					t.Fatalf("effective-byte refusal did not enforce %d-byte cap: %+v", MaxWriteRequestBytes, reply)
				}
			} else if reply.EffectiveBytes > MaxWriteRequestBytes {
				t.Fatalf("non-byte-boundary case exceeded request limit: %+v", reply)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Fatalf("derived-note plan %s changed the Redis key image", tc.mode)
			}
		})
	}
	t.Run("fence cannot append a note", func(t *testing.T) {
		raw := fmt.Sprintf(`{"epoch":"0","space":%q,"op":"fence-op","intent":"stable","fence":true,"entries":[]}`, fx.Space)
		reply := preplanNoteIdentityCall(t, fx, raw, "fence_append")
		if reply.Status != "refused" || reply.Code != "REQUEST" || reply.NoteCount != 1 ||
			reply.PlanReads != 0 || reply.PlanCharged != 0 {
			t.Fatalf("fence accepted an appended note: %+v", reply)
		}
		if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
			t.Fatal("rejected fence note changed the Redis key image")
		}
	})
}

func TestPreplanNotesOpLessStepAllowed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		implicit bool
	}{
		{name: "implicit original notes", implicit: true},
		{name: "explicit empty original notes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newPreplanNoteIdentityFixture(t)
			raw := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[]}`, fx.Space)
			if !tc.implicit {
				raw = fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[],"notes":[]}`, fx.Space)
			}
			beforeState := fx.SemanticSnapshot(t)
			beforeImage := commitProbeImage(t, fx.Client)
			reply := preplanNoteIdentityCall(t, fx, raw, "commit_opless")
			if reply.Status != "committed" || reply.Code != "" || reply.OriginalCount != 0 ||
				reply.Implicit != tc.implicit || !reply.Alias || reply.NoteCount != 1 ||
				reply.PlannedCommands != 0 || reply.Committed == nil || reply.Committed.Status != "ok" ||
				reply.Committed.Replay || reply.Committed.Changed != 0 || reply.Committed.Guarded != 0 ||
				reply.Committed.Lines != 0 || reply.Committed.FirstSeq != "0" || reply.Committed.LastSeq != "0" {
				t.Fatalf("op-less derived note did not pass complete L1 path: %+v", reply)
			}
			if after := fx.SemanticSnapshot(t); !reflect.DeepEqual(beforeState, after) {
				t.Fatalf("op-less no-op commit changed table state: before=%#v after=%#v", beforeState, after)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(beforeImage, after) {
				t.Fatal("op-less note-only L1 commit changed the Redis key image")
			}
			commitProbeNoKeys(t, fx.Client, []string{fixtureDoneKey(fx.Space, "0")})
		})
	}
}
