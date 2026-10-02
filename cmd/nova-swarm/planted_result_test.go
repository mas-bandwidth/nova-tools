package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Standing probe for issue #233: native looks for a card's RESULT.md with FindCardResult
// and wroteBytes, both of which Stat the path and follow a planted symlink. A RESULT.md
// that is not a regular file is no published result. These tests go red if native treats
// the planted path as a normal result (follow). The FIFO half lives in
// planted_result_unix_test.go so the package still builds on Windows.

func skipWindowsPlant(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows: no symlink privilege / no FIFO")
	}
}

func plantNativeResultSymlink(t *testing.T, job, body string) string {
	t.Helper()
	skipWindowsPlant(t)
	require.NoError(t, os.MkdirAll(job, 0o755))
	outside := filepath.Join(filepath.Dir(job), "outside-the-wall")
	require.NoError(t, os.WriteFile(outside, []byte(body), 0o644))
	if err := os.Symlink(outside, filepath.Join(job, "RESULT.md")); err != nil {
		t.Skipf("this filesystem will not make a symlink: %v", err)
	}
	return outside
}

// native's result lookup must not follow a symlink planted at RESULT.md and call it published.
func TestNativeDoesNotTreatAPlantedSymlinkAsAPublishedResult(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "planted-result"
	jobDir := filepath.Join(slot, "jobs", label)
	outside := plantNativeResultSymlink(t, jobDir, "RESULT plant sha=aaa\nDONE\nBRANCH worker/exfil-233\n")

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-NORESULT\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "native run exits 0, got %d:\n%s", code, errOut.String())
	require.NotEqual(t, "ok", res.harness, "native treated a planted symlink at RESULT.md as a published result; harness=%s", res.harness)
	raw, err := os.ReadFile(outside)
	require.NoError(t, err)
	require.Contains(t, string(raw), "worker/exfil-233", "the file outside the job was rewritten through the link: %q", string(raw))
}

// The lookup harnessState uses is the same question native asks of RESULT.md.
func TestNativeHarnessStateDoesNotFollowAPlantedSymlinkAtResult(t *testing.T) {
	t.Parallel()

	skipWindowsPlant(t)
	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	plantNativeResultSymlink(t, job, "RESULT plant sha=aaa\nall green from outside the wall\n")
	got := harnessState(job)
	require.NotEqual(t, "ok", got, "native's result lookup followed a planted symlink at RESULT.md")
}
