package main

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tool"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusAfter returns the first word after token in out, and whether the token
// was found on any line: the status word that leads a typed line (STANDARD §2).
func statusAfter(out, token string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, token); ok && strings.HasPrefix(rest, " ") {
			rest = strings.TrimLeft(rest, " ")
			word, _, _ := strings.Cut(rest, " ")
			return word, true
		}
	}
	return "", false
}

// TestStatusGrammar holds the tool's one status grammar: after the verb's
// token the first word is OK, REFUSED or FAILED, and the exit code tells the
// same truth (0 done, 1 the verb ran and said no, 2 could not run). Each case
// runs one verb to one outcome and asserts the status word and the exit
// together, so a word that moved without its exit (or an exit without its
// word) fails the row (STANDARD §2).
func TestStatusGrammar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		token string
		run   func(t *testing.T) (int, string)
		word  string
		code  int
	}{
		{
			name:  "slowtests OK",
			token: "CI-SLOW",
			run: func(t *testing.T) (int, string) {
				stdin := "{\"Action\":\"pass\",\"Package\":\"example.com/pkg\",\"Test\":\"TestA\",\"Elapsed\":3.2}\n" +
					"{\"Action\":\"pass\",\"Package\":\"example.com/pkg\",\"Elapsed\":3.2}\n"
				code, out, _ := runCI(t, []string{"slowtests", "--budget", "60", "--load", "1", "--cpus", "2"}, stdin)
				return code, out
			},
			word: "OK",
			code: 0,
		},
		{
			name:  "slowtests REFUSED",
			token: "nova-ci slowtests",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"slowtests", "--budget", "0"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
		{
			name:  "slowtests FAILED",
			token: "nova-ci slowtests",
			run: func(t *testing.T) (int, string) {
				var out, errb bytes.Buffer
				o := &tool.Out{Verb: "slowtests", Status: tool.OK}
				o.Fact("load", math.NaN())
				return renderJSON(&out, &errb, o), errb.String()
			},
			word: "FAILED:",
			code: 1,
		},
		{
			name:  "local FAILED",
			token: "PKG",
			run: func(t *testing.T) (int, string) {
				stream := "{\"Action\":\"run\",\"Package\":\"example.com/m/cmd/a\",\"Test\":\"TestB\"}\n" +
					"{\"Action\":\"output\",\"Package\":\"example.com/m/cmd/a\",\"Test\":\"TestB\",\"Output\":\"    b_test.go:9: got 1, want 2\\n\"}\n" +
					"{\"Action\":\"fail\",\"Package\":\"example.com/m/cmd/a\",\"Test\":\"TestB\",\"Elapsed\":0.1}\n" +
					"{\"Action\":\"fail\",\"Package\":\"example.com/m/cmd/a\",\"Elapsed\":0.2}\n"
				f := localFixture(t, "./cmd/a\n", localReply{prefix: "nice -n 15 make test ", stdout: stream, code: 2})
				code, out, _ := runLocal(t, f)
				return code, out
			},
			word: "FAILED",
			code: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, line := tc.run(t)
			word, ok := statusAfter(line, tc.token)
			require.True(t, ok, "output lacks token %q on any line:\n%s", tc.token, line)
			assert.Equal(t, tc.word, word, "first word after %q = %q, want %q (exit %d)\n%s", tc.token, word, tc.word, code, line)
			assert.Equal(t, tc.code, code, "exit = %d, want %d for status word %q", code, tc.code, tc.word)
		})
	}
}
