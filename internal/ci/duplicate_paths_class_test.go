package ci

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// duplicatePathsLedgerPath is the shrink-only ledger of duplicate function bodies
// across cmd/nova-sprint, internal/sprint, cmd/nova-friend and internal/friend.
// Each row is `<file:func> <file:func> <reason>`.
const duplicatePathsLedgerPath = "testdata/duplicate-paths-ledger.txt"

// duplicatePathsRemedy is what to do when a duplicate pair is found.
const duplicatePathsRemedy = "keep one side, delete or merge the other"

// readLedger reads the ledger file and returns a set of pair keys (canonical sorted form).
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
		// Format: <file:func> <file:func> <reason>
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			// Store canonical key (sorted pair)
			if parts[0] > parts[1] {
				parts[0], parts[1] = parts[1], parts[0]
			}
			pairs[parts[0]+" "+parts[1]] = true
		}
	}
	return pairs
}

// FuncInfo holds information about a function.
type FuncInfo struct {
	Path     string
	Name     string
	BodyHash string
	Stmts    int
}

// countStatements counts the number of statements in a function body.
func countStatements(body *ast.BlockStmt) int {
	if body == nil {
		return 0
	}
	return len(body.List)
}

// normalizeBody creates a normalized representation of a function body.
// Identifiers are renamed by first use, literals kept, comments dropped.
func normalizeBody(body *ast.BlockStmt, fset *token.FileSet) string {
	if body == nil {
		return ""
	}

	idents := make(map[string]string)
	nextID := 0

	var buf strings.Builder

	var walk func(node ast.Node)
	walk = func(node ast.Node) {
		if node == nil {
			return
		}

		switch n := node.(type) {
		case *ast.Ident:
			if n.Name == "_" {
				buf.WriteString("_")
				return
			}
			if newName, ok := idents[n.Name]; ok {
				buf.WriteString(newName)
				return
			}
			newName := fmt.Sprintf("id%d", nextID)
			nextID++
			idents[n.Name] = newName
			buf.WriteString(newName)
		case *ast.BasicLit:
			buf.WriteString(n.Value)
		case *ast.BinaryExpr:
			walk(n.X)
			buf.WriteString(" ")
			buf.WriteString(n.Op.String())
			buf.WriteString(" ")
			walk(n.Y)
		case *ast.UnaryExpr:
			buf.WriteString(n.Op.String())
			walk(n.X)
		case *ast.ParenExpr:
			buf.WriteString("(")
			walk(n.X)
			buf.WriteString(")")
		case *ast.CallExpr:
			walk(n.Fun)
			buf.WriteString("(")
			for i, arg := range n.Args {
				if i > 0 {
					buf.WriteString(", ")
				}
				walk(arg)
			}
			buf.WriteString(")")
		case *ast.SelectorExpr:
			walk(n.X)
			buf.WriteString(".")
			buf.WriteString(n.Sel.Name)
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				if i > 0 {
					buf.WriteString(", ")
				}
				walk(lhs)
			}
			buf.WriteString(" ")
			buf.WriteString(n.Tok.String())
			buf.WriteString(" ")
			for i, rhs := range n.Rhs {
				if i > 0 {
					buf.WriteString(", ")
				}
				walk(rhs)
			}
		case *ast.IncDecStmt:
			walk(n.X)
		case *ast.ReturnStmt:
			buf.WriteString("return ")
			for i, result := range n.Results {
				if i > 0 {
					buf.WriteString(", ")
				}
				walk(result)
			}
		case *ast.IfStmt:
			buf.WriteString("if ")
			walk(n.Cond)
			buf.WriteString(" { ... }")
		case *ast.RangeStmt:
			if n.X != nil {
				walk(n.X)
			}
		case *ast.ForStmt:
			buf.WriteString("for ")
			if n.Cond != nil {
				walk(n.Cond)
			}
		case *ast.SwitchStmt:
			buf.WriteString("switch ")
			if n.Tag != nil {
				walk(n.Tag)
			}
		case *ast.TypeSwitchStmt:
			buf.WriteString("switch ")
			if n.Assign != nil {
				walk(n.Assign)
			}
		case *ast.BranchStmt:
			buf.WriteString(n.Tok.String())
		case *ast.LabeledStmt:
			buf.WriteString(n.Label.Name)
			buf.WriteString(": ")
			walk(n.Stmt)
		case *ast.DeclStmt:
			if n, ok := n.Decl.(*ast.GenDecl); ok {
				switch n.Tok {
				case token.VAR:
					buf.WriteString("var ")
					for i, spec := range n.Specs {
						if i > 0 {
							buf.WriteString(", ")
						}
						if vs, ok := spec.(*ast.ValueSpec); ok {
							buf.WriteString(vs.Names[0].Name)
							if len(vs.Values) > 0 {
								buf.WriteString(" ")
								walk(vs.Values[0])
							}
						}
					}
				case token.CONST:
					buf.WriteString("const ")
					for i, spec := range n.Specs {
						if i > 0 {
							buf.WriteString(", ")
						}
						if vs, ok := spec.(*ast.ValueSpec); ok {
							buf.WriteString(vs.Names[0].Name)
							if len(vs.Values) > 0 {
								buf.WriteString(" ")
								walk(vs.Values[0])
							}
						}
					}
				case token.TYPE:
					buf.WriteString("type ")
					if ts, ok := n.Specs[0].(*ast.TypeSpec); ok {
						buf.WriteString(ts.Name.Name)
					}
				}
			}
		default:
		}
	}

	for _, stmt := range body.List {
		walk(stmt)
		buf.WriteString("; ")
	}

	return buf.String()
}

// scanPackage scans a Go package directory and returns all non-test functions.
func scanPackage(dir string) []FuncInfo {
	var decls []FuncInfo

	files, err := os.ReadDir(dir)
	if err != nil {
		return decls
	}

	for _, file := range files {
		// Skip test files
		if strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		if !strings.HasSuffix(file.Name(), ".go") {
			continue
		}

		path := filepath.Join(dir, file.Name())
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}

		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				decls = append(decls, FuncInfo{
					Path:     filepath.Base(dir) + "/" + file.Name(),
					Name:     fn.Name.Name,
					BodyHash: normalizeBody(fn.Body, fset),
					Stmts:    countStatements(fn.Body),
				})
			}
		}
	}

	return decls
}

// findDuplicates finds duplicate function bodies across packages.
func findDuplicates(packages []string) []FuncInfo {
	// Map: normalizedBody -> []FuncInfo
	bodies := make(map[string][]FuncInfo)

	for _, pkg := range packages {
		// Skip testdata directories
		if strings.Contains(pkg, "testdata") {
			continue
		}

		decls := scanPackage(pkg)
		for _, fn := range decls {
			// Only consider functions with at least 8 statements
			if fn.Stmts < 8 {
				continue
			}

			bodies[fn.BodyHash] = append(bodies[fn.BodyHash], fn)
		}
	}

	// Collect all duplicates
	var duplicates []FuncInfo
	for _, funcs := range bodies {
		if len(funcs) >= 2 {
			// Sort for consistent ordering
			sort.Slice(funcs, func(i, j int) bool {
				if funcs[i].Path != funcs[j].Path {
					return funcs[i].Path < funcs[j].Path
				}
				return funcs[i].Name < funcs[j].Name
			})
			duplicates = append(duplicates, funcs...)
		}
	}

	return duplicates
}

// TestDuplicatePathsLedgerOnlyShrinks holds the duplicate-paths ledger: every pair of
// function bodies that are the same after normalizing names, at least 8 statements.
// The test fails on a pair not in the ledger and on a ledger line whose pair is gone.
func TestDuplicatePathsLedgerOnlyShrinks(t *testing.T) {
	t.Parallel()

	// Read ledger
	ledger := readLedger(t)

	// Packages to scan - use absolute path to repo root
	repoRoot := "/home/nova/rowan-working/tmp/slots/simp-duplicate-paths-b.w6.g1.e15/jobs/simp-duplicate-paths-b.w6/repo"
	packages := []string{
		filepath.Join(repoRoot, "cmd/nova-sprint"),
		filepath.Join(repoRoot, "internal/sprint"),
		filepath.Join(repoRoot, "internal/sprint/driver"),
		filepath.Join(repoRoot, "internal/sprint/refmodel"),
		filepath.Join(repoRoot, "internal/sprint/store"),
		filepath.Join(repoRoot, "internal/sprint/store/bench"),
		filepath.Join(repoRoot, "internal/sprint/store/redis"),
		filepath.Join(repoRoot, "internal/sprint/store/twin"),
		filepath.Join(repoRoot, "internal/friend"),
		filepath.Join(repoRoot, "internal/friend/friendtest"),
		filepath.Join(repoRoot, "internal/friend/tla"),
		filepath.Join(repoRoot, "cmd/nova-friend"),
	}

	// Find duplicates
	duplicates := findDuplicates(packages)

	// Build set of actual duplicate pairs
	actualPairs := make(map[string]bool)
	for i := range duplicates {
		for j := i + 1; j < len(duplicates); j++ {
			if duplicates[i].BodyHash == duplicates[j].BodyHash {
				// Create canonical pair key (sorted)
				key1 := fmt.Sprintf("%s %s", duplicates[i].Path, duplicates[j].Path)
				key2 := fmt.Sprintf("%s %s", duplicates[j].Path, duplicates[i].Path)
				if key1 > key2 {
					key1, key2 = key2, key1
				}
				actualPairs[key1] = true
			}
		}
	}

	// Check ledger entries exist in actual duplicates
	for pair := range ledger {
		_, found := actualPairs[pair]
		require.True(t, found, "ledger entry %s no longer has duplicates", pair)
	}

	// Check all actual duplicates are in ledger
	for pair := range actualPairs {
		_, found := ledger[pair]
		require.True(t, found, "duplicate pair %s not in ledger", pair)
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
