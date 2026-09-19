package pulse

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestEveryPublishSiteIsBehindTheSecretScan is the class test Johnny's HOLD of #1838 asked
// for, and it exists because the first cut of the scan guarded ONE of four paths.
//
// It reads this package's own syntax tree, finds EVERY call that publishes a worker's bytes
// -- `git push`, `gh pr create`, `gh pr edit`, and the Forge seam's CreatePR -- and fails
// unless the function holding it calls the one guard, secretFindings. Two package-level
// leaves (`push` and `openPR`) do nothing but run the command; for those the test asserts
// the guard sits in each of their callers instead, and that their callers are exactly the
// functions named here.
//
// A fifth publish site therefore cannot appear without the guard, and a guard deleted from
// an existing one is a named failure rather than a silent hole.
func TestEveryPublishSiteIsBehindTheSecretScan(t *testing.T) {
	// The guard is secretFindings; secretScan is its one wrapper, and the test asserts
	// below that the wrapper really calls it rather than being a second implementation.
	const guard = "secretFindings"
	guardNames := map[string]bool{"secretFindings": true, "secretScan": true}

	// leaves are the package-level helpers that do nothing but run the command; the guard
	// is in their callers. The value is the caller that must hold it.
	leaves := map[string]bool{"push": true, "openPR": true, "CreatePR": true}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	type fn struct {
		key      string // "" or "recvType" + "." + name
		name     string
		file     string
		line     int
		body     *ast.BlockStmt
		publish  []int    // line numbers of the publishing calls it holds
		calls    []string // the bare-identifier calls it makes
		selCalls []string // the selector calls it makes, by final name
		guarded  bool
		isMethod bool
	}
	var funcs []*fn
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		for _, decl := range f.Decls {
			d, ok := decl.(*ast.FuncDecl)
			if !ok || d.Body == nil {
				continue
			}
			cur := &fn{name: d.Name.Name, file: e.Name(), line: fset.Position(d.Pos()).Line, body: d.Body}
			cur.isMethod = d.Recv != nil
			cur.key = d.Name.Name
			if cur.isMethod {
				cur.key = "method." + d.Name.Name
			}
			ast.Inspect(d.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch callee := call.Fun.(type) {
				case *ast.Ident:
					cur.calls = append(cur.calls, callee.Name)
					if guardNames[callee.Name] {
						cur.guarded = true
					}
				case *ast.SelectorExpr:
					cur.selCalls = append(cur.selCalls, callee.Sel.Name)
					if callee.Sel.Name == "CreatePR" {
						cur.publish = append(cur.publish, fset.Position(call.Pos()).Line)
					}
				}
				if publishes(call) {
					cur.publish = append(cur.publish, fset.Position(call.Pos()).Line)
				}
				return true
			})
			funcs = append(funcs, cur)
		}
	}
	if len(funcs) == 0 {
		t.Fatal("no source parsed; the class test read nothing and proves nothing")
	}

	byName := map[string][]*fn{}
	for _, f := range funcs {
		byName[f.name] = append(byName[f.name], f)
	}

	sites := 0
	for _, f := range funcs {
		if len(f.publish) == 0 {
			continue
		}
		sites += len(f.publish)
		if f.guarded {
			continue
		}
		if leaves[f.name] && (!f.isMethod || f.name == "CreatePR") {
			continue
		}
		t.Errorf("%s:%d: %s publishes a worker's bytes (line %v) and never calls %s; every path to a forge passes the one guard",
			f.file, f.line, f.name, f.publish, guard)
	}
	if sites < 5 {
		t.Fatalf("only %d publish sites were recognised; the detector has stopped seeing them", sites)
	}

	// The leaves' callers. A BARE leaf is called by plain identifier, so a method of the
	// same name (manager.openPR) is a publish site of its own above and is never mistaken
	// for a caller here. A SEAM leaf is the shipped implementation behind an interface and
	// is called by selector.
	for _, l := range []struct {
		leaf, wantCaller string
		bySelector       bool
	}{
		{"push", "Harvest", false},
		{"openPR", "Harvest", false},
		{"CreatePR", "harvestBench", true},
	} {
		if len(byName[l.leaf]) == 0 {
			t.Errorf("the leaf %s named here is not in this package any more; re-read the list", l.leaf)
			continue
		}
		var callers []*fn
		for _, f := range funcs {
			if f.name == l.leaf {
				continue
			}
			names := f.calls
			if l.bySelector {
				names = f.selCalls
			}
			for _, c := range names {
				if c == l.leaf {
					callers = append(callers, f)
					break
				}
			}
		}
		var got []string
		for _, c := range callers {
			got = append(got, c.name)
		}
		sort.Strings(got)
		if len(got) != 1 || got[0] != l.wantCaller {
			t.Errorf("the leaf %s is called by %v; the guard is asserted in %s alone, so a new caller must carry it too",
				l.leaf, got, l.wantCaller)
			continue
		}
		for _, c := range callers {
			if !c.guarded {
				t.Errorf("%s:%d: %s calls the leaf %s and never calls %s", c.file, c.line, c.name, l.leaf, guard)
			}
		}
	}
}

// publishes says whether one call hands a worker's bytes to a forge: a `git push`, a
// `gh pr create` or a `gh pr edit`. It reads the call's own string literals, so it does not
// care which runner seam (exec.CommandContext, runChild, gitIn, m.sh) is in front.
func publishes(call *ast.CallExpr) bool {
	var lits []string
	for _, a := range call.Args {
		b, ok := a.(*ast.BasicLit)
		if !ok || b.Kind != token.STRING {
			continue
		}
		if s, err := strconv.Unquote(b.Value); err == nil {
			lits = append(lits, s)
		}
	}
	has := func(w string) bool {
		for _, l := range lits {
			if l == w {
				return true
			}
		}
		return false
	}
	if has("push") {
		return true
	}
	return has("pr") && (has("create") || has("edit"))
}

// TestTheClassTestReadsThisPackage guards the guard: a class test pointed at an empty
// directory passes for the wrong reason.
func TestTheClassTestReadsThisPackage(t *testing.T) {
	if _, err := os.Stat(filepath.Join(".", "harvest_secret.go")); err != nil {
		t.Fatalf("the class test runs in the package directory, and harvest_secret.go is not there: %v", err)
	}
}

// TestTheWrapperIsTheGuard: the class test accepts secretScan as the guard, so secretScan
// must really be secretFindings' wrapper and not a second implementation that could drift.
func TestTheWrapperIsTheGuard(t *testing.T) {
	raw, err := os.ReadFile("harvest_secret.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	i := strings.Index(body, "func secretScan(")
	if i < 0 {
		t.Fatal("secretScan is gone; the class test's guard list names it")
	}
	end := strings.Index(body[i:], "\n}\n")
	if end < 0 || !strings.Contains(body[i:i+end], "return secretFindings(") {
		t.Fatalf("secretScan does not delegate to secretFindings:\n%s", body[i:i+200])
	}
}
