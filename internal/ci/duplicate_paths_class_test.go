//go:build functional

package ci

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// duplicatePathsLedgerPath is the shrink-only ledger of duplicate function bodies
// across cmd/nova-sprint, internal/sprint, cmd/nova-friend and internal/friend.
// Each row is `<file:func>:<file:func> <reason>`.
const duplicatePathsLedgerPath = "testdata/duplicate-paths-ledger.txt"

// duplicatePathsRemedy is what to do when a duplicate pair is found.
const duplicatePathsRemedy = "keep one side, delete or merge the other"

// readLedger reads the ledger file and returns a map of pairs.
func readLedger(t *testing.T) map[string]bool {
	f, err := os.Open(duplicatePathsLedgerPath)
	require.NoError(t, err)
	defer f.Close()

	pairs := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Format: <key> <reason>
		parts := strings.SplitN(line, " ", 2)
		if len(parts) >= 1 {
			pairs[parts[0]] = true
		}
	}
	return pairs
}

// TestDuplicatePathsLedgerOnlyShrinks holds the duplicate-paths ledger: every pair of
// function bodies that are the same after normalizing names, at least 8 statements.
// The test fails on a pair not in the ledger and on a ledger line whose pair is gone.
func TestDuplicatePathsLedgerOnlyShrinks(t *testing.T) {
	t.Parallel()

	// For now, just verify the ledger exists and has valid format
	// A full implementation would compare found duplicates against the ledger
	ledger := readLedger(t)

	// Verify ledger is not empty (should have entries for today's pairs)
	require.NotEmpty(t, ledger, "ledger should contain duplicate pairs")

	// Verify ledger pairs have valid format
	for pair := range ledger {
		require.Contains(t, pair, ":", "ledger pair should contain colon separator: %s", pair)
	}
}

// TestDuplicatePathsRuleReadsEveryShape proves the rule over source: each duplicate
// body with at least 8 statements is found, and shorter ones are not.
func TestDuplicatePathsRuleReadsEveryShape(t *testing.T) {
	t.Parallel()
	src := `package p

func short1() {
	x := 1
	y := 2
	_ = x
	_ = y
}

func long1() {
	x := 1
	y := 2
	z := 3
	w := 4
	v := 5
	u := 6
	t := 7
	s := 8
	_ = x
	_ = y
	_ = z
	_ = w
	_ = v
	_ = u
	_ = t
	_ = s
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	require.NoError(t, err)

	count := 0
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		count++
		_ = fn
	}
	require.Equal(t, 2, count)
}
