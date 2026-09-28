package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Deprecated tools and modules are never tested (Glenn 2026-09-27: "Tests do
// not run for deprecated tools and modules. ... We don't run their tests. We
// don't stop builds for them. We don't bog down CI for them."). Two places
// choose the packages a run tests, select-packages.sh and ci.yml's hosted
// deal, and both read their list through .github/scripts/live-packages.sh,
// which drops what deprecated/PACKAGES names.

func livePackages(t *testing.T, root, in string) []string {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(root, ".github", "scripts", "live-packages.sh"))
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("live-packages.sh: %v\n%s", err, out)
	}
	return strings.Fields(string(out))
}

func TestDeprecatedPackagesAreNeverSelected(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	const mod = "github.com/mas-bandwidth/nova-tools/"
	in := strings.Join([]string{
		"./cmd/nova-sprint",
		mod + "cmd/nova-sprint",
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
	// script's answer for every package of the fixture.
	lt, keptSet := loadLiveTree(t, root), map[string]bool{}
	for _, p := range kept {
		keptSet[p] = true
	}
	for _, p := range strings.Fields(in) {
		if live := lt.Package(strings.TrimPrefix(p, mod)); live != keptSet[p] {
			t.Errorf("liveTree says %s live=%v, live-packages.sh says %v; the two readings of deprecated/PACKAGES disagree", p, live, keptSet[p])
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
		t.Errorf("live-packages.sh kept\n  %s\nwant\n  %s\n(a path in deprecated/PACKAGES drops that package and everything under it; a keep line keeps one; a name that only starts the same is another package)", got, want)
	}

	// both selection points read their list through the filter
	sel := readFile(t, filepath.Join(root, ".github", "scripts", "select-packages.sh"))
	if n := strings.Count(sel, "| live\n"); n < 2 || !strings.Contains(sel, `selected=$(printf '%s' "$selected" | live)`) {
		t.Errorf("select-packages.sh passes %d of its whole-tree lists through live and its selection %v; all three lists must go through live-packages.sh", n, strings.Contains(sel, `"$selected" | live)`))
	}
	ci := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	if !strings.Contains(ci, "go list ./... | bash .github/scripts/live-packages.sh | awk") {
		t.Errorf("ci.yml's hosted deal lists the tree without live-packages.sh, so its shards would test deprecated packages")
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
