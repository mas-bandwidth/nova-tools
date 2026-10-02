package sprint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// One deadline: a work card's deal and take stamps are read only by
// WorkDeadline, which every deadline (the tick's, the no-stall rule's) calls,
// and by the steps that write them. A second reader of dealt or taken is a
// second clock that can speak at another moment.
func TestTheWorkStampsAreReadOnlyByWorkDeadline(t *testing.T) {
	t.Parallel()
	stamps := map[string]bool{"dealt": true, "taken": true, "first_dealt": true, "first_taken": true, "untaken_since": true}
	allowed := map[string]bool{
		"WorkDeadline": true, // the one deadline
		"takenStamps":  true, // writes first_taken once
		"nextGen":      true, // writes untaken_since once per take
		"AttemptLine":  true, // prints an attempt's stamps, judges nothing
		"RouteStats":   true, // a route's mean wall, shown, judges nothing
		"takeStamps":   true, // a take's waiting and running time, recorded and shown, judges nothing (cost.go)
	}
	var dirs []string
	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return err
	})
	require.NoError(t, err)
	var bad []string
	for _, dir := range dirs {
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
		require.NoError(t, err)
		for _, pkg := range pkgs {
			for _, f := range pkg.Files {
				for _, decl := range f.Decls {
					fd, ok := decl.(*ast.FuncDecl)
					if !ok || allowed[fd.Name.Name] {
						continue
					}
					ast.Inspect(fd, func(n ast.Node) bool {
						var lits []*ast.BasicLit
						switch x := n.(type) {
						case *ast.CallExpr: // c.F("dealt")
							if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "F" && len(x.Args) == 1 {
								if l, ok := x.Args[0].(*ast.BasicLit); ok {
									lits = append(lits, l)
								}
							}
						case *ast.AssignStmt: // field := "dealt", then c.F(field); c.Fields["dealt"] on the right
							for _, e := range x.Rhs {
								switch y := e.(type) {
								case *ast.BasicLit:
									lits = append(lits, y)
								case *ast.IndexExpr:
									if l, ok := y.Index.(*ast.BasicLit); ok {
										lits = append(lits, l)
									}
								}
							}
						case *ast.ValueSpec:
							for _, e := range x.Values {
								if l, ok := e.(*ast.BasicLit); ok {
									lits = append(lits, l)
								}
							}
						}
						for _, lit := range lits {
							if lit.Kind == token.STRING {
								if v, err := strconv.Unquote(lit.Value); err == nil && stamps[v] {
									bad = append(bad, fset.Position(lit.Pos()).String()+" "+fd.Name.Name+" reads "+v)
								}
							}
						}
						return true
					})
				}
			}
		}
	}
	require.Empty(t, bad, "a work stamp read outside WorkDeadline, a second clock:\n%s", strings.Join(bad, "\n"))
}
