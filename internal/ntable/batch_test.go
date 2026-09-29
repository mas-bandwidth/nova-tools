package ntable_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

func strPtr(s string) *string { return &s }

func TestBatchManifestSerialization(t *testing.T) {
	t.Parallel()
	manifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "work",
		Epoch:                 "0",
		ExpectedTableRevision: "12",
		OperationID:           "op-17",
		Actor:                 "coordinator",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "c1",
				Expect: &ntable.MemberExpect{
					Revision: "2",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
					Fields: map[string]ntable.FieldGuard{
						"definition": {Equals: strPtr("definition-id")},
					},
				},
				Move: &ntable.MemberMoveOp{Row: "build", Col: "working"},
				Set:  map[string]string{"result": "event-17"},
			},
			{
				ID:     "c2",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{Row: "build", Col: "waiting", Score: 1},
				Set:    map[string]string{"definition": "definition-2"},
			},
			{
				ID: "c0",
				Expect: &ntable.MemberExpect{
					Revision: "4",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "landed"},
				},
			},
		},
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	for _, expected := range []string{
		`"schema":1`,
		`"table":"work"`,
		`"epoch":"0"`,
		`"expected_table_revision":"12"`,
		`"operation_id":"op-17"`,
		`"actor":"coordinator"`,
		`"id":"c1"`,
		`"definition":{"equals":"definition-id"}`,
		`"absent":true`,
	} {
		if !strings.Contains(s, expected) {
			t.Errorf("manifest json missing %s in: %s", expected, s)
		}
	}
}

func TestBatchDeltaRobustUnmarshal(t *testing.T) {
	t.Parallel()
	// Lua cjson can encode empty arrays as `{}` or `[]`. BatchDelta unmarshaler must handle all variants cleanly.
	rawJSON := `{
		"operation_id": "op-test",
		"digest": "abcdef123456",
		"actor": "worker",
		"guard_count": 1,
		"changed_count": 1,
		"members": [
			{
				"id": "m1",
				"before_place": "build:ready",
				"after_place": "build:done",
				"before_rev": "1",
				"after_rev": "2",
				"fields_set": {},
				"fields_unset": {}
			},
			{
				"id": "m2",
				"before_place": "",
				"after_place": "build:ready",
				"before_rev": "0",
				"after_rev": "1",
				"fields_set": {"status": "ok"},
				"fields_unset": ["old_field"]
			}
		]
	}`

	var delta ntable.BatchDelta
	if err := json.Unmarshal([]byte(rawJSON), &delta); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if delta.OperationID != "op-test" || delta.Digest != "abcdef123456" || delta.Actor != "worker" {
		t.Fatalf("unexpected delta header: %+v", delta)
	}
	if len(delta.Members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(delta.Members))
	}
	m1 := delta.Members[0]
	if m1.ID != "m1" || len(m1.FieldsSet) != 0 || len(m1.FieldsUnset) != 0 {
		t.Fatalf("unexpected m1: %+v", m1)
	}
	m2 := delta.Members[1]
	if m2.ID != "m2" || m2.FieldsSet["status"] != "ok" || len(m2.FieldsUnset) != 1 || m2.FieldsUnset[0] != "old_field" {
		t.Fatalf("unexpected m2: %+v", m2)
	}
}

func TestBatchInvalidTableName(t *testing.T) {
	t.Parallel()
	_, err := ntable.ApplyBatch(context.Background(), nil, ntable.BatchManifest{
		Table: "invalid table name!",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid name") {
		t.Fatalf("expected invalid name error, got: %v", err)
	}

	_, err = ntable.ReadSet(context.Background(), nil, "invalid table name!", ntable.ReadSetScope{})
	if err == nil || !strings.Contains(err.Error(), "invalid name") {
		t.Fatalf("expected invalid name error, got: %v", err)
	}
}
