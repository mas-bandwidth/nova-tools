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

// The callback uses the production open/plan seam but never prepares or
// commits. It appends one valid named note after open, then measures the
// composed request exactly as S.plan does.
const preplanBytesBoundaryProbeLua = `
redis.register_function('ns_tset_preplan_bytes_boundary', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=2 then return S.json.encode(S.refuse('ARGS')) end
  local ctx,err=S.open(args[1],args[2])
  if err then return S.json.encode(err) end
  local defs_before=0
  for _ in pairs(ctx.defs) do defs_before=defs_before+1 end
  if ctx.replay or not ctx.notes_implicit or #ctx.notes~=0 or
      ctx.notes~=ctx.request.notes or ctx.request.op==nil or ctx.request.intent==nil then
    return S.json.encode(S.refuse('REQUEST'))
  end
  ctx.notes[#ctx.notes+1]=S.json.decode(
    '{"line":{"kind":"note","meta":{"reason":"preplan-bytes"}},"about":["primary-large"]}')
  local effective=#S.json.encode(ctx.request)
  local function counters()
    return {field=ctx.budget.field,record=ctx.budget.record,cell=ctx.budget.cell,
      store_commands=ctx.budget.store_commands,fetched_bytes=ctx.budget.fetched_bytes}
  end
  local before=counters()
  local plan;plan,err=S.plan(ctx)
  local after=counters()
  local loaded=0
  for _ in pairs(ctx.defs) do loaded=loaded+1 end
  if err then
    local detail=err.detail or {}
    return S.json.encode({status='refused',code=err.code,phase='plan',
      original_bytes=#ctx.raw_request,effective_bytes=effective,notes=#ctx.notes,
      aliases=ctx.notes==ctx.request.notes,defs_before=defs_before,loaded_defs=loaded,before=before,after=after,
      budget=detail.budget,actual=detail.actual,limit=detail.limit})
  end
  return S.json.encode({status='planned',original_bytes=#ctx.raw_request,
    effective_bytes=effective,notes=#ctx.notes,aliases=ctx.notes==ctx.request.notes,
    defs_before=defs_before,loaded_defs=loaded,before=before,after=after,planned_commands=#plan.commands})
end)
`

type preplanBytesBoundaryReply struct {
	Status         string `json:"status"`
	Code           string `json:"code"`
	Phase          string `json:"phase"`
	Budget         string `json:"budget"`
	Actual         int    `json:"actual"`
	Limit          int    `json:"limit"`
	OriginalBytes  int    `json:"original_bytes"`
	EffectiveBytes int    `json:"effective_bytes"`
	Notes          int    `json:"notes"`
	Aliases        bool   `json:"aliases"`
	DefsBefore     int    `json:"defs_before"`
	LoadedDefs     int    `json:"loaded_defs"`
	Before         struct {
		Field         int `json:"field"`
		Record        int `json:"record"`
		Cell          int `json:"cell"`
		StoreCommands int `json:"store_commands"`
		FetchedBytes  int `json:"fetched_bytes"`
	} `json:"before"`
	After struct {
		Field         int `json:"field"`
		Record        int `json:"record"`
		Cell          int `json:"cell"`
		StoreCommands int `json:"store_commands"`
		FetchedBytes  int `json:"fetched_bytes"`
	} `json:"after"`
	PlannedCommands int `json:"planned_commands"`
}

func TestPreplanEffectiveRequestBytesBoundary(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		target int
		refuse bool
	}{
		{name: "limit_minus_one", target: MaxWriteRequestBytes - 1},
		{name: "limit", target: MaxWriteRequestBytes},
		{name: "limit_plus_one", target: MaxWriteRequestBytes + 1, refuse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixture(t)
			fx.Define(t, "work", "c")
			fx.AddRow(t, "work", "r", 0)
			fx.ActivateWithLua(t, preplanBytesBoundaryProbeLua)

			raw := preplanEffectiveBoundaryRequest(t, fx.Space, tc.target)
			if strings.Contains(raw, `"notes"`) {
				t.Fatal("fixture must omit the original notes property")
			}
			if len(raw) >= MaxWriteRequestBytes {
				t.Fatalf("original request is %d bytes; want below the %d-byte cap", len(raw), MaxWriteRequestBytes)
			}
			before := commitProbeImage(t, fx.Client)
			reply := preplanBytesBoundaryCall(t, fx, raw)
			if reply.OriginalBytes != len(raw) || reply.EffectiveBytes != tc.target ||
				reply.Notes != 1 || !reply.Aliases {
				t.Fatalf("preplan sizes/notes: %+v; raw bytes=%d target=%d", reply, len(raw), tc.target)
			}
			if tc.refuse {
				if reply.Status != "refused" || reply.Code != "LIMIT" || reply.Phase != "plan" ||
					reply.Budget != "request_bytes" || reply.Actual != tc.target || reply.Limit != MaxWriteRequestBytes {
					t.Fatalf("effective request over cap: %+v", reply)
				}
				if reply.LoadedDefs != reply.DefsBefore || !reflect.DeepEqual(reply.Before, reply.After) {
					t.Errorf("over-limit preplan reached planner state: defs=%d counters before=%+v after=%+v", reply.LoadedDefs, reply.Before, reply.After)
				}
			} else if reply.Status != "planned" || reply.LoadedDefs != 1 || reply.PlannedCommands == 0 {
				t.Fatalf("effective request at %d bytes should plan without commit: %+v", tc.target, reply)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Error("plan-only boundary probe changed the store")
			}
		})
	}
}

func preplanBytesBoundaryCall(t *testing.T, fx *tsetFixture, raw string) preplanBytesBoundaryReply {
	t.Helper()
	value, err := fx.Client.FCall(context.Background(), "ns_tset_preplan_bytes_boundary", []string{}, Version, raw).Result()
	if err != nil {
		t.Fatalf("preplan byte-boundary FCALL: %v", err)
	}
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("preplan byte-boundary FCALL returned %T, want JSON bulk string", value)
	}
	var reply preplanBytesBoundaryReply
	if err := json.Unmarshal([]byte(encoded), &reply); err != nil {
		t.Fatalf("preplan byte-boundary reply %q: %v", encoded, err)
	}
	return reply
}

func preplanEffectiveBoundaryRequest(t *testing.T, space string, targetEffective int) string {
	t.Helper()

	note := `{"line":{"kind":"note","meta":{"reason":"preplan-bytes"}},"about":["primary-large"]}`
	// The original request omits notes. Once this derived note is appended,
	// cjson's compact encoding adds the notes key, array brackets, and note.
	addedBytes := len(`,"notes":[`) + len(note) + len(`]`)
	targetOriginal := targetEffective - addedBytes
	set := make(map[string]string, 64)
	for i := 0; i < 64; i++ {
		set[fmt.Sprintf("f%02d", i)] = strings.Repeat("x", 65500)
	}
	request := map[string]any{
		"epoch": "0", "space": space, "op": "preplan-bytes", "intent": "effective-request-boundary",
		"entries": []any{map[string]any{
			"kind": "create", "t": "work", "to": "r:c", "ids": []string{"large"},
			"scores": []string{"1"}, "about": []string{"primary-large"}, "set": set,
		}},
	}
	encode := func() []byte {
		t.Helper()
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	fields := request["entries"].([]any)[0].(map[string]any)["set"].(map[string]string)
	delta := targetOriginal - len(encode())
	for i := 0; i < 64 && delta != 0; i++ {
		key := fmt.Sprintf("f%02d", i)
		current := len(fields[key])
		if delta > 0 {
			room := MaxFieldValueBytes - current
			if room > delta {
				room = delta
			}
			fields[key] = strings.Repeat("x", current+room)
			delta -= room
		} else {
			remove := -delta
			if remove > current {
				remove = current
			}
			fields[key] = strings.Repeat("x", current-remove)
			delta += remove
		}
	}
	if delta != 0 {
		t.Fatalf("cannot construct %d-byte original request within field value limits; %d bytes remain", targetOriginal, delta)
	}
	for key, value := range fields {
		if len(value) > MaxFieldValueBytes {
			t.Fatalf("field %q has %d bytes; field cap is %d", key, len(value), MaxFieldValueBytes)
		}
	}
	raw := encode()
	if len(raw) != targetOriginal {
		t.Fatalf("original request size = %d, want %d", len(raw), targetOriginal)
	}
	return string(raw)
}
