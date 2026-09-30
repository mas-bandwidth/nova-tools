package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func readFixture(t *testing.T) *Mem {
	t.Helper()
	m := NewMem()
	if err := m.DefineTable("read:", "work", TableDefinition{Columns: []string{"status"}, MemberPrefix: "read:member:", EpochKey: "read:epoch", EpochField: "active"}); err != nil {
		t.Fatal(err)
	}
	for row, rank := range map[string]Decimal{"alpha": "7", "beta": "9"} {
		if err := m.SeedRow("read:", "work", "0", row, rank); err != nil {
			t.Fatal(err)
		}
	}
	m.SetClock(func() time.Time { return time.UnixMilli(1712345678901) })
	return m
}

func requireReadCode(t *testing.T, err error, code string, query int) {
	t.Helper()
	r, ok := err.(*Refusal)
	if !ok || r.Code != code || r.Detail.QueryIndex == nil || *r.Detail.QueryIndex != query {
		t.Fatalf("want %s at query %d, got %#v", code, query, err)
	}
}

func TestMemRangeTieOrderHasMoreAndRawKey(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	for _, p := range []struct{ id, score string }{{"z", "2"}, {"b", "1"}, {"a", "1"}} {
		if err := m.SeedMember("read:", "work", "0", p.id, MemRecord{Epoch: "0", Revision: "1", Row: "alpha", Column: "status", Score: p.score}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.SeedZSet("read:", "read:index", map[string]string{"ix-a": "1", "ix-b": "2"}); err != nil {
		t.Fatal(err)
	}
	q := ReadQuery{Kind: "range", Table: "work", Cell: "alpha:status", Min: "-inf", Max: "+inf", Limit: 2}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{q}})
	if err != nil {
		t.Fatal(err)
	}
	a := reply.Answers[0]
	if len(a.IDs) != 2 || a.IDs[0] != "a" || a.IDs[1] != "b" || !a.HasMore || reply.TimeMS != "1712345678901" || reply.ActiveEpoch != "0" || !reply.Complete {
		t.Fatalf("wrong ordered range/envelope: %#v, %#v", a, reply)
	}
	q.Desc = true
	reply, err = m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{q}})
	if err != nil {
		t.Fatal(err)
	}
	if got := reply.Answers[0].IDs; len(got) != 2 || got[0] != "z" || got[1] != "b" {
		t.Fatalf("descending tie order: %#v", got)
	}
	q = ReadQuery{Kind: "range", Key: "read:index", Min: "-inf", Max: "+inf", Limit: 2}
	reply, err = m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{q}})
	if err != nil || len(reply.Answers[0].IDs) != 2 {
		t.Fatalf("authorized raw range: %#v, %v", reply, err)
	}
	q.Key = "other:index"
	_, err = m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{q}})
	requireReadCode(t, err, "REQUEST", 0)
}

func TestMemReadMissingUnplacedProjectionAndDrift(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	if err := m.SeedMember("read:", "work", "0", "parked", MemRecord{Epoch: "0", Revision: "3", Fields: map[string]string{"empty": "", "secret": "s"}}); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedMember("read:", "work", "0", "placed", MemRecord{Epoch: "0", Revision: "4", Row: "alpha", Column: "status", Score: "2", Fields: map[string]string{"empty": "", "secret": "s"}}); err != nil {
		t.Fatal(err)
	}
	q := ReadQuery{Kind: "ids", Table: "work", IDs: []string{"missing", "parked", "placed"}, Fields: []string{"empty", "absent"}}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{q}})
	if err != nil {
		t.Fatal(err)
	}
	r := reply.Answers[0].Records
	if len(r) != 3 || r[0].Exists || !r[1].Exists || r[1].Place != nil || r[1].Revision != "3" || r[2].Place == nil || r[2].Fields["empty"].Present != true || r[2].Fields["empty"].Value != "" || r[2].Fields["absent"].Present || len(r[2].Fields) != 2 {
		t.Fatalf("missing, unplaced or projection wrong: %#v", r)
	}
	all, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "range", Table: "work", Cell: "alpha:status", Min: "-inf", Max: "+inf", Limit: 1, Records: true}}})
	if err != nil {
		t.Fatal(err)
	}
	allFields := all.Answers[0].Records[0].Fields
	if len(allFields) != 2 || !allFields["empty"].Present || allFields["secret"].Value != "s" {
		t.Fatalf("omitted range fields must project all application fields: %#v", allFields)
	}
	// A corrupted score is a refusal for the whole mixed atomic call.
	m.mu.Lock()
	m.spaces["read:"].epochs["0"].tables["work"].cells["alpha"]["status"]["placed"] = "3"
	m.mu.Unlock()
	reply, err = m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "count", Table: "work", Cells: []string{"alpha:status"}}, q}})
	requireReadCode(t, err, "DRIFT", 1)
	if len(reply.Answers) != 0 {
		t.Fatalf("partial answers escaped: %#v", reply)
	}
}

func TestMemCountsRowsAndExclusiveBounds(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	for _, p := range []struct{ id, score, row string }{{"a", "1", "alpha"}, {"b", "2", "alpha"}, {"c", "2", "beta"}} {
		if err := m.SeedMember("read:", "work", "0", p.id, MemRecord{Epoch: "0", Revision: "1", Row: p.row, Column: "status", Score: p.score}); err != nil {
			t.Fatal(err)
		}
	}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{
		{Kind: "count", Table: "work", Cells: []string{"alpha:status", "beta:status"}},
		{Kind: "rcount", Table: "work", Cells: []string{"alpha:status", "beta:status"}, Min: "(1", Max: "2"},
		{Kind: "rows", Table: "work"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := reply.Answers[0]; len(got.Counts) != 2 || got.Counts[0] != 2 || got.Counts[1] != 1 || got.Sum != 3 {
		t.Fatalf("count: %#v", got)
	}
	if got := reply.Answers[1]; len(got.Counts) != 2 || got.Counts[0] != 1 || got.Counts[1] != 1 || got.Sum != 2 {
		t.Fatalf("rcount: %#v", got)
	}
	if got := reply.Answers[2].Rows; len(got) != 2 || got[0].Row != "alpha" || got[0].Rank != "7" || got[1].Row != "beta" || got[1].Rank != "9" {
		t.Fatalf("rows: %#v", got)
	}
	// Range bounds follow Redis's range parser, which accepts these spellings
	// even though the stricter ZADD score parser rejects them.
	hex, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "rcount", Table: "work", Cells: []string{"alpha:status"}, Min: " 0x1p+0", Max: "+infinity"}}})
	if err != nil || hex.Answers[0].Sum != 2 {
		t.Fatalf("Redis score-bound grammar: %#v, %v", hex, err)
	}
	_, err = m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "rcount", Table: "work", Cells: []string{"alpha:status"}, Min: "garbage", Max: "+inf"}}})
	requireReadCode(t, err, "REQUEST", 0)
}

func TestMemReadExactEmptyScoreBounds(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	for _, item := range []struct{ id, score string }{{"negative", "-1"}, {"zero", "0"}, {"positive", "1"}} {
		if err := m.SeedMember("read:", "work", "0", item.id, MemRecord{
			Epoch: "0", Revision: "1", Row: "alpha", Column: "status", Score: item.score,
		}); err != nil {
			t.Fatal(err)
		}
	}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{
		{Kind: "range", Table: "work", Cell: "alpha:status", Min: "", Max: "+inf", Limit: 3},
		{Kind: "range", Table: "work", Cell: "alpha:status", Min: "(", Max: "+inf", Limit: 3},
		{Kind: "rcount", Table: "work", Cells: []string{"alpha:status"}, Min: "-inf", Max: ""},
		{Kind: "rcount", Table: "work", Cells: []string{"alpha:status"}, Min: "-inf", Max: "("},
	}})
	if err != nil || len(reply.Answers) != 4 {
		t.Fatalf("empty score bounds: answers=%d err=%v", len(reply.Answers), err)
	}
	if got := reply.Answers[0].IDs; !reflect.DeepEqual(got, []string{"zero", "positive"}) {
		t.Fatalf("inclusive empty minimum: %v", got)
	}
	if got := reply.Answers[1].IDs; !reflect.DeepEqual(got, []string{"positive"}) {
		t.Fatalf("exclusive empty minimum: %v", got)
	}
	if reply.Answers[2].Sum != 2 || reply.Answers[3].Sum != 1 {
		t.Fatalf("empty maximum counts: inclusive=%d exclusive=%d", reply.Answers[2].Sum, reply.Answers[3].Sum)
	}
	for _, bad := range []string{" ", "( "} {
		_, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{
			Kind: "range", Table: "work", Cell: "alpha:status", Min: bad, Max: "+inf", Limit: 3,
		}}})
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != "REQUEST" {
			t.Fatalf("whitespace-only bound %q: %v", bad, err)
		}
	}
}

func TestMemMixedAtomicReadBudgetNoPayload(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	ids := make([]string, 129)
	for i := range ids {
		ids[i] = fmt.Sprintf("wide-%03d", i)
		if err := m.SeedMember("read:", "work", "0", ids[i], MemRecord{Epoch: "0", Revision: "1", Row: "alpha", Column: "status", Score: "1", Fields: map[string]string{"payload": strings.Repeat("x", 64<<10)}}); err != nil {
			t.Fatal(err)
		}
	}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{
		{Kind: "count", Table: "work", Cells: []string{"alpha:status"}},
		{Kind: "ids", Table: "work", IDs: ids, Fields: []string{"payload"}},
	}})
	requireReadCode(t, err, "BUDGET", 1)
	if len(reply.Answers) != 0 || reply.Status != "" {
		t.Fatalf("budget leaked partial answer: %#v", reply)
	}
}

func TestMemEncodedReplyBudgetNoPayload(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	if err := m.SeedMember("read:", "work", "0", "escaped", MemRecord{Epoch: "0", Revision: "1", Row: "alpha", Column: "status", Score: "1", Fields: map[string]string{"payload": strings.Repeat("\u0001", 64<<10)}}); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 22)
	for i := range ids {
		ids[i] = "escaped"
	}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "ids", Table: "work", IDs: ids, Fields: []string{"payload"}}}})
	requireReadCode(t, err, "BUDGET", 0)
	if len(reply.Answers) != 0 {
		t.Fatalf("encoded budget leaked answer: %#v", reply)
	}
}

func TestMemReadActiveEpochAndRetainedRows(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	if err := m.SetActiveEpoch("read:", "1"); err != nil {
		t.Fatal(err)
	}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "rows", Table: "work"}}})
	if err != nil {
		t.Fatal(err)
	}
	if reply.ActiveEpoch != "1" || len(reply.Answers[0].Rows) != 2 {
		t.Fatalf("retained epoch: %#v", reply)
	}
	_, err = m.Read(context.Background(), ReadPlan{Epoch: "2", Space: "read:", Queries: []ReadQuery{{Kind: "rows", Table: "work"}}})
	if r, ok := err.(*Refusal); !ok || r.Code != "EPOCHAHEAD" || r.Detail.ActiveEpoch != "1" {
		t.Fatalf("epoch ahead detail: %#v", err)
	}
}

func TestMemReadOneTimestampAndQueryCeiling(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	calls := 0
	m.SetClock(func() time.Time {
		calls++
		return time.UnixMilli(1000 + int64(calls))
	})
	q := ReadQuery{Kind: "rows", Table: "work"}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{q, q}})
	if err != nil || calls != 1 || reply.TimeMS != "1001" || len(reply.Answers) != 2 {
		t.Fatalf("call clock must be sampled once: %#v, calls=%d, err=%v", reply, calls, err)
	}
	tooMany := make([]ReadQuery, MaxQueries+1)
	for i := range tooMany {
		tooMany[i] = q
	}
	_, err = m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: tooMany})
	if refusal, ok := err.(*Refusal); !ok || refusal.Code != "LIMIT" {
		t.Fatalf("query ceiling: %#v", err)
	}
}

func TestMemReadTenThousandRecordsWithFiveFields(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	fields := map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"}
	ids := make([]string, 10000)
	for i := range ids {
		ids[i] = fmt.Sprintf("member-%05d", i)
		if err := m.SeedMember("read:", "work", "0", ids[i], MemRecord{Epoch: "0", Revision: "1", Row: "alpha", Column: "status", Score: "1", Fields: fields}); err != nil {
			t.Fatal(err)
		}
	}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "ids", Table: "work", IDs: ids, Fields: []string{"a", "b", "c", "d", "e"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Answers) != 1 || len(reply.Answers[0].Records) != 10000 || !reply.Complete {
		t.Fatalf("incomplete 10k record read: answers=%d, records=%d, complete=%t", len(reply.Answers), len(reply.Answers[0].Records), reply.Complete)
	}
	var counters struct{ Cell, Record, Field int64 }
	if err := json.Unmarshal(reply.Counters, &counters); err != nil {
		t.Fatal(err)
	}
	if counters.Record != 10000 || counters.Field != 50000 || counters.Cell >= 20000 {
		t.Fatalf("field payload consumed cell budget: %+v", counters)
	}
}

func TestMemCountsRowProbeCacheAndCap(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	cells := make([]string, 10000)
	for i := range cells {
		cells[i] = "alpha:status"
	}
	reply, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "count", Table: "work", Cells: cells}}})
	if err != nil || len(reply.Answers[0].Counts) != 10000 {
		t.Fatalf("cached repeated row count: answer=%d err=%v", len(reply.Answers[0].Counts), err)
	}
	var counters struct {
		Cell int64 `json:"cell"`
	}
	if err := json.Unmarshal(reply.Counters, &counters); err != nil {
		t.Fatal(err)
	}
	if counters.Cell != 10005 {
		t.Fatalf("want 4 structural probes + 1 row + 10,000 ZCARDs, got %d", counters.Cell)
	}
	tooMany := make([]string, 20000)
	for i := range tooMany {
		tooMany[i] = "alpha:status"
	}
	_, err = m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "count", Table: "work", Cells: tooMany}}})
	requireReadCode(t, err, "BUDGET", 0)
}

func TestMemReadMissingRowProbeAtCellBudget(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	before, err := m.Snapshot("read:")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		existing int
		code     string
	}{{19994, "NOROW"}, {19995, "BUDGET"}} {
		cells := make([]string, tc.existing+1)
		for i := 0; i < tc.existing; i++ {
			cells[i] = "alpha:status"
		}
		cells[tc.existing] = "missing:status"
		reply, readErr := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "count", Table: "work", Cells: cells}}})
		requireReadCode(t, readErr, tc.code, 0)
		if reply.Status != "" || len(reply.Answers) != 0 {
			t.Fatalf("%d existing cells leaked an answer: %+v", tc.existing, reply)
		}
		if tc.code == "BUDGET" {
			refusal := readErr.(*Refusal)
			if refusal.Detail.Budget != "cell" || refusal.Detail.Limit == nil || *refusal.Detail.Limit != 20000 ||
				refusal.Detail.Actual == nil || *refusal.Detail.Actual != 20001 {
				t.Fatalf("missing-row attempted probe: %+v", refusal.Detail)
			}
		}
		after, err := m.Snapshot("read:")
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("read changed state: err=%v", err)
		}
	}
}

func TestMemReadMissingPlacedRowDriftBeforeCellProbe(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	if err := m.SeedMember("read:", "work", "0", "orphaned", MemRecord{
		Epoch: "0", Revision: "1", Row: "beta", Column: "status", Score: "1",
	}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	delete(m.spaces["read:"].epochs["0"].tables["work"].rows, "beta")
	m.mu.Unlock()
	before, err := m.Snapshot("read:")
	if err != nil {
		t.Fatal(err)
	}
	cells := make([]string, 19994)
	for i := range cells {
		cells[i] = "alpha:status"
	}
	reply, readErr := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{
		{Kind: "count", Table: "work", Cells: cells},
		{Kind: "ids", Table: "work", IDs: []string{"orphaned"}, Fields: []string{}},
	}})
	requireReadCode(t, readErr, "DRIFT", 1)
	if reply.Status != "" || len(reply.Answers) != 0 {
		t.Fatalf("missing placed row leaked earlier count answer: %+v", reply)
	}
	refusal := readErr.(*Refusal)
	if len(refusal.Detail.Rows) != 1 || refusal.Detail.Rows[0] != "beta" {
		t.Fatalf("missing placed row detail: %+v", refusal.Detail)
	}
	after, err := m.Snapshot("read:")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read changed state: err=%v", err)
	}
}
