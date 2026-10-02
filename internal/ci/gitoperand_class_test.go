package ci

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The class test behind issue mas-bandwidth/ideas#829: a card is untrusted input, and
// a value a card supplies (base-repo:, base-sha:, a BASE: ref, a PR-HEAD:) that reaches
// git as a positional is read as an option when it starts with `-`. Every git call under
// internal/swarm and cmd/nova-swarm puts such an operand behind `--` (or behind
// `--end-of-options`, the form for a rev that `--` would turn into a path), so git reads
// every word after the separator as an operand and nothing else.
//
// The test reads the parse tree, never the text. A git call is:
//
//   - a call of a swarm git helper (stageGit, baseGit, gitOut, gitOutput), or of gitrun
//     (found by import path, whatever its local name): Run, Output, Combined, Command, Prepare;
//   - exec.Command or exec.CommandContext whose program is the literal "git";
//   - an argv built apart from its call: a []string literal whose first element is a git
//     subcommand, and append calls onto the identifier it is assigned to.
//
// An argument is fine when it is a string literal, when it is the value of an option that
// takes one (-C dir, -B branch, -e pattern, --reference dir), when it follows a literal
// `--` or `--end-of-options`, or when it is a concatenation that begins with a literal
// that does not start with `-` ("origin/"+ref). Any other argument is a card-derived
// operand with no separator before it, and is a finding. A final `args...` spread is not
// read: the argv it spreads is read where it is built.
const (
	gitrunPath = "github.com/mas-bandwidth/nova-tools/internal/gitrun"
)

// gitOperandDirs are where card values reach git.
var gitOperandDirs = []string{"internal/swarm", "cmd/nova-swarm"}

// gitOperandHelpers are the swarm helpers that run a git, with the count of leading
// parameters before the argv.
var gitOperandHelpers = map[string]int{"stageGit": 1, "stageCmd": 1, "baseGit": 1, "gitOut": 1, "gitOutput": 1}

// gitOperandGitrun are the gitrun runners: a context and Options, then the argv.
var gitOperandGitrun = map[string]bool{"Run": true, "Output": true, "Combined": true, "Command": true, "Prepare": true}

// gitOperandSubcommands start a git argv literal.
var gitOperandSubcommands = map[string]bool{
	"clone": true, "fetch": true, "checkout": true, "remote": true, "rev-parse": true,
	"cat-file": true, "show": true, "ls-tree": true, "grep": true, "rev-list": true,
	"config": true, "init": true, "add": true, "commit": true, "push": true, "pull": true,
	"diff": true, "log": true, "switch": true, "branch": true, "worktree": true, "merge-base": true,
}

// gitOperandValueOptions take the next argument as their value, so that argument is the
// option's, never an operand.
var gitOperandValueOptions = map[string]bool{
	"-C": true, "-B": true, "-b": true, "-c": true, "-m": true,
	"--reference": true, "--format": true, "--depth": true, "--branch": true,
}

// gitOperandSubcommandValueOptions are value-taking options of one subcommand only: grep's
// -e takes a pattern, cat-file's -e takes nothing and the operand after it is an object.
var gitOperandSubcommandValueOptions = map[string]map[string]bool{
	"grep": {"-e": true},
}

// gitOperandAllowed names the sites allowed an operand with no separator, as "file:Func"
// with the reason: a site whose operand is held to a shape git printed, where no separator works.
var gitOperandAllowed = map[string]string{
	"internal/swarm/lintbase.go:testDefinedAt": "git grep 2.43 reads --end-of-options before a tree-ish as a revision; the function refuses any tree that is not the 40 hex digits of rev-parse",
}

// gitSubcommandOf is the first literal of an argv that is neither an option nor the value
// of one: the git subcommand ("" when there is none to read).
func gitSubcommandOf(args []ast.Expr) string {
	valueNext := false
	for _, a := range args {
		bl, ok := a.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			valueNext = false
			continue
		}
		s, err := strconv.Unquote(bl.Value)
		if err != nil {
			return ""
		}
		switch {
		case valueNext:
			valueNext = false
		case gitOperandValueOptions[s]:
			valueNext = true
		case !strings.HasPrefix(s, "-"):
			return s
		}
	}
	return ""
}

func gitOperandFindings(rel string, src []byte) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return []string{rel + ": " + err.Error()}
	}
	names := map[string]string{}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		local := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		names[path] = local
	}
	execName, gitrunName := names[osExecPath], names[gitrunPath]
	at := func(n ast.Node) string { return rel + ":" + strconv.Itoa(fset.Position(n.Pos()).Line) }
	var out []string

	stringLit := func(e ast.Expr) (string, bool) {
		bl, ok := e.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(bl.Value)
		return s, err == nil
	}
	// prefixSafe is a concatenation whose left-most operand is a string literal that
	// does not begin with `-`: the operand cannot start with one.
	var prefixSafe func(e ast.Expr) bool
	prefixSafe = func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.BinaryExpr:
			if x.Op != token.ADD {
				return false
			}
			if s, ok := stringLit(x.X); ok {
				return s != "" && !strings.HasPrefix(s, "-")
			}
			return prefixSafe(x.X)
		case *ast.ParenExpr:
			return prefixSafe(x.X)
		}
		return false
	}
	// read checks one argv: args is its elements, spread says the last is an `x...`.
	read := func(args []ast.Expr, spread bool, fn string) {
		afterSep, valueNext := false, false
		sub := gitSubcommandOf(args)
		for i, a := range args {
			if spread && i == len(args)-1 {
				continue
			}
			if s, ok := stringLit(a); ok {
				if s == "--" || s == "--end-of-options" {
					afterSep = true
				}
				valueNext = gitOperandValueOptions[s] || gitOperandSubcommandValueOptions[sub][s]
				continue
			}
			if afterSep || valueNext || prefixSafe(a) {
				valueNext = false
				continue
			}
			if gitOperandAllowed[rel+":"+fn] != "" {
				continue
			}
			out = append(out, at(a)+": a git operand that is not a literal has no `--` or `--end-of-options` before it; a card-derived value starting with `-` would be read as an option (ideas#829)")
		}
	}
	gitLiteral := func(e ast.Expr) bool {
		s, ok := stringLit(e)
		return ok && s == "git"
	}
	check := func(body ast.Node, fn string) {
		argvs := map[string]bool{} // identifiers assigned a git argv literal
		isArgvLit := func(e ast.Expr) bool {
			cl, ok := e.(*ast.CompositeLit)
			if !ok || len(cl.Elts) == 0 {
				return false
			}
			arr, ok := cl.Type.(*ast.ArrayType)
			if !ok {
				return false
			}
			if id, ok := arr.Elt.(*ast.Ident); !ok || id.Name != "string" {
				return false
			}
			s, ok := stringLit(cl.Elts[0])
			return ok && gitOperandSubcommands[s]
		}
		ast.Inspect(body, func(n ast.Node) bool {
			if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == len(as.Rhs) {
				for i, lhs := range as.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && isArgvLit(as.Rhs[i]) {
						argvs[id.Name] = true
					}
				}
			}
			return true
		})
		ast.Inspect(body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CompositeLit:
				if isArgvLit(x) {
					read(x.Elts, false, fn)
				}
			case *ast.CallExpr:
				spread := x.Ellipsis.IsValid()
				switch f := x.Fun.(type) {
				case *ast.Ident:
					if skip, ok := gitOperandHelpers[f.Name]; ok && len(x.Args) >= skip {
						read(x.Args[skip:], spread, fn)
					}
					if f.Name == "append" && len(x.Args) > 1 {
						if id, ok := x.Args[0].(*ast.Ident); ok && argvs[id.Name] {
							read(x.Args[1:], spread, fn)
						}
					}
				case *ast.SelectorExpr:
					pkg, ok := f.X.(*ast.Ident)
					if !ok {
						return true
					}
					switch {
					case gitrunName != "" && pkg.Name == gitrunName && gitOperandGitrun[f.Sel.Name] && len(x.Args) >= 2:
						read(x.Args[2:], spread, fn)
					case execName != "" && pkg.Name == execName && f.Sel.Name == "Command" && len(x.Args) >= 1 && gitLiteral(x.Args[0]):
						read(x.Args[1:], spread, fn)
					case execName != "" && pkg.Name == execName && f.Sel.Name == "CommandContext" && len(x.Args) >= 2 && gitLiteral(x.Args[1]):
						read(x.Args[2:], spread, fn)
					}
				}
			}
			return true
		})
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body != nil {
				check(d.Body, d.Name.Name)
			}
		case *ast.GenDecl:
			check(d, "")
		}
	}
	sort.Strings(out)
	return out
}

func TestCardDerivedGitOperandsFollowTheSeparator(t *testing.T) {
	t.Parallel()

	checked := 0
	for _, f := range repoTree(t).GoFilesUnder(false, gitOperandDirs...) {
		if f.AST == nil || f.HasDirNamed("testdata") {
			continue
		}
		checked++
		for _, finding := range gitOperandFindings(f.Rel, f.Src) {
			assert.Fail(t, finding)
		}
	}
	assert.Positive(t, checked, "the class test read no file under %v", gitOperandDirs)
}

// TestGitOperandClassTestRefusesItsProbes: each shape the rule exists for is red, and its
// neighbour that is fine is green.
func TestGitOperandClassTestRefusesItsProbes(t *testing.T) {
	t.Parallel()

	const head = "package p\n\nimport (\n\t\"context\"\n\t\"os/exec\"\n\n\t\"github.com/mas-bandwidth/nova-tools/internal/gitrun\"\n)\n\nvar _ = context.Background\nvar _ = exec.Command\nvar _ = gitrun.Run\n\n"
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"set-url with a card value and no separator", head + "func f(ctx context.Context, repo string) { stageGit(ctx, \"-C\", \"d\", \"remote\", \"set-url\", \"origin\", repo) }", 1},
		{"set-url behind --", head + "func f(ctx context.Context, repo string) { stageGit(ctx, \"-C\", \"d\", \"remote\", \"set-url\", \"origin\", \"--\", repo) }", 0},
		{"switch to a sha with no separator", head + "func f(ctx context.Context, sha string) { stageGit(ctx, \"switch\", \"-q\", \"-C\", \"b\", sha) }", 1},
		{"switch to a sha behind --end-of-options", head + "func f(ctx context.Context, sha string) { stageGit(ctx, \"switch\", \"-q\", \"-C\", \"b\", \"--end-of-options\", sha) }", 0},
		{"a rev suffixed, no separator", head + "func f(sha string) { baseGit(\"r\", \"rev-parse\", \"--verify\", sha+\"^{commit}\") }", 1},
		{"a rev suffixed, behind --end-of-options", head + "func f(sha string) { baseGit(\"r\", \"rev-parse\", \"--verify\", \"--end-of-options\", sha+\"^{commit}\") }", 0},
		{"a literal prefix makes the operand safe", head + "func f(ref string) { baseGit(\"r\", \"checkout\", \"origin/\"+ref) }", 0},
		{"a literal prefix that is an option is not safe", head + "func f(ref string) { baseGit(\"r\", \"checkout\", \"-\"+ref) }", 1},
		{"the value of -C is the option's", head + "func f(ctx context.Context, dir string) { stageGit(ctx, \"-C\", dir, \"rev-parse\", \"HEAD\") }", 0},
		{"an argv spread is read where it is built", head + "func f(ctx context.Context, argv []string) { stageGit(ctx, argv...) }", 0},
		{"a gitrun call with a card value", head + "func f(ctx context.Context, sha string) { _, _ = gitrun.Output(ctx, gitrun.Options{}, \"show\", sha) }", 1},
		{"a gitrun call behind --end-of-options", head + "func f(ctx context.Context, sha string) { _, _ = gitrun.Output(ctx, gitrun.Options{}, \"show\", \"--end-of-options\", sha) }", 0},
		{"an aliased gitrun", strings.Replace(head, "\"github.com/mas-bandwidth/nova-tools/internal/gitrun\"", "g \"github.com/mas-bandwidth/nova-tools/internal/gitrun\"", 1) + "func f(ctx context.Context, sha string) { _, _ = g.Output(ctx, g.Options{}, \"show\", sha) }", 1},
		{"exec.CommandContext of git with a card value", head + "func f(ctx context.Context, sha string) { _ = exec.CommandContext(ctx, \"git\", \"show\", sha) }", 1},
		{"exec.Command of git behind --", head + "func f(sha string) { _ = exec.Command(\"git\", \"show\", \"--\", sha) }", 0},
		{"exec.Command of another program", head + "func f(sha string) { _ = exec.Command(\"ls\", sha) }", 0},
		{"a clone argv literal with a card value", head + "func f(src string) []string { return []string{\"clone\", \"-q\", src} }", 1},
		{"a clone argv literal behind --", head + "func f(src string) []string { return []string{\"clone\", \"-q\", \"--\", src} }", 0},
		{"an append onto a clone argv", head + "func f(m string) []string {\n\targs := []string{\"clone\", \"-q\"}\n\treturn append(args, \"--reference\", m, \"--dissociate\", m)\n}", 1},
		{"an append onto a clone argv behind --", head + "func f(m string) []string {\n\targs := []string{\"clone\", \"-q\"}\n\treturn append(args, \"--reference\", m, \"--dissociate\", \"--\", m)\n}", 0},
		{"an append onto some other slice", head + "func f(m string) []string {\n\targs := []string{\"a\", \"b\"}\n\treturn append(args, m)\n}", 0},
		{"cat-file -e takes no value: the operand after it is read", head + "func f(ctx context.Context, sha string) { stageGit(ctx, \"cat-file\", \"-e\", sha+\"^{commit}\") }", 1},
		{"cat-file -e behind --end-of-options", head + "func f(ctx context.Context, sha string) { stageGit(ctx, \"cat-file\", \"-e\", \"--end-of-options\", sha+\"^{commit}\") }", 0},
		{"git -C dir then grep -e: the pattern is the value", head + "func f(ctx context.Context, pat string) { stageGit(ctx, \"-C\", \"d\", \"grep\", \"-e\", pat) }", 0},
		{"a grep tree-ish with no separator", head + "func f(pat, sha string) []string { return []string{\"grep\", \"-q\", \"-e\", pat, sha, \"--\"} }", 1},
		{"a grep pattern is the value of -e", head + "func f(pat, sha string) []string { return []string{\"grep\", \"-q\", \"-e\", pat, \"--end-of-options\", sha, \"--\"} }", 0},
		{"a separator in a comment is not one", head + "func f(sha string) {\n\t// -- before sha\n\tbaseGit(\"r\", \"show\", sha)\n}", 1},
	}
	for _, c := range cases {
		got := gitOperandFindings("internal/swarm/probe.go", []byte(c.src))
		assert.Len(t, got, c.want, "%s: %v", c.name, got)
	}

	// The real stage.go is read, so a probe above is not the only proof the rule sees it.
	f := stageFile(t)
	require.NotNil(t, f, "internal/swarm/stage.go is not in the tree")
	assert.Empty(t, gitOperandFindings(f.Rel, f.Src))
	stripped := strings.Replace(string(f.Src), "\"remote\", \"set-url\", \"origin\", \"--\", baseRepo", "\"remote\", \"set-url\", \"origin\", baseRepo", 1)
	require.NotEqual(t, string(f.Src), stripped, "the set-url call moved; re-aim this probe")
	assert.Len(t, gitOperandFindings(f.Rel, []byte(stripped)), 1, "stage.go without its `--` before the card's base-repo is red")
}

func TestGitOperandClassTestSeesTheCatFileSeparatorInStage(t *testing.T) {
	t.Parallel()

	f := stageFile(t)
	require.NotNil(t, f, "internal/swarm/stage.go is not in the tree")
	stripped := strings.Replace(string(f.Src), "\"cat-file\", \"-e\", \"--end-of-options\", baseSha", "\"cat-file\", \"-e\", baseSha", 1)
	require.NotEqual(t, string(f.Src), stripped, "the cat-file call moved; re-aim this probe")
	assert.Len(t, gitOperandFindings(f.Rel, []byte(stripped)), 1, "stage.go without its separator before the card's base-sha in cat-file is red")
}

func stageFile(t *testing.T) *treeFile {
	t.Helper()
	for _, f := range repoTree(t).GoFilesUnder(false, "internal/swarm") {
		if f.Rel == "internal/swarm/stage.go" {
			return f
		}
	}
	return nil
}
