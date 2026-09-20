package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// TestNativeRun_PersistsDurableLaunchRecord verifies that nativeRun atomically persists
// a launch record in both slotDir and jobDir before execution begins.
func TestNativeRun_PersistsDurableLaunchRecord(t *testing.T) {
	root := t.TempDir()
	slotDir := filepath.Join(root, "1")
	cardText := []byte("contract: TEST card-durable-native at rev1\nhello\n")

	// Create a dummy harness binary (sh script that exits 0)
	harnessDir := t.TempDir()
	harnessPath := filepath.Join(harnessDir, "fake-harness")
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(harnessPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := nativeRunConfig{
		binary:   harnessPath,
		model:    "provider/model",
		label:    "card-durable-native",
		card:     cardText,
		slotDir:  slotDir,
		root:     root,
		deadline: 10 * time.Second,
		noWall:   true,
	}

	res, rc := nativeRun(cfg, os.Stderr)
	if rc != 0 {
		t.Fatalf("nativeRun returned rc %d", rc)
	}
	if res.rc != 0 {
		t.Fatalf("child process exited with %d", res.rc)
	}

	// Verify slot launch record
	slotRec, err := fleet.ReadLaunchRecord(slotDir)
	if err != nil {
		t.Fatalf("read slot launch record: %v", err)
	}
	if slotRec.AttemptID == "" {
		t.Errorf("slot record missing attempt id")
	}
	if slotRec.CardHash != res.cardSHA256 {
		t.Errorf("slot record card hash: got %s want %s", slotRec.CardHash, res.cardSHA256)
	}
	if slotRec.Job != "card-durable-native" {
		t.Errorf("slot record job: got %s want card-durable-native", slotRec.Job)
	}
	if slotRec.Lane != "native" {
		t.Errorf("slot record lane: got %s want native", slotRec.Lane)
	}

	// Verify job launch record
	jobDir := filepath.Join(slotDir, "jobs", "card-durable-native")
	jobRec, err := fleet.ReadLaunchRecord(jobDir)
	if err != nil {
		t.Fatalf("read job launch record: %v", err)
	}
	if jobRec.AttemptID != slotRec.AttemptID {
		t.Errorf("job record attempt id %s does not match slot %s", jobRec.AttemptID, slotRec.AttemptID)
	}
	if jobRec.CardHash != res.cardSHA256 {
		t.Errorf("job record card hash: got %s want %s", jobRec.CardHash, res.cardSHA256)
	}
	if jobRec.Pid <= 0 {
		t.Errorf("expected recorded PID > 0, got %d", jobRec.Pid)
	}
	if jobRec.State != "COMPLETED" {
		t.Errorf("expected state COMPLETED, got %s", jobRec.State)
	}
}
