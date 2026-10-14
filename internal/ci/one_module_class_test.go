package ci

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/pkgselect"
)

// TestOneModule holds the rule that nova-tools is ONE Go module: the root
// go.mod, and no go.mod or go.work anywhere else in the tree.
//
// A directory with a go.mod of its own is a separate module. The root's
// `go list ./...`, `go test ./...` and `go vet ./...` never reach it, and an
// outside module that requires nova-tools must resolve it by its own version,
// which a replace directive in the root go.mod cannot supply (replace applies
// only in the main module). hook/ and pkg/oneline/ were nested modules required
// at versions that were never tagged, so `go mod tidy` in any module importing
// nova-tools failed. Folding them into the root fixed it; this test keeps them
// folded. go.work is refused for the same reason: a workspace build sees what
// an outside module does not.
//
// **Its allowlist:** none. testdata/ directories are skipped (the go tool
// ignores them too).
func TestOneModule(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	require.FileExists(t, filepath.Join(root, "go.mod"), "the root go.mod is the one module")

	_, err := os.Stat(filepath.Join(root, "go.work"))
	assert.Truef(t, os.IsNotExist(err), "go.work exists at the root; nova-tools is one module and a workspace build sees what an outside module importing nova-tools does not")

	var nested []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", ".git", "bin", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" && filepath.Dir(path) != root {
			rel, _ := filepath.Rel(root, path)
			nested = append(nested, rel)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Emptyf(t, nested, "nested go.mod files %v: nova-tools is one module; delete them and drop any require/replace for them from the root go.mod, keeping the import paths (they are already under github.com/mas-bandwidth/nova-tools)", nested)
}

// notSelected are the top-level directories that hold Go packages and are
// outside pkgselect.Roots on purpose, each with its reason.
var notSelected = map[string]string{
	"fleet":    "certification.yml deals fleet/ and fleet/toolchainapply/ as heavy packages of their own; the pull request selection leaves them out",
	"profiles": "pkg/sandbox's embedded darwin profile; no tests, and go test ./pkg/sandbox checks it",
}

// TestEveryTopLevelPackageDirIsSelected holds the CL selection's roots to the
// tree: every top-level directory holding a non-test .go file outside
// testdata/ is in pkgselect.Roots (so select-packages, the whole-tree
// fallback and change selection reach its packages) or in notSelected with a
// reason. hook/ was folded into the root module and was then tested by no CI
// leg until it joined Roots; this keeps the next folded directory from
// dropping out of CI the same way.
func TestEveryTopLevelPackageDirIsSelected(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	var missing []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		if slices.Contains(pkgselect.Roots, name) {
			continue
		}
		if _, ok := notSelected[name]; ok {
			continue
		}
		if holdsGoPackage(t, filepath.Join(root, name)) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	assert.Emptyf(t, missing, "top-level directories %v hold Go packages but are neither in pkgselect.Roots (pkg/pkgselect/select.go) nor in notSelected here; their tests would run in no CI leg. Add each to Roots, or to notSelected with the reason", missing)
	for name := range notSelected {
		assert.Falsef(t, slices.Contains(pkgselect.Roots, name), "%s is both in pkgselect.Roots and in notSelected; drop the notSelected row", name)
	}
}

// holdsGoPackage reports whether dir holds a .go file outside testdata/ and
// outside . or _ directories, which is what go list ./... would name.
func holdsGoPackage(t *testing.T, dir string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if path != dir && (n == "testdata" || n == "vendor" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	require.NoError(t, err)
	return found
}
