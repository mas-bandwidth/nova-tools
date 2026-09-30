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

// callbackKeyConstructorProbeLua registers a probe function to test key-constructor
// authority gap sealing across all ten initialized constructors.
const callbackKeyConstructorProbeLua = `
redis.register_function('ns_tset_callback_key_probe', function(keys, args)
  local S = NS.tset
  if #keys ~= 0 or #args ~= 3 then return S.json.encode(S.refuse('ARGS')) end
  local mode = args[3]
  local spy_called = 0
  local function make_spy(target_fn)
    return function(...)
      spy_called = spy_called + 1
      return target_fn(...)
    end
  end
  local extension = {kinds = {'keyprobe'}}
  extension.validate = function(q, index)
    if not S.is_object(q) or q.kind ~= 'keyprobe' then
      return nil, S.refuse('REQUEST', {query_index = index})
    end
    for k in pairs(q) do
      if k ~= 'kind' and k ~= 'mode' then
        return nil, S.refuse('REQUEST', {query_index = index})
      end
    end
    return true, nil
  end
  extension.read = function(ctx, q, index)
    local m = q.mode or mode
    if m == 'normal' then
      return {kind = 'keyprobe', ok = true}, nil
    elseif m == 'forged_props_key' then
      ctx.props_key = make_spy(ctx.props_key)
    elseif m == 'forged_table_prefix' then
      ctx.table_prefix = make_spy(ctx.table_prefix)
    elseif m == 'forged_record_key' then
      ctx.record_key = make_spy(ctx.record_key)
    elseif m == 'forged_table_key' then
      ctx.table_key = make_spy(ctx.table_key)
    elseif m == 'forged_rows_key' then
      ctx.rows_key = make_spy(ctx.rows_key)
    elseif m == 'forged_cell_key' then
      ctx.cell_key = make_spy(ctx.cell_key)
    elseif m == 'forged_definition_key' then
      ctx.definition_key = make_spy(ctx.definition_key)
    elseif m == 'forged_log_key' then
      ctx.log_key = make_spy(ctx.log_key)
    elseif m == 'forged_history_key' then
      ctx.history_key = make_spy(ctx.history_key)
    elseif m == 'forged_done_key' then
      ctx.done_key = make_spy(ctx.done_key)
    end
    return {kind = 'keyprobe', ok = true}, nil
  end
  local ok, res = pcall(S.read, args[1], args[2], nil, extension)
  if not ok then error(res, 0) end
  return S.json.encode({reply = S.json.decode(res), spy_called = spy_called})
end)
`

type callbackKeyProbeResult struct {
	Reply struct {
		Status string `json:"status"`
		Code   string `json:"code"`
		Detail struct {
			QueryIndex *int `json:"query_index"`
		} `json:"detail"`
		Answers []json.RawMessage `json:"answers"`
	} `json:"reply"`
	SpyCalled int `json:"spy_called"`
}

func execCallbackKeyProbe(t *testing.T, fx *tsetFixture, raw, mode string) callbackKeyProbeResult {
	t.Helper()
	res, err := fx.Client.FCall(context.Background(), "ns_tset_callback_key_probe",
		nil, Version, raw, mode).Text()
	if err != nil {
		t.Fatalf("ns_tset_callback_key_probe FCall failed: %v", err)
	}
	var out callbackKeyProbeResult
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatalf("decode probe output %q: %v", res, err)
	}
	return out
}

// TestCallbackKeyConstructorSealing verifies that all ten initialized constructor identities
// are sealed against extension replacement. Any forged helper attempt must be refused with
// CONFIG at query 0, the replacement must never be invoked, whole store image must remain
// unchanged, and subsequent normal queries must recover cleanly.
func TestCallbackKeyConstructorSealing(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	ctx := context.Background()

	// Seed properties and data for verification.
	if err := fx.Client.HSet(ctx, fx.Space+"table:work:props", "title", "initial-title").Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.ZAdd(ctx, fixtureRowsKey(fx.Space, "work", "0"),
		redis.Z{Score: 0, Member: "r"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "c"),
		redis.Z{Score: 1, Member: "card"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.HSet(ctx, fixtureRecordKey(fx.Space, "work", "card"),
		"epoch", "0", "revision", "1", "state", "ready").Err(); err != nil {
		t.Fatal(err)
	}

	fx.ActivateWithLua(t, callbackKeyConstructorProbeLua)

	constructors := []string{
		"forged_props_key",
		"forged_table_prefix",
		"forged_record_key",
		"forged_table_key",
		"forged_rows_key",
		"forged_cell_key",
		"forged_definition_key",
		"forged_log_key",
		"forged_history_key",
		"forged_done_key",
	}

	for _, mode := range constructors {
		t.Run(mode, func(t *testing.T) {
			beforeImage := commitProbeImage(t, fx.Client)

			raw := readExtensionRaw(fx.Space, "0", "atomic",
				fmt.Sprintf(`[{"kind":"keyprobe","mode":%q}]`, mode))
			res := execCallbackKeyProbe(t, fx, raw, mode)

			// 1. Refused with CONFIG on query 0.
			if res.Reply.Status != "refused" || res.Reply.Code != "CONFIG" {
				t.Fatalf("%s: expected status=refused code=CONFIG, got status=%q code=%q",
					mode, res.Reply.Status, res.Reply.Code)
			}
			if res.Reply.Detail.QueryIndex == nil || *res.Reply.Detail.QueryIndex != 0 {
				t.Fatalf("%s: expected query_index=0, got %v", mode, res.Reply.Detail.QueryIndex)
			}

			// 2. No partial answers.
			if len(res.Reply.Answers) != 0 {
				t.Fatalf("%s: expected 0 answers, got %d", mode, len(res.Reply.Answers))
			}

			// 3. No replacement invocation.
			if res.SpyCalled != 0 {
				t.Fatalf("%s: replacement was invoked %d times, want 0", mode, res.SpyCalled)
			}

			// 4. Whole store image unchanged.
			afterImage := commitProbeImage(t, fx.Client)
			if !reflect.DeepEqual(beforeImage, afterImage) {
				t.Fatalf("%s: store image mutated during refused forged constructor attempt", mode)
			}

			// 5. Clean recovery with a normal control query.
			rawNormal := readExtensionRaw(fx.Space, "0", "atomic", `[{"kind":"keyprobe","mode":"normal"}]`)
			normalRes := execCallbackKeyProbe(t, fx, rawNormal, "normal")
			if normalRes.Reply.Status != "read" || len(normalRes.Reply.Answers) != 1 {
				t.Fatalf("%s: recovery failed: %+v", mode, normalRes.Reply)
			}
			if normalRes.SpyCalled != 0 {
				t.Fatalf("%s: spy was called during normal recovery: %d", mode, normalRes.SpyCalled)
			}
		})
	}
}

// TestCallbackKeyConstructorComposedTrailingReaders tests composed trailing readers
// where query 0 attempts to forge a helper and subsequent queries invoke built-in readers
// (props, rows, range). The composite call must refuse at query 0 with CONFIG, return no
// partial answers, never invoke the replacement, leave the store unchanged, and recover.
func TestCallbackKeyConstructorComposedTrailingReaders(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	ctx := context.Background()

	if err := fx.Client.HSet(ctx, fx.Space+"table:work:props", "k1", "v1").Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.ZAdd(ctx, fixtureRowsKey(fx.Space, "work", "0"),
		redis.Z{Score: 0, Member: "r"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "c"),
		redis.Z{Score: 1, Member: "card"}).Err(); err != nil {
		t.Fatal(err)
	}

	fx.ActivateWithLua(t, callbackKeyConstructorProbeLua)

	cases := []struct {
		name         string
		forgedMode   string
		trailingJSON string
	}{
		{
			name:         "trailing_props",
			forgedMode:   "forged_props_key",
			trailingJSON: `{"kind":"props","t":"work"}`,
		},
		{
			name:         "trailing_rows",
			forgedMode:   "forged_rows_key",
			trailingJSON: `{"kind":"rows","t":"work"}`,
		},
		{
			name:         "trailing_range",
			forgedMode:   "forged_cell_key",
			trailingJSON: `{"kind":"range","t":"work","cell":"r:c","min":"-inf","max":"+inf","limit":10}`,
		},
		{
			name:         "trailing_props_via_table_prefix",
			forgedMode:   "forged_table_prefix",
			trailingJSON: `{"kind":"props","t":"work"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beforeImage := commitProbeImage(t, fx.Client)

			queries := fmt.Sprintf(`[{"kind":"keyprobe","mode":%q},%s]`, tc.forgedMode, tc.trailingJSON)
			raw := readExtensionRaw(fx.Space, "0", "atomic", queries)
			res := execCallbackKeyProbe(t, fx, raw, tc.forgedMode)

			// Query 0 CONFIG refusal.
			if res.Reply.Status != "refused" || res.Reply.Code != "CONFIG" {
				t.Fatalf("%s: expected status=refused code=CONFIG, got status=%q code=%q",
					tc.name, res.Reply.Status, res.Reply.Code)
			}
			if res.Reply.Detail.QueryIndex == nil || *res.Reply.Detail.QueryIndex != 0 {
				t.Fatalf("%s: expected query_index=0, got %v", tc.name, res.Reply.Detail.QueryIndex)
			}

			// No partial answers.
			if len(res.Reply.Answers) != 0 {
				t.Fatalf("%s: expected 0 answers, got %d", tc.name, len(res.Reply.Answers))
			}

			// No replacement invocation.
			if res.SpyCalled != 0 {
				t.Fatalf("%s: spy was invoked %d times, want 0", tc.name, res.SpyCalled)
			}

			// Unchanged whole store image.
			afterImage := commitProbeImage(t, fx.Client)
			if !reflect.DeepEqual(beforeImage, afterImage) {
				t.Fatalf("%s: store image mutated", tc.name)
			}

			// Clean recovery with normal control.
			normalQueries := fmt.Sprintf(`[{"kind":"keyprobe","mode":"normal"},%s]`, tc.trailingJSON)
			rawNormal := readExtensionRaw(fx.Space, "0", "atomic", normalQueries)
			normalRes := execCallbackKeyProbe(t, fx, rawNormal, "normal")
			if normalRes.Reply.Status != "read" || len(normalRes.Reply.Answers) != 2 {
				t.Fatalf("%s: normal control failed: status=%q answers=%d",
					tc.name, normalRes.Reply.Status, len(normalRes.Reply.Answers))
			}
			if normalRes.SpyCalled != 0 {
				t.Fatalf("%s: normal control invoked spy: %d", tc.name, normalRes.SpyCalled)
			}
		})
	}
}
