//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/redis/go-redis/v9"
)

// Exercise the production helper from a test-owned Redis Function. The probe
// creates an invocation context, calls S.zguard directly, and exposes only its
// result and shared read counters; it never stages or commits a write.
const zguardProbeLua = `
redis.register_function('ns_tset_zguard_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=2 then return S.json.encode(S.refuse('ARGS')) end
  local ctx,err=S.context({space='l1:',epoch='0',entries=S.json.decode('[]')},'step')
  if err then return S.json.encode(err) end
  local guard_ok,problem=S.zguard(ctx,args[1],S.json.decode(args[2]))
  return S.json.encode({status=problem and 'refused' or 'ok',
    code=problem and problem.code or '',count=guard_ok,
    budget={cell=ctx.budget.cell,cell_commands=ctx.budget.cell_commands or 0,
      store_commands=ctx.budget.store_commands,fetched_bytes=ctx.budget.fetched_bytes}})
end)
`

type zguardProbeReply struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Count  *int   `json:"count"`
	Budget struct {
		Cell          int `json:"cell"`
		CellCommands  int `json:"cell_commands"`
		StoreCommands int `json:"store_commands"`
		FetchedBytes  int `json:"fetched_bytes"`
	} `json:"budget"`
}

func TestZGuardRCountBounds(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	ctx := context.Background()
	key := fx.Space + "zguard:members"
	if err := fx.Client.ZAdd(ctx, key,
		redis.Z{Score: 1, Member: "one"},
		redis.Z{Score: 2, Member: "two"},
		redis.Z{Score: 3, Member: "three"}).Err(); err != nil {
		t.Fatal(err)
	}
	wrongTypeKey := fx.Space + "zguard:wrongtype"
	if err := fx.Client.Set(ctx, wrongTypeKey, "not-a-zset", 0).Err(); err != nil {
		t.Fatal(err)
	}
	fx.ActivateWithLua(t, zguardProbeLua)

	cases := []struct {
		name       string
		key        string
		guard      string
		wantStatus string
		wantCode   string
		wantCount  *int
		wantRead   bool
	}{
		{
			name: "inclusive exact score bounds and exact count limits",
			key:  key, guard: `{"kind":"rcount","min":"1","max":"2","atleast":2,"atmost":2}`,
			wantStatus: "ok", wantCount: zguardInt(2), wantRead: true,
		},
		{
			name: "exclusive score bounds",
			key:  key, guard: `{"kind":"rcount","min":"(1","max":"(3","atleast":1,"atmost":1}`,
			wantStatus: "ok", wantCount: zguardInt(1), wantRead: true,
		},
		{
			name: "atleast one above observation refuses",
			key:  key, guard: `{"kind":"rcount","min":"1","max":"2","atleast":3}`,
			wantStatus: "refused", wantCode: "RANGECOUNT", wantRead: true,
		},
		{
			name: "atmost one below observation refuses",
			key:  key, guard: `{"kind":"rcount","min":"1","max":"2","atmost":1}`,
			wantStatus: "refused", wantCode: "RANGECOUNT", wantRead: true,
		},
		{
			name: "malformed score bound refuses before read",
			key:  key, guard: `{"kind":"rcount","min":"NaN","max":"2","atmost":1}`,
			wantStatus: "refused", wantCode: "REQUEST",
		},
		{
			name: "missing score bound refuses before read",
			key:  key, guard: `{"kind":"rcount","max":"2","atmost":1}`,
			wantStatus: "refused", wantCode: "REQUEST",
		},
		{
			name: "missing count limit refuses before read",
			key:  key, guard: `{"kind":"rcount","min":"1","max":"2"}`,
			wantStatus: "refused", wantCode: "REQUEST",
		},
		{
			name: "fractional count limit refuses before read",
			key:  key, guard: `{"kind":"rcount","min":"1","max":"2","atleast":1.5}`,
			wantStatus: "refused", wantCode: "REQUEST",
		},
		{
			name: "inverted count limits refuse before read",
			key:  key, guard: `{"kind":"rcount","min":"1","max":"2","atleast":2,"atmost":1}`,
			wantStatus: "refused", wantCode: "REQUEST",
		},
		{
			name: "unknown guard member refuses before read",
			key:  key, guard: `{"kind":"rcount","min":"1","max":"2","atmost":2,"head":true}`,
			wantStatus: "refused", wantCode: "REQUEST",
		},
		{
			name: "wrong guard kind refuses before read",
			key:  key, guard: `{"kind":"count","min":"1","max":"2","atmost":2}`,
			wantStatus: "refused", wantCode: "REQUEST",
		},
		{
			name: "null guard shape refuses before read",
			key:  key, guard: `null`,
			wantStatus: "refused", wantCode: "REQUEST",
		},
		{
			name: "numeric score bound shape refuses before read",
			key:  key, guard: `{"kind":"rcount","min":1,"max":"2","atmost":2}`,
			wantStatus: "refused", wantCode: "REQUEST",
		},
		{
			name: "key outside invocation namespace refuses before read",
			key:  "other:zguard:members", guard: `{"kind":"rcount","min":"1","max":"2","atmost":2}`,
			wantStatus: "refused", wantCode: "REQUEST",
		},
		{
			name: "wrong redis key type is a checked refusal",
			key:  wrongTypeKey, guard: `{"kind":"rcount","min":"1","max":"2","atmost":2}`,
			wantStatus: "refused", wantCode: "WRONGTYPE", wantRead: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := commitProbeImage(t, fx.Client)
			value, err := fx.Client.FCall(ctx, "ns_tset_zguard_probe", []string{}, tc.key, tc.guard).Result()
			if err != nil {
				t.Fatalf("zguard probe FCALL: %v", err)
			}
			encoded, ok := value.(string)
			if !ok {
				t.Fatalf("zguard probe reply type %T, want JSON string", value)
			}
			var got zguardProbeReply
			if err := json.Unmarshal([]byte(encoded), &got); err != nil {
				t.Fatalf("decode zguard probe reply %q: %v", encoded, err)
			}
			if got.Status != tc.wantStatus || got.Code != tc.wantCode || !reflect.DeepEqual(got.Count, tc.wantCount) {
				t.Fatalf("zguard result=%+v, want status=%q code=%q count=%v", got, tc.wantStatus, tc.wantCode, tc.wantCount)
			}
			if tc.wantRead {
				if got.Budget.Cell != 1 || got.Budget.CellCommands != 1 || got.Budget.StoreCommands != 2 {
					t.Errorf("checked ZCOUNT budget=%+v, want one cell probe and ACL+command charges", got.Budget)
				}
				wantFetched := 1
				if tc.wantCode == "WRONGTYPE" {
					wantFetched = 0
				}
				if got.Budget.FetchedBytes != wantFetched {
					t.Errorf("fetched bytes=%d, want %d", got.Budget.FetchedBytes, wantFetched)
				}
			} else if got.Budget.Cell != 0 || got.Budget.CellCommands != 0 || got.Budget.StoreCommands != 0 || got.Budget.FetchedBytes != 0 {
				t.Errorf("static refusal performed charged reads: budget=%+v", got.Budget)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Errorf("zguard %s changed Redis state", tc.name)
			}
		})
	}
}

func zguardInt(value int) *int { return &value }
