package ci

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// duplicatePathsLedgerPath is the shrink-only ledger of duplicate code paths
// (nova-sprint v1.0.0, lens simplicity: two ways of doing one thing are two
// places to get it wrong). One line per pair, `<file:func> <file:func> <why>`.
//
// The list only shrinks. A normal run fails on a pair the ledger does not hold
// and on a row whose pair is gone; NOVA_CI_UPDATE=1 drops the stale rows and
// refuses to add a row for a new pair, the way the dead-code and flag-usage
// ledgers in this package behave.
const duplicatePathsLedgerPath = "testdata/duplicate-paths-ledger.txt"

// duplicatePathsMinStmts is the body size below which a look-alike is not read:
// a pair is debt only when each body holds at least this many statements.
const duplicatePathsMinStmts = 8

// duplicatePathsDirs is the tree the rule reads: the sprint's and the friend's
// own packages, cmd and internal both. internal/sprint/... includes its
// subpackages (store, driver, refmodel, friendtest).
var duplicatePathsDirs = []string{"cmd/nova-sprint", "internal/sprint", "cmd/nova-friend", "internal/friend"}

// duplicatePathsUpdateEnv is the variable that lowers the ledger. Only "1" does.
const duplicatePathsUpdateEnv = "NOVA_CI_UPDATE"

// duplicatePathsRemedy is what a worker does with a pair the ledger does not hold.
const duplicatePathsRemedy = "delete one side or have both call one lower-level helper; the ledger only shrinks and the update adds no row"

// duplicateBody is one non-test function body the rule measured.
type duplicateBody struct {
	id   string // file:func
	hash string
}

// duplicatePathPair is one finding: two names and why they are the same path.
type duplicatePathPair struct {
	a, b, why string
}

// duplicateFuncID is the `<file>:<func>` name a finding prints. A method carries
// its receiver type so two same-named methods of different types are distinct.
func duplicateFuncID(rel string, fd *ast.FuncDecl) string {
	name := fd.Name.Name
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		name = duplicateRecvName(fd.Recv.List[0].Type) + "." + name
	}
	return rel + ":" + name
}

// duplicateRecvName is the receiver's type name, pointer and type parameters
// stripped: `*T`, `T[P]` and `T` all read `T`.
func duplicateRecvName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return duplicateRecvName(x.X)
	case *ast.IndexExpr:
		return duplicateRecvName(x.X)
	case *ast.IndexListExpr:
		return duplicateRecvName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return "<recv>"
}

// duplicateStmts counts the statements in a body. The body's own block and the
// clause blocks/cases are containers, not statements; every other ast.Stmt node
// counts, including a control clause's init statement.
func duplicateStmts(body *ast.BlockStmt) int {
	n := 0
	ast.Inspect(body, func(x ast.Node) bool {
		switch x.(type) {
		case nil, *ast.BlockStmt, *ast.CaseClause, *ast.CommClause:
			return true
		}
		if _, ok := x.(ast.Stmt); ok {
			n++
		}
		return true
	})
	return n
}

// duplicateHash is the body normalised and hashed: every identifier is renamed
// in order of first use, a literal keeps its kind and text, and comments are not
// in the AST (the tree parses with mode 0). nil, true and false keep their names.
func duplicateHash(body *ast.BlockStmt) string {
	h := sha256.New()
	names := map[string]string{}
	seq := 0
	rename := func(name string) string {
		switch name {
		case "nil", "true", "false":
			return name
		}
		if v, ok := names[name]; ok {
			return v
		}
		v := fmt.Sprintf("v%d", seq)
		seq++
		names[name] = v
		return v
	}
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		switch x := n.(type) {
		case *ast.Ident:
			fmt.Fprintf(h, "id(%s) ", rename(x.Name))
		case *ast.BasicLit:
			fmt.Fprintf(h, "lit(%s,%q) ", x.Kind, x.Value)
		case *ast.BinaryExpr:
			fmt.Fprintf(h, "%T(%s) ", n, x.Op)
		case *ast.UnaryExpr:
			fmt.Fprintf(h, "%T(%s) ", n, x.Op)
		case *ast.AssignStmt:
			fmt.Fprintf(h, "%T(%s) ", n, x.Tok)
		case *ast.IncDecStmt:
			fmt.Fprintf(h, "%T(%s) ", n, x.Tok)
		case *ast.BranchStmt:
			fmt.Fprintf(h, "%T(%s) ", n, x.Tok)
		default:
			fmt.Fprintf(h, "%T ", n)
		}
		return true
	})
	return hex.EncodeToString(h.Sum(nil))
}

// duplicateBodiesInFile returns the hash of every non-test function body in f
// that holds at least duplicatePathsMinStmts statements.
func duplicateBodiesInFile(rel string, f *ast.File) []duplicateBody {
	var out []duplicateBody
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fd.Name == nil {
			continue
		}
		if duplicateStmts(fd.Body) < duplicatePathsMinStmts {
			continue
		}
		out = append(out, duplicateBody{id: duplicateFuncID(rel, fd), hash: duplicateHash(fd.Body)})
	}
	return out
}

// duplicatePairsFromBodies groups the measured bodies by hash and returns one
// pair for each unordered pair inside a group. A group of n names has C(n,2)
// pairs, so a three-way copy is three pairs, not one.
func duplicatePairsFromBodies(bodies []duplicateBody) []duplicatePathPair {
	byHash := map[string][]string{}
	for _, b := range bodies {
		byHash[b.hash] = append(byHash[b.hash], b.id)
	}
	var out []duplicatePathPair
	for _, ids := range byHash {
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				out = append(out, duplicatePathPair{a: ids[i], b: ids[j], why: "duplicate body"})
			}
		}
	}
	return out
}

// duplicateVerb is one tool.Verb help entry as the source declares it.
type duplicateVerb struct {
	id, name, usage string
}

// duplicateVerbsInFile reads every tool.Verb composite literal in f: a value
// literal (`tool.Verb{...}`) and each element of a `[]tool.Verb{...}` slice.
func duplicateVerbsInFile(rel string, f *ast.File) []duplicateVerb {
	var out []duplicateVerb
	add := func(lit *ast.CompositeLit) {
		name, usage := duplicateVerbFields(lit)
		if name == "" {
			return
		}
		out = append(out, duplicateVerb{id: rel + ":" + name, name: name, usage: usage})
	}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		switch t := lit.Type.(type) {
		case *ast.SelectorExpr:
			if t.Sel.Name == "Verb" {
				add(lit)
			}
		case *ast.ArrayType:
			sel, ok := t.Elt.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Verb" {
				return true
			}
			for _, e := range lit.Elts {
				if el, ok := e.(*ast.CompositeLit); ok {
					add(el)
				}
			}
		}
		return true
	})
	return out
}

// duplicateVerbFields reads the Name and Usage string literals of one Verb
// literal. A field built at run time is not read.
func duplicateVerbFields(lit *ast.CompositeLit) (name, usage string) {
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		bl, ok := kv.Value.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			continue
		}
		s, err := strconv.Unquote(bl.Value)
		if err != nil {
			continue
		}
		switch key.Name {
		case "Name":
			name = s
		case "Usage":
			usage = s
		}
	}
	return name, usage
}

// duplicateAliasPairs flags two verbs whose help usage line names the same verb
// under two different names: an alias left behind when the usage line was copied
// and the name was not. The usage line's first word is the verb it prints.
func duplicateAliasPairs(verbs []duplicateVerb) []duplicatePathPair {
	byVerb := map[string][]duplicateVerb{}
	for _, v := range verbs {
		first, _, _ := strings.Cut(strings.TrimSpace(v.usage), " ")
		if first == "" {
			continue
		}
		byVerb[first] = append(byVerb[first], v)
	}
	var out []duplicatePathPair
	for _, vs := range byVerb {
		if len(vs) < 2 {
			continue
		}
		names := map[string]bool{}
		for _, v := range vs {
			names[v.name] = true
		}
		if len(names) < 2 {
			continue
		}
		sort.Slice(vs, func(i, j int) bool { return vs[i].id < vs[j].id })
		for i := 0; i < len(vs); i++ {
			for j := i + 1; j < len(vs); j++ {
				if vs[i].name == vs[j].name {
					continue
				}
				out = append(out, duplicatePathPair{a: vs[i].id, b: vs[j].id, why: "alias help usage line"})
			}
		}
	}
	return out
}

// duplicatePathFindings reads the scoped tree and returns every pair the rule
// finds: duplicate function bodies and verb help aliases.
func duplicatePathFindings(t *testing.T) []duplicatePathPair {
	t.Helper()
	tree := repoTree(t)
	var bodies []duplicateBody
	var verbs []duplicateVerb
	files := 0
	for _, f := range tree.GoFilesUnder(false, duplicatePathsDirs...) {
		if f.HasDirNamed("testdata") || f.AST == nil {
			continue
		}
		files++
		bodies = append(bodies, duplicateBodiesInFile(f.Rel, f.AST)...)
		verbs = append(verbs, duplicateVerbsInFile(f.Rel, f.AST)...)
	}
	require.NotZero(t, files, "no Go file under %v; this test would pass by reading nothing", duplicatePathsDirs)
	require.NotEmpty(t, bodies, "no function body of %d statements or more under %v", duplicatePathsMinStmts, duplicatePathsDirs)
	out := duplicatePairsFromBodies(bodies)
	return append(out, duplicateAliasPairs(verbs)...)
}

// duplicatePathKey is the map key of a pair, its two names sorted so the order
// in the ledger does not matter.
func duplicatePathKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + " " + b
}

// duplicatePathIndex indexes pairs by their sorted key.
func duplicatePathIndex(pairs []duplicatePathPair) map[string]duplicatePathPair {
	out := make(map[string]duplicatePathPair, len(pairs))
	for _, p := range pairs {
		out[duplicatePathKey(p.a, p.b)] = p
	}
	return out
}

// duplicatePathKeys returns the map's keys, sorted.
func duplicatePathKeys(m map[string]duplicatePathPair) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// duplicatePathMissing returns the entries of a whose key is not in b.
func duplicatePathMissing(a, b map[string]duplicatePathPair) map[string]duplicatePathPair {
	out := map[string]duplicatePathPair{}
	for k, v := range a {
		if _, ok := b[k]; !ok {
			out[k] = v
		}
	}
	return out
}

// readDuplicatePathsLedger reads the ledger's rows: two names then the why. A
// comment line and a blank line are skipped; a row with no why is refused.
func readDuplicatePathsLedger(t *testing.T, path string) []duplicatePathPair {
	t.Helper()
	var rows []duplicatePathPair
	for i, line := range strings.Split(readFile(t, path), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		require.GreaterOrEqual(t, len(fields), 3, "%s:%d: a row is `<file:func> <file:func> <why>`", path, i+1)
		rows = append(rows, duplicatePathPair{a: fields[0], b: fields[1], why: strings.Join(fields[2:], " ")})
	}
	return rows
}

// writeDuplicatePathsLedger drops the stale rows and writes the rest back. It is
// only called from the update path, where a new pair has already been refused, so
// the written file can only be a subset of the one read.
func writeDuplicatePathsLedger(t *testing.T, rows []duplicatePathPair) {
	t.Helper()
	var b strings.Builder
	b.WriteString("# duplicate-paths ledger: function bodies that normalise to the same hash\n")
	b.WriteString("# One row per pair: `<file:func> <file:func> <why>`. The list only shrinks:\n")
	b.WriteString("# NOVA_CI_UPDATE=1 drops a stale row and refuses to add a row for a new pair.\n")
	b.WriteString("# Held by TestDuplicatePathsLedgerOnlyShrinks in internal/ci/duplicate_paths_class_test.go.\n")
	sorted := append([]duplicatePathPair(nil), rows...)
	sort.Slice(sorted, func(i, j int) bool {
		return duplicatePathKey(sorted[i].a, sorted[i].b) < duplicatePathKey(sorted[j].a, sorted[j].b)
	})
	for _, r := range sorted {
		fmt.Fprintf(&b, "%s %s %s\n", r.a, r.b, r.why)
	}
	require.NoError(t, os.WriteFile(duplicatePathsLedgerPath, []byte(b.String()), 0o644))
}

// TestDuplicatePathsLedgerOnlyShrinks holds the ledger: every pair of function
// bodies under cmd/nova-sprint, internal/sprint/..., cmd/nova-friend and
// internal/friend that normalise to the same hash holds a row, every alias verb
// help line holds a row, and a row whose pair is gone is refused so the list
// only shrinks.
func TestDuplicatePathsLedgerOnlyShrinks(t *testing.T) {
	t.Parallel()

	measured := duplicatePathIndex(duplicatePathFindings(t))
	listed := duplicatePathIndex(readDuplicatePathsLedger(t, duplicatePathsLedgerPath))

	unlisted := duplicatePathMissing(measured, listed)
	stale := duplicatePathMissing(listed, measured)

	if os.Getenv(duplicatePathsUpdateEnv) == "1" {
		require.Empty(t, unlisted, "%s: the update adds no row for a new pair; fix one side: %s", duplicatePathsLedgerPath, duplicatePathsRemedy)
		var keep []duplicatePathPair
		for _, k := range duplicatePathKeys(listed) {
			if _, ok := stale[k]; !ok {
				keep = append(keep, listed[k])
			}
		}
		updated := len(stale) > 0
		if updated {
			writeDuplicatePathsLedger(t, keep)
		}
		require.False(t, updated, "%s: dropped %d stale row(s); rerun without %s=1", duplicatePathsLedgerPath, len(stale), duplicatePathsUpdateEnv)
		return
	}

	var problems []string
	for _, k := range duplicatePathKeys(unlisted) {
		p := measured[k]
		problems = append(problems, fmt.Sprintf("%s and %s are the same after normalising names; %s", p.a, p.b, duplicatePathsRemedy))
	}
	for _, k := range duplicatePathKeys(stale) {
		r := listed[k]
		problems = append(problems, fmt.Sprintf("%s lists %s and %s, which is gone; delete the stale row (the ledger only shrinks; %s=1 drops it)", duplicatePathsLedgerPath, r.a, r.b, duplicatePathsUpdateEnv))
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// TestDuplicatePathsRuleReadsEveryShape proves the rule over source: two bodies
// of at least eight statements that differ only in identifier names are one
// pair, and a shorter look-alike is not read.
func TestDuplicatePathsRuleReadsEveryShape(t *testing.T) {
	t.Parallel()
	src := `package p

func alpha() {
	a := 1
	b := 2
	c := a + b
	_ = c
	d := alpha2(c)
	_ = d
	e := alpha2(d)
	_ = e
}

func beta() {
	x := 1
	y := 2
	z := x + y
	_ = z
	w := alpha2(z)
	_ = w
	v := alpha2(w)
	_ = v
}

func short() {
	a := 1
	_ = a
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	require.NoError(t, err)

	bodies := duplicateBodiesInFile("p.go", f)
	require.Len(t, bodies, 2, "only the two long bodies are measured")
	pairs := duplicatePairsFromBodies(bodies)
	require.Len(t, pairs, 1)
	assert.Equal(t, "duplicate body", pairs[0].why)
	assert.ElementsMatch(t, []string{"p.go:alpha", "p.go:beta"}, []string{pairs[0].a, pairs[0].b})
}

// TestDuplicatePathsAliasHelpReadsEveryShape proves the alias rule over source:
// two verbs whose usage line opens with the same verb word are one pair, while a
// verb whose usage names its own name is not.
func TestDuplicatePathsAliasHelpReadsEveryShape(t *testing.T) {
	t.Parallel()
	src := `package p

var verbs = []tool.Verb{
	{Name: "take", Usage: "take --card <id>"},
	{Name: "grab", Usage: "take --card <id>"},
	{Name: "wait", Usage: "wait --for <dur>"},
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	require.NoError(t, err)

	verbs := duplicateVerbsInFile("p.go", f)
	require.Len(t, verbs, 3)
	pairs := duplicateAliasPairs(verbs)
	require.Len(t, pairs, 1)
	assert.Equal(t, "alias help usage line", pairs[0].why)
	assert.ElementsMatch(t, []string{"p.go:grab", "p.go:take"}, []string{pairs[0].a, pairs[0].b})
}
