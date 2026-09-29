package testredis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func init() {
	switch os.Getenv("NOVA_TESTREDIS_HELPER") {
	case "sleep":
		select {}
	case "exit":
		os.Exit(0)
	}
}

func startHelperSleep(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "NOVA_TESTREDIS_HELPER=sleep")
	if err := cmd.Start(); err != nil {
		t.Fatalf("startHelperSleep: %v", err)
	}
	return cmd
}

func getDeadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "NOVA_TESTREDIS_HELPER=exit")
	if err := cmd.Start(); err != nil {
		t.Fatalf("getDeadPID: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait getDeadPID: %v", err)
	}
	return pid
}

func TestRegisterAndUnregister(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pid := os.Getpid()
	port := "16379"
	ppid := os.Getppid()

	unregister := RegisterDir(dir, pid, port, ppid)
	filePath := filepath.Join(dir, fmt.Sprintf("%d.json", pid))

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("expected registry file %s to exist: %v", filePath, err)
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("corrupted JSON: %v", err)
	}

	if entry.PID != pid || entry.Port != port || entry.PPID != ppid {
		t.Errorf("entry mismatch: got %+v, want pid=%d port=%s ppid=%d", entry, pid, port, ppid)
	}
	if entry.StartedAt.IsZero() {
		t.Errorf("started_at is zero")
	}

	unregister()

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("expected registry file to be removed after unregister, err=%v", err)
	}

	// Double unregister should be a no-op
	unregister()
}

func TestSweepOrphansKillsDeadParentChild(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Start a running process
	child := startHelperSleep(t)
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})

	// Get a dead PID to act as dead parent
	deadPPID := getDeadPID(t)

	// Register entry with dead parent
	RegisterDir(dir, child.Process.Pid, "12345", deadPPID)

	var errBuf bytes.Buffer
	killed := SweepDir(dir, &errBuf)

	if killed != 1 {
		t.Fatalf("SweepDir returned killed=%d, want 1", killed)
	}

	wantLog := "SWEEP REDIS killed=1\n"
	if errBuf.String() != wantLog {
		t.Errorf("logged output %q, want %q", errBuf.String(), wantLog)
	}

	// Verify the process was actually terminated
	_ = child.Wait()
	if isProcessAlive(child.Process.Pid) {
		t.Errorf("process %d is still alive after sweep", child.Process.Pid)
	}

	// Verify the file was removed
	filePath := filepath.Join(dir, fmt.Sprintf("%d.json", child.Process.Pid))
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("registry file %s was not removed", filePath)
	}
}

func TestSweepOrphansLeavesLiveParentServerAlone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	child := startHelperSleep(t)
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})

	// Register entry where parent is os.Getpid() (current live process)
	RegisterDir(dir, child.Process.Pid, "12345", os.Getpid())

	var errBuf bytes.Buffer
	killed := SweepDir(dir, &errBuf)

	if killed != 0 {
		t.Fatalf("SweepDir returned killed=%d, want 0", killed)
	}
	if errBuf.Len() > 0 {
		t.Errorf("expected no output, got %q", errBuf.String())
	}

	// Process should still be alive
	if !isProcessAlive(child.Process.Pid) {
		t.Errorf("process %d was unexpectedly killed", child.Process.Pid)
	}

	// File should still exist
	filePath := filepath.Join(dir, fmt.Sprintf("%d.json", child.Process.Pid))
	if _, err := os.Stat(filePath); err != nil {
		t.Errorf("registry file %s should still exist: %v", filePath, err)
	}
}

func TestSweepOrphansCleansDeadServerEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	deadPID := getDeadPID(t)
	deadPPID := getDeadPID(t)

	RegisterDir(dir, deadPID, "12345", deadPPID)

	var errBuf bytes.Buffer
	killed := SweepDir(dir, &errBuf)

	if killed != 0 {
		t.Fatalf("SweepDir returned killed=%d, want 0 (server was already dead)", killed)
	}
	if errBuf.Len() > 0 {
		t.Errorf("expected no output, got %q", errBuf.String())
	}

	filePath := filepath.Join(dir, fmt.Sprintf("%d.json", deadPID))
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("registry file %s should have been removed", filePath)
	}
}

func TestSweepOrphansHandlesCorruptedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	deadPID := getDeadPID(t)
	filePath := filepath.Join(dir, fmt.Sprintf("%d.json", deadPID))
	_ = os.WriteFile(filePath, []byte("NOT_JSON"), 0644)

	var errBuf bytes.Buffer
	killed := SweepDir(dir, &errBuf)
	if killed != 0 {
		t.Errorf("expected 0 killed, got %d", killed)
	}

	// Since deadPID is not alive, corrupted file with dead pid in filename should be cleaned
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("corrupted file for dead pid was not removed")
	}
}
