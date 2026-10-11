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

// gitIdentityAllowlistPath is the counted, shrink-only ledger of the test files that
// still commit without the shared identity: `file count reason` per row, the count the
// number of commit sites in the file. Each was green on a bench only because the bench
// has a global git identity, or names an identity of its own instead of testgit's.
const gitIdentityAllowlistPath = "testdata/git_identity_allowlist.txt"

// testgitDoor is the shared identity's own package: its tests commit with Env
// unqualified, so reading them against the rule would be circular.
const testgitDoor = "pkg/testgit/"

// gitIdentityRemedy is the one thing to do at a refused site.
const gitIdentityRemedy = "run the commit with the shared identity: cmd.Env = testgit.Environ(...) in the helper that runs it (pkg/testgit)"

// gitShellCommit is a `git commit` written into a shell script: git, any `-c k=v` or
// `-C dir`, then commit and a flag. A literal is a script when it carries `&&` or a
// newline; one command's text on its own (a fake git's expected argv) is not read.
var gitShellCommit = regexp.MustCompile(`(^|[\s;&|(])git(\s+-[cC]\s+\S+)*\s+commit\s+-`)

// gitIdentitySite is one commit in a test file, and whether it carries the identity.
type gitIdentitySite struct {
	Rel  string
	Line int
	Kind string // "call", "args" (a [][]string of git commands) or "shell"
	OK   bool
}

// TestNoTestCommitsWithoutTheSharedGitIdentity is the class rule behind the hosted
// ubuntu-latest red of run 37344601638: TestWallCoverWallCommitsCountsPastBaseRef
// committed in a scratch clone with no identity of its own, and only the benches'
// global git config made it green. Every `git commit` in a _test.go under cmd/,
// internal/ and tools/ must run under pkg/testgit's identity: the call is
// testgit's, or the helper it calls (a function, method or closure of the same
// package) names testgit, or, for a direct exec, a command list or a shell script,
// the function it stands in names testgit (docs/SPEC-CI.md, "Tests this spec
// demands").
func TestNoTestCommitsWithoutTheSharedGitIdentity(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	byDir := map[string]map[string]*ast.File{}
	for _, src := range tree.GoFilesUnder(true, "cmd", "internal", "pkg", "tools") {
		if strings.HasPrefix(src.Rel, testgitDoor) || strings.Contains(src.Rel, "/testdata/") {
			continue
		}
		require.NoError(t, src.ParseErr, src.Rel)
		dir := path.Dir(src.Rel)
		if byDir[dir] == nil {
			byDir[dir] = map[string]*ast.File{}
		}
		byDir[dir][src.Rel] = src.AST
	}

	measured := map[string]int{}
	bare := map[string][]string{}
	for _, files := range byDir {
		for _, s := range gitIdentitySites(tree.FSet, files) {
			if s.OK {
				continue
			}
			measured[s.Rel]++
			bare[s.Rel] = append(bare[s.Rel], fmt.Sprintf("%s:%d (%s)", s.Rel, s.Line, s.Kind))
		}
	}

	l := loadAllowlist(t, gitIdentityAllowlistPath, allowlist.Options{Ceiling: true, Counted: true})
	update := allowlist.Updating()
	res := allowlist.CheckCountedMode(t, l, measured, update)
	if update {
		return
	}
	var problems []string
	for _, rel := range res.Unlisted {
		problems = append(problems, fmt.Sprintf("%s: a git commit without the shared identity; %s",
			strings.Join(bare[rel], ", "), gitIdentityRemedy))
	}
	for _, row := range res.Over {
		problems = append(problems, fmt.Sprintf("%s: %d commits without the shared identity, over the ledger's %d (%s); %s",
			row.Key, row.Measured, row.Listed, strings.Join(bare[row.Key], ", "), gitIdentityRemedy))
	}
	for _, row := range res.Lowered {
		problems = append(problems, fmt.Sprintf("%s: %d commits without the shared identity, under the ledger's %d; lower the row; run: %s=1 make test PKGS=./internal/ci",
			row.Key, row.Measured, row.Listed, allowlist.UpdateEnv))
	}
	for _, row := range res.Stale {
		problems = append(problems, fmt.Sprintf("%s lists %s, but every commit there carries the shared identity; delete the stale entry (the list only shrinks); run: %s=1 make test PKGS=./internal/ci",
			gitIdentityAllowlistPath, row.Key, allowlist.UpdateEnv))
	}
	sort.Strings(problems)
	for _, p := range problems {
		assert.Fail(t, p)
	}
}

// TestGitIdentityRuleRefusesItsProbes pins each shape the rule reads, both ways: a bare
// exec, an identity of the test's own (`-c user.name`, its own GIT_AUTHOR_*), a
// [][]string of git commands, a shell script and a helper that does not name testgit
// are refused; the same through testgit, a helper that names it and a "commit" that is
// not a git command are not.
func TestGitIdentityRuleRefusesItsProbes(t *testing.T) {
	t.Parallel()

	const head = "package p\n\nimport (\n\t\"os/exec\"\n\t\"testing\"\n\n\t\"github.com/mas-bandwidth/nova-tools/pkg/testgit\"\n)\n\nvar _ = testgit.Env\nvar _ = exec.Command\n\n"
	cases := []struct {
		name, src string
		want      []bool // OK of each site, in source order
	}{
		{"bare exec", `func f() { exec.Command("git", "commit", "-m", "x").Run() }`, []bool{false}},
		{"exec through testgit", `func f() { c := exec.Command("git", "commit", "-m", "x"); c.Env = testgit.Environ(); c.Run() }`, []bool{true}},
		{"its own -c identity", `func f() { exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "x").Run() }`, []bool{false}},
		{"its own GIT_AUTHOR", `func f() { c := exec.Command("git", "commit", "-m", "x"); c.Env = []string{"GIT_AUTHOR_NAME=t"}; c.Run() }`, []bool{false}},
		{"a helper without testgit", "func gitAs(t *testing.T, dir string, args ...string) { exec.Command(\"git\", args...).Run() }\nfunc f(t *testing.T) { gitAs(t, \".\", \"commit\", \"-m\", \"x\") }", []bool{false}},
		{"a helper with testgit", "func gitAs(t *testing.T, dir string, args ...string) { c := exec.Command(\"git\", args...); c.Env = testgit.Environ(); c.Run() }\nfunc f(t *testing.T) { gitAs(t, \".\", \"commit\", \"-m\", \"x\") }", []bool{true}},
		{"a closure that runs git", "func f(t *testing.T) { run := func(args ...string) { exec.Command(\"git\", args...).Run() }; run(\"commit\", \"-m\", \"x\") }", []bool{false}},
		{"a method helper with testgit", "type r struct{}\nfunc (r) must(args ...string) { c := exec.Command(\"git\", args...); c.Env = testgit.Environ(); c.Run() }\nfunc f() { r{}.must(\"commit\", \"-m\", \"x\") }", []bool{true}},
		{"a list of git commands", `func f() { for _, a := range [][]string{{"init", "-q"}, {"commit", "-q", "-m", "x"}} { exec.Command("git", a...).Run() } }`, []bool{false}},
		{"a shell script", "func f() { exec.Command(\"sh\", \"-c\", \"echo x >> f && git " + "commit -q -am x\").Run() }", []bool{false}},
		{"a script of lines", "func f() { exec.Command(\"sh\", \"-c\", `cd repo\ngit " + "commit -q -am x`).Run() }", []bool{false}},
		{"one command's text, not a script", "func f() { _ = \"git " + "commit --amend -q -m x\" }", nil},
		{"a shell script under testgit", "func f() { c := exec.Command(\"sh\", \"-c\", \"cd . && git -C repo " + "commit -q -am x\"); c.Env = testgit.Environ(); c.Run() }", []bool{true}},
		{"a word, not a command", "func f(t *testing.T, plan string) { _ = []string{\"checkout\", \"add\", \"commit\"}; field(t, plan, \"commit\"); _ = \"git commit: %v\" }\nfunc field(t *testing.T, plan, name string) {}", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "p/x_test.go", head+tc.src+"\n", 0)
			require.NoError(t, err)
			var got []bool
			for _, s := range gitIdentitySites(fset, map[string]*ast.File{"p/x_test.go": f}) {
				got = append(got, s.OK)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// gitIdentitySites is every commit in one package's test files, sorted by file and line.
func gitIdentitySites(fset *token.FileSet, files map[string]*ast.File) []gitIdentitySite {
	// The package's functions and methods by name, and each file's closures by name:
	// a call is resolved to every body that name could mean.
	decls := map[string][]ast.Node{}
	closures := map[string]map[string][]ast.Node{}
	for rel, f := range files {
		closures[rel] = map[string][]ast.Node{}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
				decls[fn.Name.Name] = append(decls[fn.Name.Name], fn.Body)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == len(as.Rhs) {
				for i, rhs := range as.Rhs {
					id, okID := as.Lhs[i].(*ast.Ident)
					lit, okLit := rhs.(*ast.FuncLit)
					if okID && okLit {
						closures[rel][id.Name] = append(closures[rel][id.Name], lit.Body)
					}
				}
			}
			return true
		})
	}
	resolve := func(rel string, call *ast.CallExpr) (string, []ast.Node, bool) {
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if bodies := closures[rel][fun.Name]; len(bodies) > 0 {
				return fun.Name, bodies, false
			}
			return fun.Name, decls[fun.Name], false
		case *ast.SelectorExpr:
			if id, ok := fun.X.(*ast.Ident); ok && id.Name == "testgit" {
				return fun.Sel.Name, nil, true
			}
			return fun.Sel.Name, decls[fun.Sel.Name], false
		}
		return "", nil, false
	}
	allName := func(bodies []ast.Node) bool {
		for _, b := range bodies {
			if !namesTestgit(b) {
				return false
			}
		}
		return len(bodies) > 0
	}

	var sites []gitIdentitySite
	for rel, f := range files {
		// stack is the enclosing functions of the node being visited, innermost last.
		var stack []ast.Node
		var visit func(n ast.Node) bool
		visit = func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl, *ast.FuncLit:
				stack = append(stack, n)
				var body *ast.BlockStmt
				if fd, ok := n.(*ast.FuncDecl); ok {
					body = fd.Body
				} else {
					body = n.(*ast.FuncLit).Body
				}
				if body != nil {
					ast.Inspect(body, visit)
				}
				stack = stack[:len(stack)-1]
				return false
			}
			enclosing := func() bool { return len(stack) > 0 && namesTestgit(stack[len(stack)-1]) }
			at := func(n ast.Node) int { return fset.Position(n.Pos()).Line }
			switch n := n.(type) {
			case *ast.CallExpr:
				commit := -1
				for i, a := range n.Args {
					if gitIdentityLit(a) == "commit" {
						commit = i
						break
					}
				}
				if commit < 0 {
					return true
				}
				name, bodies, door := resolve(rel, n)
				gitish := strings.Contains(strings.ToLower(name), "git")
				for _, a := range n.Args[:commit] {
					if gitIdentityLit(a) == "git" {
						gitish = true
					}
					if id, ok := a.(*ast.Ident); ok && strings.Contains(strings.ToLower(id.Name), "git") {
						gitish = true
					}
				}
				for _, b := range bodies {
					if containsStringLit(b, "git") {
						gitish = true
					}
				}
				if !gitish && !door {
					return true
				}
				ok := door || allName(bodies) || (len(bodies) == 0 && enclosing())
				sites = append(sites, gitIdentitySite{Rel: rel, Line: at(n), Kind: "call", OK: ok})
			case *ast.CompositeLit:
				for _, elt := range n.Elts {
					inner, isLit := elt.(*ast.CompositeLit)
					if !isLit || len(inner.Elts) == 0 {
						continue
					}
					first := gitIdentityLit(inner.Elts[0])
					hasCommit := false
					for _, e := range inner.Elts {
						if gitIdentityLit(e) == "commit" {
							hasCommit = true
						}
					}
					if hasCommit && (first == "commit" || strings.HasPrefix(first, "-")) {
						sites = append(sites, gitIdentitySite{Rel: rel, Line: at(inner), Kind: "args", OK: enclosing()})
					}
				}
			case *ast.BasicLit:
				if script := gitIdentityLit(n); (strings.Contains(script, "&&") || strings.Contains(script, "\n")) && gitShellCommit.MatchString(script) {
					sites = append(sites, gitIdentitySite{Rel: rel, Line: at(n), Kind: "shell", OK: enclosing()})
				}
			}
			return true
		}
		for _, d := range f.Decls {
			ast.Inspect(d, visit)
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].Rel != sites[j].Rel {
			return sites[i].Rel < sites[j].Rel
		}
		return sites[i].Line < sites[j].Line
	})
	return sites
}

// namesTestgit reports whether n refers to the testgit package anywhere inside it.
func namesTestgit(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "testgit" {
				found = true
			}
		}
		return !found
	})
	return found
}

// containsStringLit reports whether the string literal s stands anywhere inside n.
func containsStringLit(n ast.Node, s string) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if gitIdentityLit(n) == s {
			found = true
		}
		return !found
	})
	return found
}

// gitIdentityLit is the value of a string literal, or "" for anything else.
func gitIdentityLit(n ast.Node) string {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}
