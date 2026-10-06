package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// gitIdentityLedgerPath is the shrink-only, counted list of the test files that still
// commit in a scratch repository without the shared identity: `<file> <sites> <why>`.
const gitIdentityLedgerPath = "testdata/git-identity_allowlist.txt"

// gitIdentityPkg is the one helper. Its own tests commit bare on purpose (the witness that
// a hosted runner refuses a commit with no identity), so the walk does not read it.
const gitIdentityPkg = "internal/testgit"

// gitIdentityRemedy is the fix every finding names.
const gitIdentityRemedy = "run the test's git with cmd.Env = testgit.Env() (or testgit.EnvFrom(base)) from internal/testgit, " +
	"in the function that runs it or the helper it calls; never lean on the runner's global git config"

// gitIdentityUpdateCommand lowers the ledger after a file is fixed.
const gitIdentityUpdateCommand = "go test -count=1 -run '^TestNoTestCommitsWithoutTheSharedGitIdentity$' ./internal/ci/"

// TestNoTestCommitsWithoutTheSharedGitIdentity is the class rule for dev CI run
// 37344601638, shard 6: TestWallCoverWallCommitsCountsPastBaseRef ran `git commit` in a
// clone with no identity of its own, the self-hosted benches lent it theirs, and the hosted
// ubuntu-latest runner, which has none, refused it with `Author identity unknown`. Every
// _test.go under cmd/, internal/ and tools/ that runs a git commit (a "commit" or
// "commit-tree" argument, or `git ... commit` in an `sh -c` script) does it from a function
// that reaches internal/testgit, or through a helper of the same package that does.
func TestNoTestCommitsWithoutTheSharedGitIdentity(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	byDir := map[string][]gitIdentityFile{}
	for _, f := range tree.GoFilesUnder(true, "cmd", "internal", "tools") {
		if f.HasDirNamed("testdata") || f.InDir(gitIdentityPkg) {
			continue
		}
		require.NoError(t, f.ParseErr)
		dir := path.Dir(f.Rel)
		byDir[dir] = append(byDir[dir], gitIdentityFile{Rel: f.Rel, AST: f.AST})
	}
	measured, sites := map[string]int{}, map[string][]string{}
	for _, files := range byDir {
		for _, s := range bareGitCommits(tree.FSet, files) {
			measured[s.Rel]++
			sites[s.Rel] = append(sites[s.Rel], strconv.Itoa(s.Line))
		}
	}

	l := loadAllowlist(t, gitIdentityLedgerPath, allowlist.Options{Ceiling: true, Counted: true})
	res := allowlist.CheckCountedMode(t, l, measured, allowlist.Updating())
	var problems []string
	for _, rel := range res.Unlisted {
		problems = append(problems, fmt.Sprintf("%s:%s: git commit with no identity of the test's own; remedy: %s",
			rel, strings.Join(sites[rel], ","), gitIdentityRemedy))
	}
	for _, row := range res.Over {
		problems = append(problems, fmt.Sprintf("%s lists %s at %d sites, but %d are there now (lines %s); remedy: %s",
			gitIdentityLedgerPath, row.Key, row.Listed, row.Measured, strings.Join(sites[row.Key], ","), gitIdentityRemedy))
	}
	for _, row := range res.Lowered {
		problems = append(problems, fmt.Sprintf("%s lists %s at %d sites, but %d are there now; lower the row's count to %d; run: %s=1 %s",
			gitIdentityLedgerPath, row.Key, row.Listed, row.Measured, row.Measured, allowlist.UpdateEnv, gitIdentityUpdateCommand))
	}
	for _, row := range res.Stale {
		problems = append(problems, fmt.Sprintf("%s lists %s, but it commits with the shared identity now; delete the stale row (the list only shrinks; run: %s=1 %s)",
			gitIdentityLedgerPath, row.Key, allowlist.UpdateEnv, gitIdentityUpdateCommand))
	}
	sort.Strings(problems)
	for _, p := range problems {
		assert.Fail(t, p)
	}
}

// gitIdentityFile is one test file of a package directory.
type gitIdentityFile struct {
	Rel string
	AST *ast.File
}

// gitIdentitySite is one git commit a test runs without the shared identity.
type gitIdentitySite struct {
	Rel  string
	Line int
}

// gitCommitWords are the git subcommands that write a commit object and so need an
// author and a committer.
var gitCommitWords = map[string]bool{"commit": true, "commit-tree": true}

// gitRunnerPkgs are the packages whose calls run a command: a "commit" argument to any
// other package's function (assert, require, strings, fmt) is a value, not git's word.
var gitRunnerPkgs = map[string]bool{"exec": true, "gitrun": true, "subproc": true}

// shellGitCommit is a git commit in a shell script: `git`, any options and their values,
// then `commit` or `commit-tree` as a word.
var shellGitCommit = regexp.MustCompile(`(^|[\s;&|(])git(\s+-\S+(\s+[^\s-]\S*)?)*\s+commit(-tree)?(\s|$)`)

// bareGitCommits reads the test files of one package directory and returns every git
// commit that is not run with internal/testgit: a site is clear when the top-level
// function it stands in reaches testgit (a closure or a cmd.Env set there counts), or
// when it is an argument of a call to a function or method of the same directory that
// reaches testgit, itself or through a function of the directory it calls.
func bareGitCommits(fset *token.FileSet, files []gitIdentityFile) []gitIdentitySite {
	funcs, methods := map[string][]*ast.FuncDecl{}, map[string][]*ast.FuncDecl{}
	for _, f := range files {
		for _, d := range f.AST.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if fn.Recv != nil {
				methods[fn.Name.Name] = append(methods[fn.Name.Name], fn)
			} else {
				funcs[fn.Name.Name] = append(funcs[fn.Name.Name], fn)
			}
		}
	}
	memo := map[*ast.FuncDecl]bool{}
	var reaches func(fn *ast.FuncDecl) bool
	reaches = func(fn *ast.FuncDecl) bool {
		if v, seen := memo[fn]; seen {
			return v
		}
		memo[fn] = false // a cycle reaches nothing it does not reach directly
		ok := carriesTestgit(fn.Body)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if ok {
				return false
			}
			if call, isCall := n.(*ast.CallExpr); isCall {
				for _, callee := range calleeDecls(call, funcs, methods) {
					if reaches(callee) {
						ok = true
					}
				}
			}
			return !ok
		})
		memo[fn] = ok
		return ok
	}

	var out []gitIdentitySite
	for _, f := range files {
		for _, d := range f.AST.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || carriesTestgit(fn.Body) {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					if !callCommits(x) {
						return true
					}
					for _, callee := range calleeDecls(x, funcs, methods) {
						if reaches(callee) {
							return true
						}
					}
					out = append(out, gitIdentitySite{Rel: f.Rel, Line: fset.Position(x.Pos()).Line})
					return false
				case *ast.CompositeLit:
					// A table of git argument lists run in a loop: the loop's function is the
					// one that must carry the identity, and it does not.
					if compositeCommits(x) {
						out = append(out, gitIdentitySite{Rel: f.Rel, Line: fset.Position(x.Pos()).Line})
						return false
					}
				}
				return true
			})
		}
	}
	return out
}

// callCommits reports whether call hands git a commit: a "commit" or "commit-tree"
// argument, or an `sh -c` script holding `git ... commit`, to a function of the test's own
// or a package that runs commands.
func callCommits(call *ast.CallExpr) bool {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if x, ok := sel.X.(*ast.Ident); ok && x.Obj == nil && isImportLike(x.Name) && !gitRunnerPkgs[x.Name] {
			return false // assert.Equal(t, "commit", kind), strings.Contains(line, "commit")
		}
	}
	shell := false
	for _, a := range call.Args {
		if s, ok := stringLit(a); ok && s == "-c" {
			shell = true
		}
	}
	for _, a := range call.Args {
		s, ok := stringLit(a)
		if !ok {
			continue
		}
		if gitCommitWords[s] || (shell && shellGitCommit.MatchString(s)) {
			return true
		}
	}
	return false
}

// compositeCommits reports whether a []string literal outside any call's argument list
// is a git argument list that commits: a "commit" or "commit-tree" element beside an
// option (`{"commit", "-q", "-m", "x"}`), never a list of verbs (`{"add", "commit", "push"}`).
func compositeCommits(lit *ast.CompositeLit) bool {
	commits, option := false, false
	for _, e := range lit.Elts {
		s, ok := stringLit(e)
		commits = commits || (ok && gitCommitWords[s])
		option = option || (ok && strings.HasPrefix(s, "-"))
	}
	return commits && option
}

// calleeDecls is the same-directory declarations call may run: the function of its name,
// or every method of its selector's name. A call into another package names none.
func calleeDecls(call *ast.CallExpr, funcs, methods map[string][]*ast.FuncDecl) []*ast.FuncDecl {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return funcs[fun.Name]
	case *ast.SelectorExpr:
		if x, ok := fun.X.(*ast.Ident); ok && x.Obj == nil && funcs[x.Name] == nil && isImportLike(x.Name) {
			return nil
		}
		return methods[fun.Sel.Name]
	}
	return nil
}

// isImportLike reports whether name reads as a package qualifier rather than a value:
// the parse is mode 0 with no type information, so a selector on an unresolved
// lower-case identifier is taken as a package's (exec.Command, gitrun.Run).
func isImportLike(name string) bool {
	return name != "" && strings.ToLower(name) == name
}

// carriesTestgit reports whether n refers to the testgit package.
func carriesTestgit(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "testgit" {
				found = true
			}
		}
		return !found
	})
	return found
}

// TestGitIdentityRuleReadsTheShapes holds the reader to the shapes it is for: the
// wall-cover bug (an identity configured in the origin only, then a bare commit in the
// clone) and a bare exec, a closure and a shell script are refused; a helper in a sibling
// file, a method, a loop over argument tables and a two-step helper chain that reach
// testgit are clear, and so is a "commit" that never reaches git as a commit argument.
func TestGitIdentityRuleReadsTheShapes(t *testing.T) {
	t.Parallel()

	refused := map[string]string{
		"config_in_origin_only": `
func coverGit(t *testing.T, dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, _ := cmd.CombinedOutput()
	return string(out)
}
func TestX(t *testing.T) {
	coverGit(t, origin, "config", "user.name", "test")
	coverGit(t, repo, "commit", "-q", "-m", "work")
}`,
		"bare_exec": `
func TestX(t *testing.T) {
	_ = exec.Command("git", "-C", dir, "commit", "-q", "-m", "x").Run()
}`,
		"inline_config_is_not_the_helper": `
func TestX(t *testing.T) {
	_ = exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "x").Run()
}`,
		"closure": `
func TestX(t *testing.T) {
	git := func(args ...string) { _ = exec.Command("git", args...).Run() }
	git("commit-tree", tree, "-m", "x")
}`,
		"shell": `
func TestX(t *testing.T) {
	_ = exec.Command("sh", "-c", "git add -A && git -C repo commit -q -m x").Run()
}`,
		"table": `
func TestX(t *testing.T) {
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "pr"}} {
		_ = exec.Command("git", args...).Run()
	}
}`,
	}
	for name, src := range refused {
		t.Run("refused "+name, func(t *testing.T) {
			t.Parallel()
			got := gitIdentityFixture(t, map[string]string{"a_test.go": src})
			assert.Len(t, got, 1, "%s: want one bare commit, got %v", name, got)
		})
	}

	clear := map[string]map[string]string{
		"helper_in_a_sibling_file": {
			"helpers_test.go": `
func execCmd(t *testing.T, dir, name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Env = append(testgit.Env(), "GIT_TERMINAL_PROMPT=0")
	out, _ := cmd.CombinedOutput()
	return string(out)
}`,
			"a_test.go": `
func TestX(t *testing.T) { execCmd(t, src, "git", "commit", "-q", "-m", "base") }`,
		},
		"method_and_chain": {
			"a_test.go": `
func (r *rig) run(dir string, args ...string) { cmd := exec.Command("git", args...); cmd.Env = testgit.Env(); _ = cmd.Run() }
func (r *rig) git(dir string, args ...string) { r.run(dir, args...) }
func commit(t *testing.T, r *rig) { r.git(r.dir, "commit", "-q", "-m", "x") }`,
		},
		"table_in_a_function_that_carries_it": {
			"a_test.go": `
func addCommit(t *testing.T) {
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "pr"}} {
		cmd := exec.Command("git", args...)
		cmd.Env = testgit.Env()
		_ = cmd.Run()
	}
}`,
		},
		"not_a_git_argument": {
			"a_test.go": `
func TestX(t *testing.T) {
	assert.Equal(t, "commit", kind)
	_ = exec.Command("sh", "-c", "echo commit").Run()
	_ = []string{"commit"}
	for _, verb := range []string{"checkout", "add", "commit", "push"} {
		_ = verb
	}
}`,
		},
	}
	for name, files := range clear {
		t.Run("clear "+name, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, gitIdentityFixture(t, files), name)
		})
	}
}

// gitIdentityFixture parses files (name to the body after a package clause) as one
// package directory and returns the reader's findings.
func gitIdentityFixture(t *testing.T, files map[string]string) []gitIdentitySite {
	t.Helper()
	fset := token.NewFileSet()
	var parsed []gitIdentityFile
	for name, body := range files {
		f, err := parser.ParseFile(fset, name, "package x\n"+body, 0)
		require.NoError(t, err, name)
		parsed = append(parsed, gitIdentityFile{Rel: "x/" + name, AST: f})
	}
	return bareGitCommits(fset, parsed)
}
