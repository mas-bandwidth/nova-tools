package cardcontract

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

// THE READ'S GATE (docs/SPEC-CARD-CONTRACT.md, JOB.md). A read judges a diff, and the tests
// it runs are the diff's: the packages it touches, and the packages whose tests read a doc it
// changes (nova-tools#5111). Never ./... and never a class-test package whole: on a 36-thread bench,
// 2026-10-02, sixteen reads each ran `go test ./internal/ci/`, whose tests fork thousands of
// processes and build every command, and the machine spent 85% of its CPU in the kernel. The
// work's own gate ran those tests, and the pull request's CI runs the class-test packages
// (pkgselect.EveryRun) on every change, so a read runs of them only the tests its diff
// reaches: those in a test file it changes, and those in a test file that reads a doc it
// changes.

// GateRun is a package a read runs only some tests of: one CI runs whole on every change.
type GateRun struct {
	Pkg   string   // ./dir
	Tests []string // the Test functions it runs, sorted
}

// Gate is a read's gate: the packages it vets and tests whole, the class-test packages it
// runs some tests of, and whether go.mod or go.sum changed (the whole module is built).
type Gate struct {
	Packages []string // ./dir, sorted
	Runs     []GateRun
	Module   bool
}

// testFuncRE is a Test function declared in a _test.go file.
var testFuncRE = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)

// gateSkipDir is a directory that holds no package of the module: testdata, vendor, and a
// name Go ignores (a leading . or _).
func gateSkipDir(name string) bool {
	return name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// ReadGate is the gate of a read whose diff changed these files (repository-relative, slash
// separated) in the checkout at repo. A checkout with no go.mod has none (nil): the card's
// gate stands.
func ReadGate(repo string, changed []string) (*Gate, error) {
	if _, err := os.Stat(filepath.Join(repo, "go.mod")); err != nil {
		return nil, nil
	}
	everyRun := map[string]bool{}
	for _, p := range pkgselect.EveryRun {
		everyRun[strings.TrimPrefix(p, "./")] = true
	}
	g := &Gate{}
	whole := map[string]bool{}
	some := map[string]map[string]bool{}
	// add puts the package at dir in the gate: some of its tests (the Test functions of the
	// test file b) when CI runs it on every change, else whole.
	add := func(dir string, b []byte) {
		if !everyRun[dir] {
			whole[dir] = true
			return
		}
		if some[dir] == nil {
			some[dir] = map[string]bool{}
		}
		for _, m := range testFuncRE.FindAllSubmatch(b, -1) {
			some[dir][string(m[1])] = true
		}
	}
	var docs []string
	for _, f := range changed {
		dir := path.Dir(f)
		switch {
		case f == "go.mod" || f == "go.sum":
			g.Module = true
		case strings.Contains("/"+f, "/testdata/"):
			// a package's testdata is read by its tests
			if d, _, ok := strings.Cut("/"+f, "/testdata/"); ok && holdsGo(repo, strings.TrimPrefix(d, "/")) {
				add(strings.TrimPrefix(d, "/"), nil)
			}
		case strings.HasSuffix(f, ".go") && holdsGo(repo, dir):
			if everyRun[dir] && !strings.HasSuffix(f, "_test.go") {
				continue // its own code: the pull request's CI runs it whole
			}
			b, _ := os.ReadFile(filepath.Join(repo, filepath.FromSlash(f)))
			add(dir, b)
		case strings.HasPrefix(f, "docs/") && strings.HasSuffix(f, ".md"):
			docs = append(docs, f)
		}
	}
	if len(docs) > 0 {
		err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if p != repo && gateSkipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for _, doc := range docs {
				if readsPath(b, doc) {
					rel, _ := filepath.Rel(repo, filepath.Dir(p))
					add(filepath.ToSlash(rel), b)
					break
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("reading the checkout's tests for the docs %s: %w", strings.Join(docs, ", "), err)
		}
	}
	for dir := range whole {
		g.Packages = append(g.Packages, pkgArg(dir))
	}
	slices.Sort(g.Packages)
	for dir, tests := range some {
		if len(tests) > 0 {
			g.Runs = append(g.Runs, GateRun{Pkg: pkgArg(dir), Tests: slices.Sorted(maps.Keys(tests))})
		}
	}
	slices.SortFunc(g.Runs, func(a, b GateRun) int { return strings.Compare(a.Pkg, b.Pkg) })
	return g, nil
}

// readsPath reports whether a test file's text names the doc as a path it reads: the path in
// a string ending at it ("docs/<name>.md", "../../docs/<name>.md"), or its elements as filepath.Join's
// arguments ("docs", "<name>.md"). A path in prose (a comment, a quoted sentence) reads nothing.
func readsPath(b []byte, doc string) bool {
	s := string(b)
	if strings.Contains(s, `"`+doc+`"`) || strings.Contains(s, `/`+doc+`"`) {
		return true
	}
	return strings.Contains(s, `"`+strings.Join(strings.Split(doc, "/"), `", "`)+`"`)
}

// holdsGo reports whether the directory at dir under repo holds a .go file: a package, and
// not one the diff deleted whole.
func holdsGo(repo, dir string) bool {
	for _, el := range strings.Split(dir, "/") {
		if gateSkipDir(el) && el != "." {
			return false
		}
	}
	entries, err := os.ReadDir(filepath.Join(repo, filepath.FromSlash(dir)))
	if err != nil {
		return false
	}
	return slices.ContainsFunc(entries, func(e os.DirEntry) bool { return !e.IsDir() && strings.HasSuffix(e.Name(), ".go") })
}

// pkgArg is the package at dir as go's argument.
func pkgArg(dir string) string {
	if dir == "." {
		return "."
	}
	return "./" + dir
}

// writeReadGate is what a read's JOB.md says it runs: its gate, as commands, in place of the
// card's. Nothing when it has none.
func writeReadGate(b *strings.Builder, s Staged) {
	g := s.Gate
	if g == nil {
		return
	}
	var lines []string
	if len(g.Packages) > 0 {
		pkgs := strings.Join(g.Packages, " ")
		lines = append(lines, "nice -n 19 go vet "+pkgs, "nice -n 19 go test -count=1 -timeout 600s "+pkgs)
	}
	for _, r := range g.Runs {
		lines = append(lines, fmt.Sprintf("nice -n 19 go test -count=1 -timeout 600s -run '^(%s)$' %s", strings.Join(r.Tests, "|"), r.Pkg))
	}
	if g.Module {
		lines = append(lines, "nice -n 19 go build ./...")
	}
	b.WriteString("This is the read's gate, in place of the card's and of any rule that asks for more: the packages the change touches and the tests that read a doc it changes. Never run ./... or a package whole that is not on it: the work's own gate ran those, and the pull request's CI runs " +
		strings.Join(pkgselect.EveryRun, " and ") + " on every change.")
	if len(lines) == 0 {
		b.WriteString(" This change touches nothing a read tests, so the read runs no go command: judge the diff.\n\n")
		return
	}
	b.WriteString("\n\n")
	for _, l := range lines {
		b.WriteString("    " + l + "\n")
	}
	b.WriteString("\n")
}

// gateWords is what a read's JOB.md calls the gate to run: its own, else the card's.
func gateWords(s Staged) string {
	if s.Gate != nil {
		return "the read's gate (below)"
	}
	return "the card's gate"
}
