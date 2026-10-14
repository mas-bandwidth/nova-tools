package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func shipped(h *harness, stamp string) []string {
	var out []string
	for _, t := range h.e.targets {
		for _, n := range []string{"nova-bus", "nova-tokens"} {
			out = append(out, artifactName(n, stamp, t))
		}
	}
	return out
}

func fillDist(t *testing.T, h *harness, stamp string) string {
	t.Helper()
	h.tools("nova-bus", "nova-tokens")
	for _, n := range shipped(h, stamp) {
		h.write("dist/"+n, "bytes of "+n, 0o755)
	}
	return filepath.Join(h.dir, "dist")
}

func TestSumsWritesAndVerifiesOverExactlyTheShippedSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.e.targets = []target{{"linux", "amd64"}, {"windows", "amd64"}}
	dist := fillDist(t, h, "v1.0.0")
	h.wantRC(h.do("sums", "v1.0.0", "dist"), 0)

	raw, err := os.ReadFile(filepath.Join(dist, "SHA256SUMS"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	require.Len(t, lines, 4, "SHA256SUMS lines, want 4 (two tools x two platforms):\n%s", raw)
	for _, l := range lines {
		sum, name, _ := strings.Cut(l, "  ")
		b, _ := os.ReadFile(filepath.Join(dist, name))
		want := sha256.Sum256(b)
		assert.Equal(t, hex.EncodeToString(want[:]), sum, "%s: sum is not the sha256 of the bytes on disk", name)
		assert.NotEqual(t, "SHA256SUMS", name, "SHA256SUMS lists itself")
	}
	assert.Contains(t, string(raw), "nova-bus_v1.0.0_windows_amd64.exe")
	fi, err := os.Stat(filepath.Join(dist, "SHA256SUMS"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
	h.mustContain("nova-bus_v1.0.0_linux_amd64: OK\n")
	h.mustContain("SHA256SUMS over 4 artifacts:\n")
}

func TestSumsRefusesADirectoryThatIsNotTheShippedSetNamingTheDifference(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dist := fillDist(t, h, "v1.0.0")
	h.remove("dist/nova-tokens_v1.0.0_linux_amd64")
	h.write("dist/stray.log", "a runner left this", 0o644)
	h.wantRun(1, "-nova-tokens_v1.0.0_linux_amd64\n", "sums", "v1.0.0", "dist")
	h.mustContain("+stray.log\n")
	h.mustContain("is not the shipped set (- missing, + not shipped)")
	assert.NoFileExists(t, filepath.Join(dist, "SHA256SUMS"), "a checksum file was written over a set that is not the shipped set")
}

func TestSumsRefusesAStampThatDoesNotNameTheArtifacts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fillDist(t, h, "v1.0.0")
	h.wantRun(1, "-nova-bus_v2.0.0_linux_amd64", "sums", "v2.0.0", "dist")
	h.mustContain("+nova-bus_v1.0.0_linux_amd64")
}

func TestSumsRefusesWhatIsNotADirectoryOrAlreadyHoldsSums(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tools("nova-bus")
	h.wantRun(1, "refusing: nowhere is not a directory", "sums", "v1.0.0", "nowhere")

	h = newHarness(t)
	dist := fillDist(t, h, "v1.0.0")
	h.write("dist/SHA256SUMS", "an earlier one\n", 0o644)
	h.wantRun(1, "already holds a SHA256SUMS", "sums", "v1.0.0", "dist")
	b, err := os.ReadFile(filepath.Join(dist, "SHA256SUMS"))
	require.NoError(t, err)
	assert.Equal(t, "an earlier one\n", string(b), "an existing SHA256SUMS was overwritten")
}

func TestSumsRefusesAnEmptyShippedSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("dist/x", "", 0o644)
	h.wantRun(1, "refusing: the shipped set is empty (cmd/*/ x release-targets)", "sums", "v1.0.0", "dist")
}

func TestSumsWrongArgumentCountIsAUsageError(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"sums"}, {"sums", "v1"}, {"sums", "v1", "dist", "x"}} {
		newHarness(t).wantRun(2, "usage:", args...)
	}
}

func TestVerifySumsNamesAFileWhoseBytesChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dist := fillDist(t, h, "v1.0.0")
	h.wantRC(h.do("sums", "v1.0.0", "dist"), 0)
	require.NoError(t, os.WriteFile(filepath.Join(dist, "nova-bus_v1.0.0_linux_amd64"), []byte("tampered"), 0o755))
	h2 := newHarness(t)
	assert.False(t, verifySums(h2.out, h2.errb, dist, filepath.Join(dist, "SHA256SUMS")), "a tampered file verified")
	h2.mustContain("nova-bus_v1.0.0_linux_amd64: FAILED")
	h2.mustContain("nova-tokens_v1.0.0_linux_amd64: OK")
}

func TestDiffSortedFindsBothHalves(t *testing.T) {
	t.Parallel()
	missing, extra := diffSorted([]string{"a", "b", "d"}, []string{"b", "c", "e"})
	assert.Equal(t, []string{"a", "d"}, missing)
	assert.Equal(t, []string{"c", "e"}, extra)
}
