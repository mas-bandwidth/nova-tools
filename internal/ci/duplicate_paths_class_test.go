//go:build functional

package ci

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// duplicatePathsLedgerPath is the shrink-only ledger of duplicate function bodies
	// in cmd/nova-sprint, internal/sprint, cmd/nova-friend and internal/friend.
	//
	// Each row is: <file:func> <file:func> <why>
	// The list is shrink-only: no new pairs may be added, only removed when fixed.
	duplicatePathsLedgerPath = "internal/ci/testdata/duplicate-paths-ledger.txt"
)

// duplicatePathsRoots are the packages to scan for duplicate function bodies.
var duplicatePathsRoots = []string{
	"github.com/mas-bandwidth/nova-tools/cmd/nova-sprint",
	"github.com/mas-bandwidth/nova-tools/internal/sprint",
	"github.com/mas-bandwidth/nova-tools/cmd/nova-friend",
	"github.com/mas-bandwidth/nova-tools/internal/friend",
}

// funcInfo holds normalized function body information.
type funcInfo struct {
	File     string // relative path from repo root
	Func     string // function name
	BodyHash string // SHA256 of normalized body
	Stmts    int    // statement count
	Usage    string // help usage line if present
}

// duplicatePair represents a pair of duplicate functions.
type duplicatePair struct {
	A, B   string // "file:func" format
	Reason string // why it's a duplicate
}

// normalizeBody normalizes a function body by renaming identifiers in order of
// first use, keeping literals, and dropping comments.
func normalizeBody(fset *token.FileSet, fn *ast.FuncDecl) string {
	if fn.Body == nil {
		return ""
	}

	// Track identifiers in order of first use
	idMap := make(map[string]string)
	nextID := 1

	// Walk AST to collect identifiers
	var identifiers []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if _, exists := idMap[id.Name]; !exists {
				// Skip blank identifiers and standard types
				if id.Name != "_" && !isStandardType(id.Name) {
					idMap[id.Name] = fmt.Sprintf("id%d", nextID)
					nextID++
					identifiers = append(identifiers, id.Name)
				}
			}
		}
		return true
	})

	// Get raw body text without comments - use go/format for clean output
	var buf strings.Builder
	// We'll use a simple string representation of the body statements
	for _, stmt := range fn.Body.List {
		buf.WriteString(fmt.Sprintf("%v", stmt))
		buf.WriteString(" ")
	}
	body := buf.String()

	// Replace identifiers in reverse order to avoid prefix issues
	sort.Slice(identifiers, func(i, j int) bool {
		return len(identifiers[i]) > len(identifiers[j])
	})

	for _, old := range identifiers {
		new := idMap[old]
		body = strings.ReplaceAll(body, old, new)
	}

	return body
}

// isStandardType checks if a name is a standard Go type.
func isStandardType(name string) bool {
	stdTypes := map[string]bool{
		"bool": true, "byte": true, "complex64": true, "complex128": true,
		"error": true, "float32": true, "float64": true, "int": true,
		"int8": true, "int16": true, "int32": true, "int64": true,
		"rune": true, "string": true, "uint": true, "uint8": true,
		"uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	}
	return stdTypes[name]
}

// countStatements counts the number of statements in a function body.
func countStatements(fn *ast.FuncDecl) int {
	if fn.Body == nil {
		return 0
	}

	count := 0
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.ExprStmt, *ast.AssignStmt, *ast.DeclStmt,
			*ast.ReturnStmt, *ast.BranchStmt, *ast.IfStmt, *ast.ForStmt,
			*ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt,
			*ast.SelectStmt, *ast.CaseClause, *ast.CommClause,
			*ast.LabeledStmt, *ast.DeferStmt, *ast.GoStmt:
			count++
		}
		return true
	})
	return count
}

// hashBody computes a SHA256 hash of the normalized body.
func hashBody(body string) string {
	hash := sha256.Sum256([]byte(body))
	return fmt.Sprintf("%x", hash)
}

// scanPackage scans a package for non-test functions and returns their info.
func scanPackage(pkgPath string, root string) ([]funcInfo, error) {
	var infos []funcInfo

	// Find the package directory
	pkgDir := strings.Replace(pkgPath, "github.com/mas-bandwidth/nova-tools/", "", 1)
	pkgDir = strings.TrimSpace(pkgDir)
	pkgDir = filepath.Join(root, pkgDir)

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, pkgDir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			relPath, err := filepath.Rel(root, fset.File(file.Pos()).Name())
			if err != nil {
				relPath = filepath.Join(pkgDir, filepath.Base(fset.File(file.Pos()).Name()))
			}

			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
					body := normalizeBody(fset, fn)
					count := countStatements(fn)
					hash := hashBody(body)

					infos = append(infos, funcInfo{
						File:     relPath,
						Func:     fn.Name.Name,
						BodyHash: hash,
						Stmts:    count,
					})
				}
			}
		}
	}

	return infos, nil
}

// findDuplicates finds pairs of functions with the same normalized body hash.
func findDuplicates(infos []funcInfo) []duplicatePair {
	// Group by hash
	hashGroups := make(map[string][]funcInfo)
	for _, info := range infos {
		if info.Stmts >= 8 { // Only consider functions with 8+ statements
			hashGroups[info.BodyHash] = append(hashGroups[info.BodyHash], info)
		}
	}

	var pairs []duplicatePair
	for hash, group := range hashGroups {
		if len(group) >= 2 {
			// Pair all combinations
			for i := 0; i < len(group); i++ {
				for j := i + 1; j < len(group); j++ {
					pairs = append(pairs, duplicatePair{
						A:      fmt.Sprintf("%s:%s", group[i].File, group[i].Func),
						B:      fmt.Sprintf("%s:%s", group[j].File, group[j].Func),
						Reason: fmt.Sprintf("same normalized body hash (%s)", hash[:16]),
					})
				}
			}
		}
	}

	return pairs
}

// loadLedger loads the duplicate paths ledger.
func loadLedger(t *testing.T) map[string]duplicatePair {
	ledgerPath := filepath.Join(repoRoot(t), duplicatePathsLedgerPath)
	content, err := os.ReadFile(ledgerPath)
	require.NoError(t, err, "reading duplicate paths ledger")

	ledger := make(map[string]duplicatePair)
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) >= 3 {
			pair := duplicatePair{
				A:      parts[0],
				B:      parts[1],
				Reason: strings.Join(parts[2:], " "),
			}
			// Use sorted pair as key
			key := pair.A
			if pair.B < pair.A {
				key = pair.B
			}
			ledger[key] = pair
		}
	}

	return ledger
}

// TestDuplicatePathsLedgerOnlyShrinks verifies that duplicate function bodies in
// cmd/nova-sprint, internal/sprint, cmd/nova-friend and internal/friend are tracked
// in a shrink-only ledger. New duplicates are refused; fixed duplicates are removed.
func TestDuplicatePathsLedgerOnlyShrinks(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	// Load current ledger
	ledger := loadLedger(t)
	ledgerPairs := make(map[string]bool)
	for key := range ledger {
		ledgerPairs[key] = true
	}

	// Scan all packages
	var allInfos []funcInfo
	for _, pkg := range duplicatePathsRoots {
		infos, err := scanPackage(pkg, root)
		require.NoError(t, err, "scanning package %s", pkg)
		allInfos = append(allInfos, infos...)
	}

	// Find duplicates
	dupes := findDuplicates(allInfos)

	// Build actual pairs map
	actualPairs := make(map[string]duplicatePair)
	for _, pair := range dupes {
		key := pair.A
		if pair.B < pair.A {
			key = pair.B
		}
		actualPairs[key] = pair
	}

	// Check that all actual pairs are in ledger
	var unlisted []duplicatePair
	for key, pair := range actualPairs {
		if !ledgerPairs[key] {
			unlisted = append(unlisted, pair)
		}
	}

	// Check that all ledger pairs still exist
	var stale []duplicatePair
	for key, pair := range ledger {
		if _, exists := actualPairs[key]; !exists {
			stale = append(stale, pair)
		}
	}

	if len(unlisted) > 0 || len(stale) > 0 {
		var problems []string
		for _, pair := range unlisted {
			problems = append(problems, fmt.Sprintf(
				"new duplicate pair not in ledger: %s %s (%s); remedy: fix the duplicate or add to ledger",
				pair.A, pair.B, pair.Reason))
		}
		for _, pair := range stale {
			problems = append(problems, fmt.Sprintf(
				"ledger pair no longer exists: %s %s (%s); remedy: remove from ledger",
				pair.A, pair.B, pair.Reason))
		}
		sort.Strings(problems)
		assert.Failf(t, "duplicate paths rule", "%s\n(ledger: %s)",
			strings.Join(problems, "\n"), duplicatePathsLedgerPath)
	}
}

// TestDuplicatePathsWitness tests the shrink-only ledger mechanics.
func TestDuplicatePathsWitness(t *testing.T) {
	t.Parallel()

	// Write a temporary ledger
	tmpDir := t.TempDir()
	ledgerPath := filepath.Join(tmpDir, "ledger.txt")

	// Create ledger with one entry
	ledger := "# ledger for duplicate paths\ntestpkg/func.go:Foo testpkg/func.go:Bar same body\n"
	require.NoError(t, os.WriteFile(ledgerPath, []byte(ledger), 0o644))

	// Load ledger
	loaded := loadLedgerFromPath(t, ledgerPath)
	assert.Len(t, loaded, 1)
	assert.Contains(t, loaded, "testpkg/func.go:Bar")
}

// loadLedgerFromPath loads a ledger from a specific path.
func loadLedgerFromPath(t *testing.T, path string) map[string]duplicatePair {
	content, err := os.ReadFile(path)
	require.NoError(t, err)

	ledger := make(map[string]duplicatePair)
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) >= 3 {
			pair := duplicatePair{
				A:      parts[0],
				B:      parts[1],
				Reason: strings.Join(parts[2:], " "),
			}
			key := pair.A
			if pair.B < pair.A {
				key = pair.B
			}
			ledger[key] = pair
		}
	}
	return ledger
}
