//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// A test-only extension replaces ctx.props_key (or other key constructors)
// during an extension read callback, then returns normally.
// On unsealed code, downstream built-in queries invoke the replacement without
// tripping the callback guard, allowing redirecting lookups to a different hash.
// On sealed code, tampering with the key constructor triggers CONFIG refusal.
const propsKeyAuthorityGapLua = `
redis.register_function('ns_tset_props_key_authority_gap',function(keys,args)
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

const (
	propsKeyGapProbeName   = "ns_tset_props_key_authority_gap"
	unsealedFieldsTail     = "'original_advance_from'}"
	sealedFieldsList       = "'record_key','table_key','table_prefix','rows_key','props_key','cell_key','definition_key','log_key','history_key','done_key'"
	sealedFieldsTail       = "'original_advance_from'," + sealedFieldsList + "}"
	sealedFieldsLuaBlock   = "'original_advance_from',\n    'record_key','table_key','table_prefix','rows_key','props_key','cell_key','definition_key',\n    'log_key','history_key','done_key'}"
	isolatedPropsKeyCall   = "local key = props_key(q.t, private_request_epoch)"
	unisolatedPropsKeyCall = "local key = ctx.props_key(q.t, ctx.request_epoch)"
)

type propsKeyGapResult struct {
	ReplacementCalls int `json:"replacement_calls"`
	Reply            struct {
		Status string `json:"status"`
		Code   string `json:"code"`
		Detail struct {
			QueryIndex *int `json:"query_index"`
		} `json:"detail"`
		Answers []struct {
			Kind  string            `json:"kind"`
			Props map[string]string `json:"props"`
			Done  []struct {
				Status string `json:"status"`
			} `json:"done"`
		} `json:"answers"`
	} `json:"reply"`
}

func activatePropsKeyGapWithLua(t *testing.T, fx *tsetFixture, extraSource string, sealed bool) {
	t.Helper()
	fx.mustInitialize(t)
	if fx.loaded {
		t.Fatal("fixture test callback must be loaded before any public row step")
	}
	name := tsetTestProbeName(t, extraSource)
	source := tsetTestSourceWithProbe(t, fx.profile, extraSource, name)
	if sealed {
		isSealed := (strings.Contains(source, sealedFieldsList) || strings.Contains(source, sealedFieldsLuaBlock))
		if !isSealed {
			if strings.Count(source, unsealedFieldsTail) != 1 {
				t.Fatal("unsealedFieldsTail not found exactly once in tset source")
			}
			source = strings.Replace(source, unsealedFieldsTail, sealedFieldsTail, 1)
		}
	} else {
		if strings.Contains(source, sealedFieldsLuaBlock) {
			source = strings.Replace(source, sealedFieldsLuaBlock, unsealedFieldsTail, 1)
		} else if strings.Contains(source, sealedFieldsTail) {
			source = strings.Replace(source, sealedFieldsTail, unsealedFieldsTail, 1)
		}
		if strings.Contains(source, isolatedPropsKeyCall) {
			source = strings.Replace(source, isolatedPropsKeyCall, unisolatedPropsKeyCall, 1)
		}
	}
	if err := fx.Client.FunctionLoad(context.Background(), source).Err(); err != nil {
		t.Fatalf("load tset with test callback (sealed=%v): %v", sealed, err)
	}
	tsetRequireProbeLoaded(t, fx.Client, name)
	fx.loaded = true
	fx.flushPendingRows(t)
	fx.active = true
}

func callPropsKeyGapProbe(t *testing.T, fx *tsetFixture, raw, mode string) propsKeyGapResult {
	t.Helper()
	wire, err := fx.Client.FCall(context.Background(), propsKeyGapProbeName, nil, Version, raw, mode).Text()
	if err != nil {
		t.Fatalf("fcall %s mode %s: %v", propsKeyGapProbeName, mode, err)
	}
	var res propsKeyGapResult
	if err := json.Unmarshal([]byte(wire), &res); err != nil {
		t.Fatalf("unmarshal propsKeyGapResult: %v (raw: %s)", err, wire)
	}
	return res
}

// TestPropsKeyCallbackAuthorityGap reproduces the property-era callback authority
// gap identified in note stella-f3de87ec39bb:
//
// 1. UnsealedGapExploit demonstrates that on unpatched code, ctx.props_key can be
// replaced by an extension read callback. The callback returns normally without
// tripping the callback boundary guard, and the downstream built-in query invokes
// the forged function, redirecting property lookup to a different table.
//
// 2. SealedAuthorityGuard demonstrates that sealing ctx.props_key (and companion
// key constructors) in sealed_fields prevents this gap: tampering triggers CONFIG
// refusal at query 0, replacement is never invoked, clean recovery is preserved,
// normal controls pass, and the whole Redis store image remains unchanged.
func TestPropsKeyCallbackAuthorityGap(t *testing.T) {
	t.Parallel()

	t.Run("UnsealedGapExploit", func(t *testing.T) {
		t.Parallel()

		t.Run("props_key", func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixtureProfile(t, fn.TSetStandalone)
			fx.Define(t, "work", "ready")
			fx.Define(t, "other", "ready")
			activatePropsKeyGapWithLua(t, fx, propsKeyAuthorityGapLua, false /* unsealed */)
			store := newFixtureRedis(t, fx.Client)

			workVal, otherVal := "intended-work", "forged-other"
			op, intent := "seed", "seed properties"
			seeded, err := store.Step(context.Background(), Step{
				Epoch: "0", Space: fx.Space, Op: &op, Intent: &intent,
				Entries: []Entry{
					{Kind: "prop", Table: "work", Name: "marker", Value: &workVal},
					{Kind: "prop", Table: "other", Name: "marker", Value: &otherVal},
				},
			})
			if err != nil || seeded.Status != "ok" || seeded.Changed != 2 {
				t.Fatalf("seed properties: %+v/%v", seeded, err)
			}

			queryJSON, err := json.Marshal(ReadQuery{Kind: "props", Table: "work", Names: []string{"marker"}})
			if err != nil {
				t.Fatal(err)
			}
			raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"keycheck"},`+string(queryJSON)+`]`)

			// Execute probe with props_key tampering on unsealed code.
			res := callPropsKeyGapProbe(t, fx, raw, "props_key")

			// RUNTIME PROOF OF GAP:
			// 1. Guard did NOT trip: reply status is "read", not refused.
			if res.Reply.Status != "read" {
				t.Fatalf("unsealed callback guard tripped unexpectedly: reply=%+v", res.Reply)
			}
			if len(res.Reply.Answers) != 2 {
				t.Fatalf("expected 2 answers, got %d: %+v", len(res.Reply.Answers), res.Reply.Answers)
			}
			// 2. Downstream query invoked the replacement function!
			if res.ReplacementCalls != 1 {
				t.Fatalf("expected replacement to be called 1 time by downstream query, got %d", res.ReplacementCalls)
			}
			// 3. Stolen property from table 'other' returned instead of table 'work'!
			stolenVal := res.Reply.Answers[1].Props["marker"]
			if stolenVal != "forged-other" {
				t.Fatalf("expected stolen property %q from other table, got %q", "forged-other", stolenVal)
			}
			t.Logf("CONFIRMED GAP (props_key): unsealed code allowed extension to replace ctx.props_key; downstream built-in query executed replacement (%d calls) and read table 'other' property (%q) for query on table 'work'",
				res.ReplacementCalls, stolenVal)
		})

		t.Run("table_prefix", func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixtureProfile(t, fn.TSetStandalone)
			fx.Define(t, "work", "ready")
			fx.Define(t, "other", "ready")
			activatePropsKeyGapWithLua(t, fx, propsKeyAuthorityGapLua, false /* unsealed */)
			store := newFixtureRedis(t, fx.Client)

			workVal, otherVal := "intended-work", "forged-other"
			op, intent := "seed", "seed properties"
			seeded, err := store.Step(context.Background(), Step{
				Epoch: "0", Space: fx.Space, Op: &op, Intent: &intent,
				Entries: []Entry{
					{Kind: "prop", Table: "work", Name: "marker", Value: &workVal},
					{Kind: "prop", Table: "other", Name: "marker", Value: &otherVal},
				},
			})
			if err != nil || seeded.Status != "ok" || seeded.Changed != 2 {
				t.Fatalf("seed properties: %+v/%v", seeded, err)
			}

			queryJSON, err := json.Marshal(ReadQuery{Kind: "props", Table: "work", Names: []string{"marker"}})
			if err != nil {
				t.Fatal(err)
			}
			raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"keycheck"},`+string(queryJSON)+`]`)

			// Execute probe with table_prefix tampering on unsealed code.
			res := callPropsKeyGapProbe(t, fx, raw, "table_prefix")

			// RUNTIME PROOF OF GAP:
			// 1. Guard did NOT trip: reply status is "read", not refused.
			if res.Reply.Status != "read" {
				t.Fatalf("unsealed callback guard tripped unexpectedly: reply=%+v", res.Reply)
			}
			if len(res.Reply.Answers) != 2 {
				t.Fatalf("expected 2 answers, got %d: %+v", len(res.Reply.Answers), res.Reply.Answers)
			}
			// 2. Downstream query invoked the replacement function via transitive props_key!
			if res.ReplacementCalls != 1 {
				t.Fatalf("expected replacement to be called 1 time by downstream query, got %d", res.ReplacementCalls)
			}
			// 3. Stolen property from table 'other' returned instead of table 'work'!
			stolenVal := res.Reply.Answers[1].Props["marker"]
			if stolenVal != "forged-other" {
				t.Fatalf("expected stolen property %q from other table, got %q", "forged-other", stolenVal)
			}
			t.Logf("CONFIRMED GAP (table_prefix): unsealed code allowed extension to replace ctx.table_prefix; downstream built-in query executed replacement (%d calls) and read table 'other' property (%q) for query on table 'work'",
				res.ReplacementCalls, stolenVal)
		})

		t.Run("done_key", func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixtureProfile(t, fn.TSetStandalone)
			fx.Define(t, "work", "ready")
			fx.Define(t, "other", "ready")
			activatePropsKeyGapWithLua(t, fx, propsKeyAuthorityGapLua, false /* unsealed */)
			store := newFixtureRedis(t, fx.Client)

			workVal := "intended-work"
			op, intent := "seed", "seed properties"
			seeded, err := store.Step(context.Background(), Step{
				Epoch: "0", Space: fx.Space, Op: &op, Intent: &intent,
				Entries: []Entry{
					{Kind: "prop", Table: "work", Name: "marker", Value: &workVal},
				},
			})
			if err != nil || seeded.Status != "ok" || seeded.Changed != 1 {
				t.Fatalf("seed properties: %+v/%v", seeded, err)
			}

			queryJSON, err := json.Marshal(ReadQuery{Kind: "done", Ops: []DoneIdentity{{Epoch: "0", Op: "seed", IntentDigest: intentDigest("seed properties")}}})
			if err != nil {
				t.Fatal(err)
			}
			raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"keycheck"},`+string(queryJSON)+`]`)

			// Execute probe with done_key tampering on unsealed code.
			res := callPropsKeyGapProbe(t, fx, raw, "done_key")

			// Guard did not trip, downstream query invoked replacement done_key.
			if res.Reply.Status != "read" {
				t.Fatalf("unsealed callback guard tripped unexpectedly: reply=%+v", res.Reply)
			}
			if res.ReplacementCalls != 1 {
				t.Fatalf("expected replacement to be called 1 time by downstream query, got %d", res.ReplacementCalls)
			}
			t.Logf("CONFIRMED GAP (done_key): unsealed code allowed extension to replace ctx.done_key; downstream built-in query executed replacement (%d calls)",
				res.ReplacementCalls)
		})
	})

	t.Run("SealedAuthorityGuard", func(t *testing.T) {
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
				activatePropsKeyGapWithLua(t, fx, propsKeyAuthorityGapLua, true /* sealed */)
				store := newFixtureRedis(t, fx.Client)

				workVal, otherVal := "intended-work", "forged-other"
				op, intent := "seed", "seed properties"
				seeded, err := store.Step(context.Background(), Step{
					Epoch: "0", Space: fx.Space, Op: &op, Intent: &intent,
					Entries: []Entry{
						{Kind: "prop", Table: "work", Name: "marker", Value: &workVal},
						{Kind: "prop", Table: "other", Name: "marker", Value: &otherVal},
					},
				})
				if err != nil || seeded.Status != "ok" || seeded.Changed != 2 {
					t.Fatalf("seed properties: %+v/%v", seeded, err)
				}

				queryJSON, err := json.Marshal(tc.query)
				if err != nil {
					t.Fatal(err)
				}
				raw := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"keycheck"},`+string(queryJSON)+`]`)
				before := commitProbeImage(t, fx.Client)

				// Normal control call: without tampering, returns normally.
				control := callPropsKeyGapProbe(t, fx, raw, "normal")
				if control.Reply.Status != "read" || len(control.Reply.Answers) != 2 ||
					control.Reply.Answers[1].Kind != tc.query.Kind || control.ReplacementCalls != 0 {
					t.Fatalf("normal control failed: reply=%+v calls=%d", control.Reply, control.ReplacementCalls)
				}

				// Tampered call: tampering with sealed constructor must return CONFIG refusal at query 0.
				got := callPropsKeyGapProbe(t, fx, raw, tc.mode)
				if got.Reply.Status != "refused" || got.Reply.Code != "CONFIG" ||
					got.Reply.Detail.QueryIndex == nil || *got.Reply.Detail.QueryIndex != 0 ||
					len(got.Reply.Answers) != 0 || got.ReplacementCalls != 0 {
					t.Fatalf("sealed constructor replacement escaped guard: reply=%+v calls=%d", got.Reply, got.ReplacementCalls)
				}

				// Clean recovery: ordinary built-in read succeeds with original intended values.
				recovered, err := store.Read(context.Background(), ReadPlan{
					Epoch: "0", Space: fx.Space, Mode: "atomic", Queries: []ReadQuery{tc.query},
				})
				if err != nil || recovered.Status != "read" || len(recovered.Answers) != 1 || recovered.Answers[0].Kind != tc.query.Kind {
					t.Fatalf("recovery read failed: reply=%+v err=%v", recovered, err)
				}
				if tc.query.Kind == "props" && !reflect.DeepEqual(recovered.Answers[0].Props, map[string]string{"marker": "intended-work"}) {
					t.Fatalf("property recovery returned corrupted or cross-table data: %+v", recovered.Answers[0])
				}
				if tc.query.Kind == "done" && (len(recovered.Answers[0].Done) != 1 || recovered.Answers[0].Done[0].Status != "match") {
					t.Fatalf("receipt recovery lost named operation: %+v", recovered.Answers[0])
				}

				// Unchanged whole store image.
				if !reflect.DeepEqual(before, commitProbeImage(t, fx.Client)) {
					t.Fatal("tampered call or recovery modified whole Redis store image")
				}
			})
		}
	})
}
