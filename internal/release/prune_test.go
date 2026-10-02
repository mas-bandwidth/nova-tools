package release

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// versionDirs makes one directory per name under root, the first the OLDEST:
// each is given a modification time a minute after the one before, so "newest"
// in the rule is decided by the clock the test set rather than by how fast the
// loop ran.
func versionDirs(t *testing.T, root string, names ...string) {
	t.Helper()
	base := time.Now().Add(-24 * time.Hour)
	for i, name := range names {
		dir := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "linux-amd64"), 0o755))
		at := base.Add(time.Duration(i) * time.Minute)
		require.NoError(t, os.Chtimes(dir, at, at))
	}
}

// left is every name still directly under root, sorted.
func left(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestPruneKeepsTheProtectedAndTheNewestThreeAndRemovesTheRest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// Oldest first. v1.1.0-dev.prev is the OLDEST and protected, so age alone
	// would remove it; it stays because keep says so.
	versionDirs(t, root, "v1.1.0-dev.prev", "v1.1.0-dev.a1", "v1.1.0-dev.a2", "v1.1.0-dev.a3",
		"v1.1.0-dev.a4", "v1.1.0-dev.a5", "v1.1.0-dev.a6", "v1.1.0-dev.now")
	// Not this tooling's: a name that does not parse as a version, a
	// version-shaped FILE, and a version-shaped name that is not a directory.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scratch"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "v1.1"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "v0.1.0"), []byte("a file"), 0o644))
	if runtime.GOOS != "windows" {
		target := t.TempDir()
		require.NoError(t, os.Symlink(target, filepath.Join(root, "v0.2.0")))
	}

	var errs bytes.Buffer
	removed, failed := pruneDefault(root, func(name string) bool {
		return name == "v1.1.0-dev.now" || name == "v1.1.0-dev.prev"
	}, &errs)

	assert.Equal(t, 3, removed, errs.String())
	assert.Equal(t, 0, failed)
	want := []string{"scratch", "v0.1.0", "v1.1", "v1.1.0-dev.a4", "v1.1.0-dev.a5", "v1.1.0-dev.a6", "v1.1.0-dev.now", "v1.1.0-dev.prev"}
	if runtime.GOOS != "windows" {
		want = append(want, "v0.2.0")
		sort.Strings(want)
	}
	assert.Equal(t, want, left(t, root))
}

func TestPruneOfAFewVersionsRemovesNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	versionDirs(t, root, "v1.1.0-dev.a1", "v1.1.0-dev.a2", "v1.1.0-dev.a3", "v1.1.0-dev.now")
	var errs bytes.Buffer
	removed, failed := pruneDefault(root, func(name string) bool { return name == "v1.1.0-dev.now" }, &errs)
	assert.Equal(t, 0, removed)
	assert.Equal(t, 0, failed)
	assert.Len(t, left(t, root), 4)
}

func TestPruneCountsAndSaysAFailedRemovalAndGoesOn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	versionDirs(t, root, "v1.1.0-dev.a1", "v1.1.0-dev.a2", "v1.1.0-dev.a3", "v1.1.0-dev.a4", "v1.1.0-dev.a5", "v1.1.0-dev.now")
	var errs bytes.Buffer
	var tried []string
	removed, failed := prune(root, func(name string) bool { return name == "v1.1.0-dev.now" }, func(_, path string) error {
		tried = append(tried, filepath.Base(path))
		if filepath.Base(path) == "v1.1.0-dev.a1" {
			return errors.New("permission denied")
		}
		return os.Remove(filepath.Join(path, "linux-amd64"))
	}, &errs)

	assert.Equal(t, 1, removed)
	assert.Equal(t, 1, failed)
	assert.ElementsMatch(t, []string{"v1.1.0-dev.a1", "v1.1.0-dev.a2"}, tried, "the newest three besides the protected one are never tried")
	assert.Contains(t, errs.String(), "cannot remove the old release "+filepath.Join(root, "v1.1.0-dev.a1")+": permission denied")
}

func TestPruneOfARootThatCannotBeListedIsAFailureNotAPanic(t *testing.T) {
	t.Parallel()

	var errs bytes.Buffer
	removed, failed := pruneDefault(filepath.Join(t.TempDir(), "absent"), func(string) bool { return false }, &errs)
	assert.Equal(t, 0, removed)
	assert.Equal(t, 1, failed)
	assert.Contains(t, errs.String(), "cannot list")
}

// The install keeps the version it installs and every version the bin
// directory answered BEFORE it, however old, and removes the rest beyond the
// newest three; a removal that fails leaves the install's exit code at 0.
func TestInstallPrunesOldReleasesButNeverTheOneTheBinCameFrom(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "", "nova-bus")
	// v0.15.0-dev.prev is the OLDEST: by age it would go.
	versionDirs(t, from, "v0.15.0-dev.prev", "v0.15.0-dev.a1", "v0.15.0-dev.a2", "v0.15.0-dev.a3", "v0.15.0-dev.a4", "v0.15.0-dev.a5")
	bin := t.TempDir()
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin}, &o, &e,
		Deps{VersionOf: func(context.Context, string) (string, error) { return "nova-bus v0.15.0-dev.prev linux/amd64", nil }})
	require.Equal(t, 0, code, e.String())
	assert.Contains(t, o.String(), "RELEASE INSTALLED version=v0.16.0 tools=1 skipped=0 retired=0 ")
	assert.Contains(t, o.String(), " pruned=2 prune-failed=0\n")
	assert.Equal(t, []string{"v0.15.0-dev.a3", "v0.15.0-dev.a4", "v0.15.0-dev.a5", "v0.15.0-dev.prev", "v0.16.0"}, left(t, from))
	assertRunnable(t, filepath.Join(bin, ToolFile("nova-bus", runtime.GOOS)))
}

func TestInstallStandsWhenAnOldReleaseCannotBeRemoved(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory is not a refusal on windows or to root")
	}

	from := built(t, "v0.16.0", "", "nova-bus")
	versionDirs(t, from, "v0.15.0-dev.a1", "v0.15.0-dev.a2", "v0.15.0-dev.a3", "v0.15.0-dev.a4")
	// A file inside a directory nobody may write: RemoveAll cannot unlink it.
	locked := filepath.Join(from, "v0.15.0-dev.a1", "linux-amd64")
	require.NoError(t, os.WriteFile(filepath.Join(locked, "nova-bus"), []byte("old"), 0o644))
	require.NoError(t, os.Chmod(locked, 0o555))
	t.Cleanup(func() { require.NoError(t, os.Chmod(locked, 0o755)) })

	bin := t.TempDir()
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin}, &o, &e,
		Deps{VersionOf: func(context.Context, string) (string, error) { return "", errors.New("no such file") }})
	require.Equal(t, 0, code, e.String())
	assert.Contains(t, o.String(), " pruned=0 prune-failed=1\n")
	assert.Contains(t, e.String(), "cannot remove the old release "+filepath.Join(from, "v0.15.0-dev.a1"))
	assertRunnable(t, filepath.Join(bin, ToolFile("nova-bus", runtime.GOOS)))
}

// The build keeps the version it built and the version of the nova-update
// running it (what the build machine has installed), and removes the rest of
// --out beyond the newest three.
func TestBuildPrunesItsOutButKeepsTheRunningVersion(t *testing.T) {
	t.Parallel()

	source, out := sourceTree(t), t.TempDir()
	versionDirs(t, out, "v0.15.0-dev.self", "v0.15.0-dev.a1", "v0.15.0-dev.a2", "v0.15.0-dev.a3", "v0.15.0-dev.a4", "v0.15.0-dev.a5")
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", out, "--source", source}, &o, &e,
		Deps{Toolchain: &fakeToolchain{}, Self: func() string { return "v0.15.0-dev.self" }})
	require.Equal(t, 0, code, e.String())
	assert.Contains(t, o.String(), "RELEASE BUILD OK version=v0.16.0 ")
	assert.Contains(t, o.String(), " pruned=2 prune-failed=0\n")
	assert.Equal(t, []string{"v0.15.0-dev.a3", "v0.15.0-dev.a4", "v0.15.0-dev.a5", "v0.15.0-dev.self", "v0.16.0"}, left(t, out))
}
