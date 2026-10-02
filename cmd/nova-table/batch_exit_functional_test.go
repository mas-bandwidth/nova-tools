//go:build functional

package main

import (
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
)

// A manifest that cannot be parsed or read exits 2; a manifest that reads as
// one and that any rule refuses (a bound, a repeated id, a set and an unset of one
// field, a reserved field, a create with a move, an absent with a revision, an empty
// members array) exits 1, as a refusal by the store does.
func TestBatchExitCodesSeparateUnreadableFromRefused(t *testing.T) {
	t.Parallel()
	addr, rev := batchFixture(t)
	manifest := func(op, members string) string {
		return `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"` + op + `","members":[` + members + `]}`
	}
	unset := make([]string, 1001)
	for i := range unset {
		unset[i] = `"u` + strings.Repeat("x", i%9) + string(rune('a'+i%26)) + strings.Repeat("y", i/26) + `"`
	}
	cases := []struct {
		name string
		raw  string
		exit int
		want []string
	}{
		{"repeated id", manifest("x1", `{"id":"m1","expect":{}},{"id":"m1","expect":{}}`), 1, []string{"code=TWICE", "changed=no", "member \"m1\""}},
		{"set and unset", manifest("x2", `{"id":"m1","expect":{},"set":{"q":"1"},"unset":["q"]}`), 1, []string{"code=MUTATION"}},
		{"reserved field", manifest("x3", `{"id":"m1","expect":{},"set":{"epoch":"1"}}`), 1, []string{"code=RESERVEDFIELD"}},
		{"create and move", manifest("x4", `{"id":"n","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1},"move":{"row":"build","col":"done"}}`), 1, []string{"code=MUTATION"}},
		{"absent and revision", manifest("x5", `{"id":"n","expect":{"absent":true,"revision":"1"}}`), 1, []string{"code=MUTATION"}},
		{"no members", manifest("x6", ``), 1, []string{"code=MANIFEST", "at least one member"}},
		{"over a bound", manifest("x7", `{"id":"m1","expect":{},"unset":[`+strings.Join(unset, ",")+`]}`), 1, []string{"code=LIMIT", "bound 1000, observed 1001", "change at most 1000 fields of a member"}},
		{"the store's refusal", manifest("x8", `{"id":"m1","expect":{"revision":"9"}}`), 1, []string{"code=MEMBERREVISION", "changed=no", "member read"}},
		{"score as a string", manifest("x9", `{"id":"n","expect":{"absent":true},"create":{"row":"build","col":"ready","score":"5"}}`), 2, []string{"score must be a JSON number, found a string at members[0].create.score", "changed=no"}},
		{"unknown key", manifest("x10", `{"id":"m1","expect":{},"bogus":1}`), 2, []string{"unknown field"}},
		{"not JSON", `{"schema":`, 2, []string{"invalid batch manifest"}},
		{"epoch differs from --epoch", manifest("x11", `{"id":"m1","expect":{}}`), 2, []string{"--epoch"}},
	}
	for _, tc := range cases {
		args := []string{"batch", "--redis", addr, tc.raw}
		if tc.name == "epoch differs from --epoch" {
			args = append(args, "--epoch", "3")
		}
		code, stdout, stderr := runTable(args...)
		if code != tc.exit || stdout != "" {
			assert.Equal(t, tc.exit, code, "%s: exit %d (want %d), stdout %q, stderr %q", tc.name, code, tc.exit, stdout, stderr)
			assert.Empty(t, stdout, "%s: refusal wrote stdout: %s", tc.name, stdout)
			continue
		}
		for _, w := range tc.want {
			assert.Contains(t, stderr, w, "%s: stderr lacks %q: %s", tc.name, w, stderr)
		}
		assert.NotContains(t, stderr, "cannot unmarshal", "%s: the parser's words: %s", tc.name, stderr)
		assert.NotContains(t, stderr, "Go struct", "%s: the parser's words: %s", tc.name, stderr)
	}
	{
		code, _, stderr := runTable("batch", "--redis", addr, "/no/such/manifest.json")
		assert.EqualValues(t, 2, code, "an unreadable file: exit %d %s", code, stderr)
		assert.Contains(t, stderr, "/no/such/manifest.json", "an unreadable file: exit %d %s", code, stderr)
	}
}
