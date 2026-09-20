package swarm_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// initFakeRepo initializes a git repo inside dir with an initial commit.
func initFakeRepo(t *testing.T, dir string) string {
	t.Helper()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Test Runner")
	runGit(t, dir, "config", "user.email", "test@mas-bandwidth.internal")
	runGit(t, dir, "config", "commit.gpgsign", "false")

	file := filepath.Join(dir, "README.md")
	if err := os.WriteFile(file, []byte("# Base Repo\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial commit")

	out := runGit(t, dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s failed: %v\nOutput:\n%s", strings.Join(args, " "), dir, err, string(out))
	}
	return string(out)
}

func TestCaptureKeyValidationAndPaths(t *testing.T) {
	// 1. Keyed by card + attempt + job (not bare label)
	// Validation tests
	badKeys := []swarm.CaptureKey{
		{Card: "", Attempt: 1, Job: "job-1"},
		{Card: "card-1", Attempt: 0, Job: "job-1"},
		{Card: "card-1", Attempt: -1, Job: "job-1"},
		{Card: "card-1", Attempt: 1, Job: ""},
	}
	for _, k := range badKeys {
		if err := k.Validate(); err == nil {
			t.Errorf("expected error for invalid key %+v, got nil", k)
		}
	}

	validKey1 := swarm.CaptureKey{Card: "card-1", Attempt: 1, Job: "job-1"}
	if err := validKey1.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	validKey2 := swarm.CaptureKey{Card: "card-1", Attempt: 2, Job: "job-1"}
	if err := validKey2.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	root := "/tmp/test-results"
	path1 := validKey1.ResultsDir(root)
	path2 := validKey2.ResultsDir(root)

	// Ensure attempt 1 and attempt 2 never collide
	if path1 == path2 {
		t.Fatalf("collision between attempt 1 and 2: %s", path1)
	}
	if !strings.Contains(path1, "attempt-1") || !strings.Contains(path2, "attempt-2") {
		t.Fatalf("paths must contain attempt numbers: %s, %s", path1, path2)
	}
	if !strings.Contains(path1, "card-1") || !strings.Contains(path1, "job-1") {
		t.Fatalf("paths must contain card and job: %s", path1)
	}
}

func TestCaptureManifestAndBundle(t *testing.T) {
	// Setup test directories
	tmp := t.TempDir()
	resultsRoot := filepath.Join(tmp, "results")
	jobDir := filepath.Join(tmp, "slot", "jobs", "card-8379")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir jobDir: %v", err)
	}

	// 1. Initialize repo inside jobDir
	repoDir := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	baseSHA := initFakeRepo(t, repoDir)

	// Card makes a commit
	newFile := filepath.Join(repoDir, "feature.go")
	if err := os.WriteFile(newFile, []byte("package feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.go: %v", err)
	}
	runGit(t, repoDir, "add", "feature.go")
	runGit(t, repoDir, "commit", "-m", "feat: card work committed")
	headSHA := strings.TrimSpace(runGit(t, repoDir, "rev-parse", "HEAD"))

	// 2. Write owned result, log, usage, timeline
	resultContent := "# RESULT\n\nAll green.\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(resultContent), 0o644); err != nil {
		t.Fatalf("write RESULT.md: %v", err)
	}
	logContent := "STARTING\nSTEP 1: compile\nPASSED\n"
	if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), []byte(logContent), 0o644); err != nil {
		t.Fatalf("write harness.log: %v", err)
	}
	usageContent := "job\tattempt\tstarted\tended\tend\trc\tprovider\tmodel\tusd\ncard-8379\t1\t2026-09-20T20:00:00Z\t2026-09-20T20:05:00Z\tdone\t0\topenai\tgpt-4\t0.05\n"
	if err := os.WriteFile(filepath.Join(jobDir, "usage.tsv"), []byte(usageContent), 0o644); err != nil {
		t.Fatalf("write usage.tsv: %v", err)
	}
	timelineContent := "turn\ttool\tcost\n1\tgit\t0.01\n"
	if err := os.WriteFile(filepath.Join(jobDir, "timeline.tsv"), []byte(timelineContent), 0o644); err != nil {
		t.Fatalf("write timeline.tsv: %v", err)
	}

	key := swarm.CaptureKey{
		Card:    "card-8379",
		Attempt: 1,
		Job:     "job-8379",
	}
	now := time.Now().UTC()
	in := swarm.CaptureInput{
		ResultsRoot: resultsRoot,
		JobDir:      jobDir,
		Key:         key,
		Provenance: swarm.CaptureProvenance{
			Host:         "hulk",
			Bench:        "bench-a",
			Model:        "openai/gpt-4",
			HarnessPath:  "/fake/harness",
			BinarySHA256: "abc123sha",
			CardSHA256:   "card123sha",
			ConfigSHA:    "cfg12345",
			WallBackend:  "darwin-sandbox",
		},
		ExecutorProof: swarm.ExecutorProof{
			PID:       12345,
			ExitCode:  0,
			EndedAt:   now,
			ExitKind:  "done",
			Completed: true,
		},
		StartedAt:   now.Add(-5 * time.Minute),
		EndedAt:     now,
		WallSeconds: 300.0,
		BaseSHA:     baseSHA,
	}

	ctx := context.Background()
	manifest, err := swarm.CaptureJob(ctx, in)
	if err != nil {
		t.Fatalf("CaptureJob failed: %v", err)
	}

	// Verify manifest contents
	if manifest.Key != key {
		t.Errorf("manifest key mismatch: got %+v, want %+v", manifest.Key, key)
	}
	if manifest.Result == nil || manifest.Result.Path != "RESULT.md" {
		t.Errorf("manifest result missing or invalid: %+v", manifest.Result)
	}
	if manifest.Usage == nil || manifest.Usage.Path != "usage.tsv" {
		t.Errorf("manifest usage missing or invalid: %+v", manifest.Usage)
	}
	if manifest.Timeline == nil || manifest.Timeline.Path != "timeline.tsv" {
		t.Errorf("manifest timeline missing or invalid: %+v", manifest.Timeline)
	}
	if manifest.Bundle == nil {
		t.Fatalf("manifest bundle missing")
	}
	if manifest.Bundle.Head != headSHA {
		t.Errorf("bundle head mismatch: got %s, want %s", manifest.Bundle.Head, headSHA)
	}
	if !manifest.Bundle.Verified {
		t.Errorf("bundle should be marked verified")
	}
	if !manifest.DeletionDisabled {
		t.Errorf("deletion_disabled must be true")
	}

	// Check files in results directory
	resDir := key.ResultsDir(resultsRoot)
	if _, err := os.Stat(filepath.Join(resDir, swarm.ManifestFileName)); err != nil {
		t.Errorf("manifest.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(resDir, swarm.ManifestDigestFileName)); err != nil {
		t.Errorf("manifest.digest missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(resDir, swarm.BundleFileName)); err != nil {
		t.Errorf("branch.bundle missing: %v", err)
	}

	// Verify bundle file directly with git bundle verify
	runGit(t, repoDir, "bundle", "verify", filepath.Join(resDir, swarm.BundleFileName))

	// Verify readback verification succeeds
	verified, err := swarm.VerifyCapture(resDir)
	if err != nil {
		t.Fatalf("VerifyCapture failed: %v", err)
	}
	if verified.Key != key {
		t.Errorf("readback manifest key mismatch: %+v", verified.Key)
	}
}

func TestCaptureUncommittedDeliverables(t *testing.T) {
	tmp := t.TempDir()
	resultsRoot := filepath.Join(tmp, "results")
	jobDir := filepath.Join(tmp, "job-dirty")
	repoDir := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_ = initFakeRepo(t, repoDir)

	// Modify a tracked file (uncommitted modification)
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Base Repo\nModified work\n"), 0o644); err != nil {
		t.Fatalf("modify README: %v", err)
	}

	// Create an untracked file (deliverable)
	if err := os.WriteFile(filepath.Join(repoDir, "deliverable.txt"), []byte("important output data\n"), 0o644); err != nil {
		t.Fatalf("write deliverable.txt: %v", err)
	}

	// Create a cache directory that MUST BE EXCLUDED
	cacheDir := filepath.Join(repoDir, "gomod", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "cached.bin"), []byte("big cache"), 0o644); err != nil {
		t.Fatalf("write cache file: %v", err)
	}

	// Create a symlink that MUST BE EXCLUDED
	extTarget := filepath.Join(tmp, "outside.txt")
	if err := os.WriteFile(extTarget, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(extTarget, filepath.Join(repoDir, "symlink_outside")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	key := swarm.CaptureKey{Card: "card-dirty", Attempt: 1, Job: "job-dirty"}
	now := time.Now().UTC()
	in := swarm.CaptureInput{
		ResultsRoot: resultsRoot,
		JobDir:      jobDir,
		Key:         key,
		Provenance: swarm.CaptureProvenance{
			Host:  "hulk",
			Model: "anthropic/claude-3-7",
		},
		ExecutorProof: swarm.ExecutorProof{
			ExitCode:  0,
			EndedAt:   now,
			ExitKind:  "done",
			Completed: true,
		},
		StartedAt:   now.Add(-time.Minute),
		EndedAt:     now,
		WallSeconds: 60.0,
	}

	manifest, err := swarm.CaptureJob(context.Background(), in)
	if err != nil {
		t.Fatalf("CaptureJob failed: %v", err)
	}

	// Verify uncommitted deliverables were captured
	foundTracked := false
	foundUntracked := false
	for _, d := range manifest.Deliverables {
		if d.Path == "README.md" && d.Kind == swarm.DeliverableTrackedModified {
			foundTracked = true
		}
		if d.Path == "deliverable.txt" && d.Kind == swarm.DeliverableUntracked {
			foundUntracked = true
		}
		if strings.Contains(d.Path, "cached.bin") {
			t.Errorf("cache file must NOT be captured as deliverable: %s", d.Path)
		}
		if strings.Contains(d.Path, "symlink_outside") {
			t.Errorf("symlink must NOT be captured as deliverable: %s", d.Path)
		}
	}
	if !foundTracked {
		t.Errorf("expected modified README.md in deliverables")
	}
	if !foundUntracked {
		t.Errorf("expected deliverable.txt in deliverables")
	}

	resDir := key.ResultsDir(resultsRoot)
	// Verify deliverable files exist on disk in results dir
	if _, err := os.Stat(filepath.Join(resDir, "deliverables", "deliverable.txt")); err != nil {
		t.Errorf("deliverable.txt missing from results dir: %v", err)
	}

	// Readback verification must succeed
	if _, err := swarm.VerifyCapture(resDir); err != nil {
		t.Fatalf("VerifyCapture failed: %v", err)
	}
}

func TestVerifyCaptureTamperDetection(t *testing.T) {
	tmp := t.TempDir()
	resultsRoot := filepath.Join(tmp, "results")
	jobDir := filepath.Join(tmp, "job-tamper")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte("# Clean\n"), 0o644); err != nil {
		t.Fatalf("write RESULT: %v", err)
	}
	key := swarm.CaptureKey{Card: "card-tamper", Attempt: 1, Job: "job-tamper"}
	now := time.Now().UTC()
	in := swarm.CaptureInput{
		ResultsRoot: resultsRoot,
		JobDir:      jobDir,
		Key:         key,
		ExecutorProof: swarm.ExecutorProof{
			ExitCode:  0,
			EndedAt:   now,
			Completed: true,
			ExitKind:  "done",
		},
		StartedAt: now.Add(-10 * time.Second),
		EndedAt:   now,
	}

	_, err := swarm.CaptureJob(context.Background(), in)
	if err != nil {
		t.Fatalf("CaptureJob failed: %v", err)
	}
	resDir := key.ResultsDir(resultsRoot)

	// 1. Tamper manifest.digest
	digestFile := filepath.Join(resDir, swarm.ManifestDigestFileName)
	origDigest, err := os.ReadFile(digestFile)
	if err != nil {
		t.Fatalf("read digest: %v", err)
	}

	badDigest := strings.Replace(string(origDigest), string(origDigest[:4]), "dead", 1)
	if err := os.WriteFile(digestFile, []byte(badDigest), 0o644); err != nil {
		t.Fatalf("write bad digest: %v", err)
	}
	if _, err := swarm.VerifyCapture(resDir); err == nil {
		t.Errorf("expected error for tampered manifest.digest, got nil")
	}

	// Restore digest
	if err := os.WriteFile(digestFile, origDigest, 0o644); err != nil {
		t.Fatalf("restore digest: %v", err)
	}
	if _, err := swarm.VerifyCapture(resDir); err != nil {
		t.Fatalf("VerifyCapture should succeed after restore: %v", err)
	}

	// 2. Tamper an artifact file (RESULT.md)
	resFile := filepath.Join(resDir, "RESULT.md")
	if err := os.WriteFile(resFile, []byte("# Tampered content\n"), 0o644); err != nil {
		t.Fatalf("tamper result: %v", err)
	}
	if _, err := swarm.VerifyCapture(resDir); err == nil {
		t.Errorf("expected error for tampered RESULT.md sha256 mismatch, got nil")
	}
}

func TestFinalizerPreservesWorkingDir(t *testing.T) {
	// 5. Single finalizer owns deletion, and deletion is strictly DISABLED in this PR.
	tmp := t.TempDir()
	resultsRoot := filepath.Join(tmp, "results")
	jobDir := filepath.Join(tmp, "job-finalize")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte("# OK\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	key := swarm.CaptureKey{Card: "card-fin", Attempt: 1, Job: "job-fin"}
	now := time.Now().UTC()
	in := swarm.FinalizeInput{
		ResultsRoot: resultsRoot,
		JobDir:      jobDir,
		Key:         key,
		Proof: swarm.ExecutorProof{
			ExitCode:  0,
			EndedAt:   now,
			Completed: true,
			ExitKind:  "done",
		},
		StartedAt:   now.Add(-time.Second),
		EndedAt:     now,
		WallSeconds: 1.0,
	}

	res, err := swarm.FinalizeJob(context.Background(), in)
	if err != nil {
		t.Fatalf("FinalizeJob failed: %v", err)
	}
	if !res.Verified {
		t.Errorf("FinalizeResult should be verified")
	}
	if !res.WorkingDirKept {
		t.Errorf("WorkingDirKept must be true")
	}
	if res.DeletionSkipped != "deletion strictly disabled in this PR" {
		t.Errorf("unexpected deletion skipped message: %s", res.DeletionSkipped)
	}

	// Assert the working directory was NOT deleted!
	if fi, err := os.Stat(jobDir); err != nil || !fi.IsDir() {
		t.Fatalf("working directory was deleted or missing! It must be preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "RESULT.md")); err != nil {
		t.Fatalf("working directory content was removed: %v", err)
	}
}

func TestCaptureWithoutResultRetainsPartialEdits(t *testing.T) {
	tmp := t.TempDir()
	resultsRoot := filepath.Join(tmp, "results")
	jobDir := filepath.Join(tmp, "job-silent")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No RESULT.md exists (card was silent or harness failed)
	logContent := "STARTING\nharness died unexpectedly\n"
	if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), []byte(logContent), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	scratchFile := filepath.Join(jobDir, "partial_edit.txt")
	if err := os.WriteFile(scratchFile, []byte("partial model output"), 0o644); err != nil {
		t.Fatalf("write partial: %v", err)
	}

	key := swarm.CaptureKey{Card: "card-silent", Attempt: 1, Job: "job-silent"}
	now := time.Now().UTC()
	in := swarm.FinalizeInput{
		ResultsRoot: resultsRoot,
		JobDir:      jobDir,
		Key:         key,
		Proof: swarm.ExecutorProof{
			ExitCode:  1,
			EndedAt:   now,
			Completed: true,
			ExitKind:  "silent",
		},
		StartedAt: now.Add(-time.Second),
		EndedAt:   now,
	}

	res, err := swarm.FinalizeJob(context.Background(), in)
	if err != nil {
		t.Fatalf("FinalizeJob failed: %v", err)
	}
	if res.Manifest.Result != nil {
		t.Errorf("manifest result should be nil when no RESULT.md was published")
	}
	if len(res.Manifest.Logs) == 0 {
		t.Errorf("harness.log should still be captured for silent/failed run")
	}
	// Working dir must still be kept
	if !res.WorkingDirKept {
		t.Errorf("working dir must be kept on failure")
	}
	if _, err := os.Stat(scratchFile); err != nil {
		t.Errorf("scratch file in working dir must be preserved: %v", err)
	}
}
