package swarm

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A probe that plants both at once, on the certification schedule, and asserts the refusal
// lines is the canary (issue #233). The #226 tests cover each site's guarded read; the two
// dispatcher reads of a job's RESULT.md -- the batch gather (batch.go) and the single-card
// check (contract.go) -- read with os.ReadFile and walked straight past the wall. These
// plant a symlink and a FIFO at RESULT.md and ask that each be refused with one line naming
// the path and the kind, never followed and never blocked on.

func plantSymlinkRunner(t *testing.T, dir string) string {
	t.Helper()
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
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func plantFIFORunner(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "plant-fifo.sh")
	body := "#!/bin/sh\n" +
		"label=\"$1\"; slot=\"$2\"; root=\"$5\"\n" +
		"job=\"$root/$slot/jobs/$label\"\n" +
		"mkdir -p \"$job\"\n" +
		"mkfifo \"$job/RESULT.md\"\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The gather refuses a symlink planted at a job's RESULT.md, names it, and never follows it.
func TestGatherRefusesAPlantedSymlinkAtResult(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{{"a", "RESULT: a\nall green"}})
	runner := plantSymlinkRunner(t, dir)
	code, out, errs := runBatch(t, tsv, root, runner, 5*time.Second)
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
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{{"a", "RESULT: a\nall green"}})
	runner := plantFIFORunner(t, dir)
	done := make(chan struct{})
	go func() {
		runBatch(t, tsv, root, runner, 5*time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("STILL BLOCKED after 30s gathering a FIFO at RESULT.md: the pass is wedged")
	}
}

// The single-card contract read refuses a symlink at RESULT.md and never follows it.
func TestCheckResultRefusesAPlantedSymlink(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret-outside-the-wall")
	if err := os.WriteFile(secret, []byte("a secret the wall was keeping\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(dir, "job")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	plantLink(t, secret, ResultPath(job))
	_, err := CheckResult(ResultPath(job), Contract{Label: "a", ContractLine: "RESULT: a"})
	if err == nil {
		t.Fatal("CheckResult read through a planted symlink and raised nothing")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("the refusal does not name the kind symlink: %v", err)
	}
}

// The sequence both probes walk together: a symlink at RESULT.md first, then a FIFO, each
// refused with its kind named and never followed or blocked on.
func TestFriendSequencePlantedResultIsRefused(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret-outside-the-wall")
	if err := os.WriteFile(secret, []byte("a secret the wall was keeping\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	symJob := filepath.Join(dir, "job-symlink")
	if err := os.MkdirAll(symJob, 0o755); err != nil {
		t.Fatal(err)
	}
	plantLink(t, secret, ResultPath(symJob))
	if _, err := readFileSteady(ResultPath(symJob)); err == nil {
		t.Fatal("the steady read read through a planted symlink")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("the symlink refusal does not name the kind: %v", err)
	}

	fifoJob := filepath.Join(dir, "job-fifo")
	if err := os.MkdirAll(fifoJob, 0o755); err != nil {
		t.Fatal(err)
	}
	plantFIFO(t, ResultPath(fifoJob))
	done := make(chan error, 1)
	go func() {
		_, err := readFileSteady(ResultPath(fifoJob))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO read as a published report")
		}
		if !strings.Contains(err.Error(), "fifo") {
			t.Fatalf("the fifo refusal does not name the kind: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("STILL BLOCKED after 30s: the dispatcher is wedged")
	}
}
