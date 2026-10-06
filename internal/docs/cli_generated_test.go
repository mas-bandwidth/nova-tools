package docs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// TestCLIReferenceIsGeneratedFromHelp builds the current source rather than trusting
// NOVA_BIN_DIR or bin/, then pins every marked reference block to its tool's help.
func TestCLIReferenceIsGeneratedFromHelp(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	binDir := t.TempDir()
	build, cancelBuild := subproc.Command(context.Background(), subproc.Go, "go", "build", "-o", binDir+string(os.PathSeparator), "./cmd/...")
	defer cancelBuild()
	build.Dir = root
	build.Env = goenv.Clean(os.Environ())
	out, err := build.CombinedOutput()
	require.NoErrorf(t, err, "building current ./cmd/... source: %s", out)

	check, cancelCheck := subproc.Command(context.Background(), subproc.Go, "go", "run", "./tools/clidoc", "--bin", binDir, "--doc", filepath.Join(root, "docs", "CLI.md"), "--check")
	defer cancelCheck()
	check.Dir = root
	check.Env = goenv.Clean(os.Environ())
	out, err = check.CombinedOutput()
	require.NoErrorf(t, err, "docs/CLI.md differs from help output; run: make clidoc\n%s", out)
}
