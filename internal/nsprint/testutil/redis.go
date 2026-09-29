// Package testutil starts the throwaway Redis the nova-sprint controls share.
// One helper, so a missing redis-server has one answer: skip on a laptop, fail
// when NOVA_CI=1. The process is private. It is not the fleet store, and this
// file names no bench address.
package testutil

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// CIEnv is set to "1" by the CI workflows. A missing redis-server fails the
// test in that environment. Anywhere else it skips, so a laptop without the
// binary can still run the rest of the suite.
const CIEnv = "NOVA_CI"

// Absent is Start's answer when redis-server is not on PATH. CI fails closed.
// A skip here would let a green run be a run that never executed the store.
func Absent(t *testing.T, cause error) {
	t.Helper()
	if cause == nil {
		t.Fatal("Absent requires the error from looking up redis-server")
	}
	if os.Getenv(CIEnv) == "1" {
		t.Fatalf("redis-server is required under NOVA_CI=1: %v", cause)
	}
	t.Skipf("redis-server unavailable: %v", cause)
}

// PIDDir returns the directory where test redis PID files are recorded.
// It uses NOVA_TEST_REDIS_PID_DIR if set, otherwise $TMPDIR/nova-test-redis
// or os.TempDir()/nova-test-redis.
func PIDDir() string {
	if d := os.Getenv("NOVA_TEST_REDIS_PID_DIR"); d != "" {
		return d
	}
	tmp := os.Getenv("TMPDIR")
	if tmp == "" {
		tmp = os.TempDir()
	}
	return filepath.Join(tmp, "nova-test-redis")
}

// SweepOrphans scans PIDDir() for test redis PID files, checks each PID, and
// kills orphaned or dead processes while deleting their PID files.
// When swept count > 0, it prints to os.Stderr.
func SweepOrphans() int {
	return SweepOrphansIn(PIDDir())
}

// SweepOrphansIn scans dir for test redis PID files, checks each PID, and
// kills orphaned or dead processes while deleting their PID files.
// When swept count > 0, it prints to os.Stderr.
func SweepOrphansIn(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	swept := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		rec, err := parsePIDFile(path)
		if err != nil || rec.PID <= 0 {
			if err := os.Remove(path); err == nil {
				swept++
			}
			continue
		}
		if !isProcessRunning(rec.PID) {
			if err := os.Remove(path); err == nil {
				swept++
			}
			continue
		}
		comm, ppid := getProcessInfo(rec.PID)
		if comm == "" && ppid == 0 && !isProcessRunning(rec.PID) {
			if err := os.Remove(path); err == nil {
				swept++
			}
			continue
		}
		if !strings.Contains(strings.ToLower(comm), "redis-server") {
			// Process recycled by another program; do not kill it, just remove stale pid file.
			if err := os.Remove(path); err == nil {
				swept++
			}
			continue
		}
		isOrphan := false
		if ppid == 1 {
			isOrphan = true
		} else if rec.CreatorPID > 0 && rec.CreatorPID != os.Getpid() {
			if !isProcessRunning(rec.CreatorPID) {
				isOrphan = true
			}
		} else if rec.CreatorPID <= 0 && ppid != os.Getpid() {
			isOrphan = true
		}

		if isOrphan {
			killRedisProcess(rec.PID)
			if err := os.Remove(path); err == nil {
				swept++
			}
		}
	}
	if swept > 0 {
		fmt.Fprintf(os.Stderr, "nova-test-redis: swept %d orphaned redis-server(s)\n", swept)
	}
	return swept
}

type pidRecord struct {
	PID        int
	Port       string
	CreatorPID int
}

func parsePIDFile(path string) (*pidRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rec := &pidRecord{}
	var plainLines []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			k = strings.TrimSpace(strings.ToLower(k))
			v = strings.TrimSpace(v)
			switch k {
			case "pid":
				rec.PID, _ = strconv.Atoi(v)
			case "port":
				rec.Port = v
			case "ppid":
				rec.CreatorPID, _ = strconv.Atoi(v)
			}
		} else {
			plainLines = append(plainLines, line)
		}
	}
	if rec.PID == 0 && len(plainLines) > 0 {
		rec.PID, _ = strconv.Atoi(plainLines[0])
		if len(plainLines) > 1 {
			rec.Port = plainLines[1]
		}
		if len(plainLines) > 2 {
			rec.CreatorPID, _ = strconv.Atoi(plainLines[2])
		}
	}
	if rec.Port == "" {
		base := filepath.Base(path)
		rec.Port = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return rec, nil
}

func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err != nil {
		return false
	}
	out, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(out))
	if strings.HasPrefix(state, "Z") {
		return false
	}
	return true
}

func getProcessInfo(pid int) (comm string, ppid int) {
	out, err := exec.Command("ps", "-o", "ppid=", "-o", "state=", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", 0
	}
	fields := strings.Fields(string(out))
	if len(fields) >= 3 {
		ppid, _ = strconv.Atoi(fields[0])
		state := fields[1]
		comm = strings.Join(fields[2:], " ")
		if strings.HasPrefix(state, "Z") || strings.Contains(comm, "<defunct>") {
			return "", 0
		}
	} else if len(fields) >= 2 {
		ppid, _ = strconv.Atoi(fields[0])
		comm = fields[1]
	}
	return comm, ppid
}

func killRedisProcess(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if !isProcessRunning(pid) {
			return
		}
	}
	_ = proc.Kill()
	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if !isProcessRunning(pid) {
			return
		}
	}
}

// Start runs a throwaway redis-server on 127.0.0.1 and returns host:port.
// The server uses --save "" and no append-only file, in the test's temporary
// directory, and the cleanup kills it. Extra arguments follow the fixed ones
// (for example --user lines that turn the default user off, the fleet shape);
// a NOAUTH answer to the readiness PING is a live server.
//
// Redis treats --port 0 as "do not listen" (it logs "Configured to not listen
// anywhere" and exits). The ephemeral port is taken here, the way the controls
// used to, and handed to redis-server as --port. That is a free loopback port,
// not a dial to a bench.
func Start(t *testing.T, extra ...string) string {
	t.Helper()
	SweepOrphans()
	bin := Program(t)
	// THE PORT IS TAKEN, CLOSED AND HANDED OVER, so another process can bind it
	// in between: with the package's tests in parallel that is another test's
	// redis-server. Ours then exits on "Address already in use" while the PING
	// below is answered by the other one, and the test silently shares a store.
	// So the server is ready only when its OWN log says it is, and a server that
	// lost its port is started again on a fresh one.
	var lastErr string
	for attempt := 0; attempt < 5; attempt++ {
		addr, ok, why := startOnce(t, bin, extra)
		if ok {
			return addr
		}
		lastErr = why
	}
	t.Fatalf("throwaway redis did not start in five attempts: %s", lastErr)
	return ""
}

// startOnce is one attempt: ok is false, with the server's log, when this
// server did not bind its port.
func startOnce(t *testing.T, bin string, extra []string) (string, bool, string) {
	t.Helper()
	port := FreePort(t)
	addr := net.JoinHostPort("127.0.0.1", port)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "redis.log")
	logf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logf.Close() })
	args := append([]string{"--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no", "--dir", dir}, extra...)
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatalf("redis-server did not start: %v", err)
	}
	pidDir := PIDDir()
	_ = os.MkdirAll(pidDir, 0755)
	pidPath := filepath.Join(pidDir, port+".pid")
	pidContent := fmt.Sprintf("pid=%d\nport=%s\nppid=%d\n", cmd.Process.Pid, port, os.Getpid())
	_ = os.WriteFile(pidPath, []byte(pidContent), 0644)

	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
		_ = os.Remove(pidPath)
	})
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer func() { _ = client.Close() }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-exited:
			body, _ := os.ReadFile(logPath)
			_ = os.Remove(pidPath)
			return "", false, string(body)
		default:
		}
		body, _ := os.ReadFile(logPath)
		if strings.Contains(string(body), "Ready to accept connections") {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			pingErr := client.Ping(ctx).Err()
			cancel()
			if pingErr == nil || strings.Contains(pingErr.Error(), "NOAUTH") {
				return addr, true, ""
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("throwaway redis did not start: %s", body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Program is the redis-server on PATH, or Absent's answer when there is none.
// A test that launches the server through its own production path (nova-redis
// serve) takes the program from here, so the missing-binary rule stays one.
func Program(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		Absent(t, err)
	}
	return bin
}

// FreePort takes a free loopback port and returns it as text. Redis treats
// --port 0 as "do not listen", so the port is taken here and handed over.
func FreePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("loopback port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	return port
}
