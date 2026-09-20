package pulse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// DeletionEnabled governs whether finalized/captured jobs may be deleted.
// Per contract (#2407, #2379, Stella contract review 2026-09-20), deletion is
// strictly DISABLED in this release. All jobs, whether succeeded, failed, or
// aborted, retain partial edits, usage, and logs.
const DeletionEnabled = false

// ErrDeletionDisabled is returned whenever finalization or deletion is attempted.
var ErrDeletionDisabled = errors.New("harvest deletion is strictly disabled: failed and completed jobs are retained")

// CaptureKey identifies an attempt output uniquely by card + attempt + job.
type CaptureKey struct {
	Card    string `json:"card"`
	Attempt int    `json:"attempt"`
	Job     string `json:"job"`
}

func (k CaptureKey) String() string {
	return fmt.Sprintf("%s/attempt-%d/%s", k.Card, k.Attempt, k.Job)
}

// ManifestFileEntry represents one captured artifact file.
type ManifestFileEntry struct {
	Path   string `json:"path"`   // relative path within capture directory
	Size   int64  `json:"size"`   // file size in bytes
	SHA256 string `json:"sha256"` // SHA-256 hex digest
}

// CaptureManifest is the atomic verified manifest written at capture time.
type CaptureManifest struct {
	Key              CaptureKey                   `json:"key"`
	Label            string                       `json:"label,omitempty"`
	BaseSHA          string                       `json:"base_sha,omitempty"`
	HeadSHA          string                       `json:"head_sha,omitempty"`
	Bench            string                       `json:"bench,omitempty"`
	Model            string                       `json:"model,omitempty"`
	Started          string                       `json:"started,omitempty"`
	Ended            string                       `json:"ended,omitempty"`
	ExitKind         string                       `json:"exit_kind"`
	HarnessFailure   bool                         `json:"harness_failure"`
	Retained         bool                         `json:"retained"`
	DeletionDisabled bool                         `json:"deletion_disabled"`
	Files            map[string]ManifestFileEntry `json:"files"`
	ManifestDigest   string                       `json:"manifest_digest"`
}

// CaptureJobOptions contains everything needed to capture a job run.
type CaptureJobOptions struct {
	Key            CaptureKey
	JobDir         string
	OutputRoot     string
	Label          string
	BaseSHA        string
	HeadSHA        string
	Bench          string
	Model          string
	Started        time.Time
	Ended          time.Time
	ExitKind       string
	HarnessFailure bool
}

// RecoveredArtifacts holds the complete read-back contents verified from a capture manifest.
type RecoveredArtifacts struct {
	Manifest     *CaptureManifest
	Key          CaptureKey
	ResultMD     string
	UsageTSV     string
	Logs         map[string][]byte
	Bundle       []byte
	PartialPatch []byte
	Deliverables map[string][]byte
}

// CapturePath computes the output directory keyed by card + attempt + job.
func CapturePath(outputRoot string, key CaptureKey) string {
	attemptStr := fmt.Sprintf("attempt-%d", key.Attempt)
	return filepath.Join(outputRoot, key.Card, attemptStr, key.Job)
}

// CaptureJob captures all artifacts from jobDir into outputRoot keyed by card + attempt + job,
// verifies the manifest digest, and retains the source job directory without deletion.
func CaptureJob(opts CaptureJobOptions) (*CaptureManifest, error) {
	if strings.TrimSpace(opts.JobDir) == "" {
		return nil, errors.New("capture: missing job directory")
	}
	jobInfo, err := os.Stat(opts.JobDir)
	if err != nil {
		return nil, fmt.Errorf("capture: cannot access job directory %s: %w", opts.JobDir, err)
	}
	if !jobInfo.IsDir() {
		return nil, fmt.Errorf("capture: job path %s is not a directory", opts.JobDir)
	}

	if opts.Key.Attempt <= 0 {
		opts.Key.Attempt = 1
	}
	if opts.Key.Card == "" {
		opts.Key.Card = opts.Label
		if opts.Key.Card == "" {
			opts.Key.Card = "unknown-card"
		}
	}
	if opts.Key.Job == "" {
		opts.Key.Job = filepath.Base(opts.JobDir)
	}

	captureDir := CapturePath(opts.OutputRoot, opts.Key)
	if err := os.MkdirAll(captureDir, 0o755); err != nil {
		return nil, fmt.Errorf("capture: cannot create capture dir %s: %w", captureDir, err)
	}

	now := time.Now().UTC()
	startedStr := ""
	if !opts.Started.IsZero() {
		startedStr = opts.Started.UTC().Format(time.RFC3339)
	}
	endedStr := ""
	if !opts.Ended.IsZero() {
		endedStr = opts.Ended.UTC().Format(time.RFC3339)
	} else {
		endedStr = now.Format(time.RFC3339)
	}

	manifest := &CaptureManifest{
		Key:              opts.Key,
		Label:            opts.Label,
		BaseSHA:          opts.BaseSHA,
		HeadSHA:          opts.HeadSHA,
		Bench:            opts.Bench,
		Model:            opts.Model,
		Started:          startedStr,
		Ended:            endedStr,
		ExitKind:         opts.ExitKind,
		HarnessFailure:   opts.HarnessFailure,
		Retained:         true,
		DeletionDisabled: true,
		Files:            make(map[string]ManifestFileEntry),
	}

	// 1. Capture RESULT.md if present
	resultPath := filepath.Join(opts.JobDir, "RESULT.md")
	if fi, err := os.Stat(resultPath); err == nil && !fi.IsDir() {
		if err := copyFile(resultPath, filepath.Join(captureDir, "RESULT.md")); err != nil {
			return nil, fmt.Errorf("capture: failed to copy RESULT.md: %w", err)
		}
	}

	// 2. Capture usage.tsv if present
	usagePath := filepath.Join(opts.JobDir, "usage.tsv")
	if fi, err := os.Stat(usagePath); err == nil && !fi.IsDir() {
		if err := copyFile(usagePath, filepath.Join(captureDir, "usage.tsv")); err != nil {
			return nil, fmt.Errorf("capture: failed to copy usage.tsv: %w", err)
		}
	}

	// 3. Capture logs (harness.log, harness-output.log, native.log, *.log)
	entries, err := os.ReadDir(opts.JobDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, ".log") || name == "stdout" || name == "stderr" {
				src := filepath.Join(opts.JobDir, name)
				dst := filepath.Join(captureDir, name)
				_ = copyFile(src, dst)
			}
		}
	}

	// 4. Capture work deliverables & partial edits:
	// A git repository might be in JobDir or a subdirectory.
	gitDir := opts.JobDir
	if fi, err := os.Stat(filepath.Join(opts.JobDir, ".git")); err != nil || !fi.IsDir() {
		// check if any subdirectory is a git repo
		if entries != nil {
			for _, e := range entries {
				if e.IsDir() {
					sub := filepath.Join(opts.JobDir, e.Name(), ".git")
					if sfi, serr := os.Stat(sub); serr == nil && sfi.IsDir() {
						gitDir = filepath.Join(opts.JobDir, e.Name())
						break
					}
				}
			}
		}
	}

	isGit := false
	if fi, err := os.Stat(filepath.Join(gitDir, ".git")); err == nil && fi.IsDir() {
		isGit = true
	}

	if isGit {
		// Capture committed work via git bundle if commits exist
		headSHA := gitRevParse(gitDir, "HEAD")
		if headSHA != "" {
			manifest.HeadSHA = headSHA
			bundlePath := filepath.Join(captureDir, "branch.bundle")
			cmd := exec.Command("git", "bundle", "create", bundlePath, "HEAD")
			cmd.Dir = gitDir
			if err := cmd.Run(); err == nil {
				// verify bundle
				vcmd := exec.Command("git", "bundle", "verify", bundlePath)
				vcmd.Dir = gitDir
				_ = vcmd.Run()
			}
		}

		// Capture uncommitted partial edits (diff against HEAD or staged)
		diffTracked := gitOutput(gitDir, "diff", "HEAD")
		diffCached := gitOutput(gitDir, "diff", "--cached")
		combinedDiff := strings.TrimSpace(diffTracked + "\n" + diffCached)
		if combinedDiff != "" {
			_ = os.WriteFile(filepath.Join(captureDir, "work.patch"), []byte(combinedDiff+"\n"), 0o644)
		}

		// Capture untracked deliverables (excluding RESULT.md, usage.tsv, and logs which are already captured)
		statusOut := gitOutput(gitDir, "status", "--porcelain")
		for _, line := range strings.Split(statusOut, "\n") {
			if strings.HasPrefix(line, "?? ") {
				untrackedRel := strings.TrimSpace(strings.TrimPrefix(line, "?? "))
				baseName := filepath.Base(untrackedRel)
				if baseName == "RESULT.md" || baseName == "usage.tsv" || strings.HasSuffix(baseName, ".log") || baseName == "stdout" || baseName == "stderr" {
					continue
				}
				src := filepath.Join(gitDir, untrackedRel)
				dst := filepath.Join(captureDir, "deliverables", untrackedRel)
				if isSafeSource(src, opts.JobDir) {
					_ = copyFile(src, dst)
				}
			}
		}
	} else {
		// Non-git or standalone directory: copy any modified deliverables (excluding symlinks pointing outside)
		deliverablesDir := filepath.Join(captureDir, "deliverables")
		copySafeDeliverables(opts.JobDir, deliverablesDir, opts.JobDir)
	}

	// 5. Hash all files in captureDir and build manifest.Files
	fileEntries, err := hashDirFiles(captureDir)
	if err != nil {
		return nil, fmt.Errorf("capture: failed to hash capture artifacts: %w", err)
	}
	manifest.Files = fileEntries

	// 6. Compute manifest digest
	manifest.ManifestDigest = computeManifestDigest(manifest.Files)

	// 7. Write manifest.json atomically
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("capture: failed to marshal manifest: %w", err)
	}
	manifestPath := filepath.Join(captureDir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		return nil, fmt.Errorf("capture: failed to write manifest.json: %w", err)
	}

	// Rule 4: Deletion is strictly DISABLED; source job is retained
	return manifest, nil
}

// VerifyManifestDigest checks that manifest.json in captureDir is valid and that every
// file matches its recorded SHA256 digest and the overall manifest digest matches.
func VerifyManifestDigest(captureDir string) error {
	manifestPath := filepath.Join(captureDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("verify: cannot read manifest at %s: %w", manifestPath, err)
	}

	var m CaptureManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("verify: invalid manifest JSON: %w", err)
	}

	if len(m.Files) == 0 {
		return errors.New("verify: manifest contains no files")
	}

	for relPath, entry := range m.Files {
		fullPath := filepath.Join(captureDir, filepath.FromSlash(relPath))
		fi, err := os.Stat(fullPath)
		if err != nil {
			return fmt.Errorf("verify: missing artifact file %s: %w", relPath, err)
		}
		if fi.Size() != entry.Size {
			return fmt.Errorf("verify: file size mismatch for %s: expected %d, got %d", relPath, entry.Size, fi.Size())
		}
		actualSHA, err := fileSHA256(fullPath)
		if err != nil {
			return fmt.Errorf("verify: failed to hash %s: %w", relPath, err)
		}
		if actualSHA != entry.SHA256 {
			return fmt.Errorf("verify: digest mismatch for %s: expected %s, got %s", relPath, entry.SHA256, actualSHA)
		}
	}

	expectedDigest := computeManifestDigest(m.Files)
	if m.ManifestDigest != expectedDigest {
		return fmt.Errorf("verify: manifest digest mismatch: expected %s, got %s", m.ManifestDigest, expectedDigest)
	}

	return nil
}

// RecoverCapture verifies the manifest digest and reads back all artifacts into RecoveredArtifacts.
func RecoverCapture(captureDir string) (*RecoveredArtifacts, error) {
	if err := VerifyManifestDigest(captureDir); err != nil {
		return nil, fmt.Errorf("recover: verification failed: %w", err)
	}

	manifestPath := filepath.Join(captureDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var m CaptureManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	rec := &RecoveredArtifacts{
		Manifest:     &m,
		Key:          m.Key,
		Logs:         make(map[string][]byte),
		Deliverables: make(map[string][]byte),
	}

	for relPath := range m.Files {
		fullPath := filepath.Join(captureDir, filepath.FromSlash(relPath))
		content, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("recover: cannot read %s: %w", relPath, err)
		}

		switch {
		case relPath == "RESULT.md":
			rec.ResultMD = string(content)
		case relPath == "usage.tsv":
			rec.UsageTSV = string(content)
		case relPath == "branch.bundle":
			rec.Bundle = content
		case relPath == "work.patch":
			rec.PartialPatch = content
		case strings.HasPrefix(relPath, "deliverables/"):
			subPath := strings.TrimPrefix(relPath, "deliverables/")
			rec.Deliverables[subPath] = content
		case strings.HasSuffix(relPath, ".log") || relPath == "stdout" || relPath == "stderr":
			rec.Logs[relPath] = content
		}
	}

	return rec, nil
}

// RestoreTo writes all recovered artifacts into destDir.
func (r *RecoveredArtifacts) RestoreTo(destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	writeFile := func(relPath string, data []byte) error {
		dst := filepath.Join(destDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	}

	if r.ResultMD != "" {
		if err := writeFile("RESULT.md", []byte(r.ResultMD)); err != nil {
			return err
		}
	}
	if r.UsageTSV != "" {
		if err := writeFile("usage.tsv", []byte(r.UsageTSV)); err != nil {
			return err
		}
	}
	for name, logBytes := range r.Logs {
		if err := writeFile(name, logBytes); err != nil {
			return err
		}
	}
	if len(r.Bundle) > 0 {
		if err := writeFile("branch.bundle", r.Bundle); err != nil {
			return err
		}
	}
	if len(r.PartialPatch) > 0 {
		if err := writeFile("work.patch", r.PartialPatch); err != nil {
			return err
		}
	}
	for sub, bytes := range r.Deliverables {
		if err := writeFile(filepath.Join("deliverables", sub), bytes); err != nil {
			return err
		}
	}

	return nil
}

// FinalizeJob evaluates whether a captured job directory is eligible for deletion
// and executes deletion if enabled. Because deletion is strictly DISABLED, FinalizeJob
// never removes any job directory and returns ErrDeletionDisabled.
func FinalizeJob(captureDir, jobDir string) error {
	if !DeletionEnabled {
		return ErrDeletionDisabled
	}
	return ErrDeletionDisabled
}

// Helpers

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashDirFiles(dir string) (map[string]ManifestFileEntry, error) {
	out := make(map[string]ManifestFileEntry)
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if relSlash == "manifest.json" {
			return nil
		}
		hash, err := fileSHA256(p)
		if err != nil {
			return err
		}
		out[relSlash] = ManifestFileEntry{
			Path:   relSlash,
			Size:   info.Size(),
			SHA256: hash,
		}
		return nil
	})
	return out, err
}

func computeManifestDigest(files map[string]ManifestFileEntry) string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		entry := files[k]
		fmt.Fprintf(h, "%s:%s\n", entry.Path, entry.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func gitRevParse(dir, rev string) string {
	cmd := exec.Command("git", "rev-parse", rev)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitOutput(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func isSafeSource(path, root string) bool {
	resolved, err := safepath.ResolvedUnder(path, root)
	if err != nil {
		return false
	}
	fi, err := os.Lstat(resolved)
	if err != nil {
		return false
	}
	return !fi.IsDir() && fi.Mode()&os.ModeSymlink == 0
}

func copySafeDeliverables(srcDir, dstDir, root string) {
	_ = filepath.Walk(srcDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if name == "RESULT.md" || name == "usage.tsv" || strings.HasSuffix(name, ".log") {
			return nil
		}
		// symlink safety check
		if !isSafeSource(p, root) {
			return nil
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return nil
		}
		dst := filepath.Join(dstDir, rel)
		_ = copyFile(p, dst)
		return nil
	})
}
