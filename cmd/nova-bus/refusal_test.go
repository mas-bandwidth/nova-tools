package main

import (
	"strings"
	"testing"
)

// THE ONLY THING THE bounded-output CHANGE TOUCHES IN THIS BINARY: three refusal sites.
// A flag typo, an unknown verb or a bare invocation used to dump the whole 102-line
// usage -- 6,473 bytes, about 1.6K tokens, to say a dash was in the wrong place. The
// open-list work on `inbox` and `wait` belongs to another line and is not touched here.
func TestARefusalIsOneLineAndNamesTheDoor(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"wibble"},
		{"inbox", "--azz", "Ada"},
		{"check", "-h"},
	} {
		r := invoke(t, "", args...)
		if r.code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, r.code)
		}
		if n := strings.Count(r.stderr, "\n"); n != 1 {
			t.Errorf("%v: the refusal is %d lines, want 1:\n%s", args, n, r.stderr)
		}
		if !strings.Contains(r.stderr, "run: nova-bus help") {
			t.Errorf("%v: the refusal names no door: %q", args, r.stderr)
		}
		if r.stdout != "" {
			t.Errorf("%v: a refusal wrote to stdout: %q", args, r.stdout)
		}
	}
	// And the door opens.
	invoke(t, "", "help").mustCode(t, 0).mustContain(t, "stdout", "usage:")
}
