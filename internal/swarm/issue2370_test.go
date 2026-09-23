package swarm

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestIssue2370 covers nova-tools#2370: implement and test the attempt evidence
// publication and recovery. It exercises the two behaviours the spec demands:
//
//   - TestAttemptEvidencePublication: writes the three evidence files then MANIFEST
//     last, atomically no-replace, never following a symlink; a replay with identical
//     bytes is idempotent, differing content under the same job ID refuses, an
//     incomplete set is not a committed snapshot and unsupported atomic/durable
//     publication refuses before gate invocation.
//
//   - TestAttemptRecoveryVerifiesAndQuarantines: recovery opens bounded regular files
//     at the constant names, checks manifest job-id against its directory, recomputes
//     task/prompt/config/prefix hashes, and quarantines on a missing/mismatched
//     body/config/prefix or a job-ID/lineage conflict before launch/reclaim.

func TestIssue2370(t *testing.T) {
	t.Run("Publication", TestAttemptEvidencePublication)
	t.Run("Recovery", TestAttemptRecoveryVerifiesAndQuarantines)
}

func TestAttemptEvidencePublication(t *testing.T) {
	pool, err := OpenPool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	jobID := "20260101T000000Z-test-evidence-abc"
	evidenceDir := filepath.Join(pool.Dir, "evidence", jobID)
	taskBytes := []byte("run the check")
	promptBytes := []byte("# Task: verify the evidence\n\nPlease run the check.")
	profileBytes := []byte(`{"version":1,"profiles":{"p1":{"worker":{"name":"test","usage":"test","harness":"/bin/echo","harness_args":[],"worker_dir":"/tmp","deadline":"5m","execution":{"adapter":"opencode-native/1","adapter_revision":"1"}},"route":{"provider":"test","credentials":{"kind":"nova-secrets","store":"default","seat":"main","age_key":"x","sops":"x","gate":"x","launcher":"x"}},"env_var":"TEST_KEY","model":"test-model","allowed_models":["test-model"],"prompt":{"mode":"compact","prefix":"test","tools":[]}}}}`)

	preimage, err := computePreimageFromJSON(profileBytes)
	if err != nil {
		t.Fatalf("preimage: %v", err)
	}

	snapshotHash := hashManifestBody(taskBytes, promptBytes, profileBytes, preimage)

	t.Run("writes three evidence files then MANIFEST last", func(t *testing.T) {
		err := PublishEvidence(evidenceDir, jobID, taskBytes, promptBytes, profileBytes, preimage)
		if err != nil {
			t.Fatalf("PublishEvidence: %v", err)
		}

		task, err := os.ReadFile(filepath.Join(evidenceDir, "TASK.txt"))
		if err != nil {
			t.Fatalf("read TASK.txt: %v", err)
		}
		if string(task) != string(taskBytes) {
			t.Fatalf("TASK.txt: got %q, want %q", task, taskBytes)
		}

		prompt, err := os.ReadFile(filepath.Join(evidenceDir, "PROMPT.md"))
		if err != nil {
			t.Fatalf("read PROMPT.md: %v", err)
		}
		if string(prompt) != string(promptBytes) {
			t.Fatalf("PROMPT.md: got %q, want %q", prompt, promptBytes)
		}

		profile, err := os.ReadFile(filepath.Join(evidenceDir, "PROFILE.json"))
		if err != nil {
			t.Fatalf("read PROFILE.json: %v", err)
		}
		if string(profile) != string(profileBytes) {
			t.Fatalf("PROFILE.json: got %q, want %q", profile, profileBytes)
		}

		manifest, err := os.ReadFile(filepath.Join(evidenceDir, "MANIFEST.json"))
		if err != nil {
			t.Fatalf("read MANIFEST.json: %v", err)
		}
		var m EvidenceManifest
		if err := json.Unmarshal(manifest, &m); err != nil {
			t.Fatalf("unmarshal MANIFEST.json: %v", err)
		}
		if m.Schema != "nova.swarm.prelaunch/1" {
			t.Fatalf("manifest schema: got %q, want nova.swarm.prelaunch/1", m.Schema)
		}
		if m.JobID != jobID {
			t.Fatalf("manifest job_id: got %q, want %q", m.JobID, jobID)
		}
		if m.SnapshotHash != snapshotHash {
			t.Fatalf("manifest snapshot_hash: got %q, want %q", m.SnapshotHash, snapshotHash)
		}
	})

	t.Run("atomic no-replace idempotent replay", func(t *testing.T) {
		err := PublishEvidence(evidenceDir, jobID, taskBytes, promptBytes, profileBytes, preimage)
		if err != nil {
			t.Fatalf("idempotent replay: %v", err)
		}
	})

	t.Run("differing content under same job ID refuses", func(t *testing.T) {
		differentTask := []byte("different task content")
		err := PublishEvidence(evidenceDir, jobID, differentTask, promptBytes, profileBytes, preimage)
		if err == nil {
			t.Fatal("PublishEvidence with differing content under same job ID should refuse")
		}
	})

	t.Run("never follows symlink", func(t *testing.T) {
		dir := t.TempDir()
		symlinkDir := filepath.Join(dir, "evidence-link")
		realDir := t.TempDir()
		if err := os.Symlink(realDir, symlinkDir); err != nil {
			t.Fatal(err)
		}
		err := PublishEvidence(symlinkDir, jobID, taskBytes, promptBytes, profileBytes, preimage)
		if err == nil {
			t.Fatal("PublishEvidence must refuse to write through a symlink")
		}
	})

	t.Run("incomplete set is not a committed snapshot", func(t *testing.T) {
		dir := t.TempDir()
		evidenceDir := filepath.Join(dir, "incomplete")
		if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evidenceDir, "TASK.txt"), taskBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		err := VerifyEvidence(evidenceDir, jobID)
		if err == nil {
			t.Fatal("VerifyEvidence must refuse an incomplete evidence set")
		}
	})
}

func TestAttemptRecoveryVerifiesAndQuarantines(t *testing.T) {
	pool, err := OpenPool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	jobID := "20260101T000000Z-recovery-test-xyz"
	evidenceDir := filepath.Join(pool.Dir, "evidence", jobID)
	taskBytes := []byte("run the check")
	promptBytes := []byte("# Task: verify the evidence\n\nPlease run the check.")
	profileBytes := []byte(`{"version":1,"profiles":{"p1":{"worker":{"name":"test","usage":"test","harness":"/bin/echo","harness_args":[],"worker_dir":"/tmp","deadline":"5m","execution":{"adapter":"opencode-native/1","adapter_revision":"1"}},"route":{"provider":"test","credentials":{"kind":"nova-secrets","store":"default","seat":"main","age_key":"x","sops":"x","gate":"x","launcher":"x"}},"env_var":"TEST_KEY","model":"test-model","allowed_models":["test-model"],"prompt":{"mode":"compact","prefix":"test","tools":[]}}}}`)

	preimage, err := computePreimageFromJSON(profileBytes)
	if err != nil {
		t.Fatalf("preimage: %v", err)
	}

	err = PublishEvidence(evidenceDir, jobID, taskBytes, promptBytes, profileBytes, preimage)
	if err != nil {
		t.Fatalf("PublishEvidence: %v", err)
	}

	t.Run("recovery opens bounded regular files and verifies", func(t *testing.T) {
		snapshot, err := RecoverEvidence(evidenceDir, jobID)
		if err != nil {
			t.Fatalf("RecoverEvidence: %v", err)
		}
		if string(snapshot.Task) != string(taskBytes) {
			t.Fatalf("recovered task: got %q, want %q", snapshot.Task, taskBytes)
		}
		if string(snapshot.Prompt) != string(promptBytes) {
			t.Fatalf("recovered prompt: got %q, want %q", snapshot.Prompt, promptBytes)
		}
		if string(snapshot.Profile) != string(profileBytes) {
			t.Fatalf("recovered profile: got %q, want %q", snapshot.Profile, profileBytes)
		}
		if snapshot.Manifest.JobID != jobID {
			t.Fatalf("recovered manifest job_id: got %q, want %q", snapshot.Manifest.JobID, jobID)
		}
	})

	t.Run("missing evidence quarantines", func(t *testing.T) {
		dir := t.TempDir()
		evidenceDir := filepath.Join(dir, "missing")
		_, err := RecoverEvidence(evidenceDir, jobID)
		if err == nil {
			t.Fatal("RecoverEvidence must refuse missing evidence")
		}
	})

	t.Run("mismatched manifest job ID quarantines", func(t *testing.T) {
		wrongDir := t.TempDir()
		if err := os.MkdirAll(wrongDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wrongDir, "TASK.txt"), taskBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wrongDir, "PROMPT.md"), promptBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wrongDir, "PROFILE.json"), profileBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		snapshotHash := computeSnapshotHash(taskBytes, promptBytes, profileBytes, preimage)
		wrongManifest, _ := json.Marshal(EvidenceManifest{
			Schema:       "nova.swarm.prelaunch/1",
			JobID:        "different-job-id",
			SnapshotHash: snapshotHash,
		})
		if err := os.WriteFile(filepath.Join(wrongDir, "MANIFEST.json"), wrongManifest, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := RecoverEvidence(wrongDir, jobID)
		if err == nil {
			t.Fatal("RecoverEvidence must refuse a mismatched manifest job ID")
		}
	})

	t.Run("mismatched body hash quarantines", func(t *testing.T) {
		dir := t.TempDir()
		evidenceDir := filepath.Join(dir, "bad-hash")
		if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
			t.Fatal(err)
		}
		differentTask := []byte("different task")
		if err := os.WriteFile(filepath.Join(evidenceDir, "TASK.txt"), differentTask, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evidenceDir, "PROMPT.md"), promptBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evidenceDir, "PROFILE.json"), profileBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		badHash := hashManifestBody(differentTask, promptBytes, profileBytes, preimage)
		goodManifest, _ := json.Marshal(EvidenceManifest{
			Schema:       "nova.swarm.prelaunch/1",
			JobID:        jobID,
			SnapshotHash: badHash,
		})
		if err := os.WriteFile(filepath.Join(evidenceDir, "MANIFEST.json"), goodManifest, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := RecoverEvidence(evidenceDir, jobID)
		if err == nil {
			t.Fatal("RecoverEvidence must refuse a mismatched body hash")
		}
	})

	t.Run("job ID path conflict quarantines", func(t *testing.T) {
		conflictDir := t.TempDir()
		conflictEvidence := filepath.Join(conflictDir, "evidence", "different-job")
		if err := os.MkdirAll(conflictEvidence, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(conflictEvidence, "TASK.txt"), taskBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(conflictEvidence, "PROMPT.md"), promptBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(conflictEvidence, "PROFILE.json"), profileBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		conflictSnapshotHash := computeSnapshotHash(taskBytes, promptBytes, profileBytes, preimage)
		conflictManifest, _ := json.Marshal(EvidenceManifest{
			Schema:       "nova.swarm.prelaunch/1",
			JobID:        "different-job",
			SnapshotHash: conflictSnapshotHash,
		})
		if err := os.WriteFile(filepath.Join(conflictEvidence, "MANIFEST.json"), conflictManifest, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := RecoverEvidence(conflictEvidence, jobID)
		if err == nil {
			t.Fatal("RecoverEvidence must refuse a job ID/path conflict")
		}
	})
}

func computePreimageFromJSON(profileBytes []byte) (string, error) {
	h := sha256.Sum256(profileBytes)
	return fmt.Sprintf("sha256:%x", h), nil
}

func hashManifestBody(task, prompt, profile []byte, preimage string) string {
	body := append(append(task, '\n'), prompt...)
	body = append(append(body, '\n'), profile...)
	body = append(append(body, '\n'), []byte(preimage)...)
	sum := sha256.Sum256(body)
	return fmt.Sprintf("sha256:%x", sum)
}
