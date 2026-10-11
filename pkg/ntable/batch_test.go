package ntable_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
						"definition": {Equals: new("definition-id")},
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
	require.NoError(t, err, "Marshal")
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
		assert.Contains(t, s, expected, "manifest json missing %s in: %s", expected, s)
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
	require.NoError(t, json.Unmarshal([]byte(rawJSON), &delta), "Unmarshal")

	require.Equal(t, "op-test", delta.OperationID, "unexpected delta header: %+v", delta)
	require.Equal(t, "abcdef123456", delta.Digest, "unexpected delta header: %+v", delta)
	require.Equal(t, "worker", delta.Actor, "unexpected delta header: %+v", delta)
	require.Len(t, delta.Members, 2, "expected 2 members, got %d", len(delta.Members))
	m1, m2 := delta.Members[0], delta.Members[1]
	require.Equal(t, "m1", m1.ID, "unexpected m1: %+v", m1)
	require.Empty(t, m1.FieldsSet, "unexpected m1: %+v", m1)
	require.Empty(t, m1.FieldsUnset, "unexpected m1: %+v", m1)
	require.Equal(t, "m2", m2.ID, "unexpected m2: %+v", m2)
	require.Equal(t, "ok", m2.FieldsSet["status"], "unexpected m2: %+v", m2)
	require.Equal(t, []string{"old_field"}, m2.FieldsUnset, "unexpected m2: %+v", m2)
}

func TestBatchInvalidTableName(t *testing.T) {
	t.Parallel()
	_, err := ntable.ApplyBatch(context.Background(), nil, ntable.BatchManifest{
		Table: "invalid table name!",
	})
	require.ErrorContains(t, err, "invalid name", "expected invalid name error, got")

	_, err = ntable.ReadSet(context.Background(), nil, "invalid table name!", ntable.ReadSetScope{})
	require.ErrorContains(t, err, "invalid name", "expected invalid name error, got")
}

// A refusal the library makes before it sends says so: this call changed nothing,
// and it says nothing about an earlier call with the same operation id, which the
// store may have applied under looser rules and would replay.
func TestBatchRefusalBeforeSendingIsAboutThisCallOnly(t *testing.T) {
	t.Parallel()
	set := map[string]string{}
	for i := 0; i <= ntable.LimitSetFields; i++ {
		set[strings.Repeat("f", 1+i%5)+strings.Repeat("g", i/5)] = "v"
	}
	manifest := func(members ...ntable.BatchMemberEntry) ntable.BatchManifest {
		return ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: "3", OperationID: "op-1", Members: members}
	}
	cases := map[string]ntable.BatchManifest{
		"a manifest error": func() ntable.BatchManifest { m := manifest(); m.Schema = 2; return m }(),
		"a bound":          manifest(ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{}, Set: set}),
		"a refusal code":   manifest(ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{}, Set: map[string]string{"x": "1"}, Unset: []string{"x"}}),
	}
	for name, m := range cases {
		_, err := ntable.ApplyBatch(context.Background(), nil, m)
		if !assert.Error(t, err, "%s: accepted", name) {
			continue
		}
		for _, want := range []string{"changed=no", "this call changed nothing", "earlier call with the same operation id"} {
			assert.ErrorContains(t, err, want, "%s: %q lacks %q", name, err, want)
		}
	}
}
