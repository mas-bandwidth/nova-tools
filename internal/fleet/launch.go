package fleet

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LaunchRecordFilename is the canonical name of a durable launch record file.
const LaunchRecordFilename = "launch.json"

// LaunchRecord is the durable launch record persisted per slot and job
// before execution begins (Issue #2070 / #2079, docs/SPEC-PULSE.md).
type LaunchRecord struct {
	// AttemptID is an unguessable CSPRNG-minted hex identifier for this attempt.
	AttemptID string `json:"attempt_id"`
	// Timestamp is the UTC instant when the launch was prepared and persisted.
	Timestamp time.Time `json:"timestamp"`
	// CardHash is the SHA-256 hex digest of the card payload.
	CardHash string `json:"card_hash"`
	// Lane is the coordination or worker lane (e.g. "from-emma", "main", "native").
	Lane string `json:"lane"`
	// Slot is the slot identifier (e.g. "1", "slot-1").
	Slot string `json:"slot"`
	// Job is the job or card label.
	Job string `json:"job"`
	// Node is the node / bench name where the card is placed.
	Node string `json:"node,omitempty"`
	// RunningCommitSHA is the git commit SHA of the worker binary / repo when launched.
	RunningCommitSHA string `json:"running_commit_sha,omitempty"`
	// Pid is the process ID of the launched worker (populated once spawned).
	Pid int `json:"pid,omitempty"`
	// State is the lifecycle state: "STARTING", "STARTED", "COMPLETED", "FAILED", "CRASHED".
	State string `json:"state,omitempty"`
}

// MintAttemptID generates a cryptographically random 16-byte (32 hex character)
// attempt ID, satisfying the invariant that attempt IDs are unguessable and
// never derived from label, path, time, host, or counter.
func MintAttemptID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("mint attempt id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// MustMintAttemptID generates an attempt ID or panics if the CSPRNG fails.
func MustMintAttemptID() string {
	id, err := MintAttemptID()
	if err != nil {
		panic(err)
	}
	return id
}

// Validate checks that all required fields of a durable launch record are present.
func (r LaunchRecord) Validate() error {
	if strings.TrimSpace(r.AttemptID) == "" {
		return errors.New("launch record: attempt_id is required")
	}
	if r.Timestamp.IsZero() {
		return errors.New("launch record: timestamp is required")
	}
	if strings.TrimSpace(r.CardHash) == "" {
		return errors.New("launch record: card_hash is required")
	}
	if strings.TrimSpace(r.Lane) == "" {
		return errors.New("launch record: lane is required")
	}
	if strings.TrimSpace(r.Slot) == "" {
		return errors.New("launch record: slot is required")
	}
	if strings.TrimSpace(r.Job) == "" {
		return errors.New("launch record: job is required")
	}
	return nil
}

// WriteLaunchRecord writes the launch record atomically to the given directory.
//
// ATOMIC PERSISTENCE PROTOCOL:
// 1. Stage the JSON payload into a temporary file on the same filesystem.
// 2. Flush and Sync() the temporary file to guarantee durability.
// 3. Atomically rename the temporary file into LaunchRecordFilename.
//
// This guarantees that any sudden worker crash, panic, or SIGKILL cannot produce
// a torn or half-written launch record, and that state persistence completes
// before execution begins.
func WriteLaunchRecord(dir string, rec LaunchRecord) error {
	if err := rec.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal launch record: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".launch-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp launch record in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write launch record %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync launch record %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close launch record %s: %w", tmpPath, err)
	}

	destPath := filepath.Join(dir, LaunchRecordFilename)
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("rename launch record %s -> %s: %w", tmpPath, destPath, err)
	}

	cleanup = false
	return nil
}

// ReadLaunchRecord reads the durable launch record from the specified directory.
func ReadLaunchRecord(dir string) (LaunchRecord, error) {
	recordPath := filepath.Join(dir, LaunchRecordFilename)
	raw, err := os.ReadFile(recordPath)
	if err != nil {
		return LaunchRecord{}, err
	}
	var rec LaunchRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return LaunchRecord{}, fmt.Errorf("unmarshal launch record %s: %w", recordPath, err)
	}
	return rec, nil
}

// RecordLaunch persists the launch record atomically to both the slot directory
// and the job directory before execution begins.
func RecordLaunch(slotDir, jobDir string, rec LaunchRecord) error {
	if strings.TrimSpace(slotDir) != "" {
		if err := WriteLaunchRecord(slotDir, rec); err != nil {
			return fmt.Errorf("record slot launch: %w", err)
		}
	}
	if strings.TrimSpace(jobDir) != "" {
		if err := WriteLaunchRecord(jobDir, rec); err != nil {
			return fmt.Errorf("record job launch: %w", err)
		}
	}
	return nil
}

// CurrentCommitSHA attempts to discover the current git commit SHA for a repository directory.
// If repoDir is empty, it checks the current directory, git environment, or NOVA_COMMIT_SHA.
func CurrentCommitSHA(repoDir string) string {
	if env := os.Getenv("NOVA_COMMIT_SHA"); env != "" {
		return strings.TrimSpace(env)
	}
	if env := os.Getenv("GIT_COMMIT"); env != "" {
		return strings.TrimSpace(env)
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	if repoDir != "" {
		cmd.Dir = repoDir
	}
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}
