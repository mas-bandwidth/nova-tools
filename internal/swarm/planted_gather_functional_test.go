//go:build functional

package swarm

import (
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests run a batch whose runner is a real program that plants a symlink
// or a FIFO: exec of a whole program is the functional tier's (Glenn 2026-09-26,
// nova-tools#4328).

// The gather refuses a symlink planted at a job's RESULT.md, names it, and never follows it.
func TestGatherRefusesAPlantedSymlinkAtResult(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{{"a", "RESULT: a\nall green"}})
	runner := plantSymlinkRunner(t, dir)
	code, out, errs := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 1 {
		t.Fatalf("a batch whose RESULT.md is a symlink is an abstain and exits 1, got %d; stderr: %s\n%s", code, errs, out)
	}
	if strings.Contains(out, "a secret the wall was keeping") {
		t.Fatalf("the gather read through the planted symlink and folded the outside file:\n%s", out)
	}
	if strings.Contains(out, "a slot=1: all green") {
		t.Fatalf("a planted symlink was gathered as a done result:\n%s", out)
	}
	if !strings.Contains(out, "symlink") || !strings.Contains(out, "RESULT.md") {
		t.Fatalf("the refusal does not name the kind and the path:\n%s", out)
	}
}

// The gather refuses a FIFO planted at a job's RESULT.md and never blocks on it.
func TestGatherDoesNotBlockOnAPlantedFIFO(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{{"a", "RESULT: a\nall green"}})
	runner := plantFIFORunner(t, dir)
	done := make(chan struct{})
	go func() {
		runBatch(t, tsv, root, runner, 30*time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("STILL BLOCKED after 30s gathering a FIFO at RESULT.md: the pass is wedged")
	}
}

func plantSymlinkRunner(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows: no symlink privilege / no FIFO")
	}
	secret := filepath.Join(dir, "secret-outside-the-wall")
	if err := os.WriteFile(secret, []byte("a secret the wall was keeping\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "plant-symlink.sh")
	body := "#!/bin/sh\n" +
		"label=\"$1\"; slot=\"$2\"; root=\"$5\"\n" +
		"job=\"$root/$slot/jobs/$label\"\n" +
		"mkdir -p \"$job\"\n" +
		"ln -s " + strconv.Quote(secret) + " \"$job/RESULT.md\"\n"
	if err := testbin.WriteExecutable(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func plantFIFORunner(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows: no symlink privilege / no FIFO")
	}
	path := filepath.Join(dir, "plant-fifo.sh")
	body := "#!/bin/sh\n" +
		"label=\"$1\"; slot=\"$2\"; root=\"$5\"\n" +
		"job=\"$root/$slot/jobs/$label\"\n" +
		"mkdir -p \"$job\"\n" +
		"mkfifo \"$job/RESULT.md\"\n"
	if err := testbin.WriteExecutable(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
