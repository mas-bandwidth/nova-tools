//go:build functional

package docs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
)

// cli_generated_functional_test.go holds docs/CLI.md to the built tools: a
// real build and real processes, so it is a functional test (docs/TESTING.md)
// and runs in the merge queue and the nightly. cli_generated_test.go keeps the
// marker scan it builds from in the unit tier.

// TestCLIReferenceIsGeneratedFromHelp holds docs/CLI.md's clidoc blocks to the
// built tools: it builds every tool a marker names plus tools/clidoc itself,
// runs the generator over the file, and requires the file on disk to be what
// clidoc writes — a reference block that drifts from the binaries' help is red,
// and the failure names the command that regenerates it.
func TestCLIReferenceIsGeneratedFromHelp(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "CLI.md"))
	require.NoError(t, err, "reading docs/CLI.md: %v", err)
	tools := markerTools(string(raw))
	require.NotEmpty(t, tools,
		"docs/CLI.md carries no <!-- clidoc:begin <tool> --> marker; run `make clidoc` to generate the reference blocks")

	stage := filepath.Join(t.TempDir(), "stage")
	require.NoError(t, os.MkdirAll(stage, 0o755))
	bin := t.TempDir()
	for _, tool := range append(tools[:len(tools):len(tools)], "clidoc") {
		pkg := "./cmd/" + tool
		if tool == "clidoc" {
			pkg = "./tools/clidoc"
		}
		out := filepath.Join(stage, tool)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		build := exec.CommandContext(ctx, "go", "build", "-o", out, pkg)
		build.Dir = root
		build.Env = goenv.Clean(os.Environ())
		built, err := build.CombinedOutput()
		cancel()
		require.NoError(t, err, "go build %s: %s", pkg, built)
		require.NoError(t, testbin.Place(out, filepath.Join(bin, tool)))
	}

	generated := filepath.Join(t.TempDir(), "CLI.md")
	run := exec.Command(filepath.Join(bin, "clidoc"), "--bin", bin, "--out", generated)
	run.Dir = root
	run.Env = goenv.Clean(os.Environ())
	out, err := run.CombinedOutput()
	require.NoError(t, err, "tools/clidoc: %s", out)

	want, err := os.ReadFile(generated)
	require.NoError(t, err, "reading clidoc's output: %v", err)
	require.Equal(t, string(want), string(raw),
		"docs/CLI.md differs from what tools/clidoc generates from the built tools' help; run `make clidoc` to regenerate it")
}
