package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
)

// The command judges a manifest before it sends it, and says what the library
// says: this call changed nothing, and nothing about an earlier call with the
// same operation id (the store replays a recorded request whatever the rules now).
func TestBatchCommandPreSendRefusalsSayThisCallOnly(t *testing.T) {
	t.Parallel()
	var fields []string
	for i := 0; i <= ntable.LimitSetFields; i++ {
		fields = append(fields, fmt.Sprintf(`"k%d":"v"`, i))
	}
	manifest := func(member string) string {
		return `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"3","operation_id":"op-1","members":[` + member + `]}`
	}
	cases := []struct {
		name, manifest string
		exit           int
	}{
		{"a bound", manifest(`{"id":"a","expect":{},"set":{` + strings.Join(fields, ",") + `}}`), 1},
		{"a rule", manifest(`{"id":"a","expect":{},"set":{"x":"1"},"unset":["x"]}`), 1},
		{"a manifest error", manifest(`{"id":"a","expect":{"absent":true},"create":{"row":"build","col":"ready","score":"0x10"}}`), 2},
	}
	for _, tc := range cases {
		code, stdout, stderr := runTable("batch", tc.manifest)
		assert.Equal(t, tc.exit, code, "%s: exit %d stdout %q stderr %q; want exit %d", tc.name, code, stdout, stderr, tc.exit)
		assert.Empty(t, stdout, "%s: exit %d stdout %q stderr %q; want exit %d", tc.name, code, stdout, stderr, tc.exit)
		assert.Contains(t, stderr, ntable.CheckedBeforeSending, "%s: %q lacks %q", tc.name, stderr, ntable.CheckedBeforeSending)
		assert.Contains(t, stderr, "changed=no", "%s: %q lacks %q", tc.name, stderr, ntable.CheckedBeforeSending)
	}
}
