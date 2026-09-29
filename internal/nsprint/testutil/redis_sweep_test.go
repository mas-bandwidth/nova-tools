package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPIDDir(t *testing.T) {
	t.Parallel()
	dir := PIDDir()
	if dir == "" {
		t.Fatal("PIDDir returned empty string")
	}
	if !strings.HasSuffix(filepath.ToSlash(dir), "nova-test-redis") {
		t.Fatalf("PIDDir %q does not end with nova-test-redis", dir)
	}
}

func TestParsePIDFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	// Key-value formatted
	kvPath := filepath.Join(tmp, "1234.pid")
	content := "pid=5678\nport=1234\nppid=999\n"
	if err := os.WriteFile(kvPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	rec, err := parsePIDFile(kvPath)
	if err != nil {
		t.Fatalf("parsePIDFile kv failed: %v", err)
	}
	if rec.PID != 5678 || rec.Port != "1234" || rec.CreatorPID != 999 {
		t.Fatalf("got pid=%d port=%s ppid=%d; want 5678, 1234, 999", rec.PID, rec.Port, rec.CreatorPID)
	}

	// Plain lines formatted
	plainPath := filepath.Join(tmp, "4321.pid")
	plainContent := "8765\n4321\n111\n"
	if err := os.WriteFile(plainPath, []byte(plainContent), 0644); err != nil {
		t.Fatal(err)
	}
	rec2, err := parsePIDFile(plainPath)
	if err != nil {
		t.Fatalf("parsePIDFile plain failed: %v", err)
	}
	if rec2.PID != 8765 || rec2.Port != "4321" || rec2.CreatorPID != 111 {
		t.Fatalf("got pid=%d port=%s ppid=%d; want 8765, 4321, 111", rec2.PID, rec2.Port, rec2.CreatorPID)
	}

	// Port inferred from filename when missing from plain format
	inferredPath := filepath.Join(tmp, "7777.pid")
	if err := os.WriteFile(inferredPath, []byte("3333\n"), 0644); err != nil {
		t.Fatal(err)
	}
	rec3, err := parsePIDFile(inferredPath)
	if err != nil {
		t.Fatalf("parsePIDFile inferred failed: %v", err)
	}
	if rec3.PID != 3333 || rec3.Port != "7777" {
		t.Fatalf("got pid=%d port=%s; want 3333, 7777", rec3.PID, rec3.Port)
	}
}

func TestSweepOrphansInEmptyDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if swept := SweepOrphansIn(tmp); swept != 0 {
		t.Fatalf("SweepOrphansIn on empty dir = %d, want 0", swept)
	}
	nonexistent := filepath.Join(tmp, "does-not-exist")
	if swept := SweepOrphansIn(nonexistent); swept != 0 {
		t.Fatalf("SweepOrphansIn on nonexistent dir = %d, want 0", swept)
	}
}

func TestSweepOrphansInStalePIDFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	pidPath := filepath.Join(tmp, "9876.pid")
	content := "pid=9999999\nport=9876\nppid=9999998\n"
	if err := os.WriteFile(pidPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	swept := SweepOrphansIn(tmp)
	if swept != 1 {
		t.Fatalf("SweepOrphansIn = %d, want 1", swept)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("stale pid file %s was not removed", pidPath)
	}
}

func TestSweepOrphansInNonRedisProcess(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	pidPath := filepath.Join(tmp, "2222.pid")
	// The current process is a Go test runner, NOT redis-server.
	// Creator PID is dead (9999999).
	content := fmt.Sprintf("pid=%d\nport=2222\nppid=9999999\n", os.Getpid())
	if err := os.WriteFile(pidPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	swept := SweepOrphansIn(tmp)
	if swept != 1 {
		t.Fatalf("SweepOrphansIn non-redis process = %d, want 1", swept)
	}
	// The file should be deleted because PID was recycled / wrong command
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("stale pid file %s for non-redis process was not removed", pidPath)
	}
	// And this test process must still be alive!
	if !isProcessRunning(os.Getpid()) {
		t.Fatal("current process was incorrectly killed")
	}
}

func TestSweepOrphansInCorruptFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	pidPath := filepath.Join(tmp, "bad.pid")
	if err := os.WriteFile(pidPath, []byte("not a valid pid file"), 0644); err != nil {
		t.Fatal(err)
	}
	swept := SweepOrphansIn(tmp)
	if swept != 1 {
		t.Fatalf("SweepOrphansIn corrupt file = %d, want 1", swept)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("corrupt pid file %s was not removed", pidPath)
	}
}
