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

func TestMemReadIDsUseStoredRecordEpoch(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	if err := m.SeedMember("read:", "work", "0", "old", MemRecord{
		Epoch: "0", Revision: "3", Row: "alpha", Column: "status", Score: "2",
		Fields: map[string]string{"tag": "old"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedMember("read:", "work", "0", "parked", MemRecord{
		Epoch: "0", Revision: "4", Fields: map[string]string{"tag": "parked"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetActiveEpoch("read:", "1"); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedRow("read:", "work", "1", "gamma", "11"); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedMember("read:", "work", "1", "new", MemRecord{
		Epoch: "1", Revision: "7", Row: "gamma", Column: "status", Score: "5",
		Fields: map[string]string{"tag": "new"},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := m.Snapshot("read:")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		request Decimal
		ids     []string
		want    []struct {
			epoch Decimal
			row   string
			tag   string
		}
	}{
		{"1", []string{"old", "parked", "new"}, []struct {
			epoch Decimal
			row   string
			tag   string
		}{{"0", "alpha", "old"}, {"0", "", "parked"}, {"1", "gamma", "new"}}},
		{"0", []string{"new", "old"}, []struct {
			epoch Decimal
			row   string
			tag   string
		}{{"1", "gamma", "new"}, {"0", "alpha", "old"}}},
	} {
		reply, err := m.Read(context.Background(), ReadPlan{Epoch: tc.request, Space: "read:", Queries: []ReadQuery{{
			Kind: "ids", Table: "work", IDs: tc.ids, Fields: []string{"tag"},
		}}})
		if err != nil || !reply.Complete || len(reply.Answers) != 1 || len(reply.Answers[0].Records) != len(tc.want) {
			t.Fatalf("epoch %s ids read: reply=%+v err=%v", tc.request, reply, err)
		}
		for i, record := range reply.Answers[0].Records {
			want := tc.want[i]
			row := ""
			if record.Place != nil {
				row = record.Place.Row
			}
			if !record.Exists || record.ID != tc.ids[i] || record.Epoch != want.epoch ||
				row != want.row || !record.Fields["tag"].Present || record.Fields["tag"].Value != want.tag {
				t.Fatalf("epoch %s record %d: got %+v want %+v", tc.request, i, record, want)
			}
		}
	}
	after, err := m.Snapshot("read:")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("cross-epoch read changed state: %v", err)
	}
}

func TestMemDoneMissingIdentityEpochReportsActive(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	if err := m.SetActiveEpoch("read:", "1"); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	delete(m.spaces["read:"].epochs, "0")
	m.mu.Unlock()
	before, err := m.Snapshot("read:")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "1", Space: "read:", Queries: []ReadQuery{
		{Kind: "rows", Table: "work"},
		{Kind: "done", Ops: []DoneIdentity{{Epoch: "0", Op: "old-op", IntentDigest: strings.Repeat("0", 40)}}},
	}})
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != "EPOCHGONE" ||
		refusal.Detail.ActiveEpoch != "1" || refusal.Detail.QueryIndex == nil || *refusal.Detail.QueryIndex != 1 {
		t.Fatalf("done missing identity epoch: reply=%+v err=%v", reply, err)
	}
	if reply.Status != "" || len(reply.Answers) != 0 {
		t.Fatalf("done refusal leaked answers: %+v", reply)
	}
	after, err := m.Snapshot("read:")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("done refusal changed state: %v", err)
	}
}

func TestMemRowsCountPreflightAndEmptyPage(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	if err := m.SetActiveEpoch("read:", "1"); err != nil {
		t.Fatal(err)
	}
	empty, err := m.Read(context.Background(), ReadPlan{Epoch: "1", Space: "read:", Queries: []ReadQuery{{Kind: "rows", Table: "work"}}})
	if err != nil || len(empty.Answers) != 1 || len(empty.Answers[0].Rows) != 0 {
		t.Fatalf("empty rows: reply=%+v err=%v", empty, err)
	}
	var counters struct {
		Cell int64 `json:"cell"`
	}
	if err := json.Unmarshal(empty.Counters, &counters); err != nil || counters.Cell != 5 {
		t.Fatalf("empty rows should probe ZCARD but no ZRANGE: %+v err=%v", counters, err)
	}
	// The raw reservation for 30,000 rows exceeds 8 MiB. It must refuse
	// immediately after ZCARD, before sorting or returning any row objects.
	for i := 0; i < 30000; i++ {
		if err := m.SeedRow("read:", "work", "1", fmt.Sprintf("r%05d", i), Decimal(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "1", Space: "read:", Queries: []ReadQuery{{Kind: "rows", Table: "work"}}})
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != "BUDGET" || refusal.Detail.Budget != "fetched_bytes" ||
		refusal.Detail.QueryIndex == nil || *refusal.Detail.QueryIndex != 0 {
		t.Fatalf("oversized rows count: reply=%+v err=%v", reply, err)
	}
	if reply.Status != "" || len(reply.Answers) != 0 {
		t.Fatalf("rows refusal leaked answers: %+v", reply)
	}
}

func TestMemReadCJSONStringByteAccounting(t *testing.T) {
	t.Parallel()
	value := "<>&/\x7f\u2028\u2029\b\t\n\f\r\\\""
	got, err := readCJSONLength(map[string]string{"v": value})
	if err != nil || got != 39 { // object syntax 6; CJSON string 33
		t.Fatalf("CJSON byte length = %d, err=%v; want 39", got, err)
	}
	// Go's default JSON spelling is longer for HTML and U+2028/U+2029.
	wire, err := json.Marshal(map[string]string{"v": value})
	if err != nil || int64(len(wire)) <= got {
		t.Fatalf("fixture failed to separate Go/CJSON escaping: Go=%d CJSON=%d err=%v", len(wire), got, err)
	}
}

func TestMemRowsPreflightUsesCJSONPriorAnswerBytes(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	if err := m.SeedMember("read:", "work", "0", "large", MemRecord{
		Epoch: "0", Revision: "1", Fields: map[string]string{"html": strings.Repeat("<", MaxFieldValueBytes)},
	}); err != nil {
		t.Fatal(err)
	}
	const rowCount = 29000
	for i := 0; i < rowCount; i++ {
		if err := m.SeedRow("read:", "work", "0", fmt.Sprintf("r%05d", i), Decimal(fmt.Sprint(i+10))); err != nil {
			t.Fatal(err)
		}
	}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{
		{Kind: "ids", Table: "work", IDs: []string{"large"}, Fields: []string{"html"}},
		{Kind: "rows", Table: "work"},
	}})
	if err != nil || len(reply.Answers) != 2 || len(reply.Answers[1].Rows) != rowCount+2 ||
		!reply.Answers[0].Records[0].Fields["html"].Present {
		t.Fatalf("CJSON-sized ids + rows should fit: answers=%d err=%v", len(reply.Answers), err)
	}
}
