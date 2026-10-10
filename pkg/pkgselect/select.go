package pkgselect

import (
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Options is one selection: what to select against and what a failed `go list`
// does.
type Options struct {
	// Root is the repository root: the directory every command runs in and
	// where DeprecatedFile and the tracked files are read.
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
// every package in scope. A changed file that is not Go selects the packages
// whose tests or testdata name it or whose source embeds it (keyedPackages).
// Deprecated packages are never selected (Live).
//
// NEVER SILENTLY NOTHING. A `go list` that fails on a runner must not read as
// "nothing to test": the selection would print an empty list, the caller would
// report no packages touched, and a green run would test nothing. Every
// `go list` here goes through goList: a non-zero exit, or "cannot" or "no such
// file" on its stderr, is a failure, and so is a selection of zero packages from
// a diff that touches Go files. What a failure does is the caller's choice
// (Options.WholeTreeOnError). internal/ci's
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
	run    Runner
	o      Options
	dep    *Deprecated
	mod    string // the module's import-path prefix, with its trailing slash
	dirErr error  // set by dotted when a directory is outside packageDirClass
}

// packageDirClass is the charset a selected directory may have. The package
// list is interpolated into a shell by make test PKGS=, so a directory outside
// this class is not a package: the selection fails with a *ListError that
// names it, the same way a local path that cannot be listed fails.
// docs/SPEC-CI.md (`selection`) is the design of that list.
// TestWholeTreeRefusesAPackageDirectoryWithShellSyntax holds the boundary.
const packageDirClass = `^[A-Za-z0-9._~/+-]+$`

var packageDirRe = regexp.MustCompile(packageDirClass)

// refusePackageDir returns a *ListError naming dir when dir is outside
// packageDirClass, and nil when the directory may be selected.
func refusePackageDir(dir string) error {
	if packageDirRe.MatchString(dir) {
		return nil
	}
	return &ListError{Text: fmt.Sprintf("select-packages: refusing directory %q; rename it to match %s\n", dir, packageDirClass)}
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

// dotted returns the package list as ./<dir>. A directory outside
// packageDirClass is not a package: dirErr is a *ListError naming it and the
// list is nil. listAll still returns s.dep.Live(s.dotted(pkgs)), the text
// internal/ci pins, and reads dirErr before that return.
func (s *selector) dotted(importPaths []string) []string {
	s.dirErr = nil
	out := make([]string, 0, len(importPaths))
	for _, l := range importPaths {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		dir := "./" + strings.TrimPrefix(l, s.mod)
		if err := refusePackageDir(dir); err != nil {
			s.dirErr = err
			return nil
		}
		out = append(out, dir)
	}
	return out
}

var treeRoots = []string{"./cmd/...", "./internal/...", "./pkg/...", "./tools/..."}

func (s *selector) listAll() (Outcome, error) {
	pkgs, why, err := s.goList(treeRoots...)
	if err != nil {
		return Outcome{}, err
	}
	if why != "" {
		return s.failed(why)
	}
	s.dotted(pkgs)
	if s.dirErr != nil {
		return Outcome{}, s.dirErr
	}
	return Outcome{Packages: s.dep.Live(s.dotted(pkgs))}, nil
}

var treeExcludedDir = regexp.MustCompile(`(^|/)(testdata|vendor|[_.][^/]*)/`)

// treeFromFiles is the whole tree without `go list`: every directory under
// cmd/, internal/ and tools/ holding a tracked .go file with no //go:build
// line, less testdata, vendor and _ or . directories. It equals
// `go list ./cmd/... ./internal/... ./pkg/... ./tools/...` package for package.
func (s *selector) treeFromFiles() ([]string, error) {
	res, err := s.run(s.o.Root, nil, "git", "ls-files", "-z", "--", "cmd/*.go", "internal/*.go", "pkg/*.go", "tools/*.go")
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
		dir := "./" + path.Dir(f)
		if err := refusePackageDir(dir); err != nil {
			return nil, err
		}
		dirs[dir] = true
	}
	out := slices.Sorted(maps.Keys(dirs))
	return s.dep.Live(out), nil
}

// hasBuildLine reports whether any line of the file starts with //go:build.
func hasBuildLine(b []byte) bool {
	return slices.ContainsFunc(strings.Split(string(b), "\n"), func(l string) bool { return strings.HasPrefix(l, "//go:build") })
}

var goModFileRe = regexp.MustCompile(`^(go\.mod|go\.sum)$`)

// ensureBase makes the base commit present. It fetches only when the commit is
// missing, and passes --depth=1 only when the clone is already shallow: a
// fetch with --depth shallows the clone it runs in, and `nova-ci local` runs
// this in a developer's own clone. A failed fetch is not an error: the diff
// that follows says whether the base is there.
func (s *selector) ensureBase() error {
	have, err := s.run(s.o.Root, nil, "git", "cat-file", "-e", s.o.Base+"^{commit}")
	if err != nil {
		return err
	}
	if have.Code == 0 {
		return nil
	}
	argv := []string{"git", "fetch", "-q"}
	shallow, err := s.run(s.o.Root, nil, "git", "rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}
	if shallow.Code == 0 && strings.TrimSpace(shallow.Stdout) == "true" {
		argv = append(argv, "--depth=1")
	}
	_, err = s.run(s.o.Root, nil, append(argv, "origin", s.o.Base)...)
	return err
}

func (s *selector) selectChange() (Outcome, error) {
	if strings.TrimSpace(s.o.Base) == "" {
		return Outcome{}, fmt.Errorf("select-packages: no base commit and no --all")
	}
	if err := s.ensureBase(); err != nil {
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
	if s.dirErr != nil {
		return Outcome{}, s.dirErr
	}

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
	depsOut, why, err := s.goList("-f", "{{.ImportPath}}{{range .Deps}} {{.}}{{end}}", "./cmd/...", "./internal/...", "./pkg/...", "./tools/...")
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

	// A changed file that is not Go reaches the packages whose tests read it
	// (nova-tools#5111), which no import edge says.
	keyed, err := s.keyedPackages(changed)
	if err != nil {
		return Outcome{}, err
	}
	for p := range keyed {
		want[p] = true
	}

	var selected []string
	for _, p := range all {
		if want[p] {
			selected = append(selected, p)
		}
	}
	selected = s.dep.Live(selected)

	// ZERO PACKAGES FROM A GO DIFF IS AN ERROR, not "nothing to test": a change
	// that moved a .go file under cmd/, internal/ or tools/ and selected nothing
	// means the selection broke.
	if len(selected) == 0 && len(goChanged) > 0 {
		return s.failed(fmt.Sprintf("select-packages: the diff touches Go files (%s) but selected zero packages\n", goChanged[0]))
	}
	return Outcome{Packages: selected}, nil
}

// EveryRun are the class-test packages selectChange adds to every selection, whatever the
// change (the two `want` lines above, and why): the pull request's CI runs them whole on every
// change, so a read runs of them only the tests its diff reaches (pkg/cardcontract, the
// read's gate). TestSelectChangeIsTheTouchedPackagesTheirDependentsAndTheClassTestPackages
// holds the two equal.
var EveryRun = []string{"./internal/ci", "./internal/docs"}

// hasRootDir reports whether the file is under cmd/, internal/ or tools/.
func hasRootDir(f string) bool {
	return slices.ContainsFunc([]string{"cmd/", "internal/", "pkg/", "tools/"}, func(r string) bool { return strings.HasPrefix(f, r) })
}

// modulePrefix is the module path of root with its trailing slash, or "" when
// root names no module.
func modulePrefix(root string) string {
	if m := ModulePath(root); m != "" {
		return m + "/"
	}
	return ""
}
