package batchmodel

import (
	"fmt"
	"strings"
	"testing"
)

func TestAcceptedWireBindsSparseFieldEndpointsAndSelection(t *testing.T) {
	t.Parallel()
	before := Snapshot{Members: map[string]Member{"m1": {Exists: true, Epoch: "1", Revision: "7", Place: &Place{Row: "r1", Column: "c1", Score: "1"}, Fields: map[string]string{"status": "ready", "token": "permit"}}}}
	raw := []byte(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":"12","operation_id":"op1","actor":"w1","members":[{"id":"m1","expect":{"revision":"7","fields":{"status":{"equals":"ready"}}},"set":{"status":"done"}}]}`)
	q, _, err := DecodeRequest(raw, before, "12", map[string]string{"m1": "7"})
	if err != nil {
		t.Fatal(err)
	}
	delta := fmt.Sprintf(`{"operation_id":"op1","digest":%q,"actor":"w1","selected_count":1,"guard_count":0,"changed_count":1,"members":[{"id":"m1","before_place":"r1:c1","after_place":"r1:c1","before_score":1.0,"after_score":1.0,"before_rev":"7","after_rev":"8","fields_set":{"status":"done"},"fields_unset":[],"fields":{"status":{"before":"ready","after":"done"}}}]}`, q.Digest)
	reply := func(text string) any { return []any{"OK", []any{"RECEIPT", "10-0", "1", "12", "13", "changed", text}} }
	r, err := DecodeAcceptedReply(reply(delta), q, before)
	if err != nil {
		t.Fatal(err)
	}
	if r.SelectedCount != 1 || r.GuardCount != 0 || r.ChangedCount != 1 || r.Members[0].After.Fields["status"] != "done" || r.Members[0].After.Fields["token"] != "permit" {
		t.Fatalf("decoded receipt lost state: %+v", r)
	}
	cases := map[string]string{
		"missing selected count":  strings.Replace(delta, `"selected_count":1,`, "", 1),
		"sparse touched omitted":  strings.Replace(delta, `"fields":{"status":{"before":"ready","after":"done"}}`, `"fields":{}`, 1),
		"before endpoint corrupt": strings.Replace(delta, `"before":"ready"`, `"before":"wrong"`, 1),
		"set echo corrupt":        strings.Replace(delta, `"fields_set":{"status":"done"}`, `"fields_set":{"status":"ready"}`, 1),
		"nullable set echo":       strings.Replace(delta, `"fields_set":{"status":"done"}`, `"fields_set":{"status":null}`, 1),
		"placed score absent":     strings.Replace(delta, `"before_score":1.0`, `"before_score":null`, 1),
		"nonempty unset object":   strings.Replace(delta, `"fields_unset":[]`, `"fields_unset":{"status":true}`, 1),
		"case alias":              strings.Replace(delta, `"actor":"w1"`, `"Actor":"w1"`, 1),
		"escaped duplicate":       strings.Replace(delta, `"actor":"w1"`, `"actor":"w1","\u0061ctor":"w1"`, 1),
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAcceptedReply(reply(wire), q, before); err == nil {
				t.Fatal("corrupt receipt was accepted")
			}
		})
	}
}

func TestAcceptedWireRequiresTouchedFieldEvenForNoop(t *testing.T) {
	t.Parallel()
	before := Snapshot{Members: map[string]Member{"m1": {Exists: true, Epoch: "1", Revision: "7", Place: &Place{Row: "r1", Column: "c1", Score: "1"}, Fields: map[string]string{"status": "ready"}}}}
	raw := []byte(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":"12","operation_id":"noop","actor":"w1","members":[{"id":"m1","expect":{"revision":"7"},"set":{"status":"ready"}}]}`)
	q, _, err := DecodeRequest(raw, before, "12", map[string]string{"m1": "7"})
	if err != nil {
		t.Fatal(err)
	}
	delta := fmt.Sprintf(`{"operation_id":"noop","digest":%q,"actor":"w1","selected_count":1,"guard_count":0,"changed_count":0,"members":[{"id":"m1","before_place":"r1:c1","after_place":"r1:c1","before_score":1,"after_score":1,"before_rev":"7","after_rev":"7","fields_set":{"status":"ready"},"fields_unset":[],"fields":{"status":{"before":"ready","after":"ready"}}}]}`, q.Digest)
	reply := []any{"OK", []any{"RECEIPT", "11-0", "1", "12", "13", "noop", delta}}
	if _, err := DecodeAcceptedReply(reply, q, before); err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(delta, `"fields":{"status":{"before":"ready","after":"ready"}}`, `"fields":{}`, 1)
	reply[1].([]any)[6] = bad
	if _, err := DecodeAcceptedReply(reply, q, before); err == nil {
		t.Fatal("no-op touch omitted its before/after evidence")
	}
}
