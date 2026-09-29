package batchmodel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReplayRequiresExactMarkerAndDurableOriginal(t *testing.T) {
	t.Parallel()
	before := Snapshot{Members: map[string]Member{"m1": {Exists: true, Epoch: "1", Revision: "7", Fields: map[string]string{"status": "ready"}}}}
	raw := []byte(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":"12","operation_id":"retry","actor":"w1","members":[{"id":"m1","expect":{"revision":"7"},"set":{"status":"done"}}]}`)
	q, _, err := DecodeRequest(raw, before, "12", map[string]string{"m1": "7"})
	if err != nil {
		t.Fatal(err)
	}
	delta := fmt.Sprintf(`{"operation_id":"retry","digest":%q,"actor":"w1","selected_count":1,"guard_count":0,"changed_count":1,"members":[{"id":"m1","before_place":"","after_place":"","before_score":null,"after_score":null,"before_rev":"7","after_rev":"8","fields_set":{"status":"done"},"fields_unset":{},"fields":{"status":{"before":"ready","after":"done"}}}]}`, q.Digest)
	original := []any{"OK", []any{"RECEIPT", "10-0", "1", "12", "13", "changed", delta}}
	receipt, err := DecodeAcceptedReply(original, q, before)
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	record := OperationRecord{Table: q.Table, Epoch: q.Epoch, OperationID: q.OperationID, Digest: q.Digest,
		ReceiptID: receipt.StreamID, BeforeRevision: receipt.BeforeRevision, AfterRevision: receipt.AfterRevision,
		Outcome: receipt.Kind, Canonical: q.Canonical, ResultJSON: result}
	prior := AcceptedEvidence{Request: q, Before: before, Receipt: *receipt, Record: record}
	replay := []any{"OK", original[1], "REPLAY"}
	if got, err := decodeReplayedReply(replay, record, prior); err != nil || got == nil || got.StreamID != receipt.StreamID {
		t.Fatalf("exact replay failed: %+v, %v", got, err)
	}
	for name, bad := range map[string][]any{
		"missing marker":  {"OK", original[1]},
		"wrong marker":    {"OK", original[1], "REPLAYED"},
		"changed payload": {"OK", []any{"RECEIPT", "other", "1", "12", "13", "changed", delta}, "REPLAY"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeReplayedReply(bad, record, prior); err == nil {
				t.Fatal("altered replay accepted")
			}
		})
	}
	corrupt := record
	corrupt.ResultJSON = []byte(strings.Replace(string(result), "10-0", "other", 1))
	if _, err := decodeReplayedReply(replay, corrupt, prior); err == nil {
		t.Fatal("altered durable result accepted")
	}
}
