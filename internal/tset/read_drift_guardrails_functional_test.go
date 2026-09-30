//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func readDriftRangePlan(space string) ReadPlan {
	return ReadPlan{Epoch: "0", Space: space, Mode: "atomic", Queries: []ReadQuery{
		{Kind: "rows", Table: "work"},
		{Kind: "range", Table: "work", Cell: "r:c", Min: "-inf", Max: "+inf",
			Limit: 1, Records: true, Fields: []string{}},
	}}
}

func readDriftRequireNoPartialRefusal(t *testing.T, fx *tsetFixture, before map[string]commitProbeKey,
	raw []byte, code string, queryIndex int) {
	t.Helper()
	var refusal Refusal
	if err := json.Unmarshal(raw, &refusal); err != nil || refusal.Status != "refused" ||
		refusal.Code != code || refusal.Detail.QueryIndex == nil ||
		*refusal.Detail.QueryIndex != queryIndex {
		t.Fatalf("want %s at query %d; reply=%s err=%v", code, queryIndex, raw, err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	if _, exists := object["answers"]; exists {
		t.Fatalf("%s refusal exposed an earlier answer: %s", code, raw)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatalf("%s read refusal changed the whole Redis TYPE/DUMP image", code)
	}
}

func TestReadDriftRefusesWhole(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *tsetFixture)
	}{
		{name: "wrong_place", setup: func(t *testing.T, fx *tsetFixture) {
			ctx := context.Background()
			if err := fx.Client.HSet(ctx, fixtureRecordKey(fx.Space, "work", "card"),
				"epoch", "0", "revision", "1", "place:work", "s:c").Err(); err != nil {
				t.Fatal(err)
			}
			// The record points to a valid second cell. A duplicate membership in
			// r:c makes the wrong placement visible to the range-record join.
			if err := fx.Client.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "c"),
				redis.Z{Score: 1, Member: "card"}).Err(); err != nil {
				t.Fatal(err)
			}
			if err := fx.Client.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "s", "c"),
				redis.Z{Score: 2, Member: "card"}).Err(); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unsupported_score", setup: func(t *testing.T, fx *tsetFixture) {
			ctx := context.Background()
			if err := fx.Client.HSet(ctx, fixtureRecordKey(fx.Space, "work", "card"),
				"epoch", "0", "revision", "1", "place:work", "r:c").Err(); err != nil {
				t.Fatal(err)
			}
			// Redis can store +inf, but it is outside the TSet score domain. The
			// actual range-record projection must refuse before exposing rows.
			if err := fx.Client.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "c"),
				redis.Z{Score: math.Inf(1), Member: "card"}).Err(); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newTSetFixture(t)
			fx.Define(t, "work", "c")
			fx.AddRow(t, "work", "r", 1)
			fx.AddRow(t, "work", "s", 2)
			tc.setup(t, fx)
			fx.Activate(t)
			before := commitProbeImage(t, fx.Client)
			readDriftRequireNoPartialRefusal(t, fx, before,
				readLuaRaw(t, fx, readDriftRangePlan(fx.Space)), "DRIFT", 1)
		})
	}
}

func TestReadDuplicateProjectionNamesChargeOccurrences(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	if err := fx.Client.HSet(context.Background(), fixtureRecordKey(fx.Space, "work", "one"),
		"epoch", "0", "revision", "1", "state", "ready").Err(); err != nil {
		t.Fatal(err)
	}
	fx.Activate(t)
	before := commitProbeImage(t, fx.Client)
	raw := readLuaRaw(t, fx, ReadPlan{Epoch: "0", Space: fx.Space,
		Queries: []ReadQuery{{Kind: "ids", Table: "work", IDs: []string{"one"},
			Fields: []string{"state", "state", "state"}}}})
	var reply ReadReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatal(err)
	}
	var counters struct {
		Field int `json:"field"`
	}
	if err := json.Unmarshal(reply.Counters, &counters); err != nil {
		t.Fatal(err)
	}
	if reply.Status != "read" || !reply.Complete || len(reply.Answers) != 1 ||
		len(reply.Answers[0].Records) != 1 || counters.Field != 3 {
		t.Fatalf("duplicate field occurrences were not charged independently: reply=%+v", reply)
	}
	if len(reply.Answers[0].Records[0].Fields) != 1 ||
		reply.Answers[0].Records[0].Fields["state"].Value != "ready" {
		t.Fatalf("duplicate projection changed the returned map value: %+v", reply.Answers[0].Records[0])
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatal("duplicate projection read changed the whole Redis TYPE/DUMP image")
	}
}

func TestReadEncodedReplyRefusalStopsProjection(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	const total = 24
	ids := make([]string, total)
	blob := strings.Repeat("\x01", MaxFieldValueBytes)
	pipe := fx.Client.Pipeline()
	ctx := context.Background()
	for i := range ids {
		ids[i] = fmt.Sprintf("item%02d", i)
		pipe.HSet(ctx, fixtureRecordKey(fx.Space, "work", ids[i]),
			"epoch", "0", "revision", "1", "blob", blob)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fx.Activate(t)
	before := commitProbeImage(t, fx.Client)
	beforeStats := readExtensionCommandStats(t, fx.Client)
	raw := readLuaRaw(t, fx, ReadPlan{Epoch: "0", Space: fx.Space,
		Queries: []ReadQuery{{Kind: "ids", Table: "work", IDs: ids, Fields: []string{"blob"}}}})
	afterStats := readExtensionCommandStats(t, fx.Client)
	var refusal Refusal
	if err := json.Unmarshal(raw, &refusal); err != nil || refusal.Status != "refused" ||
		refusal.Code != "BUDGET" || refusal.Detail.Budget != "encoded_reply" {
		t.Fatalf("want atomic encoded-reply refusal; response=%s err=%v", raw, err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	if _, exists := object["answers"]; exists {
		t.Fatalf("encoded-reply refusal exposed a partial record list: %s", raw)
	}
	hmgets := readExtensionExecutedDelta(t, beforeStats, afterStats, "HMGET")
	// Each fully projected distinct member needs two HMGETs (metadata and
	// payload). The refusal should stop while processing the first item that
	// crosses the byte cap, before all 24 records are materialized.
	if hmgets <= 0 || hmgets >= 2*total {
		t.Fatalf("encoded refusal did not stop before full projection: HMGET calls=%d for %d records", hmgets, total)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatal("encoded-reply refusal changed the whole Redis TYPE/DUMP image")
	}
}

func TestReadRetainedDefinitionCapAndMissingSnapshot(t *testing.T) {
	t.Parallel()
	t.Run("four_retained_definitions_then_cap", func(t *testing.T) {
		fx := newTSetFixture(t)
		for _, name := range []string{"one", "two", "three", "four"} {
			fx.Define(t, name, "c")
		}
		fx.Epoch = "1"
		fx.seedEpoch(t)
		fx.ActivateWithLua(t, readExtensionProbeLua)
		before := commitProbeImage(t, fx.Client)
		queries := `[{"kind":"related","tables":["one","two","three","four"],"fields":[]}]`
		accepted := readExtensionCall(t, fx, readExtensionRaw(fx.Space, "0", "atomic", queries), "normal")
		if accepted.Reply.Status != "read" || len(accepted.Reply.Answers) != 1 {
			t.Fatalf("four retained table definitions refused: %+v", accepted.Reply)
		}
		queries = `[{"kind":"related","tables":["one","two","three","four","fifth"],"fields":[]}]`
		refused := readExtensionCall(t, fx, readExtensionRaw(fx.Space, "0", "atomic", queries), "normal")
		if refused.Reply.Status != "refused" || refused.Reply.Code != "LIMIT" ||
			refused.Reply.Detail.Budget != "tables" || refused.Reply.Detail.Actual != 5 ||
			refused.Reply.Detail.Limit != 4 || len(refused.Reply.Answers) != 0 {
			t.Fatalf("fifth retained table did not refuse atomically: %+v", refused.Reply)
		}
		if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
			t.Fatal("retained table reads changed the whole Redis TYPE/DUMP image")
		}
	})
	t.Run("missing_historical_snapshot", func(t *testing.T) {
		fx := newTSetFixture(t)
		fx.Define(t, "work", "c")
		fx.Epoch = "1"
		fx.seedEpoch(t)
		if err := fx.Client.Del(context.Background(), fixtureDefinitionKey(fx.Space, "work", "0")).Err(); err != nil {
			t.Fatal(err)
		}
		fx.ActivateWithLua(t, readExtensionProbeLua)
		before := commitProbeImage(t, fx.Client)
		queries := `[{"kind":"related","tables":["work"],"fields":[]}]`
		result := readExtensionCall(t, fx, readExtensionRaw(fx.Space, "0", "atomic", queries), "normal")
		if result.Reply.Status != "refused" || result.Reply.Code != "EPOCHGONE" ||
			result.Reply.Detail.ActiveEpoch != "1" || result.Reply.Detail.QueryIndex == nil ||
			*result.Reply.Detail.QueryIndex != 0 || len(result.Reply.Answers) != 0 {
			t.Fatalf("missing historical definition lacked epoch-aware refusal: %+v", result.Reply)
		}
		if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
			t.Fatal("missing snapshot refusal changed the whole Redis TYPE/DUMP image")
		}
	})
}
