//go:build functional

package testutil

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisServerRecordsPIDAndCleansUp(t *testing.T) {
	t.Parallel()

	var redisPID int
	var pidPath string

	t.Run("start", func(subT *testing.T) {
		addr := Start(subT)
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			subT.Fatalf("split host port: %v", err)
		}
		pidPath = filepath.Join(PIDDir(), port+".pid")
		rec, err := parsePIDFile(pidPath)
		if err != nil {
			subT.Fatalf("parse pid file %s: %v", pidPath, err)
		}
		if rec.PID <= 0 {
			subT.Fatalf("recorded pid %d <= 0", rec.PID)
		}
		if rec.Port != port {
			subT.Fatalf("recorded port %s != expected %s", rec.Port, port)
		}
		if rec.CreatorPID != os.Getpid() {
			subT.Fatalf("recorded creator pid %d != %d", rec.CreatorPID, os.Getpid())
		}
		if !isProcessRunning(rec.PID) {
			subT.Fatalf("redis process %d is not running", rec.PID)
		}
		redisPID = rec.PID
	})

	// Subtest has completed; its t.Cleanup must have killed the process and unlinked the PID file.
	if isProcessRunning(redisPID) {
		t.Fatalf("redis-server process %d is still running after test cleanup", redisPID)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("pid file %s was not unlinked after test cleanup", pidPath)
	}
}

func TestRedisSweepOrphanedServer(t *testing.T) {
	t.Parallel()

	bin := Program(t)
	port := FreePort(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "redis.log")
	logf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()

	cmd := exec.Command(bin, "--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no", "--dir", dir)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatalf("redis-server failed to start: %v", err)
	}
	// Fallback cleanup in case test fails early
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Wait until ready
	deadline := time.Now().Add(30 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		body, _ := os.ReadFile(logPath)
		if strings.Contains(string(body), "Ready to accept connections") {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("redis-server did not become ready")
	}

	// Write PID file in a private sweep dir, with a non-existent parent PID (simulating an orphan)
	sweepDir := t.TempDir()
	pidFile := filepath.Join(sweepDir, port+".pid")
	content := fmt.Sprintf("pid=%d\nport=%s\nppid=9999999\n", cmd.Process.Pid, port)
	if err := os.WriteFile(pidFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	swept := SweepOrphansIn(sweepDir)
	if swept != 1 {
		t.Fatalf("SweepOrphansIn = %d, want 1", swept)
	}

	// Since cmd is a direct child of this test runner, reap it.
	_ = cmd.Wait()

	// The orphaned redis-server process must have been killed
	if isProcessRunning(cmd.Process.Pid) {
		t.Fatalf("orphaned redis-server process %d is still alive", cmd.Process.Pid)
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("orphaned pid file %s was not deleted", pidFile)
	}
}

func TestRedisSweepIgnoresActiveChildServer(t *testing.T) {
	t.Parallel()

	addr := Start(t)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}

	pidFile := filepath.Join(PIDDir(), port+".pid")
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("pid file %s does not exist: %v", pidFile, err)
	}

	// Running sweep should not kill our active server
	_ = SweepOrphans()

	// Verify server is still running and answers PING
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("active redis server at %s stopped answering after sweep: %v", addr, err)
	}
}
