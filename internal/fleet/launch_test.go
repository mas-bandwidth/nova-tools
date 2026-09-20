package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMintAttemptID_UnguessableCSPRNG verifies that minted attempt IDs are
// 32-character lowercase hex strings generated from CSPRNG, and are unique.
func TestMintAttemptID_UnguessableCSPRNG(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := MintAttemptID()
		if err != nil {
			t.Fatalf("mint attempt id: %v", err)
		}
		if len(id) != 32 {
			t.Fatalf("attempt id length %d, want 32", len(id))
		}
		for _, r := range id {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				t.Fatalf("attempt id %q contains non-hex char %c", id, r)
			}
		}
		if seen[id] {
			t.Fatalf("attempt id collision: %s already seen", id)
		}
		seen[id] = true
	}
}

// TestLaunchRecord_Validate ensures required fields are checked before write.
func TestLaunchRecord_Validate(t *testing.T) {
	now := time.Now().UTC()
	valid := LaunchRecord{
		AttemptID: MustMintAttemptID(),
		Timestamp: now,
		CardHash:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Lane:      "from-emma",
		Slot:      "slot-1",
		Job:       "card-101",
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("valid record failed validation: %v", err)
	}

	// Missing attempt ID
	invalid := valid
	invalid.AttemptID = ""
	if err := invalid.Validate(); err == nil {
		t.Fatalf("expected error on missing attempt_id")
	}

	// Missing timestamp
	invalid = valid
	invalid.Timestamp = time.Time{}
	if err := invalid.Validate(); err == nil {
		t.Fatalf("expected error on missing timestamp")
	}

	// Missing card hash
	invalid = valid
	invalid.CardHash = ""
	if err := invalid.Validate(); err == nil {
		t.Fatalf("expected error on missing card_hash")
	}

	// Missing lane
	invalid = valid
	invalid.Lane = ""
	if err := invalid.Validate(); err == nil {
		t.Fatalf("expected error on missing lane")
	}

	// Missing slot
	invalid = valid
	invalid.Slot = ""
	if err := invalid.Validate(); err == nil {
		t.Fatalf("expected error on missing slot")
	}

	// Missing job
	invalid = valid
	invalid.Job = ""
	if err := invalid.Validate(); err == nil {
		t.Fatalf("expected error on missing job")
	}
}

// TestWriteAndReadLaunchRecord_Atomic tests round-trip persistence and atomic staging.
func TestWriteAndReadLaunchRecord_Atomic(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Millisecond)
	cardBytes := []byte("contract: TEST card-101 at rev1\nwork on issue 2079\n")
	cardHash := sha256.Sum256(cardBytes)

	rec := LaunchRecord{
		AttemptID:        MustMintAttemptID(),
		Timestamp:        now,
		CardHash:         hex.EncodeToString(cardHash[:]),
		Lane:             "from-emma",
		Slot:             "1",
		Job:              "card-101",
		Node:             "batman",
		RunningCommitSHA: "abc1234567890abcdef1234567890abcdef1234",
		Pid:              12345,
		State:            "STARTING",
	}

	if err := WriteLaunchRecord(dir, rec); err != nil {
		t.Fatalf("write launch record: %v", err)
	}

	readBack, err := ReadLaunchRecord(dir)
	if err != nil {
		t.Fatalf("read launch record: %v", err)
	}

	if readBack.AttemptID != rec.AttemptID {
		t.Errorf("attempt id: got %s, want %s", readBack.AttemptID, rec.AttemptID)
	}
	if !readBack.Timestamp.Equal(rec.Timestamp) {
		t.Errorf("timestamp: got %s, want %s", readBack.Timestamp, rec.Timestamp)
	}
	if readBack.CardHash != rec.CardHash {
		t.Errorf("card hash: got %s, want %s", readBack.CardHash, rec.CardHash)
	}
	if readBack.Lane != rec.Lane {
		t.Errorf("lane: got %s, want %s", readBack.Lane, rec.Lane)
	}
	if readBack.Slot != rec.Slot {
		t.Errorf("slot: got %s, want %s", readBack.Slot, rec.Slot)
	}
	if readBack.Job != rec.Job {
		t.Errorf("job: got %s, want %s", readBack.Job, rec.Job)
	}
	if readBack.Node != rec.Node {
		t.Errorf("node: got %s, want %s", readBack.Node, rec.Node)
	}
	if readBack.RunningCommitSHA != rec.RunningCommitSHA {
		t.Errorf("running commit sha: got %s, want %s", readBack.RunningCommitSHA, rec.RunningCommitSHA)
	}
	if readBack.Pid != rec.Pid {
		t.Errorf("pid: got %d, want %d", readBack.Pid, rec.Pid)
	}
	if readBack.State != rec.State {
		t.Errorf("state: got %s, want %s", readBack.State, rec.State)
	}

	// Verify no temporary files were left behind
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".launch-") {
			t.Errorf("stray temporary file found: %s", e.Name())
		}
	}
}

// TestRecordLaunch_SlotAndJob verifies that both the slot directory and the
// job directory receive atomic, identical durable launch records.
func TestRecordLaunch_SlotAndJob(t *testing.T) {
	root := t.TempDir()
	slotDir := filepath.Join(root, "slots", "1")
	jobDir := filepath.Join(slotDir, "jobs", "card-101")

	now := time.Now().UTC()
	rec := LaunchRecord{
		AttemptID:        MustMintAttemptID(),
		Timestamp:        now,
		CardHash:         "feedface12345678",
		Lane:             "from-emma",
		Slot:             "1",
		Job:              "card-101",
		Node:             "superman",
		RunningCommitSHA: "11223344556677889900aabbccddeeff11223344",
		State:            "STARTING",
	}

	if err := RecordLaunch(slotDir, jobDir, rec); err != nil {
		t.Fatalf("record launch: %v", err)
	}

	// Verify slot record
	slotRec, err := ReadLaunchRecord(slotDir)
	if err != nil {
		t.Fatalf("read slot launch record: %v", err)
	}
	if slotRec.AttemptID != rec.AttemptID || slotRec.Job != rec.Job {
		t.Errorf("slot record mismatch: %+v", slotRec)
	}

	// Verify job record
	jobRec, err := ReadLaunchRecord(jobDir)
	if err != nil {
		t.Fatalf("read job launch record: %v", err)
	}
	if jobRec.AttemptID != rec.AttemptID || jobRec.Job != rec.Job {
		t.Errorf("job record mismatch: %+v", jobRec)
	}
}

// TestLaunchRecord_PersistenceAcrossSuddenWorkerCrash proves Requirement 4:
// A launch record is persisted atomically BEFORE execution begins. When the worker
// crashes suddenly (e.g. abrupt SIGKILL / death before completion receipt), the
// launch record remains fully intact on disk and the status query identifies it as CRASHED.
func TestLaunchRecord_PersistenceAcrossSuddenWorkerCrash(t *testing.T) {
	root := t.TempDir()
	slotDir := filepath.Join(root, "slots", "slot-crash")
	jobDir := filepath.Join(slotDir, "jobs", "card-crash-test")

	attemptID := MustMintAttemptID()
	cardHash := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	runningCommit := "c9273e5f3a4c7a00d9ee60d9f713a55dcaf5a3b1"
	lane := "from-emma"

	// 1. Prepare launch record BEFORE execution
	launchRec := LaunchRecord{
		AttemptID:        attemptID,
		Timestamp:        time.Now().UTC(),
		CardHash:         cardHash,
		Lane:             lane,
		Slot:             "slot-crash",
		Job:              "card-crash-test",
		Node:             "batman",
		RunningCommitSHA: runningCommit,
		State:            "STARTING",
	}

	// 2. Atomically persist to slot and job directories BEFORE starting worker
	if err := RecordLaunch(slotDir, jobDir, launchRec); err != nil {
		t.Fatalf("atomic record launch before execution: %v", err)
	}

	// 3. Start a worker process
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start worker process: %v", err)
	}
	workerPID := cmd.Process.Pid

	// Update PID in launch record
	launchRec.Pid = workerPID
	launchRec.State = "STARTED"
	if err := RecordLaunch(slotDir, jobDir, launchRec); err != nil {
		t.Fatalf("update launch record with pid: %v", err)
	}

	// 4. Sudden worker crash: KILL the process abruptly with SIGKILL
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("failed to send SIGKILL: %v", err)
	}
	_ = cmd.Wait() // Reaped; process is now definitively dead

	// 5. Verify the durable launch record on disk is completely intact and readable!
	persistedRec, err := ReadLaunchRecord(slotDir)
	if err != nil {
		t.Fatalf("launch record unreadable after worker crash: %v", err)
	}

	if persistedRec.AttemptID != attemptID {
		t.Errorf("attempt id mismatch: got %s, want %s", persistedRec.AttemptID, attemptID)
	}
	if persistedRec.CardHash != cardHash {
		t.Errorf("card hash mismatch: got %s, want %s", persistedRec.CardHash, cardHash)
	}
	if persistedRec.Lane != lane {
		t.Errorf("lane mismatch: got %s, want %s", persistedRec.Lane, lane)
	}
	if persistedRec.Slot != "slot-crash" {
		t.Errorf("slot mismatch: got %s, want slot-crash", persistedRec.Slot)
	}
	if persistedRec.Job != "card-crash-test" {
		t.Errorf("job mismatch: got %s, want card-crash-test", persistedRec.Job)
	}
	if persistedRec.RunningCommitSHA != runningCommit {
		t.Errorf("running commit mismatch: got %s, want %s", persistedRec.RunningCommitSHA, runningCommit)
	}
	if persistedRec.Pid != workerPID {
		t.Errorf("pid mismatch: got %d, want %d", persistedRec.Pid, workerPID)
	}

	// 6. Query node status to prove that sudden crash without completion receipt is detected as CRASHED
	nodeStatus, err := QueryNodeStatus(QueryNodeOptions{
		NodeName: "batman",
		NodeRoot: root,
		DiskSHA:  runningCommit,
	})
	if err != nil {
		t.Fatalf("query node status: %v", err)
	}

	if len(nodeStatus.Slots) != 1 {
		t.Fatalf("expected 1 slot, got %d", len(nodeStatus.Slots))
	}
	slotStatus := nodeStatus.Slots[0]
	if slotStatus.LiveStatus != LiveStatusCrashed {
		t.Errorf("expected slot live status to be CRASHED after sudden death, got %s", slotStatus.LiveStatus)
	}
	if slotStatus.AttemptID != attemptID {
		t.Errorf("expected attempt id %s in status query, got %s", attemptID, slotStatus.AttemptID)
	}
	if !slotStatus.SHAMatch {
		t.Errorf("expected SHAMatch true since running == disk, got false")
	}
}
