package main

import (
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func replay(t *testing.T) workgh.Query {
	t.Helper()
	q, err := workgh.Replay("../../internal/workgh/testdata/reliable")
	require.NoError(t, err)
	return q
}

func workTool(q workgh.Query) testkit.Main {
	return func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdout, stderr, q)
	}
}

// TestImportThenVerifyIsZeroDifferences (SPEC-WORK-V1 sections 1.6 and
// 1.10): import writes the tree from the recorded fetch; verify against the
// same fetch finds nothing; one changed field is one DRIFT line and exit 1.
func TestImportThenVerifyIsZeroDifferences(t *testing.T) {
	t.Parallel()
	tree := filepath.Join(t.TempDir(), "tree.lisp")
	repo := []string{"--repo", "mas-bandwidth/reliable", "--page-size", "15"}
	res := workTool(replay(t)).Run(append([]string{"import", "--org", "mas-bandwidth", "--out", tree}, repo...)...)
	require.Equal(t, 0, res.Code, "import exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stdout, "IMPORT OK", "import exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stdout, " issues=20 ", "import exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stdout, " calls=3 ", "import exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stdout, " rest=0 ", "import exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stdout, "PLAN OK org=mas-bandwidth repos=1 issues=20 est_calls=3", "no plan line:\n%s", res.Stdout)

	data := testkit.ReadFile(t, tree)
	got := regexp.MustCompile(`sha256=([0-9a-f]{64})`).FindStringSubmatch(res.Stdout)
	require.NotNil(t, got, "the import's sha256 does not name the file written:\n%s", res.Stdout)
	require.Equal(t, sum([]byte(data)), got[1], "the import's sha256 does not name the file written:\n%s", res.Stdout)

	res = workTool(replay(t)).Run(append([]string{"verify", "--tree", tree}, repo...)...)
	require.Equal(t, 0, res.Code, "verify exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stdout, "VERIFY OK", "verify exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stdout, "differences=0", "verify exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)

	changed := strings.Replace(data, `:title "`, `:title "changed `, 1)
	testkit.WriteFile(t, tree, changed)

	res = workTool(replay(t)).Run(append([]string{"verify", "--tree", tree}, repo...)...)
	require.Equal(t, 1, res.Code, "verify after a change: exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Regexp(t, `(?m)^DRIFT path=repos/mas-bandwidth/reliable/issues/\d+ field=title want=\S+ got=changed\\x20`, res.Stdout, "verify after a change: exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stderr, "VERIFY FAIL", "verify after a change: exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stderr, "differences=1 missing=0 extra=0 drift=1", "verify after a change: exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)

	res = workTool(replay(t)).Run(append([]string{"import", "--org", "mas-bandwidth", "--dry-run"}, repo...)...)
	require.Equal(t, 0, res.Code, "dry run exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stdout, "dry_run=true", "dry run exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stdout, "out=-", "dry run exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
}

// TestTheDryRunSaysWhatItReads: a dry run is not offline. It reads GitHub as
// the import does (every call of the recording) and writes nothing, and the
// run, the verb's help and the banner each say so, so no reader takes it for
// an offline rehearsal.
func TestTheDryRunSaysWhatItReads(t *testing.T) {
	t.Parallel()
	res := workTool(replay(t)).Run("import", "--org", "mas-bandwidth", "--repo", "mas-bandwidth/reliable", "--page-size", "15", "--dry-run")
	require.Equal(t, 0, res.Code, "dry run exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, " calls=3 ", "the dry run made fewer calls than the import makes:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "IMPORT NOTE the dry run read GitHub as the import does (calls=3, read-only) and wrote nothing\n", "the run does not say what it read:\n%s", res.Stdout)

	for _, args := range [][]string{{"import", "-h"}, {"help"}} {
		help := workTool(nil).Run(args...)
		require.Equal(t, 0, help.Code, "%v exit %d", args, help.Code)
		assert.Regexp(t, `(?s)--dry-run.{0,20}reads?\s+GitHub\s+(exactly\s+)?as\s+the\s+import\s+does.*writes?\s+nothing`, help.Stdout, "%v does not say a dry run reads GitHub and writes nothing:\n%s", args, help.Stdout)
	}
}

// TestTheBudgetIsCheckedBeforeTheIssuesAreRead: a plan past --max-calls is
// refused at exit 2 after the listing, before any issue is read.
func TestTheBudgetIsCheckedBeforeTheIssuesAreRead(t *testing.T) {
	t.Parallel()
	res := workTool(replay(t)).Run("import", "--org", "mas-bandwidth", "--repo", "mas-bandwidth/reliable", "--page-size", "15", "--max-calls", "2", "--dry-run")
	require.Equal(t, 2, res.Code, "exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stderr, "IMPORT FAIL", "exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stderr, "calls=1 ", "exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stderr, "needs\\x20about\\x203\\x20calls", "exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
}

// TestRefusalsNameTheFlag: a malformed invocation exits 2 naming every
// problem at once and pointing at the verb's help; help exits 0.
func TestRefusalsNameTheFlag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		code int
		want []string
	}{
		{"bare import", []string{"import"}, 2, []string{"--org is required", "--out is required unless --dry-run", "run: nova-work import -h"}},
		{"bad calls and page size", []string{"import", "--org", "o", "--dry-run", "--page-size", "0", "--max-calls", "0"}, 2, []string{"--max-calls must be positive", "--page-size must be 1 to 100"}},
		{"missing out dir", []string{"import", "--org", "o", "--out", "/nonexistent-dir/t.lisp"}, 2, []string{"does not exist"}},
		{"repo not in org", []string{"import", "--org", "o", "--dry-run", "--repo", "p/r"}, 2, []string{"--repo p/r is not in --org o"}},
		{"bad repo name", []string{"import", "--org", "o", "--dry-run", "--repo", "bad"}, 2, []string{"is not owner/name"}},
		{"nonexistent gh", []string{"import", "--org", "o", "--dry-run", "--gh", "/nonexistent/gh-cli"}, 2, []string{"is not found", "--gh"}},
		{"bogus flag", []string{"import", "--bogus"}, 2, []string{"bogus", "run: nova-work import -h"}},
		{"bare verify", []string{"verify"}, 2, []string{"--tree is required", "run: nova-work verify -h"}},
		{"nonexistent tree", []string{"verify", "--tree", "/nonexistent/t.lisp"}, 2, []string{"VERIFY FAIL"}},
		{"unknown verb", []string{"frob"}, 2, []string{"unknown verb", "import verify"}},
		{"no verb", []string{}, 2, []string{"no verb", "run: nova-work help"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := workTool(nil).Run(tc.args...)
			assert.Equal(t, tc.code, res.Code, "%v: exit %d, want %d\n%s%s", tc.args, res.Code, tc.code, res.Stdout, res.Stderr)
			for _, w := range tc.want {
				assert.Contains(t, res.Stderr, w, "%v: stderr lacks %q:\n%s", tc.args, w, res.Stderr)
			}
		})
	}

	helpCases := []struct {
		name string
		args []string
	}{
		{"bare help", []string{"help"}},
		{"help import", []string{"help", "import"}},
		{"help verify", []string{"help", "verify"}},
		{"import -h", []string{"import", "-h"}},
		{"verify --help", []string{"verify", "--help"}},
	}
	for _, tc := range helpCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := workTool(nil).Run(tc.args...)
			assert.Equal(t, 0, res.Code, "%v: exit %d\n%s", tc.args, res.Code, res.Stdout)
			assert.Contains(t, res.Stdout, "nova-work", "%v: exit %d\n%s", tc.args, res.Code, res.Stdout)
		})
	}
}

// Both spellings of the build identity print the one line at exit 0, like
// every other tool's version verb and its --version alias.
func TestVersionAndItsAliasPrintTheBuildIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		arg  string
	}{
		{"verb", "version"},
		{"alias", "--version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := workTool(nil).Run(tc.arg)
			diag := fmt.Sprintf("%s: exit %d, stdout %q, stderr %q; want the identity line at exit 0", tc.arg, res.Code, res.Stdout, res.Stderr)
			assert.Equal(t, 0, res.Code, diag)
			assert.True(t, strings.HasPrefix(res.Stdout, "nova-work "), diag)
			assert.Empty(t, res.Stderr, diag)
		})
	}
}

// TestABareCommandRefusesInOneLine (ONBOARDING.md point 1): no verb is exit 2
// and one stderr line naming the verbs and the door, never the whole banner.
func TestABareCommandRefusesInOneLine(t *testing.T) {
	t.Parallel()
	res := workTool(nil).Run()
	require.Equal(t, 2, res.Code, "bare nova-work: exit %d, stdout %q, stderr %q", res.Code, res.Stdout, res.Stderr)
	require.Empty(t, res.Stdout, "bare nova-work: exit %d, stdout %q, stderr %q", res.Code, res.Stdout, res.Stderr)
	require.Equal(t, "nova-work: no verb; verbs: import verify help version; run: nova-work help\n", res.Stderr, "bare nova-work: exit %d, stdout %q, stderr %q", res.Code, res.Stdout, res.Stderr)
}
