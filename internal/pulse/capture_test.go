package pulse

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Helper to initialize a git repo for testing bundle and partial edits capture.
func initTestRepo(t *testing.T, dir string) {
	t.Helper()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	runGit("init")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "initial.txt"), []byte("base version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "initial.txt")
	runGit("commit", "-m", "initial commit")
}

// 1. Remove the harness-failure immediate deletion exception:
// failed/aborted jobs retain partial edits, usage, and logs.
func TestCaptureFailedJobRetainsPartialEditsUsageLogs(t *testing.T) {
	temp := t.TempDir()
	jobDir := filepath.Join(temp, "job-failed-1")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestRepo(t, jobDir)

	// Partial edits: modify tracked file
	if err := os.WriteFile(filepath.Join(jobDir, "initial.txt"), []byte("partially edited work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Partial edits: add untracked file
	if err := os.WriteFile(filepath.Join(jobDir, "untracked.go"), []byte("package test\n// uncommitted deliverable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Logs: harness failure logs
	harnessLog := "NATIVE FAIL label=card-fail rc=1 wall=15.2s harness=aborted reason=model-timeout\n"
	if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), []byte(harnessLog), 0o644); err != nil {
		t.Fatal(err)
	}
	harnessOut := "Error: model timed out after 300s\npartial transcript trace...\n"
	if err := os.WriteFile(filepath.Join(jobDir, "harness-output.log"), []byte(harnessOut), 0o644); err != nil {
		t.Fatal(err)
	}
	// Usage: usage spend
	usageContent := "input_tokens\toutput_tokens\tusd\n1500\t350\t0.0125\n"
	if err := os.WriteFile(filepath.Join(jobDir, "usage.tsv"), []byte(usageContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outputRoot := filepath.Join(temp, "results")
	key := CaptureKey{Card: "card-2407", Attempt: 1, Job: "job-failed-1"}

	opts := CaptureJobOptions{
		Key:            key,
		JobDir:         jobDir,
		OutputRoot:     outputRoot,
		Label:          "card-2407",
		Bench:          "bench-alpha",
		Model:          "model-pro",
		ExitKind:       "harness-failure",
		HarnessFailure: true,
	}

	manifest, err := CaptureJob(opts)
	if err != nil {
		t.Fatalf("CaptureJob failed: %v", err)
	}

	// Verify manifest metadata
	if !manifest.HarnessFailure {
		t.Errorf("expected HarnessFailure=true in manifest")
	}
	if !manifest.Retained {
		t.Errorf("expected Retained=true in manifest")
	}
	if !manifest.DeletionDisabled {
		t.Errorf("expected DeletionDisabled=true in manifest")
	}

	// Verify that source job directory was NOT deleted (harness-failure immediate deletion exception removed)
	if _, err := os.Stat(jobDir); err != nil {
		t.Fatalf("source job directory was deleted; harness failure exception must NOT delete job: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "initial.txt")); err != nil {
		t.Errorf("partial edits lost in source job dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "untracked.go")); err != nil {
		t.Errorf("untracked file lost in source job dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "usage.tsv")); err != nil {
		t.Errorf("usage.tsv lost in source job dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "harness.log")); err != nil {
		t.Errorf("harness.log lost in source job dir: %v", err)
	}

	// Verify that capture destination directory contains partial edits, usage, and logs
	captureDir := CapturePath(outputRoot, key)
	if _, err := os.Stat(filepath.Join(captureDir, "work.patch")); err != nil {
		t.Errorf("capture directory missing work.patch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(captureDir, "deliverables", "untracked.go")); err != nil {
		t.Errorf("capture directory missing untracked deliverable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(captureDir, "usage.tsv")); err != nil {
		t.Errorf("capture directory missing usage.tsv: %v", err)
	}
	if _, err := os.Stat(filepath.Join(captureDir, "harness.log")); err != nil {
		t.Errorf("capture directory missing harness.log: %v", err)
	}
	if _, err := os.Stat(filepath.Join(captureDir, "harness-output.log")); err != nil {
		t.Errorf("capture directory missing harness-output.log: %v", err)
	}
}

// 2. Key outputs by card + attempt + job.
func TestCaptureKeyedByCardAttemptJob(t *testing.T) {
	temp := t.TempDir()
	jobDir := filepath.Join(temp, "slot-1", "jobs", "j1")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outputRoot := filepath.Join(temp, "captures")
	key := CaptureKey{
		Card:    "card-audit-99",
		Attempt: 3,
		Job:     "job-worker-xyz",
	}

	expectedRelPath := filepath.Join("card-audit-99", "attempt-3", "job-worker-xyz")
	actualPath := CapturePath(outputRoot, key)
	expectedFullPath := filepath.Join(outputRoot, expectedRelPath)
	if actualPath != expectedFullPath {
		t.Fatalf("CapturePath mismatch: got %s, want %s", actualPath, expectedFullPath)
	}

	opts := CaptureJobOptions{
		Key:        key,
		JobDir:     jobDir,
		OutputRoot: outputRoot,
		ExitKind:   "ok",
	}

	manifest, err := CaptureJob(opts)
	if err != nil {
		t.Fatalf("CaptureJob failed: %v", err)
	}

	if manifest.Key != key {
		t.Fatalf("manifest key mismatch: got %+v, want %+v", manifest.Key, key)
	}

	// Verify manifest.json exists at key path
	manifestPath := filepath.Join(actualPath, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest.json not found at %s: %v", manifestPath, err)
	}
}

// 3. Capture recovery: verify manifest digest and read back artifacts.
func TestCaptureRecoveryVerifiesManifestDigestAndReadsBackArtifacts(t *testing.T) {
	temp := t.TempDir()
	jobDir := filepath.Join(temp, "job-to-recover")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestRepo(t, jobDir)

	// Make a committed change in the repo so branch.bundle is created
	if err := os.WriteFile(filepath.Join(jobDir, "feature.txt"), []byte("new feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "feature.txt")
	cmd.Dir = jobDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("git", "commit", "-m", "add feature")
	cmd.Dir = jobDir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@e.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@e.com")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Also make an uncommitted change for partial edits
	if err := os.WriteFile(filepath.Join(jobDir, "feature.txt"), []byte("new feature + uncommitted edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "untracked.md"), []byte("# Notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resultContent := "RESULT fixed card=c-rec branch=rowan/test pr=42\nAll tests green\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(resultContent), 0o644); err != nil {
		t.Fatal(err)
	}
	usageContent := "input_tokens\toutput_tokens\tusd\n1000\t200\t0.005\n"
	if err := os.WriteFile(filepath.Join(jobDir, "usage.tsv"), []byte(usageContent), 0o644); err != nil {
		t.Fatal(err)
	}
	harnessLog := "NATIVE OK label=c-rec rc=0 wall=4.2s harness=ok reason=fixed\n"
	if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), []byte(harnessLog), 0o644); err != nil {
		t.Fatal(err)
	}

	outputRoot := filepath.Join(temp, "results")
	key := CaptureKey{Card: "card-rec", Attempt: 2, Job: "job-to-recover"}

	opts := CaptureJobOptions{
		Key:        key,
		JobDir:     jobDir,
		OutputRoot: outputRoot,
		Label:      "card-rec",
		ExitKind:   "fixed",
	}

	manifest, err := CaptureJob(opts)
	if err != nil {
		t.Fatalf("CaptureJob failed: %v", err)
	}

	captureDir := CapturePath(outputRoot, key)

	// Step 3a: Verify manifest digest passes on untouched capture
	if err := VerifyManifestDigest(captureDir); err != nil {
		t.Fatalf("VerifyManifestDigest failed on valid capture: %v", err)
	}

	// Step 3b: Verify tampering detection: alter one file
	tamperPath := filepath.Join(captureDir, "usage.tsv")
	origUsage, _ := os.ReadFile(tamperPath)
	if err := os.WriteFile(tamperPath, []byte("tampered content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifestDigest(captureDir); err == nil {
		t.Fatalf("VerifyManifestDigest should fail after tampering with usage.tsv")
	}
	// Restore original file
	if err := os.WriteFile(tamperPath, origUsage, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifestDigest(captureDir); err != nil {
		t.Fatalf("VerifyManifestDigest failed after restoring usage.tsv: %v", err)
	}

	// Step 3c: Delete the original jobDir completely to test independent recovery
	if err := os.RemoveAll(jobDir); err != nil {
		t.Fatal(err)
	}

	// Step 3d: Read back artifacts via RecoverCapture
	recovered, err := RecoverCapture(captureDir)
	if err != nil {
		t.Fatalf("RecoverCapture failed: %v", err)
	}

	if recovered.Key != key {
		t.Errorf("recovered key mismatch: got %+v, want %+v", recovered.Key, key)
	}
	if recovered.ResultMD != resultContent {
		t.Errorf("recovered ResultMD mismatch: got %q, want %q", recovered.ResultMD, resultContent)
	}
	if recovered.UsageTSV != usageContent {
		t.Errorf("recovered UsageTSV mismatch: got %q, want %q", recovered.UsageTSV, usageContent)
	}
	if string(recovered.Logs["harness.log"]) != harnessLog {
		t.Errorf("recovered harness.log mismatch: got %q, want %q", string(recovered.Logs["harness.log"]), harnessLog)
	}
	if len(recovered.Bundle) == 0 {
		t.Errorf("recovered branch.bundle is empty")
	}
	if len(recovered.PartialPatch) == 0 {
		t.Errorf("recovered partial work.patch is empty")
	}
	if len(recovered.Deliverables) == 0 {
		t.Errorf("recovered deliverables missing untracked files")
	}

	// Test restoring artifacts to a brand new directory
	restoreDir := filepath.Join(temp, "restored-job")
	if err := recovered.RestoreTo(restoreDir); err != nil {
		t.Fatalf("RestoreTo failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "RESULT.md")); err != nil {
		t.Errorf("restored RESULT.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "usage.tsv")); err != nil {
		t.Errorf("restored usage.tsv missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "branch.bundle")); err != nil {
		t.Errorf("restored branch.bundle missing: %v", err)
	}

	_ = manifest
}

// 4. Keep deletion strictly DISABLED.
func TestCaptureDeletionStrictlyDisabled(t *testing.T) {
	if DeletionEnabled {
		t.Fatalf("DeletionEnabled must be false; deletion is strictly DISABLED")
	}

	temp := t.TempDir()
	jobDir := filepath.Join(temp, "job-to-keep")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(jobDir, "keepme.txt")
	if err := os.WriteFile(testFile, []byte("preserve this\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outputRoot := filepath.Join(temp, "results")
	key := CaptureKey{Card: "card-keep", Attempt: 1, Job: "job-to-keep"}

	opts := CaptureJobOptions{
		Key:        key,
		JobDir:     jobDir,
		OutputRoot: outputRoot,
		ExitKind:   "failed",
	}

	_, err := CaptureJob(opts)
	if err != nil {
		t.Fatalf("CaptureJob failed: %v", err)
	}

	// Attempt finalization / deletion
	captureDir := CapturePath(outputRoot, key)
	err = FinalizeJob(captureDir, jobDir)
	if err == nil {
		t.Fatalf("FinalizeJob must fail when deletion is disabled")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("expected disabled error, got: %v", err)
	}

	// Verify jobDir was NOT deleted
	if _, err := os.Stat(testFile); err != nil {
		t.Fatalf("jobDir was deleted despite deletion disabled: %v", err)
	}
}

// Test Symlink safety and outside references exclusion
func TestCaptureSymlinkAndMirrorExclusion(t *testing.T) {
	temp := t.TempDir()
	outsideDir := filepath.Join(temp, "outside-secret-mirror")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.key"), []byte("sensitive\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	jobDir := filepath.Join(temp, "job-symlink")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "normal.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Symlink pointing outside
	symlinkPath := filepath.Join(jobDir, "escape-link")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Fatal(err)
	}

	outputRoot := filepath.Join(temp, "results")
	key := CaptureKey{Card: "card-sym", Attempt: 1, Job: "job-symlink"}

	opts := CaptureJobOptions{
		Key:        key,
		JobDir:     jobDir,
		OutputRoot: outputRoot,
		ExitKind:   "ok",
	}

	manifest, err := CaptureJob(opts)
	if err != nil {
		t.Fatalf("CaptureJob failed: %v", err)
	}

	// Verify that secret.key from outside was NOT captured
	for path := range manifest.Files {
		if strings.Contains(path, "secret.key") {
			t.Errorf("symlink to outside directory leaked into capture: %s", path)
		}
	}
}
