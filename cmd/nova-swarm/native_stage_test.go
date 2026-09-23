package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStageUsesTheBenchMirrorAndTimesOut tests the stage requirements of mas-bandwidth/nova-tools#2882:
// 1. A card pointing to a remote repo without a bench mirror fails staging without going to GitHub.
// 2. Staging clones from the bench's local mirror (--reference or clone --shared) and dissociates.
// 3. Staging past the timeout ends the card RESULT: BLOCKED stage-timeout <bench> <secs> and writes usage.tsv.
func TestStageUsesTheBenchMirrorAndTimesOut(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	benchHome := filepath.Join(root, "bench-home")
	srcDir := filepath.Join(root, "src-repo")
	mirrorDir := filepath.Join(benchHome, "nova-bench", "mirror", "sample-repo.git")

	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(mirrorDir), 0o755); err != nil {
		t.Fatal(err)
	}

	runGit(t, srcDir, "init", "-q")
	runGit(t, srcDir, "config", "user.name", "test")
	runGit(t, srcDir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("# sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, srcDir, "add", "README.md")
	runGit(t, srcDir, "commit", "-q", "-m", "initial commit")
	headSha := strings.TrimSpace(runGit(t, srcDir, "rev-parse", "HEAD"))

	runGit(t, root, "clone", "--mirror", "-q", srcDir, mirrorDir)

	t.Run("fails on clone that would go to github without mirror", func(t *testing.T) {
		cardText := []byte("base-repo: https://example.com/mas-bandwidth/missing-mirror.git\nbase-sha: 1234567890123456789012345678901234567890\n")
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary:       bin,
			model:        "fake/fake-model",
			label:        "card-no-mirror",
			card:         cardText,
			slotDir:      slot,
			root:         root,
			benchHome:    benchHome,
			benchName:    "vision",
			stageTimeout: 30 * time.Second,
			deadline:     30 * time.Second,
			noWall:       true,
		}, &errOut)
		if code != 2 {
			t.Fatalf("expected refusal exit code 2 when no bench mirror exists, got %d:\n%s", code, errOut.String())
		}
		if !strings.Contains(errOut.String(), "no bench mirror") {
			t.Fatalf("expected error mentioning no bench mirror, got:\n%s", errOut.String())
		}
	})

	t.Run("stages cleanly from bench mirror", func(t *testing.T) {
		cardText := []byte("base-repo: https://example.com/mas-bandwidth/sample-repo.git\nbase-sha: " + headSha + "\n")
		var errOut bytes.Buffer
		res, code := nativeRun(nativeRunConfig{
			binary:       bin,
			model:        "fake/fake-model",
			label:        "card-mirrored",
			card:         cardText,
			slotDir:      slot,
			root:         root,
			benchHome:    benchHome,
			benchName:    "vision",
			stageTimeout: 30 * time.Second,
			deadline:     30 * time.Second,
			noWall:       true,
		}, &errOut)
		if code != 0 {
			t.Fatalf("expected code 0 on successful staging, got %d:\n%s", code, errOut.String())
		}
		if res.rc != 0 {
			t.Fatalf("expected harness rc 0, got %d", res.rc)
		}
		repoDir := filepath.Join(slot, "jobs", "card-mirrored", "repo")
		if _, err := os.Stat(repoDir); err != nil {
			t.Fatalf("expected repo dir %s to exist: %v", repoDir, err)
		}
		alternates := filepath.Join(repoDir, ".git", "objects", "info", "alternates")
		if _, err := os.Stat(alternates); !os.IsNotExist(err) {
			t.Fatalf("staging did not dissociate from mirror: alternates file exists at %s", alternates)
		}
	})

	t.Run("times out and writes blocked result and usage", func(t *testing.T) {
		cardText := []byte("base-repo: https://example.com/mas-bandwidth/sample-repo.git\nbase-sha: " + headSha + "\n")
		var errOut bytes.Buffer
		res, code := nativeRun(nativeRunConfig{
			binary:       bin,
			model:        "fake/fake-model",
			label:        "card-timeout",
			card:         cardText,
			slotDir:      slot,
			root:         root,
			benchHome:    benchHome,
			benchName:    "hulk",
			stageTimeout: 1 * time.Nanosecond,
			deadline:     30 * time.Second,
			noWall:       true,
		}, &errOut)
		if code == 0 {
			t.Fatalf("expected non-zero code on stage timeout, got %d", code)
		}
		if res.end != "stage-timeout" {
			t.Fatalf("expected res.end = stage-timeout, got %q", res.end)
		}

		resultPath := filepath.Join(slot, "jobs", "card-timeout", "RESULT.md")
		resultBytes, err := os.ReadFile(resultPath)
		if err != nil {
			t.Fatalf("RESULT.md not written on stage timeout: %v", err)
		}
		resultLines := strings.Split(string(resultBytes), "\n")
		expectedLine1 := "RESULT: BLOCKED stage-timeout hulk 1"
		if resultLines[0] != expectedLine1 {
			t.Fatalf("expected RESULT.md line 1 %q, got %q", expectedLine1, resultLines[0])
		}

		usagePath := filepath.Join(slot, "jobs", "card-timeout", "usage.tsv")
		usageBytes, err := os.ReadFile(usagePath)
		if err != nil {
			t.Fatalf("usage.tsv not written on stage timeout: %v", err)
		}
		if !strings.Contains(string(usageBytes), "card-timeout") {
			t.Fatalf("usage.tsv does not mention card-timeout:\n%s", string(usageBytes))
		}
	})
}
