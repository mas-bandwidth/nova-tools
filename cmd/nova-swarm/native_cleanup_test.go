package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// TestNativeRunCleansStorageOnIdleReap verifies that an idle-reaped card cleans up its job
// directory and sandbox tmp, while preserving RESULT.md and harness log in the results store.
func TestNativeRunCleansStorageOnIdleReap(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "card-idle-clean"
	resultsDir := filepath.Join(root, "results", label)
	jobDir := filepath.Join(slot, "jobs", label)
	tmpDir := filepath.Join(slot, "tmp", label)

	// Inject an immediate idle event via nativeWatchIdle seam
	prevWatch := nativeWatchIdle
	defer func() { nativeWatchIdle = prevWatch }()
	nativeWatchIdle = func(w swarm.IdleWatch, stop <-chan struct{}) <-chan swarm.IdleEnd {
		c := make(chan swarm.IdleEnd, 1)
		c <- swarm.IdleEnd{
			Idle:    10 * time.Second,
			Step:    "-",
			Refused: false,
		}
		return c
	}

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary:     bin,
		model:      "fake/fake-model",
		label:      label,
		card:       []byte("card\n"),
		slotDir:    slot,
		root:       root,
		deadline:   30 * time.Second,
		noWall:     true,
		resultsDir: resultsDir,
		cleanJobs:  true,
	}, &errOut)

	if code != 0 {
		t.Fatalf("nativeRun returned code %d; stderr:\n%s", code, errOut.String())
	}
	if !res.idled {
		t.Fatalf("expected res.idled to be true")
	}
	if !res.cleanedJob {
		t.Fatalf("expected res.cleanedJob to be true")
	}

	// Verify job directory and sandbox tmp were removed
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Errorf("job directory %s still exists after idle cleanup", jobDir)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("tmp directory %s still exists after idle cleanup", tmpDir)
	}

	// Verify RESULT.md was preserved in resultsDir
	resultPath := filepath.Join(resultsDir, "RESULT.md")
	if _, err := os.Stat(resultPath); err != nil {
		t.Errorf("RESULT.md not preserved in resultsDir %s: %v", resultsDir, err)
	}
}

// TestNativeRunCleansStorageWhenResultsMoved: when resultsDir is specified, durable outputs move
// and the job directory and sandbox tmp are removed.
func TestNativeRunCleansStorageWhenResultsMoved(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "card-moved-clean"
	resultsDir := filepath.Join(root, "results", label)
	jobDir := filepath.Join(slot, "jobs", label)
	tmpDir := filepath.Join(slot, "tmp", label)

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary:     bin,
		model:      "fake/fake-model",
		label:      label,
		card:       []byte("card\n"),
		slotDir:    slot,
		root:       root,
		deadline:   30 * time.Second,
		noWall:     true,
		resultsDir: resultsDir,
		cleanJobs:  true,
	}, &errOut)

	if code != 0 {
		t.Fatalf("nativeRun returned code %d; stderr:\n%s", code, errOut.String())
	}
	if !res.cleanedJob {
		t.Fatalf("expected res.cleanedJob to be true when resultsDir provided")
	}

	// Verify job directory and sandbox tmp were removed
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Errorf("job directory %s still exists after results moved", jobDir)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("tmp directory %s still exists after results moved", tmpDir)
	}

	// Verify harness log exists in resultsDir
	logPath := filepath.Join(resultsDir, "harness-output.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("harness-output.log not preserved in resultsDir %s: %v", resultsDir, err)
	}
}

// TestNativeRunPreservesStorageWhenResultsNotMoved: when results have not moved and it is not
// a harness failure, the job directory is preserved intact (preventing loss of unpushed commits).
func TestNativeRunPreservesStorageWhenResultsNotMoved(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "card-preserve"
	jobDir := filepath.Join(slot, "jobs", label)

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary:   bin,
		model:    "fake/fake-model",
		label:    label,
		card:     []byte("card\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second,
		noWall:   true,
	}, &errOut)

	if code != 0 {
		t.Fatalf("nativeRun returned code %d; stderr:\n%s", code, errOut.String())
	}
	if res.cleanedJob {
		t.Fatalf("expected res.cleanedJob to be false when results not moved")
	}

	// Verify job directory still exists
	if _, err := os.Stat(jobDir); err != nil {
		t.Errorf("job directory %s was unexpectedly removed: %v", jobDir, err)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "usage.tsv")); err != nil {
		t.Errorf("usage.tsv not found in preserved job directory: %v", err)
	}
}

// TestNativeRunNeverRemovesSharedCachesOrMirrors: shared caches (tmp/cache/{go-mod,go-build,npm})
// and mirrors (~/nova-bench/mirror) are never in the removal set.
func TestNativeRunNeverRemovesSharedCachesOrMirrors(t *testing.T) {
	bin := nativeHarness(t)
	home := t.TempDir()
	root := filepath.Join(home, "swarm")
	slot := filepath.Join(root, "001")
	mirrorDir := filepath.Join(home, "nova-bench", "mirror")
	cacheMod := filepath.Join(root, "tmp", "cache", "go-mod")
	cacheBuild := filepath.Join(root, "tmp", "cache", "go-build")
	cacheNPM := filepath.Join(root, "tmp", "cache", "npm")

	for _, d := range []string{slot, mirrorDir, cacheMod, cacheBuild, cacheNPM} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	mirrorSentinel := filepath.Join(mirrorDir, "HEAD")
	if err := os.WriteFile(mirrorSentinel, []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheSentinel := filepath.Join(cacheMod, "cache.txt")
	if err := os.WriteFile(cacheSentinel, []byte("shared-cache\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	label := "card-protect"
	resultsDir := filepath.Join(root, "results", label)
	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary:     bin,
		model:      "fake/fake-model",
		label:      label,
		card:       []byte("card\n"),
		slotDir:    slot,
		root:       root,
		deadline:   30 * time.Second,
		noWall:     true,
		resultsDir: resultsDir,
		cleanJobs:  true,
		benchHome:  home,
	}, &errOut)

	if code != 0 {
		t.Fatalf("nativeRun returned code %d; stderr:\n%s", code, errOut.String())
	}
	if !res.cleanedJob {
		t.Fatalf("expected res.cleanedJob to be true")
	}

	// Verify sentinels in mirror and shared caches were NEVER touched
	if _, err := os.Stat(mirrorSentinel); err != nil {
		t.Fatalf("mirror sentinel was deleted! %v", err)
	}
	if _, err := os.Stat(cacheSentinel); err != nil {
		t.Fatalf("cache sentinel was deleted! %v", err)
	}
}

// TestCmdNativeCleanFlag verifies that --clean and --clean-jobs CLI flags enable job storage cleanup.
func TestCmdNativeCleanFlag(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("echo hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	label := "card-cli-clean"
	resultsDir := filepath.Join(root, "results", label)
	jobDir := filepath.Join(slot, "jobs", label)

	args := []string{
		"native", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall", "--clean", "--results-dir", resultsDir,
	}
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	if code != 0 {
		t.Fatalf("run returned code %d; stderr:\n%s", code, stderr.String())
	}

	// With --clean and --results-dir, jobDir must be removed
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Errorf("job directory %s should be removed with --clean; stat err: %v", jobDir, err)
	}

	// Results must be in resultsDir
	if _, err := os.Stat(filepath.Join(resultsDir, "harness-output.log")); err != nil {
		t.Errorf("expected harness log in resultsDir: %v", err)
	}
}
