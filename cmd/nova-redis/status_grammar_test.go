package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusGrammar pins the one status grammar docs/STANDARD.md section 2
// holds for every tool: after the verb token the first word, and the exit
// code, tell the same truth (0 done, 1 the verb ran and said no, 2 it could
// not run). Each row drives one outcome of one verb through run and asserts
// that word and that exit together, so a renamed word that leaves its exit
// behind turns this test red.
//
// A verb with no such outcome through run, without starting redis-server, is
// named here and has no row for it. serve's done line is SERVE STOP, not
// SERVE OK: its output is the instance's own, plus START and STOP, so the
// done row pins STOP with exit 0. fn load and fn check have no OK here (a
// store that answers FUNCTION is the functional tier); their failure line
// leads with FAILED and the library name follows, so the status word is the
// first word of the line. acl check and acl apply have no OK here (a store
// that answers ACL is the functional tier). acl render has no FAILED: a
// library that does not build is a refusal, and render opens no store.
// version and help print no status line (the build line, the usage) and have
// no FAILED: nothing after a parsed line fails.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()

	const unreachable = "127.0.0.1:1"

	cases := []struct {
		name   string
		args   []string
		setup  func(t *testing.T) (args []string, run func(args ...string) (int, string, string))
		token  string
		word   string
		leads  bool
		marker string
		exit   int
	}{
		{
			name: "serve done",
			setup: func(t *testing.T) ([]string, func(...string) (int, string, string)) {
				t.Helper()
				h := newServeHarness(t, "fixture-secret-only")
				return []string{"serve", "--bind", "127.0.0.1", "--port", "6380", "--dir", h.dir}, h.run
			},
			token: "SERVE", word: "STOP", exit: 0,
		},
		{
			name: "serve refused without a bind",
			setup: func(t *testing.T) ([]string, func(...string) (int, string, string)) {
				t.Helper()
				h := newServeHarness(t, "")
				return []string{"serve"}, h.run
			},
			token: "SERVE", word: "REFUSED", marker: "run: nova-redis help", exit: 2,
		},
		{
			name: "serve failed when redis-server is not on PATH",
			setup: func(t *testing.T) ([]string, func(...string) (int, string, string)) {
				t.Helper()
				h := newServeHarness(t, "fixture-secret-only")
				h.d.lookPath = func(string) (string, error) { return "", errors.New("not found") }
				return []string{"serve", "--bind", "127.0.0.1", "--port", "6380", "--dir", h.dir}, h.run
			},
			token: "SERVE", word: "FAILED", exit: 1,
		},
		{
			name:  "spill ok on a dry run",
			args:  []string{"spill", "--dry-run", "--addr", "127.0.0.1:6379", "--owner", "ada", "--name", "note", "--ttl", "10m", "--value", "hi"},
			token: "SPILL", word: "OK", exit: 0,
		},
		{
			name:  "spill refused without an owner",
			args:  []string{"spill", "--addr", "127.0.0.1:6379", "--name", "note", "--ttl", "10m", "--value", "hi"},
			token: "SPILL", word: "REFUSED", marker: "run: nova-redis help", exit: 2,
		},
		{
			name:  "spill refused when the store does not answer",
			args:  []string{"spill", "--addr", unreachable, "--owner", "ada", "--name", "note", "--ttl", "10m", "--value", "hi"},
			token: "SPILL", word: "REFUSED", marker: "run: nova-redis help", exit: 2,
		},
		{
			name: "recall ok",
			setup: func(t *testing.T) ([]string, func(...string) (int, string, string)) {
				t.Helper()
				h := newHarness(t)
				code, _, stderr := h.run("spill", "--owner", "ada", "--name", "note", "--ttl", "10m", "--value", "hi")
				require.Equal(t, 0, code, "setup spill: exit %d stderr %s", code, stderr)
				return []string{"recall", "--owner", "ada", "--name", "note"}, h.run
			},
			token: "RECALL", word: "OK", exit: 0,
		},
		{
			name:  "recall refused without an owner",
			args:  []string{"recall", "--addr", "127.0.0.1:6379", "--name", "note"},
			token: "RECALL", word: "REFUSED", marker: "run: nova-redis help", exit: 2,
		},
		{
			name:  "recall refused when the store does not answer",
			args:  []string{"recall", "--addr", unreachable, "--owner", "ada", "--name", "note"},
			token: "RECALL", word: "REFUSED", marker: "run: nova-redis help", exit: 2,
		},
		{
			name:  "fn load refused without an address",
			args:  []string{"fn", "load"},
			token: "FN-LOAD", word: "REFUSED", marker: "run: nova-redis help", exit: 2,
		},
		{
			name:  "fn load failed when the store does not answer",
			args:  []string{"fn", "load", "--addr", unreachable},
			leads: true, word: "FAILED", exit: 2,
		},
		{
			name:  "fn check refused without an address",
			args:  []string{"fn", "check"},
			token: "FN-CHECK", word: "REFUSED", marker: "run: nova-redis help", exit: 2,
		},
		{
			name:  "fn check failed when the store does not answer",
			args:  []string{"fn", "check", "--addr", unreachable},
			leads: true, word: "FAILED", exit: 2,
		},
		{
			name:  "acl render ok",
			args:  []string{"acl", "render"},
			token: "ACL RENDER", word: "OK", exit: 0,
		},
		{
			name:  "acl render refused on an unknown flag",
			args:  []string{"acl", "render", "--zzz"},
			token: "ACL-RENDER", word: "REFUSED", marker: "run: nova-redis acl render -h", exit: 2,
		},
		{
			name:  "acl check refused without an address",
			args:  []string{"acl", "check"},
			token: "ACL-CHECK", word: "REFUSED", marker: "run: nova-redis help", exit: 2,
		},
		{
			name:  "acl check failed when the store does not answer",
			args:  []string{"acl", "check", "--addr", unreachable},
			token: "ACL CHECK", word: "FAILED", exit: 2,
		},
		{
			name:  "acl apply refused without an address",
			args:  []string{"acl", "apply"},
			token: "ACL-APPLY", word: "REFUSED", marker: "run: nova-redis help", exit: 2,
		},
		{
			name:  "acl apply failed when the store does not answer",
			args:  []string{"acl", "apply", "--addr", unreachable},
			token: "ACL APPLY", word: "FAILED", exit: 2,
		},
		{
			name:  "version refused on an unknown flag",
			args:  []string{"version", "--zzz"},
			token: "VERSION", word: "REFUSED", marker: "run: nova-redis version -h", exit: 2,
		},
		{
			name:  "help refused on an unknown verb",
			args:  []string{"help", "--zzz"},
			token: "REDIS", word: "REFUSED", marker: "run: nova-redis help", exit: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			args := tc.args
			drive := func(a ...string) (int, string, string) {
				var out, errb bytes.Buffer
				d := deps{
					now:    func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) },
					getenv: func(string) string { return "" },
				}
				return run(a, &out, &errb, d), out.String(), errb.String()
			}
			if tc.setup != nil {
				args, drive = tc.setup(t)
			}
			code, stdout, stderr := drive(args...)
			require.Equal(t, tc.exit, code, "exit = %d, want %d\nstdout: %q\nstderr: %q", code, tc.exit, stdout, stderr)
			stream := stdout
			if tc.exit != 0 {
				stream = stderr
			}
			var got string
			var found bool
			for _, line := range strings.Split(strings.TrimRight(stream, "\n"), "\n") {
				var word string
				if tc.leads {
					fields := strings.Fields(line)
					if len(fields) == 0 {
						continue
					}
					word = fields[0]
				} else {
					rest, ok := strings.CutPrefix(line, tc.token+" ")
					if !ok {
						continue
					}
					fields := strings.Fields(rest)
					if len(fields) == 0 {
						continue
					}
					word = strings.TrimRight(fields[0], ":")
				}
				if word != tc.word {
					continue
				}
				got, found = word, true
				break
			}
			require.True(t, found, "no line carries %q after %q in %q", tc.word, tc.token, stream)
			assert.Equal(t, tc.word, got, "the word after the verb token is %q, want %q in %q", got, tc.word, stream)
			if tc.word == "REFUSED" {
				assert.Empty(t, stdout, "a refusal must print nothing on stdout, got %q", stdout)
				assert.Contains(t, stderr, tc.marker, "the refusal must carry its remedy, got %q", stderr)
			}
		})
	}
}
