package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func requireBoundaryState(t *testing.T, m *Mem, before MemSnapshot) {
	t.Helper()
	after, err := m.Snapshot("read:")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("read changed the Mem state")
	}
}

func requireBoundaryBudget(t *testing.T, reply ReadReply, err error, budget string, limit, actual int64, query int) {
	t.Helper()
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != "BUDGET" || refusal.Detail.Budget != budget ||
		refusal.Detail.Limit == nil || *refusal.Detail.Limit != limit ||
		refusal.Detail.Actual == nil || *refusal.Detail.Actual != actual ||
		refusal.Detail.QueryIndex == nil || *refusal.Detail.QueryIndex != query {
		t.Fatalf("want %s budget %d/%d at query %d; reply=%+v err=%v", budget, actual, limit, query, reply, err)
	}
	if reply.Status != "" || len(reply.Answers) != 0 {
		t.Fatalf("budget refusal leaked an answer: %+v", reply)
	}
}

func TestMemReadAggregateRecordBoundary(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	if err := m.SeedMember("read:", "work", "0", "one", MemRecord{Epoch: "0", Revision: "1"}); err != nil {
		t.Fatal(err)
	}
	before, err := m.Snapshot("read:")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{9999, 10000, 10001} {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = "one"
		}
		plan := ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{
			{Kind: "ids", Table: "work", IDs: ids[:5000], Fields: []string{}},
			{Kind: "ids", Table: "work", IDs: ids[5000:], Fields: []string{}},
		}}
		reply, readErr := m.Read(context.Background(), plan)
		if n == 10001 {
			requireBoundaryBudget(t, reply, readErr, "record", 10000, 10001, 1)
		} else {
			if readErr != nil || !reply.Complete || len(reply.Answers) != 2 ||
				len(reply.Answers[0].Records)+len(reply.Answers[1].Records) != n {
				got := 0
				for _, answer := range reply.Answers {
					got += len(answer.Records)
				}
				t.Fatalf("%d aggregate records: answers=%d records=%d complete=%t err=%v", n, len(reply.Answers), got, reply.Complete, readErr)
			}
			var counters struct {
				Record int64 `json:"record"`
			}
			if err := json.Unmarshal(reply.Counters, &counters); err != nil || counters.Record != int64(n) {
				t.Fatalf("%d aggregate records charged %+v: %v", n, counters, err)
			}
		}
		requireBoundaryState(t, m, before)
	}
}

func TestMemReadAggregateRangeIDBoundary(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	key := "read:index:boundary"
	members := make(map[string]string, 2000)
	for i := 0; i < 2000; i++ {
		members[fmt.Sprintf("r%04d", i)] = "1"
	}
	if err := m.SeedZSet("read:", key, members); err != nil {
		t.Fatal(err)
	}
	before, err := m.Snapshot("read:")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{19999, 20000, 20001} {
		queries := make([]ReadQuery, 0, n/2000+1)
		for remaining := n; remaining > 0; {
			limit := remaining
			if limit > 2000 {
				limit = 2000
			}
			queries = append(queries, ReadQuery{Kind: "range", Key: key, Min: "-inf", Max: "+inf", Limit: limit})
			remaining -= limit
		}
		reply, readErr := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: queries})
		if n == 20001 {
			requireBoundaryBudget(t, reply, readErr, "range_id", 20000, 20001, 10)
		} else {
			if readErr != nil || !reply.Complete || len(reply.Answers) != len(queries) {
				t.Fatalf("%d aggregate range IDs: answers=%d err=%v", n, len(reply.Answers), readErr)
			}
			got := 0
			for _, answer := range reply.Answers {
				got += len(answer.IDs)
			}
			var counters struct {
				RangeID int64 `json:"range_id"`
			}
			if err := json.Unmarshal(reply.Counters, &counters); err != nil || got != n || counters.RangeID != int64(n) {
				t.Fatalf("%d aggregate range IDs: got=%d counters=%+v err=%v", n, got, counters, err)
			}
		}
		requireBoundaryState(t, m, before)
	}
}

func TestMemReadDoneIdentityQueryBoundary(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	before, err := m.Snapshot("read:")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1999, 2000, 2001} {
		ops := make([]DoneIdentity, n)
		for i := range ops {
			ops[i] = DoneIdentity{Epoch: "0", Op: fmt.Sprintf("op-%04d", i), IntentDigest: strings.Repeat("0", 40)}
		}
		reply, readErr := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "done", Ops: ops}}})
		if n == 2001 {
			var refusal *Refusal
			if !errors.As(readErr, &refusal) || refusal.Code != "LIMIT" || reply.Status != "" || len(reply.Answers) != 0 {
				t.Fatalf("2,001 done identities: reply=%+v err=%v", reply, readErr)
			}
		} else {
			if readErr != nil || !reply.Complete || len(reply.Answers) != 1 || len(reply.Answers[0].Done) != n {
				got := 0
				if len(reply.Answers) != 0 {
					got = len(reply.Answers[0].Done)
				}
				t.Fatalf("%d done identities: answers=%d slots=%d complete=%t err=%v", n, len(reply.Answers), got, reply.Complete, readErr)
			}
			for i, slot := range reply.Answers[0].Done {
				if slot.Status != "absent" {
					t.Fatalf("%d done identities: slot %d is %+v", n, i, slot)
				}
			}
		}
		requireBoundaryState(t, m, before)
	}
}
