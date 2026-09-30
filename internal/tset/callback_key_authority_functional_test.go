//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// A test-only extension changes a key constructor, then returns. The next
// built-in query must never invoke that replacement outside the callback.
const callbackKeyAuthorityLua = `
redis.register_function('ns_tset_callback_key_authority',function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local mode=args[3]
  if mode~='normal' and mode~='props_key' and mode~='table_prefix' and
      mode~='done_key' and mode~='log_key' and mode~='history_key' then
    return S.json.encode(S.refuse('ARGS'))
  end
  local calls=0
  local extension={kinds={'keycheck'}}
  extension.validate=function(q,index)
    if q.kind~='keycheck' then return nil,S.refuse('REQUEST',{query_index=index}) end
    for key in pairs(q) do
      if key~='kind' then return nil,S.refuse('REQUEST',{query_index=index}) end
    end
    return true,nil
  end
  extension.read=function(ctx,q,index)
    if mode~='normal' then
      local original=ctx[mode]
      if type(original)~='function' then return nil,S.refuse('DRIFT',{query_index=index}) end
      ctx[mode]=function(...)
        calls=calls+1
        if mode=='props_key' then return ctx.space..'table:other:props' end
        if mode=='table_prefix' then return ctx.space..'table:other' end
        return original(...)..':redirected'
      end
    end
    return {kind='keycheck'},nil
  end
  local log_reader=NS.tlog and NS.tlog.read or nil
  local wire=S.read(args[1],args[2],log_reader,extension)
  return S.json.encode({reply=S.json.decode(wire),replacement_calls=calls})
end)
`

func TestCallbackKeyConstructorsCannotRedirectLaterReads(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode    string
		profile fn.TSetProfile
		query   ReadQuery
	}{
		{"props_key", fn.TSetStandalone, ReadQuery{Kind: "props", Table: "work", Names: []string{"marker"}}},
		{"table_prefix", fn.TSetStandalone, ReadQuery{Kind: "props", Table: "work", Names: []string{"marker"}}},
		{"done_key", fn.TSetStandalone, ReadQuery{Kind: "done", Ops: []DoneIdentity{{Epoch: "0", Op: "seed", IntentDigest: intentDigest("seed properties")}}}},
		{"log_key", fn.TSetComposed, ReadQuery{Kind: "last"}},
		{"history_key", fn.TSetComposed, ReadQuery{Kind: "cardlines", Abouts: []string{"card"}, Fields: []string{}, Limit: 1}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			if tc.profile == fn.TSetComposed {
				requireTSetLogFragment(t)
			}
			fx := newTSetFixtureProfile(t, tc.profile)
			fx.Define(t, "work", "ready")
			fx.Define(t, "other", "ready")
			fx.ActivateWithLua(t, callbackKeyAuthorityLua)
			store := newFixtureRedis(t, fx.Client)
			work, other := "intended", "other-table"
			op, intent := "seed", "seed properties"
			seeded, err := store.Step(context.Background(), Step{Epoch: "0", Space: fx.Space, Op: &op, Intent: &intent, Entries: []Entry{
				{Kind: "prop", Table: "work", Name: "marker", Value: &work},
				{Kind: "prop", Table: "other", Name: "marker", Value: &other},
			}})
			if err != nil || seeded.Status != "ok" || seeded.Changed != 2 {
				t.Fatalf("seed properties: %+v/%v", seeded, err)
			}
			query, err := json.Marshal(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"keycheck"},`+string(query)+`]`)
			before := commitProbeImage(t, fx.Client)
			call := func(mode string) (callbackBoundaryResult, int) {
				t.Helper()
				wire, err := fx.Client.FCall(context.Background(), "ns_tset_callback_key_authority", nil, Version, raw, mode).Text()
				if err != nil {
					t.Fatal(err)
				}
				var got callbackBoundaryResult
				var counter struct {
					Calls int `json:"replacement_calls"`
				}
				if err := json.Unmarshal([]byte(wire), &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(wire), &counter); err != nil {
					t.Fatal(err)
				}
				return got, counter.Calls
			}
			control, calls := call("normal")
			if control.Reply.Status != "read" || len(control.Reply.Answers) != 2 || control.Reply.Answers[1].Kind != tc.query.Kind || calls != 0 {
				t.Fatalf("normal extension and built-in control: %+v calls=%d", control, calls)
			}
			got, calls := call(tc.mode)
			if got.Reply.Status != "refused" || got.Reply.Code != "CONFIG" || len(got.Reply.Answers) != 0 ||
				got.Reply.Detail.QueryIndex == nil || *got.Reply.Detail.QueryIndex != 0 || calls != 0 {
				t.Errorf("constructor replacement escaped callback: reply=%+v replacement_calls=%d", got.Reply, calls)
			}
			recovered, err := store.Read(context.Background(), ReadPlan{Epoch: "0", Space: fx.Space, Mode: "atomic", Queries: []ReadQuery{tc.query}})
			if err != nil || recovered.Status != "read" || len(recovered.Answers) != 1 || recovered.Answers[0].Kind != tc.query.Kind {
				t.Fatalf("ordinary built-in recovery: %+v/%v", recovered, err)
			}
			if tc.query.Kind == "props" && !reflect.DeepEqual(recovered.Answers[0].Props, map[string]string{"marker": "intended"}) {
				t.Fatalf("property recovery read another table: %+v", recovered.Answers[0])
			}
			if tc.query.Kind == "done" && (len(recovered.Answers[0].Done) != 1 || recovered.Answers[0].Done[0].Status != "match") {
				t.Fatalf("receipt recovery lost named operation: %+v", recovered.Answers[0])
			}
			if !reflect.DeepEqual(before, commitProbeImage(t, fx.Client)) {
				t.Fatal("callback or recovery changed whole Redis image")
			}
		})
	}
}
