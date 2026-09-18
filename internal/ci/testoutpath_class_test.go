package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// testoutpath_class_test.go is the class rule behind the deps.json defect: `go
// test ./cmd/nova-work/` left cmd/nova-work/deps.json in the tree, because the
// usage example the first-run test RUNS names its graph `./deps.json` and the
// test's working directory is the package directory. It is the class #1311
// closed for scratch/ -- a test that leaves a file in the tree -- met again one
// directory along.
//
// The rule a unit test has to keep: a path a tool WRITES is named inside
// t.TempDir(). What this test can see mechanically is narrower than that, and
// deliberately so:
//
//	it reads every _test.go under cmd/ and internal/, finds the calls that run
//	a tool, and refuses a STRING LITERAL relative path given as the value of an
//	output flag (-o, --out, --output, --graph, and their `=`-joined spellings).
//
// A value that is not a literal -- a variable, filepath.Join(t.TempDir(), ...),
// a field -- is never refused: that is how a temp path arrives, and the test
// cannot follow it without becoming a type checker. The heuristic therefore
// under-reports by design, and two limits are worth naming out loud:
//
//   - the defect that prompted it is NOT caught by it. In cmd/nova-work the
//     `./deps.json` was never in the _test.go at all; it came out of the usage
//     banner in main.go and out of docs/TESTS.md, and the test ran it verbatim.
//     What this catches is the same mistake written directly, which is the form
//     it takes everywhere else.
//   - a function that chdirs is skipped whole. `os.Chdir`/`t.Chdir` into a temp
//     directory makes a relative path safe again (cmd/nova-review/decide_test.go
//     does exactly this), and telling the safe chdir from the unsafe one needs
//     to know where it went -- so the conservative answer is to say nothing.
//
// The allowlist is the same shape as the other class tests: one `file:function`
// per line, checked in BOTH directions, so it can only ever shrink.

// testOutPathAllowlistPath is the shrink-only list of the relative output paths
// this repository still permits. It lives in testdata so a reader sees the whole
// exception set without reading the test.
const testOutPathAllowlistPath = "testdata/testoutpath_allowlist.txt"

// testOutPathRemedy is the one thing to do about a finding.
const testOutPathRemedy = "name it inside t.TempDir(): filepath.Join(t.TempDir(), ...)"

// outputFlags are the flags whose value is a path the tool WRITES. The set is
// small on purpose: an input flag (--file, --pool, --templates) may name a
// testdata fixture relative to the package directory, which is correct and
// common, so only flags that name an output are read here.
var outputFlags = map[string]bool{
	"-o": true, "--o": true,
	"-out": true, "--out": true,
	"-output": true, "--output": true,
	"-graph": true, "--graph": true,
}

// outPathFinding is one relative output path, with the file and line it sits on,
// the flag that named it and the value it was given.
type outPathFinding struct {
	File  string // repo-relative, slash-separated
	Func  string // the enclosing test or helper, the allowlist's key
	Line  int
	Flag  string
	Value string
}

func (f outPathFinding) String() string {
	return fmt.Sprintf("%s:%d: %s in %s names %q, a path relative to the test's working directory: the run writes it into the package directory and leaves it in the tree; %s",
		f.File, f.Line, f.Flag, f.Func, f.Value, testOutPathRemedy)
}

// TestToolRunsInTestsWriteIntoATempDir walks the two trees on the CI path and
// refuses a relative output path that is not allowlisted, and an allowlist entry
// that no longer names one.
func TestToolRunsInTestsWriteIntoATempDir(t *testing.T) {
	root := repoRoot(t)
	allow := readTestOutPathAllowlist(t)
	seen := map[string]bool{}
	var violations []string

	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// testdata holds the fixtures this very test reads, so walking it
				// would find the offenders it is meant to find.
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			found, err := relativeOutputPaths(rel, raw)
			if err != nil {
				return err
			}
			for _, f := range found {
				key := f.File + ":" + f.Func
				seen[key] = true
				if !allow[key] {
					violations = append(violations, f.String()+"\n  (or add "+key+" to internal/ci/"+testOutPathAllowlistPath+" with a reason)")
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for key := range allow {
		if !seen[key] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, but no relative output path is there any more; delete the stale entry (the list only shrinks)",
				testOutPathAllowlistPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// TestRelativeOutputPathScannerReadsTheFixtures is the red-test contract: the
// scanner flags the pre-fix text and says nothing about the fixed text. Without
// it, a scanner that had quietly stopped matching would keep the tree green by
// finding nothing at all.
func TestRelativeOutputPathScannerReadsTheFixtures(t *testing.T) {
	before := readFile(t, filepath.Join("testdata", "testoutpath", "before.go.txt"))
	found, err := relativeOutputPaths("cmd/fixture/firstrun_test.go", []byte(before))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("the pre-fix fixture holds two relative output paths, the scanner found %d: %v", len(found), found)
	}
	want := []struct{ fn, flag, value string }{
		{"TestFirstRunExampleRuns", "--graph", "./deps.json"},
		{"TestPacketIsWrittenWhereItIsAsked", "--out", "packet.md"},
	}
	for i, w := range want {
		got := found[i]
		if got.Func != w.fn || got.Flag != w.flag || got.Value != w.value {
			t.Errorf("finding %d = %s %s %q, want %s %s %q", i, got.Func, got.Flag, got.Value, w.fn, w.flag, w.value)
		}
		if got.Line == 0 {
			t.Errorf("finding %d carries no line; a finding a reader cannot open is half a finding", i)
		}
	}

	after := readFile(t, filepath.Join("testdata", "testoutpath", "after.go.txt"))
	found, err = relativeOutputPaths("cmd/fixture/firstrun_test.go", []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("the fixed fixture writes into t.TempDir() and must pass, the scanner found %v", found)
	}
}

// relativeOutputPaths reads one _test.go and returns every relative output path
// literal handed to a tool run in it. rel is the name the findings carry.
func relativeOutputPaths(rel string, src []byte) ([]outPathFinding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, err
	}
	var found []outPathFinding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		// A chdir makes every relative path in the function relative to
		// somewhere else, which this cannot follow: say nothing about it.
		if chdirs(fn.Body) {
			continue
		}
		name := fn.Name.Name
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !runsATool(call) {
				return true
			}
			for _, a := range flagValues(call) {
				found = append(found, outPathFinding{
					File: rel, Func: name, Line: fset.Position(a.pos).Line,
					Flag: a.flag, Value: a.value,
				})
			}
			return true
		})
	}
	return found, nil
}

// runsATool reports whether call runs a tool: os/exec, or one of this
// repository's in-process runners, whose names all begin with `run` (run,
// runCLI, b.run). The set is small and named rather than guessed, so a call
// this does not recognise is simply not read.
func runsATool(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return strings.HasPrefix(strings.ToLower(fun.Name), "run")
	case *ast.SelectorExpr:
		if id, ok := fun.X.(*ast.Ident); ok && id.Name == "exec" {
			return fun.Sel.Name == "Command" || fun.Sel.Name == "CommandContext"
		}
		return strings.HasPrefix(strings.ToLower(fun.Sel.Name), "run")
	}
	return false
}

// argument is one flattened argument of a tool run: its literal value when it is
// a string literal, and otherwise only its position, so that `--out, out` (a
// variable) is told apart from `--out, "packet.md"` by adjacency.
type argument struct {
	value string
	lit   bool
	pos   token.Pos
}

// flagged is one output flag and the relative path it was given.
type flagged struct {
	flag  string
	value string
	pos   token.Pos
}

// flagValues walks the argument list of a tool run -- flattening a []string{...}
// composite literal, which is how the in-process runners are called -- and
// returns every output flag whose value is a relative path literal.
func flagValues(call *ast.CallExpr) []flagged {
	var args []argument
	for _, arg := range call.Args {
		switch a := arg.(type) {
		case *ast.BasicLit:
			args = append(args, literalArg(a))
		case *ast.CompositeLit:
			for _, elt := range a.Elts {
				if lit, ok := elt.(*ast.BasicLit); ok {
					args = append(args, literalArg(lit))
					continue
				}
				args = append(args, argument{pos: elt.Pos()})
			}
		default:
			args = append(args, argument{pos: arg.Pos()})
		}
	}
	var out []flagged
	for i, a := range args {
		if !a.lit {
			continue
		}
		if flag, value, ok := strings.Cut(a.value, "="); ok && outputFlags[flag] {
			if relativeOutputPath(value) {
				out = append(out, flagged{flag: flag, value: value, pos: a.pos})
			}
			continue
		}
		if !outputFlags[a.value] || i+1 >= len(args) {
			continue
		}
		if next := args[i+1]; next.lit && relativeOutputPath(next.value) {
			out = append(out, flagged{flag: a.value, value: next.value, pos: next.pos})
		}
	}
	return out
}

func literalArg(lit *ast.BasicLit) argument {
	if lit.Kind != token.STRING {
		return argument{pos: lit.Pos()}
	}
	return argument{value: strings.Trim(lit.Value, "`\""), lit: true, pos: lit.Pos()}
}

// relativeOutputPath reports whether value is a path relative to the working
// directory. An absolute path is somebody's own choice and is left alone; a
// value carrying `=` is an option, not a path (ssh's BatchMode=yes, `ps -o
// stat=`); a value starting with `-` is the next flag; and a value with neither
// a dot nor a separator is a word (`1`, `json`), not a path this can be sure of.
func relativeOutputPath(value string) bool {
	switch {
	case value == "", filepath.IsAbs(value), strings.HasPrefix(value, "/"):
		return false
	case strings.HasPrefix(value, "-"), strings.Contains(value, "="):
		return false
	}
	return strings.ContainsAny(value, "./")
}

// chdirs reports whether the body calls os.Chdir or t.Chdir.
func chdirs(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Chdir" {
			found = true
		}
		return !found
	})
	return found
}

func readTestOutPathAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(testOutPathAllowlistPath)
	if err != nil {
		t.Fatal(err)
	}
	allow := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		allow[line] = true
	}
	return allow
}
