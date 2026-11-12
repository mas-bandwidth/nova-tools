//go:build functional

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runner_packages_tla_functional_test.go runs roles/runner-packages/tasks/tla.yml through ansible-core on localhost
// against a jar in a temp directory: a jar that differs from the pin is refused by name and left as it was, a matching
// one is left as it was, and a missing one with no source machine is refused naming the fix. None of these paths
// becomes root.

const tlaTestPin = "936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88"

func runTLATasks(t *testing.T, jar, pin string) (string, error) {
	t.Helper()
	return runTLATasksFrom(t, jar, pin, "")
}

func runTLATasksFrom(t *testing.T, jar, pin, source string) (string, error) {
	t.Helper()
	fleet, err := filepath.Abs(".")
	require.NoError(t, err)
	_, out, err := runPackagesPlayResult(t, map[string]any{"nova_runner_tla_jar": jar, "nova_runner_tla_jar_sha256": pin, "nova_runner_tla_jar_source": source},
		[]any{map[string]any{"ansible.builtin.include_tasks": fleet + "/roles/runner-packages/tasks/tla.yml"}})
	return out, err
}

func writeJar(t *testing.T, body string) (string, string) {
	t.Helper()
	jar := filepath.Join(t.TempDir(), "tla", "tla2tools.jar")
	require.NoError(t, os.MkdirAll(filepath.Dir(jar), 0o755))
	require.NoError(t, os.WriteFile(jar, []byte(body), 0o644))
	sum := sha256.Sum256([]byte(body))
	return jar, hex.EncodeToString(sum[:])
}

func TestFunctionalRunnerPackagesTLAJarMismatchRefused(t *testing.T) {
	t.Parallel()
	jar, got := writeJar(t, "not the pinned jar")
	before, err := os.Stat(jar)
	require.NoError(t, err)

	out, err := runTLATasks(t, jar, tlaTestPin)
	require.Error(t, err, out)
	assert.Contains(t, out, "TLA-JAR-MISMATCH host=localhost path="+jar+" want="+tlaTestPin+" got="+got)
	assert.Contains(t, out, "run: nova-fleet runner-packages -- -l localhost")
	assert.NotContains(t, out, "TLA-JAR host=", "no receipt after a refusal")

	b, err := os.ReadFile(jar)
	require.NoError(t, err)
	assert.Equal(t, "not the pinned jar", string(b), "a refused jar is never replaced")
	after, err := os.Stat(jar)
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime())
}

func TestFunctionalRunnerPackagesTLAJarMatchingLeftAlone(t *testing.T) {
	t.Parallel()
	jar, sum := writeJar(t, "the pinned jar")
	before, err := os.Stat(jar)
	require.NoError(t, err)

	out, err := runTLATasks(t, jar, sum)
	require.NoError(t, err, out)
	assert.Contains(t, out, "TLA-JAR host=localhost present "+jar+" sha256="+sum)
	assert.Contains(t, out, "changed=0")
	after, err := os.Stat(jar)
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime())
}

func TestFunctionalRunnerPackagesTLAJarMissingWithNoSourceRefused(t *testing.T) {
	t.Parallel()
	jar := filepath.Join(t.TempDir(), "tla", "tla2tools.jar")

	out, err := runTLATasks(t, jar, tlaTestPin)
	require.Error(t, err, out)
	assert.Contains(t, out, "TLA-JAR-NO-SOURCE host=localhost")
	assert.Contains(t, out, "-e nova_runner_tla_jar_source=")
	assert.NoDirExists(t, filepath.Dir(jar), "nothing is written without a source")
}

func TestFunctionalRunnerPackagesTLAJarMachineIsNeverItsOwnSource(t *testing.T) {
	t.Parallel()
	jar := filepath.Join(t.TempDir(), "tla", "tla2tools.jar")

	out, err := runTLATasksFrom(t, jar, tlaTestPin, "localhost")
	require.Error(t, err, out)
	assert.Contains(t, out, "TLA-JAR-NO-SOURCE host=localhost")
	assert.NoDirExists(t, filepath.Dir(jar))
}

func TestFunctionalRunnerPackagesTLAJarLinkToThePinnedJarLeftAlone(t *testing.T) {
	t.Parallel()
	real, sum := writeJar(t, "the pinned jar")
	link := filepath.Join(t.TempDir(), "tla2tools.jar")
	require.NoError(t, os.Symlink(real, link))

	out, err := runTLATasks(t, link, sum)
	require.NoError(t, err, out)
	assert.Contains(t, out, "TLA-JAR host=localhost present "+link)
	target, err := os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, real, target)
}
