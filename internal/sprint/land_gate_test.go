package sprint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A batch whose change breaks another package's test is refused by the tree gate,
// naming the broken package, even though the batch passes build, vet, and internal/ci.
func TestTheTreeGateRefusesABatchThatBreaksAnotherPackage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}

	write("go.mod", "module example.com/m\n\ngo 1.21\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	write("internal/ci/ci.go", "package ci\n")
	write("internal/ci/ci_test.go", "package ci\n\nimport \"testing\"\n\nfunc TestCI(t *testing.T) {}\n")
	write("pkgA/a.go", "package pkga\n\nfunc Value() int { return 1 }\n")
	write("pkgB/b.go", "package pkgb\n\nimport \"example.com/m/pkgA\"\n\nfunc Get() int { return pkga.Value() }\n")
	write("pkgB/b_test.go", "package pkgb\n\nimport \"testing\"\n\nfunc TestGet(t *testing.T) {\n\tif Get() != 1 {\n\t\tt.Fatalf(\"want 1, got %d\", Get())\n\t}\n}\n")

	// The green batch passes the tree gate.
	whyClean := TreeGate(context.Background(), dir, []string{"pkgA/a.go"}, true, nil)
	assert.Empty(t, whyClean, "green batch passes the tree gate")

	// Changing pkgA/a.go to return 2 breaks pkgB's test, which imports pkgA.
	write("pkgA/a.go", "package pkga\n\nfunc Value() int { return 2 }\n")

	why := TreeGate(context.Background(), dir, []string{"pkgA/a.go"}, true, nil)
	assert.NotEmpty(t, why, "gate must refuse a batch that breaks another package")
	assert.Contains(t, why, "go test")
	assert.Contains(t, why, "./pkgB/")
	assert.Contains(t, why, "want 1, got 2")
}

// In gatePackages, if go list fails, do not silently fallback to internal/docs and internal/ci.
// Return the go list error as the gate's finding.
func TestGatePackagesReturnsGoListErrorWhenGoListFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("invalid go.mod syntax\n"), 0o644))
	why := TreeGate(context.Background(), dir, []string{"something.go"}, true, nil)
	assert.NotEmpty(t, why)
	assert.Contains(t, why, "go list")
}
