package batchmodel

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
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
	delta := fmt.Sprintf(`{"operation_id":"op1","digest":%q,"actor":"w1","selected_count":1,"guard_count":0,"changed_count":1,"members":[{"id":"m1","before_place":"r1:c1","after_place":"r1:c1","before_score":"1.0","after_score":"1.0","before_rev":"7","after_rev":"8","fields_set":{"status":"done"},"fields_unset":[],"fields":{"status":{"before":"ready","after":"done"}}}]}`, q.Digest)
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
		"placed score null":       strings.Replace(delta, `"before_score":"1.0"`, `"before_score":null`, 1),
		"after score null":        strings.Replace(delta, `"after_score":"1.0"`, `"after_score":null`, 1),
		"before score missing":    strings.Replace(delta, `"before_score":"1.0",`, "", 1),
		"after score missing":     strings.Replace(delta, `"after_score":"1.0",`, "", 1),
		"numeric before score":    strings.Replace(delta, `"before_score":"1.0"`, `"before_score":1.0`, 1),
		"numeric after score":     strings.Replace(delta, `"after_score":"1.0"`, `"after_score":1.0`, 1),
		"malformed score string":  strings.Replace(delta, `"before_score":"1.0"`, `"before_score":"NaN"`, 1),
		"null spelled as string":  strings.Replace(delta, `"before_score":"1.0"`, `"before_score":"null"`, 1),
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

func TestAcceptedWireVerifiesLongFieldAgainstIndependentPrestateThenRefusesDomain(t *testing.T) {
	t.Parallel()
	prior := strings.Repeat("x", receiptValueBytes+1)
	before := Snapshot{Members: map[string]Member{"m1": {Exists: true, Epoch: "1", Revision: "7", Fields: map[string]string{"status": prior}}}}
	raw := []byte(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":"12","operation_id":"long","actor":"w1","members":[{"id":"m1","expect":{"revision":"7"},"set":{"status":"done"}}]}`)
	q, _, err := DecodeRequest(raw, before, "12", map[string]string{"m1": "7"})
	if err != nil {
		t.Fatal(err)
	}
	h := sha1.Sum([]byte(prior))
	digest := hex.EncodeToString(h[:])
	delta := fmt.Sprintf(`{"operation_id":"long","digest":%q,"actor":"w1","selected_count":1,"guard_count":0,"changed_count":1,"members":[{"id":"m1","before_place":"","after_place":"","before_score":null,"after_score":null,"before_rev":"7","after_rev":"8","fields_set":{"status":"done"},"fields_unset":[],"fields":{"status":{"before":null,"before_bytes":%d,"before_sha1":%q,"after":"done"}}}]}`, q.Digest, len(prior), digest)
	reply := func(s string) any { return []any{"OK", []any{"RECEIPT", "12-0", "1", "12", "13", "changed", s}} }
	if _, err := DecodeAcceptedReply(reply(delta), q, before); err == nil || !strings.Contains(err.Error(), "outside finite model") {
		t.Fatalf("verified long value must be refused by finite projection: %v", err)
	}
	for name, bad := range map[string]string{
		"wrong digest":         strings.Replace(delta, digest, strings.Repeat("0", 40), 1),
		"wrong length":         strings.Replace(delta, `"before_bytes":65`, `"before_bytes":66`, 1),
		"missing digest":       strings.Replace(delta, `,"before_sha1":`+fmt.Sprintf("%q", digest), "", 1),
		"mixed raw and digest": strings.Replace(delta, `"before":null`, `"before":"`+prior+`"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAcceptedReply(reply(bad), q, before); err == nil || strings.Contains(err.Error(), "outside finite model") {
				t.Fatalf("bad hash evidence must fail before domain refusal: %v", err)
			}
		})
	}
}

func TestAcceptedWireKeepsFieldAt64ByteBoundaryInFull(t *testing.T) {
	t.Parallel()
	prior := strings.Repeat("x", receiptValueBytes)
	before := Snapshot{Members: map[string]Member{"m1": {Exists: true, Epoch: "1", Revision: "7", Fields: map[string]string{"status": prior}}}}
	raw := []byte(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":"12","operation_id":"edge","actor":"w1","members":[{"id":"m1","expect":{"revision":"7"},"set":{"status":"done"}}]}`)
	q, _, err := DecodeRequest(raw, before, "12", map[string]string{"m1": "7"})
	if err != nil {
		t.Fatal(err)
	}
	delta := fmt.Sprintf(`{"operation_id":"edge","digest":%q,"actor":"w1","selected_count":1,"guard_count":0,"changed_count":1,"members":[{"id":"m1","before_place":"","after_place":"","before_score":null,"after_score":null,"before_rev":"7","after_rev":"8","fields_set":{"status":"done"},"fields_unset":{},"fields":{"status":{"before":%q,"after":"done"}}}]}`, q.Digest, prior)
	reply := func(s string) any { return []any{"OK", []any{"RECEIPT", "12-0", "1", "12", "13", "changed", s}} }
	if _, err := DecodeAcceptedReply(reply(delta), q, before); err != nil {
		t.Fatalf("64-byte field must be carried in full: %v", err)
	}
	h := sha1.Sum([]byte(prior))
	compact := strings.Replace(delta, fmt.Sprintf(`"before":%q`, prior), fmt.Sprintf(`"before":null,"before_bytes":64,"before_sha1":%q`, hex.EncodeToString(h[:])), 1)
	if _, err := DecodeAcceptedReply(reply(compact), q, before); err == nil {
		t.Fatal("compact hash at 64 bytes accepted")
	}
	if _, err := DecodeAcceptedReply([]any{"OK", []any{"RECEIPT", "12-0", "1", "12", "13", "changed", delta}, "REPLAY"}, q, before); err == nil {
		t.Fatal("replay marker accepted as a new receipt")
	}
}

func TestSparseLongSetEchoUsesHashOnly(t *testing.T) {
	t.Parallel()
	value := strings.Repeat("y", receiptValueBytes+1)
	h := sha1.Sum([]byte(value))
	size := json.Number(fmt.Sprint(len(value)))
	digest := hex.EncodeToString(h[:])
	before := "ready"
	w := wireMemberDelta{FieldsSet: map[string]string{}, Fields: map[string]wireFieldChange{
		"status": {Before: &before, AfterBytes: &size, AfterSHA1: &digest},
	}}
	entry := MemberAction{SetField: "status", SetValue: value}
	observed := Member{Fields: map[string]string{"status": before}}
	long, err := checkSparseFields(w, nil, entry, observed)
	if err != nil || !long {
		t.Fatalf("long set with hash evidence: long=%t err=%v", long, err)
	}
	w.FieldsSet["status"] = value
	if _, err := checkSparseFields(w, nil, entry, observed); err == nil {
		t.Fatal("long set value echoed in fields_set")
	}
	delete(w.FieldsSet, "status")
	change := w.Fields["status"]
	wrong := strings.Repeat("0", 40)
	change.AfterSHA1 = &wrong
	w.Fields["status"] = change
	if _, err := checkSparseFields(w, nil, entry, observed); err == nil {
		t.Fatal("corrupt long set hash accepted")
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
	delta := fmt.Sprintf(`{"operation_id":"noop","digest":%q,"actor":"w1","selected_count":1,"guard_count":0,"changed_count":0,"members":[{"id":"m1","before_place":"r1:c1","after_place":"r1:c1","before_score":"1","after_score":"1","before_rev":"7","after_rev":"7","fields_set":{"status":"ready"},"fields_unset":[],"fields":{"status":{"before":"ready","after":"ready"}}}]}`, q.Digest)
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

func TestAcceptedWireAllowsNullScoreOnlyForUnplacedMember(t *testing.T) {
	t.Parallel()
	before := Snapshot{Members: map[string]Member{"m1": {Exists: true, Epoch: "1", Revision: "7", Fields: map[string]string{"status": "ready"}}}}
	raw := []byte(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":"12","operation_id":"unplaced","actor":"w1","members":[{"id":"m1","expect":{"revision":"7"},"set":{"status":"done"}}]}`)
	q, _, err := DecodeRequest(raw, before, "12", map[string]string{"m1": "7"})
	if err != nil {
		t.Fatal(err)
	}
	delta := fmt.Sprintf(`{"operation_id":"unplaced","digest":%q,"actor":"w1","selected_count":1,"guard_count":0,"changed_count":1,"members":[{"id":"m1","before_place":"","after_place":"","before_score":null,"after_score":null,"before_rev":"7","after_rev":"8","fields_set":{"status":"done"},"fields_unset":[],"fields":{"status":{"before":"ready","after":"done"}}}]}`, q.Digest)
	reply := func(s string) any { return []any{"OK", []any{"RECEIPT", "12-0", "1", "12", "13", "changed", s}} }
	if _, err := DecodeAcceptedReply(reply(delta), q, before); err != nil {
		t.Fatal(err)
	}
	for name, corrupt := range map[string]string{
		"missing null field": strings.Replace(delta, `"before_score":null,`, "", 1),
		"quoted null":        strings.Replace(delta, `"before_score":null`, `"before_score":"null"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAcceptedReply(reply(corrupt), q, before); err == nil {
				t.Fatal("malformed unplaced score accepted")
			}
		})
	}
}
