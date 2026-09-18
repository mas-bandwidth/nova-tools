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

// fieldsIndexAllowlistPath is the shrink-only list of the split-result index and
// slice expressions this repository still permits without a length check in the
// same function. Every row carries its reason, and every row is checked in BOTH
// directions -- an unlisted unchecked index is a red run, and a listed one that
// has left is a stale row and also a red run -- so the list can only get
// shorter. It lives in testdata so a reader sees the whole exception set without
// reading the test.
const fieldsIndexAllowlistPath = "testdata/fieldsindex_allowlist.txt"

// splitFuncs are the splitters whose result is a slice whose length is decided
// by the DATA, not by the code: a line with fewer separators than the code
// expects yields a shorter slice, and the next index panics.
var splitFuncs = map[string]map[string]bool{
	"strings": {"Fields": true, "Split": true},
	"bytes":   {"Fields": true},
}

// TestNoUncheckedFieldsIndex is the class rule behind Emma's #1390: a
// commit-only cursor line reached `fields[2:]` on a two-field slice and the
// walker panicked. The instance was one missing `len(fields) < 3` refusal; the
// class is every index or slice expression on a strings.Fields / strings.Split /
// bytes.Fields result that no `len(x)` comparison in the same function guards.
// It walks every non-test .go file under cmd/ and internal/, and names the ones
// that are neither guarded nor allowlisted with a reason.
func TestNoUncheckedFieldsIndex(t *testing.T) {
	root := repoRoot(t)
	allow := readFieldsIndexAllowlist(t)
	seen := map[string]bool{}
	var violations []string

	for _, dir := range []string{"cmd", "internal"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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
			findings, err := uncheckedSplitIndexes(rel, raw)
			if err != nil {
				return err
			}
			for _, f := range findings {
				seen[f.key] = true
				if !allow[f.key] {
					violations = append(violations, f.line)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// The list only shrinks: a row whose unchecked index has left is a red run,
	// so nobody can quietly widen the exception set and leave it there.
	for key := range allow {
		if !seen[key] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, but no unchecked split index is there any more; delete the stale row (the list only shrinks)",
				fieldsIndexAllowlistPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// splitFinding is one unchecked index or slice expression: the allowlist key it
// would be listed under, and the line a red run prints.
type splitFinding struct {
	key  string
	line string
}

// uncheckedSplitIndexes is the rule itself, over one file's source. It is a
// function over bytes rather than a walk step so the rule can be proved against
// the shape of #1390 and against the shapes it must NOT refuse, without a fixture
// tree on disk.
func uncheckedSplitIndexes(rel string, src []byte) ([]splitFinding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, err
	}
	var findings []splitFinding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		split := splitVars(fn)
		if len(split) == 0 {
			continue
		}
		measured := measuredVars(fn)
		name := fieldsIndexFuncName(fn)
		key := rel + ":" + name
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			target, kind := splitIndexTarget(n, split)
			if target == "" || measured[target] {
				return true
			}
			findings = append(findings, splitFinding{key: key, line: fmt.Sprintf(
				"%s:%d: %s on %s, the result of %s, with no len(%s) comparison in %s; add the length check and a refusal line, or an allowlist row in %s with the reason",
				rel, fset.Position(n.Pos()).Line, kind, target, split[target].call, target, name,
				fieldsIndexAllowlistPath)})
			return true
		})
	}
	return findings, nil
}

// splitOrigin is where a split variable came from: the qualified splitter name
// for the message, and whether that call is one strings.Split guarantees at
// least one element from -- a non-empty separator, which never explodes the
// string into nothing.
type splitOrigin struct {
	call     string
	atLeast1 bool
}

// splitIndexTarget reports the split variable an index or slice expression reads
// and which of the two it is, or "" when n is neither, is in range by
// construction, or measures the slice in the index itself.
//
//   - `x[:]` has no bound that can be out of range.
//   - `x[len(x)-1]` and `x[:len(x)-1]` read the length in the subscript, so the
//     function has measured the slice at the point of use.
//   - `x[0]` and `x[1:]` on a strings.Split with a non-empty separator are in
//     range for EVERY input, because that Split always returns at least one
//     element; refusing them would make the rule noise around the commonest
//     line-and-tab parsing in the tool, and train a reader to allowlist.
func splitIndexTarget(n ast.Node, split map[string]splitOrigin) (string, string) {
	switch e := n.(type) {
	case *ast.IndexExpr:
		id, ok := e.X.(*ast.Ident)
		if !ok {
			return "", ""
		}
		origin, ok := split[id.Name]
		if !ok {
			return "", ""
		}
		if lenArgName(e.Index) == id.Name {
			return "", ""
		}
		if origin.atLeast1 && isIntLiteral(e.Index, 0) {
			return "", ""
		}
		return id.Name, "index expression"
	case *ast.SliceExpr:
		if e.Low == nil && e.High == nil && e.Max == nil {
			return "", ""
		}
		id, ok := e.X.(*ast.Ident)
		if !ok {
			return "", ""
		}
		origin, ok := split[id.Name]
		if !ok {
			return "", ""
		}
		for _, bound := range []ast.Expr{e.Low, e.High, e.Max} {
			if bound != nil && lenArgName(bound) == id.Name {
				return "", ""
			}
		}
		if origin.atLeast1 && e.High == nil && e.Max == nil &&
			(e.Low == nil || isIntLiteral(e.Low, 0) || isIntLiteral(e.Low, 1)) {
			return "", ""
		}
		return id.Name, "slice expression"
	}
	return "", ""
}

// splitVars maps each identifier in fn assigned from strings.Fields,
// strings.Split or bytes.Fields to where it came from. A variable assigned from
// a splitter anywhere in the function counts, because the rule asks about the
// function, not the statement.
func splitVars(fn *ast.FuncDecl) map[string]splitOrigin {
	vars := map[string]splitOrigin{}
	record := func(lhs []ast.Expr, rhs []ast.Expr) {
		if len(lhs) != len(rhs) {
			// A multi-value right-hand side never comes from these splitters.
			return
		}
		for i, r := range rhs {
			call, ok := r.(*ast.CallExpr)
			if !ok {
				continue
			}
			origin, ok := splitCallOrigin(call)
			if !ok {
				continue
			}
			if id, ok := lhs[i].(*ast.Ident); ok && id.Name != "_" {
				vars[id.Name] = origin
			}
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			record(s.Lhs, s.Rhs)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, len(s.Names))
			for i, name := range s.Names {
				lhs[i] = name
			}
			record(lhs, s.Values)
		}
		return true
	})
	return vars
}

// splitCallOrigin reads a call as one of the splitters. strings.Split with a
// separator that is a non-empty string literal returns at least one element for
// every input; a separator that is empty, or computed and therefore possibly
// empty, is not given that guarantee.
func splitCallOrigin(call *ast.CallExpr) (splitOrigin, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return splitOrigin{}, false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || !splitFuncs[pkg.Name][sel.Sel.Name] {
		return splitOrigin{}, false
	}
	origin := splitOrigin{call: pkg.Name + "." + sel.Sel.Name}
	if pkg.Name == "strings" && sel.Sel.Name == "Split" && len(call.Args) == 2 {
		if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING && len(lit.Value) > 2 {
			origin.atLeast1 = true
		}
	}
	return origin, true
}

// measuredVars is the set of identifiers x whose length fn looked at: a `len(x)`
// in a comparison, in the header of a for statement (the reverse walk
// `for i := len(x) - 1; i >= 0; i--` is in range by construction), or as a
// switch tag (`switch len(x) { case 3: ... }` is a comparison per case); and a
// `range x`, which produces only indexes the slice has.
//
// The rule asks only that the length was LOOKED at in the same function: judging
// which branch the comparison guards needs the control-flow graph, and a
// function that measures the slice and then indexes it wrongly is a different
// mistake from the one that never measured it at all.
func measuredVars(fn *ast.FuncDecl) map[string]bool {
	measured := map[string]bool{}
	mark := func(n ast.Node) {
		if n == nil {
			return
		}
		ast.Inspect(n, func(n ast.Node) bool {
			if name := lenArgName(n); name != "" {
				measured[name] = true
			}
			return true
		})
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.BinaryExpr:
			switch s.Op {
			case token.LSS, token.LEQ, token.GTR, token.GEQ, token.EQL, token.NEQ:
				mark(s.X)
				mark(s.Y)
			}
		case *ast.ForStmt:
			mark(s.Init)
			mark(s.Cond)
			mark(s.Post)
		case *ast.SwitchStmt:
			mark(s.Tag)
		case *ast.RangeStmt:
			if id, ok := s.X.(*ast.Ident); ok {
				measured[id.Name] = true
			}
		}
		return true
	})
	return measured
}

// lenArgName is the identifier inside a `len(x)` call, or "" when n is not one.
func lenArgName(n ast.Node) string {
	switch e := n.(type) {
	case *ast.CallExpr:
		id, ok := e.Fun.(*ast.Ident)
		if !ok || id.Name != "len" || len(e.Args) != 1 {
			return ""
		}
		arg, ok := e.Args[0].(*ast.Ident)
		if !ok {
			return ""
		}
		return arg.Name
	case *ast.BinaryExpr:
		if name := lenArgName(e.X); name != "" {
			return name
		}
		return lenArgName(e.Y)
	case *ast.ParenExpr:
		return lenArgName(e.X)
	}
	return ""
}

// isIntLiteral reports whether expr is exactly the untyped integer literal want.
func isIntLiteral(expr ast.Expr, want int) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == fmt.Sprint(want)
}

// fieldsIndexFuncName is the allowlist's function key: the method name qualified
// by its receiver when there is one, so a name that reads the same on two types
// is still two rows.
func fieldsIndexFuncName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return receiverName(fn.Recv.List[0].Type) + "." + fn.Name.Name
	}
	return fn.Name.Name
}

func readFieldsIndexAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(fieldsIndexAllowlistPath)
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
