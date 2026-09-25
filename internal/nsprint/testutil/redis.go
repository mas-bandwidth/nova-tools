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
	"strconv"
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
//
// Between FreePort closing the port and redis-server binding it, a parallel
// test can take the same port. The loser logs "Address already in use" and
// exits, while its readiness PING is answered by the winner, so two tests
// would share one store (#4027: two harvest subtests' benches fenced each
// other's lease). So the answer counts only from this test's own server --
// INFO's process_id is its pid, or under NOAUTH its log says it is listening
// -- and a server that lost the bind is started again on a new port.
func Start(t *testing.T, extra ...string) string {
	t.Helper()
	bin := Program(t)
	const tries = 8
	for i := 1; ; i++ {
		addr, lostBind := startOnce(t, bin, FreePort(t), extra)
		if !lostBind {
			return addr
		}
		if i == tries {
			t.Fatalf("throwaway redis lost the bind on %d free ports in a row (last %s)", tries, addr)
		}
	}
}

// startOnce starts one redis-server on port and waits for it. It returns the
// address and whether the server exited because another process holds the
// port; any other failure is fatal.
func startOnce(t *testing.T, bin, port string, extra []string) (string, bool) {
	t.Helper()
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
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	pid := "process_id:" + strconv.Itoa(cmd.Process.Pid) + "\r\n"
	deadline := time.Now().Add(30 * time.Second)
	var pingErr error
	for {
		select {
		case <-exited:
			body, _ := os.ReadFile(logPath)
			if strings.Contains(string(body), "Address already in use") {
				return addr, true
			}
			t.Fatalf("throwaway redis on %s exited: %s", addr, body)
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		pingErr = client.Ping(ctx).Err()
		switch {
		case pingErr == nil:
			if info, err := client.Info(ctx, "server").Result(); err == nil && strings.Contains(info, pid) {
				cancel()
				return addr, false
			}
		case strings.Contains(pingErr.Error(), "NOAUTH"):
			if body, _ := os.ReadFile(logPath); strings.Contains(string(body), "Ready to accept connections") {
				cancel()
				return addr, false
			}
		}
		cancel()
		if time.Now().After(deadline) {
			body, _ := os.ReadFile(logPath)
			t.Fatalf("throwaway redis did not start on %s (last ping %v; answered by another server?)\n%s", addr, pingErr, body)
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
