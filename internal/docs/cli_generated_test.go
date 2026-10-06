package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// TestCLIReferenceIsGeneratedFromHelp ensures docs/CLI.md is generated from
// tool help output via tools/clidoc.
func TestCLIReferenceIsGeneratedFromHelp(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	docPath := filepath.Join(root, "docs", "CLI.md")
	require.FileExists(t, docPath)

	binDir := locateOrBuildBinaries(t, root)

	cmd := exec.Command("go", "run", filepath.Join(root, "tools", "clidoc"), "--bin", binDir, "--doc", docPath, "--check")
	cmd.Dir = root
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docs/CLI.md differs from help output; run: make clidoc\n%s", out)
	}
}

func locateOrBuildBinaries(t *testing.T, root string) string {
	t.Helper()
	tools := expectedNovaTools(t, root)
	if dir := os.Getenv("NOVA_BIN_DIR"); dir != "" && hasAllTools(dir, tools) {
		return dir
	}
	binDir := filepath.Join(root, "bin")
	if hasAllTools(binDir, tools) {
		return binDir
	}
	return buildAllTools(t, root)
}

func expectedNovaTools(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)
	var tools []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "nova-") {
			tools = append(tools, e.Name())
		}
	}
	return tools
}

func hasAllTools(dir string, tools []string) bool {
	for _, tool := range tools {
		name := tool
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil || fi.IsDir() {
			return false
		}
	}
	return true
}

func buildAllTools(t *testing.T, root string) string {
	t.Helper()
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", dir+string(os.PathSeparator), "./cmd/...")
	build.Env = goenv.Clean(os.Environ())
	build.Dir = root
	out, err := build.CombinedOutput()
	require.NoErrorf(t, err, "building ./cmd/...: %v\n%s", err, out)
	return dir
}
