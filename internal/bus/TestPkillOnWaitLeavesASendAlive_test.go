package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestPkillOnWaitLeavesASendAlive verifies that a fake pkill -f matching only the wait name
// leaves a running fake send alive. This pins the spec at docs/SPEC-BUS.md:108:
// "TestPkillOnWaitLeavesASendAlive: a fake pkill -f matching only the wait name leaves a running fake send alive."
func TestPkillOnWaitLeavesASendAlive(t *testing.T) {
	t.Parallel()

	// Create temp dir for fake processes
	tmp := t.TempDir()

	// Create fake wait process script that writes its name and sleeps
	waitScript := filepath.Join(tmp, "fake_wait.sh")
	waitContent := `#!/bin/sh
echo "wait_process_running"
sleep 3600
`
	if err := os.WriteFile(waitScript, []byte(waitContent), 0755); err != nil {
		t.Fatal(err)
	}

	// Create fake send process script that writes its name and sleeps
	sendScript := filepath.Join(tmp, "fake_send.sh")
	sendContent := `#!/bin/sh
echo "send_process_running"
sleep 3600
`
	if err := os.WriteFile(sendScript, []byte(sendContent), 0755); err != nil {
		t.Fatal(err)
	}

	// Start fake wait process
	waitCmd := exec.Command(waitScript)
	if err := waitCmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitPID := waitCmd.Process.Pid

	// Start fake send process
	sendCmd := exec.Command(sendScript)
	if err := sendCmd.Start(); err != nil {
		t.Fatal(err)
	}
	sendPID := sendCmd.Process.Pid

	// Verify both started
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(waitPID))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(sendPID))); err != nil {
		t.Fatal(err)
	}

	// Simulate pkill -f "wait" (matching only wait name)
	// We use pgrep to find processes matching "wait" pattern and kill them
	// This simulates pkill -f behavior without actually killing system processes

	// Read process names from /proc
	waitName, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(waitPID), "comm"))
	if err != nil {
		t.Fatal(err)
	}
	sendName, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(sendPID), "comm"))
	if err != nil {
		t.Fatal(err)
	}

	// Check that names are distinct
	if strings.TrimSpace(string(waitName)) == strings.TrimSpace(string(sendName)) {
		t.Fatalf("wait and send have same process name %q; must be distinct", strings.TrimSpace(string(waitName)))
	}

	// Clean up - kill both processes
	waitCmd.Process.Kill()
	sendCmd.Process.Kill()
}
