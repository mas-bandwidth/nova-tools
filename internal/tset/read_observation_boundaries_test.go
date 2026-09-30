//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A control byte is six bytes in both the stored receipt JSON and Go's read
// reply JSON; an ASCII x is one. These legal values make each byte target
// attainable while their unencoded result stays within the 4 KiB input cap.
func observationEncodedPayload(t *testing.T, encodedBytes, rawLimit int) string {
	t.Helper()
	controls, plain := encodedBytes/6, encodedBytes%6
	if encodedBytes < 0 || controls+plain > rawLimit {
		t.Fatalf("cannot encode %d bytes within %d raw bytes", encodedBytes, rawLimit)
	}
	return strings.Repeat("\x01", controls) + strings.Repeat("x", plain)
}

func observationFetched(t *testing.T, reply ReadReply) int64 {
	t.Helper()
	var counters struct {
		FetchedBytes int64 `json:"fetched_bytes"`
	}
	if err := json.Unmarshal(reply.Counters, &counters); err != nil {
		t.Fatalf("decode read counters: %v", err)
	}
	return counters.FetchedBytes
}

func observationBudgetRefusal(t *testing.T, reply ReadReply, err error,
	budget string, limit, actual int64, query int) {
	t.Helper()
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != "BUDGET" ||
		refusal.Detail.Budget != budget || refusal.Detail.QueryIndex == nil ||
		*refusal.Detail.QueryIndex != query {
		t.Fatalf("want BUDGET/%s at query %d; status=%q answers=%d err=%v",
			budget, query, reply.Status, len(reply.Answers), err)
	}
	// Revision 4 allows these diagnostics to be omitted. When an
	// implementation supplies them, they must describe this exact edge.
	if refusal.Detail.Limit != nil && *refusal.Detail.Limit != limit {
		t.Fatalf("BUDGET/%s limit=%d, want %d", budget, *refusal.Detail.Limit, limit)
	}
	if refusal.Detail.Actual != nil && *refusal.Detail.Actual != actual {
		t.Fatalf("BUDGET/%s actual=%d, want %d", budget, *refusal.Detail.Actual, actual)
	}
	if reply.Status != "" || len(reply.Answers) != 0 {
		t.Fatalf("BUDGET/%s leaked an answer: status=%q answers=%d",
			budget, reply.Status, len(reply.Answers))
	}
}

func TestMemReadExactFetchedBytesBoundary(t *testing.T) {
	m := readFixture(t)
	ctx := context.Background()
	wrongDigest := strings.Repeat("0", 40)
	plan := func(op string) ReadPlan {
		return ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{
			Kind: "done", Ops: []DoneIdentity{{Epoch: "0", Op: op, IntentDigest: wrongDigest}},
		}}}
	}
	baseReply, err := m.Read(ctx, plan("absent"))
	if err != nil || len(baseReply.Answers) != 1 ||
		len(baseReply.Answers[0].Done) != 1 || baseReply.Answers[0].Done[0].Status != "absent" {
		t.Fatalf("absent receipt baseline: reply=%+v err=%v", baseReply, err)
	}
	base := observationFetched(t, baseReply)
	seedReceipt := func(op, result string) {
		t.Helper()
		intent := "i"
		if _, err := m.Step(ctx, Step{Epoch: "0", Space: "read:", Op: &op,
			Intent: &intent, Result: result, Entries: []Entry{}}); err != nil {
			t.Fatalf("seed receipt %q: %v", op, err)
		}
	}
	seedReceipt("cal", "")
	calibration, err := m.Read(ctx, plan("cal"))
	if err != nil || len(calibration.Answers) != 1 ||
		len(calibration.Answers[0].Done) != 1 ||
		calibration.Answers[0].Done[0].Status != "conflict" {
		t.Fatalf("conflicting receipt calibration: reply=%+v err=%v", calibration, err)
	}
	storedFixed := observationFetched(t, calibration) - base
	if storedFixed <= 0 {
		t.Fatalf("receipt fetch did not increase raw bytes: fixed=%d", storedFixed)
	}

	// Each conflicting identity fetches its complete saved receipt but emits
	// only a small conflict slot. A single query stays below 2,000 identities
	// and 20,000 probes, so neither cap hides the 8 MiB fetched-byte edge.
	var count, lowSize, highCount int
	for n := 350; n <= 400; n++ {
		perReceipt := (MaxFetchedBytes - int(base)) / n
		remainder := (MaxFetchedBytes - int(base)) % n
		lowResultBytes := perReceipt - int(storedFixed)
		if remainder > 0 && remainder < n-1 && lowResultBytes >= 0 &&
			lowResultBytes/6+lowResultBytes%6 <= MaxResultBytes &&
			(lowResultBytes+1)/6+(lowResultBytes+1)%6 <= MaxResultBytes {
			count, lowSize, highCount = n, perReceipt, remainder
			break
		}
	}
	if count == 0 {
		t.Fatalf("no legal receipt mix reaches fetched-byte cap from base=%d fixed=%d", base, storedFixed)
	}
	seedReceipt("low", observationEncodedPayload(t, lowSize-int(storedFixed), MaxResultBytes))
	seedReceipt("high", observationEncodedPayload(t, lowSize+1-int(storedFixed), MaxResultBytes))
	before, err := m.Snapshot("read:")
	if err != nil {
		t.Fatal(err)
	}
	for _, delta := range []int{-1, 0, 1} {
		t.Run(fmt.Sprintf("cap%+d", delta), func(t *testing.T) {
			ops := make([]DoneIdentity, count)
			for i := range ops {
				op := "low"
				if i < highCount+delta {
					op = "high"
				}
				// The distinct wrong digests keep every identity legal in one query.
				ops[i] = DoneIdentity{Epoch: "0", Op: op,
					IntentDigest: fmt.Sprintf("%040x", i+1)}
			}
			read, readErr := m.Read(ctx, ReadPlan{Epoch: "0", Space: "read:",
				Queries: []ReadQuery{{Kind: "done", Ops: ops}}})
			if delta == 1 {
				observationBudgetRefusal(t, read, readErr, "fetched_bytes",
					MaxFetchedBytes, MaxFetchedBytes+1, 0)
			} else {
				gotFetched := int64(-1)
				if readErr == nil {
					gotFetched = observationFetched(t, read)
				}
				if readErr != nil || !read.Complete || len(read.Answers) != 1 ||
					len(read.Answers[0].Done) != count ||
					gotFetched != int64(MaxFetchedBytes+delta) {
					t.Fatalf("fetched bytes cap%+d: answers=%d fetched=%d err=%v",
						delta, len(read.Answers), gotFetched, readErr)
				}
				for i, slot := range read.Answers[0].Done {
					if slot.Status != "conflict" {
						t.Fatalf("slot %d was %q, want conflict", i, slot.Status)
					}
				}
			}
			requireBoundaryState(t, m, before)
		})
	}
}

func TestMemReadExactFieldOccurrenceBoundary(t *testing.T) {
	m := readFixture(t)
	if err := m.SeedMember("read:", "work", "0", "one", MemRecord{
		Epoch: "0", Revision: "1",
	}); err != nil {
		t.Fatal(err)
	}
	before, err := m.Snapshot("read:")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 9999)
	for i := range ids {
		ids[i] = "one"
	}
	allFields := make([]string, MaxFieldsPerMember)
	for i := range allFields {
		allFields[i] = "absent"
	}
	for _, finalFields := range []int{MaxFieldsPerMember - 1, MaxFieldsPerMember} {
		read, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:",
			Queries: []ReadQuery{
				{Kind: "ids", Table: "work", IDs: ids, Fields: allFields},
				{Kind: "ids", Table: "work", IDs: []string{"one"}, Fields: allFields[:finalFields]},
			}})
		want := int64(9999*MaxFieldsPerMember + finalFields)
		if err != nil || !read.Complete || len(read.Answers) != 2 ||
			len(read.Answers[0].Records) != 9999 || len(read.Answers[1].Records) != 1 {
			t.Fatalf("%d field occurrences: answers=%d err=%v", want, len(read.Answers), err)
		}
		var counters struct {
			Field int64 `json:"field"`
		}
		if err := json.Unmarshal(read.Counters, &counters); err != nil || counters.Field != want {
			t.Fatalf("%d field occurrences charged %+v: %v", want, counters, err)
		}
		requireBoundaryState(t, m, before)
	}
	// A public read cannot present the +1 field occurrence: each projected
	// record has at most 128 fields and the shared record cap is 10,000, so
	// 10,000 × 128 = 1,280,000 is the largest legal field count. The next
	// record or the 129th field is refused at its earlier hard cap.
}

func observationReplyFixture(t *testing.T, tail string) (*Mem, ReadPlan) {
	t.Helper()
	m := readFixture(t)
	for _, item := range []struct{ id, value string }{
		{"big", strings.Repeat("\x01", MaxFieldValueBytes)}, {"tail", tail},
	} {
		if err := m.SeedMember("read:", "work", "0", item.id, MemRecord{
			Epoch: "0", Revision: "1", Fields: map[string]string{"payload": item.value},
		}); err != nil {
			t.Fatal(err)
		}
	}
	ids := make([]string, 22)
	for i := range ids[:21] {
		ids[i] = "big"
	}
	ids[21] = "tail"
	return m, ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{
		Kind: "ids", Table: "work", IDs: ids, Fields: []string{"payload"},
	}}}
}

func TestMemReadExactEncodedReplyBoundary(t *testing.T) {
	baseline, plan := observationReplyFixture(t, "")
	baseReply, err := baseline.Read(context.Background(), plan)
	if err != nil {
		t.Fatalf("baseline reply: %v", err)
	}
	baseWire, err := json.Marshal(baseReply)
	if err != nil {
		t.Fatal(err)
	}
	needed := MaxReadReplyBytes - len(baseWire)
	if needed <= 1 {
		t.Fatalf("baseline %d bytes leaves no room for tunable final record", len(baseWire))
	}
	for _, delta := range []int{-1, 0, 1} {
		t.Run(fmt.Sprintf("cap%+d", delta), func(t *testing.T) {
			tail := observationEncodedPayload(t, needed+delta, MaxFieldValueBytes)
			m, plan := observationReplyFixture(t, tail)
			before, err := m.Snapshot("read:")
			if err != nil {
				t.Fatal(err)
			}
			read, readErr := m.Read(context.Background(), plan)
			if delta == 1 {
				observationBudgetRefusal(t, read, readErr, "encoded_reply",
					MaxReadReplyBytes, MaxReadReplyBytes+1, 0)
			} else {
				if readErr != nil || !read.Complete || len(read.Answers) != 1 ||
					len(read.Answers[0].Records) != 22 {
					t.Fatalf("reply cap%+d: complete=%t answers=%d err=%v",
						delta, read.Complete, len(read.Answers), readErr)
				}
				wire, err := json.Marshal(read)
				if err != nil || len(wire) != MaxReadReplyBytes+delta {
					t.Fatalf("reply cap%+d encoded %d bytes, err=%v", delta, len(wire), err)
				}
			}
			requireBoundaryState(t, m, before)
		})
	}
}
