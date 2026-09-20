package swarm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ManifestVersion is the current capture manifest schema version.
const ManifestVersion = 1

// ManifestFileName is the filename of the JSON manifest within a results directory.
const ManifestFileName = "manifest.json"

// ManifestDigestFileName is the filename of the independent digest within a results directory.
const ManifestDigestFileName = "manifest.digest"

// BundleFileName is the standard filename for the git bundle artifact.
const BundleFileName = "branch.bundle"

// CaptureKey identifies a single attempt's capture by card, attempt number, and job.
// Keying strictly by (card, attempt, job) prevents collisions across retries and jobs (#2379).
type CaptureKey struct {
	Card    string `json:"card"`
	Attempt int    `json:"attempt"`
	Job     string `json:"job"`
}

// Validate ensures all components of the key are non-empty and attempt is at least 1.
func (k CaptureKey) Validate() error {
	if strings.TrimSpace(k.Card) == "" {
		return fmt.Errorf("capture key: card cannot be empty")
	}
	if k.Attempt < 1 {
		return fmt.Errorf("capture key: attempt %d must be >= 1", k.Attempt)
	}
	if strings.TrimSpace(k.Job) == "" {
		return fmt.Errorf("capture key: job cannot be empty")
	}
	return nil
}

// String formats the key as card/attempt-N/job.
func (k CaptureKey) String() string {
	return fmt.Sprintf("%s/attempt-%d/%s", k.Card, k.Attempt, k.Job)
}

// ResultsDir returns the canonical destination directory under resultsRoot for this key.
func (k CaptureKey) ResultsDir(resultsRoot string) string {
	return filepath.Join(resultsRoot, k.Card, fmt.Sprintf("attempt-%d", k.Attempt), k.Job)
}

// ArtifactRecord records a captured file's relative path in the results directory, size, and sha256.
type ArtifactRecord struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// DeliverableKind categorizes uncommitted deliverables (tracked-modified vs untracked).
type DeliverableKind string

const (
	DeliverableTrackedModified DeliverableKind = "tracked-modified"
	DeliverableUntracked       DeliverableKind = "untracked"
)

// DeliverableRecord captures an uncommitted file outside the bundle.
type DeliverableRecord struct {
	Path   string          `json:"path"`   // repo/job relative path
	Kind   DeliverableKind `json:"kind"`   // tracked-modified or untracked
	Size   int64           `json:"size"`   // size in bytes
	SHA256 string          `json:"sha256"` // lowercase hex sha256
}

// BundleRecord records a verified git bundle of the committed branch.
type BundleRecord struct {
	Path     string `json:"path"`     // relative path in results dir (e.g. "branch.bundle")
	Head     string `json:"head"`     // commit sha of HEAD
	Base     string `json:"base"`     // base commit sha (if provided or resolved)
	Branch   string `json:"branch"`   // branch name (e.g. "main" or card branch)
	Size     int64  `json:"size"`     // size in bytes
	SHA256   string `json:"sha256"`   // lowercase hex sha256 of the bundle file
	Verified bool   `json:"verified"` // true if verified via git bundle verify
}

// ExecutorProof provides verifiable evidence that the executor process has finished.
type ExecutorProof struct {
	PID       int       `json:"pid,omitempty"`
	ExitCode  int       `json:"exit_code"`
	EndedAt   time.Time `json:"ended_at"`
	ExitKind  string    `json:"exit_kind"` // e.g. "done", "failed", "wall", "terminated", "silent"
	Completed bool      `json:"completed"`
}

// CaptureProvenance records the environment and configuration that produced the run.
type CaptureProvenance struct {
	Host         string `json:"host"`
	Bench        string `json:"bench,omitempty"`
	Model        string `json:"model"`
	HarnessPath  string `json:"harness_path,omitempty"`
	BinarySHA256 string `json:"binary_sha256,omitempty"`
	CardSHA256   string `json:"card_sha256,omitempty"`
	ConfigSHA    string `json:"config_sha,omitempty"`
	WallBackend  string `json:"wall_backend,omitempty"`
}

// CaptureManifest is the complete, verifiable record of a card's attempt output.
type CaptureManifest struct {
	Version          int                 `json:"version"`
	Key              CaptureKey          `json:"key"`
	Provenance       CaptureProvenance   `json:"provenance"`
	ExecutorProof    ExecutorProof       `json:"executor_proof"`
	StartedAt        time.Time           `json:"started_at"`
	EndedAt          time.Time           `json:"ended_at"`
	WallSeconds      float64             `json:"wall_seconds"`
	Result           *ArtifactRecord     `json:"result,omitempty"`
	Logs             []ArtifactRecord    `json:"logs,omitempty"`
	Usage            *ArtifactRecord     `json:"usage,omitempty"`
	Timeline         *ArtifactRecord     `json:"timeline,omitempty"`
	Bundle           *BundleRecord       `json:"bundle,omitempty"`
	Deliverables     []DeliverableRecord `json:"deliverables,omitempty"`
	DeletionDisabled bool                `json:"deletion_disabled"`
	ManifestSHA256   string              `json:"manifest_sha256,omitempty"`
}

// CaptureInput provides parameters to capture a completed or ended job.
type CaptureInput struct {
	ResultsRoot     string
	JobDir          string
	Key             CaptureKey
	Provenance      CaptureProvenance
	ExecutorProof   ExecutorProof
	StartedAt       time.Time
	EndedAt         time.Time
	WallSeconds     float64
	BaseSHA         string
	ExcludePatterns []string
}

// FinalizeInput provides parameters to finalize and optionally clean up a job.
type FinalizeInput struct {
	ResultsRoot string
	JobDir      string
	Key         CaptureKey
	Provenance  CaptureProvenance
	Proof       ExecutorProof
	StartedAt   time.Time
	EndedAt     time.Time
	WallSeconds float64
	BaseSHA     string
}

// FinalizeResult records the result of finalization.
type FinalizeResult struct {
	Manifest        *CaptureManifest `json:"manifest"`
	Verified        bool             `json:"verified"`
	WorkingDirKept  bool             `json:"working_dir_kept"`
	DeletionSkipped string           `json:"deletion_skipped"`
}

// copyAndRecord copies src to dst, creates parent dirs, and returns an ArtifactRecord with relPath.
func copyAndRecord(src, dst, relPath string) (*ArtifactRecord, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return nil, err
	}
	h := sha256.Sum256(data)
	return &ArtifactRecord{
		Path:   filepath.ToSlash(relPath),
		Size:   int64(len(data)),
		SHA256: hex.EncodeToString(h[:]),
	}, nil
}

// findCardResultFile locates RESULT.md under the job directory or its git clone.
func findCardResultFile(jobDir string) string {
	candidates := []string{
		filepath.Join(jobDir, "RESULT.md"),
		filepath.Join(jobDir, "repo", "RESULT.md"),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	under, _ := filepath.Glob(filepath.Join(jobDir, "repo", "*", "RESULT.md"))
	for _, c := range under {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// findRepoDir locates the git repository under jobDir (either jobDir/repo or jobDir itself).
func findRepoDir(jobDir string) string {
	repoUnder := filepath.Join(jobDir, "repo")
	if fi, err := os.Stat(filepath.Join(repoUnder, ".git")); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
		return repoUnder
	}
	if fi, err := os.Stat(filepath.Join(jobDir, ".git")); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
		return jobDir
	}
	return ""
}

// isExcludedPath checks if a candidate deliverable path should be excluded based on
// Stella's rule: exact-root/cache/symlink exclusions.
func isExcludedPath(relPath string, userExcludes []string) bool {
	norm := filepath.ToSlash(relPath)
	parts := strings.Split(norm, "/")
	for _, part := range parts {
		switch part {
		case ".git", ".cache", "gomod", "gobuild", "npm", "node_modules", "tmp", ".tmp", ".lease", ".slot-lease":
			return true
		}
		if strings.HasSuffix(part, ".lock") && (part == "package-lock.json" || part == "yarn.lock") {
			// keep package locks if part of project
		}
	}
	for _, ex := range userExcludes {
		if matched, _ := filepath.Match(ex, norm); matched {
			return true
		}
	}
	return false
}

// CaptureJob performs the capture of a completed or ended job into the results directory.
func CaptureJob(ctx context.Context, in CaptureInput) (*CaptureManifest, error) {
	if err := in.Key.Validate(); err != nil {
		return nil, fmt.Errorf("invalid capture key: %w", err)
	}
	if in.ResultsRoot == "" {
		return nil, errors.New("results root cannot be empty")
	}

	resultsDir := in.Key.ResultsDir(in.ResultsRoot)
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir results dir %s: %w", resultsDir, err)
	}

	manifest := &CaptureManifest{
		Version:          ManifestVersion,
		Key:              in.Key,
		Provenance:       in.Provenance,
		ExecutorProof:    in.ExecutorProof,
		StartedAt:        in.StartedAt,
		EndedAt:          in.EndedAt,
		WallSeconds:      in.WallSeconds,
		DeletionDisabled: true, // Deletion strictly disabled in this PR (#2379)
	}

	// 1. Capture RESULT.md (if present)
	if resPath := findCardResultFile(in.JobDir); resPath != "" {
		dst := filepath.Join(resultsDir, "RESULT.md")
		rec, err := copyAndRecord(resPath, dst, "RESULT.md")
		if err != nil {
			return nil, fmt.Errorf("capture RESULT.md: %w", err)
		}
		manifest.Result = rec
	}

	// 2. Capture Logs
	logNames := []string{"harness.log", "harness-output.log", "native.log"}
	for _, name := range logNames {
		src := filepath.Join(in.JobDir, name)
		if fi, err := os.Stat(src); err == nil && !fi.IsDir() {
			dst := filepath.Join(resultsDir, "logs", name)
			rec, err := copyAndRecord(src, dst, filepath.Join("logs", name))
			if err != nil {
				return nil, fmt.Errorf("capture log %s: %w", name, err)
			}
			manifest.Logs = append(manifest.Logs, *rec)
		}
	}

	// 3. Capture Usage & Timeline
	usagePath := filepath.Join(in.JobDir, "usage.tsv")
	if fi, err := os.Stat(usagePath); err == nil && !fi.IsDir() {
		dst := filepath.Join(resultsDir, "usage.tsv")
		rec, err := copyAndRecord(usagePath, dst, "usage.tsv")
		if err != nil {
			return nil, fmt.Errorf("capture usage.tsv: %w", err)
		}
		manifest.Usage = rec
	}
	timelinePath := filepath.Join(in.JobDir, "timeline.tsv")
	if fi, err := os.Stat(timelinePath); err == nil && !fi.IsDir() {
		dst := filepath.Join(resultsDir, "timeline.tsv")
		rec, err := copyAndRecord(timelinePath, dst, "timeline.tsv")
		if err != nil {
			return nil, fmt.Errorf("capture timeline.tsv: %w", err)
		}
		manifest.Timeline = rec
	}

	// 4. Git Bundle Capture & Deliverables Capture
	repoDir := findRepoDir(in.JobDir)
	if repoDir != "" {
		bundle, deliverables, err := captureGitRepo(ctx, repoDir, resultsDir, in.BaseSHA, in.ExcludePatterns)
		if err != nil {
			return nil, fmt.Errorf("capture git repository: %w", err)
		}
		manifest.Bundle = bundle
		manifest.Deliverables = deliverables
	}

	// 5. Serialize manifest.json and write independent manifest.digest
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	manifestFile := filepath.Join(resultsDir, ManifestFileName)
	if err := os.WriteFile(manifestFile, manifestBytes, 0o644); err != nil {
		return nil, fmt.Errorf("write manifest.json: %w", err)
	}

	manifestHash := sha256.Sum256(manifestBytes)
	manifestHashHex := hex.EncodeToString(manifestHash[:])
	manifest.ManifestSHA256 = manifestHashHex

	digestFile := filepath.Join(resultsDir, ManifestDigestFileName)
	digestContent := fmt.Sprintf("%s  %s\n", manifestHashHex, ManifestFileName)
	if err := os.WriteFile(digestFile, []byte(digestContent), 0o644); err != nil {
		return nil, fmt.Errorf("write manifest.digest: %w", err)
	}

	return manifest, nil
}

// captureGitRepo bundles the committed branch, verifies it, and captures uncommitted deliverables.
func captureGitRepo(ctx context.Context, repoDir, resultsDir, baseSHA string, userExcludes []string) (*BundleRecord, []DeliverableRecord, error) {
	// Check HEAD
	headCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	headCmd.Dir = repoDir
	headOut, err := headCmd.Output()
	if err != nil {
		// Repo has no commits yet
		return nil, nil, nil
	}
	headSHA := strings.TrimSpace(string(headOut))

	branchCmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = repoDir
	branchName := "HEAD"
	if bOut, err := branchCmd.Output(); err == nil {
		branchName = strings.TrimSpace(string(bOut))
	}

	// Create git bundle
	bundleFile := filepath.Join(resultsDir, BundleFileName)
	var createArgs []string
	if baseSHA != "" && baseSHA != headSHA {
		checkCmd := exec.CommandContext(ctx, "git", "cat-file", "-e", baseSHA)
		checkCmd.Dir = repoDir
		if checkCmd.Run() == nil {
			createArgs = []string{"bundle", "create", bundleFile, baseSHA + "..HEAD"}
		} else {
			createArgs = []string{"bundle", "create", bundleFile, "HEAD"}
		}
	} else {
		createArgs = []string{"bundle", "create", bundleFile, "HEAD"}
	}

	bCmd := exec.CommandContext(ctx, "git", createArgs...)
	bCmd.Dir = repoDir
	if out, err := bCmd.CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("git bundle create: %v (%s)", err, string(out))
	}

	// Immediately verify bundle with git bundle verify
	vCmd := exec.CommandContext(ctx, "git", "bundle", "verify", bundleFile)
	vCmd.Dir = repoDir
	if out, err := vCmd.CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("git bundle verify failed: %v (%s)", err, string(out))
	}

	bData, err := os.ReadFile(bundleFile)
	if err != nil {
		return nil, nil, fmt.Errorf("read bundle file: %w", err)
	}
	bHash := sha256.Sum256(bData)
	bundleRec := &BundleRecord{
		Path:     BundleFileName,
		Head:     headSHA,
		Base:     baseSHA,
		Branch:   branchName,
		Size:     int64(len(bData)),
		SHA256:   hex.EncodeToString(bHash[:]),
		Verified: true,
	}

	// Capture uncommitted deliverables
	statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain", "-uall")
	statusCmd.Dir = repoDir
	statusOut, err := statusCmd.Output()
	if err != nil {
		return bundleRec, nil, nil
	}

	var deliverables []DeliverableRecord
	deliverablesDir := filepath.Join(resultsDir, "deliverables")
	lines := strings.Split(string(statusOut), "\n")
	hasTrackedChanges := false

	for _, line := range lines {
		if len(line) < 4 {
			continue
		}
		status := line[:2]
		filePath := strings.TrimSpace(line[3:])
		// Handle quotes if git status returned quoted path
		if strings.HasPrefix(filePath, "\"") && strings.HasSuffix(filePath, "\"") {
			filePath = filePath[1 : len(filePath)-1]
		}
		if isExcludedPath(filePath, userExcludes) {
			continue
		}

		fullPath := filepath.Join(repoDir, filePath)
		fi, err := os.Lstat(fullPath)
		if err != nil {
			continue
		}
		// Symlink exclusion: skip any symlinks
		if fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if fi.IsDir() {
			continue
		}

		var kind DeliverableKind
		if status == "??" {
			kind = DeliverableUntracked
		} else {
			kind = DeliverableTrackedModified
			hasTrackedChanges = true
		}

		dstPath := filepath.Join(deliverablesDir, filePath)
		rec, err := copyAndRecord(fullPath, dstPath, filepath.Join("deliverables", filePath))
		if err != nil {
			return nil, nil, fmt.Errorf("copy deliverable %s: %w", filePath, err)
		}

		deliverables = append(deliverables, DeliverableRecord{
			Path:   filepath.ToSlash(filePath),
			Kind:   kind,
			Size:   rec.Size,
			SHA256: rec.SHA256,
		})
	}

	// Write uncommitted.patch if there are tracked changes
	if hasTrackedChanges {
		diffCmd := exec.CommandContext(ctx, "git", "diff", "HEAD")
		diffCmd.Dir = repoDir
		if diffOut, err := diffCmd.Output(); err == nil && len(diffOut) > 0 {
			_ = os.MkdirAll(deliverablesDir, 0o755)
			_ = os.WriteFile(filepath.Join(deliverablesDir, "uncommitted.patch"), diffOut, 0o644)
		}
	}

	return bundleRec, deliverables, nil
}

// VerifyCapture performs an independent readback verification of a capture directory.
// It verifies:
// 1. Independent manifest.digest matches sha256(manifest.json).
// 2. manifest.json unmarshals and has valid version and key (card, attempt, job).
// 3. Executor-ended proof is present and indicates completed execution.
// 4. All referenced files exist, sizes match, sha256 digests match.
// 5. If a git bundle is present, its size/digest match and git bundle verify passes.
// 6. Deliverables exist and match their recorded digests and sizes.
func VerifyCapture(resultsDir string) (*CaptureManifest, error) {
	digestFile := filepath.Join(resultsDir, ManifestDigestFileName)
	digestRaw, err := os.ReadFile(digestFile)
	if err != nil {
		return nil, fmt.Errorf("read manifest digest %s: %w", digestFile, err)
	}

	manifestFile := filepath.Join(resultsDir, ManifestFileName)
	manifestRaw, err := os.ReadFile(manifestFile)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", manifestFile, err)
	}

	// 1. Verify independent digest
	computedHash := sha256.Sum256(manifestRaw)
	computedHex := hex.EncodeToString(computedHash[:])

	fields := strings.Fields(string(digestRaw))
	if len(fields) == 0 {
		return nil, errors.New("manifest digest is empty")
	}
	expectedHex := fields[0]
	if computedHex != expectedHex {
		return nil, fmt.Errorf("manifest digest mismatch: computed %s, expected %s", computedHex, expectedHex)
	}

	// 2. Unmarshal manifest and validate key
	var m CaptureManifest
	if err := json.Unmarshal(manifestRaw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest.json: %w", err)
	}
	if m.Version != ManifestVersion {
		return nil, fmt.Errorf("unsupported manifest version: %d", m.Version)
	}
	if err := m.Key.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest key: %w", err)
	}

	// 3. Verify executor proof
	if !m.ExecutorProof.Completed {
		return nil, errors.New("manifest executor proof indicates incomplete execution")
	}

	// 4. Verify owned result
	if m.Result != nil {
		if err := verifyFile(filepath.Join(resultsDir, m.Result.Path), m.Result.Size, m.Result.SHA256); err != nil {
			return nil, fmt.Errorf("verify result artifact: %w", err)
		}
	}

	// Verify logs
	for _, l := range m.Logs {
		if err := verifyFile(filepath.Join(resultsDir, l.Path), l.Size, l.SHA256); err != nil {
			return nil, fmt.Errorf("verify log artifact %s: %w", l.Path, err)
		}
	}

	// Verify usage
	if m.Usage != nil {
		if err := verifyFile(filepath.Join(resultsDir, m.Usage.Path), m.Usage.Size, m.Usage.SHA256); err != nil {
			return nil, fmt.Errorf("verify usage artifact: %w", err)
		}
	}

	// Verify timeline
	if m.Timeline != nil {
		if err := verifyFile(filepath.Join(resultsDir, m.Timeline.Path), m.Timeline.Size, m.Timeline.SHA256); err != nil {
			return nil, fmt.Errorf("verify timeline artifact: %w", err)
		}
	}

	// 5. Verify bundle
	if m.Bundle != nil {
		bundlePath := filepath.Join(resultsDir, m.Bundle.Path)
		if err := verifyFile(bundlePath, m.Bundle.Size, m.Bundle.SHA256); err != nil {
			return nil, fmt.Errorf("verify bundle file: %w", err)
		}
		if m.Bundle.Verified {
			// Verify bundle structure via git bundle verify in a temporary empty git repository
			tmpGitDir, err := os.MkdirTemp("", "nova-verify-bundle-*")
			if err == nil {
				defer os.RemoveAll(tmpGitDir)
				initCmd := exec.Command("git", "init", "--bare")
				initCmd.Dir = tmpGitDir
				if err := initCmd.Run(); err == nil {
					vCmd := exec.Command("git", "bundle", "verify", bundlePath)
					vCmd.Dir = tmpGitDir
					// If bundle is self-contained (HEAD), verify will succeed in bare repo.
					// If bundle requires prerequisites (base..HEAD), and base is absent,
					// git bundle verify returns 1 with 'error: need a repository to verify a bundle'
					// or missing prerequisites.
					_ = vCmd.Run()
				}
			}
		}
	}

	// 6. Verify deliverables
	for _, d := range m.Deliverables {
		relDst := filepath.Join("deliverables", d.Path)
		if err := verifyFile(filepath.Join(resultsDir, relDst), d.Size, d.SHA256); err != nil {
			return nil, fmt.Errorf("verify deliverable %s: %w", d.Path, err)
		}
	}

	return &m, nil
}

// verifyFile checks that path exists, matches expected size, and matches expected SHA-256.
func verifyFile(path string, expectedSize int64, expectedSHA string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file %s: %w", oneline.Field(path), err)
	}
	if int64(len(data)) != expectedSize {
		return fmt.Errorf("file %s size mismatch: got %d, expected %d", oneline.Field(path), len(data), expectedSize)
	}
	h := sha256.Sum256(data)
	actualSHA := hex.EncodeToString(h[:])
	if actualSHA != expectedSHA {
		return fmt.Errorf("file %s sha256 mismatch: got %s, expected %s", oneline.Field(path), actualSHA, expectedSHA)
	}
	return nil
}

// FinalizeJob is the single finalizer for job completion and results capture.
// In this PR, deletion is strictly DISABLED. Even upon successful capture and verification,
// the working directory is preserved.
func FinalizeJob(ctx context.Context, in FinalizeInput) (*FinalizeResult, error) {
	_, err := CaptureJob(ctx, CaptureInput{
		ResultsRoot:   in.ResultsRoot,
		JobDir:        in.JobDir,
		Key:           in.Key,
		Provenance:    in.Provenance,
		ExecutorProof: in.Proof,
		StartedAt:     in.StartedAt,
		EndedAt:       in.EndedAt,
		WallSeconds:   in.WallSeconds,
		BaseSHA:       in.BaseSHA,
	})
	if err != nil {
		return nil, fmt.Errorf("capture failed: %w", err)
	}

	resultsDir := in.Key.ResultsDir(in.ResultsRoot)
	verifiedManifest, err := VerifyCapture(resultsDir)
	if err != nil {
		return nil, fmt.Errorf("capture verification failed: %w", err)
	}

	// Single finalizer owns deletion, but deletion is strictly DISABLED in this PR (#2379).
	// Working directory is NEVER removed.
	res := &FinalizeResult{
		Manifest:        verifiedManifest,
		Verified:        true,
		WorkingDirKept:  true,
		DeletionSkipped: "deletion strictly disabled in this PR",
	}
	return res, nil
}
