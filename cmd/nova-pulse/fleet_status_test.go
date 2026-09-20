package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// TestCmdFleetStatus verifies the `nova-pulse fleet status` CLI command.
func TestCmdFleetStatus(t *testing.T) {
	root := t.TempDir()
	slotDir := filepath.Join(root, "slots", "slot-1")
	jobDir := filepath.Join(slotDir, "jobs", "card-101")

	rec := fleet.LaunchRecord{
		AttemptID:        fleet.MustMintAttemptID(),
		Timestamp:        time.Now().UTC(),
		CardHash:         "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Lane:             "from-emma",
		Slot:             "slot-1",
		Job:              "card-101",
		RunningCommitSHA: "86abcf23dd6cd95668ae1a865e11e29556996b03",
		State:            "STARTING",
	}
	if err := fleet.RecordLaunch(slotDir, jobDir, rec); err != nil {
		t.Fatalf("record launch: %v", err)
	}

	t.Run("text output with --root", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		rc := cmdFleet([]string{"status", "--root", root, "--bench", "batman"}, &stdout, &stderr)
		if rc != 0 {
			t.Fatalf("expected rc 0, got %d; stderr: %s", rc, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "FLEET NODE name=batman") {
			t.Errorf("stdout missing node name:\n%s", out)
		}
		if !strings.Contains(out, "slot=slot-1") || !strings.Contains(out, "job=card-101") {
			t.Errorf("stdout missing slot details:\n%s", out)
		}
		if !strings.Contains(out, "FLEET SUMMARY") {
			t.Errorf("stdout missing summary line:\n%s", out)
		}
	})

	t.Run("json output with --json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		rc := cmdFleet([]string{"status", "--root", root, "--bench", "batman", "--json"}, &stdout, &stderr)
		if rc != 0 {
			t.Fatalf("expected rc 0, got %d; stderr: %s", rc, stderr.String())
		}
		var report fleet.FleetStatusReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("invalid json output: %v\n%s", err, stdout.String())
		}
		if len(report.Nodes) != 1 || report.Nodes[0].Name != "batman" {
			t.Errorf("unexpected json node report: %+v", report.Nodes)
		}
		if len(report.Nodes[0].Slots) != 1 || report.Nodes[0].Slots[0].Slot != "slot-1" {
			t.Errorf("unexpected json slot report: %+v", report.Nodes[0].Slots)
		}
	})

	t.Run("with --machines registry file", func(t *testing.T) {
		machinesFile := filepath.Join(t.TempDir(), "machines.tsv")
		content := "batman\tbatman.internal\tdarwin/arm64\tbench\t-\t10\t-\n"
		if err := os.WriteFile(machinesFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		rc := cmdFleet([]string{"status", "--machines", machinesFile, "--root", root}, &stdout, &stderr)
		if rc != 0 {
			t.Fatalf("expected rc 0, got %d; stderr: %s", rc, stderr.String())
		}
		if !strings.Contains(stdout.String(), "FLEET NODE name=batman") {
			t.Errorf("stdout missing batman node:\n%s", stdout.String())
		}
	})

	t.Run("refuses when neither --machines nor --root is provided", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		rc := cmdFleet([]string{"status"}, &stdout, &stderr)
		if rc != 2 {
			t.Fatalf("expected refusal rc 2, got %d", rc)
		}
		if !strings.Contains(stderr.String(), "either --machines <file> or --root <dir> is required") {
			t.Errorf("unexpected stderr: %s", stderr.String())
		}
	})
}
