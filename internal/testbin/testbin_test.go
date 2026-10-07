package testbin

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain lets a placed copy of this test binary answer as a trivial program:
// with TESTBIN_CHILD=1 it prints one word and exits, so Place can be proven to
// produce something that runs without a second build.
func TestMain(m *testing.M) {
	if os.Getenv("TESTBIN_CHILD") == "1" {
		_, _ = os.Stdout.WriteString("placed\n") // ignored: the parent asserts this word in its output, so a lost write fails there
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestPlaceRunsThePlacedProgram: the thing Place produces is an executable, on
// the link path and on the copy path alike. This test binary is both the source
// and, re-entered through TestMain, the program.
func TestPlaceRunsThePlacedProgram(t *testing.T) {
	t.Parallel()

	src, err := os.Executable()
	require.NoError(t, err)
	dst := filepath.Join(t.TempDir(), "placed")
	if runtime.GOOS == "windows" {
		dst += ".exe"
	}
	err = Place(src, dst)
	require.NoError(t, err, "Place: %v", err)
	cmd := exec.Command(dst)
	cmd.Env = append(os.Environ(), "TESTBIN_CHILD=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "running the placed program: %v\n%s", err, out)
	assert.Equal(t, "placed", strings.TrimSpace(string(out)), "placed program printed %q, want %q", out, "placed")
}

// TestPlaceHardLinksInTheSameDirectory: on a platform with links, a source and
// a destination in the same directory are the same file after Place -- the link
// is what avoids the macOS scan, and a copy here would be a regression.
func TestPlaceHardLinksInTheSameDirectory(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows always copies")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(src, []byte("x"), 0o755))
	require.NoError(t, Place(src, dst))
	si, err := os.Stat(src)
	require.NoError(t, err)
	di, err := os.Stat(dst)
	require.NoError(t, err)
	assert.True(t, os.SameFile(si, di), "Place copied instead of linking: %s and %s are different files", src, dst)
}

// TestPlaceFallsBackToCopyWhenLinkFails: a destination on another filesystem
// cannot be linked, so Place must copy the bytes -- executable -- rather than
// fail. The link function is place's linkFn parameter, the per-test seam, so
// the fallback is exercised on any filesystem.
func TestPlaceFallsBackToCopyWhenLinkFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	raw := []byte("built\n")
	require.NoError(t, os.WriteFile(src, raw, 0o755))
	err := place(src, dst, func(oldname, newname string) error { return os.ErrInvalid })
	require.NoError(t, err, "place with a failing link: %v", err)
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, string(raw), string(got), "copied content = %q, want %q", got, raw)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dst)
		require.NoError(t, err)
		assert.NotZero(t, info.Mode().Perm()&0o111, "the fallback copy mode %v has no execute bit", info.Mode().Perm())
		si, err := os.Stat(src)
		require.NoError(t, err)
		di, err := os.Stat(dst)
		require.NoError(t, err)
		assert.False(t, os.SameFile(si, di), "the fallback must copy, not link")
	}
}

// TestPlaceReplacesAnExistingFile: a caller may ask twice, so a dst already
// there is removed rather than linked through.
func TestPlaceReplacesAnExistingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(src, []byte("new"), 0o755))
	require.NoError(t, os.WriteFile(dst, []byte("old"), 0o644))
	require.NoError(t, Place(src, dst))
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "new", string(got), "dst = %q, want %q", got, "new")
}
