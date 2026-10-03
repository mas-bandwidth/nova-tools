package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// THE ONLY THING THE bounded-output CHANGE TOUCHES IN THIS BINARY: three refusal sites.
// A flag typo, an unknown verb or a bare invocation used to dump the whole 102-line
// usage -- 6,473 bytes, about 1.6K tokens, to say a dash was in the wrong place. The
// open-list work on `inbox` and `wait` belongs to another line and is not touched here.
func TestARefusalIsOneLineAndNamesTheDoor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		door string
	}{
		{nil, "run: nova-bus help"},
		{[]string{"wibble"}, "run: nova-bus help"},
		{[]string{"inbox", "--azz", "Ada"}, "run: nova-bus inbox -h"},
	} {
		r := invoke(t, "", tc.args...)
		assert.Equalf(t, 2, r.code, "%v: exit = %d, want 2", tc.args, r.code)
		{
			n := strings.Count(r.stderr, "\n")
			assert.Equalf(t, 1, n, "%v: the refusal is %d lines, want 1:\n%s", tc.args, n, r.stderr)
		}
		assert.Containsf(t, r.stderr, tc.door, "%v: the refusal names no door: %q", tc.args, r.stderr)
		assert.Emptyf(t, r.stdout, "%v: a refusal wrote to stdout: %q", tc.args, r.stdout)
	}
	// And the door opens.
	invoke(t, "", "help").mustCode(t, 0).mustContain(t, "stdout", "usage:")
}
