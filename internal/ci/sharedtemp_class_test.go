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

// sharedtemp_class_test.go is the READ side of the temp directory rule, and the
// `testoutpath` class is the write side. That one says a path a tool WRITES is
// named inside t.TempDir(); this one says a directory a test READS is too.
//
// The hurt: internal/review.TestMutateRemovesItsWorktreeOnBothPaths proved that
// review.Mutate removes its throwaway worktree by globbing
// os.TempDir()/nova-review-mutate-* before the run and again after it, and
// refusing any entry that was not there before. On a laptop that is exact. On a
// self-hosted runner, os.TempDir() is SHARED with every other job on the box: a
// sibling shard starting its own mutate between the snapshot and the check puts
// a directory in the glob that this test never made and cannot remove, and the
// run goes red for something no change in it caused. It cost #1341 twice, #1345
// and #1360 in one night.
//
// The remedy is the same one the write side already asks for: the tool takes its
// temp root from the caller, defaulting to os.TempDir(), and the test hands it
// t.TempDir(). The assertion does not weaken -- it stays "nothing was left
// behind" -- it just runs over a directory only this test writes, and the
// snapshot-and-diff dance disappears with it.
//
// The rule this test enforces mechanically:
//
//	no _test.go under cmd/ or internal/ lists a directory built from
//	os.TempDir() -- filepath.Glob, os.ReadDir or ioutil.ReadDir over an
//	expression that names os.TempDir(), directly or through a local variable
//	assigned from it in the same function.
//
// Its narrowings are named where they are made, below. The allowlist is the same
// shape as the other class tests: one `file:function` per line with its reason,
// checked in BOTH directions, so it can only ever shrink.

// sharedTempAllowlistPath is the shrink-only list of the shared-temp reads this
// repository still permits. It lives in testdata so a reader sees the whole
// exception set without reading the test.
const sharedTempAllowlistPath = "testdata/sharedtemp_allowlist.txt"

// sharedTempRemedy is the one thing to do about a finding.
const sharedTempRemedy = "give the tool a temp root option defaulting to os.TempDir(), pass t.TempDir() from the test, and read THAT directory: the assertion stays \"nothing left behind\", over a directory only this test writes"

// sharedTempReaders are the calls that LIST a directory. A call that opens or
// stats ONE named path is not here: knowing a path this test made is still there
// is a question about that path, not about what else the box is doing.
var sharedTempReaders = map[string]map[string]bool{
	"filepath": {"Glob": true},
	"os":       {"ReadDir": true},
	"ioutil":   {"ReadDir": true},
}

// sharedTempFinding is one listing of the shared temp directory, with the file
// and line it sits on and the call that made it.
type sharedTempFinding struct {
	File string // repo-relative, slash-separated
	Func string // the enclosing test or helper, the allowlist's key
	Line int
	Call string // e.g. filepath.Glob
}

func (f sharedTempFinding) String() string {
	return fmt.Sprintf("%s:%d: %s in %s lists a directory built from os.TempDir(), which every other job on a self-hosted runner writes to as well: a sibling's entry appearing mid-test reddens this run for something it did not do; %s",
		f.File, f.Line, f.Call, f.Func, sharedTempRemedy)
}

// TestNoTestGlobsTheSharedTempDir walks the two trees on the CI path and refuses
// a listing of the shared temp directory that is not allowlisted, and an
// allowlist entry that no longer names one.
func TestNoTestGlobsTheSharedTempDir(t *testing.T) {
	root := repoRoot(t)
	allow := readSharedTempAllowlist(t)
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
			found, err := sharedTempReads(rel, raw)
			if err != nil {
				return err
			}
			for _, f := range found {
				key := f.File + ":" + f.Func
				seen[key] = true
				if !allow[key] {
					violations = append(violations, f.String()+"\n  (or add "+key+" to internal/ci/"+sharedTempAllowlistPath+" with a reason)")
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
				"%s lists %s, but no shared-temp listing is there any more; delete the stale entry (the list only shrinks)",
				sharedTempAllowlistPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// TestSharedTempReadScannerReadsTheFixtures is the red-test contract: the scanner
// flags the pre-fix text and says nothing about the fixed text. Without it, a
// scanner that had quietly stopped matching would keep the tree green by finding
// nothing at all.
func TestSharedTempReadScannerReadsTheFixtures(t *testing.T) {
	before := readFile(t, filepath.Join("testdata", "sharedtemp", "before.go.txt"))
	found, err := sharedTempReads("internal/fixture/mutate_test.go", []byte(before))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ fn, call string }{
		{"TestRemovesItsWorktree", "filepath.Glob"},
		{"TestRemovesItsWorktree", "filepath.Glob"},
		{"TestReadsTheTempDirThroughAVariable", "os.ReadDir"},
		{"TestReadsTheTempDirThroughIoutil", "ioutil.ReadDir"},
	}
	if len(found) != len(want) {
		t.Fatalf("the pre-fix fixture holds %d shared-temp listings, the scanner found %d: %v", len(want), len(found), found)
	}
	for i, w := range want {
		got := found[i]
		if got.Func != w.fn || got.Call != w.call {
			t.Errorf("finding %d = %s %s, want %s %s", i, got.Func, got.Call, w.fn, w.call)
		}
		if got.Line == 0 {
			t.Errorf("finding %d carries no line; a finding a reader cannot open is half a finding", i)
		}
	}

	after := readFile(t, filepath.Join("testdata", "sharedtemp", "after.go.txt"))
	found, err = sharedTempReads("internal/fixture/mutate_test.go", []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("the fixed fixture reads its own t.TempDir() and must pass, the scanner found %v", found)
	}
}

// sharedTempReads reads one _test.go and returns every listing of a directory
// built from os.TempDir() in it. rel is the name the findings carry.
//
// Two narrowings, both deliberate:
//
//   - the taint is per FUNCTION and per LOCAL: a variable assigned os.TempDir()
//     anywhere in the same function body taints every listing of it, but a
//     package-level variable, a struct field or a value handed in by a caller is
//     invisible. Following those means becoming a type checker; the shape that
//     hurt is written inline, and that is the shape this reads.
//   - only listings are read. os.Stat, os.Open and os.RemoveAll over a path in
//     the shared temp directory are questions about ONE path this test named,
//     and no sibling job can answer them wrongly.
func sharedTempReads(rel string, src []byte) ([]sharedTempFinding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, err
	}
	var found []sharedTempFinding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		tainted := tempDirLocals(fn.Body)
		name := fn.Name.Name
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			pkg, sel, ok := qualifiedCall(call)
			if !ok || !sharedTempReaders[pkg][sel] {
				return true
			}
			for _, arg := range call.Args {
				if !namesTheSharedTempDir(arg, tainted) {
					continue
				}
				found = append(found, sharedTempFinding{
					File: rel, Func: name, Line: fset.Position(call.Pos()).Line,
					Call: pkg + "." + sel,
				})
				break
			}
			return true
		})
	}
	return found, nil
}

// qualifiedCall reports the `pkg.Sel` of a call written that way.
func qualifiedCall(call *ast.CallExpr) (pkg, sel string, ok bool) {
	fun, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	id, ok := fun.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	return id.Name, fun.Sel.Name, true
}

// tempDirLocals returns the local names assigned from an expression that names
// os.TempDir(), so `root := os.TempDir()` and `dir := filepath.Join(os.TempDir(),
// "x")` are followed one hop. It is order-insensitive, which over-reports only in
// the direction that is still a finding: a name bound to the shared temp
// directory anywhere in the function is the shared temp directory wherever it is
// listed.
func tempDirLocals(body *ast.BlockStmt) map[string]bool {
	tainted := map[string]bool{}
	bind := func(lhs []ast.Expr, rhs []ast.Expr) {
		if len(lhs) != len(rhs) {
			// A multi-value call (v, err := f()) binds no single expression this
			// can attribute, so it binds nothing.
			return
		}
		for i, l := range lhs {
			id, ok := l.(*ast.Ident)
			if !ok || id.Name == "_" {
				continue
			}
			if namesTheSharedTempDir(rhs[i], tainted) {
				tainted[id.Name] = true
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			bind(s.Lhs, s.Rhs)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, 0, len(s.Names))
			for _, id := range s.Names {
				lhs = append(lhs, id)
			}
			bind(lhs, s.Values)
		}
		return true
	})
	return tainted
}

// namesTheSharedTempDir reports whether expr mentions os.TempDir(), or a local
// already known to hold it.
func namesTheSharedTempDir(expr ast.Expr, tainted map[string]bool) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if found {
			return false
		}
		switch e := n.(type) {
		case *ast.CallExpr:
			if pkg, sel, ok := qualifiedCall(e); ok && pkg == "os" && sel == "TempDir" {
				found = true
			}
		case *ast.Ident:
			if tainted[e.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}

func readSharedTempAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(sharedTempAllowlistPath)
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
