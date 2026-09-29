package batchmodel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeCanonicalBindsEveryModelGuardAndMutation(t *testing.T) {
	t.Parallel()
	before := Snapshot{Members: map[string]Member{
		"m1": {Exists: true, Revision: "7", Place: &Place{Row: "r1", Column: "c1", Score: "1"}, Fields: map[string]string{"status": "ready"}},
		"m3": {Exists: true, Revision: "2", Place: &Place{Row: "r1", Column: "c2", Score: "2"}, Fields: map[string]string{"token": "permit"}},
	}}
	raw := []byte(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":"12","operation_id":"op1","actor":"w1","members":[{"id":"m1","expect":{"place":{"row":"r1","col":"c1"},"fields":{"status":{"equals":"ready"}}},"remove":true,"unset":["status"]},{"id":"m3","expect":{"revision":"2","fields":{"token":{"one_of":["permit","ready"]}}}}]}`)
	q, a, err := DecodeRequest(raw, before, "12", map[string]string{"m1": "7", "m3": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if q.ExpectedRevision["m1"] != nil || q.ExpectedRevision["m3"] == nil || a.Members[0].Revision != nil || a.Members[1].Revision == nil || *a.Members[1].Revision != 0 {
		t.Fatalf("revision guards lost: %+v %+v", q.ExpectedRevision, a.Members)
	}
	if !a.Members[0].RemoveSupplied || !a.Members[0].Remove || a.Members[0].UnsetField != "status" || a.Members[1].Change {
		t.Fatalf("mutation/guard classification lost: %+v", a.Members)
	}
	if a.Members[1].GuardKind != "one_of" || len(a.Members[1].GuardValues) != 2 {
		t.Fatalf("guard lost: %+v", a.Members[1])
	}
	if _, err := a.TLA(); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeCanonicalRejectsEscapedDuplicateKeysAndUnknownNestedFields(t *testing.T) {
	t.Parallel()
	base := `{"schema":1,"table":"t1","epoch":"1","expected_table_revision":"12","operation_id":"op1","actor":"w1","members":[{"id":"m1","expect":{"revision":"7"}}]}`
	before := Snapshot{Members: map[string]Member{"m1": {Exists: true, Revision: "7", Place: &Place{Row: "r1", Column: "c1", Score: "1"}}}}
	for name, raw := range map[string]string{
		"escaped root duplicate":   strings.Replace(base, `"actor":"w1"`, `"actor":"w1","\u0061ctor":"w1"`, 1),
		"escaped nested duplicate": strings.Replace(base, `"revision":"7"`, `"absent":true,"\u0061bsent":true,"revision":"7"`, 1),
		"unknown nested field":     strings.Replace(base, `"revision":"7"`, `"revision":"7","surprise":1`, 1),
		"unknown member field":     strings.Replace(base, `"id":"m1"`, `"id":"m1","surprise":1`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if !json.Valid([]byte(raw)) {
				t.Fatal("test input is not valid JSON")
			}
			if _, _, err := DecodeRequest([]byte(raw), before, "12", map[string]string{"m1": "7"}); err == nil {
				t.Fatal("invalid canonical request accepted")
			} else if strings.HasPrefix(name, "escaped") && !strings.Contains(err.Error(), "duplicate JSON key") {
				t.Fatalf("error %v did not prove decoded duplicate detection", err)
			}
		})
	}
}

func TestDecodeCanonicalRefusesToDropUnmodelledGuards(t *testing.T) {
	t.Parallel()
	base := `{"schema":1,"table":"t1","epoch":"1","expected_table_revision":"12","operation_id":"op1","actor":"w1","members":[{"id":"m1","expect":{"revision":"7","fields":{"status":{"equals":"ready"}}}}]}`
	before := Snapshot{Members: map[string]Member{"m1": {Exists: true, Revision: "7", Place: &Place{Row: "r1", Column: "c1", Score: "1"}}}}
	for name, raw := range map[string]string{
		"second guard":      strings.Replace(base, `"status":{"equals":"ready"}`, `"status":{"equals":"ready"},"token":{"absent":true}`, 1),
		"second set":        `{"schema":1,"table":"t1","epoch":"1","expected_table_revision":"12","operation_id":"op1","actor":"w1","members":[{"id":"m1","expect":{"revision":"7","fields":{"status":{"equals":"ready"}}},"set":{"status":"done","token":"new"}}]}`,
		"unsupported score": strings.Replace(base, `"id":"m1"`, `"id":"m1","move":{"row":"r2","col":"c1","score":1.5}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if !json.Valid([]byte(raw)) {
				t.Fatal("test input is not valid JSON")
			}
			if _, _, err := DecodeRequest([]byte(raw), before, "12", map[string]string{"m1": "7"}); err == nil {
				t.Fatal("unmodelled condition was dropped")
			}
		})
	}
}

func TestDecodeCanonicalRejectsCaseAliasesAndNulls(t *testing.T) {
	t.Parallel()
	base := `{"schema":1,"table":"t1","epoch":"1","expected_table_revision":"12","operation_id":"op1","actor":"w1","members":[{"id":"m1","expect":{"revision":"7","fields":{"status":{"equals":"ready"}}}}]}`
	before := Snapshot{Members: map[string]Member{"m1": {Exists: true, Revision: "7", Place: &Place{Row: "r1", Column: "c1", Score: "1"}}}}
	cases := map[string]string{
		"root case alias":       strings.Replace(base, `"actor":"w1"`, `"Actor":"w1"`, 1),
		"case folded duplicate": strings.Replace(base, `"actor":"w1"`, `"actor":"w1","Actor":"w1"`, 1),
		"nested case alias":     strings.Replace(base, `"revision":"7"`, `"Revision":"7"`, 1),
		"null revision":         strings.Replace(base, `"revision":"7"`, `"revision":null`, 1),
		"null place":            strings.Replace(base, `"revision":"7"`, `"revision":"7","place":null`, 1),
		"null expect":           strings.Replace(base, `"expect":{"revision":"7","fields":{"status":{"equals":"ready"}}}`, `"expect":null`, 1),
		"null move":             strings.Replace(base, `"id":"m1"`, `"id":"m1","move":null`, 1),
		"null remove":           strings.Replace(base, `"id":"m1"`, `"id":"m1","remove":null`, 1),
		"null set":              strings.Replace(base, `"id":"m1"`, `"id":"m1","set":null`, 1),
		"null unset":            strings.Replace(base, `"id":"m1"`, `"id":"m1","unset":null`, 1),
		"null set value":        strings.Replace(base, `"id":"m1"`, `"id":"m1","set":{"status":null}`, 1),
		"null one_of item":      strings.Replace(base, `"equals":"ready"`, `"one_of":[null]`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if !json.Valid([]byte(raw)) {
				t.Fatal("invalid test input")
			}
			if _, _, err := DecodeRequest([]byte(raw), before, "12", map[string]string{"m1": "7"}); err == nil {
				t.Fatal("wire alias or null silently changed action")
			}
		})
	}
}
