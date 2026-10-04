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
		{nil, "; run: nova-bus help"},
		{[]string{"wibble"}, "; run: nova-bus help"},
		{[]string{"inbox", "--azz", "Ada"}, "; run: nova-bus inbox -h"},
	} {
		r := invoke(t, "", tc.args...)
		assert.Equalf(t, 2, r.code, "%v: exit = %d, want 2", tc.args, r.code)
		assert.Equalf(t, 1, strings.Count(r.stderr, "\n"), "%v: the refusal is not one line:\n%s", tc.args, r.stderr)
		assert.Containsf(t, r.stderr, " REFUSED: ", "%v: the refusal has no REFUSED word: %q", tc.args, r.stderr)
		assert.Truef(t, strings.HasSuffix(r.stderr, tc.door+"\n"), "%v: the refusal does not end at its door %q: %q", tc.args, tc.door, r.stderr)
		assert.Emptyf(t, r.stdout, "%v: a refusal wrote to stdout: %q", tc.args, r.stdout)
	}
	// And the door opens.
	invoke(t, "", "help").mustCode(t, 0).mustContain(t, "stdout", "usage:")
}

// One run names every value its flags cannot take, every missing required flag, and the
// unknown-flag answer names the verb's flags: a reader fixes the call once.
func TestOneRunNamesEveryProblem(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		args  []string
		wants []string
	}{
		{"two bad values and the missing flags", []string{"wait", "--receipt-max-words", "abc", "--timeout", "5x"}, []string{
			"WAIT REFUSED: invalid value for --receipt-max-words: it wants a whole number",
			"WAIT REFUSED: invalid value for --timeout: it wants a duration such as 30s or 5m",
			"WAIT REFUSED: --as is required; it wants which participant you are; refusing to guess; run: nova-bus wait -h",
			"WAIT REFUSED: --bus is required",
		}},
		{"a bad boolean", []string{"close", "--dry-run=maybe", "--bus", "b", "--as", "Ada", "--before", "2026-09-09T00:00:00Z"}, []string{
			"CLOSE REFUSED: invalid value for --dry-run: it wants true or false",
		}},
		{"an unknown flag names the verb's flags and the nearest", []string{"inbox", "--buss", "b"}, []string{
			"INBOX REFUSED: unknown flag --buss; the flags of inbox are --advance,", "did you mean --bus?", "; run: nova-bus inbox -h",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := invoke(t, "", tc.args...).mustCode(t, 2)
			for _, want := range tc.wants {
				assert.Contains(t, r.stderr, want)
			}
			assert.NotContains(t, r.stderr, "flag provided but not defined")
			assert.Empty(t, r.stdout)
		})
	}
}
