//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// This callback exists only in the private test library. It observes absent
// records through the real S.before cache, then invokes the real S.plan. An
// accepted plan is deliberately not committed.
const writeRecordBudgetProbeLua = `
redis.register_function('ns_tset_write_record_budget_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local mode=args[3]
  local ctx,err=S.open(args[1],args[2])
  if err then return S.json.encode(err) end
  if ctx.replay then return S.json.encode(S.refuse('REQUEST')) end
  local n
  if mode=='total_5999' then n=5998
  elseif mode=='total_6000' then n=5999
  elseif mode=='total_6001' or mode=='overflow_preplan' then n=6000
  elseif mode=='duplicate_cache' or mode=='same_id_tables' then n=2
  else return S.json.encode(S.refuse('REQUEST')) end
  local ids={}
  for i=1,n do ids[i]=string.format('r%04d',i) end
  if mode=='duplicate_cache' or mode=='same_id_tables' then
    ids[1]='shared';ids[n]='planned'
  end
  local _,observed=S.before(ctx,'work',ids,S.array())
  if observed then return S.json.encode(observed) end
  local preplan_count=ctx.before_count
  local preplan_record=ctx.budget.record
  local preplan_record_commands=ctx.budget.record_commands or 0
  local preplan_metadata_fields=ctx.budget.record_metadata_fields or 0
  if mode=='duplicate_cache' then
    _,err=S.before(ctx,'work',{'planned','planned','shared'},S.array())
  elseif mode=='same_id_tables' then
    -- The same spelling in another table is a different record observation.
    _,err=S.before(ctx,'aux',{'shared'},S.array())
  elseif mode=='overflow_preplan' then
    _,err=S.before(ctx,'work',{'overflow'},S.array())
  end
  local after_extra_count=ctx.before_count
  if not err and mode~='overflow_preplan' then
    local plan;plan,err=S.plan(ctx)
    if not err then
      return S.json.encode({status='planned',preplan_count=preplan_count,
        after_extra_count=after_extra_count,post_count=ctx.before_count,
        preplan_record=preplan_record,post_record=ctx.budget.record,
        preplan_record_commands=preplan_record_commands,
        post_record_commands=ctx.budget.record_commands or 0,
        preplan_metadata_fields=preplan_metadata_fields,
        post_metadata_fields=ctx.budget.record_metadata_fields or 0,
        planned_commands=#plan.commands})
    end
  end
  return S.json.encode({status='refused',code=err and err.code or 'REQUEST',
    phase=mode=='overflow_preplan' and 'before' or 'plan',
    budget=err and err.detail.budget or cjson.null,
    actual=err and err.detail.actual or cjson.null,
    limit=err and err.detail.limit or cjson.null,
    preplan_count=preplan_count,after_extra_count=after_extra_count,
    post_count=ctx.before_count,preplan_record=preplan_record,
    post_record=ctx.budget.record,
    preplan_record_commands=preplan_record_commands,
    post_record_commands=ctx.budget.record_commands or 0,
    preplan_metadata_fields=preplan_metadata_fields,
    post_metadata_fields=ctx.budget.record_metadata_fields or 0})
end)
`

type writeRecordBudgetReply struct {
	Status                string `json:"status"`
	Code                  string `json:"code"`
	Phase                 string `json:"phase"`
	Budget                string `json:"budget"`
	Actual                int    `json:"actual"`
	Limit                 int    `json:"limit"`
	PreplanCount          int    `json:"preplan_count"`
	AfterExtraCount       int    `json:"after_extra_count"`
	PostCount             int    `json:"post_count"`
	PreplanRecord         int    `json:"preplan_record"`
	PostRecord            int    `json:"post_record"`
	PreplanRecordCommands int    `json:"preplan_record_commands"`
	PostRecordCommands    int    `json:"post_record_commands"`
	PreplanMetadataFields int    `json:"preplan_metadata_fields"`
	PostMetadataFields    int    `json:"post_metadata_fields"`
	PlannedCommands       int    `json:"planned_commands"`
}

func writeRecordBudgetCall(t *testing.T, fx *tsetFixture, mode string) writeRecordBudgetReply {
	t.Helper()
	// Each test uses a dedicated local fixture server. This second client only
	// raises the read timeout for the intentionally large single FCALL.
	opts := fx.Client.Options()
	c := redis.NewClient(&redis.Options{Addr: opts.Addr, MaxRetries: -1, ReadTimeout: 30 * time.Second})
	t.Cleanup(func() { _ = c.Close() })
	raw := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"create","t":"work","to":"r:c","ids":["planned"],"scores":["1"],"set":{}}]}`, fx.Space)
	value, err := c.FCall(context.Background(), "ns_tset_write_record_budget_probe", []string{}, Version, raw, mode).Result()
	if err != nil {
		t.Fatalf("write record budget FCALL %s: %v", mode, err)
	}
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("write record budget FCALL %s returned %T", mode, value)
	}
	var reply writeRecordBudgetReply
	if err := json.Unmarshal([]byte(encoded), &reply); err != nil {
		t.Fatalf("write record budget reply %q: %v", encoded, err)
	}
	return reply
}

func TestWriteRecordObservationBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode             string
		preplanCount     int
		afterExtraCount  int
		postCount        int
		observedRecords  int
		wantStatus       string
		wantRefusalPhase string
	}{
		{mode: "total_5999", preplanCount: 5998, afterExtraCount: 5998, postCount: 5999, observedRecords: 5999, wantStatus: "planned"},
		{mode: "total_6000", preplanCount: 5999, afterExtraCount: 5999, postCount: 6000, observedRecords: 6000, wantStatus: "planned"},
		{mode: "total_6001", preplanCount: 6000, afterExtraCount: 6000, postCount: 6001, observedRecords: 6000, wantStatus: "refused", wantRefusalPhase: "plan"},
		{mode: "duplicate_cache", preplanCount: 2, afterExtraCount: 2, postCount: 2, observedRecords: 2, wantStatus: "planned"},
		{mode: "same_id_tables", preplanCount: 2, afterExtraCount: 3, postCount: 3, observedRecords: 3, wantStatus: "planned"},
		{mode: "overflow_preplan", preplanCount: 6000, afterExtraCount: 6001, postCount: 6001, observedRecords: 6000, wantStatus: "refused", wantRefusalPhase: "before"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixture(t)
			fx.Define(t, "work", "c")
			fx.Define(t, "aux", "c")
			fx.AddRow(t, "work", "r", 1)
			fx.ActivateWithLua(t, writeRecordBudgetProbeLua)
			before := commitProbeImage(t, fx.Client)
			reply := writeRecordBudgetCall(t, fx, tc.mode)
			if reply.Status != tc.wantStatus || reply.PreplanCount != tc.preplanCount ||
				reply.AfterExtraCount != tc.afterExtraCount || reply.PostCount != tc.postCount ||
				reply.PreplanRecord != tc.preplanCount || reply.PostRecord != tc.observedRecords ||
				reply.PreplanRecordCommands != 2*tc.preplanCount || reply.PostRecordCommands != 2*tc.observedRecords ||
				reply.PreplanMetadataFields != 3*tc.preplanCount || reply.PostMetadataFields != 3*tc.observedRecords {
				t.Errorf("%s record observation counts: %+v", tc.mode, reply)
			}
			if tc.wantStatus == "refused" {
				if reply.Code != "LIMIT" || reply.Phase != tc.wantRefusalPhase || reply.Budget != "before_records" ||
					reply.Actual != 6001 || reply.Limit != 6000 {
					t.Errorf("%s: want LIMIT before_records 6001/6000 in %s; got %+v", tc.mode, tc.wantRefusalPhase, reply)
				}
			} else if reply.PlannedCommands == 0 {
				t.Errorf("%s: S.plan did not assemble the create", tc.mode)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Error("record budget probe changed the whole store")
			}
			commitProbeNoKeys(t, fx.Client, []string{fx.Space + "member:work:planned"})
		})
	}
}
