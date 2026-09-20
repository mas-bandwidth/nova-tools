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

// benchNameAllowlistPath is the shrink-only list of the functions that take a bench NAME and
// do not resolve it through the machines registry. Every entry carries its reason; every
// entry is checked in BOTH directions -- a function not listed is a red run, and a listed
// function that has left, or has since been guarded, is a stale entry and also a red run --
// so the list can only ever get shorter. It lives in testdata so a reader sees the whole
// exception set without reading the test.
const benchNameAllowlistPath = "testdata/benchname_allowlist.txt"

// benchNamePackages are the two packages where a bench name reaches a machine: the pulse
// library, and the command that parses the flags and holds the ssh. Nothing outside them
// may take a bench name at all, which is a rule the compiler already keeps -- the launcher
// and capacity seams are pulse's types.
var benchNamePackages = []string{"internal/pulse", "cmd/nova-pulse"}

// benchNameResolvers are the calls that turn a NAME into a resolved machine. One of them in
// the body is what this rule asks for: the registry's own guard, the registry lookup, or one
// of the two places in pulse that wrap them (fleetOneBench for the single-bench fleet verbs,
// refuseNonBenches for the fill's whole bench list).
var benchNameResolvers = map[string]bool{
	"RequireBench":      true,
	"ReadRegistry":      true,
	"Lookup":            true,
	"fleetRoleRefusal":  true,
	"fleetPowerRefusal": true,
	"fleetOneBench":     true,
	"refuseNonBenches":  true,
	"surveyBenches":     true,
}

// TestEveryBenchNameIsResolvedThroughTheRegistry is the class rule behind Glenn's lock of
// 2026-09-18: "runner hosts are CI-only -- no card, probe or load on a machine that serves
// the merge group's shards".
//
// The lock was broken by a SHAPE, not by a mistake: a bench name was a bare string, so
// `--bench batman` was just a hostname to ssh to and nothing in the tools knew that batman
// is six CI runners and not a card bench. The rule closes the shape: a function that takes
// a `bench string` must resolve it -- ask internal/fleet's registry whether that machine may
// take work -- or be named here, with the reason, as a narrowing.
//
// The narrowings are real and are listed in full in the allowlist: a line formatter that
// never touches a machine, the two raw seams that pulse.Fill wraps before it ever calls
// them, and the Mac power verbs, which exist FOR the runner hosts and so are the one place
// a runner host is the right answer.
func TestEveryBenchNameIsResolvedThroughTheRegistry(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	allow := readBenchNameAllowlist(t)
	seen := map[string]bool{}
	var violations []string

	for _, dir := range benchNamePackages {
		base := filepath.Join(tree.Root, filepath.FromSlash(dir))
		entries, err := os.ReadDir(base)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			rel := dir + "/" + name
			src := tree.ByRel(rel)
			if src == nil {
				t.Fatalf("%s is in the package directory and not in the shared tree", rel)
			}
			if src.ParseErr != nil {
				t.Fatal(src.ParseErr)
			}
			fset, file := tree.FSet, src.AST
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || !takesABenchName(fn) {
					continue
				}
				key := rel + ":" + benchFuncName(fn)
				if resolvesABenchName(fn.Body) {
					if allow[key] {
						violations = append(violations, fmt.Sprintf(
							"%s resolves its bench name now; delete its entry from %s (the list only shrinks)",
							key, benchNameAllowlistPath))
					}
					continue
				}
				seen[key] = true
				if !allow[key] {
					violations = append(violations, fmt.Sprintf(
						"%s:%d: %s takes a bench NAME and never resolves it; ask the machines registry (fleet.Registry.RequireBench) before the name reaches a machine, or add the function to %s with the reason it is a narrowing",
						rel, fset.Position(fn.Pos()).Line, benchFuncName(fn), benchNameAllowlistPath))
				}
			}
		}
	}
	for key := range allow {
		if !seen[key] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, but no such unresolved bench name is there any more; delete the stale entry (the list only shrinks)",
				benchNameAllowlistPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// takesABenchName reports whether the function has a `bench string` parameter. The name is
// the signal on purpose: a bench name is the thing that is a bare string everywhere, and
// this package has called it `bench` since fill-loop.sh.
func takesABenchName(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		id, ok := field.Type.(*ast.Ident)
		if !ok || id.Name != "string" {
			continue
		}
		for _, name := range field.Names {
			if name.Name == "bench" {
				return true
			}
		}
	}
	return false
}

// resolvesABenchName reports whether the body calls one of the resolvers.
func resolvesABenchName(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if benchNameResolvers[fun.Name] {
				found = true
			}
		case *ast.SelectorExpr:
			if benchNameResolvers[fun.Sel.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}

// benchFuncName is the allowlist's key: the method name qualified by its receiver when
// there is one, so a name that reads the same on two types is still two entries.
func benchFuncName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return receiverName(fn.Recv.List[0].Type) + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// TestTheBenchNameHeuristicReadsWhatItClaims holds the walker against hand-written sources,
// so a rule that quietly stopped matching anything cannot pass as a green run.
func TestTheBenchNameHeuristicReadsWhatItClaims(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		takes    bool
		resolves bool
	}{
		{
			name:  "a bench name and no resolution",
			src:   "func f(bench string) { ssh(bench) }",
			takes: true,
		},
		{
			name:     "a bench name asked of the registry",
			src:      "func f(reg *fleet.Registry, bench string) error { return reg.RequireBench(bench) }",
			takes:    true,
			resolves: true,
		},
		{
			name:     "a bench name behind the fill's list check",
			src:      "func f(bench string) { refuseNonBenches(w, reg, []string{bench}) }",
			takes:    true,
			resolves: true,
		},
		{
			name: "a second string beside the bench",
			src:  "func f(card string) {}",
		},
		{
			name:  "the bench sharing its type with a card",
			src:   "func f(bench, card string) {}",
			takes: true,
		},
		{
			name: "a bench that is not a name",
			src:  "func f(bench FleetBench) {}",
		},
	}
	for _, tc := range cases {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "x.go", "package p\n"+tc.src+"\n", 0)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		fn := file.Decls[0].(*ast.FuncDecl)
		if got := takesABenchName(fn); got != tc.takes {
			t.Errorf("%s: takesABenchName = %v, want %v", tc.name, got, tc.takes)
		}
		if !tc.takes {
			continue
		}
		if got := resolvesABenchName(fn.Body); got != tc.resolves {
			t.Errorf("%s: resolvesABenchName = %v, want %v", tc.name, got, tc.resolves)
		}
	}
}

func readBenchNameAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(benchNameAllowlistPath)
	if err != nil {
		t.Fatal(err)
	}
	allow := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, reason, _ := strings.Cut(line, " ")
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s: %q carries no reason; every narrowing says why it is one", benchNameAllowlistPath, key)
		}
		allow[strings.TrimSpace(key)] = true
	}
	return allow
}
