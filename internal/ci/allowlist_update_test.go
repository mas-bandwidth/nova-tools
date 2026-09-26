package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// shrinkOnly is the options of every list in internal/ci/testdata: keyed by the
// row's first field, and ceiling-only -- each list's header says it only shrinks,
// so NOVA_CI_UPDATE=1 drops stale rows and refuses to add one (nova-tools#4339).
var shrinkOnly = allowlist.Options{Ceiling: true}

// loadAllowlist reads a class test's list through the one helper; a list that
// cannot be read is a broken test, not an empty list.
func loadAllowlist(t *testing.T, path string, opt allowlist.Options) *allowlist.List {
	t.Helper()
	l, err := allowlist.Load(filepath.FromSlash(path), opt)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// listFilePatterns name the allowlists under internal/ci/testdata: the shapes
// every list file there is spelt in today.
var listFilePatterns = []string{"*allowlist*.txt", "*.allow", "*_examples.txt"}

// TestEveryAllowlistIsReadThroughTheOneHelper is the class test of #4339: every
// list file under internal/ci/testdata is loaded by a call to loadAllowlist or
// allowlist.Load somewhere in internal/ci, and nothing there reads one with
// os.ReadFile, os.Open or readFile. A list the helper does not read has no
// NOVA_CI_UPDATE=1 path, and a script would be back to editing it by hand.
//
// The walk is syntactic. A call's path argument is resolved through string
// literals (filepath.Join parts included), package constants, a local variable
// assigned in the same function, and one level of function parameter (every
// call site's argument); a path computed any other way is not seen.
func TestEveryAllowlistIsReadThroughTheOneHelper(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(repoRoot(t), "internal", "ci")
	lists := map[string]bool{}
	for _, pat := range listFilePatterns {
		matches, err := filepath.Glob(filepath.Join(dir, "testdata", pat))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range matches {
			lists[filepath.Base(m)] = true
		}
	}
	if len(lists) == 0 {
		t.Fatal("no list file under internal/ci/testdata; the walk is looking in the wrong place")
	}

	loaded, raw := helperReads(t, dir, lists)
	for _, name := range mapKeysSorted(lists) {
		if !loaded[name] {
			t.Errorf("internal/ci/testdata/%s is not read through allowlist.Load (loadAllowlist in a test); its class test has no %s=1 path, so a removal would edit it by hand",
				name, allowlist.UpdateEnv)
		}
	}
	for _, r := range raw {
		t.Errorf("%s reads a list file directly; read it with loadAllowlist or allowlist.Load (nova-tools#4339)", r)
	}
}

// helperReads parses the Go files of dir and returns the list files a helper
// call loads and every raw read of one.
func helperReads(t *testing.T, dir string, lists map[string]bool) (map[string]bool, []string) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	consts := map[string]string{}
	funcs := map[string]*ast.FuncDecl{}
	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if i < len(vs.Values) {
							if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								if v, err := strconv.Unquote(lit.Value); err == nil {
									consts[name.Name] = v
								}
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil && d.Body != nil {
					funcs[d.Name.Name] = d
				}
			}
		}
	}

	r := listResolver{lists: lists, consts: consts, funcs: funcs}
	loaded := map[string]bool{}
	var raw []string
	for _, fn := range funcs {
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch callName(call.Fun) {
			case "loadAllowlist":
				if len(call.Args) > 1 {
					for _, name := range r.resolve(fn, call.Args[1], 0) {
						loaded[name] = true
					}
				}
			case "allowlist.Load", "allowlist.Parse":
				if len(call.Args) > 0 {
					for _, name := range r.resolve(fn, call.Args[0], 0) {
						loaded[name] = true
					}
				}
			case "os.ReadFile", "os.Open", "readFile":
				arg := call.Args[len(call.Args)-1]
				for _, name := range r.resolve(fn, arg, 0) {
					raw = append(raw, fmt.Sprintf("%s: %s(%s)", fset.Position(call.Pos()), callName(call.Fun), name))
				}
			}
			return true
		})
	}
	sort.Strings(raw)
	return loaded, raw
}

// callName spells a call's function as `name` or `pkg.name`.
func callName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name + "." + f.Sel.Name
		}
	}
	return ""
}

// listResolver turns a path expression into the list files it names.
type listResolver struct {
	lists  map[string]bool
	consts map[string]string
	funcs  map[string]*ast.FuncDecl
}

func (r listResolver) resolve(fn *ast.FuncDecl, e ast.Expr, depth int) []string {
	var out []string
	add := func(v string) {
		if base := path.Base(filepath.ToSlash(v)); r.lists[base] {
			out = append(out, base)
		}
	}
	ast.Inspect(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				if v, err := strconv.Unquote(x.Value); err == nil {
					add(v)
				}
			}
		case *ast.Ident:
			if v, ok := r.consts[x.Name]; ok {
				add(v)
				return true
			}
			out = append(out, r.local(fn, x.Name, depth)...)
		}
		return true
	})
	return out
}

// local resolves a name inside fn: a variable assigned there, or a parameter
// through every call site of fn (one level).
func (r listResolver) local(fn *ast.FuncDecl, name string, depth int) []string {
	var out []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
				if rhs, ok := as.Rhs[i].(*ast.Ident); ok && rhs.Name == name {
					continue
				}
				out = append(out, r.resolve(fn, as.Rhs[i], depth)...)
			}
		}
		return true
	})
	if depth > 0 {
		return out
	}
	idx, i := -1, 0
	for _, field := range fn.Type.Params.List {
		for _, n := range field.Names {
			if n.Name == name {
				idx = i
			}
			i++
		}
	}
	if idx < 0 {
		return out
	}
	for _, caller := range r.funcs {
		ast.Inspect(caller.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if ok && callName(call.Fun) == fn.Name.Name && idx < len(call.Args) {
				out = append(out, r.resolve(caller, call.Args[idx], depth+1)...)
			}
			return true
		})
	}
	return out
}
