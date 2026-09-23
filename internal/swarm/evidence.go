package swarm

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// EvidenceManifest is the prelaunch expected-hash reference published last in the
// evidence directory (SPEC-SWARM-PROFILES, lines 344-365).
type EvidenceManifest struct {
	Schema       string `json:"schema"`
	JobID        string `json:"job_id"`
	SnapshotHash string `json:"snapshot_hash"`
}

// EvidenceSnapshot holds the recovered evidence payload with bounded lengths.
type EvidenceSnapshot struct {
	Task       []byte
	Prompt     []byte
	Profile    []byte
	Manifest   EvidenceManifest
	TaskLen    int
	PromptLen  int
	ProfileLen int
}

const (
	evidenceTask     = "TASK.txt"
	evidencePrompt   = "PROMPT.md"
	evidenceProfile  = "PROFILE.json"
	evidenceManifest = "MANIFEST.json"
	evidenceSchema   = "nova.swarm.prelaunch/1"
)

// PublishEvidence writes the three evidence files then MANIFEST last, atomically
// no-replace, never following a symlink. An existing complete set with identical
// verified bytes is an idempotent replay; differing content under the same job ID
// refuses. (SPEC-SWARM-PROFILES, lines 344-365)
func PublishEvidence(evidenceDir, jobID string, task, prompt, profile []byte, preimage string) error {
	if err := validateEvidenceDir(evidenceDir); err != nil {
		return err
	}

	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		return err
	}

	taskPath := filepath.Join(evidenceDir, evidenceTask)
	promptPath := filepath.Join(evidenceDir, evidencePrompt)
	profilePath := filepath.Join(evidenceDir, evidenceProfile)
	manifestPath := filepath.Join(evidenceDir, evidenceManifest)

	existingManifest, err := os.ReadFile(manifestPath)
	if err == nil {
		var existing EvidenceManifest
		if jsonErr := json.Unmarshal(existingManifest, &existing); jsonErr != nil {
			return fmt.Errorf("existing MANIFEST.json is malformed: %w", jsonErr)
		}
		if existing.JobID != jobID {
			return fmt.Errorf("evidence directory exists with different job_id %q, cannot publish %q", existing.JobID, jobID)
		}

		existingTask, taskErr := os.ReadFile(taskPath)
		existingPrompt, promptErr := os.ReadFile(promptPath)
		existingProfile, profileErr := os.ReadFile(profilePath)

		if taskErr == nil && promptErr == nil && profileErr == nil {
			if string(existingTask) == string(task) &&
				string(existingPrompt) == string(prompt) &&
				string(existingProfile) == string(profile) {
				return nil
			}
		}
		return fmt.Errorf("evidence exists for job %q with differing content; refusing to overwrite", jobID)
	}

	// No MANIFEST.json: any payload already present is a partial snapshot from an
	// earlier attempt. Refuse before writing anything so the incomplete set is left,
	// byte for byte, for reconciliation (no-overwrite, no-replace).
	for _, p := range []string{taskPath, promptPath, profilePath} {
		if _, err := os.Lstat(p); err == nil {
			return fmt.Errorf("evidence for job %q is incomplete (%s present, MANIFEST.json absent); refusing to overwrite, left for reconciliation", jobID, filepath.Base(p))
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	if err := writeAtomicNoFollow(taskPath, task, 0o644); err != nil {
		return err
	}
	if err := writeAtomicNoFollow(promptPath, prompt, 0o644); err != nil {
		return err
	}
	if err := writeAtomicNoFollow(profilePath, profile, 0o644); err != nil {
		return err
	}

	snapshotHash := computeSnapshotHash(task, prompt, profile, preimage)

	manifestBytes, err := json.Marshal(EvidenceManifest{
		Schema:       evidenceSchema,
		JobID:        jobID,
		SnapshotHash: snapshotHash,
	})
	if err != nil {
		return err
	}
	if err := writeAtomicNoFollow(manifestPath, manifestBytes, 0o644); err != nil {
		return err
	}

	return nil
}

func validateEvidenceDir(evidenceDir string) error {
	info, err := os.Lstat(evidenceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("evidence directory path is a symlink; refusing to follow")
	}
	if !info.IsDir() {
		return fmt.Errorf("evidence directory path exists but is not a directory")
	}
	return nil
}

func writeAtomicNoFollow(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("parent directory %q does not exist", dir)
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("parent directory %q is a symlink; refusing to follow", dir)
	}

	info, err = os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("target path %q is a symlink; refusing to follow", path)
		}
		return fmt.Errorf("target path %q already exists; refusing to replace", path)
	} else if !os.IsNotExist(err) {
		return err
	}

	return writeNoReplace(path, data, mode)
}

// writeNoReplace publishes data at path atomically and never replaces an existing
// entry: the bytes go to an exclusive temp file first, then a hard link names them
// at path. Unlike rename, link fails when path already exists (a file, a symlink or
// a racing writer's publication), so an existing entry is never overwritten.
func writeNoReplace(path string, data []byte, mode os.FileMode) error {
	nonce, err := Nonce()
	if err != nil {
		return err
	}
	tmp := path + "." + nonce + ".tmp"
	f, err := openRegularWrite(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Link(tmp, path); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("target path %q already exists; refusing to replace", path)
		}
		return err
	}
	return nil
}

func computeSnapshotHash(task, prompt, profile []byte, preimage string) string {
	h := sha256.New()
	h.Write(task)
	h.Write([]byte("\n"))
	h.Write(prompt)
	h.Write([]byte("\n"))
	h.Write(profile)
	h.Write([]byte("\n"))
	h.Write([]byte(preimage))
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

// VerifyEvidence checks whether an evidence set is complete and valid.
// An incomplete set is not a committed snapshot.
func VerifyEvidence(evidenceDir, jobID string) error {
	_, err := RecoverEvidence(evidenceDir, jobID)
	return err
}

// RecoverEvidence opens bounded regular files at the constant names, verifies
// manifest job ID against its directory, recomputes task/prompt/config/prefix
// hashes before using the snapshot. Missing evidence, mismatched hashes, or a
// job-ID/path/lineage conflict returns an error.
func RecoverEvidence(evidenceDir, jobID string) (*EvidenceSnapshot, error) {
	manifestPath := filepath.Join(evidenceDir, evidenceManifest)
	manifestBytes, err := readRegularBounded(manifestPath, 4096)
	if err != nil {
		return nil, fmt.Errorf("evidence manifest unread: %w", err)
	}

	var manifest EvidenceManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("evidence manifest malformed: %w", err)
	}

	if manifest.Schema != evidenceSchema {
		return nil, fmt.Errorf("evidence manifest schema %q is not %q", manifest.Schema, evidenceSchema)
	}

	if manifest.JobID != jobID {
		return nil, fmt.Errorf("evidence manifest job_id %q does not match expected %q", manifest.JobID, jobID)
	}

	dirName := filepath.Base(evidenceDir)
	if dirName != jobID {
		return nil, fmt.Errorf("evidence directory name %q does not match manifest job_id %q", dirName, manifest.JobID)
	}

	taskPath := filepath.Join(evidenceDir, evidenceTask)
	promptPath := filepath.Join(evidenceDir, evidencePrompt)
	profilePath := filepath.Join(evidenceDir, evidenceProfile)

	task, err := readRegularBounded(taskPath, 1048576)
	if err != nil {
		return nil, fmt.Errorf("evidence task unread: %w", err)
	}

	prompt, err := readRegularBounded(promptPath, 1048576)
	if err != nil {
		return nil, fmt.Errorf("evidence prompt unread: %w", err)
	}

	profile, err := readRegularBounded(profilePath, ProfileCatalogMax+1)
	if err != nil {
		return nil, fmt.Errorf("evidence profile unread: %w", err)
	}

	recoveredPreimage := sha256.Sum256(profile)
	preimageStr := fmt.Sprintf("sha256:%x", recoveredPreimage)

	computedHash := computeSnapshotHash(task, prompt, profile, preimageStr)

	if computedHash != manifest.SnapshotHash {
		return nil, fmt.Errorf("evidence body hash mismatch: computed %q, manifest %q", computedHash, manifest.SnapshotHash)
	}

	return &EvidenceSnapshot{
		Task:       task,
		Prompt:     prompt,
		Profile:    profile,
		Manifest:   manifest,
		TaskLen:    len(task),
		PromptLen:  len(prompt),
		ProfileLen: len(profile),
	}, nil
}
