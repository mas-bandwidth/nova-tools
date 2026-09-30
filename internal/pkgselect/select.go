package pkgselect

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Options is one selection: what to select against and what a failed `go list`
// does.
type Options struct {
	// Root is the repository root: the directory every command runs in and
	// where deprecated/PACKAGES and the tracked files are read.
	Root string
	// All selects the whole tree. Otherwise Base is the commit the change is
	// diffed against (the event's own base: pull_request.base.sha or
	// merge_group.base_sha).
	All  bool
	Base string
	// WholeTreeOnError makes a failed `go list` select the whole tree, read
	// from the tracked files, with a warning (the landing must not stall on one
	// runner's cache). Otherwise a failed `go list` is a *ListError, cheap and
	// fast: the runner is broken, re-run it.
	WholeTreeOnError bool
}

// Outcome is a selection: the packages as ./<dir>, in `go list` order, and the
// warning to print on stderr when the whole tree stood in for a failed
// `go list`.
type Outcome struct {
	Packages []string
	Warning  string
}

// ListError is a selection that failed rather than select nothing. Text is what
// to print on stderr.
type ListError struct{ Text string }

func (e *ListError) Error() string { return strings.TrimRight(e.Text, "\n") }

// Select prints the ./cmd, ./internal and ./tools Go packages a change touches,
// plus every in-repo package that imports one of them, or with All the whole
// tree. The diff is read against Options.Base. A go.mod or go.sum change puts
// every package in scope. Deprecated packages are never selected (Live).
//
// NEVER SILENTLY NOTHING. On PR #4370's final head the shards reported
// `test (nothing)` because `go list` failed on a runner (a shared GOCACHE race:
// "open .../go-build/...: no such file or directory") and the selection printed
// an empty list, so the caller said "0 package(s) touched: none" and a green run
// tested nothing. Every `go list` here goes through goList: a non-zero exit, or
// "cannot" or "no such file" on its stderr, is a failure, and so is a selection
// of zero packages from a diff that touches Go files. What a failure does is the
// caller's choice (Options.WholeTreeOnError). internal/ci's
// TestSelectPackagesNeverSilentlySelectsNothing holds both.
func Select(run Runner, o Options) (Outcome, error) {
	dep, err := LoadDeprecated(o.Root)
	if err != nil {
		return Outcome{}, err
	}
	s := &selector{run: run, o: o, dep: dep, mod: modulePrefix(o.Root)}
	if o.All {
		return s.listAll()
	}
	return s.selectChange()
}

type selector struct {
	run Runner
	o   Options
	dep *Deprecated
	mod string // the module's import-path prefix, with its trailing slash
}

var listFailureRe = regexp.MustCompile(`cannot|no such file`)

// goList runs `go list args...` and returns its output lines, or the text that
// says why it cannot be trusted.
func (s *selector) goList(args ...string) ([]string, string, error) {
	res, err := s.run(s.o.Root, nil, append([]string{"go", "list"}, args...)...)
	if err != nil {
		return nil, "", err
	}
	errText := res.Stderr
	if res.Code != 0 || listFailureRe.MatchString(errText) {
		if strings.TrimSpace(errText) == "" {
			errText = fmt.Sprintf("go list exited %d with nothing on stderr\n", res.Code)
		}
		return nil, errText, nil
	}
	out := strings.TrimRight(res.Stdout, "\n")
	if out == "" {
		return nil, "go list exited 0 and listed no packages\n", nil
	}
	return strings.Split(out, "\n"), "", nil
}

// failed ends a selection that could not be trusted: the error, or the whole
// tree with the warning.
func (s *selector) failed(errText string) (Outcome, error) {
	first := ""
	for _, l := range strings.Split(errText, "\n") {
		if l != "" {
			first = l
			break
		}
	}
	if s.o.WholeTreeOnError {
		tree, err := s.treeFromFiles()
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{Packages: tree, Warning: fmt.Sprintf("WARN select-packages: go list failed (%s); testing the whole tree", first)}, nil
	}
	if !strings.HasSuffix(errText, "\n") {
		errText += "\n"
	}
	return Outcome{}, &ListError{Text: "ERROR select-packages: go list failed; failing the job rather than testing nothing (re-run on a healthy runner):\n" + errText}
}

// dotted returns the package list as ./<dir>.
func (s *selector) dotted(importPaths []string) []string {
	out := make([]string, 0, len(importPaths))
	for _, l := range importPaths {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		out = append(out, "./"+strings.TrimPrefix(l, s.mod))
	}
	return out
}

var treeRoots = []string{"./cmd/...", "./internal/...", "./tools/..."}

func (s *selector) listAll() (Outcome, error) {
	pkgs, why, err := s.goList(treeRoots...)
	if err != nil {
		return Outcome{}, err
	}
	if why != "" {
		return s.failed(why)
	}
	return Outcome{Packages: s.dep.Live(s.dotted(pkgs))}, nil
}

var treeExcludedDir = regexp.MustCompile(`(^|/)(testdata|vendor|[_.][^/]*)/`)

// treeFromFiles is the whole tree without `go list`: every directory under
// cmd/, internal/ and tools/ holding a tracked .go file with no //go:build
// line, less testdata, vendor and _ or . directories. It equals
// `go list ./cmd/... ./internal/... ./tools/...` package for package.
func (s *selector) treeFromFiles() ([]string, error) {
	res, err := s.run(s.o.Root, nil, "git", "ls-files", "-z", "--", "cmd/*.go", "internal/*.go", "tools/*.go")
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, &ListError{Text: fmt.Sprintf("select-packages: git ls-files exited %d: %s\n", res.Code, strings.TrimSpace(res.Stderr))}
	}
	dirs := map[string]bool{}
	for _, f := range strings.Split(res.Stdout, "\x00") {
		if f == "" || treeExcludedDir.MatchString(f) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.o.Root, filepath.FromSlash(f)))
		if err != nil || hasBuildLine(b) {
			continue
		}
		dirs["./"+path.Dir(f)] = true
	}
	out := make([]string, 0, len(dirs))
	for d := range dirs {
		out = append(out, d)
	}
	sort.Strings(out)
	return s.dep.Live(out), nil
}

// hasBuildLine reports whether any line of the file starts with //go:build.
func hasBuildLine(b []byte) bool {
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "//go:build") {
			return true
		}
	}
	return false
}

var goModFileRe = regexp.MustCompile(`^(go\.mod|go\.sum)$`)

func (s *selector) selectChange() (Outcome, error) {
	if strings.TrimSpace(s.o.Base) == "" {
		return Outcome{}, fmt.Errorf("select-packages: no base commit and no --all")
	}
	// The base may not be in a shallow clone; a failed fetch is not an error:
	// the diff below says whether it is there.
	if _, err := s.run(s.o.Root, nil, "git", "fetch", "-q", "--depth=1", "origin", s.o.Base); err != nil {
		return Outcome{}, err
	}
	diff, err := s.run(s.o.Root, nil, "git", "diff", "--name-only", s.o.Base, "HEAD")
	if err != nil {
		return Outcome{}, err
	}
	if diff.Code != 0 {
		return Outcome{}, &ListError{Text: fmt.Sprintf("select-packages: git diff %s HEAD exited %d: %s\n", s.o.Base, diff.Code, strings.TrimSpace(diff.Stderr))}
	}
	changed := lines(diff.Stdout)
	// A go.mod or go.sum change can move any package, so it puts the whole tree
	// in scope.
	for _, f := range changed {
		if goModFileRe.MatchString(f) {
			return s.listAll()
		}
	}

	allOut, why, err := s.goList(treeRoots...)
	if err != nil {
		return Outcome{}, err
	}
	if why != "" {
		return s.failed(why)
	}
	all := s.dotted(allOut)

	// The changed .go files name the directories that moved; their import paths
	// are the `want` set.
	want := map[string]bool{}
	var goChanged []string
	for _, f := range changed {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		want["./"+path.Dir(f)] = true
		if hasRootDir(f) {
			goChanged = append(goChanged, f)
		}
	}

	// dependents: every package in the tree that imports a changed one.
	depsOut, why, err := s.goList("-f", "{{.ImportPath}}{{range .Deps}} {{.}}{{end}}", "./cmd/...", "./internal/...", "./tools/...")
	if err != nil {
		return Outcome{}, err
	}
	if why != "" {
		return s.failed(why)
	}
	for _, line := range depsOut {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		p := "./" + strings.TrimPrefix(f[0], s.mod)
		for _, dep := range f[1:] {
			if !strings.HasPrefix(dep, s.mod) {
				continue
			}
			if want["./"+strings.TrimPrefix(dep, s.mod)] {
				want[p] = true
				break
			}
		}
	}

	// THE TWO CLASS-TEST PACKAGES ARE ADDED AFTER THE DEPENDENTS, not before.
	// Added before, they dragged their own importers into every PR's shards
	// although the change touched none of them. When a change does touch
	// internal/ci or internal/docs, the diff names them above and the loop above
	// still selects their importers, so no change loses a dependent.
	//
	// internal/ci holds the class tests that read the workflow and script files
	// as text; it scans the tree rather than importing what it guards, so no *.go
	// diff can name it as a dependent. Select it on every run, not only when the
	// diff touches .github/: a change that can move the workflow's law always
	// pays for the package that holds it.
	want["./internal/ci"] = true
	// internal/docs holds the front page's contract: AGENTS.md must name every
	// class rule indexed in docs/SPEC-CI.md. It, too, reads the tree as text
	// instead of importing what it guards, so a docs-only change can break its
	// class test without naming a dependent in the import graph. Select it on
	// every run for the same reason.
	want["./internal/docs"] = true

	var selected []string
	for _, p := range all {
		if want[p] {
			selected = append(selected, p)
		}
	}
	selected = s.dep.Live(selected)

	// ZERO PACKAGES FROM A GO DIFF IS AN ERROR, not "nothing to test": a change
	// that moved a .go file under cmd/, internal/ or tools/ and selected nothing
	// means the selection broke, the #4370 shape.
	if len(selected) == 0 && len(goChanged) > 0 {
		return s.failed(fmt.Sprintf("select-packages: the diff touches Go files (%s) but selected zero packages\n", goChanged[0]))
	}
	return Outcome{Packages: selected}, nil
}

// hasRootDir reports whether the file is under cmd/, internal/ or tools/.
func hasRootDir(f string) bool {
	for _, r := range []string{"cmd/", "internal/", "tools/"} {
		if strings.HasPrefix(f, r) {
			return true
		}
	}
	return false
}

// modulePrefix is the module path of root with its trailing slash, or "" when
// root names no module.
func modulePrefix(root string) string {
	if m := ModulePath(root); m != "" {
		return m + "/"
	}
	return ""
}
