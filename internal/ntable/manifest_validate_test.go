package ntable_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

func TestValidateBatchManifestRaw(t *testing.T) {
	t.Parallel()

	validJSON := `{
		"schema": 1,
		"table": "demo",
		"epoch": "0",
		"expected_table_revision": "2",
		"operation_id": "op-1",
		"actor": "user",
		"members": [
			{
				"id": "m1",
				"expect": {"absent": true},
				"create": {"row": "build", "col": "ready", "score": 10},
				"set": {"role": "builder"}
			},
			{
				"id": "m2",
				"expect": {"revision": "1"},
				"remove": true
			}
		]
	}`

	manifest, err := ntable.ValidateBatchManifestRaw([]byte(validJSON))
	if err != nil {
		t.Fatalf("expected valid manifest, got error: %v", err)
	}
	if manifest.Table != "demo" {
		t.Errorf("expected table demo, got %s", manifest.Table)
	}
	if len(manifest.Members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(manifest.Members))
	}
	if !manifest.Members[1].Remove {
		t.Errorf("expected member m2 remove=true")
	}

	tests := []struct {
		name      string
		raw       string
		errSubstr string
	}{
		{
			name:      "empty manifest",
			raw:       "",
			errSubstr: "empty manifest",
		},
		{
			name:      "whitespace only",
			raw:       "   \t\n  ",
			errSubstr: "empty manifest",
		},
		{
			name:      "array instead of object",
			raw:       `[{"id":"m1"}]`,
			errSubstr: "manifest must be a JSON object",
		},
		{
			name:      "duplicate key top level",
			raw:       `{"schema":1,"table":"demo","table":"demo2"}`,
			errSubstr: `duplicate key "table" in manifest`,
		},
		{
			name:      "duplicate key nested member",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m1","id":"m2"}]}`,
			errSubstr: `duplicate key "id" in manifest`,
		},
		{
			name:      "duplicate key in set map",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m1","set":{"role":"a","role":"b"}}]}`,
			errSubstr: `duplicate key "role" in manifest`,
		},
		{
			name:      "unknown field top level",
			raw:       `{"schema":1,"table":"demo","unexpected":123}`,
			errSubstr: `unknown field "unexpected"`,
		},
		{
			name:      "unknown field in member",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m1","unknown_prop":true}]}`,
			errSubstr: `unknown field "unknown_prop"`,
		},
		{
			name:      "remove false",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m1","remove":false}]}`,
			errSubstr: `remove must be true`,
		},
		{
			name:      "remove non-bool number",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m1","remove":1}]}`,
			errSubstr: `remove must be true`,
		},
		{
			name:      "remove non-bool string",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m1","remove":"true"}]}`,
			errSubstr: `remove must be true`,
		},
		{
			name:      "remove non-bool object",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m1","remove":{}}]}`,
			errSubstr: `remove must be true`,
		},
		{
			name:      "trailing non-whitespace content",
			raw:       `{"schema":1,"table":"demo"} extra`,
			errSubstr: "unexpected trailing content after JSON manifest",
		},
		{
			name:      "trailing second json object",
			raw:       `{"schema":1,"table":"demo"} {"schema":2}`,
			errSubstr: "unexpected trailing content after JSON manifest",
		},
		{
			name:      "duplicate member id in same manifest",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m1"},{"id":"m1"}]}`,
			errSubstr: `duplicate member id "m1" in manifest`,
		},
		{
			name:      "empty member id",
			raw:       `{"schema":1,"table":"demo","members":[{"id":""}]}`,
			errSubstr: "member id cannot be empty",
		},
		{
			name:      "member id with control characters",
			raw:       "{\"schema\":1,\"table\":\"demo\",\"members\":[{\"id\":\"m1\\n\"}]}",
			errSubstr: "invalid member id",
		},
		{
			name:      "stella case 1: uppercase REMOVE false rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","REMOVE":false,"set":{"status":"done"}}]}`,
			errSubstr: `unknown field "REMOVE" in member object`,
		},
		{
			name:      "stella case 2: null value in set rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","set":{"status":null}}]}`,
			errSubstr: `null value not allowed in set for field "status"`,
		},
		{
			name:      "stella case 3: case variant duplicate actor and Actor rejected",
			raw:       `{"schema":1,"table":"demo","actor":"a","Actor":"b","members":[{"id":"m"}]}`,
			errSubstr: `duplicate key "Actor" in manifest`,
		},
		{
			name:      "stella case A: null revision rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":{"revision":null}}]}`,
			errSubstr: `null value not allowed for revision`,
		},
		{
			name:      "stella case B: null create score rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","create":{"row":"r","col":"c","score":null}}]}`,
			errSubstr: `null value not allowed for score`,
		},
		{
			name:      "stella case C: null equals guard rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":{"fields":{"status":{"equals":null,"one_of":["ready"]}}}}]}`,
			errSubstr: `null value not allowed for equals`,
		},
		{
			name:      "type mismatch: non-number revision",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":{"revision":"abc"}}]}`,
			errSubstr: `expected number for revision`,
		},
		{
			name:      "type mismatch: non-string row",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","create":{"row":123,"col":"c","score":10}}]}`,
			errSubstr: `expected string for row`,
		},
		{
			name:      "type mismatch: non-string col",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","create":{"row":"r","col":true,"score":10}}]}`,
			errSubstr: `expected string for col`,
		},
		{
			name:      "type mismatch: non-number score",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","create":{"row":"r","col":"c","score":"ten"}}]}`,
			errSubstr: `expected number for score`,
		},
		{
			name:      "type mismatch: non-boolean remove",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","remove":"true"}]}`,
			errSubstr: `remove must be true`,
		},
		{
			name:      "type mismatch: non-boolean absent",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":{"absent":"true"}}]}`,
			errSubstr: `expected boolean for absent`,
		},
		{
			name:      "type mismatch: non-object expect",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":"invalid"}]}`,
			errSubstr: `expected object for expect`,
		},
		{
			name:      "type mismatch: non-array members",
			raw:       `{"schema":1,"table":"demo","members":{"id":"m"}}`,
			errSubstr: `expected array for members`,
		},
		{
			name:      "type mismatch: non-array unset",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","unset":"field"}]}`,
			errSubstr: `expected array for unset`,
		},
		{
			name:      "null element in unset rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","unset":[null]}]}`,
			errSubstr: `null value not allowed in unset`,
		},
		{
			name:      "null element in one_of rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":{"fields":{"status":{"one_of":[null]}}}}]}`,
			errSubstr: `null value not allowed in one_of`,
		},
		{
			name:      "null root field actor rejected",
			raw:       `{"schema":1,"table":"demo","actor":null,"members":[]}`,
			errSubstr: `null value not allowed for actor`,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ntable.ValidateBatchManifestRaw([]byte(tc.raw))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("expected error containing %q, got: %v", tc.errSubstr, err)
			}
		})
	}

	// stella case 4: application field named "remove" inside set is accepted
	t.Run("stella case 4: application field remove in set accepted", func(t *testing.T) {
		t.Parallel()
		raw := `{"schema":1,"table":"demo","members":[{"id":"m","set":{"remove":"done"}}]}`
		m, err := ntable.ValidateBatchManifestRaw([]byte(raw))
		if err != nil {
			t.Fatalf("expected valid manifest, got error: %v", err)
		}
		if m.Members[0].Set["remove"] != "done" {
			t.Errorf("expected set.remove='done', got %q", m.Members[0].Set["remove"])
		}
	})
}
