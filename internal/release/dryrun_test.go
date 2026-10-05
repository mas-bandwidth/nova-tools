package release

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// build --dry-run resolves the platforms and the tools, prints the plan as one
// receipt and compiles and writes nothing: no toolchain call, no --out.
func TestBuildDryRunPrintsThePlanAndWritesNothing(t *testing.T) {
	t.Parallel()

	source := sourceTree(t)
	out := filepath.Join(t.TempDir(), "out")
	tc := &fakeToolchain{}
	var o, e bytes.Buffer
	code := Run("nova-release", []string{"build", "--version", "v0.16.0", "--out", out, "--source", source, "--dry-run"},
		&o, &e, Deps{Toolchain: tc})
	require.Equal(t, 0, code, e.String())
	assert.Empty(t, tc.calls, "a dry run compiled")
	assert.NoDirExists(t, out, "a dry run made --out")
	assert.Contains(t, o.String(), "RELEASE BUILD OK version=v0.16.0 platforms="+runtime.GOOS+"-"+runtime.GOARCH+" tools=3 ")
	assert.Contains(t, o.String(), "dry-run=yes")
}

// install --dry-run verifies the release whole, counts what it would install
// and what already holds the bytes, and creates no --bin.
func TestInstallDryRunCountsWhatItWouldInstallAndWritesNothing(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "", "nova-bus", "nova-swarm", "nova-wake")
	bin := filepath.Join(t.TempDir(), "bin")
	var o, e bytes.Buffer
	code := Run("nova-release", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin, "--dry-run"}, &o, &e, Deps{})
	require.Equal(t, 0, code, e.String())
	assert.NoDirExists(t, bin, "a dry run made --bin")
	assert.Contains(t, o.String(), "RELEASE INSTALL OK version=v0.16.0 tools=3 skipped=0 ")
	assert.Contains(t, o.String(), "dry-run=yes")

	// A release that does not verify refuses under --dry-run as it does without it.
	dir := ArtifactDir(from, "v0.16.0", runtime.GOOS, runtime.GOARCH)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ToolFile("nova-bus", runtime.GOOS)), []byte("tampered"), 0o755))
	o.Reset()
	e.Reset()
	code = Run("nova-release", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin, "--dry-run"}, &o, &e, Deps{})
	assert.Equal(t, 2, code, e.String())
	assert.Contains(t, e.String(), "INSTALL REFUSED")
	assert.NoDirExists(t, bin)
}
