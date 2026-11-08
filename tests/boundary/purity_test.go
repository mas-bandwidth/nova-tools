package boundary

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// impure are the calls a unit test may not make: real time, sockets, the network and other
// programs. A test that needs one is a functional test, in a file built with the functional (or
// slow) tag, which CI runs in its own job (docs/TESTING.md).
var impure = map[string]map[string]bool{
	"time": {"Sleep": true, "After": true, "Tick": true, "NewTimer": true, "NewTicker": true, "AfterFunc": true},
	"net":  {"Listen": true, "ListenPacket": true, "Dial": true, "DialTimeout": true, "DialTCP": true, "DialUDP": true, "ListenTCP": true, "ListenUDP": true},
	"http": {"Get": true, "Post": true, "Head": true, "ListenAndServe": true},
	"exec": {"Command": true, "CommandContext": true, "LookPath": true},
	"os":   {"StartProcess": true},
}

// TestUnitTestsArePure fails on a unit-tier test file (no functional or slow build tag) that
// calls real time, a socket, the network or another program.
func TestUnitTestsArePure(t *testing.T) {
	base := root(t)
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "docs" || n == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		if tagged(f) {
			return nil
		}
		names := map[string]string{} // local name -> import path's last element
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			local := p[strings.LastIndex(p, "/")+1:]
			if imp.Name != nil {
				local = imp.Name.Name
			}
			names[local] = p[strings.LastIndex(p, "/")+1:]
		}
		rel, _ := filepath.Rel(base, path) // ignored: path is under base
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if impure[names[pkg.Name]][sel.Sel.Name] {
				t.Errorf("%s:%d: %s.%s in a unit test: real time, sockets, the network and other programs belong in a functional test (a file with //go:build functional)",
					rel, fset.Position(sel.Pos()).Line, pkg.Name, sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// tagged reports whether the file's build constraint names functional or slow.
func tagged(f *ast.File) bool {
	for _, g := range f.Comments {
		if g.Pos() > f.Package {
			break
		}
		for _, c := range g.List {
			if strings.HasPrefix(c.Text, "//go:build ") && (strings.Contains(c.Text, "functional") || strings.Contains(c.Text, "slow")) {
				return true
			}
		}
	}
	return false
}
