package main

import (
	"encoding/json"
	"fmt"
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

// The CLI checks v2 manifests before a store call. A refusal describes this
// attempt only: an older operation with the same ID may already have committed.
func TestBatchSchema2PreSendRefusalsSayThisCallOnly(t *testing.T) {
	t.Parallel()
	header := `{"schema":2,"scope":"release","operation_id":"op-1","tables":[{"name":"a","epoch":"0","expected_table_revision":"1"},{"name":"b","epoch":"0","expected_table_revision":"1"}],"members":[`
	manifest := func(member string) string { return header + member + `]}` }
	var fields []string
	for i := 0; i <= ntable.LimitSetFields; i++ {
		fields = append(fields, fmt.Sprintf(`"k%d":"v"`, i))
	}
	cases := []struct {
		name, raw string
		args      []string
		exit      int
		code      string
	}{
		{"a bound", manifest(`{"record_table":"a","id":"m","expect":{},"set":{` + strings.Join(fields, ",") + `}}`), nil, 1, "code=LIMIT"},
		{"a rule", manifest(`{"record_table":"a","id":"m","expect":{},"set":{"x":"1"},"unset":["x"]}`), nil, 1, "code=MUTATION"},
		{"a manifest error", manifest(`{"record_table":"a","id":"m","expect":{"absent":true,"places":[{"table":"a","absent":true}]},"placements":[{"table":"a","add":{"row":"work","col":"ready","score":"1"}}]}`), nil, 2, "invalid batch manifest"},
		{"epoch flag", manifest(`{"record_table":"a","id":"m","expect":{}}`), []string{"--epoch", "4"}, 2, `--epoch 4 differs from table "a"`},
		{"actor flag", strings.Replace(manifest(`{"record_table":"a","id":"m","expect":{}}`), `"operation_id":"op-1"`, `"operation_id":"op-1","actor":"original"`, 1), []string{"--actor", "other"}, 2, `--actor "other" differs`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := append([]string{"batch", "--redis", "127.0.0.1:1"}, tc.args...)
			args = append(args, tc.raw)
			code, stdout, stderr := runTable(args...)
			if code != tc.exit || stdout != "" || !strings.Contains(stderr, tc.code) {
				t.Fatalf("exit=%d stdout=%q stderr=%q; want exit=%d and %q", code, stdout, stderr, tc.exit, tc.code)
			}
			if !strings.Contains(stderr, ntable.CheckedBeforeSending) || !strings.Contains(stderr, "changed=no") {
				t.Fatalf("v2 refusal lacks the checked-before-sending sentence: %q", stderr)
			}
		})
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
