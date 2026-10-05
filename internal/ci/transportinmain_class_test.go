package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// transportinmain_class_test.go holds the owner's rule that logic takes its
// transport as a parameter (docs/STANDARD.md section 7: "A server is a function
// (a request in, a reply out, the state moved) apart from its transport, so its
// tests step it in one process and open no socket"). A transport the logic reads
// for itself is a default nobody injected: the test that holds that logic gets
// the real clock, the real environment, the real standard streams and the real
// child, and the only way to fake one is a package global the test mutates,
// which the parallel rule then refuses. So the transport is a parameter, and the
// two places that build the real one are main(), the composition root that wires
// the logic to the real transport, and a function named real*, the seam that
// answers an injected parameter with the real thing.
//
// The sweep reads the non-test Go under cmd/ and internal/ and measures four
// kinds of site:
//
//	clock  a call of time.Now
//	env    a call of os.Getenv or os.Environ
//	stdio  a read of os.Stdin, os.Stdout or os.Stderr
//	exec   a call of exec.Command
//
// testdata/transport-in-main/ holds one shard per tool or package: each row is
// `<package>:<kind> <sites> <reason>`, and the count only falls. A package that
// measures more sites than its row is red, a package with a site and no row is
// red, and a row above what the package measures is red, so the change that
// takes a transport as a parameter lowers its row in the same commit.
// NOVA_CI_UPDATE=1 lowers the counts and drops the rows at zero, and never
// raises one or adds one.
const transportLedgerPath = "testdata/transport-in-main"

// transportKinds are the four kinds of site, in the order the refusal lists them.
var transportKinds = []string{"clock", "env", "stdio", "exec"}

// transportSelectors are the sites: the selector as the logic writes it, and the
// kind of transport it takes for itself.
var transportSelectors = map[string]string{
	"time.Now":     "clock",
	"os.Getenv":    "env",
	"os.Environ":   "env",
	"os.Stdin":     "stdio",
	"os.Stdout":    "stdio",
	"os.Stderr":    "stdio",
	"exec.Command": "exec",
}

// transportRemedy is the one thing to do for each kind.
var transportRemedy = map[string]string{
	"clock": "take the clock as a parameter (a now func() time.Time) and pass time.Now from main()",
	"env":   "take the value or the environment as a parameter and read os.Getenv in main()",
	"stdio": "take the stream as an io.Reader or io.Writer parameter and pass os.Stdin, os.Stdout or os.Stderr from main()",
	"exec":  "take the runner as a parameter (internal/subproc) and start the child in main() or in a real* seam",
}

// transportUpdateCommand is the command that lowers a row: the ledger is
// ceiling-only, so it falls only under the update variable.
const transportUpdateCommand = "NOVA_CI_UPDATE=1 go test -count=1 -timeout 600s -run '^TestNoTransportIsBuiltOutsideMain$' ./internal/ci/"

// transportSite is one measured site.
type transportSite struct {
	Pkg, Kind, Where, What string
}

func (s transportSite) key() string { return s.Pkg + ":" + s.Kind }

// TestNoTransportIsBuiltOutsideMain sweeps every tool package's non-test Go and
// refuses a transport read outside main() and a real* function.
func TestNoTransportIsBuiltOutsideMain(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	var sites []transportSite
	for _, f := range tree.GoFilesUnder(false, "cmd", "internal") {
		if f.HasDirNamed("testdata") {
			continue
		}
		require.NoError(t, f.ParseErr, f.Rel)
		sites = append(sites, transportSitesInFile(tree.FSet, f.AST, f.Rel)...)
	}
	counts, byKey := transportCounts(sites)

	l, err := allowlist.LoadPackages(transportLedgerPath, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)
	rows, err := transportLedgerRows(l)
	require.NoError(t, err)

	update := allowlist.Updating()
	problems, kindsSeen := transportJudge(counts, byKey, rows, update)
	if len(problems) > 0 {
		assert.Failf(t, "the transport rule", "%s", transportRefusal(problems, kindsSeen))
		return
	}
	allowlist.CheckPackagesCountedMode(t, l, counts, update)
}

// transportCounts is the measured ledger input: the site count per key, and the
// sites behind each count, for the refusal to name.
func transportCounts(sites []transportSite) (map[string]int, map[string][]transportSite) {
	counts, byKey := map[string]int{}, map[string][]transportSite{}
	for _, s := range sites {
		counts[s.key()]++
		byKey[s.key()] = append(byKey[s.key()], s)
	}
	return counts, byKey
}

// transportJudge compares the measured counts with the ledger: the problems to
// print, and the kinds they concern. Under the update a count to lower and a row
// to drop are no problem: CheckPackagesCountedMode writes them.
func transportJudge(counts map[string]int, byKey map[string][]transportSite, rows map[string]int, update bool) (problems []string, kindsSeen map[string]bool) {
	kindsSeen = map[string]bool{}
	for _, key := range sortedKeys(counts) {
		n := counts[key]
		row, listed := rows[key]
		kind := key[strings.LastIndex(key, ":")+1:]
		switch {
		case !listed:
			kindsSeen[kind] = true
			problems = append(problems, fmt.Sprintf("%s: %d sites and no row; the ledger gains no row\n%s", key, n, transportExamples(byKey[key], 5)))
		case n > row:
			kindsSeen[kind] = true
			problems = append(problems, fmt.Sprintf("%s: %d sites, over its ledger of %d; the count only falls, and the new sites are in the files your change touches; the package's first five are:\n%s", key, n, row, transportExamples(byKey[key], 5)))
		case n < row && !update:
			problems = append(problems, fmt.Sprintf("%s: the tree has %d sites and the ledger %d; lower the row", key, n, row))
		}
	}
	for _, key := range sortedKeys(rows) {
		if counts[key] == 0 && !update {
			problems = append(problems, fmt.Sprintf("%s: %d in the ledger and none in the tree; delete the stale entry", key, rows[key]))
		}
	}
	return problems, kindsSeen
}

// transportRefusal is the one refusal: every problem, then the remedy of each
// kind it found, then the ledger and the command that lowers a row.
func transportRefusal(problems []string, kindsSeen map[string]bool) string {
	var remedies []string
	for _, kind := range transportKinds {
		if kindsSeen[kind] {
			remedies = append(remedies, kind+": "+transportRemedy[kind])
		}
	}
	return fmt.Sprintf("%s\n%s\n(docs/STANDARD.md section 7; the ledger is %s and a row only shrinks; run: %s)",
		strings.Join(problems, "\n"), strings.Join(remedies, "\n"), transportLedgerPath, transportUpdateCommand)
}

// transportLedgerRows reads the `<package>:<kind> <sites> <reason>` rows: a row
// with no reason or an unknown kind is refused, so every package says why its
// debt is still there.
func transportLedgerRows(l *allowlist.Packages) (map[string]int, error) {
	rows := map[string]int{}
	for _, shard := range l.Lists() {
		for _, r := range shard.Rows() {
			f := strings.Fields(r.Text)
			if len(f) < 3 {
				return nil, fmt.Errorf("%s:%d: %q is not `<package>:<kind> <sites> <reason>`", shard.Path, r.Line, r.Text)
			}
			n, err := strconv.Atoi(f[1])
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("%s:%d: %q: the sites are a positive number", shard.Path, r.Line, r.Text)
			}
			pkg, kind, ok := strings.Cut(f[0], ":")
			if !ok || pkg == "" || !slices.Contains(transportKinds, kind) {
				return nil, fmt.Errorf("%s:%d: %q: the kind is one of %v", shard.Path, r.Line, f[0], transportKinds)
			}
			if _, dup := rows[f[0]]; dup {
				return nil, fmt.Errorf("%s:%d: %s is listed twice", shard.Path, r.Line, f[0])
			}
			rows[f[0]] = n
		}
	}
	return rows, nil
}

// transportExamples lists up to max sites of one key, so a refusal names the
// sites and not only their count.
func transportExamples(sites []transportSite, max int) string {
	var b strings.Builder
	for i, s := range sites {
		if i == max {
			fmt.Fprintf(&b, "  ... and %d more\n", len(sites)-max)
			break
		}
		fmt.Fprintf(&b, "  %s %s\n", s.Where, s.What)
	}
	return strings.TrimRight(b.String(), "\n")
}

// transportSitesInFile measures one parsed non-test file: every transport
// selector that stands outside main() and outside a function named real*
// (docs/STANDARD.md section 7).
func transportSitesInFile(fset *token.FileSet, file *ast.File, rel string) []transportSite {
	pkg := path.Dir(rel)
	root := transportCompositionRoots(file)
	var sites []transportSite
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		what := id.Name + "." + sel.Sel.Name
		kind, ok := transportSelectors[what]
		if !ok || root(sel.Pos()) {
			return true
		}
		sites = append(sites, transportSite{
			Pkg:   pkg,
			Kind:  kind,
			Where: fmt.Sprintf("%s:%d", rel, fset.Position(sel.Pos()).Line),
			What:  what,
		})
		return true
	})
	return sites
}

// transportCompositionRoots answers whether a position stands inside one of the
// two places that build the real transport: the file's main(), which wires the
// logic to the real clock, environment, streams and child runner, or a function
// named real*, the seam that answers an injected parameter with the real thing.
// A function literal inside either is inside it: a closure the root builds is
// wired by the root.
func transportCompositionRoots(file *ast.File) func(token.Pos) bool {
	var spans [][2]token.Pos
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Name.Name != "main" && !strings.HasPrefix(fn.Name.Name, "real") {
			continue
		}
		spans = append(spans, [2]token.Pos{fn.Pos(), fn.End()})
	}
	return func(p token.Pos) bool {
		for _, s := range spans {
			if s[0] <= p && p < s[1] {
				return true
			}
		}
		return false
	}
}

// transportFixturePath is the name every fixture's sites are printed under, so a
// witness names its site the way the sweep names one in the tree.
const transportFixturePath = "tool/p.go"

// transportBrokenFixture breaks the rule once per kind — a global stream, a wall
// clock, the environment twice, a stream in a print, a child — and holds the two
// shapes that are exempt: main(), the composition root, a function literal
// inside it, and a function named real*.
const transportBrokenFixture = `package p

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

var banner = os.Stdout

func stamp() string { return time.Now().Format("2006-01-02") }

func home() string { return os.Getenv("HOME") + os.Environ()[0] }

func greet(name string) { fmt.Fprintln(os.Stdout, "hello", name) }

func list() error { return exec.Command("ls").Run() }

func main() {
	defer func() { fmt.Fprintln(os.Stderr, "done") }()
	_ = time.Now()
	_ = os.Getenv("HOME")
}

func realClock() time.Time { return time.Now() }

type server struct{}

func (s server) realEnvironment() []string { return os.Environ() }
`

// transportFixedFixture is the same logic with its transport as a parameter: the
// one place that builds the real transport is main(), and the child goes through
// a real* seam.
const transportFixedFixture = `package p

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// transport is what this package's logic needs, handed to it.
type transport struct {
	now    func() time.Time
	lookup func(string) string
	out    io.Writer
	run    func(string, ...string) error
}

func stamp(tr transport) string { return tr.now().Format("2006-01-02") }

func home(tr transport) string { return tr.lookup("HOME") }

func greet(tr transport, name string) { fmt.Fprintln(tr.out, "hello", name) }

func list(tr transport) error { return tr.run("ls") }

func main() {
	tr := transport{now: time.Now, lookup: os.Getenv, out: os.Stdout, run: realRun}
	_ = stamp(tr)
	_ = home(tr)
	greet(tr, "reader")
	_ = list(tr)
}

func realRun(name string, arg ...string) error { return exec.Command(name, arg...).Run() }
`

// TestTransportInMainRuleReadsTheShapes is the rule's witness: a fixture that
// breaks the rule once is refused naming the site, and the fixed fixture — the
// same logic with its transport as a parameter — passes.
func TestTransportInMainRuleReadsTheShapes(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		src  string
		want []string
	}{
		{"the logic reads its transport", transportBrokenFixture, []string{
			"stdio tool/p.go:10", "clock tool/p.go:12", "env tool/p.go:14", "env tool/p.go:14",
			"stdio tool/p.go:16", "exec tool/p.go:18",
		}},
		{"the transport is a parameter", transportFixedFixture, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, s := range transportFixtureSites(t, c.src) {
				got = append(got, s.Kind+" "+s.Where)
			}
			assert.Equal(t, c.want, got)
		})
	}

	t.Run("a breach is refused naming the site and its remedy", func(t *testing.T) {
		t.Parallel()
		counts, byKey := transportCounts(transportFixtureSites(t, transportBrokenFixture))
		problems, kindsSeen := transportJudge(counts, byKey, map[string]int{}, false)
		require.NotEmpty(t, problems)
		refusal := transportRefusal(problems, kindsSeen)
		for _, want := range []string{"tool:clock", "tool:env", "tool:stdio", "tool:exec",
			"tool/p.go:10", "tool/p.go:12", "tool/p.go:14", "tool/p.go:16", "tool/p.go:18",
			transportRemedy["clock"], transportRemedy["env"], transportRemedy["stdio"], transportRemedy["exec"]} {
			assert.Contains(t, refusal, want)
		}
	})
}

// transportFixtureSites parses one fixture and measures it under the fixture's
// name, the way the sweep measures a file of the tree.
func transportFixtureSites(t *testing.T, src string) []transportSite {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, transportFixturePath, src, 0)
	require.NoError(t, err)
	return transportSitesInFile(fset, file, transportFixturePath)
}
