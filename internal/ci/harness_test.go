package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ciHarness encapsulates repository tree access, AST parsing, allowlist/ledger
// verification, and fixture assertions for internal/ci tests.
type ciHarness struct {
	t    *testing.T
	tree *repoTreeIndex
}

// newCIHarness returns a new test harness bound to t.
func newCIHarness(t *testing.T) *ciHarness {
	t.Helper()
	return &ciHarness{t: t}
}

// repoTree returns the shared repository tree, loading it once per test process.
func (h *ciHarness) repoTree() *repoTreeIndex {
	h.t.Helper()
	if h.tree == nil {
		h.tree = repoTree(h.t)
	}
	return h.tree
}

// newSiteLedger loads a package-sharded counted ledger for the class test.
func (h *ciHarness) newSiteLedger(path string) *siteLedger {
	h.t.Helper()
	return newSiteLedger(h.t, path)
}

// checkLedger asserts that measured sites satisfy the ledger and reports any violations.
func (h *ciHarness) checkLedger(l *siteLedger, remedy string) {
	h.t.Helper()
	violations := l.violations(h.t, remedy)
	for _, v := range violations {
		assert.Fail(h.t, v)
	}
}

// checkAllowlist checks seen findings against allow, verifies stale entries,
// and asserts that no violations exist.
func (h *ciHarness) checkAllowlist(allow *allowlist.List, allowPath string, seen map[string]bool, itemDesc string, unlisted []string) {
	h.t.Helper()
	var violations []string
	violations = append(violations, unlisted...)
	for _, row := range allowlist.Check(h.t, allow, seen).Stale {
		key := row.Key
		violations = append(violations, fmt.Sprintf(
			"%s lists %s, but no %s is there any more; delete the stale entry (the list only shrinks)",
			allowPath, key, itemDesc))
	}
	sort.Strings(violations)
	for _, v := range violations {
		assert.Fail(h.t, v)
	}
}

// parseSource parses Go source text into an AST using a fresh FileSet.
func (h *ciHarness) parseSource(name, src string) (*token.FileSet, *ast.File) {
	h.t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	require.NoError(h.t, err)
	return fset, f
}
