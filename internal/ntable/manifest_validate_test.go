package ntable_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err, "expected valid manifest, got error")
	assert.Equal(t, "demo", manifest.Table, "expected table demo, got %s", manifest.Table)
	require.Len(t, manifest.Members, 2, "expected 2 members, got %d", len(manifest.Members))
	assert.True(t, manifest.Members[1].Remove, "expected member m2 remove=true")

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
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m1","expect":{}},{"id":"m1","expect":{}}]}`,
			errSubstr: `member "m1": duplicate manifest member: the id appears more than once in the manifest`,
		},
		{
			name:      "empty member id",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"","expect":{}}]}`,
			errSubstr: "member id cannot be empty",
		},
		{
			name:      "member id with control characters",
			raw:       "{\"schema\":1,\"table\":\"demo\",\"members\":[{\"id\":\"m1\\n\",\"expect\":{}}]}",
			errSubstr: "invalid member id",
		},
		{
			name:      "case 1: uppercase REMOVE false rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","REMOVE":false,"set":{"status":"done"}}]}`,
			errSubstr: `unknown field "REMOVE" in member object`,
		},
		{
			name:      "case 2: null value in set rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","set":{"status":null}}]}`,
			errSubstr: `status must not be null`,
		},
		{
			name:      "case 3: case variant duplicate actor and Actor rejected",
			raw:       `{"schema":1,"table":"demo","actor":"a","Actor":"b","members":[{"id":"m"}]}`,
			errSubstr: `duplicate key "Actor" in manifest`,
		},
		{
			name:      "case A: null revision rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":{"revision":null}}]}`,
			errSubstr: `revision must not be null`,
		},
		{
			name:      "case B: null create score rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","create":{"row":"r","col":"c","score":null}}]}`,
			errSubstr: `score must not be null`,
		},
		{
			name:      "case C: null equals guard rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":{"fields":{"status":{"equals":null,"one_of":["ready"]}}}}]}`,
			errSubstr: `equals must not be null`,
		},
		{
			name:      "type mismatch: revision that is not a decimal number",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":{"revision":"abc"}}]}`,
			errSubstr: `revision must be a decimal number in a string`,
		},
		{
			name:      "type mismatch: non-string row",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","create":{"row":123,"col":"c","score":10}}]}`,
			errSubstr: `row must be a string, found a number`,
		},
		{
			name:      "type mismatch: non-string col",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","create":{"row":"r","col":true,"score":10}}]}`,
			errSubstr: `col must be a string, found true`,
		},
		{
			name:      "type mismatch: non-number score",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","create":{"row":"r","col":"c","score":"ten"}}]}`,
			errSubstr: `score must be a JSON number, found a string`,
		},
		{
			name:      "type mismatch: non-boolean remove",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","remove":"true"}]}`,
			errSubstr: `remove must be true`,
		},
		{
			name:      "type mismatch: non-boolean absent",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":{"absent":"true"}}]}`,
			errSubstr: `absent must be true, found a string`,
		},
		{
			name:      "type mismatch: non-object expect",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":"invalid"}]}`,
			errSubstr: `expect must be an object, found a string`,
		},
		{
			name:      "type mismatch: non-array members",
			raw:       `{"schema":1,"table":"demo","members":{"id":"m"}}`,
			errSubstr: `members must be an array, found an object`,
		},
		{
			name:      "type mismatch: non-array unset",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","unset":"field"}]}`,
			errSubstr: `unset must be an array, found a string`,
		},
		{
			name:      "null element in unset rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","unset":[null]}]}`,
			errSubstr: `an element must not be null`,
		},
		{
			name:      "null element in one_of rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":{"fields":{"status":{"one_of":[null]}}}}]}`,
			errSubstr: `an element must not be null`,
		},
		{
			name:      "null root field actor rejected",
			raw:       `{"schema":1,"table":"demo","actor":null,"members":[]}`,
			errSubstr: `actor must not be null`,
		},
		{
			name:      "strict schema: version rejected",
			raw:       `{"schema":1,"version":"1","table":"demo","members":[]}`,
			errSubstr: `unknown field "version" in root manifest`,
		},
		{
			name:      "strict schema: expect_revision rejected",
			raw:       `{"schema":1,"expect_revision":"1","table":"demo","members":[]}`,
			errSubstr: `unknown field "expect_revision" in root manifest`,
		},
		{
			name:      "strict schema: receipt rejected",
			raw:       `{"schema":1,"receipt":true,"table":"demo","members":[]}`,
			errSubstr: `unknown field "receipt" in root manifest`,
		},
		{
			name:      "strict schema: score in expect rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":{"score":10}}]}`,
			errSubstr: `unknown field "score" in expect`,
		},
		{
			name:      "strict schema: exists in field guard rejected",
			raw:       `{"schema":1,"table":"demo","members":[{"id":"m","expect":{"fields":{"col":{"exists":true}}}}]}`,
			errSubstr: `unknown field "exists" in field guard`,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ntable.ValidateBatchManifestRaw([]byte(tc.raw))
			require.Error(t, err, "expected error containing %q, got nil", tc.errSubstr)
			assert.ErrorContains(t, err, tc.errSubstr, "expected error containing %q, got: %v", tc.errSubstr, err)
		})
	}

	// case 4: application field named "remove" inside set is accepted
	t.Run("case 4: application field remove in set accepted", func(t *testing.T) {
		t.Parallel()
		raw := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"0","operation_id":"op","members":[{"id":"m","expect":{},"set":{"remove":"done"}}]}`
		m, err := ntable.ValidateBatchManifestRaw([]byte(raw))
		require.NoError(t, err, "expected valid manifest, got error")
		assert.Equal(t, "done", m.Members[0].Set["remove"], "expected set.remove='done', got %q", m.Members[0].Set["remove"])
	})
}

// A manifest that cannot be read is refused in the manifest's own words and at
// its place; no message names the parser or its Go types.
func TestManifestErrorsSpeakTheManifestsLanguage(t *testing.T) {
	t.Parallel()
	head := func(members string) string {
		return `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"1","operation_id":"op","members":[` + members + `]}`
	}
	cases := []struct{ name, raw, want string }{
		{"score string", head(`{"id":"a","expect":{"absent":true},"create":{"row":"r","col":"c","score":"5"}}`), `score must be a JSON number, found a string at members[0].create.score`},
		{"move score", head(`{"id":"a","expect":{},"move":{"row":"r","col":"c","score":true}}`), `score must be a JSON number, found true at members[0].move.score`},
		{"second member", head(`{"id":"a","expect":{}},{"id":"b","expect":{},"set":{"k":5}}`), `k must be a string, found a number at members[1].set.k`},
		{"epoch number", `{"schema":1,"table":"demo","epoch":0,"expected_table_revision":"1","operation_id":"op","members":[]}`, `epoch must be a decimal string, found a number at epoch`},
		{"revision number", head(`{"id":"a","expect":{"revision":1}}`), `revision must be a decimal string, found a number at members[0].expect.revision`},
		{"schema string", `{"schema":"1","table":"demo"}`, `schema must be the number 1, found a string at schema`},
		{"place row", head(`{"id":"a","expect":{"place":{"row":5,"col":"c"}}}`), `row must be a string, found a number at members[0].expect.place.row`},
		{"unset item", head(`{"id":"a","expect":{},"unset":["x",7]}`), `must be a field name (a string), found a number at members[0].unset[1]`},
		{"one_of item", head(`{"id":"a","expect":{"fields":{"f":{"one_of":["x",{}]}}}}`), `must be an option (a string), found an object at members[0].expect.fields.f.one_of[1]`},
		{"member entry", head(`5`), `must be a member entry (an object), found a number at members[0]`},
		{"remove", head(`{"id":"a","expect":{},"remove":false}`), `remove must be true, found false at members[0].remove`},
		{"expect null", head(`{"id":"a","expect":null}`), `expect must not be null at members[0].expect`},
	}
	for _, tc := range cases {
		_, err := ntable.ValidateBatchManifestRaw([]byte(tc.raw))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v; want %q", tc.name, err, tc.want)
			continue
		}
		var me *ntable.ManifestError
		if !errors.As(err, &me) || !errors.Is(err, ntable.ErrMalformedManifest) {
			t.Errorf("%s: %T is not a ManifestError", tc.name, err)
		}
		for _, leak := range []string{"unmarshal", "Go struct", "Go value", "cannot use", "json:"} {
			assert.NotContains(t, err.Error(), leak, "%s: %q leaks the parser: %v", tc.name, leak, err)
		}
	}
}
