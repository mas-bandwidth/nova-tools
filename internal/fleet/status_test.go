package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFleetStatus_QueryAccuracy proves Requirement 3 & 4:
// Accurate per-node and per-slot live execution status reporting and
// running commit SHA vs disk SHA drift detection across running, crashed,
// completed, and idle slots.
func TestFleetStatus_QueryAccuracy(t *testing.T) {
	nodeRoot := t.TempDir()
	diskSHA := "86abcf23dd6cd95668ae1a865e11e29556996b03"

	// Slot 1: Running worker with matching commit SHA
	slot1Dir := filepath.Join(nodeRoot, "slots", "slot-1")
	job1Dir := filepath.Join(slot1Dir, "jobs", "job-running-match")
	rec1 := LaunchRecord{
		AttemptID:        MustMintAttemptID(),
		Timestamp:        time.Now().UTC().Add(-10 * time.Minute),
		CardHash:         "hash111111111111111111111111111111111111111111111111111111111111",
		Lane:             "from-emma",
		Slot:             "slot-1",
		Job:              "job-running-match",
		RunningCommitSHA: diskSHA, // matches disk!
		Pid:              1001,
		State:            "STARTED",
	}
	if err := RecordLaunch(slot1Dir, job1Dir, rec1); err != nil {
		t.Fatalf("record slot 1: %v", err)
	}

	// Slot 2: Running worker with drifted commit SHA (running old commit)
	slot2Dir := filepath.Join(nodeRoot, "slots", "slot-2")
	job2Dir := filepath.Join(slot2Dir, "jobs", "job-running-drift")
	rec2 := LaunchRecord{
		AttemptID:        MustMintAttemptID(),
		Timestamp:        time.Now().UTC().Add(-30 * time.Minute),
		CardHash:         "hash222222222222222222222222222222222222222222222222222222222222",
		Lane:             "from-emma",
		Slot:             "slot-2",
		Job:              "job-running-drift",
		RunningCommitSHA: "0000000000000000000000000000000000000000", // stale commit!
		Pid:              1002,
		State:            "STARTED",
	}
	if err := RecordLaunch(slot2Dir, job2Dir, rec2); err != nil {
		t.Fatalf("record slot 2: %v", err)
	}

	// Slot 3: Crashed worker (dead PID, no RESULT.md)
	slot3Dir := filepath.Join(nodeRoot, "slots", "slot-3")
	job3Dir := filepath.Join(slot3Dir, "jobs", "job-crashed")
	rec3 := LaunchRecord{
		AttemptID:        MustMintAttemptID(),
		Timestamp:        time.Now().UTC().Add(-15 * time.Minute),
		CardHash:         "hash333333333333333333333333333333333333333333333333333333333333",
		Lane:             "from-emma",
		Slot:             "slot-3",
		Job:              "job-crashed",
		RunningCommitSHA: diskSHA,
		Pid:              1003,
		State:            "STARTED",
	}
	if err := RecordLaunch(slot3Dir, job3Dir, rec3); err != nil {
		t.Fatalf("record slot 3: %v", err)
	}

	// Slot 4: Completed worker (dead PID, valid RESULT.md present)
	slot4Dir := filepath.Join(nodeRoot, "slots", "slot-4")
	job4Dir := filepath.Join(slot4Dir, "jobs", "job-completed")
	rec4 := LaunchRecord{
		AttemptID:        MustMintAttemptID(),
		Timestamp:        time.Now().UTC().Add(-45 * time.Minute),
		CardHash:         "hash444444444444444444444444444444444444444444444444444444444444",
		Lane:             "from-emma",
		Slot:             "slot-4",
		Job:              "job-completed",
		RunningCommitSHA: diskSHA,
		Pid:              1004,
		State:            "COMPLETED",
	}
	if err := RecordLaunch(slot4Dir, job4Dir, rec4); err != nil {
		t.Fatalf("record slot 4: %v", err)
	}
	if err := os.WriteFile(filepath.Join(job4Dir, "RESULT.md"), []byte("RESULT OK card-completed\napproved\n"), 0o644); err != nil {
		t.Fatalf("write result.md: %v", err)
	}

	// Slot 5: Idle slot (no launch record, just empty slot dir)
	slot5Dir := filepath.Join(nodeRoot, "slots", "slot-5")
	if err := os.MkdirAll(slot5Dir, 0o755); err != nil {
		t.Fatalf("create slot 5: %v", err)
	}

	// Define fake liveness: 1001 and 1002 are alive; 1003 and 1004 are dead
	mockAlive := func(pid int) bool {
		return pid == 1001 || pid == 1002
	}

	opts := QueryNodeOptions{
		NodeName: "batman",
		NodeRoot: nodeRoot,
		DiskSHA:  diskSHA,
		IsAlive:  mockAlive,
	}

	nodeStatus, err := QueryNodeStatus(opts)
	if err != nil {
		t.Fatalf("query node status: %v", err)
	}

	if nodeStatus.Name != "batman" {
		t.Errorf("node name: got %s, want batman", nodeStatus.Name)
	}
	if nodeStatus.DiskSHA != diskSHA {
		t.Errorf("disk sha: got %s, want %s", nodeStatus.DiskSHA, diskSHA)
	}
	if len(nodeStatus.Slots) != 5 {
		t.Fatalf("expected 5 slots, got %d", len(nodeStatus.Slots))
	}

	// Check Slot 1
	s1 := nodeStatus.Slots[0]
	if s1.LiveStatus != LiveStatusRunning {
		t.Errorf("slot 1 status: got %s, want RUNNING", s1.LiveStatus)
	}
	if !s1.SHAMatch {
		t.Errorf("slot 1 SHAMatch: got false, want true")
	}
	if s1.RunningCommitSHA != diskSHA {
		t.Errorf("slot 1 running sha: got %s, want %s", s1.RunningCommitSHA, diskSHA)
	}

	// Check Slot 2
	s2 := nodeStatus.Slots[1]
	if s2.LiveStatus != LiveStatusRunning {
		t.Errorf("slot 2 status: got %s, want RUNNING", s2.LiveStatus)
	}
	if s2.SHAMatch {
		t.Errorf("slot 2 SHAMatch: got true, want false (drifted!)")
	}
	if s2.RunningCommitSHA != "0000000000000000000000000000000000000000" {
		t.Errorf("slot 2 running sha mismatch")
	}

	// Check Slot 3
	s3 := nodeStatus.Slots[2]
	if s3.LiveStatus != LiveStatusCrashed {
		t.Errorf("slot 3 status: got %s, want CRASHED", s3.LiveStatus)
	}

	// Check Slot 4
	s4 := nodeStatus.Slots[3]
	if s4.LiveStatus != LiveStatusCompleted {
		t.Errorf("slot 4 status: got %s, want COMPLETED", s4.LiveStatus)
	}

	// Check Slot 5
	s5 := nodeStatus.Slots[4]
	if s5.LiveStatus != LiveStatusIdle {
		t.Errorf("slot 5 status: got %s, want IDLE", s5.LiveStatus)
	}

	// Check node counts
	if nodeStatus.LiveCount != 2 {
		t.Errorf("live count: got %d, want 2", nodeStatus.LiveCount)
	}
	if nodeStatus.CrashedCount != 1 {
		t.Errorf("crashed count: got %d, want 1", nodeStatus.CrashedCount)
	}
	if nodeStatus.CompletedCount != 1 {
		t.Errorf("completed count: got %d, want 1", nodeStatus.CompletedCount)
	}
	if nodeStatus.IdleCount != 1 {
		t.Errorf("idle count: got %d, want 1", nodeStatus.IdleCount)
	}
	if nodeStatus.DriftCount != 1 {
		t.Errorf("drift count: got %d, want 1", nodeStatus.DriftCount)
	}

	// Query whole fleet
	fleetReport, err := QueryFleetStatus([]QueryNodeOptions{opts})
	if err != nil {
		t.Fatalf("query fleet status: %v", err)
	}

	if fleetReport.Summary.TotalNodes != 1 {
		t.Errorf("summary total nodes: got %d, want 1", fleetReport.Summary.TotalNodes)
	}
	if fleetReport.Summary.TotalSlots != 5 {
		t.Errorf("summary total slots: got %d, want 5", fleetReport.Summary.TotalSlots)
	}
	if fleetReport.Summary.LiveSlots != 2 {
		t.Errorf("summary live slots: got %d, want 2", fleetReport.Summary.LiveSlots)
	}
	if fleetReport.Summary.CrashedSlots != 1 {
		t.Errorf("summary crashed slots: got %d, want 1", fleetReport.Summary.CrashedSlots)
	}
	if fleetReport.Summary.DriftCount != 1 {
		t.Errorf("summary drift count: got %d, want 1", fleetReport.Summary.DriftCount)
	}

	// Test Formatted text output
	formatted := FormatFleetStatus(fleetReport)
	if !strings.Contains(formatted, "FLEET NODE name=batman disk_sha=86abcf23") {
		t.Errorf("formatted missing node line:\n%s", formatted)
	}
	if !strings.Contains(formatted, "status=RUNNING") || !strings.Contains(formatted, "status=CRASHED") {
		t.Errorf("formatted missing status lines:\n%s", formatted)
	}
	if !strings.Contains(formatted, "FLEET SUMMARY nodes=1 slots=5 live=2 crashed=1 completed=1 idle=1 drift=1") {
		t.Errorf("formatted missing summary line:\n%s", formatted)
	}

	// Test JSON marshal
	rawJSON, err := json.Marshal(fleetReport)
	if err != nil {
		t.Fatalf("json marshal fleet report: %v", err)
	}
	var unmarshaled FleetStatusReport
	if err := json.Unmarshal(rawJSON, &unmarshaled); err != nil {
		t.Fatalf("json unmarshal fleet report: %v", err)
	}
	if unmarshaled.Summary.LiveSlots != 2 {
		t.Errorf("unmarshaled json live slots mismatch")
	}
}
