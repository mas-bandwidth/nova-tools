package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNativeRemovesTheJobDirWhenTheCardEnds is #2379: the tool that ends the card
// stores the result outside the job directory and then removes the directory,
// including the sandbox tmp. The shared cache is not part of that removal.
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
	rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("exit %d\n%s\n%s", rc, stdout.String(), stderr.String())
	}
	job := filepath.Join(slot, "jobs", label)
	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatalf("job directory still present after the card ended: %v", err)
	}
	tmp := filepath.Join(slot, "tmp", label)
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("sandbox tmp still present: %v", err)
	}
	result := filepath.Join(root, "results", label, "RESULT.md")
	raw, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("RESULT.md was not stored: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(string(raw), "findings: 0") {
		t.Fatalf("stored RESULT.md is not the card's report:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(root, "results", label, "usage.tsv")); err != nil {
		t.Fatalf("usage.tsv was not stored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "results", label, "harness-output.log")); err != nil {
		t.Fatalf("harness log was not stored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "cache", "go-mod", "keep")); err != nil {
		t.Fatalf("shared cache was removed with the job: %v", err)
	}
	if !strings.Contains(stderr.String(), "removed job directory") {
		t.Fatalf("the run did not say it removed the job directory:\n%s", stderr.String())
	}
}
