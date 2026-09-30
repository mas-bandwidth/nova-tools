//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// This private AL5 query uses the production S.read context and S.done_read
// helper. It does not claim to exercise the built-in done query wrapper:
// unique identities are chosen from the live post-TIME budget inside the
// callback, while the built-in wrapper separately validates exact duplicates.
const luaReadFetchedBoundaryProbe = `
redis.register_function('ns_tset_lua_read_fetched_boundary_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=2 then return S.json.encode(S.refuse('ARGS')) end
  local extension={kinds={'fetchboundary'}}
  extension.validate=function(q,index)
    if not S.is_object(q) or q.kind~='fetchboundary' or
        (q.delta~=-1 and q.delta~=0 and q.delta~=1) then
      return nil,S.refuse('REQUEST',{query_index=index})
    end
    for key in pairs(q) do
      if key~='kind' and key~='delta' then
        return nil,S.refuse('REQUEST',{query_index=index})
      end
    end
    return true,nil
  end
  extension.read=function(ctx,q,index)
    local cap,count,low_bytes=8388608,400,20971
    local baseline=ctx.budget.fetched_bytes
    -- The last low receipt triggers one five-byte HSTRLEN. Choose the number
    -- of high receipts from this invocation's post-TIME baseline, never from
    -- a different call's sampled clock payload.
    local highs=cap+q.delta-baseline-5-count*low_bytes
    if highs<0 or highs>count-1 or highs~=math.floor(highs) then
      return nil,S.refuse('CONFIG',{query_index=index,budget='calibration',
        actual=highs,limit=count-1})
    end
    local before=ctx.budget.metadata_commands or 0
    local ops=S.array()
    local function wrong_digest(i)
      return string.rep('0',37)..string.format('%03x',i)
    end
    for i=1,count-1 do
      local high=i<=highs
      ops[i]={epoch=ctx.request_epoch,op=high and 'high' or 'low',
        intent_digest=wrong_digest(i)}
    end
    ops[count]={epoch=ctx.request_epoch,op='low',intent_digest=wrong_digest(count)}
    local slots,err=S.done_read(ctx,ops)
    local metadata_delta=(ctx.budget.metadata_commands or 0)-before
    if err then
      err.detail=err.detail or {}
      err.detail.metadata_delta=metadata_delta
      err.detail.baseline=baseline
      err.detail.high_count=highs
      return nil,err
    end
    if #slots~=count then return nil,S.refuse('CONFIG',{query_index=index}) end
    for i=1,count do
      if slots[i].status~='conflict' then
        return nil,S.refuse('CONFIG',{query_index=index})
      end
    end
    return {kind='fetchboundary',fetched_bytes=ctx.budget.fetched_bytes,
      metadata_delta=metadata_delta,baseline=baseline,high_count=highs,
      slots=#slots},nil
  end
  return S.read(args[1],args[2],nil,extension)
end)
`

// The saved value is a valid compact receipt. Control characters expand to
// six JSON bytes each, allowing adjacent exact stored byte lengths while its
// decoded result remains below the 4 KiB caller-result limit.
func luaFetchedBoundaryReceipt(t *testing.T, target int) string {
	t.Helper()
	type receipt struct {
		IntentDigest string `json:"intent_digest"`
		Status       string `json:"status"`
		EpochBefore  string `json:"epoch_before"`
		EpochAfter   string `json:"epoch_after"`
		FirstSeq     string `json:"first_seq"`
		LastSeq      string `json:"last_seq"`
		Changed      int    `json:"changed"`
		Result       string `json:"result"`
	}
	r := receipt{IntentDigest: strings.Repeat("1", 40), Status: "ok",
		EpochBefore: "0", EpochAfter: "0", FirstSeq: "0", LastSeq: "0"}
	base, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	additional := target - len(base)
	if additional < 0 {
		t.Fatalf("receipt target %d below fixed metadata size %d", target, len(base))
	}
	r.Result = strings.Repeat("\x01", additional/6) + strings.Repeat("x", additional%6)
	if len(r.Result) > MaxResultBytes {
		t.Fatalf("receipt result has %d decoded bytes; max %d", len(r.Result), MaxResultBytes)
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != target || len(encoded) > MaxReceiptBytes {
		t.Fatalf("stored receipt has %d bytes; want %d (cap %d)", len(encoded), target, MaxReceiptBytes)
	}
	return string(encoded)
}

func TestLuaReadExactFetchedBytesBoundary(t *testing.T) {
	const (
		lowBytes = 20971
		count    = 400
	)
	fx := newTSetFixture(t)
	ctx := context.Background()
	low := luaFetchedBoundaryReceipt(t, lowBytes)
	high := luaFetchedBoundaryReceipt(t, lowBytes+1)
	doneKey := fixtureDoneKey(fx.Space, "0")
	if err := fx.Client.HSet(ctx, doneKey, "low", low, "high", high).Err(); err != nil {
		t.Fatalf("seed valid compact receipts: %v", err)
	}
	for _, saved := range []struct {
		op   string
		want int
	}{{"low", lowBytes}, {"high", lowBytes + 1}} {
		got, err := fx.Client.HGet(ctx, doneKey, saved.op).Bytes()
		if err != nil || len(got) != saved.want {
			t.Fatalf("seeded %s receipt has %d bytes, want %d (err %v)", saved.op, len(got), saved.want, err)
		}
	}
	fx.ActivateWithLua(t, luaReadFetchedBoundaryProbe)

	// The fixture client uses the normal short timeout. This read-only probe
	// decodes 400 full receipts inside one FCALL, so give it a bounded allowance.
	probeClient := redis.NewClient(&redis.Options{Addr: fx.Client.Options().Addr,
		MaxRetries: -1, ReadTimeout: 20 * time.Second})
	t.Cleanup(func() { _ = probeClient.Close() })
	type boundaryReply struct {
		Status   string `json:"status"`
		Code     string `json:"code"`
		Complete bool   `json:"complete"`
		Answers  []struct {
			Kind          string `json:"kind"`
			FetchedBytes  int    `json:"fetched_bytes"`
			MetadataDelta int    `json:"metadata_delta"`
			Baseline      int    `json:"baseline"`
			HighCount     int    `json:"high_count"`
			Slots         int    `json:"slots"`
		} `json:"answers"`
		Counters struct {
			FetchedBytes int `json:"fetched_bytes"`
		} `json:"counters"`
		Detail struct {
			QueryIndex    *int   `json:"query_index"`
			Budget        string `json:"budget"`
			Actual        *int   `json:"actual"`
			Limit         *int   `json:"limit"`
			MetadataDelta int    `json:"metadata_delta"`
			Baseline      int    `json:"baseline"`
			HighCount     int    `json:"high_count"`
		} `json:"detail"`
	}
	for _, tc := range []struct {
		name  string
		delta int
	}{{"limit_minus_one", -1}, {"limit", 0}, {"limit_plus_one", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := json.Marshal(map[string]any{"epoch": "0", "space": fx.Space,
				"mode": "atomic", "queries": []any{map[string]any{
					"kind": "fetchboundary", "delta": tc.delta}}})
			if err != nil {
				t.Fatal(err)
			}
			before := commitProbeImage(t, fx.Client)
			callCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			wire, err := probeClient.FCall(callCtx,
				"ns_tset_lua_read_fetched_boundary_probe", nil, Version, string(plan)).Text()
			if err != nil {
				t.Fatalf("AL5 receipt read returned Redis error: %v", err)
			}
			var reply boundaryReply
			if err := json.Unmarshal([]byte(wire), &reply); err != nil {
				t.Fatalf("decode AL5 receipt reply: %v", err)
			}
			after := commitProbeImage(t, fx.Client)
			if !reflect.DeepEqual(after, before) {
				t.Fatal("read boundary probe changed the whole Redis TYPE/DUMP image")
			}
			want := MaxFetchedBytes + tc.delta
			if tc.delta <= 0 {
				if len(reply.Answers) != 1 {
					t.Fatalf("accepted target %d returned %d answers: %+v", want, len(reply.Answers), reply)
				}
				planned := reply.Answers[0].Baseline + count*lowBytes +
					reply.Answers[0].HighCount + 5
				if reply.Status != "read" || !reply.Complete ||
					reply.Answers[0].Kind != "fetchboundary" ||
					reply.Answers[0].FetchedBytes != want || reply.Counters.FetchedBytes != want ||
					reply.Answers[0].MetadataDelta != count+1 || reply.Answers[0].Slots != count ||
					planned != want {
					t.Fatalf("accepted target %d: reply=%+v", want, reply)
				}
				if reply.Answers[0].HighCount < 0 || reply.Answers[0].HighCount >= count {
					t.Fatalf("high receipt count %d outside calibrated range", reply.Answers[0].HighCount)
				}
				return
			}
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal([]byte(wire), &envelope); err != nil {
				t.Fatal(err)
			}
			if _, hasAnswers := envelope["answers"]; hasAnswers {
				t.Fatalf("refusal exposed partial answers: %s", wire)
			}
			if reply.Status != "refused" || reply.Code != "BUDGET" ||
				reply.Detail.QueryIndex == nil || *reply.Detail.QueryIndex != 0 ||
				reply.Detail.Budget != "fetched_bytes" ||
				reply.Detail.MetadataDelta != count {
				t.Fatalf("over-cap target %d: reply=%+v", want, reply)
			}
			planned := reply.Detail.Baseline + count*lowBytes + reply.Detail.HighCount + 5
			if planned != want ||
				(reply.Detail.Actual != nil && *reply.Detail.Actual != want) ||
				(reply.Detail.Limit != nil && *reply.Detail.Limit != MaxFetchedBytes) {
				t.Fatalf("over-cap planned byte boundary %d: reply=%+v", want, reply)
			}
			if reply.Detail.HighCount < 0 || reply.Detail.HighCount >= count {
				t.Fatalf("high receipt count %d outside calibrated range", reply.Detail.HighCount)
			}
		})
	}
}
