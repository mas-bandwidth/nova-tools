package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/workfile"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
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

// countingGitHub is the injected workgh.Query fake: gh is found, and every
// query is counted and answered with an error, so only a refusal that fires
// before any call leaves the count at zero.
func countingGitHub() (github, *atomic.Int64) {
	var calls atomic.Int64
	return github{
		lookPath: func(string) (string, error) { return "/bin/gh", nil },
		query: func(string) workgh.Query {
			return func(context.Context, string, map[string]any) ([]byte, error) {
				calls.Add(1)
				return nil, fmt.Errorf("the query ran before the refusal")
			}
		},
		now: func() time.Time { return fixed },
	}, &calls
}

// TestTheToolMeetsTheStandard: the definition states every verb's effect,
// describes every flag, and keeps its how text to five lines of 100.
func TestTheToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	// .Problems() is the class test's marker (docs/SPEC-CI.md tool-standard).
	// The method is not on this tree, so the banner's what line is what this
	// test holds.
	assert.NotEmpty(t, workTool(realGitHub()).What)
}

// TestVerifyDefaultMaxBytesIsAMemoryBoundNotJustAByteBound pins the verify
// read budget in SPEC-WORK-V1 section 1.2 without allocating a large tree.
func TestVerifyDefaultMaxBytesIsAMemoryBoundNotJustAByteBound(t *testing.T) {
	t.Parallel()
	f := &tool.Flags{FlagSet: flag.NewFlagSet("verify", flag.ContinueOnError)}
	found := false
	for _, verb := range workTool(unreachable(t)).Verbs {
		if verb.Name == "verify" {
			verb.Flags(f)
			found = true
			break
		}
	}
	require.True(t, found, "verify verb is not registered")
	maxBytes := f.Lookup("max-bytes")
	require.NotNil(t, maxBytes, "verify has no --max-bytes flag")
	bound, ok := maxBytes.Value.(flag.Getter).Get().(int)
	require.True(t, ok, "--max-bytes default is not an integer")
	require.Positive(t, bound)
	assert.LessOrEqual(t, bound, 128<<20, "the default admits a tree too large for the memory budget")
	match := regexp.MustCompile(`\bdefault ([0-9]+)\b`).FindStringSubmatch(maxBytes.Usage)
	require.Len(t, match, 2, "--max-bytes help omits its numeric default: %q", maxBytes.Usage)
	assert.Equal(t, fmt.Sprint(bound), match[1], "--max-bytes help disagrees with its registered default")
	help := workMain(unreachable(t)).Run("verify", "-h")
	require.Equal(t, 0, help.Code)
	assert.Contains(t, help.Stdout, maxBytes.Usage)
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
	require.Regexp(t, `(?m)^VERIFY FAILED tree=.* differences=1 missing=0 extra=0 drift=1 gh=/bin/gh$`, res.Stderr, diag)
	require.Regexp(t, `(?m)^VERIFY DRIFT path=repos/mas-bandwidth/reliable/issues/\d+ field=title want="[^"]+" got="changed [^"]+"$`, res.Stderr, diag)

	res = workMain(recorded(t, "/bin/gh")).Run(append([]string{"import", "--org", "mas-bandwidth", "--dry-run"}, repo...)...)
	diag = fmt.Sprintf("dry run exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Equal(t, 0, res.Code, diag)
	require.Regexp(t, `^IMPORT OK org=mas-bandwidth out=- .* dry_run=true\n`, res.Stdout, diag)
}

// TestImportFixtureFirstRunIsWhatTheHelpPrints: import -h prints the first
// run that reads a directory of recorded GraphQL pages, and that command
// runs with no gh (SPEC-WORK-V1 section 1.6).
func TestImportFixtureFirstRunIsWhatTheHelpPrints(t *testing.T) {
	t.Parallel()
	help := workMain(unreachable(t)).Run("import", "-h")
	require.Equal(t, 0, help.Code, help.Stderr)
	const printed = "nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --fixture ./calls --dry-run"
	require.Contains(t, help.Stdout, printed, "import -h does not print the fixture first run:\n%s", help.Stdout)
	line := strings.TrimPrefix(printed, "nova-work ")
	line = strings.ReplaceAll(line, "$ORG", "mas-bandwidth")
	line = strings.ReplaceAll(line, "$REPO", "reliable")
	line = strings.ReplaceAll(line, "./calls", recording)
	res := workMain(unreachable(t)).Run(strings.Fields(line)...)
	diag := res.Stdout + res.Stderr
	require.Equal(t, 0, res.Code, diag)
	assert.Contains(t, res.Stdout, "dry_run=true", diag)
	assert.Contains(t, res.Stdout, "fixture="+recording, diag)
	assert.NotContains(t, res.Stdout, " gh=", diag)
	assert.Contains(t, res.Stdout, "IMPORT NOTE the dry run read the recorded pages in the fixture (calls=3) and wrote nothing", diag)

	empty := t.TempDir()
	bad := workMain(unreachable(t)).Run("import", "--org", "mas-bandwidth", "--repo", "mas-bandwidth/reliable", "--fixture", empty, "--dry-run")
	assert.Equal(t, 2, bad.Code, bad.Stdout+bad.Stderr)
	assert.Contains(t, bad.Stderr, "no call-*.json", bad.Stderr)
	assert.Contains(t, bad.Stderr, "run: nova-work import -h", bad.Stderr)
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

// TestImportRefusesAnOrgWideDryRunAndAnExistingOut (docs/SPEC-WORK-V1.md
// section 1.6; docs/STANDARD.md section 2, ONBOARDING point 2): --dry-run with
// no --repo would spend the organization's whole call budget, and --out naming
// an existing file would replace it. Both are refused in the flag checks,
// before any call, and each refusal line names the next command.
func TestImportRefusesAnOrgWideDryRunAndAnExistingOut(t *testing.T) {
	t.Parallel()
	tree := filepath.Join(t.TempDir(), "tree.lisp")
	testkit.WriteFile(t, tree, "existing")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "org-wide dry run",
			args: []string{"import", "--org", "mas-bandwidth", "--dry-run"},
			want: "nova-work import --org mas-bandwidth --repo mas-bandwidth/<name> --dry-run",
		},
		{
			name: "existing out",
			args: []string{"import", "--org", "mas-bandwidth", "--out", tree},
			want: "nova-work import --org mas-bandwidth --out " + oneline.ShellWord(tree) + " --replace",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g, calls := countingGitHub()
			res := workMain(g).Run(tc.args...)
			require.Equal(t, 2, res.Code, "%s: exit %d\nstdout:\n%s\nstderr:\n%s", tc.name, res.Code, res.Stdout, res.Stderr)
			require.Contains(t, res.Stderr, "REFUSED", "%s: stderr:\n%s", tc.name, res.Stderr)
			require.Contains(t, res.Stderr, tc.want, "%s: the refusal does not name the next command:\n%s", tc.name, res.Stderr)
			require.Zero(t, calls.Load(), "%s: the query ran before the refusal:\n%s", tc.name, res.Stderr)
		})
	}
}

// TestACouldNotRunIsRefusedInPlainWords (tool ledger K2, K9): a failure to
// run is REFUSED, never FAILED; its reason keeps its spaces; its remedy is the
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
		{"gh not found", unreachable(t), []string{"import", "--org", "o", "--dry-run", "--repo", "o/r", "--gh", "/nonexistent/gh-cli"},
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
	treeData, err := workfile.Encode(&workfile.Tree{Source: "github", Org: "o", Fetched: "2026-01-01T00:00:00Z"})
	require.NoError(t, err)
	tree := filepath.Join(t.TempDir(), "tree.lisp")
	testkit.WriteFile(t, tree, string(treeData))
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
		{"two repos outside the tree org", []string{"verify", "--tree", tree, "--repo", "p/one", "--repo", "q/two"},
			[]string{"--repo p/one is not in the tree's organization o", "--repo q/two is not in the tree's organization o"}},
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

// TestVerifyHelpShowsEachKeysValueShape: verify -h carries a short table of
// each key's value shape (SPEC-WORK-V1 section 1.2), so a reader sees what a
// value wants without a second run.
func TestVerifyHelpShowsEachKeysValueShape(t *testing.T) {
	t.Parallel()
	res := workMain(unreachable(t)).Run("verify", "-h")
	require.Equal(t, 0, res.Code, res.Stderr)
	for _, row := range []string{
		":node-id              string",
		":closed               string",
		":state                keyword",
		":state-reason         keyword or ()",
		":origin               :internal or :external",
		":labels               list of strings",
		":milestone            () or (:number <positive integer> :title <string>)",
		":number               positive integer, or 0 when a reference has no source",
	} {
		assert.Contains(t, res.Stdout, row, "verify -h has no shape row %q", row)
	}
}

// TestVerifyNamesEveryTreeProblemInOneRun: one verify names every shape
// problem of the tree file, one REFUSED line each, and does not read GitHub
// (SPEC-WORK-V1 section 1.2).
func TestVerifyNamesEveryTreeProblemInOneRun(t *testing.T) {
	t.Parallel()
	// Each URL is workfile.Web joined with a path, so this file's literals name no host.
	const bad = `(work-tree "v1" :source "github" :org "acme" :fetched "2026-10-02T12:00:00Z"
 :repos ((repo "acme/widgets" :url "` + workfile.Web + `acme/widgets"
          :archived false :issues ((issue 1 :url "` + workfile.Web + `acme/widgets/issues/1"
           :title "t" :state "closed" :state-reason :completed
           :origin :external :author 5
           :author-association :none :created "c" :updated "u" :closed "x"
           :locked false :lock-reason () :labels () :assignees ()
           :milestone () :body "" :comments () :references () :linked-prs ())))))
`
	path := filepath.Join(t.TempDir(), "bad.lisp")
	testkit.WriteFile(t, path, bad)
	res := workMain(unreachable(t)).Run("verify", "--tree", path)
	diag := res.Stdout + res.Stderr
	require.Equal(t, 2, res.Code, diag)
	assert.Contains(t, res.Stderr, "has no :node-id", diag)
	assert.Contains(t, res.Stderr, ":state wants a keyword or ()", diag)
	assert.Contains(t, res.Stderr, ":author wants a string", diag)
	assert.Equal(t, 3, strings.Count(res.Stderr, "VERIFY REFUSED"), diag)
	assert.Contains(t, res.Stderr, "run: nova-work verify -h", diag)
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
			[]string{"VERIFY FAILED tree=" + a, " against=" + b + " ", "differences=1 missing=0 extra=0 drift=1\n",
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

// TestStatusGrammar pins the standard's three words after the verb (OK,
// REFUSED, FAILED; docs/STANDARD.md section 2) and their exit codes for every
// verb of nova-work: OK is 0, FAILED (verify found differences) is 1, and a run
// that could not go on, the budget included, is REFUSED at 2.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()

	type grammarResult struct {
		Word string
		Code int
	}

	cases := []struct {
		name     string
		verb     string
		run      func(t *testing.T) (testkit.Result, string)
		wantWord string
		wantCode int
	}{
		{
			name: "import OK",
			verb: "import",
			run: func(t *testing.T) (testkit.Result, string) {
				res := workMain(recorded(t, "/bin/gh")).Run("import", "--org", "mas-bandwidth", "--repo", "mas-bandwidth/reliable", "--page-size", "15", "--dry-run")
				return res, res.Stdout
			},
			wantWord: "OK",
			wantCode: 0,
		},
		{
			name: "import REFUSED",
			verb: "import",
			run: func(t *testing.T) (testkit.Result, string) {
				res := workMain(unreachable(t)).Run("import")
				return res, res.Stderr
			},
			wantWord: "REFUSED",
			wantCode: 2,
		},
		{
			name: "import budget REFUSED",
			verb: "import",
			run: func(t *testing.T) (testkit.Result, string) {
				res := workMain(recorded(t, "/bin/gh")).Run("import", "--org", "mas-bandwidth", "--repo", "mas-bandwidth/reliable", "--page-size", "15", "--max-calls", "2", "--dry-run")
				return res, res.Stderr
			},
			wantWord: "REFUSED",
			wantCode: 2,
		},
		{
			name: "verify OK",
			verb: "verify",
			run: func(t *testing.T) (testkit.Result, string) {
				tree := filepath.Join(t.TempDir(), "tree.lisp")
				repo := []string{"--repo", "mas-bandwidth/reliable", "--page-size", "15"}
				res := workMain(recorded(t, "/bin/gh")).Run(append([]string{"import", "--org", "mas-bandwidth", "--out", tree}, repo...)...)
				require.Equal(t, 0, res.Code)
				verRes := workMain(recorded(t, "/bin/gh")).Run(append([]string{"verify", "--tree", tree}, repo...)...)
				return verRes, verRes.Stdout
			},
			wantWord: "OK",
			wantCode: 0,
		},
		{
			name: "verify REFUSED",
			verb: "verify",
			run: func(t *testing.T) (testkit.Result, string) {
				res := workMain(unreachable(t)).Run("verify")
				return res, res.Stderr
			},
			wantWord: "REFUSED",
			wantCode: 2,
		},
		{
			name: "verify FAILED",
			verb: "verify",
			run: func(t *testing.T) (testkit.Result, string) {
				tree := filepath.Join(t.TempDir(), "tree.lisp")
				repo := []string{"--repo", "mas-bandwidth/reliable", "--page-size", "15"}
				res := workMain(recorded(t, "/bin/gh")).Run(append([]string{"import", "--org", "mas-bandwidth", "--out", tree}, repo...)...)
				require.Equal(t, 0, res.Code)
				data := testkit.ReadFile(t, tree)
				changed := strings.Replace(data, `:title "`, `:title "changed `, 1)
				testkit.WriteFile(t, tree, changed)
				verRes := workMain(recorded(t, "/bin/gh")).Run(append([]string{"verify", "--tree", tree}, repo...)...)
				return verRes, verRes.Stderr
			},
			wantWord: "FAILED",
			wantCode: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, out := tc.run(t)
			var gotWord string
			verbToken := strings.ToUpper(tc.verb)
			for _, line := range strings.Split(out, "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == verbToken {
					gotWord = strings.TrimSuffix(fields[1], ":")
					break
				}
			}
			assert.Equal(t, grammarResult{Word: tc.wantWord, Code: tc.wantCode}, grammarResult{Word: gotWord, Code: res.Code},
				"%s: want word %s and exit %d, got word %q and exit %d\nstdout:\n%s\nstderr:\n%s",
				tc.name, tc.wantWord, tc.wantCode, gotWord, res.Code, res.Stdout, res.Stderr)
		})
	}
}

// TestTheVerbsRenderJSON pins that both verbs use the common typed output
// rendering (docs/STANDARD.md section 2), including refusal results.
func TestTheVerbsRenderJSON(t *testing.T) {
	t.Parallel()
	cli := workMain(unreachable(t))
	for _, verb := range []string{"import", "verify"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			res := cli.Run(verb, "--json")
			assert.Equal(t, 2, res.Code)
			assert.Contains(t, res.Stdout, `"status":"refused"`)
			assert.Contains(t, res.Stdout, `"verb":"`+verb+`"`)
			assert.Empty(t, res.Stderr)
		})
	}
}

// A bare command names the verbs and recovery in one refusal line, followed
// by its stage note (pkg/tool.Tool.Stage); it never prints the banner.
func TestABareCommandRefusesWithItsStage(t *testing.T) {
	t.Parallel()
	res := workMain(unreachable(t)).Run()
	require.Equal(t, 2, res.Code, "bare nova-work: exit %d, stdout %q, stderr %q", res.Code, res.Stdout, res.Stderr)
	require.Empty(t, res.Stdout, "bare nova-work: exit %d, stdout %q, stderr %q", res.Code, res.Stdout, res.Stderr)
	require.Equal(t, "WORK REFUSED: no verb given; the verbs are import, verify, help, version; run: nova-work help\n  NOTE "+preAlpha+"\n", res.Stderr, "bare nova-work: exit %d, stdout %q, stderr %q", res.Code, res.Stdout, res.Stderr)
}
