package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sprintReleaseDir is a nova-sprint release as `gh release download` leaves
// it: <tool>_<tag>_<goos>_<goarch> and SHA256SUMS_<goos>_<goarch> naming each
// tool bare, for every platform given.
func sprintReleaseDir(t *testing.T, tag string, platforms ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range platforms {
		goos, goarch, ok := strings.Cut(p, "-")
		require.True(t, ok)
		var sums strings.Builder
		for _, tool := range []string{"nova-card", "nova-sprint", "nova-work"} {
			body := []byte(tool + " " + tag + " " + p + "\n")
			sum := sha256.Sum256(body)
			sums.WriteString(hex.EncodeToString(sum[:]) + "  " + tool + "\n")
			require.NoError(t, os.WriteFile(filepath.Join(dir, tool+"_"+tag+"_"+goos+"_"+goarch), body, 0o644))
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SHA256SUMS_"+goos+"_"+goarch), []byte(sums.String()), 0o644))
	}
	return dir
}

// TestBuildTakesNovaSprintFromItsOwnRelease is layer L7 of the split: the
// sprint tools are not compiled from this tree; a build given a downloaded
// nova-sprint release copies its verified tools into each platform directory,
// executable, and the release's own SHA256SUMS covers them.
func TestBuildTakesNovaSprintFromItsOwnRelease(t *testing.T) {
	t.Parallel()
	source, out := sourceTree(t), t.TempDir()
	rel := sprintReleaseDir(t, "v1.2.3", "linux-amd64")
	tc := &fakeToolchain{}
	o, _ := buildAt(t, source, out, "v1.2.3", &fakeSource{}, tc, "--sprint-release", rel)

	for _, call := range tc.calls {
		assert.NotContains(t, call, "nova-sprint", "a sprint tool was compiled here")
	}
	assert.Contains(t, o, "RELEASE BUILT version=v1.2.3 platform=linux-amd64 tools=6 verified=6 ")
	assert.Contains(t, o, " sprint=v1.2.3\n")
	dir := ArtifactDir(out, "v1.2.3", "linux", "amd64")
	sums, err := os.ReadFile(filepath.Join(dir, SumsFile))
	require.NoError(t, err)
	for _, tool := range []string{"nova-card", "nova-sprint", "nova-work"} {
		got, err := os.ReadFile(filepath.Join(dir, tool))
		require.NoError(t, err)
		assert.Equal(t, tool+" v1.2.3 linux-amd64\n", string(got))
		info, err := os.Stat(filepath.Join(dir, tool))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(), "%s is not executable", tool)
		sum := sha256.Sum256(got)
		assert.Contains(t, string(sums), hex.EncodeToString(sum[:])+"  "+tool+"\n")
	}
}

// TestBuildWithoutASprintReleaseShipsNoSprintTool: no --sprint-release, no
// sprint tool, and the line says so.
func TestBuildWithoutASprintReleaseShipsNoSprintTool(t *testing.T) {
	t.Parallel()
	source, out := sourceTree(t), t.TempDir()
	o, _ := buildAt(t, source, out, "v1.2.3", &fakeSource{}, &fakeToolchain{})
	assert.Contains(t, o, "RELEASE BUILT version=v1.2.3 platform=linux-amd64 tools=3 verified=3 ")
	assert.Contains(t, o, " sprint=none\n")
	_, err := os.Stat(filepath.Join(ArtifactDir(out, "v1.2.3", "linux", "amd64"), "nova-sprint"))
	assert.True(t, os.IsNotExist(err))
}

// TestBuildRefusesABadSprintRelease is the reversed witness of the take: each
// way a downloaded release can be wrong is refused before the first compile,
// and nothing is written under --out.
func TestBuildRefusesABadSprintRelease(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		spoil func(t *testing.T, dir string)
		want  string
	}{
		{"a platform the release lacks", func(t *testing.T, dir string) {
			require.NoError(t, os.Remove(filepath.Join(dir, "SHA256SUMS_linux_amd64")))
		}, "the release has no linux-amd64 build"},
		{"bytes that are not the checksums'", func(t *testing.T, dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "nova-work_v1.2.3_linux_amd64"), []byte("tampered\n"), 0o644))
		}, "is not the bytes"},
		{"a listed tool missing", func(t *testing.T, dir string) {
			require.NoError(t, os.Remove(filepath.Join(dir, "nova-card_v1.2.3_linux_amd64")))
		}, "holds 0 files named nova-card_<tag>_linux_amd64"},
		{"two releases in one directory", func(t *testing.T, dir string) {
			require.NoError(t, os.Rename(filepath.Join(dir, "nova-sprint_v1.2.3_linux_amd64"), filepath.Join(dir, "nova-sprint_v1.2.2_linux_amd64")))
		}, "holds two nova-sprint releases"},
		{"a tool this tree also builds", func(t *testing.T, dir string) {
			body := []byte("bus\n")
			sum := sha256.Sum256(body)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "nova-bus_v1.2.3_linux_amd64"), body, 0o644))
			f, err := os.OpenFile(filepath.Join(dir, "SHA256SUMS_linux_amd64"), os.O_APPEND|os.O_WRONLY, 0)
			require.NoError(t, err)
			_, err = f.WriteString(hex.EncodeToString(sum[:]) + "  nova-bus\n")
			require.NoError(t, err)
			require.NoError(t, f.Close())
		}, "--source builds nova-bus and the nova-sprint release"},
		{"a checksum line that names no tool", func(t *testing.T, dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "SHA256SUMS_linux_amd64"), []byte("abc  ../nova-sprint\n"), 0o644))
		}, "is not <sha256>  <tool>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rel := sprintReleaseDir(t, "v1.2.3", "linux-amd64")
			tc.spoil(t, rel)
			source, out := sourceTree(t), t.TempDir()
			chain := &fakeToolchain{}
			var o, e bytes.Buffer
			code := Run("nova-update", []string{"build", "--version", "v1.2.3", "--out", out, "--source", source,
				"--platform", "linux-amd64", "--sprint-release", rel}, &o, &e, Deps{Toolchain: chain, Source: &fakeSource{}})
			assert.Equal(t, 2, code, "stdout:%s\nstderr:%s", o.String(), e.String())
			assert.Contains(t, e.String(), "BUILD REFUSED")
			assert.Contains(t, e.String(), tc.want)
			if !strings.Contains(tc.want, "--source builds") {
				assert.Contains(t, e.String(), "gh release download", "the refusal does not say how to get a release")
			}
			assert.Empty(t, chain.calls, "a tool was compiled before the refusal")
			_, err := os.Stat(filepath.Join(out, "v1.2.3"))
			assert.True(t, os.IsNotExist(err), "the refusal left a directory under --out")
		})
	}
}
