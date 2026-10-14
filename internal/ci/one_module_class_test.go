package ci

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
