package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNativeRemovesTheJobDirWhenTheCardEnds is #2379 on top of #2632: with
// --sweep-now, the tool that ends the card stores the job's record beside the
// published results, outside the job directory, and then removes the directory,
// including the sandbox tmp. The shared cache is not part of that removal, and
// the published usage.tsv is not replaced by the job's own copy.
func TestNativeRemovesTheJobDirWhenTheCardEnds(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "stored"
	if err := os.MkdirAll(filepath.Join(root, "cache", "go-mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cache", "go-mod", "keep"), []byte("mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-FINDINGS 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--idle", "0", "--no-wall", "--sweep-now"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("exit %d\n%s\n%s", rc, stdout.String(), stderr.String())
	}
	job := filepath.Join(slot, "jobs", label)
	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatalf("job directory still present after the card ended: %v\n%s", err, stderr.String())
	}
	tmp := filepath.Join(slot, "tmp", label)
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("sandbox tmp still present: %v", err)
	}
	resultsRoot := filepath.Join(root, "results")
	attempt := oneRunAttempt(t, resultsRoot, label)
	raw, err := os.ReadFile(filepath.Join(attempt, "RESULT.md"))
	if err != nil {
		t.Fatalf("RESULT.md was not stored: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(string(raw), "findings: 0") {
		t.Fatalf("stored RESULT.md is not the card's report:\n%s", raw)
	}
	for _, name := range []string{"usage.tsv", "report", "harness-output.log", "release-manifest.txt"} {
		if _, err := os.Stat(filepath.Join(attempt, name)); err != nil {
			t.Fatalf("%s was not stored under %s: %v\n%s", name, attempt, err, stderr.String())
		}
	}
	// ONE usage.tsv under the results root: the status walk counts every one it
	// finds, so a second copy of the job's rows would be spend counted twice.
	var usages []string
	_ = filepath.WalkDir(resultsRoot, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "usage.tsv" {
			usages = append(usages, path)
		}
		return nil
	})
	if len(usages) != 1 {
		t.Fatalf("want one usage.tsv under %s, got %v", resultsRoot, usages)
	}
	if _, err := os.Stat(filepath.Join(root, "cache", "go-mod", "keep")); err != nil {
		t.Fatalf("shared cache was removed with the job: %v", err)
	}
	if !strings.Contains(stderr.String(), "removed job directory") {
		t.Fatalf("the run did not say it removed the job directory:\n%s", stderr.String())
	}
}

// TestNativeKeepsTheJobDirWithoutSweepNow is #2632's contract: a run without
// --sweep-now publishes its results and leaves the job directory for the bench
// sweep; the release removes nothing on its own.
func TestNativeKeepsTheJobDirWithoutSweepNow(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "kept"
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-FINDINGS 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--idle", "0", "--no-wall"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("exit %d\n%s\n%s", rc, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(slot, "jobs", label, "RESULT.md")); err != nil {
		t.Fatalf("a run without --sweep-now keeps its job directory: %v\n%s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "removed job directory") {
		t.Fatalf("a run without --sweep-now removed its job directory:\n%s", stderr.String())
	}
}
