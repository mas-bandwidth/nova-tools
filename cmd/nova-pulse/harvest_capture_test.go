package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func TestCmdHarvestCaptureAndRecovery(t *testing.T) {
	temp := t.TempDir()
	working := filepath.Join(temp, "working")
	jobDir := filepath.Join(working, "tmp", "guid-card1", "jobs", "card1")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write job files
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte("RESULT fixed card=card1 branch=rowan/test pr=10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "usage.tsv"), []byte("tokens\tusd\n100\t0.001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), []byte("NATIVE OK label=card1 rc=0 wall=2s harness=ok reason=fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "partial.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	captureDir := filepath.Join(temp, "captures")
	var stdout, stderr bytes.Buffer
	now := time.Now().UTC()

	// 1. Run harvest with --working and --capture-dir
	code := cmdHarvest([]string{
		"--working", working,
		"--capture-dir", captureDir,
	}, &stdout, &stderr, now)

	if code != 0 && code != 1 {
		t.Fatalf("cmdHarvest failed (code %d):\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "HARVEST CAPTURE") {
		t.Fatalf("expected HARVEST CAPTURE in output, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "retained=true") {
		t.Errorf("expected retained=true in output, got:\n%s", outStr)
	}

	// Verify keyed output path: card1/attempt-1/card1
	expectedJobCapture := filepath.Join(captureDir, "card1", "attempt-1", "card1")
	if _, err := os.Stat(filepath.Join(expectedJobCapture, "manifest.json")); err != nil {
		t.Fatalf("manifest.json not found in %s: %v", expectedJobCapture, err)
	}

	// Verify source job was NOT deleted
	if _, err := os.Stat(filepath.Join(jobDir, "partial.go")); err != nil {
		t.Fatalf("source job directory was deleted: %v", err)
	}

	// 2. Run recovery with --recover <jobCaptureDir>
	stdout.Reset()
	stderr.Reset()
	recCode := cmdHarvest([]string{
		"--recover", expectedJobCapture,
	}, &stdout, &stderr, now)

	if recCode != 0 {
		t.Fatalf("cmdHarvest --recover failed (code %d):\nstdout: %s\nstderr: %s", recCode, stdout.String(), stderr.String())
	}
	recOut := stdout.String()
	if !strings.Contains(recOut, "HARVEST RECOVERY OK") {
		t.Fatalf("expected HARVEST RECOVERY OK, got:\n%s", recOut)
	}
	if !strings.Contains(recOut, "retained=true") {
		t.Errorf("expected retained=true in recovery output, got:\n%s", recOut)
	}

	// 3. Test tampering detection
	if err := os.WriteFile(filepath.Join(expectedJobCapture, "usage.tsv"), []byte("corrupted"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	failCode := cmdHarvest([]string{
		"--recover", expectedJobCapture,
	}, &stdout, &stderr, now)
	if failCode == 0 {
		t.Fatalf("expected recovery to fail on tampered file, got 0")
	}
	if !strings.Contains(stderr.String(), "HARVEST RECOVERY REFUSED") {
		t.Errorf("expected HARVEST RECOVERY REFUSED in stderr, got:\n%s", stderr.String())
	}
}

func TestCmdHarvestHarnessFailureRetainsAll(t *testing.T) {
	temp := t.TempDir()
	working := filepath.Join(temp, "working")
	jobDir := filepath.Join(working, "tmp", "guid-failed", "jobs", "card-fail")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Failed job with harness failure
	if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), []byte("NATIVE FAIL label=card-fail rc=1 wall=10s harness=crash reason=oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "harness-output.log"), []byte("panic: memory limit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "usage.tsv"), []byte("tokens\tusd\n500\t0.005\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "work-in-progress.txt"), []byte("partial progress before crash\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	captureDir := filepath.Join(temp, "captures")
	var stdout, stderr bytes.Buffer
	now := time.Now().UTC()

	code := cmdHarvest([]string{
		"--working", working,
		"--capture-dir", captureDir,
	}, &stdout, &stderr, now)

	// Since job failed, harvest may return 1 (counts[classFailed] > 0)
	_ = code

	// Key: card-fail/attempt-1/card-fail
	expectedCapture := filepath.Join(captureDir, "card-fail", "attempt-1", "card-fail")

	// Verify capture has all files
	if _, err := os.Stat(filepath.Join(expectedCapture, "harness.log")); err != nil {
		t.Errorf("captured harness.log missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(expectedCapture, "harness-output.log")); err != nil {
		t.Errorf("captured harness-output.log missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(expectedCapture, "usage.tsv")); err != nil {
		t.Errorf("captured usage.tsv missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(expectedCapture, "deliverables", "work-in-progress.txt")); err != nil {
		t.Errorf("captured partial work missing: %v", err)
	}

	// Verify source job directory retains everything (no immediate deletion exception)
	if _, err := os.Stat(filepath.Join(jobDir, "work-in-progress.txt")); err != nil {
		t.Fatalf("source job partial edits deleted on failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "usage.tsv")); err != nil {
		t.Fatalf("source job usage.tsv deleted on failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "harness.log")); err != nil {
		t.Fatalf("source job harness.log deleted on failure: %v", err)
	}

	// Verify manifest
	rec, err := pulse.RecoverCapture(expectedCapture)
	if err != nil {
		t.Fatalf("RecoverCapture failed: %v", err)
	}
	if !rec.Manifest.Retained {
		t.Errorf("manifest Retained must be true")
	}
	if !rec.Manifest.DeletionDisabled {
		t.Errorf("manifest DeletionDisabled must be true")
	}
	if !rec.Manifest.HarnessFailure {
		t.Errorf("manifest HarnessFailure must be true")
	}
}
