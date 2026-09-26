package ci

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
)

// unitwaits_class_test.go is the ENFORCED half of Rowan's ruling on
// nova-tools#4413 (2026-09-26): a budget verdict must be the same on any
// machine, so what fails a leg is static, and the wall-time budgets are
// measurements (internal/ci/slowtests: Verdict). The static rule is Glenn's
// (2026-09-25): no unit test waits on the wall clock.
//
// THE RULE. Every _test.go the unit tier compiles -- under cmd/, internal/ and
// tools/, outside testdata, and built with no custom tag (a file behind
// `//go:build functional`, slow, soak, nightly, novadisk, perf or race is not
// a unit test; unitTierFile reads the constraint) -- is read, and a direct
// wall-clock wait is found:
//
//   - a call of time.Sleep, time.After, time.Tick, time.NewTimer,
//     time.NewTicker or time.AfterFunc (so `<-time.After(d)` in a select is
//     one), and any of those named as a value (the real clock handed to a
//     seam: `Sleep: time.Sleep`);
//   - a context.WithTimeout or context.WithDeadline whose context the same
//     function waits on (`<-ctx.Done()`).
//
// A wait that goes through an injected clock seam is not a wall-clock wait
// and is not found: the test calls the seam, never package time. The seams the
// tree already has: internal/wake.Clock (a fake that advances on Sleep),
// internal/bus lockClock, internal/swarm batchClock and pullClock,
// internal/nsprint/land.Clock, internal/log.Clock, and the injected
// `Sleep func(time.Duration)` / `now func() time.Time` fields of internal/merge,
// internal/gh, internal/swarm, cmd/nova-merge and cmd/nova-sprint.
//
// THE LEDGER. A wait is keyed by its package directory and the top-level
// function it is written in (a Test, a helper, a method `Type.Method`), and it
// is allowed only when internal/ci/sleeps-skips_allowlist.txt names that key.
// The ledger holds exactly the tree's SLEEPS skips and waiting functions
// (TestSleepsLedgerIsTheTreesSleepsSkips), and it ONLY SHRINKS against the
// merge parent: a row HEAD has that HEAD's first parent's ledger does not is red
// (TestSleepsLedgerOnlyShrinksAgainstTheMergeParent), so a change that adds a
// wait and its row together is red. A first parent with no ledger at all is the
// seed: the change that introduced the file (#4413).

// sleepsLedgerKey is a ledger row's key: `<package dir>\t<function>`.
func sleepsLedgerKey(pkg, fn string) string { return pkg + "\t" + fn }

// wallClockWait is one direct wall-clock wait: the file, the line, the
// top-level function it is written in, and what it is.
type wallClockWait struct {
	Rel, Func, What string
	Line            int
}

// Key is the ledger key the wait is allowed under.
func (w wallClockWait) Key() string { return sleepsLedgerKey(path.Dir(w.Rel), w.Func) }

// unitWaitDirs are the trees the unit tier's tests live in.
var unitWaitDirs = []string{"cmd", "internal", "tools"}

// timeWaits are the package time functions that wait on the wall clock, called
// or named as a value.
var timeWaits = map[string]bool{"Sleep": true, "After": true, "Tick": true, "NewTimer": true, "NewTicker": true, "AfterFunc": true}

// unitPlatforms are the GOOS/GOARCH pairs the unit tier's legs run on: the
// space shards and the Studio's darwin-arm64 shards (and the Intel Macs).
var unitPlatforms = [][2]string{{"linux", "amd64"}, {"darwin", "arm64"}, {"darwin", "amd64"}}

// unitTierFile reports whether the unit tier compiles this file: its
// `//go:build` line holds on a unit platform with no custom tag set. A file
// with no constraint is a unit file.
func unitTierFile(src []byte) bool {
	var expr constraint.Expr
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			break
		}
		if constraint.IsGoBuild(line) {
			e, err := constraint.Parse(line)
			if err != nil {
				return true
			}
			expr = e
			break
		}
	}
	if expr == nil {
		return true
	}
	for _, p := range unitPlatforms {
		goos, goarch := p[0], p[1]
		if expr.Eval(func(tag string) bool {
			switch tag {
			case goos, goarch, "gc", "cgo":
				return true
			case "unix":
				return goos == "linux" || goos == "darwin"
			}
			return strings.HasPrefix(tag, "go1.")
		}) {
			return true
		}
	}
	return false
}

// importName is the name file f uses for the import path p ("" when not imported).
func importName(f *ast.File, p string) string {
	for _, imp := range f.Imports {
		v, err := strconv.Unquote(imp.Path.Value)
		if err != nil || v != p {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return path.Base(p)
	}
	return ""
}

// topLevelName is the ledger name of a top-level declaration: the function, the
// method as `Type.Method`, or the first name of a var or const spec.
func topLevelName(d ast.Decl) string {
	switch x := d.(type) {
	case *ast.FuncDecl:
		if x.Recv != nil && len(x.Recv.List) > 0 {
			t := x.Recv.List[0].Type
			if s, ok := t.(*ast.StarExpr); ok {
				t = s.X
			}
			if g, ok := t.(*ast.IndexExpr); ok {
				t = g.X
			}
			if id, ok := t.(*ast.Ident); ok {
				return id.Name + "." + x.Name.Name
			}
		}
		return x.Name.Name
	case *ast.GenDecl:
		for _, spec := range x.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Names) > 0 {
				return vs.Names[0].Name
			}
		}
	}
	return "-"
}

// scanWallClockWaits returns the direct wall-clock waits in one parsed file.
func scanWallClockWaits(fset *token.FileSet, rel string, f *ast.File) []wallClockWait {
	timeName, ctxName := importName(f, "time"), importName(f, "context")
	var out []wallClockWait
	for _, d := range f.Decls {
		fn := topLevelName(d)
		// The contexts this declaration makes with a deadline, by variable
		// name and line, and the ones it waits on.
		deadlines := map[string]int{}
		waited := map[string]bool{}
		calls := map[*ast.SelectorExpr]bool{}
		ast.Inspect(d, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
					calls[sel] = true
				}
			case *ast.AssignStmt:
				if len(x.Rhs) == 1 && len(x.Lhs) >= 1 && ctxName != "" {
					if call, ok := x.Rhs[0].(*ast.CallExpr); ok {
						if pkg, sel := selName(call.Fun); pkg == ctxName && (sel == "WithTimeout" || sel == "WithDeadline") {
							if id, ok := x.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
								deadlines[id.Name] = fset.Position(call.Pos()).Line
							}
						}
					}
				}
			case *ast.UnaryExpr:
				if x.Op != token.ARROW {
					return true
				}
				if call, ok := x.X.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Done" {
						if id, ok := sel.X.(*ast.Ident); ok {
							waited[id.Name] = true
						}
					}
				}
			}
			return true
		})
		ast.Inspect(d, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || timeName == "" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == timeName && timeWaits[sel.Sel.Name] {
				what := "time." + sel.Sel.Name
				if !calls[sel] {
					what += " as a value"
				}
				out = append(out, wallClockWait{Rel: rel, Func: fn, What: what, Line: fset.Position(sel.Pos()).Line})
			}
			return true
		})
		for name, line := range deadlines {
			if waited[name] {
				out = append(out, wallClockWait{Rel: rel, Func: fn, What: "a context deadline waited on (<-" + name + ".Done())", Line: line})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].What < out[j].What
	})
	return out
}

// treeWallClockWaits is every direct wall-clock wait in the unit tier's test
// files, and how many files were read.
func treeWallClockWaits(t *testing.T) ([]wallClockWait, int) {
	t.Helper()
	tree := repoTree(t)
	var out []wallClockWait
	files := 0
	for _, f := range tree.GoFilesUnder(true, unitWaitDirs...) {
		if f.HasDirNamed("testdata") || f.AST == nil || !unitTierFile(f.Src) {
			continue
		}
		files++
		out = append(out, scanWallClockWaits(tree.FSet, f.Rel, f.AST)...)
	}
	return out, files
}

// treeSleepsSkips is every SLEEPS skip in cmd/ and internal/, keyed like the
// ledger, with the file it is in.
func treeSleepsSkips(t *testing.T) map[string]string {
	t.Helper()
	tree := map[string]string{}
	for _, f := range repoTree(t).GoFilesUnder(true, unitWaitDirs...) {
		if f.HasDirNamed("testdata") || f.AST == nil {
			continue
		}
		for _, name := range sleepsSkippers(f.AST) {
			tree[sleepsLedgerKey(path.Dir(f.Rel), name)] = f.Rel
		}
	}
	return tree
}

// readSleepsLedger reads the ledger at HEAD through the one reader.
func readSleepsLedger(t *testing.T) []slowtests.SleepRow {
	t.Helper()
	rows, err := slowtests.ParseSleeps(strings.NewReader(readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(sleepsLedger)))))
	if err != nil {
		t.Fatalf("%s: %v", sleepsLedger, err)
	}
	return rows
}

// unledgeredWaits are the waits whose key the ledger does not name.
func unledgeredWaits(waits []wallClockWait, rows []slowtests.SleepRow) []wallClockWait {
	ledger := map[string]bool{}
	for _, row := range rows {
		ledger[sleepsLedgerKey(row.Package, row.Test)] = true
	}
	var out []wallClockWait
	for _, w := range waits {
		if !ledger[w.Key()] {
			out = append(out, w)
		}
	}
	return out
}

// TestNoUnitTestWaitsOnTheWallClock: no unit-tier test file waits on the wall
// clock outside a function the SLEEPS ledger names. The remedy is an injected
// clock seam or `//go:build functional`; the ledger only shrinks, so a new
// row is not a remedy.
func TestNoUnitTestWaitsOnTheWallClock(t *testing.T) {
	t.Parallel()

	waits, files := treeWallClockWaits(t)
	if files == 0 {
		t.Fatal("read no unit-tier test file; the rule would pass by checking nothing")
	}
	left := unledgeredWaits(waits, readSleepsLedger(t))
	keys := map[string]bool{}
	for _, w := range waits {
		keys[w.Key()] = true
	}
	t.Logf("unit-tier test files=%d wall-clock waits=%d in functions=%d unledgered=%d (the waits the tree owes; SPEC-CI's ratchet row)", files, len(waits), len(keys), len(left))
	for _, w := range left {
		t.Errorf("%s:%d: %s in %s waits on the wall clock and %s has no row %s %s; inject a clock (internal/wake.Clock, an injected Sleep func) or tag the file //go:build functional (the ledger only shrinks)",
			w.Rel, w.Line, w.What, w.Func, sleepsLedger, path.Dir(w.Rel), w.Func)
	}
}

// TestSleepsLedgerIsTheTreesSleepsSkips: internal/ci/sleeps-skips_allowlist.txt
// names exactly the tree's SLEEPS skips (a top-level test whose body calls Skip
// with a string starting with the SLEEPS marker) and the functions that wait on
// the wall clock: a SLEEPS skip with no row is red, and so is a row that names
// neither (the wait was fixed: delete the row).
func TestSleepsLedgerIsTheTreesSleepsSkips(t *testing.T) {
	t.Parallel()

	rows := readSleepsLedger(t)
	ledger := map[string]bool{}
	for _, row := range rows {
		ledger[sleepsLedgerKey(row.Package, row.Test)] = true
	}
	skips := treeSleepsSkips(t)
	waits, _ := treeWallClockWaits(t)
	named := map[string]bool{}
	for key := range skips {
		named[key] = true
	}
	for _, w := range waits {
		named[w.Key()] = true
	}
	for key, rel := range skips {
		if !ledger[key] {
			t.Errorf("%s: %s skips with the SLEEPS marker but %s has no row for it; inject a clock or tag it //go:build functional (a SLEEPS skip off the ledger is red on every leg)",
				rel, strings.Replace(key, "\t", " ", 1), sleepsLedger)
		}
	}
	for _, row := range rows {
		if !named[sleepsLedgerKey(row.Package, row.Test)] {
			t.Errorf("%s lists %s %s, but it neither skips with the SLEEPS marker nor waits on the wall clock any more; delete the row", sleepsLedger, row.Package, row.Test)
		}
	}
}

// sleepsLedgerBase is the commit the ledger is compared against: the merge
// base of HEAD and origin/dev when that ref is in the checkout and the merge
// base is not HEAD itself (a branch on a developer's machine: the whole
// branch's additions, however many commits), else HEAD's first parent (a pull
// request's merge ref or a merge-queue commit, whose first parent is dev's
// tip, and a commit on dev itself) -- the comparison classtests makes.
func sleepsLedgerBase(root string) (string, error) {
	if _, err := gitOut(root, "rev-parse", "--verify", "-q", "refs/remotes/origin/dev^{commit}"); err == nil {
		mb, mbErr := gitOut(root, "merge-base", "HEAD", "refs/remotes/origin/dev")
		head, headErr := gitOut(root, "rev-parse", "HEAD")
		if mbErr == nil && headErr == nil && strings.TrimSpace(mb) != strings.TrimSpace(head) {
			return strings.TrimSpace(mb), nil
		}
	}
	return firstParent(root)
}

// sleepsLedgerGrowth compares the ledger at HEAD with the ledger at
// sleepsLedgerBase, read out of git through gitOut. It returns the keys HEAD
// adds, and seed true when the base has no ledger (the change that introduces
// it).
func sleepsLedgerGrowth(root string) (added []string, parent string, seed bool, err error) {
	parent, err = sleepsLedgerBase(root)
	if err != nil {
		return nil, "", false, err
	}
	head, err := gitOut(root, "show", "HEAD:"+sleepsLedger)
	if err != nil {
		return nil, parent, false, err
	}
	if _, err := gitOut(root, "cat-file", "-e", parent+":"+sleepsLedger); err != nil {
		return nil, parent, true, nil
	}
	base, err := gitOut(root, "show", parent+":"+sleepsLedger)
	if err != nil {
		return nil, parent, false, err
	}
	baseRows, err := slowtests.ParseSleeps(strings.NewReader(base))
	if err != nil {
		return nil, parent, false, fmt.Errorf("%s at %s: %w", sleepsLedger, parent[:9], err)
	}
	headRows, err := slowtests.ParseSleeps(strings.NewReader(head))
	if err != nil {
		return nil, parent, false, fmt.Errorf("%s at HEAD: %w", sleepsLedger, err)
	}
	had := map[string]bool{}
	for _, row := range baseRows {
		had[sleepsLedgerKey(row.Package, row.Test)] = true
	}
	for _, row := range headRows {
		if key := sleepsLedgerKey(row.Package, row.Test); !had[key] {
			added = append(added, key)
		}
	}
	sort.Strings(added)
	return added, parent, false, nil
}

// TestSleepsLedgerOnlyShrinksAgainstTheMergeParent: the SLEEPS ledger at HEAD
// adds no row the ledger at its merge base lacks (sleepsLedgerBase: HEAD's
// first parent in CI, where that is dev's tip; the merge base with origin/dev
// on a branch). A change that adds a SLEEPS skip or a wall-clock wait AND its
// ledger row in the same diff is red here (the #4413 dodge). A base with no
// ledger is the seed.
func TestSleepsLedgerOnlyShrinksAgainstTheMergeParent(t *testing.T) {
	t.Parallel()

	added, parent, seed, err := sleepsLedgerGrowth(repoTree(t).Root)
	if err != nil {
		t.Fatal(err)
	}
	if seed {
		t.Logf("%s is not in the merge base %s: this change is the ledger's seed", sleepsLedger, parent[:9])
		return
	}
	for _, key := range added {
		t.Errorf("%s adds the row %s, which its merge base %s does not have; the ledger only shrinks: inject a clock or tag the test //go:build functional, and drop the row",
			sleepsLedger, strings.Replace(key, "\t", " ", 1), parent[:9])
	}
}

// PROBE 2 of the #4413 ruling, at the detector: a _test.go with a bare
// time.Sleep is found; the same wait through a fake clock seam is not; the
// same bare sleep behind `//go:build functional` is not a unit file; `<-
// time.After` in a select, time.NewTimer, time.Sleep handed to a seam and a
// context deadline waited on are found; a context deadline nobody waits on is
// not. A found wait is green only when the ledger names its function.
func TestWallClockWaitDetectorFindsTheWaitsAndNotTheSeam(t *testing.T) {
	t.Parallel()

	const bare = `package p

import (
	"context"
	"testing"
	"time"
)

func TestBareSleep(t *testing.T) { time.Sleep(10 * time.Millisecond) }

func TestSelectAfter(t *testing.T) {
	ch := make(chan int)
	select {
	case <-ch:
	case <-time.After(time.Minute):
	}
}

func TestTimer(t *testing.T) { _ = time.NewTimer(time.Minute) }

type opts struct{ Sleep func(time.Duration) }

func TestRealClockIntoASeam(t *testing.T) { _ = opts{Sleep: time.Sleep} }

func TestDeadlineWaited(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	<-ctx.Done()
}

func TestDeadlineNotWaited(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_ = ctx
}
`
	const seam = `package p

import (
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

func TestThroughTheSeam(t *testing.T) {
	c := &fakeClock{}
	c.Sleep(10 * time.Second)
}
`
	scan := func(rel, src string) []wallClockWait {
		t.Helper()
		fset, f, err := parseSourceFile(rel, []byte(src), 0)
		if err != nil {
			t.Fatal(err)
		}
		if !unitTierFile([]byte(src)) {
			return nil
		}
		return scanWallClockWaits(fset, rel, f)
	}
	var got []string
	for _, w := range scan("cmd/p/p_test.go", bare) {
		got = append(got, w.Func+": "+w.What)
	}
	want := []string{
		"TestBareSleep: time.Sleep",
		"TestSelectAfter: time.After",
		"TestTimer: time.NewTimer",
		"TestRealClockIntoASeam: time.Sleep as a value",
		"TestDeadlineWaited: a context deadline waited on (<-ctx.Done())",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("waits in the bare file:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if w := scan("cmd/p/seam_test.go", seam); len(w) != 0 {
		t.Errorf("the same wait through a fake clock seam: %+v, want none", w)
	}
	if w := scan("cmd/p/p_functional_test.go", "//go:build functional\n\n"+bare); len(w) != 0 {
		t.Errorf("a functional-tagged file: %+v, want none (not a unit file)", w)
	}
	for src, unit := range map[string]bool{
		"package p\n": true, "//go:build functional\n\npackage p\n": false, "//go:build slow\n\npackage p\n": false,
		"//go:build !functional\n\npackage p\n": true, "//go:build darwin\n\npackage p\n": true, "//go:build windows\n\npackage p\n": false,
		"//go:build unix && functional\n\npackage p\n": false, "//go:build !race\n\npackage p\n": true, "//go:build !unix\n\npackage p\n": false,
	} {
		if got := unitTierFile([]byte(src)); got != unit {
			t.Errorf("unitTierFile(%q) = %v, want %v", src, got, unit)
		}
	}

	waits := scan("cmd/p/p_test.go", bare)
	ledgered, err := slowtests.ParseSleeps(strings.NewReader("cmd/p\tTestBareSleep\tseed\n"))
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, w := range unledgeredWaits(waits, ledgered) {
		left = append(left, w.Func)
	}
	if strings.Join(left, ",") != "TestSelectAfter,TestTimer,TestRealClockIntoASeam,TestDeadlineWaited" {
		t.Errorf("unledgered with TestBareSleep on the ledger: %v; want every wait but TestBareSleep", left)
	}
}

// PROBES 2 and 3 of the #4413 ruling, at the merge base, over a repository
// this test builds: a ledger row the first parent has is green; a change that
// adds a SLEEPS skip and its row in the same diff is red, naming the row; a
// change that deletes a row is green (the ledger shrinks); a first parent with
// no ledger is the seed; on a branch past origin/dev the base is the merge
// base, so a row added two commits back is still red.
func TestSleepsLedgerGrowthIsReadOutOfGit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if _, err := gitOut(root, append([]string{
			"-c", "user.name=ci", "-c", "user.email=ci@example.invalid",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	growth := func() ([]string, bool) {
		t.Helper()
		added, _, seed, err := sleepsLedgerGrowth(root)
		if err != nil {
			t.Fatal(err)
		}
		return added, seed
	}

	git("init", "-q")
	write("README", "base\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	write(sleepsLedger, "cmd/a\tTestOld\tseed\n")
	write("cmd/a/a_test.go", "package a\n")
	git("add", "-A")
	git("commit", "-q", "-m", "seed")
	if added, seed := growth(); !seed || len(added) != 0 {
		t.Fatalf("a parent with no ledger: added %v seed %v; want the seed", added, seed)
	}

	// PROBE 2: a ledgered wait whose row the parent has is green.
	write("cmd/a/a_test.go", "package a\n\n// touched\n")
	git("add", "-A")
	git("commit", "-q", "-m", "touch")
	if added, seed := growth(); seed || len(added) != 0 {
		t.Fatalf("the row at the parent: added %v seed %v; want nothing added", added, seed)
	}

	// PROBE 3: the skip and its row in the same diff is red.
	write("cmd/a/new_test.go", "package a\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) { t.Skip(\"SLEEPS: waits\") }\n")
	write(sleepsLedger, "cmd/a\tTestOld\tseed\ncmd/a\tTestNew\tadded with its skip\n")
	git("add", "-A")
	git("commit", "-q", "-m", "a skip and its row")
	if added, _ := growth(); len(added) != 1 || added[0] != "cmd/a\tTestNew" {
		t.Fatalf("a skip and its row together: added %q; want [cmd/a\\tTestNew]", added)
	}

	// A deleted row is the ledger shrinking.
	write(sleepsLedger, "cmd/a\tTestNew\tadded with its skip\n")
	git("add", "-A")
	git("commit", "-q", "-m", "TestOld fixed")
	if added, _ := growth(); len(added) != 0 {
		t.Fatalf("a deleted row: added %q; want none", added)
	}

	// On a branch (origin/dev in the checkout, HEAD past it) the base is the
	// merge base, not the first parent: a row added two commits back is still
	// the branch's addition.
	git("update-ref", "refs/remotes/origin/dev", "HEAD")
	write("cmd/a/later_test.go", "package a\n\nimport \"testing\"\n\nfunc TestLater(t *testing.T) { t.Skip(\"SLEEPS: waits\") }\n")
	write(sleepsLedger, "cmd/a\tTestNew\tadded with its skip\ncmd/a\tTestLater\tadded on the branch\n")
	git("add", "-A")
	git("commit", "-q", "-m", "branch: a skip and its row")
	write("cmd/a/a_test.go", "package a\n\n// touched again\n")
	git("add", "-A")
	git("commit", "-q", "-m", "branch: an unrelated commit on top")
	if added, _ := growth(); len(added) != 1 || added[0] != "cmd/a\tTestLater" {
		t.Fatalf("a row added two commits back on a branch: added %q; want [cmd/a\\tTestLater] against the merge base", added)
	}
}
