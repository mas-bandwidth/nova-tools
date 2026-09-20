package pulse_test

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// Helper: run git in a directory with hermetic environment.
func runGitCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Emma Antigravity",
		"GIT_AUTHOR_EMAIL=emma@mas-bandwidth.internal",
		"GIT_COMMITTER_NAME=Emma Antigravity",
		"GIT_COMMITTER_EMAIL=emma@mas-bandwidth.internal",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s failed: %v\nOutput:\n%s", strings.Join(args, " "), dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

// Helper: initialize a fake git repository with a base commit and working branch.
func setupFakeJobRepo(t *testing.T, repoDir string) (baseSHA, headSHA string) {
	t.Helper()
	runGitCommand(t, repoDir, "init", "-b", "main")
	runGitCommand(t, repoDir, "config", "user.name", "Emma Antigravity")
	runGitCommand(t, repoDir, "config", "user.email", "emma@mas-bandwidth.internal")
	runGitCommand(t, repoDir, "config", "commit.gpgsign", "false")

	// Base commit on main
	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("# Bench and Manifest Repo\nInitial base content.\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runGitCommand(t, repoDir, "add", "README.md")
	runGitCommand(t, repoDir, "commit", "-m", "base: initial repository structure")
	baseSHA = runGitCommand(t, repoDir, "rev-parse", "HEAD")

	// Create and checkout branch for the job
	runGitCommand(t, repoDir, "checkout", "-b", "job/attempt-1")

	// Commit some work on the branch
	codeFile := filepath.Join(repoDir, "service.go")
	codeContent := []byte("package main\n\nfunc RunService() string {\n\treturn \"active\"\n}\n")
	if err := os.WriteFile(codeFile, codeContent, 0o644); err != nil {
		t.Fatalf("write service.go: %v", err)
	}
	runGitCommand(t, repoDir, "add", "service.go")
	runGitCommand(t, repoDir, "commit", "-m", "feat: implement service logic for job attempt 1")
	headSHA = runGitCommand(t, repoDir, "rev-parse", "HEAD")

	return baseSHA, headSHA
}

// 1. Test invalid bench updates refuse admission without fallback (#2164).
// Validates:
// - Atomic versioned record with monotonic revision tracking.
// - Invalid requested settings or observed watermarks (negative share, NaN load, Inf metrics, stale revision).
// - Rejection refuses new admission without fallback to zero (no false idle) or unlimited (no brake bypass).
// - Persisted invalid record preserves invalid state and refuses admission after readback.
func TestIntegration_InvalidBenchUpdatesRefuseAdmissionWithoutFallback(t *testing.T) {
	settings := pulse.RequestedSettings{
		Share:              24,
		MaxLoadPerCore:     2.0,
		MinDiskFreeGB:      30.0,
		MinMemFreeGB:       8.0,
		MinGBPerCard:       1.0,
		ProbeBudgetSeconds: 45,
		Drain:              false,
	}

	rec, err := pulse.NewBenchRecord("hetzner", settings)
	if err != nil {
		t.Fatalf("NewBenchRecord failed: %v", err)
	}

	// Normal admission works initially
	snapInit := rec.Snapshot()
	if snapInit.Revision != 1 {
		t.Fatalf("initial revision must be 1, got %d", snapInit.Revision)
	}
	if ok, reason := rec.CanAdmit(2); !ok {
		t.Fatalf("expected initial admission to succeed, got refused: %s", reason)
	}

	// Sub-test A: Invalid negative share update must be rejected
	badSettingsNegativeShare := settings
	badSettingsNegativeShare.Share = -12
	err = rec.UpdateRequested(1, badSettingsNegativeShare)
	if err == nil {
		t.Fatal("expected error on negative share update, got nil")
	}
	if !errors.Is(err, pulse.ErrInvalidUpdate) {
		t.Fatalf("expected ErrInvalidUpdate, got %v", err)
	}

	// Sub-test B: Invalid NaN in load telemetry must be rejected
	badObsNaN := pulse.ObservedWatermark{
		ObservedLoad1: math.NaN(),
		ObservedAt:    time.Now(),
	}
	err = rec.UpdateObserved(1, badObsNaN)
	if err == nil {
		t.Fatal("expected error on NaN load telemetry update, got nil")
	}
	if !errors.Is(err, pulse.ErrInvalidUpdate) {
		t.Fatalf("expected ErrInvalidUpdate, got %v", err)
	}

	// Sub-test C: Stale revision update must be rejected
	validObs := pulse.ObservedWatermark{
		CurrentHeld:         4,
		PeakHeld:            8,
		ObservedLoad1:       4.0,
		ObservedLoadPerCore: 0.5,
		FreeDiskGB:          80.0,
		FreeMemGB:           32.0,
		Cores:               8,
		ProbeSuccess:        true,
		ObservedAt:          time.Now(),
	}
	err = rec.UpdateObserved(999, validObs) // wrong revision 999 != 1
	if err == nil {
		t.Fatal("expected error on stale revision update, got nil")
	}
	if !errors.Is(err, pulse.ErrStaleRevision) {
		t.Fatalf("expected ErrStaleRevision, got %v", err)
	}

	// Record revision remains 1 after rejected updates
	if rec.Snapshot().Revision != 1 {
		t.Fatalf("revision changed on rejected updates: got %d, want 1", rec.Snapshot().Revision)
	}

	// Sub-test D: Explicit invalid marking transitions to AdmissionInvalid
	rec.MarkInvalid(errors.New("critical disk I/O corruption detected"))
	snapInvalid := rec.Snapshot()
	if snapInvalid.Enforced.AdmissionState != pulse.AdmissionInvalid {
		t.Fatalf("expected admission state %q, got %q", pulse.AdmissionInvalid, snapInvalid.Enforced.AdmissionState)
	}

	// Invariant Check: Admission MUST be refused without fallback
	// 1. Asking for 1 slot refuses
	ok1, reason1 := rec.CanAdmit(1)
	if ok1 {
		t.Fatal("CanAdmit(1) succeeded on invalid record! Must refuse admission")
	}
	if !strings.Contains(reason1, "refused: invalid update") {
		t.Fatalf("refusal reason must indicate invalid update, got: %s", reason1)
	}
	if !strings.Contains(reason1, "never falling back to zero or unlimited") {
		t.Fatalf("refusal reason must assert no zero/unlimited fallback, got: %s", reason1)
	}

	// 2. Asking for 0 slots MUST ALSO refuse (no fallback to zero / false idle)
	ok0, reason0 := rec.CanAdmit(0)
	if ok0 {
		t.Fatal("CanAdmit(0) succeeded on invalid record! Zero fallback is strictly forbidden")
	}
	if reason0 == "" {
		t.Fatal("refusal reason for CanAdmit(0) must not be empty on invalid record")
	}

	// 3. Asking for large slots MUST ALSO refuse (no fallback to unlimited)
	okInf, _ := rec.CanAdmit(math.MaxInt32)
	if okInf {
		t.Fatal("CanAdmit(MaxInt32) succeeded on invalid record! Unlimited fallback is strictly forbidden")
	}

	// 4. AdmitLease returns error on invalid state
	if err := rec.AdmitLease(1); err == nil {
		t.Fatal("AdmitLease(1) succeeded on invalid record, expected error")
	}

	// Sub-test E: Atomic persistence and readback retains invalid refusal
	tmpFile := filepath.Join(t.TempDir(), "hetzner_bench.json")
	if err := rec.SaveAtomic(tmpFile); err != nil {
		t.Fatalf("SaveAtomic failed: %v", err)
	}

	readbackRec, err := pulse.ReadBenchRecord(tmpFile)
	if err != nil {
		t.Fatalf("ReadBenchRecord failed: %v", err)
	}
	readSnap := readbackRec.Snapshot()
	if readSnap.Enforced.AdmissionState != pulse.AdmissionInvalid {
		t.Fatalf("readback admission state must be %q, got %q", pulse.AdmissionInvalid, readSnap.Enforced.AdmissionState)
	}
	if ok, _ := readbackRec.CanAdmit(1); ok {
		t.Fatal("readback record allowed admission on invalid record")
	}
	if ok, _ := readbackRec.CanAdmit(0); ok {
		t.Fatal("readback record allowed admission(0) on invalid record")
	}
}

// 2. Test fake job directory with commit becomes results dir with verifiable bundle (#2379).
// Validates:
// - Job directory with git repo, branch commits, RESULT.md, logs, and usage.
// - CaptureJob produces results dir at <resultsRoot>/<card>/attempt-<attempt>/<job>.
// - Verified git bundle created via `git bundle create` + `git bundle verify`.
// - Manifest records provenance, executor proof, bundle SHA, and artifact records.
func TestIntegration_FakeJobCommitBecomesResultsDirWithVerifiableBundle(t *testing.T) {
	tmp := t.TempDir()
	resultsRoot := filepath.Join(tmp, "results")
	jobDir := filepath.Join(tmp, "slot", "jobs", "card-2379-job")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir jobDir: %v", err)
	}

	// 1. Setup git repo inside jobDir
	repoDir := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repoDir: %v", err)
	}
	baseSHA, headSHA := setupFakeJobRepo(t, repoDir)

	// 2. Setup job artifacts: RESULT.md, logs, usage.tsv, timeline.tsv
	resultMD := "# RESULT\n\nIntegration test run passed with exit code 0.\nAll invariants verified.\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(resultMD), 0o644); err != nil {
		t.Fatalf("write RESULT.md: %v", err)
	}
	logData := "STEP 1: setup sandbox\nSTEP 2: compile\nSTEP 3: test passed\n"
	if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), []byte(logData), 0o644); err != nil {
		t.Fatalf("write harness.log: %v", err)
	}
	usageTSV := "job\tattempt\tstarted\tended\tend\trc\tprovider\tmodel\tusd\ncard-2379-job\t1\t2026-09-20T21:00:00Z\t2026-09-20T21:05:00Z\tdone\t0\tanthropic\tclaude-3-7\t0.12\n"
	if err := os.WriteFile(filepath.Join(jobDir, "usage.tsv"), []byte(usageTSV), 0o644); err != nil {
		t.Fatalf("write usage.tsv: %v", err)
	}
	timelineTSV := "timestamp\tevent\tcost\n2026-09-20T21:00:01Z\ttool:compile\t0.00\n"
	if err := os.WriteFile(filepath.Join(jobDir, "timeline.tsv"), []byte(timelineTSV), 0o644); err != nil {
		t.Fatalf("write timeline.tsv: %v", err)
	}

	// 3. Define capture input
	key := swarm.CaptureKey{
		Card:    "card-2379-job",
		Attempt: 1,
		Job:     "job-attempt-1",
	}
	now := time.Now().UTC()
	in := swarm.CaptureInput{
		ResultsRoot: resultsRoot,
		JobDir:      jobDir,
		Key:         key,
		Provenance: swarm.CaptureProvenance{
			Host:         "hulk",
			Bench:        "hetzner",
			Model:        "anthropic/claude-3-7",
			HarnessPath:  "/opt/nova/harness",
			BinarySHA256: "b0fa1234567890abcdef",
			CardSHA256:   "card9876543210fedcba",
			ConfigSHA:    "cfg5555555555aaaaaa",
			WallBackend:  "darwin-sandbox",
		},
		ExecutorProof: swarm.ExecutorProof{
			PID:       9876,
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

	// 4. Perform CaptureJob
	ctx := context.Background()
	manifest, err := swarm.CaptureJob(ctx, in)
	if err != nil {
		t.Fatalf("CaptureJob failed: %v", err)
	}

	// 5. Verify Results Directory structure: <resultsRoot>/<card>/attempt-<attempt>/<job>
	expectedResultsDir := filepath.Join(resultsRoot, "card-2379-job", "attempt-1", "job-attempt-1")
	actualResultsDir := key.ResultsDir(resultsRoot)
	if actualResultsDir != expectedResultsDir {
		t.Fatalf("results dir mismatch: got %s, want %s", actualResultsDir, expectedResultsDir)
	}

	// Check manifest file existence
	manifestFile := filepath.Join(expectedResultsDir, swarm.ManifestFileName)
	if fi, err := os.Stat(manifestFile); err != nil || fi.Size() == 0 {
		t.Fatalf("manifest.json missing or empty: %v", err)
	}
	digestFile := filepath.Join(expectedResultsDir, swarm.ManifestDigestFileName)
	if fi, err := os.Stat(digestFile); err != nil || fi.Size() == 0 {
		t.Fatalf("manifest.digest missing or empty: %v", err)
	}

	// Check git bundle file existence and verification
	bundleFile := filepath.Join(expectedResultsDir, swarm.BundleFileName)
	if fi, err := os.Stat(bundleFile); err != nil || fi.Size() == 0 {
		t.Fatalf("branch.bundle missing or empty: %v", err)
	}

	// Verify bundle in repo context
	runGitCommand(t, repoDir, "bundle", "verify", bundleFile)

	// Verify manifest bundle fields
	if manifest.Bundle == nil {
		t.Fatalf("manifest.Bundle is nil")
	}
	if manifest.Bundle.Head != headSHA {
		t.Errorf("manifest.Bundle.Head mismatch: got %s, want %s", manifest.Bundle.Head, headSHA)
	}
	if !manifest.Bundle.Verified {
		t.Errorf("manifest.Bundle.Verified must be true")
	}
	if manifest.Bundle.SHA256 == "" {
		t.Errorf("manifest.Bundle.SHA256 must not be empty")
	}

	// Verify artifacts were captured and hashed
	if manifest.Result == nil || manifest.Result.Path != "RESULT.md" || manifest.Result.SHA256 == "" {
		t.Errorf("manifest.Result invalid: %+v", manifest.Result)
	}
	if manifest.Usage == nil || manifest.Usage.Path != "usage.tsv" || manifest.Usage.SHA256 == "" {
		t.Errorf("manifest.Usage invalid: %+v", manifest.Usage)
	}
	if manifest.Timeline == nil || manifest.Timeline.Path != "timeline.tsv" || manifest.Timeline.SHA256 == "" {
		t.Errorf("manifest.Timeline invalid: %+v", manifest.Timeline)
	}

	// Unbundle into a destination repo containing the base commit to prove bundle commits are verifiable and restorable
	restoreDir := filepath.Join(tmp, "restored_clone")
	runGitCommand(t, tmp, "clone", "--branch", "main", repoDir, restoreDir)
	runGitCommand(t, restoreDir, "fetch", bundleFile, "HEAD:job-branch")
	runGitCommand(t, restoreDir, "checkout", "job-branch")
	restoredHead := runGitCommand(t, restoreDir, "rev-parse", "HEAD")
	if restoredHead != headSHA {
		t.Fatalf("restored HEAD %s does not match original HEAD %s", restoredHead, headSHA)
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "service.go")); err != nil {
		t.Fatalf("service.go missing in restored clone: %v", err)
	}
}

// 3. Test job with dirty/untracked files captures deliverables into manifest (#2379).
// Validates:
// - Captured uncommitted work: modified tracked files + untracked output files.
// - Exclusions: build caches and symlinks outside the tree are skipped.
// - Results directory contains deliverable files under deliverables/ with matching SHA256.
func TestIntegration_JobWithDirtyUntrackedFilesCapturesDeliverables(t *testing.T) {
	tmp := t.TempDir()
	resultsRoot := filepath.Join(tmp, "results")
	jobDir := filepath.Join(tmp, "slot", "jobs", "card-dirty-job")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir jobDir: %v", err)
	}

	repoDir := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repoDir: %v", err)
	}
	_, _ = setupFakeJobRepo(t, repoDir)

	// 1. Make uncommitted modification to tracked file
	trackedFile := filepath.Join(repoDir, "README.md")
	dirtyContent := []byte("# Bench and Manifest Repo\nUncommitted modifications made during job execution.\n")
	if err := os.WriteFile(trackedFile, dirtyContent, 0o644); err != nil {
		t.Fatalf("write dirty README.md: %v", err)
	}

	// 2. Create untracked deliverables in repo
	deliverable1 := filepath.Join(repoDir, "experiment_results.csv")
	deliverable1Content := []byte("step,score,loss\n1,0.85,0.15\n2,0.92,0.08\n")
	if err := os.WriteFile(deliverable1, deliverable1Content, 0o644); err != nil {
		t.Fatalf("write deliverable1: %v", err)
	}

	subDeliverablesDir := filepath.Join(repoDir, "artifacts", "models")
	if err := os.MkdirAll(subDeliverablesDir, 0o755); err != nil {
		t.Fatalf("mkdir subDeliverables: %v", err)
	}
	deliverable2 := filepath.Join(subDeliverablesDir, "weights.bin")
	deliverable2Content := []byte("binary weight data 0123456789")
	if err := os.WriteFile(deliverable2, deliverable2Content, 0o644); err != nil {
		t.Fatalf("write deliverable2: %v", err)
	}

	// 3. Create cache directories that MUST be excluded
	goModCache := filepath.Join(repoDir, "gomod", "cache")
	if err := os.MkdirAll(goModCache, 0o755); err != nil {
		t.Fatalf("mkdir goModCache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goModCache, "lib.zip"), []byte("cached library archive"), 0o644); err != nil {
		t.Fatalf("write lib.zip: %v", err)
	}

	// 4. Create symlink to outside path that MUST be excluded
	outsideFile := filepath.Join(tmp, "secret_outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside content"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(repoDir, "symlink_test")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	key := swarm.CaptureKey{
		Card:    "card-dirty-job",
		Attempt: 1,
		Job:     "job-dirty-1",
	}
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
		StartedAt:   now.Add(-2 * time.Minute),
		EndedAt:     now,
		WallSeconds: 120.0,
	}

	manifest, err := swarm.CaptureJob(context.Background(), in)
	if err != nil {
		t.Fatalf("CaptureJob failed: %v", err)
	}

	// Verify manifest deliverables
	var hasTrackedModified, hasUntrackedCSV, hasUntrackedWeights bool
	for _, d := range manifest.Deliverables {
		if d.Path == "README.md" && d.Kind == swarm.DeliverableTrackedModified {
			hasTrackedModified = true
		}
		if d.Path == "experiment_results.csv" && d.Kind == swarm.DeliverableUntracked {
			hasUntrackedCSV = true
		}
		if d.Path == "artifacts/models/weights.bin" && d.Kind == swarm.DeliverableUntracked {
			hasUntrackedWeights = true
		}
		// Confirm cache and symlink exclusions
		if strings.Contains(d.Path, "gomod/cache") || strings.Contains(d.Path, "lib.zip") {
			t.Errorf("cache file was not excluded: %s", d.Path)
		}
		if strings.Contains(d.Path, "symlink_test") {
			t.Errorf("symlink was not excluded: %s", d.Path)
		}
	}

	if !hasTrackedModified {
		t.Errorf("expected modified README.md in Deliverables with Kind DeliverableTrackedModified")
	}
	if !hasUntrackedCSV {
		t.Errorf("expected experiment_results.csv in Deliverables with Kind DeliverableUntracked")
	}
	if !hasUntrackedWeights {
		t.Errorf("expected artifacts/models/weights.bin in Deliverables with Kind DeliverableUntracked")
	}

	// Verify deliverable files exist in results directory
	resDir := key.ResultsDir(resultsRoot)
	copiedCSV := filepath.Join(resDir, "deliverables", "experiment_results.csv")
	csvBytes, err := os.ReadFile(copiedCSV)
	if err != nil {
		t.Fatalf("read copied deliverable: %v", err)
	}
	if string(csvBytes) != string(deliverable1Content) {
		t.Errorf("copied deliverable content mismatch: got %q, want %q", string(csvBytes), string(deliverable1Content))
	}
}

// 4. Test readback digest verification establishes independence (#2379).
// Validates:
// - VerifyCapture verifies digest, manifest, bundle, artifacts, and deliverables.
// - Tamper detection catches corrupt digest or modified artifacts.
// - Full deletion of working directory does not affect results directory.
// - Captured results directory is 100% self-contained and independently restorable.
func TestIntegration_ReadbackDigestVerificationEstablishesIndependence(t *testing.T) {
	tmp := t.TempDir()
	resultsRoot := filepath.Join(tmp, "results")
	jobDir := filepath.Join(tmp, "slot", "jobs", "card-independence")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir jobDir: %v", err)
	}

	repoDir := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repoDir: %v", err)
	}
	_, headSHA := setupFakeJobRepo(t, repoDir)

	resultContent := "# Independent Result\nVerified standalone capture.\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(resultContent), 0o644); err != nil {
		t.Fatalf("write RESULT.md: %v", err)
	}

	key := swarm.CaptureKey{
		Card:    "card-independence",
		Attempt: 1,
		Job:     "job-indep-1",
	}
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
		StartedAt:   now.Add(-time.Minute),
		EndedAt:     now,
		WallSeconds: 60.0,
	}

	_, err := swarm.CaptureJob(context.Background(), in)
	if err != nil {
		t.Fatalf("CaptureJob failed: %v", err)
	}

	resDir := key.ResultsDir(resultsRoot)

	// Sub-test A: Initial readback verification succeeds
	vManifest, err := swarm.VerifyCapture(resDir)
	if err != nil {
		t.Fatalf("initial VerifyCapture failed: %v", err)
	}
	if vManifest.Key != key {
		t.Errorf("verified manifest key mismatch: %+v", vManifest.Key)
	}

	// Sub-test B: Tampering manifest.digest is caught
	digestFile := filepath.Join(resDir, swarm.ManifestDigestFileName)
	origDigest, err := os.ReadFile(digestFile)
	if err != nil {
		t.Fatalf("read digest: %v", err)
	}
	// Tamper the digest hash
	corruptDigest := strings.Replace(string(origDigest), string(origDigest[:4]), "ffff", 1)
	if err := os.WriteFile(digestFile, []byte(corruptDigest), 0o644); err != nil {
		t.Fatalf("write corrupt digest: %v", err)
	}
	if _, err := swarm.VerifyCapture(resDir); err == nil {
		t.Fatal("expected VerifyCapture to fail on corrupt digest, got nil")
	}

	// Restore digest
	if err := os.WriteFile(digestFile, origDigest, 0o644); err != nil {
		t.Fatalf("restore digest: %v", err)
	}

	// Sub-test C: Tampering artifact content is caught
	resFile := filepath.Join(resDir, "RESULT.md")
	origResult, err := os.ReadFile(resFile)
	if err != nil {
		t.Fatalf("read RESULT.md: %v", err)
	}
	if err := os.WriteFile(resFile, []byte("# Tampered Result\nModified after capture!\n"), 0o644); err != nil {
		t.Fatalf("tamper result: %v", err)
	}
	if _, err := swarm.VerifyCapture(resDir); err == nil {
		t.Fatal("expected VerifyCapture to fail on tampered RESULT.md, got nil")
	}

	// Restore RESULT.md
	if err := os.WriteFile(resFile, origResult, 0o644); err != nil {
		t.Fatalf("restore RESULT.md: %v", err)
	}
	if _, err := swarm.VerifyCapture(resDir); err != nil {
		t.Fatalf("VerifyCapture should succeed after restoring RESULT.md: %v", err)
	}

	// Sub-test D: Complete deletion of working directory proves independence
	if err := os.RemoveAll(jobDir); err != nil {
		t.Fatalf("failed to delete jobDir: %v", err)
	}
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("jobDir must be completely deleted: %v", err)
	}

	// Verification continues to succeed with jobDir completely gone!
	postDeleteManifest, err := swarm.VerifyCapture(resDir)
	if err != nil {
		t.Fatalf("VerifyCapture failed after jobDir deletion: %v", err)
	}
	if postDeleteManifest.Bundle == nil || postDeleteManifest.Bundle.Head != headSHA {
		t.Fatalf("post-delete bundle head mismatch: got %v, want %s", postDeleteManifest.Bundle, headSHA)
	}

	// Restore from bundle into new clean directory
	cloneDest := filepath.Join(tmp, "standalone_restore")
	bundlePath := filepath.Join(resDir, swarm.BundleFileName)
	runGitCommand(t, tmp, "clone", bundlePath, cloneDest)
	if restoredHead := runGitCommand(t, cloneDest, "rev-parse", "HEAD"); restoredHead != headSHA {
		t.Fatalf("restored HEAD from bundle alone mismatch: got %s, want %s", restoredHead, headSHA)
	}
}

// 5. Test that deletion remains disabled (#2379 & Stella's pre-card hold).
// Validates:
// - Single finalizer FinalizeJob owns deletion eligibility.
// - Deletion is DISABLED in this PR: working directory is preserved.
// - DeletionDisabled is recorded as true in manifest.
// - Even for failed / silent runs with no RESULT.md, the working directory is NOT deleted.
func TestIntegration_DeletionRemainsDisabled(t *testing.T) {
	tmp := t.TempDir()
	resultsRoot := filepath.Join(tmp, "results")

	// Case A: Successful job finalizing
	jobDirOk := filepath.Join(tmp, "slot", "jobs", "card-fin-ok")
	if err := os.MkdirAll(jobDirOk, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobDirOk, "RESULT.md"), []byte("# OK\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobDirOk, "harness.log"), []byte("log data\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	keyOk := swarm.CaptureKey{
		Card:    "card-fin-ok",
		Attempt: 1,
		Job:     "job-fin-ok",
	}
	now := time.Now().UTC()
	inOk := swarm.FinalizeInput{
		ResultsRoot: resultsRoot,
		JobDir:      jobDirOk,
		Key:         keyOk,
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

	resOk, err := swarm.FinalizeJob(context.Background(), inOk)
	if err != nil {
		t.Fatalf("FinalizeJob failed: %v", err)
	}

	if !resOk.Verified {
		t.Errorf("FinalizeResult should be marked verified")
	}
	if !resOk.WorkingDirKept {
		t.Errorf("WorkingDirKept must be true")
	}
	if resOk.DeletionSkipped != "deletion strictly disabled in this PR" {
		t.Errorf("unexpected DeletionSkipped message: %s", resOk.DeletionSkipped)
	}
	if resOk.Manifest == nil || !resOk.Manifest.DeletionDisabled {
		t.Errorf("manifest DeletionDisabled must be true")
	}

	// Working dir must remain completely intact on disk!
	if fi, err := os.Stat(jobDirOk); err != nil || !fi.IsDir() {
		t.Fatalf("working dir was deleted or missing! Expected intact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jobDirOk, "RESULT.md")); err != nil {
		t.Fatalf("RESULT.md was removed from working dir: %v", err)
	}

	// Case B: Failed / harness-crashed job finalizing (removal of harness-failure deletion exception)
	jobDirFail := filepath.Join(tmp, "slot", "jobs", "card-fin-fail")
	if err := os.MkdirAll(jobDirFail, 0o755); err != nil {
		t.Fatalf("mkdir fail jobDir: %v", err)
	}
	// NO RESULT.md (e.g. timeout, harness died, crashed before writing RESULT)
	if err := os.WriteFile(filepath.Join(jobDirFail, "harness.log"), []byte("fatal: harness crashed\n"), 0o644); err != nil {
		t.Fatalf("write fail log: %v", err)
	}
	partialWorkFile := filepath.Join(jobDirFail, "partial_edits.go")
	if err := os.WriteFile(partialWorkFile, []byte("// partial edits preserved\n"), 0o644); err != nil {
		t.Fatalf("write partial: %v", err)
	}

	keyFail := swarm.CaptureKey{
		Card:    "card-fin-fail",
		Attempt: 1,
		Job:     "job-fin-fail",
	}
	inFail := swarm.FinalizeInput{
		ResultsRoot: resultsRoot,
		JobDir:      jobDirFail,
		Key:         keyFail,
		Proof: swarm.ExecutorProof{
			ExitCode:  1,
			EndedAt:   now,
			Completed: true,
			ExitKind:  "harness-failure",
		},
		StartedAt:   now.Add(-time.Second),
		EndedAt:     now,
		WallSeconds: 1.0,
	}

	resFail, err := swarm.FinalizeJob(context.Background(), inFail)
	if err != nil {
		t.Fatalf("FinalizeJob on failed job returned error: %v", err)
	}

	if !resFail.WorkingDirKept {
		t.Errorf("working dir must be kept even on harness failure")
	}
	if resFail.DeletionSkipped != "deletion strictly disabled in this PR" {
		t.Errorf("deletion skipped reason mismatch: %s", resFail.DeletionSkipped)
	}

	// Assert the failed job directory and partial files are NOT deleted
	if _, err := os.Stat(jobDirFail); err != nil {
		t.Fatalf("failed job directory was deleted! Must be preserved: %v", err)
	}
	if _, err := os.Stat(partialWorkFile); err != nil {
		t.Fatalf("partial work file was deleted on harness failure! Must be preserved: %v", err)
	}
}

// Ensure fleet import is used to satisfy linter
var _ = fleet.AdmissionAdmitting
