package ci

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
)

// The checkers in this package sweep a source tree the caller names: they walk
// it, read the files they select and parse the Go ones. They do it through these
// three, which are the standard library's. This package's own tests run the
// checkers over this repository side by side, and there they are answered from
// the tests' one shared read of the tree (tree_test.go), so six checkers open,
// list and parse each file once between them rather than once each
// (nova-tools#4328, Glenn 2026-09-26: unit tests under 2 s, frugal with cores).
// A root outside that tree -- every fixture a test builds -- takes the disk.
var (
	// walkSourceDir is filepath.WalkDir.
	walkSourceDir = filepath.WalkDir
	// readSourceFile is os.ReadFile.
	readSourceFile = os.ReadFile
	// parseSource parses one file into a FileSet of its own and returns both,
	// so every position the caller reads resolves in the set its file is in.
	parseSource = parseSourceFile
)

// parseSourceFile is parseSource's production answer: a fresh FileSet and one
// parser.ParseFile.
func parseSourceFile(name string, src []byte, mode parser.Mode) (*token.FileSet, *ast.File, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, mode)
	return fset, file, err
}
