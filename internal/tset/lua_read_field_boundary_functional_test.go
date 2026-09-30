//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// A repeated projection charges each returned record and field occurrence,
// while S.before fetches the single selected value only once. The public read
// limits of 10,000 records and 128 fields per record meet at 1,280,000.
func TestLuaReadFieldObservationMaximum(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	// The fixture client uses the normal short timeout. This read probe
	// verifies 1,280,000 field observations inside one FCALL, which under
	// concurrent test container execution requires a generous ceiling.
	probeClient := redis.NewClient(&redis.Options{Addr: fx.Client.Options().Addr,
		MaxRetries: -1, ReadTimeout: 30 * time.Second})
	t.Cleanup(func() { _ = probeClient.Close() })
	fx.Client = probeClient
	fx.Define(t, "work", "c")
	if err := fx.Client.HSet(context.Background(),
		fixtureRecordKey(fx.Space, "work", "one"),
		"epoch", "0", "revision", "1").Err(); err != nil {
		t.Fatalf("seed unplaced member: %v", err)
	}
	fx.Activate(t)
	before := commitProbeImage(t, fx.Client)

	ids := make([]string, 10000)
	for i := range ids {
		ids[i] = "one"
	}
	fields := make([]string, MaxFieldsPerMember)
	for i := range fields {
		fields[i] = "absent"
	}
	checkImage := func() {
		t.Helper()
		if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
			t.Fatal("read changed the whole Redis TYPE/DUMP image")
		}
	}

	for _, tc := range []struct {
		name    string
		queries []ReadQuery
		want    int
	}{
		{name: "max_minus_one", queries: []ReadQuery{
			{Kind: "ids", Table: "work", IDs: ids[:9999], Fields: fields},
			{Kind: "ids", Table: "work", IDs: ids[:1], Fields: fields[:127]},
		}, want: 9999*128 + 127},
		{name: "maximum", queries: []ReadQuery{
			{Kind: "ids", Table: "work", IDs: ids, Fields: fields},
		}, want: 10000 * 128},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := readLuaRaw(t, fx, ReadPlan{Epoch: "0", Space: fx.Space,
				Mode: "atomic", Queries: tc.queries})
			var reply ReadReply
			if err := json.Unmarshal(raw, &reply); err != nil {
				t.Fatalf("decode Lua read: %v", err)
			}
			if reply.Status != "read" || !reply.Complete || len(reply.Answers) != len(tc.queries) {
				t.Fatalf("incomplete Lua read: status=%q complete=%t answers=%d, want %d: %s",
					reply.Status, reply.Complete, len(reply.Answers), len(tc.queries), raw)
			}
			for i, query := range tc.queries {
				answer := reply.Answers[i]
				if answer.Kind != "ids" || len(answer.Records) != len(query.IDs) {
					t.Fatalf("answer %d: kind=%q records=%d, want %d",
						i, answer.Kind, len(answer.Records), len(query.IDs))
				}
				for j, record := range answer.Records {
					value, exists := record.Fields["absent"]
					if record.ID != "one" || !record.Exists || len(record.Fields) != 1 ||
						!exists || value.Present {
						t.Fatalf("answer %d record %d lost its selected absence: %+v", i, j, record)
					}
				}
			}
			var counters struct {
				Record int `json:"record"`
				Field  int `json:"field"`
			}
			if err := json.Unmarshal(reply.Counters, &counters); err != nil {
				t.Fatalf("decode read counters: %v", err)
			}
			if counters.Record != 10000 || counters.Field != tc.want {
				t.Fatalf("read observations=%+v, want record=10000 field=%d", counters, tc.want)
			}
			checkImage()
		})
	}

	// The next projected record reaches the earlier public record cap. The
	// first query has completed internally, but its answer must not escape.
	raw := readLuaRaw(t, fx, ReadPlan{Epoch: "0", Space: fx.Space,
		Mode: "atomic", Queries: []ReadQuery{
			{Kind: "ids", Table: "work", IDs: ids, Fields: fields},
			{Kind: "ids", Table: "work", IDs: ids[:1], Fields: fields[:1]},
		}})
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode over-cap Lua read: %v", err)
	}
	if _, leaked := envelope["answers"]; leaked {
		t.Fatalf("record-limit refusal exposed a partial answer: %s", raw)
	}
	var refusal Refusal
	if err := json.Unmarshal(raw, &refusal); err != nil || refusal.Status != "refused" ||
		refusal.Code != "BUDGET" || refusal.Detail.Budget != "record" ||
		refusal.Detail.QueryIndex == nil || *refusal.Detail.QueryIndex != 1 {
		t.Fatalf("want BUDGET/record at second query: refusal=%+v err=%v", refusal, err)
	}
	checkImage()
}
