package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/internal/workfile"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixed is the time every test passes in: the tree an import writes records
// it, so the sha256 and seconds= reproduce. No test here reads the clock.
var fixed = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// recording is the conversation with GitHub every test answers from: one
// public repository of twenty issues, read at fifteen a page.
const recording = "../../internal/workgh/testdata/reliable"

// recorded is GitHub as the recording answers it: gh is found at ghAt, and
// every query is the strict replay (a call the recording did not make is an
// error). No process starts and no socket opens.
func recorded(t *testing.T, ghAt string) github {
	t.Helper()
	q, err := workgh.Replay(recording)
	require.NoError(t, err)
	return github{
		lookPath: func(string) (string, error) { return ghAt, nil },
		query:    func(string) workgh.Query { return q },
		now:      func() time.Time { return fixed },
	}
}

// unreachable is a GitHub no test may reach: gh is not found, and a query, if
// one were made, fails the test.
func unreachable(t *testing.T) github {
	return github{
		lookPath: func(p string) (string, error) { return "", fmt.Errorf("exec: %q: %w", p, exec.ErrNotFound) },
		query: func(string) workgh.Query {
			return func(context.Context, string, map[string]any) ([]byte, error) {
				t.Error("the run reached GitHub")
				return nil, errors.New("no GitHub in this test")
			}
		},
		now: func() time.Time { return fixed },
	}
}

// answering is a GitHub whose gh is found at ghAt and whose every query fails
// with err, as gh does without a login.
func answering(ghAt string, err error) github {
	return github{
		lookPath: func(string) (string, error) { return ghAt, nil },
		query: func(string) workgh.Query {
			return func(context.Context, string, map[string]any) ([]byte, error) { return nil, err }
		},
		now: func() time.Time { return fixed },
	}
}

func workMain(g github) testkit.Main {
	return func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return workTool(g).Run(args, stdin, stdout, stderr)
	}
}

// TestTheToolMeetsTheStandard: the definition states every verb's effect,
// describes every flag, and keeps its how text to five lines of 100.
func TestTheToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, workTool(realGitHub()).Problems())
}

// TestImportThenVerifyIsZeroDifferences (SPEC-WORK-V1 sections 1.6 and
// 1.10): import writes the tree from the recorded fetch; verify against the
// same fetch finds nothing; one changed field is one DRIFT line and exit 1.
func TestImportThenVerifyIsZeroDifferences(t *testing.T) {
	t.Parallel()
	tree := filepath.Join(t.TempDir(), "tree.lisp")
	repo := []string{"--repo", "mas-bandwidth/reliable", "--page-size", "15"}
	res := workMain(recorded(t, "/bin/gh")).Run(append([]string{"import", "--org", "mas-bandwidth", "--out", tree}, repo...)...)
	diag := fmt.Sprintf("import exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Equal(t, 0, res.Code, diag)
	require.Regexp(t, `^IMPORT OK org=mas-bandwidth out=\S+ repos=1 issues=20 .* calls=3 points=\d+ rest=0 seconds=0\.0 gh=/bin/gh\n`, res.Stdout, diag)
	require.Contains(t, res.Stdout, "IMPORT PLAN repos=1 issues=20 est_calls=3 max_calls=1500 page_size=15\n", diag)
	require.Contains(t, res.Stdout, "IMPORT REPO repo=mas-bandwidth/reliable issues=20 ", diag)
	require.NotContains(t, res.Stdout, "dry_run", diag)

	data := testkit.ReadFile(t, tree)
	got := regexp.MustCompile(`sha256=([0-9a-f]{64})`).FindStringSubmatch(res.Stdout)
	require.NotNil(t, got, "the import's sha256 does not name the file written:\n%s", res.Stdout)
	require.Equal(t, sum([]byte(data)), got[1], "the import's sha256 does not name the file written:\n%s", res.Stdout)

	res = workMain(recorded(t, "/bin/gh")).Run(append([]string{"verify", "--tree", tree}, repo...)...)
	diag = fmt.Sprintf("verify exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Equal(t, 0, res.Code, diag)
	require.Regexp(t, `^VERIFY OK tree=\S+ sha256=`+got[1]+` repos=1 issues=20 .* differences=0 missing=0 extra=0 drift=0 gh=/bin/gh\n$`, res.Stdout, diag)

	testkit.WriteFile(t, tree, strings.Replace(data, `:title "`, `:title "changed `, 1))
	res = workMain(recorded(t, "/bin/gh")).Run(append([]string{"verify", "--tree", tree}, repo...)...)
	diag = fmt.Sprintf("verify after a change: exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Equal(t, 1, res.Code, diag)
	require.Empty(t, res.Stdout, diag)
	require.Regexp(t, `(?m)^VERIFY FAIL tree=.* differences=1 missing=0 extra=0 drift=1 gh=/bin/gh$`, res.Stderr, diag)
	require.Regexp(t, `(?m)^VERIFY DRIFT path=repos/mas-bandwidth/reliable/issues/\d+ field=title want="[^"]+" got="changed [^"]+"$`, res.Stderr, diag)

	res = workMain(recorded(t, "/bin/gh")).Run(append([]string{"import", "--org", "mas-bandwidth", "--dry-run"}, repo...)...)
	diag = fmt.Sprintf("dry run exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Equal(t, 0, res.Code, diag)
	require.Regexp(t, `^IMPORT OK org=mas-bandwidth out=- .* dry_run=true\n`, res.Stdout, diag)
}

// TestTheDryRunSaysWhatItReads: a dry run is not offline. It reads GitHub as
// the import does (every call of the recording) and writes nothing, and the
// run, the verb's help and the banner each say so, so no reader takes it for
// an offline rehearsal.
func TestTheDryRunSaysWhatItReads(t *testing.T) {
	t.Parallel()
	res := workMain(recorded(t, "/bin/gh")).Run("import", "--org", "mas-bandwidth", "--repo", "mas-bandwidth/reliable", "--page-size", "15", "--dry-run")
	require.Equal(t, 0, res.Code, "dry run exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, " calls=3 ", "the dry run made fewer calls than the import makes:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "IMPORT NOTE the dry run read GitHub as the import does (calls=3, read-only) and wrote nothing\n", "the run does not say what it read:\n%s", res.Stdout)

	for _, args := range [][]string{{"import", "-h"}, {"help"}} {
		help := workMain(unreachable(t)).Run(args...)
		require.Equal(t, 0, help.Code, "%v exit %d", args, help.Code)
		assert.Regexp(t, `(?s)--dry-run.{0,20}reads?\s+GitHub\s+(exactly\s+)?as\s+the\s+import\s+does.*writes?\s+nothing`, help.Stdout, "%v does not say a dry run reads GitHub and writes nothing:\n%s", args, help.Stdout)
	}
}

// TestTheBudgetIsCheckedBeforeTheIssuesAreRead: a plan past --max-calls is
// refused at exit 2 after the listing, before any issue is read, and the
// remedy is the same import with the budget the plan needs.
func TestTheBudgetIsCheckedBeforeTheIssuesAreRead(t *testing.T) {
	t.Parallel()
	res := workMain(recorded(t, "/bin/gh")).Run("import", "--org", "mas-bandwidth", "--repo", "mas-bandwidth/reliable", "--page-size", "15", "--max-calls", "2", "--dry-run")
	diag := fmt.Sprintf("exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Equal(t, 2, res.Code, diag)
	require.Contains(t, res.Stderr, "IMPORT REFUSED org=mas-bandwidth calls=1 points=1 gh=/bin/gh: the plan needs about 3 calls and --max-calls is 2; "+
		"narrow it with --repo or raise --max-calls; "+
		"run: nova-work import --org mas-bandwidth --repo mas-bandwidth/reliable --dry-run --page-size 15 --max-calls 3\n", diag)
	require.Contains(t, res.Stderr, "IMPORT PLAN repos=1 issues=20 est_calls=3 max_calls=2 page_size=15\n", diag)
}

// TestACouldNotRunIsRefusedInPlainWords (tool ledger K2, K9): a failure to
// run is REFUSED, never FAIL; its reason keeps its spaces; its remedy is the
// next command for that failure.
func TestACouldNotRunIsRefusedInPlainWords(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	notATree := filepath.Join(dir, "t.lisp")
	testkit.WriteFile(t, notATree, "hello\n")
	cases := []struct {
		name   string
		gh     github
		args   []string
		want   string
		remedy string
	}{
		{"gh without a login", answering("/x/gh", errors.New("/x/gh api graphql: exit status 4: To get started with GitHub CLI, please run:  gh auth login")),
			[]string{"import", "--org", "acme", "--repo", "acme/x", "--dry-run"},
			"IMPORT REFUSED org=acme calls=1 points=0 gh=/x/gh: /x/gh api graphql: exit status 4: To get started with GitHub CLI, please run:  gh auth login",
			"/x/gh auth status"},
		{"a file that is no tree", unreachable(t), []string{"verify", "--tree", notATree},
			"VERIFY REFUSED tree=" + notATree + ": workfile: file=" + notATree + " (root): want a (work-tree ...) record", "nova-work verify -h"},
		{"no tree there", unreachable(t), []string{"verify", "--tree", filepath.Join(dir, "none.lisp")},
			"VERIFY REFUSED tree=" + filepath.Join(dir, "none.lisp") + ": stat ", "nova-work import -h"},
		{"gh not found", unreachable(t), []string{"import", "--org", "o", "--dry-run", "--gh", "/nonexistent/gh-cli"},
			`IMPORT REFUSED: the GitHub CLI "/nonexistent/gh-cli" is not found`, "nova-work import -h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := workMain(tc.gh).Run(tc.args...)
			diag := fmt.Sprintf("%v: exit %d\n%s%s", tc.args, res.Code, res.Stdout, res.Stderr)
			assert.Equal(t, 2, res.Code, diag)
			assert.Empty(t, res.Stdout, diag)
			assert.True(t, strings.HasPrefix(res.Stderr, tc.want), "%s\nwant the line to open %q", diag, tc.want)
			assert.True(t, strings.HasSuffix(res.Stderr, "; run: "+tc.remedy+"\n"), "%s\nwant the remedy %q", diag, tc.remedy)
			assert.NotContains(t, res.Stderr, `\x20`, diag)
			assert.NotContains(t, res.Stderr, " FAIL", diag)
		})
	}
}

// TestRefusalsNameTheFlag: a malformed invocation exits 2 with the REFUSED
// word on every line, one line per problem, every problem named at once;
// help exits 0.
func TestRefusalsNameTheFlag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"bare import", []string{"import"}, []string{"IMPORT REFUSED", "--org is required", "--out is required unless --dry-run", "run: nova-work help"}},
		{"bad calls and page size", []string{"import", "--org", "o", "--dry-run", "--page-size", "0", "--max-calls", "0"}, []string{"--max-calls must be positive", "--page-size must be 1 to 100"}},
		{"out and dry run", []string{"import", "--org", "o", "--dry-run", "--out", "t.lisp"}, []string{"--out and --dry-run exclude each other"}},
		{"missing out dir", []string{"import", "--org", "o", "--out", "/nonexistent-dir/t.lisp"}, []string{"does not exist"}},
		{"repo not in org", []string{"import", "--org", "o", "--dry-run", "--repo", "p/r"}, []string{"--repo p/r is not in --org o"}},
		{"bad repo name", []string{"import", "--org", "o", "--dry-run", "--repo", "bad"}, []string{"IMPORT REFUSED", "--repo", "is not owner/name"}},
		{"bogus flag", []string{"import", "--bogus"}, []string{"IMPORT REFUSED", "unknown flag --bogus", "--org", "run: nova-work import -h"}},
		{"bare verify", []string{"verify"}, []string{"VERIFY REFUSED", "--tree is required", "run: nova-work help"}},
		{"bad max-bytes", []string{"verify", "--tree", "t.lisp", "--max-bytes", "0"}, []string{"--max-bytes must be positive"}},
		{"unknown verb", []string{"frob"}, []string{"REFUSED", `unknown verb "frob"`, "import", "verify"}},
		{"no verb", []string{}, []string{"REFUSED", "no verb given", "import", "verify", "run: nova-work help", "\n  NOTE " + preAlpha + "\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := workMain(unreachable(t)).Run(tc.args...)
			assert.Equal(t, 2, res.Code, "%v: exit %d\n%s%s", tc.args, res.Code, res.Stdout, res.Stderr)
			assert.Empty(t, res.Stdout, "%v: a refusal on stdout", tc.args)
			for _, l := range strings.SplitAfter(strings.TrimSuffix(res.Stderr, "\n"), "\n") {
				if strings.TrimSpace(l) != "NOTE "+preAlpha { // the bare command's indented NOTE stage hint
					assert.Contains(t, l, " REFUSED", "%v: a refusal line without the word:\n%s", tc.args, res.Stderr)
				}
			}
			for _, w := range tc.want {
				assert.Contains(t, res.Stderr, w, "%v: stderr lacks %q:\n%s", tc.args, w, res.Stderr)
			}
		})
	}

	helpCases := []struct {
		name string
		args []string
		want string
	}{
		{"bare help", []string{"help"}, "example:"},
		{"-h", []string{"-h"}, "example:"},
		{"help import", []string{"help", "import"}, "effect: local write"},
		{"help verify", []string{"help", "verify"}, "effect: inspection"},
		{"import -h", []string{"import", "-h"}, "--org <string>"},
		{"verify --help", []string{"verify", "--help"}, "--tree <string>"},
		{"version -h", []string{"version", "-h"}, "effect: inspection"},
	}
	for _, tc := range helpCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := workMain(unreachable(t)).Run(tc.args...)
			assert.Equal(t, 0, res.Code, "%v: exit %d\n%s", tc.args, res.Code, res.Stdout)
			assert.Contains(t, res.Stdout, tc.want, "%v: exit %d\n%s", tc.args, res.Code, res.Stdout)
			// The stage, once, in the first lines of every help (the banner's
			// line 2, a verb's -h line 2).
			head := strings.Join(strings.SplitN(res.Stdout, "\n", 3)[:2], "\n")
			assert.Contains(t, head, preAlpha, "%v: the pre-alpha sentence is not in the first two lines:\n%s", tc.args, res.Stdout)
			assert.Equal(t, 1, strings.Count(res.Stdout, preAlpha), "%v: the pre-alpha sentence is not there exactly once:\n%s", tc.args, res.Stdout)
		})
	}
}

// Both spellings of the build identity print the one line at exit 0, like
// every other tool's version verb and its --version alias.
func TestVersionAndItsAliasPrintTheBuildIdentity(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"version", "--version"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()
			res := workMain(unreachable(t)).Run(arg)
			diag := fmt.Sprintf("%s: exit %d, stdout %q, stderr %q; want the identity line at exit 0", arg, res.Code, res.Stdout, res.Stderr)
			assert.Equal(t, 0, res.Code, diag)
			assert.True(t, strings.HasPrefix(res.Stdout, "nova-work "), diag)
			assert.Empty(t, res.Stderr, diag)
		})
	}
}

// TestVerifyHelpShowsATreeTheReaderAccepts (tool ledger K1): verify -h prints
// the tree's grammar with a minimal tree, and that tree reads back; a file
// that is no tree is refused pointing at verify -h.
func TestVerifyHelpShowsATreeTheReaderAccepts(t *testing.T) {
	t.Parallel()
	res := workMain(unreachable(t)).Run("verify", "-h")
	require.Equal(t, 0, res.Code, res.Stderr)
	require.Contains(t, res.Stdout, minimalTree, "verify -h does not print the minimal tree")
	for _, key := range []string{"node-id", "state-reason", "author-association", "lock-reason", "linked-prs"} {
		assert.Contains(t, res.Stdout, key, "verify -h does not name the issue key %s", key)
	}
	tree, err := workfile.Decode("help", []byte(minimalTree), workfile.Limits(len(minimalTree)+1))
	require.NoError(t, err, "the tree verify -h shows does not read")
	assert.Equal(t, "acme", tree.Org)
	require.Len(t, tree.Repos, 1)
}

// TestVerifyAgainstASecondTreeReadsNoNetwork (tool ledger K3): verify
// --against compares two tree files with the same lines as against GitHub,
// with no gh and no network; the worked example of verify -h runs as it is
// written; a flag of the GitHub read beside --against is refused.
func TestVerifyAgainstASecondTreeReadsNoNetwork(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.lisp"), filepath.Join(dir, "b.lisp")
	testkit.WriteFile(t, a, minimalTree)
	testkit.WriteFile(t, b, strings.Replace(minimalTree, ":archived false", ":archived true", 1))
	help := workMain(unreachable(t)).Run("verify", "-h")
	require.Contains(t, help.Stdout, "nova-work verify --tree a.lisp --against b.lisp", "verify -h no longer shows the example this test runs")

	cases := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr []string
	}{
		{"the same tree", []string{"verify", "--tree", a, "--against", a}, 0, "VERIFY OK tree=" + a, nil},
		{"the help's example", []string{"verify", "--tree", a, "--against", b}, 1, "",
			[]string{"VERIFY FAIL tree=" + a, " against=" + b + " ", "differences=1 missing=0 extra=0 drift=1\n",
				`VERIFY DRIFT path=repos/acme/widgets field=archived want="true" got="false"` + "\n"}},
		{"a repository out of scope", []string{"verify", "--tree", a, "--against", b, "--repo", "acme/other"}, 0, "VERIFY OK", nil},
		{"a GitHub flag beside it", []string{"verify", "--tree", a, "--against", b, "--gh", "gh", "--page-size", "5"}, 2, "",
			[]string{"VERIFY REFUSED: --gh reads GitHub and --against reads no network", "VERIFY REFUSED: --page-size reads GitHub"}},
		{"no second tree", []string{"verify", "--tree", a, "--against", filepath.Join(dir, "none.lisp")}, 2, "",
			[]string{"VERIFY REFUSED against=" + filepath.Join(dir, "none.lisp") + ": stat ", "; run: nova-work import -h\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := workMain(unreachable(t)).Run(tc.args...)
			diag := fmt.Sprintf("%v: exit %d\n%s%s", tc.args, res.Code, res.Stdout, res.Stderr)
			assert.Equal(t, tc.code, res.Code, diag)
			if tc.stdout != "" {
				assert.True(t, strings.HasPrefix(res.Stdout, tc.stdout), diag)
				assert.NotContains(t, res.Stdout, " gh=", diag)
			}
			for _, w := range tc.stderr {
				assert.Contains(t, res.Stderr, w, diag)
			}
		})
	}
}
