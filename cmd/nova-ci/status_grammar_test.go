package main

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
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

// grammarTree is a scratch checkout with just a go.mod: what the scaffolding
// verbs dry-run against, so no test writes outside its own t.TempDir().
func grammarTree(t *testing.T) string {
	t.Helper()
	tree := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module example.com/m\n"), 0o644))
	return tree
}

// TestStatusGrammar holds the tool's one status grammar: after the verb's
// token the first word is OK, REFUSED or FAILED, and the exit code tells the
// same truth (0 done, 1 the verb ran and said no, 2 could not run). Each row
// runs one outcome of one verb through run (the tool's Run function) with a
// fake or a fixture and asserts the status word and the exit together, so a
// word that moved without its exit (or an exit without its word) fails the
// row (STANDARD §2).
//
// The rows cover every outcome each verb holds. Four outcomes have no status
// line to pin and stay out by design, each named with the test that pins it
// instead: slowtests at exit 1 prints CI-SLOW finding item lines, not a FAILED
// word (TestSlowtestsOverBudgetExitsOneOnlyUnderEnforce pins the exit), so its
// FAILED row drives the verb's own render path for --json, the one line that
// carries the FAILED word; functional, new-rule and new-verb hold only exits
// 0 and 2, so they have no FAILED row; version OK prints the build line, not
// a status line (TestVersionLineShape pins its four fields); help prints usage
// (TestHelpOpensTheDoor pins the example block). github receipt FAILED needs
// a store that refuses the write, so the unit tier cannot drive it
// (TestReceiptVerbWritesTheRowAndARefusedWriteIsExitOne pins it behind
// //go:build functional). local OK is go test's own lowercase ok, which the
// PKG line repeats; the scaffold and receipt OK rows close on a NOTE
// continuation line, which STANDARD §2 names as the shape a continuation
// opens with.
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
			name:  "local OK",
			token: "PKG",
			run: func(t *testing.T) (int, string) {
				f := localFixture(t, "./cmd/a\n", localReply{prefix: "nice -n 15 make test ", stdout: localGreenStream})
				code, out, _ := runLocal(t, f)
				return code, out
			},
			word: "ok",
			code: 0,
		},
		{
			name:  "local REFUSED",
			token: "nova-ci local",
			run: func(t *testing.T) (int, string) {
				f := localFixture(t, "./cmd/a\n")
				code, _, errb := runLocal(t, f, "--bse", "x")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
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
		{
			name:  "functional OK",
			token: "CI FUNCTIONAL",
			run: func(t *testing.T) (int, string) {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n"), 0o644))
				code, out, _ := runCI(t, []string{"functional", dir}, "")
				return code, out
			},
			word: "OK",
			code: 0,
		},
		{
			name:  "functional REFUSED",
			token: "nova-ci functional",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"functional"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
		{
			name:  "new-rule OK",
			token: "nova-ci new-rule",
			run: func(t *testing.T) (int, string) {
				code, out, _ := runCI(t, []string{"new-rule", "--root", grammarTree(t), "--dry-run", "demo"}, "")
				return code, out
			},
			word: "NOTE",
			code: 0,
		},
		{
			name:  "new-rule REFUSED",
			token: "nova-ci new-rule",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"new-rule", "--root", grammarTree(t), "Bad Name"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
		{
			name:  "new-verb OK",
			token: "nova-ci new-verb",
			run: func(t *testing.T) (int, string) {
				tree := grammarTree(t)
				require.NoError(t, os.MkdirAll(filepath.Join(tree, "cmd", "demo"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(tree, "cmd", "demo", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
				code, out, _ := runCI(t, []string{"new-verb", "--root", tree, "--dry-run", "demo", "probe"}, "")
				return code, out
			},
			word: "NOTE",
			code: 0,
		},
		{
			name:  "new-verb REFUSED",
			token: "nova-ci new-verb",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"new-verb", "--root", grammarTree(t), "ghost", "probe"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
		{
			name:  "github receipt OK",
			token: "CI RECEIPT NOTE",
			run: func(t *testing.T) (int, string) {
				code, out, _ := runCI(t, []string{"github", "receipt", "--dry-run", "--from-runner", "--repo", "mas-bandwidth/nova-tools",
					"--sha", receiptSHA, "--run-id", "42", "--workflow", "CI", "--conclusion", "success"}, "")
				return code, out
			},
			word: "--dry-run:",
			code: 0,
		},
		{
			name:  "github receipt REFUSED",
			token: "nova-ci github receipt",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"github", "receipt", "--repo", "x"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
		{
			name:  "version REFUSED",
			token: "nova-ci version",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"version", "extra"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
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
