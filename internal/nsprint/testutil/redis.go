// Package testutil starts the throwaway Redis the nova-sprint controls share.
// One helper, so a missing redis-server has one answer: skip on a laptop, fail
// when NOVA_CI=1. The process is private. It is not the fleet store, and this
// file names no bench address.
package testutil

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		Absent(t, err)
	}
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
	dir := t.TempDir()
	logf, err := os.Create(filepath.Join(dir, "redis.log"))
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
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(30 * time.Second)
	var pingErr error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		pingErr = client.Ping(ctx).Err()
		cancel()
		if pingErr == nil || strings.Contains(pingErr.Error(), "NOAUTH") {
			return addr
		}
		if time.Now().After(deadline) {
			body, _ := os.ReadFile(filepath.Join(dir, "redis.log"))
			t.Fatalf("throwaway redis did not start: %v\n%s", pingErr, body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
