package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

// Deprecated tools and modules are never tested (Glenn 2026-09-27: "Tests do
// not run for deprecated tools and modules. ... We don't run their tests. We
// don't stop builds for them. We don't bog down CI for them."). Every place
// that chooses the packages a run tests reads its list through
// pkgselect.Deprecated.Live, which drops what deprecated/PACKAGES names:
// pkgselect.Select (the change's selection and the whole tree, and the tree read
// from the tracked files), pkgselect.LiveTree (the hosted deal and the race
// dependency build) and the perf-test finder.

// livePackages is the deprecated filter's answer for the listed packages, read
// from the repository's own deprecated/PACKAGES.
func livePackages(t *testing.T, root, in string) []string {
	t.Helper()
	dep, err := pkgselect.LoadDeprecated(root)
	if err != nil {
		t.Fatal(err)
	}
	return dep.Live(strings.Fields(in))
}

func TestDeprecatedPackagesAreNeverSelected(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	mod := pkgselect.ModulePath(root) + "/"
	in := strings.Join([]string{
		"./internal/nsprint/ws",
		mod + "internal/nsprint/land/stream",
		"./internal/nsprint",
		"./internal/nsprint/store",
		mod + "internal/nsprint/verbflag",
		"./internal/nsprintx",
		"./cmd/nova-table",
		mod + "internal/ntable",
		"./cmd/nova-bus",
	}, "\n") + "\n"
	kept := livePackages(t, root, in)
	got := strings.Join(kept, " ")
	// The class tests read the same list in Go (liveTree): it gives the
	// filter's answer for every package of the fixture.
	lt, keptSet := loadLiveTree(t, root), map[string]bool{}
	for _, p := range kept {
		keptSet[p] = true
	}
	for _, p := range strings.Fields(in) {
		if live := lt.Package(strings.TrimPrefix(p, mod)); live != keptSet[p] {
			t.Errorf("liveTree says %s live=%v, the deprecated filter says %v; the two readings of deprecated/PACKAGES disagree", p, live, keptSet[p])
		}
	}
	want := strings.Join([]string{
		"./internal/nsprint/store",
		mod + "internal/nsprint/verbflag",
		"./internal/nsprintx",
		"./cmd/nova-table",
		mod + "internal/ntable",
		"./cmd/nova-bus",
	}, " ")
	if got != want {
		t.Errorf("the deprecated filter kept\n  %s\nwant\n  %s\n(a path in deprecated/PACKAGES drops that package and everything under it; a keep line keeps one; a name that only starts the same is another package)", got, want)
	}

	// every selection point reads its list through the filter
	sel := readFile(t, filepath.Join(root, "internal", "pkgselect", "select.go"))
	for _, want := range []string{
		"return Outcome{Packages: s.dep.Live(s.dotted(pkgs))}, nil", // the whole tree
		"selected = s.dep.Live(selected)",                           // the change's selection
		"return s.dep.Live(out), nil",                               // the whole tree read from tracked files
	} {
		if !strings.Contains(sel, want) {
			t.Errorf("internal/pkgselect/select.go lacks %q: a list that skips the filter would test deprecated packages", want)
		}
	}
	extras := readFile(t, filepath.Join(root, "internal", "pkgselect", "extras.go"))
	if !strings.Contains(extras, "return dep.Live(lines(res.Stdout)), nil") || !strings.Contains(extras, "!dep.LivePackage(pkg)") {
		t.Errorf("internal/pkgselect/extras.go's live tree or perf finder lists packages without the deprecated filter")
	}
	ci := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	if !strings.Contains(ci, ciRunner + " deal --shards") {
		t.Errorf("ci.yml's hosted deal does not go through `ci deal`, which lists the live tree, so its shards would test deprecated packages")
	}
	if verb := readFile(t, filepath.Join(root, "tools", "ci", "sel_deal.go")); !strings.Contains(verb, "pkgselect.LiveTree(") {
		t.Errorf("tools/ci/sel_deal.go lists the tree without pkgselect.LiveTree, so its shards would test deprecated packages")
	}
}

// Every line of deprecated/PACKAGES names a directory that is in the tree: a
// path that moved under deprecated/ or was deleted leaves the list, so the
// list only shrinks and never names nothing.
func TestDeprecatedListNamesRealPackages(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, line := range strings.Split(readFile(t, filepath.Join(root, "deprecated", "PACKAGES")), "\n") {
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "keep "))
		if line == "" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(line))); err != nil || !fi.IsDir() {
			t.Errorf("deprecated/PACKAGES names %s, which is not a directory in the tree; delete the line (the list only shrinks)", line)
		}
	}
}
