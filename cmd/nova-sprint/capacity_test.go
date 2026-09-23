package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

// TestCapacityAsActorControlReceipt exercises the specified --as control flag
// through the CLI, not merely through the Go capacity API. The actor must be
// preserved in the server-timed cap:log receipt.
func TestCapacityAsActorControlReceipt(t *testing.T) {
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skipf("redis-server unavailable: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	dir := t.TempDir()
	log, err := os.Create(filepath.Join(dir, "redis.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.Command("redis-server", "--bind", "127.0.0.1", "--port", strings.TrimPrefix(addr, "127.0.0.1:"), "--save", "", "--appendonly", "no", "--dir", dir)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if client.Ping(ctx).Err() == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("throwaway redis did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runCapacity(ctx, []string{"machine", "--redis", addr, "--as", "operator", "ctl-machine", "64"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity machine code=%d stderr=%q", code, errOut.String())
	}
	client.SAdd(ctx, "friends", "alice")
	out.Reset()
	errOut.Reset()
	if code := runCapacity(ctx, []string{"friend", "--redis", addr, "--as", "operator", "--machine", "ctl-machine", "alice", "32"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity friend code=%d stderr=%q", code, errOut.String())
	}
	entries, err := client.XRange(ctx, "cap:log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("receipts=%d want 2", len(entries))
	}
	for _, entry := range entries {
		if entry.Values["actor"] != "operator" {
			t.Fatalf("receipt actor=%v", entry.Values["actor"])
		}
	}
}
