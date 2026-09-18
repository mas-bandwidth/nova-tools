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

// removeAllAllowlistPath is the shrink-only list of the os.RemoveAll calls this
// repository still permits: the `defer os.RemoveAll(tmp)` of a directory that
// came back from os.MkdirTemp in the same function, and nothing else. Every
// entry is checked in BOTH directions -- a call not listed is a red run, and a
// listed call that has left is a stale entry and also a red run -- so the list
// can only ever get shorter. It lives in testdata so a reader sees the whole
// exception set without reading the test.
const removeAllAllowlistPath = "testdata/removeall_allowlist.txt"

// safepathPkgDir is the one implementation of the removal rule. The walk skips
// it on purpose: safepath.RemoveUnder is where the single os.RemoveAll of a
// computed path is allowed to live, so reading it against itself would be
// circular. Everything OUTSIDE this directory must go through it.
const safepathPkgDir = "internal/safepath"

// TestRemoveAllOnlyOnTempOrThroughSafepath is the class rule Glenn asked for on
// 2026-09-17: "It is just one mistake away from deleting the whole disk. I want
// safety in this tool. It shouldn't be able to delete arbitrary directories."
// It walks every non-test .go file under cmd/ and internal/ (the safepath
// implementation excepted) and refuses any os.RemoveAll whose argument is not a
// variable returned by os.MkdirTemp in the same function. A computed path is
// removed through safepath.RemoveUnder instead, which refuses an empty path, the
// root itself, a path outside its root, and a symlink.
func TestRemoveAllOnlyOnTempOrThroughSafepath(t *testing.T) {
	root := repoRoot(t)
	allow := readRemoveAllAllowlist(t)
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
			if strings.HasPrefix(rel, safepathPkgDir+"/") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, raw, 0)
			if err != nil {
				return err
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				key := rel + ":" + removeAllFuncName(fn)
				tempVars := mkdirTempVars(fn)
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || !isOSRemoveAll(call) {
						return true
					}
					if len(call.Args) != 1 {
						violations = append(violations, fmt.Sprintf(
							"%s: os.RemoveAll with %d arguments; the class rule reads one path", key, len(call.Args)))
						return true
					}
					id, ok := call.Args[0].(*ast.Ident)
					if ok && tempVars[id.Name] {
						seen[key] = true
						if !allow[key] {
							violations = append(violations, fmt.Sprintf(
								"%s:%d: os.RemoveAll on the temp dir %s is not in %s; a new raw removal needs the safepath.RemoveUnder route or an allowlist entry with a reason",
								rel, fset.Position(call.Pos()).Line, id.Name, removeAllAllowlistPath))
						}
						return true
					}
					violations = append(violations, fmt.Sprintf(
						"%s:%d: os.RemoveAll of a computed path; route it through safepath.RemoveUnder(root, path) so an arbitrary directory is refused",
						rel, fset.Position(call.Pos()).Line))
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// The list only shrinks: an entry whose call has left is a red run, so nobody
	// can quietly widen the exception set and leave it there.
	for key := range allow {
		if !seen[key] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, but no os.RemoveAll of a MkdirTemp dir is there any more; delete the stale entry (the list only shrinks)",
				removeAllAllowlistPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// isOSRemoveAll reports whether call is the selector expression `os.RemoveAll`.
func isOSRemoveAll(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "os" && sel.Sel.Name == "RemoveAll"
}

// removeAllFuncName is the allowlist's function key: the method name qualified
// by its receiver when there is one, so a name that reads the same on two types
// is still two entries.
func removeAllFuncName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return receiverName(fn.Recv.List[0].Type) + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// mkdirTempVars is the set of identifiers in fn assigned from os.MkdirTemp. The
// allowlist rule names exactly these: a temp dir the same function made is the
// only raw os.RemoveAll this repository permits.
func mkdirTempVars(fn *ast.FuncDecl) map[string]bool {
	vars := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		temp := false
		for _, rhs := range assign.Rhs {
			if call, ok := rhs.(*ast.CallExpr); ok && isOSMkdirTemp(call) {
				temp = true
			}
		}
		if !temp {
			return true
		}
		for _, lhs := range assign.Lhs {
			if id, ok := lhs.(*ast.Ident); ok {
				vars[id.Name] = true
			}
		}
		return true
	})
	return vars
}

func isOSMkdirTemp(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "os" && sel.Sel.Name == "MkdirTemp"
}

func readRemoveAllAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(removeAllAllowlistPath)
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
