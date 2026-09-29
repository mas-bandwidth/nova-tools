package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

func TestBatchSchema2PreflightsEpochAcrossEveryParticipant(t *testing.T) {
	t.Parallel()
	raw := `{"schema":2,"scope":"s","operation_id":"op","tables":[{"name":"a","epoch":"4","expected_table_revision":"1"},{"name":"b","epoch":"5","expected_table_revision":"2"}],"members":[{"id":"m","record_table":"a","expect":{"absent":true}}]}`
	code, stdout, stderr := runTable("batch", "--redis", "127.0.0.1:1", "--epoch", "4", raw)
	if code != 2 || stdout != "" || !strings.Contains(stderr, `--epoch 4 differs from table "b"'s epoch "5"`) || !strings.Contains(stderr, "changed=no") {
		t.Fatalf("got code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestBatchSchema2UsesStrictRawValidatorForDuplicateSchema(t *testing.T) {
	t.Parallel()
	raw := `{"schema":1,"schema":2,"scope":"s","operation_id":"op","tables":[],"members":[]}`
	code, stdout, stderr := runTable("batch", "--redis", "127.0.0.1:1", raw)
	if code != 2 || stdout != "" || !strings.Contains(stderr, `duplicate key "schema" in root`) {
		t.Fatalf("got code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestMultiBatchJSONKeepsHashEvidenceAndAllTableRevisions(t *testing.T) {
	t.Parallel()
	delta := ntable.MultiBatchDelta{
		Schema: 2, Scope: "scope", OperationID: "operation", Digest: "sha256", Outcome: "changed",
		Tables: []ntable.MultiBatchTableDelta{{Name: "one", Epoch: "3", RevBefore: "8", RevAfter: "9"}, {Name: "two", Epoch: "4", RevBefore: "2", RevAfter: "3"}},
		Members: []ntable.MultiBatchMemberDelta{{RecordTable: "one", ID: "m", BeforeRev: "4", AfterRev: "5", Fields: map[string]ntable.FieldChange{
			"note": {BeforeBytes: 700, BeforeSHA1: "before-hash", AfterBytes: 701, AfterSHA1: "after-hash"},
		}}},
	}
	got := multiBatchJSON(delta, ntable.MultiBatchReceipt{ID: "1-0", Replay: true}, 7)
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"digest":"sha256"`, `"replay":true`, `"name":"one"`, `"name":"two"`, `"bytes":700`, `"sha1":"before-hash"`, `"bytes":701`, `"sha1":"after-hash"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON receipt is missing %s: %s", want, b)
		}
	}
}
