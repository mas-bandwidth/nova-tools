package ci

import (
	"go/ast"
	"go/token"
	"testing"
)

// tree.go is the shared repository tree the class tests read: ONE walk and ONE
// parse per test process, filtered in memory by each rule.
//
// STUB. The contract is pinned by tree_contract_test.go; the loader is not
// written yet.

// treeFile is one file of the repository as the class tests see it.
type treeFile struct {
	// Path is the absolute path on this machine.
	Path string
	// Rel is the repo-relative, slash-separated path every finding prints.
	Rel string
	// Go is true for a .go file, Test for a _test.go file.
	Go   bool
	Test bool
	// Src is the file's bytes, kept for the rules that read the tree as text.
	Src []byte
	// AST is the parsed file, and ParseErr what go/parser said if it refused.
	AST      *ast.File
	ParseErr error
}

// repoTreeIndex is the whole tree, walked once.
type repoTreeIndex struct {
	Root  string
	FSet  *token.FileSet
	Files []*treeFile
}

// repoTree returns the shared tree, loading it on the first call.
func repoTree(t *testing.T) *repoTreeIndex {
	t.Helper()
	return &repoTreeIndex{Root: repoRoot(t)}
}

// repoTreeLoads is how many times the loader has run in this process.
func repoTreeLoads() int { return 0 }
