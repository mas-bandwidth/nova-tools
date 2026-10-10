package ci

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: ONE PATH, ONE PLACE (nova-sprint v1.0.0, lens simplicity).
//
// Two ways of doing one thing are two places to get it wrong. This rule reads
// every non-test function body under cmd/nova-sprint, internal/sprint/...,
// cmd/nova-friend and internal/friend with go/ast (the standard library only),
// normalises each body -- identifiers renamed in order of first use, literals
// kept, comments dropped -- and hashes it. A body of at least eight statements
// whose hash occurs twice is a duplicate pair. It also flags two verb
// registrations whose help usage line is the same line under two different
// names: an alias left behind.
//
// The ledger internal/ci/testdata/duplicate-paths-ledger.txt is shrink-only, in
// the style of the dead-code and flag-usage ledgers: one row per pair,
// `<file:func> <file:func> <why>`. The test fails on a pair not in the ledger
// and on a row whose pair is gone, so the list can only shrink. docs/STOPGAPS.md
// carries the deletion card that removes one side of each pair.

// duplicatePathsLedgerRel is the ledger as the repository root holds it. Every
// finding prints duplicatePathsLedgerPath, the same file relative to this test.
const (
	duplicatePathsLedgerRel  = "internal/ci/testdata/duplicate-paths-ledger.txt"
	duplicatePathsLedgerPath = "testdata/duplicate-paths-ledger.txt"
)

// duplicatePathsMinStatements is the size below which two bodies are not held:
// a two-line accessor that reads the same shape is a table row, not a defect.
const duplicatePathsMinStatements = 8

// duplicatePathsRemedy is what the author of a red pair does.
const duplicatePathsRemedy = "one path written twice: fold one side into the other and drop the ledger row " +
	"(the list only shrinks; the deletion card is in docs/STOPGAPS.md, \"duplicate paths\")"

// duplicatePathsRoots are the packages the rule reads, recursively.
var duplicatePathsRoots = []string{"cmd/nova-sprint", "internal/sprint", "cmd/nova-friend", "internal/friend"}

// duplicatePath is one pair of duplicate keys, each an endpoint `<file>:<name>`.
type duplicatePath struct {
	a, b string
}

// key is the ledger's key for the pair: the two endpoints sorted, so the two
// orders a reader may write are one row.
func (p duplicatePath) key() string {
	if p.a > p.b {
		return p.b + " " + p.a
	}
	return p.a + " " + p.b
}

// duplicatePathSource is one parsed non-test .go file under a root.
type duplicatePathSource struct {
	rel  string
	fset *token.FileSet
	file *ast.File
}

// duplicatePathBody is one function body under its key, with its statement count.
type duplicatePathBody struct {
	key   string
	stmts int
}

// duplicatePathVerb is one verb registration: its name, the file that declares
// it, and the help usage line its synopsis produces.
type duplicatePathVerb struct {
	name  string
	file  string
	usage string
}

// loadDuplicatePathSources walks the rule's roots under root and parses every
// non-test .go file (fixtures under testdata aside), in path order. root is the
// repository or a fixture; a package the root does not hold is skipped, so the
// witness below can plant one package alone.
func loadDuplicatePathSources(t *testing.T, root string) []duplicatePathSource {
	t.Helper()
	var out []duplicatePathSource
	for _, dir := range duplicatePathsRoots {
		base := filepath.Join(root, filepath.FromSlash(dir))
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == ".git" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, rel, raw, 0)
			if err != nil {
				return fmt.Errorf("parsing %s: %w", rel, err)
			}
			out = append(out, duplicatePathSource{rel: rel, fset: fset, file: f})
			return nil
		})
		require.NoError(t, err, "walking %s under %s", dir, root)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out
}

// duplicatePathTypeName is a receiver type's name, unwrapping a pointer or type
// parameters, so a method key reads `<recv>.<name>`.
func duplicatePathTypeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return duplicatePathTypeName(x.X)
	case *ast.IndexExpr:
		return duplicatePathTypeName(x.X)
	case *ast.IndexListExpr:
		return duplicatePathTypeName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return "?"
}

// duplicatePathFuncKey is the function's ledger key: file plus name, a method
// under `<recv>.<name>`.
func duplicatePathFuncKey(rel string, fd *ast.FuncDecl) string {
	name := fd.Name.Name
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		name = duplicatePathTypeName(fd.Recv.List[0].Type) + "." + name
	}
	return rel + ":" + name
}

// duplicatePathStatementCount is every statement node in the body, nested
// blocks included: the measure the eight-statement floor is read on.
func duplicatePathStatementCount(body *ast.BlockStmt) int {
	n := 0
	ast.Inspect(body, func(node ast.Node) bool {
		if _, ok := node.(ast.Stmt); ok {
			n++
		}
		return true
	})
	return n
}

// duplicatePathCanonical is the body's normalised shape: a token stream of node
// types, with every identifier renamed in order of first use and every literal
// kept as written. A selector's field or method name is not a local identifier,
// so it is kept: `x.Foo()` and `y.Bar()` are different calls, not one path. The
// AST is never mutated, so the shared tree stays read-only. Comments are already
// gone (the files are parsed with mode 0).
func duplicatePathCanonical(body *ast.BlockStmt) string {
	selectors := map[*ast.Ident]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			selectors[sel.Sel] = true
		}
		return true
	})
	names := map[string]string{}
	next := 0
	var b strings.Builder
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		switch x := n.(type) {
		case *ast.Ident:
			name := x.Name
			if name != "_" && !selectors[x] {
				if canon, ok := names[name]; ok {
					name = canon
				} else {
					name = fmt.Sprintf("v%d", next)
					next++
					names[x.Name] = name
				}
			}
			fmt.Fprintf(&b, "id(%s)", name)
		case *ast.BasicLit:
			fmt.Fprintf(&b, "lit(%s,%s)", x.Kind, x.Value)
		default:
			fmt.Fprintf(&b, "%T", n)
		}
		b.WriteByte(';')
		return true
	})
	return b.String()
}

// duplicatePathBodyPairs finds every pair of non-test function bodies under root
// that are the same normalised body and each at least duplicatePathsMinStatements
// statements long.
func duplicatePathBodyPairs(t *testing.T, root string) []duplicatePath {
	t.Helper()
	groups := map[string][]duplicatePathBody{}
	for _, src := range loadDuplicatePathSources(t, root) {
		for _, decl := range src.file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			sum := sha256.Sum256([]byte(duplicatePathCanonical(fd.Body)))
			hash := hex.EncodeToString(sum[:])
			groups[hash] = append(groups[hash], duplicatePathBody{
				key:   duplicatePathFuncKey(src.rel, fd),
				stmts: duplicatePathStatementCount(fd.Body),
			})
		}
	}
	var out []duplicatePath
	for _, g := range groups {
		var kept []duplicatePathBody
		for _, b := range g {
			if b.stmts >= duplicatePathsMinStatements {
				kept = append(kept, b)
			}
		}
		sort.Slice(kept, func(i, j int) bool { return kept[i].key < kept[j].key })
		for i := 0; i < len(kept); i++ {
			for j := i + 1; j < len(kept); j++ {
				out = append(out, duplicatePath{a: kept[i].key, b: kept[j].key})
			}
		}
	}
	return out
}

// duplicatePathString reads a string literal expression, and false for anything
// built at run time.
func duplicatePathString(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// duplicatePathVerbFields reads one verb registration's fields: Name and Usage
// for a tool.Verb, the positional name and syntax for nova-sprint's verb.
func duplicatePathVerbFields(file string, fields []ast.Expr) (duplicatePathVerb, bool) {
	var name, usage string
	named := false
	for _, e := range fields {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		s, ok := duplicatePathString(kv.Value)
		if !ok {
			continue
		}
		switch key.Name {
		case "Name":
			name, named = s, true
		case "Usage":
			usage = s
		}
	}
	if named {
		if name == "" || usage == "" {
			return duplicatePathVerb{}, false
		}
		return duplicatePathVerb{name: name, file: file, usage: usage}, true
	}
	// nova-sprint's verb{name, syntax, example, run}: positional.
	if len(fields) < 2 {
		return duplicatePathVerb{}, false
	}
	n, ok := duplicatePathString(fields[0])
	if !ok || n == "" {
		return duplicatePathVerb{}, false
	}
	syn, _ := duplicatePathString(fields[1])
	line := "nova-sprint " + n
	if syn != "" {
		line += " " + syn
	}
	return duplicatePathVerb{name: n, file: file, usage: line}, true
}

// duplicatePathVerbPairs finds two verb registrations whose help usage line is
// the same line under two different names: an alias left behind. It reads each
// `tool.Verb{...}` literal (also as an elided `[]tool.Verb{...}` element) and
// each nova-sprint `verb{...}` literal.
func duplicatePathVerbPairs(t *testing.T, root string) []duplicatePath {
	t.Helper()
	byUsage := map[string][]duplicatePathVerb{}
	add := func(v duplicatePathVerb) {
		byUsage[v.usage] = append(byUsage[v.usage], v)
	}
	for _, src := range loadDuplicatePathSources(t, root) {
		ast.Inspect(src.file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			switch typ := lit.Type.(type) {
			case *ast.SelectorExpr:
				if typ.Sel.Name != "Verb" || exprText(typ.X) != "tool" {
					return true
				}
				if v, ok := duplicatePathVerbFields(src.rel, lit.Elts); ok {
					add(v)
				}
			case *ast.ArrayType:
				switch elt := typ.Elt.(type) {
				case *ast.SelectorExpr:
					if elt.Sel.Name != "Verb" || exprText(elt.X) != "tool" {
						return true
					}
					for _, e := range lit.Elts {
						if cl, ok := e.(*ast.CompositeLit); ok {
							if v, ok := duplicatePathVerbFields(src.rel, cl.Elts); ok {
								add(v)
							}
						}
					}
				case *ast.Ident:
					if elt.Name != "verb" {
						return true
					}
					for _, e := range lit.Elts {
						if cl, ok := e.(*ast.CompositeLit); ok {
							if v, ok := duplicatePathVerbFields(src.rel, cl.Elts); ok {
								add(v)
							}
						}
					}
				}
			}
			return true
		})
	}
	var out []duplicatePath
	for _, vs := range byUsage {
		names := map[string]bool{}
		for _, v := range vs {
			names[v.name] = true
		}
		if len(names) < 2 {
			continue
		}
		for i := 0; i < len(vs); i++ {
			for j := i + 1; j < len(vs); j++ {
				if vs[i].name == vs[j].name {
					continue
				}
				out = append(out, duplicatePath{
					a: duplicatePathVerbKey(vs[i]),
					b: duplicatePathVerbKey(vs[j]),
				})
			}
		}
	}
	return out
}

// duplicatePathVerbKey is a verb registration's ledger key. A verb name with a
// space (nova-sprint's `<group> <verb>`) is written with a dot so the key stays
// one ledger field.
func duplicatePathVerbKey(v duplicatePathVerb) string {
	return v.file + ":" + strings.ReplaceAll(v.name, " ", ".")
}

// foundDuplicatePaths is every duplicate this rule finds under root: function
// body pairs and verb-usage alias pairs.
func foundDuplicatePaths(t *testing.T, root string) []duplicatePath {
	t.Helper()
	return append(duplicatePathBodyPairs(t, root), duplicatePathVerbPairs(t, root)...)
}

// parseDuplicatePathsLedger reads the ledger text into its rows (pair key -> the
// reason after the two endpoints) and every format fault.
func parseDuplicatePathsLedger(text string) (map[string]string, []string) {
	rows := map[string]string{}
	var faults []string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			faults = append(faults, fmt.Sprintf("%s:%d: %q: a row is `<file:func> <file:func> <why>`",
				duplicatePathsLedgerPath, i+1, line))
			continue
		}
		key := (duplicatePath{a: fields[0], b: fields[1]}).key()
		if _, dup := rows[key]; dup {
			faults = append(faults, fmt.Sprintf("%s:%d: %q repeats a pair the ledger already lists",
				duplicatePathsLedgerPath, i+1, line))
			continue
		}
		rows[key] = strings.Join(fields[2:], " ")
	}
	return rows, faults
}

// duplicatePathsFindings compares the measured pairs with the ledger: a pair the
// ledger lacks is red, and a row whose pair is gone is stale and must be dropped.
func duplicatePathsFindings(ledger map[string]string, found []duplicatePath) []string {
	measured := map[string]bool{}
	var problems []string
	for _, p := range found {
		key := p.key()
		if measured[key] {
			continue
		}
		measured[key] = true
		if _, ok := ledger[key]; !ok {
			problems = append(problems, fmt.Sprintf("%s and %s are one path written twice and are not in %s; %s",
				p.a, p.b, duplicatePathsLedgerPath, duplicatePathsRemedy))
		}
	}
	for key := range ledger {
		if !measured[key] {
			problems = append(problems, fmt.Sprintf("%s lists %q, but no such pair is there any more; drop the stale row (the list only shrinks)",
				duplicatePathsLedgerPath, key))
		}
	}
	sort.Strings(problems)
	return problems
}

// TestDuplicatePathsLedgerOnlyShrinks holds the duplicate-paths ledger: every
// pair of function bodies that are the same after normalising names, at least
// eight statements long, and every verb usage line that is one line under two
// names, is listed; a listed pair that is gone is refused (docs/SPEC-CI.md,
// "duplicate paths").
func TestDuplicatePathsLedgerOnlyShrinks(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	text := readFile(t, filepath.Join(root, filepath.FromSlash(duplicatePathsLedgerRel)))
	ledger, faults := parseDuplicatePathsLedger(text)
	found := foundDuplicatePaths(t, root)

	problems := append(faults, duplicatePathsFindings(ledger, found)...)
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// TestDuplicatePathsRuleReadsEveryShape proves the rule over a fixture: a pair
// of nine-statement bodies that are the same after names are normalised is
// found, a short pair is not, a body whose only twin differs in a selector's
// name is not, and two verb registrations that share one usage line are found.
// This is the red test the ledger is grown from.
func TestDuplicatePathsRuleReadsEveryShape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	longA := `func longA(x int) int {
	a := x + 1
	b := a * 2
	c := b - 3
	d := c / 4
	e := d + 5
	f := e * 6
	g := f - 7
	h := g / 8
	return h
}
`
	longB := strings.Replace(longA, "longA", "longB", 1)
	shortA := "func shortA() int { a := 1; b := 2; return a + b }\n"
	shortB := strings.Replace(shortA, "shortA", "shortB", 1)
	unique := `func unique(x int) int {
	if x > 0 {
		return x
	}
	for i := 0; i < x; i++ {
		x += i
	}
	return -x
}
`
	selectorA := `func selectorA(v T) int {
	a := v.Foo()
	b := a + 1
	c := b * 2
	d := c - 3
	e := d / 4
	f := e + 5
	g := f * 6
	h := g - 7
	return h
}
`
	selectorB := strings.Replace(selectorA, "selectorA", "selectorB", 1)
	selectorB = strings.Replace(selectorB, "v.Foo()", "v.Bar()", 1)
	sprint := "package main\n\n" + longA + longB + shortA + shortB + unique + selectorA + selectorB
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cmd", "nova-sprint"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "cmd", "nova-sprint", "fixture.go"), []byte(sprint), 0o644))

	friend := `package main

import "github.com/mas-bandwidth/nova-tools/internal/tool"

var _ = []tool.Verb{
	{Name: "alpha", Usage: "alpha --x"},
	{Name: "beta", Usage: "alpha --x"},
	{Name: "gamma", Usage: "gamma --x"},
}
`
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cmd", "nova-friend"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "cmd", "nova-friend", "fixture.go"), []byte(friend), 0o644))

	bodyKeys := map[string]bool{}
	for _, p := range duplicatePathBodyPairs(t, root) {
		bodyKeys[p.key()] = true
	}
	assert.Contains(t, bodyKeys, "cmd/nova-sprint/fixture.go:longA cmd/nova-sprint/fixture.go:longB",
		"a nine-statement pair that is the same after names are normalised is found")
	assert.NotContains(t, bodyKeys, "cmd/nova-sprint/fixture.go:shortA cmd/nova-sprint/fixture.go:shortB",
		"a pair under eight statements is not held")
	assert.NotContains(t, bodyKeys, "cmd/nova-sprint/fixture.go:selectorA cmd/nova-sprint/fixture.go:selectorB",
		"a selector's field name is kept: x.Foo and x.Bar are different calls")
	for key := range bodyKeys {
		assert.NotContains(t, key, "unique", "a body with no twin is not a pair")
	}

	verbPairs := duplicatePathVerbPairs(t, root)
	require.Len(t, verbPairs, 1, "the two registrations that share one usage line are one alias pair")
	assert.Equal(t,
		"cmd/nova-friend/fixture.go:alpha cmd/nova-friend/fixture.go:beta",
		verbPairs[0].key(), "the alias pair is the two names under the shared usage line")
}

// TestDuplicatePathsLedgerRefusesGrowthAndStaleRows pins the ledger mechanics on
// texts held in the test: a pair the ledger lacks is red, a row whose pair is
// gone is red, and the two orders of one pair are one row.
func TestDuplicatePathsLedgerRefusesGrowthAndStaleRows(t *testing.T) {
	t.Parallel()

	found := []duplicatePath{{a: "a.go:f", b: "b.go:g"}}
	ledger, faults := parseDuplicatePathsLedger("b.go:g a.go:f the deletion card\n")
	require.Empty(t, faults)
	assert.Empty(t, duplicatePathsFindings(ledger, found), "a listed pair in the other order reads green")

	empty, faults := parseDuplicatePathsLedger("# a comment\n")
	require.Empty(t, faults)
	problems := duplicatePathsFindings(empty, found)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "are one path written twice and are not in", "an unlisted pair is red")

	stale, faults := parseDuplicatePathsLedger("a.go:f b.go:g the deletion card\nc.go:h d.go:i a pair now gone\n")
	require.Empty(t, faults)
	problems = duplicatePathsFindings(stale, found)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "no such pair is there any more", "a row whose pair is gone is red")

	_, faults = parseDuplicatePathsLedger("a.go:f only-one-endpoint\n")
	require.NotEmpty(t, faults, "a row with one endpoint is malformed")
}
