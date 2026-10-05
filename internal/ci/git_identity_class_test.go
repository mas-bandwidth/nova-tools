package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// git_identity_class_test.go holds the rule that every test committing in a scratch
// repository carries its own git identity, through the one helper internal/testgit.
//
// The hurt: TestWallCoverWallCommitsCountsPastBaseRef cloned a scratch origin and ran a
// bare `git commit` in the clone. Every self-hosted bench has a global user.name and
// user.email, so the lander never saw it; the hosted ubuntu-latest runner has none, and
// dev went red there with `Author identity unknown` (run 37344601638, shard 6). A commit
// that leans on the runner's global config passes or fails by machine, not by code.
//
// The rule this test enforces mechanically:
//
//	every commit site in a _test.go under cmd/, internal/ or tools/ -- a call handed
//	the literal argument "commit" or "commit-tree", or a string literal holding a
//	`git commit` command line -- stands in a file that imports internal/testgit, or
//	calls a helper declared in a test file of the same directory that does.
//
// Its narrowings are named where they are made, below. The ledger is counted by site and
// keyed by file, so it only shrinks: a new site in a listed file is as red as a new file.

// gitIdentityAllowlistPath is the counted, shrink-only ledger of the files whose commit
// sites do not yet go through testgit: `file sites reason`.
const gitIdentityAllowlistPath = "testdata/git_identity_allowlist.txt"

// gitIdentityHelper is the import path of the one helper.
const gitIdentityHelper = "github.com/mas-bandwidth/nova-tools/internal/testgit"

// gitIdentityHelperDir is the helper's directory, repo-relative.
const gitIdentityHelperDir = "internal/testgit"

// gitIdentityRemedy is the one thing to do about a finding.
const gitIdentityRemedy = "run the git command with cmd.Env = testgit.Env(t, os.Environ()) (internal/testgit), so the commit carries the test's own author and committer and never the runner's global git config"

// gitIdentityUpdateCommand lowers the ledger's counts once sites are fixed.
const gitIdentityUpdateCommand = "go test -count=1 -timeout 600s -run '^TestNoTestCommitsWithoutItsOwnGitIdentity$' ./internal/ci/"

// gitCommitLine matches a `git commit` or `git commit-tree` command line inside a string
// literal (a shell script a test runs), after any `-C dir` or `-c key=value` options.
// The command takes an option or a tree after it, so prose naming "git commit" is not read.
var gitCommitLine = regexp.MustCompile(`(^|[\s;&|(` + "`" + `])git(\s+-[cC]\s+\S+)*\s+commit(\s+-|-tree\s+\S)`)

// gitIdentitySite is one commit site with no identity of its own.
type gitIdentitySite struct {
	File string // repo-relative, slash-separated
	Line int
	What string // the call or the literal that commits
}

func (s gitIdentitySite) String() string {
	return fmt.Sprintf("%s:%d: %s commits in a scratch repository without testgit, so its identity is whatever the runner's global git config holds (none on a hosted runner: `Author identity unknown`)", s.File, s.Line, s.What)
}

// gitIdentitySource is one test file as the scanner reads it.
type gitIdentitySource struct {
	Rel string
	AST *ast.File
}

// TestNoTestCommitsWithoutItsOwnGitIdentity walks the test files on the CI path and holds
// their commit sites without testgit to the counted ledger.
func TestNoTestCommitsWithoutItsOwnGitIdentity(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	var srcs []gitIdentitySource
	for _, src := range tree.GoFilesUnder(true, "cmd", "internal", "tools") {
		// testdata holds the fixtures this very test reads, so reading it would find the
		// offenders it is meant to find.
		// internal/testgit is the helper itself: its witness commits bare on purpose.
		if !src.Test || src.HasDirNamed("testdata") || src.InDir(gitIdentityHelperDir) {
			continue
		}
		require.NoError(t, src.ParseErr)
		srcs = append(srcs, gitIdentitySource{Rel: src.Rel, AST: src.AST})
	}
	sites := bareGitCommits(tree.FSet, srcs)

	measured := map[string]int{}
	byFile := map[string][]string{}
	for _, s := range sites {
		measured[s.File]++
		byFile[s.File] = append(byFile[s.File], s.String())
	}

	l := loadAllowlist(t, gitIdentityAllowlistPath, allowlist.Options{Ceiling: true, Counted: true})
	for _, row := range l.Rows() {
		if len(strings.Fields(row.Text)) < 3 {
			assert.Failf(t, "git identity ledger", "%s:%d: %q carries no reason after its site count", gitIdentityAllowlistPath, row.Line, row.Text)
		}
	}
	update := allowlist.Updating()
	res := allowlist.CheckCountedMode(t, l, measured, update)
	if update {
		return
	}
	var problems []string
	for _, file := range res.Unlisted {
		problems = append(problems, strings.Join(byFile[file], "\n")+"\n  remedy: "+gitIdentityRemedy)
	}
	for _, row := range res.Over {
		problems = append(problems, fmt.Sprintf("%s: %d commit sites without testgit, over the ledger's %d:\n%s\n  remedy: %s",
			row.Key, row.Measured, row.Listed, strings.Join(byFile[row.Key], "\n"), gitIdentityRemedy))
	}
	for _, row := range res.Lowered {
		problems = append(problems, fmt.Sprintf("%s: %d commit sites without testgit, below the ledger's %d; lower the row: %s=1 %s",
			row.Key, row.Measured, row.Listed, allowlist.UpdateEnv, gitIdentityUpdateCommand))
	}
	for _, row := range res.Stale {
		problems = append(problems, fmt.Sprintf("%s: no commit site without testgit is left, but the ledger lists it; drop the stale row: %s=1 %s",
			row.Key, allowlist.UpdateEnv, gitIdentityUpdateCommand))
	}
	sort.Strings(problems)
	for _, p := range problems {
		assert.Fail(t, p)
	}
}

// TestGitIdentityScannerReadsTheFixtures is the red-test contract: the scanner finds every
// bare commit in the pre-fix fixture and nothing in the fixed one. Without it, a scanner
// that had quietly stopped matching would keep the tree green by finding nothing at all.
func TestGitIdentityScannerReadsTheFixtures(t *testing.T) {
	t.Parallel()

	// scan reads fixtures, each named by its testdata file and the repo path it stands at,
	// into one FileSet, and returns what the scanner finds in them.
	scan := func(fixtures ...[2]string) []string {
		t.Helper()
		fset := token.NewFileSet()
		var srcs []gitIdentitySource
		for _, fx := range fixtures {
			f, err := parser.ParseFile(fset, fx[1], readFile(t, filepath.Join("testdata", "git-identity", fx[0])), parser.SkipObjectResolution)
			require.NoError(t, err)
			srcs = append(srcs, gitIdentitySource{Rel: fx[1], AST: f})
		}
		var got []string
		for _, s := range bareGitCommits(fset, srcs) {
			assert.NotZerof(t, s.Line, "site %s carries no line; a finding a reader cannot open is half a finding", s.What)
			got = append(got, s.What)
		}
		return got
	}

	before := [2]string{"before_test.go.txt", "internal/fixture/before_test.go"}
	assert.Equal(t, []string{
		`git("commit", ...)`,
		`exec.Command("git", "commit", ...)`,
		`r.git("commit-tree", ...)`,
		"a `git commit` command line",
		"a `git commit` command line",
	}, scan(before), "the pre-fix fixture holds five bare commit sites")

	// The fixed file imports testgit; its sibling commits through a helper declared there,
	// and a call to a helper of the same name in another directory is still bare.
	after := [2]string{"after_test.go.txt", "internal/fixture/after_test.go"}
	sibling := [2]string{"sibling_test.go.txt", "internal/fixture/sibling_test.go"}
	elsewhere := [2]string{"sibling_test.go.txt", "internal/other/sibling_test.go"}
	assert.Empty(t, scan(after, sibling), "a file importing testgit and a sibling calling its helper are not bare")
	assert.Equal(t, []string{`coverGit("commit", ...)`}, scan(after, sibling, elsewhere), "a helper is trusted only within its own directory")
}

// bareGitCommits returns the commit sites in srcs that do not go through testgit, in file
// and line order.
//
// Narrowings: it reads syntax, file by file. A file that imports testgit is trusted for
// every site in it, whether or not each command takes testgit.Env. A call is trusted when
// the function or method it names (by its last identifier) is declared in a test file of
// the same directory that imports testgit; a name reached through a variable, a field or
// another package is not resolved. A "commit" in a call's last place, or handed to a
// testify assertion, is a word looked for, not a command. A commit whose "commit" word is
// built at run time, a `git commit` line with no option after it, or a command line split
// across literals, is not seen. internal/testgit itself is not read.
func bareGitCommits(fset *token.FileSet, srcs []gitIdentitySource) []gitIdentitySite {
	trusted := map[string]map[string]bool{} // directory -> helper names declared under testgit
	importsHelper := map[string]bool{}      // rel -> imports testgit
	for _, s := range srcs {
		for _, imp := range s.AST.Imports {
			if p, err := strconv.Unquote(imp.Path.Value); err == nil && p == gitIdentityHelper {
				importsHelper[s.Rel] = true
			}
		}
		if !importsHelper[s.Rel] {
			continue
		}
		dir := path.Dir(s.Rel)
		if trusted[dir] == nil {
			trusted[dir] = map[string]bool{}
		}
		for _, d := range s.AST.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok {
				trusted[dir][fn.Name.Name] = true
			}
		}
	}

	var sites []gitIdentitySite
	for _, s := range srcs {
		if importsHelper[s.Rel] {
			continue
		}
		helpers := trusted[path.Dir(s.Rel)]
		ast.Inspect(s.AST, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				word := gitCommitArg(n.Args)
				if word == "" || gitIdentityAssertion(n.Fun) {
					return true
				}
				name := gitCalleeName(n.Fun)
				if helpers[name] {
					return true
				}
				what := fmt.Sprintf("%s(%q, ...)", gitCallText(n.Fun), word)
				if first, ok := gitArgLit(n.Args, 0); ok && first == "git" {
					what = fmt.Sprintf("%s(%q, %q, ...)", gitCallText(n.Fun), first, word)
				}
				sites = append(sites, gitIdentitySite{File: s.Rel, Line: fset.Position(n.Pos()).Line, What: what})
			case *ast.BasicLit:
				if n.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(n.Value)
				if err != nil {
					return true
				}
				for _, line := range strings.Split(text, "\n") {
					if gitCommitLine.MatchString(line) {
						sites = append(sites, gitIdentitySite{File: s.Rel, Line: fset.Position(n.Pos()).Line, What: "a `git commit` command line"})
						break
					}
				}
			}
			return true
		})
	}
	sort.SliceStable(sites, func(i, j int) bool {
		if sites[i].File != sites[j].File {
			return sites[i].File < sites[j].File
		}
		return sites[i].Line < sites[j].Line
	})
	return sites
}

// gitCommitArg is "commit" or "commit-tree" when one of args but the last is that string
// literal, else "". A git commit always takes more words (a bare one opens an editor), and
// the last place is where assert.Contains and its kind take a word they look for.
func gitCommitArg(args []ast.Expr) string {
	for i := range len(args) - 1 {
		if s, ok := gitArgLit(args, i); ok && (s == "commit" || s == "commit-tree") {
			return s
		}
	}
	return ""
}

// gitArgLit is args[i] as a string literal's value.
func gitArgLit(args []ast.Expr, i int) (string, bool) {
	if i >= len(args) {
		return "", false
	}
	lit, ok := args[i].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// gitIdentityAssertion is a testify call (assert.X, require.X): it looks for a word in
// output and runs nothing.
func gitIdentityAssertion(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && (x.Name == "assert" || x.Name == "require")
}

// gitCalleeName is the last identifier of a call's function: git, r.git and pkg.Run name
// git, git and Run.
func gitCalleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// gitCallText is a call's function as written, for the finding: git, r.git, exec.Command.
func gitCallText(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return gitCallText(f.X) + "." + f.Sel.Name
	}
	return "a call"
}
