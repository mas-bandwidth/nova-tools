package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// nohandprinting_class_test.go holds the skeleton's contract 1.5 (docs/STANDARD.md
// section 2, "The status word leads every line"): a verb builds one Out and the
// skeleton computes the status word from it and renders it as lines or as JSON, so
// a tool package neither types a status word into a format string nor parses its
// own flags nor ends the process itself. The four shapes that do:
//
//	stream-print  fmt.Fprint, fmt.Fprintf or fmt.Fprintln to os.Stdout or os.Stderr
//	flagset       flag.NewFlagSet, a flag set the verb built itself
//	verbflag      verbflag.New, the same set through the seam the skeleton owns
//	exit          os.Exit outside main() and outside a verb that declares Prints
//
// testdata/no-hand-printing/ holds one shard per tool package: each row is
// `<package>:<kind> <sites> <reason>`, and the count only falls. A package that
// measures more sites than its row is red, a package with a site and no row is
// red, and a row above what the package measures is red, so a port onto
// pkg/tool or a fix lowers its own row in the same change.
// NOVA_CI_UPDATE=1 lowers the counts and drops the rows at zero, and never
// raises a count or adds a row. The seeding run is the one time rows are written.
const noHandPrintingLedgerPath = "testdata/no-hand-printing"

// noHandPrintingWants says what each kind's site wants instead, the sentence a
// refusal prints before its remedy.
var noHandPrintingWants = map[string]string{
	"stream-print": "a tool package writes its own line to a process stream: the verb returns one Out and the skeleton computes the status word from it (skeleton contract 1.5, docs/STANDARD.md section 2), so no line is typed into a format string",
	"flagset":      "a tool package builds its own flag.FlagSet: the skeleton parses the verb's flags through tool.Flags, which gives the verb its help, its --json and its refusal grammar (docs/STANDARD.md section 3)",
	"verbflag":     "a tool package builds its own flag set through verbflag.New: a verb on pkg/tool declares its flags with tool.Flags and the skeleton parses them (docs/STANDARD.md section 3)",
	"exit":         "a tool package ends the process itself: a verb returns its Out and its exit, main() is the one os.Exit, and a verb that writes a payload the skeleton cannot render declares Prints (tool.Flags.Prints) and returns tool.Exit",
}

// noHandPrintingRemedy is the one thing to do for each kind.
var noHandPrintingRemedy = map[string]string{
	"stream-print": "build the line as an Out (tool.OK, tool.Refuse, Out.Item, Out.ItemText) and return it; a payload the skeleton cannot render goes behind a verb that declares Prints and writes to c.Stdout",
	"flagset":      "declare the flags with tool.Flags in the verb's Flags func and read them with the tool.Flag accessors; a tool not on pkg/tool moves onto it",
	"verbflag":     "declare the flags with tool.Flags in the verb's Flags func; a tool not on pkg/tool moves onto it",
	"exit":         "return the verb's Out and its exit code from the verb, or declare Prints and return tool.Exit; only main() calls os.Exit",
}

// noHandPrintingSite is one measured site.
type noHandPrintingSite struct {
	Pkg, Kind, Fn, Where string
}

func (s noHandPrintingSite) key() string { return s.Pkg + ":" + s.Kind }

// TestNoHandPrintingInAToolPackage is the class test: the non-test Go of every
// tool package is read for the four shapes, and a site is red unless its
// `<package>:<kind>` row covers it (docs/SPEC-CI.md, `no-hand-printing`).
func TestNoHandPrintingInAToolPackage(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	pkgs := toolPackages(tree)
	require.NotEmpty(t, pkgs, "no tool package under cmd/ or internal/; this test would pass by reading nothing")
	assert.Contains(t, pkgs, "pkg/release", "nova-update's release verbs are dispatched in pkg/release without a tool.Tool")
	assert.NotContains(t, pkgs, "pkg/tool", "the skeleton is not a tool's verbs")
	assert.NotContains(t, pkgs, "pkg/nsprint/verbflag", "the flag seam is not a tool's verbs")

	byKey := map[string][]noHandPrintingSite{}
	files := 0
	for _, pkg := range pkgs {
		for _, f := range tree.GoFilesUnder(false, pkg) {
			if f.HasDirNamed("testdata") || path.Dir(f.Rel) != pkg {
				continue
			}
			require.NoError(t, f.ParseErr, f.Rel)
			files++
			for _, s := range noHandPrintingSites(tree.FSet, f.AST, f.Rel, pkg) {
				byKey[s.key()] = append(byKey[s.key()], s)
			}
		}
	}
	require.NotZero(t, files, "no Go file in the tool packages; this test would pass by reading nothing")

	l, err := allowlist.LoadPackages(noHandPrintingLedgerPath, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)
	requireReasons(t, l)
	for _, v := range noHandPrintingViolations(t, l, byKey, allowlist.Updating()) {
		t.Error(v)
	}
}

// toolPackages names the packages this rule reads (docs/SPEC-CI.md,
// `no-hand-printing`): every package under cmd/ that holds a non-test Go file,
// and every package under internal/ that holds a tool's verbs, found by what it
// builds or dispatches rather than by a name written here. pkg/tool and
// pkg/nsprint/verbflag are the skeleton and the flag seam, not a tool's verbs.
func toolPackages(tree *repoTreeIndex) []string {
	set := map[string]bool{}
	for _, f := range tree.GoFilesUnder(false, "cmd") {
		if f.HasDirNamed("testdata") {
			continue
		}
		set[path.Dir(f.Rel)] = true
	}
	for _, f := range tree.GoFilesUnder(false, "internal", "pkg") {
		if f.HasDirNamed("testdata") || f.ParseErr != nil {
			continue
		}
		pkg := path.Dir(f.Rel)
		if isToolVerbPackage(pkg, f.AST) {
			set[pkg] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// isToolVerbPackage reports whether pkg holds a tool's verbs (docs/SPEC-CI.md,
// `no-hand-printing`): it builds a tool.Tool, or a function takes the verb off
// the first argument and switches on it. The skeleton and the flag seam are not
// a tool's verbs, even when a file there has one of those shapes.
func isToolVerbPackage(pkg string, f *ast.File) bool {
	if skeletonOrFlagSeam(pkg) {
		return false
	}
	return buildsATool(f) || dispatchesToolVerbs(f)
}

// skeletonOrFlagSeam is pkg/tool, the skeleton, and pkg/nsprint/verbflag,
// the flag seam. Neither holds a tool's verbs (docs/SPEC-CI.md, `no-hand-printing`).
func skeletonOrFlagSeam(pkg string) bool {
	return pkg == "pkg/tool" || strings.HasPrefix(pkg, "pkg/tool/") ||
		pkg == "pkg/nsprint/verbflag" || strings.HasPrefix(pkg, "pkg/nsprint/verbflag/")
}

// buildsATool reports whether the file builds a tool.Tool, the skeleton's one
// value a tool's verbs hang off (docs/SPEC-CI.md, `no-hand-printing`).
func buildsATool(f *ast.File) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if name, ok := lit.Type.(*ast.SelectorExpr); ok && exprText(name) == "tool.Tool" {
			found = true
			return false
		}
		return true
	})
	return found
}

// dispatchesToolVerbs reports whether a function takes the verb off the first
// argument and switches on it (docs/SPEC-CI.md, `no-hand-printing`).
// pkg/release.Run is that shape and does not build a tool.Tool. A switch
// on a word cut from a line is not one: the assigned name is not args[0].
func dispatchesToolVerbs(f *ast.File) bool {
	if f == nil {
		return false
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		verbs := firstArgIdents(fd.Body)
		if len(verbs) == 0 {
			continue
		}
		found := false
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok || sw.Tag == nil || !verbs[exprText(sw.Tag)] || !switchHasStringCase(sw) {
				return true
			}
			found = true
			return false
		})
		if found {
			return true
		}
	}
	return false
}

// firstArgIdents names identifiers assigned from the first element of a slice
// in body, the verb a hand dispatcher takes off args[0].
func firstArgIdents(body *ast.BlockStmt) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		ix, ok := as.Rhs[0].(*ast.IndexExpr)
		if !ok {
			return true
		}
		lit, ok := ix.Index.(*ast.BasicLit)
		if ok && lit.Kind == token.INT && lit.Value == "0" {
			names[id.Name] = true
		}
		return true
	})
	return names
}

// switchHasStringCase reports whether the switch names a verb as a string literal.
func switchHasStringCase(sw *ast.SwitchStmt) bool {
	for _, stmt := range sw.Body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, e := range clause.List {
			lit, ok := e.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING {
				return true
			}
		}
	}
	return false
}

// noHandPrintingSites reads one file of a tool package for the four shapes of
// skeleton contract 1.5 (docs/SPEC-CI.md, `no-hand-printing`). fn is the function
// the site stands in, so a refusal names the site and its verb.
func noHandPrintingSites(fset *token.FileSet, f *ast.File, rel, pkg string) []noHandPrintingSite {
	exempt := exitExemptSpans(f)
	var out []noHandPrintingSite
	visit := func(fn string, n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			kind := ""
			switch pkgName, name := exprText(sel.X), sel.Sel.Name; {
			case pkgName == "fmt" && (name == "Fprint" || name == "Fprintf" || name == "Fprintln") &&
				len(call.Args) > 0 && isProcessStream(call.Args[0]):
				kind = "stream-print"
			case pkgName == "flag" && name == "NewFlagSet":
				kind = "flagset"
			case pkgName == "verbflag" && name == "New":
				kind = "verbflag"
			case pkgName == "os" && name == "Exit" && !inSpans(exempt, call.Pos()):
				kind = "exit"
			}
			if kind == "" {
				return true
			}
			out = append(out, noHandPrintingSite{
				Pkg:   pkg,
				Kind:  kind,
				Fn:    fn,
				Where: fmt.Sprintf("%s:%d", rel, fset.Position(call.Pos()).Line),
			})
			return true
		})
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			fn := fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) > 0 {
				fn = exprText(fd.Recv.List[0].Type) + "." + fn
			}
			visit(fn, fd)
			continue
		}
		visit("package", d)
	}
	return out
}

// isProcessStream reports whether the expression is one of the two streams the
// process owns. A write to the skeleton's own c.Stdout, or to a bytes.Buffer a
// line is assembled in, is not one: the skeleton hands those to a verb that
// declared Prints, and the buffer is a value, not a stream.
func isProcessStream(e ast.Expr) bool {
	switch exprText(e) {
	case "os.Stdout", "os.Stderr":
		return true
	}
	return false
}

// span is a range of source an os.Exit is allowed in.
type span struct{ from, to token.Pos }

func inSpans(spans []span, pos token.Pos) bool {
	for _, s := range spans {
		if pos >= s.from && pos <= s.to {
			return true
		}
	}
	return false
}

// exitExemptSpans are the two places an os.Exit is the design's: the body of
// main(), the process's one exit, and the Run of a verb that declares Prints
// (tool.Flags.Prints), which writes a payload the skeleton cannot render and
// returns tool.Exit (pkg/tool, Flags.Prints and Call.Exit).
func exitExemptSpans(f *ast.File) []span {
	var spans []span
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		if fd.Recv == nil && fd.Name.Name == "main" {
			spans = append(spans, span{fd.Body.Pos(), fd.Body.End()})
		}
	}
	for _, lit := range verbLiterals(f) {
		var flags, run ast.Expr
		for _, e := range lit.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			switch exprText(kv.Key) {
			case "Flags":
				flags = kv.Value
			case "Run":
				run = kv.Value
			}
		}
		if run == nil || !declaresPrints(flags) {
			continue
		}
		spans = append(spans, span{run.Pos(), run.End()})
	}
	return spans
}

// verbLiterals lists every tool.Verb literal of the file, whether it stands
// alone or is an element of a []tool.Verb, whose elements elide their type.
func verbLiterals(f *ast.File) []*ast.CompositeLit {
	var out []*ast.CompositeLit
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		switch t := lit.Type.(type) {
		case *ast.SelectorExpr:
			if exprText(t) == "tool.Verb" {
				out = append(out, lit)
			}
		case *ast.ArrayType:
			if name, ok := t.Elt.(*ast.SelectorExpr); ok && exprText(name) == "tool.Verb" {
				for _, e := range lit.Elts {
					if elt, ok := e.(*ast.CompositeLit); ok {
						out = append(out, elt)
					}
				}
			}
		}
		return true
	})
	return out
}

// declaresPrints reports whether a verb's Flags func calls Prints, the verb's
// declaration that it writes its own output.
func declaresPrints(flags ast.Expr) bool {
	fn, ok := flags.(*ast.FuncLit)
	if !ok || fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Prints" && len(call.Args) == 0 {
			found = true
			return false
		}
		return true
	})
	return found
}

// noHandPrintingViolations checks the measured sites against the ledger through
// the one helper (docs/SPEC-CI.md, `allowlist`): a key with no row is red at each
// of its sites, a key over its row is red naming every site, a key under its row
// asks for the count to be lowered and a row with no site left is stale. Each
// line names the site, what it wants and the command to run next.
func noHandPrintingViolations(t *testing.T, l *allowlist.Packages, byKey map[string][]noHandPrintingSite, update bool) []string {
	t.Helper()
	measured := make(map[string]int, len(byKey))
	for k, sites := range byKey {
		measured[k] = len(sites)
	}
	res := allowlist.CheckPackagesCountedMode(t, l, measured, update)
	siteLine := func(s noHandPrintingSite) string {
		return fmt.Sprintf("%s (%s): %s; run: %s", s.Where, s.Fn, noHandPrintingWants[s.Kind], noHandPrintingRemedy[s.Kind])
	}
	var out []string
	for _, k := range res.Unlisted {
		for _, s := range byKey[k] {
			out = append(out, fmt.Sprintf("%s (no row in %s, and the ledger gains none)", siteLine(s), noHandPrintingLedgerPath))
		}
	}
	for _, c := range res.Over {
		lines := make([]string, 0, len(byKey[c.Key]))
		for _, s := range byKey[c.Key] {
			lines = append(lines, siteLine(s))
		}
		out = append(out, fmt.Sprintf("%s lists %s at %d sites, but %d are there now; a new site is not covered by a row written for fewer (the count only falls):\n%s",
			noHandPrintingLedgerPath, c.Key, c.Listed, c.Measured, strings.Join(lines, "\n")))
	}
	for _, c := range res.Lowered {
		out = append(out, fmt.Sprintf("%s lists %s at %d sites, but only %d are there now; lower the row's count to %d (the list only shrinks; NOVA_CI_UPDATE=1 lowers it)",
			noHandPrintingLedgerPath, c.Key, c.Listed, c.Measured, c.Measured))
	}
	for _, row := range res.Stale {
		out = append(out, fmt.Sprintf("%s lists %s, but no site of that key is there any more; delete the stale row (the list only shrinks; NOVA_CI_UPDATE=1 drops it)",
			noHandPrintingLedgerPath, row.Key))
	}
	sort.Strings(out)
	return out
}

// noHandPrintingBreach is the witness fixture: one site of each kind, in a tool
// package that is not on the skeleton. noHandPrintingFixed is the same tool with
// each site done the skeleton's way.
const noHandPrintingBreach = `package p

import ("flag"; "fmt"; "os")

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.Parse(args)
	vs := verbflag.New("push")
	_ = vs
	fmt.Fprintf(os.Stdout, "PUSH OK id=%s\n", "x")
	os.Exit(1)
	return 0
}

func payload(c *tool.Call) *tool.Out {
	return tool.Exit(2)
}

var verbs = []tool.Verb{{
	Name:  "spill",
	Flags: func(f *tool.Flags) { f.Prints() },
	Run:   func(c *tool.Call) *tool.Out { os.Exit(3); return nil },
}}
`

const noHandPrintingFixed = `package p

import ("fmt"; "os")

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int { return 0 }

var verbs = []tool.Verb{{
	Name:  "spill",
	Flags: func(f *tool.Flags) { f.Prints() },
	Run:   func(c *tool.Call) *tool.Out { fmt.Fprintln(c.Stdout, "SPILL OK"); return tool.Exit(0) },
}, {
	Name: "push",
	Flags: func(f *tool.Flags) { f.String("id", "", "the record to push") },
	Run:   func(c *tool.Call) *tool.Out { return tool.OK("id", "x") },
}}
`

// TestNoHandPrintingWitness is the rule's witness: the fixture that breaks the
// rule once per kind is refused with every site named, and the same tool written
// the skeleton's way is not. The ledger the breach is judged against is empty,
// because the ledger gains no row: the refusal is the point.
func TestNoHandPrintingWitness(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	breach, err := parser.ParseFile(fset, "cmd/nova-example/push.go", noHandPrintingBreach, 0)
	require.NoError(t, err)
	fixed, err := parser.ParseFile(fset, "cmd/nova-example/push.go", noHandPrintingFixed, 0)
	require.NoError(t, err)

	sites := noHandPrintingSites(fset, breach, "cmd/nova-example/push.go", "cmd/nova-example")
	var got []string
	for _, s := range sites {
		got = append(got, s.key()+"@"+s.Fn)
	}
	assert.Equal(t, []string{
		"cmd/nova-example:flagset@run",
		"cmd/nova-example:verbflag@run",
		"cmd/nova-example:stream-print@run",
		"cmd/nova-example:exit@run",
	}, got, "main()'s os.Exit and the Prints verb's are exempt; the four shapes in run are not")

	empty := emptyNoHandPrintingLedger(t)
	byKey := map[string][]noHandPrintingSite{}
	for _, s := range sites {
		byKey[s.key()] = append(byKey[s.key()], s)
	}
	violations := noHandPrintingViolations(t, empty, byKey, false)
	require.Len(t, violations, 4, "one refusal per site")
	joined := strings.Join(violations, "\n")
	for _, s := range sites {
		assert.Contains(t, joined, s.Where, "the refusal names the site")
		assert.Contains(t, joined, noHandPrintingRemedy[s.Kind], "the refusal names the remedy")
	}

	fixedSites := noHandPrintingSites(fset, fixed, "cmd/nova-example/push.go", "cmd/nova-example")
	assert.Empty(t, fixedSites, "the fixed fixture: %v", fixedSites)
	assert.Empty(t, noHandPrintingViolations(t, emptyNoHandPrintingLedger(t), map[string][]noHandPrintingSite{}, false))
}

// emptyNoHandPrintingLedger is an empty counted package ledger in the test's own
// directory, the shape the real one is read with.
func emptyNoHandPrintingLedger(t *testing.T) *allowlist.Packages {
	t.Helper()
	l, err := allowlist.LoadPackages(t.TempDir(), allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)
	return l
}

// TestNoHandPrintingReadsAHandDispatcher pins the package set: a file that takes
// the verb off the first argument and switches on it is a tool package even when
// it builds no tool.Tool, and the skeleton and the flag seam are not, and a
// switch on a word cut from a line is not a dispatch (docs/SPEC-CI.md,
// `no-hand-printing`).
func TestNoHandPrintingReadsAHandDispatcher(t *testing.T) {
	t.Parallel()

	src := `package release

import "flag"

func Run(args []string) int {
	verb := args[0]
	switch verb {
	case "cut", "build":
	}
	f := flag.NewFlagSet("release "+verb, flag.ContinueOnError)
	_ = f
	return 0
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "pkg/release/cli.go", src, 0)
	require.NoError(t, err)
	assert.True(t, dispatchesToolVerbs(file), "a hand dispatcher is a tool package without a tool.Tool")
	assert.False(t, buildsATool(file))
	assert.True(t, isToolVerbPackage("pkg/release", file))
	assert.False(t, isToolVerbPackage("pkg/tool", file), "the skeleton is not a tool's verbs")
	assert.False(t, isToolVerbPackage("pkg/nsprint/verbflag", file), "the flag seam is not a tool's verbs")

	sites := noHandPrintingSites(fset, file, "pkg/release/cli.go", "pkg/release")
	require.Len(t, sites, 1)
	assert.Equal(t, "flagset", sites[0].Kind)
	assert.Equal(t, "pkg/release/cli.go:10", sites[0].Where)

	other := `package swarm

func observe(line string) {
	verb, _, _ := cutWord(line)
	switch verb {
	case "BEGIN":
	}
}
`
	g, err := parser.ParseFile(token.NewFileSet(), "timeline.go", other, 0)
	require.NoError(t, err)
	assert.False(t, dispatchesToolVerbs(g), "a switch on a word cut from a line is not a verb dispatch")

	built := `package update

func VersionTool() *tool.Tool { return &tool.Tool{Name: "nova-version"} }
`
	b, err := parser.ParseFile(token.NewFileSet(), "versiontool.go", built, 0)
	require.NoError(t, err)
	assert.True(t, buildsATool(b))
	assert.True(t, isToolVerbPackage("internal/update", b))
}

// TestNoHandPrintingRuleReadsEveryShape pins the reader's narrowings: a write to
// the skeleton's stream or to a buffer is not a stream print, an os.Exit inside
// main() or inside a Prints verb's Run is not an exit site, and a verb whose
// Flags func does not declare Prints does not exempt one.
func TestNoHandPrintingRuleReadsEveryShape(t *testing.T) {
	t.Parallel()

	src := `package p

import ("bytes"; "flag"; "fmt"; "os")

func main() {
	os.Exit(0)
	defer func() { os.Exit(1) }()
}

func (t tool) render(c *tool.Call, usage string) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "row %s\n", usage)
	fmt.Fprintln(c.Stdout, b.String())
	fmt.Fprintf(c.Stderr, "NOTE %s\n", usage)
	fmt.Fprintln(os.Stderr, usage)
	fmt.Fprintf(os.Stdout, usage)
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	_ = fs
}

var plain = []tool.Verb{{
	Name:  "quiet",
	Flags: func(f *tool.Flags) { f.String("id", "", "the record") },
	Run:   func(c *tool.Call) *tool.Out { os.Exit(4); return nil },
}}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	require.NoError(t, err)
	var got []string
	for _, s := range noHandPrintingSites(fset, f, "p.go", "cmd/nova-example") {
		got = append(got, s.Kind+"@"+s.Fn+":"+strings.TrimPrefix(s.Where, "p.go:"))
	}
	sort.Strings(got)
	assert.Equal(t, []string{
		"exit@package:24",
		"flagset@tool.render:17",
		"stream-print@tool.render:15",
		"stream-print@tool.render:16",
	}, got)
}
