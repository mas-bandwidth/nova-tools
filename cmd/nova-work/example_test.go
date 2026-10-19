package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkExampleOfflineFixture verifies that nova-work example --dir <dir>
// writes a minimal pair of tree files and a recorded GraphQL import replay fixture
// that can be verified with nova-work verify without gh credentials or network access,
// and that root help first run: cites the offline example command.
func TestWorkExampleOfflineFixture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outDir := filepath.Join(dir, "example")

	// 1. Run example --dir
	res := workMain(unreachable(t)).Run("example", "--dir", outDir)
	require.Equal(t, 0, res.Code, "%s%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "EXAMPLE OK")

	// 2. Verify minimal pair of tree files exists
	treePath := filepath.Join(outDir, "tree.lisp")
	againstPath := filepath.Join(outDir, "against.lisp")
	require.FileExists(t, treePath)
	require.FileExists(t, againstPath)

	// 3. Verify GraphQL fixture exists
	fixtureFiles, err := filepath.Glob(filepath.Join(outDir, "call-*.json"))
	require.NoError(t, err)
	assert.NotEmpty(t, fixtureFiles, "expected call-*.json under %s", outDir)

	// 4. Verify with nova-work verify without network or gh
	verifyRes := workMain(unreachable(t)).Run("verify", "--tree", treePath, "--against", againstPath)
	require.Equal(t, 0, verifyRes.Code, "%s%s", verifyRes.Stdout, verifyRes.Stderr)
	assert.Contains(t, verifyRes.Stdout, "VERIFY OK")

	// 5. Verify import with --fixture without network or gh
	importRes := workMain(unreachable(t)).Run("import", "--org", "acme", "--repo", "acme/widgets", "--fixture", outDir, "--dry-run")
	require.Equal(t, 0, importRes.Code, "%s%s", importRes.Stdout, importRes.Stderr)
	assert.Contains(t, importRes.Stdout, "IMPORT OK")

	// 6. Root help first run: cites offline example command instead of requiring GitHub credentials
	helpRes := workMain(unreachable(t)).Run("help")
	require.Equal(t, 0, helpRes.Code, "%s%s", helpRes.Stdout, helpRes.Stderr)
	assert.Contains(t, helpRes.Stdout, "first run: nova-work example --dir")
	assert.NotContains(t, helpRes.Stdout, "first run: gh logged in")
}
