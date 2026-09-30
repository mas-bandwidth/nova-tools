//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// This test-only wrapper intercepts the built-in done query after S.read has
// sampled TIME. It builds unique identities from that invocation's live
// fetched-byte baseline, then calls the original production S.done_read.
// No AL5 callback gains access to the internal receipt reader.
const luaReadFetchedBoundaryProbe = `
redis.register_function('ns_tset_lua_read_fetched_boundary_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local delta=tonumber(args[3])
  if delta~=-1 and delta~=0 and delta~=1 then
    return S.json.encode(S.refuse('REQUEST'))
  end
  local original=S.done_read
  local probe={}
  S.done_read=function(ctx,_)
    local cap,count,low_bytes=8388608,400,20971
    local baseline=ctx.budget.fetched_bytes
    -- The last low receipt triggers one five-byte HSTRLEN. Choose the number
    -- of high receipts from this invocation's post-TIME baseline, never from
    -- a different call's sampled clock payload.
    local highs=cap+delta-baseline-5-count*low_bytes
    probe.baseline=baseline
    probe.high_count=highs
    if highs<0 or highs>count-1 or highs~=math.floor(highs) then
      return nil,S.refuse('CONFIG',{query_index=ctx.query_index,budget='calibration',
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
    local slots,err=original(ctx,ops)
    probe.metadata_delta=(ctx.budget.metadata_commands or 0)-before
    probe.fetched_bytes=ctx.budget.fetched_bytes
    probe.slots=slots and #slots or 0
    if slots then
      if #slots~=count then return nil,S.refuse('CONFIG',{query_index=ctx.query_index}) end
      for i=1,count do
        if slots[i].status~='conflict' then
          return nil,S.refuse('CONFIG',{query_index=ctx.query_index})
        end
      end
    end
    return slots,err
  end
  local ok,encoded=pcall(S.read,args[1],args[2],nil,nil)
  S.done_read=original
  if not ok then error(encoded,0) end
  return S.json.encode({reply=S.json.decode(encoded),probe=probe})
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
	t.Parallel()
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
		Status   string            `json:"status"`
		Code     string            `json:"code"`
		Complete bool              `json:"complete"`
		Answers  []json.RawMessage `json:"answers"`
		Counters struct {
			FetchedBytes int `json:"fetched_bytes"`
		} `json:"counters"`
		Detail struct {
			QueryIndex *int   `json:"query_index"`
			Budget     string `json:"budget"`
			Actual     *int   `json:"actual"`
			Limit      *int   `json:"limit"`
		} `json:"detail"`
	}
	for _, tc := range []struct {
		name  string
		delta int
	}{{"limit_minus_one", -1}, {"limit", 0}, {"limit_plus_one", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := json.Marshal(map[string]any{"epoch": "0", "space": fx.Space,
				"mode": "atomic", "queries": []any{map[string]any{
					"kind": "done", "ops": []any{map[string]any{
						"epoch": "0", "op": "low", "intent_digest": strings.Repeat("0", 40)}}}}})
			if err != nil {
				t.Fatal(err)
			}
			before := commitProbeImage(t, fx.Client)
			callCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			wire, err := probeClient.FCall(callCtx,
				"ns_tset_lua_read_fetched_boundary_probe", nil, Version, string(plan), strconv.Itoa(tc.delta)).Text()
			if err != nil {
				t.Fatalf("receipt boundary read returned Redis error: %v", err)
			}
			var observed struct {
				Reply boundaryReply `json:"reply"`
				Probe struct {
					FetchedBytes  int `json:"fetched_bytes"`
					MetadataDelta int `json:"metadata_delta"`
					Baseline      int `json:"baseline"`
					HighCount     int `json:"high_count"`
					Slots         int `json:"slots"`
				} `json:"probe"`
			}
			if err := json.Unmarshal([]byte(wire), &observed); err != nil {
				t.Fatalf("decode receipt boundary reply: %v", err)
			}
			reply, probe := observed.Reply, observed.Probe
			after := commitProbeImage(t, fx.Client)
			if !reflect.DeepEqual(after, before) {
				t.Fatal("read boundary probe changed the whole Redis TYPE/DUMP image")
			}
			want := MaxFetchedBytes + tc.delta
			if tc.delta <= 0 {
				if len(reply.Answers) != 1 {
					t.Fatalf("accepted target %d returned %d answers: %+v", want, len(reply.Answers), reply)
				}
				planned := probe.Baseline + count*lowBytes + probe.HighCount + 5
				if reply.Status != "read" || !reply.Complete ||
					probe.FetchedBytes != want || reply.Counters.FetchedBytes != want ||
					probe.MetadataDelta != count+1 || probe.Slots != count ||
					planned != want {
					t.Fatalf("accepted target %d: reply=%+v probe=%+v", want, reply, probe)
				}
				if probe.HighCount < 0 || probe.HighCount >= count {
					t.Fatalf("high receipt count %d outside calibrated range", probe.HighCount)
				}
				return
			}
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal([]byte(wire), &envelope); err != nil {
				t.Fatal(err)
			}
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(envelope["reply"], &nested); err != nil {
				t.Fatal(err)
			}
			if _, hasAnswers := nested["answers"]; hasAnswers {
				t.Fatalf("refusal exposed partial answers: %s", wire)
			}
			if reply.Status != "refused" || reply.Code != "BUDGET" ||
				reply.Detail.QueryIndex == nil || *reply.Detail.QueryIndex != 0 ||
				reply.Detail.Budget != "fetched_bytes" ||
				probe.MetadataDelta != count {
				t.Fatalf("over-cap target %d: reply=%+v probe=%+v", want, reply, probe)
			}
			planned := probe.Baseline + count*lowBytes + probe.HighCount + 5
			if planned != want ||
				(reply.Detail.Actual != nil && *reply.Detail.Actual != want) ||
				(reply.Detail.Limit != nil && *reply.Detail.Limit != MaxFetchedBytes) {
				t.Fatalf("over-cap planned byte boundary %d: reply=%+v", want, reply)
			}
			if probe.HighCount < 0 || probe.HighCount >= count {
				t.Fatalf("high receipt count %d outside calibrated range", probe.HighCount)
			}
		})
	}
}
