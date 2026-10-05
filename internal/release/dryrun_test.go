package release

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

// install --dry-run validates --retire as the real run does and plans the
// retirement before it answers OK: --retire naming --bin refuses, and a stale
// nova-* file in the named directory is counted and left in place.
func TestInstallDryRunValidatesAndPlansRetire(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "", "nova-bus", "nova-swarm")
	bin := filepath.Join(t.TempDir(), "bin")
	stale := filepath.Join(t.TempDir(), "gobin")
	require.NoError(t, os.MkdirAll(stale, 0o755))
	old := filepath.Join(stale, ToolFile("nova-bus", runtime.GOOS))
	require.NoError(t, os.WriteFile(old, []byte("old"), 0o755))

	var o, e bytes.Buffer
	code := Run("nova-release", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin, "--retire", bin, "--dry-run"}, &o, &e, Deps{})
	assert.Equal(t, 2, code, e.String())
	assert.Contains(t, e.String(), "INSTALL REFUSED")
	assert.Contains(t, e.String(), "is --bin")
	assert.Empty(t, o.String(), "a refused dry run printed OK")

	o.Reset()
	e.Reset()
	code = Run("nova-release", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin, "--retire", stale, "--dry-run"}, &o, &e, Deps{})
	require.Equal(t, 0, code, e.String())
	assert.Contains(t, o.String(), "retired=1 ")
	assert.FileExists(t, old, "a dry run removed the stale tool")
	assert.NoDirExists(t, bin)
}

// The fleet play runs the release verbs through nova-release: nova-update
// refuses `release`, so a command vector that still names it fails the play.
func TestFleetToolsPlayCallsNovaRelease(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(filepath.Join("..", "..", "fleet", "tools.yml"))
	require.NoError(t, err)
	var plays []struct {
		Tasks []map[string]any `yaml:"tasks"`
	}
	require.NoError(t, yaml.Unmarshal(b, &plays))
	var verbs []string
	for _, p := range plays {
		for _, task := range p.Tasks {
			cmd, ok := task["ansible.builtin.command"].(map[string]any)
			if !ok {
				continue
			}
			argv := fmt.Sprint(cmd["argv"])
			if strings.Contains(argv, "nova-update") {
				assert.NotContains(t, argv, "release", "a play vector runs the moved verb through nova-update")
			}
			for _, verb := range []string{"build", "install"} {
				if strings.Contains(argv, "nova-release") && strings.Contains(argv, verb) {
					verbs = append(verbs, verb)
				}
			}
		}
	}
	assert.ElementsMatch(t, []string{"build", "install"}, verbs, "the play builds and installs through nova-release")
}
