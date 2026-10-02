//go:build functional

// The help's setup line and its examples, run as a stranger pastes them: a shell runs the
// setup line in an empty directory and then every example, with this build first on PATH.
// It starts the go toolchain and a shell, so it is a functional test, never a unit one.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTheHelpsSetupLineAndExamplesRunAsPrinted(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the setup line is a POSIX shell line")
	}
	banner := memoryTool().Banner()
	setup := onboarding.SetupLine(banner)
	require.NotEmpty(t, setup, "the help has no setup line, so its examples read a corpus nobody made")
	examples, err := onboarding.ExampleLines(banner, "nova-memory")
	require.NoError(t, err)

	bin := filepath.Join(t.TempDir(), "nova-memory")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = goenv.Clean(os.Environ())
	out, err := build.CombinedOutput()
	require.NoError(t, err, "building nova-memory:\n%s", out)

	dir := t.TempDir()
	for _, line := range append([]string{setup}, examples...) {
		cmd := exec.Command("sh", "-c", line)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		assert.NoError(t, err, "`%s` does not run as printed:\n%s", line, out)
	}
}
