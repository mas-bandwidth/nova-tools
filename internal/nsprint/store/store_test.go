package store_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

func startRedis(t *testing.T, extra ...string) string {
	t.Helper()
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skipf("redis-server unavailable; run this integration control on a Redis bench: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log, err := os.Create(filepath.Join(dir, "redis.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	port := strings.TrimPrefix(addr, "127.0.0.1:")
	args := append([]string{"--bind", "127.0.0.1", "--port", port,
		"--save", "", "--appendonly", "no", "--dir", dir}, extra...)
	cmd := exec.Command("redis-server", args...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := client.Ping(ctx).Err()
		cancel()
		// NOAUTH is a live server whose default user is off (the fleet shape).
		if err == nil || strings.Contains(err.Error(), "NOAUTH") {
			return addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("throwaway redis did not start: %v", err)
		}
		// Wait only for the next readiness probe, never as the assertion.
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFunctionLibraryLoadsFromFiles(t *testing.T) {
	addr := startRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx := context.Background()
	source, err := fn.Source()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(source, "#!lua name=nova_sprint\n") ||
		!strings.Contains(source, "redis.register_function('ns_ping'") ||
		!strings.Contains(source, "redis.register_function('ns_health'") {
		t.Fatalf("library did not concatenate embedded verb files: %q", source)
	}
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	result, err := client.FCall(ctx, "ns_ping", []string{}).Text()
	if err != nil || result != "PONG" {
		t.Fatalf("loaded ns_ping = %q, %v; want PONG", result, err)
	}
	health, err := client.FCall(ctx, "ns_health", []string{"a"}, "b").Int()
	if err != nil || health != 2 {
		t.Fatalf("loaded ns_health = %d, %v; want 2", health, err)
	}
	// An updated binary may load the same library again without a gap.
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("reload: %v", err)
	}
}

type countedConn struct {
	net.Conn
	writes      *atomic.Int64
	reads       *atomic.Int64
	interleaved *atomic.Int64
}

func (c countedConn) Write(p []byte) (int, error) {
	if c.reads.Load() != 0 {
		c.interleaved.Add(1)
	}
	c.writes.Add(1)
	return c.Conn.Write(p)
}

func (c countedConn) Read(p []byte) (int, error) {
	c.reads.Add(1)
	return c.Conn.Read(p)
}

func TestPipelineThousandReadsOneRoundTrip(t *testing.T) {
	addr := startRedis(t)
	var writes atomic.Int64
	var readCalls atomic.Int64
	var interleaved atomic.Int64
	client := redis.NewClient(&redis.Options{
		Addr: addr, PoolSize: 1,
		Dialer: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return countedConn{Conn: conn, writes: &writes, reads: &readCalls, interleaved: &interleaved}, nil
		},
	})
	defer client.Close()
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	seed := client.Pipeline()
	reads := make([]store.HashRead, 1000)
	for i := range reads {
		key := fmt.Sprintf("s:control:task:%d", i)
		seed.HSet(ctx, key, "state", "open", "owner", "stella")
		reads[i] = store.HashRead{Key: key, Fields: []string{"state", "owner"}}
	}
	if _, err := seed.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	writes.Store(0)
	readCalls.Store(0)
	interleaved.Store(0)
	got, err := store.New(client).PipelineHMGet(ctx, reads)
	if err != nil {
		t.Fatal(err)
	}
	if writes.Load() == 0 || readCalls.Load() == 0 || interleaved.Load() != 0 {
		t.Fatalf("1000 HMGETs used %d writes, %d reads, %d writes after a reply; want one pipelined exchange", writes.Load(), readCalls.Load(), interleaved.Load())
	}
	if len(got) != len(reads) {
		t.Fatalf("got %d replies; want 1000", len(got))
	}
	for i, values := range got {
		if len(values) != 2 || values[0] != "open" || values[1] != "stella" {
			t.Fatalf("reply %d = %v", i, values)
		}
	}
}

// TestOpenAuthenticatesFromEnv is the fleet shape (users.acl on space:6380):
// the default user is off, so an unauthenticated Open fails NOAUTH, and every
// nova-sprint verb failed that way on 2026-09-23 (adoption receipt on #3009).
// The password comes from the environment nova-secrets exec leaves it in,
// never from a flag.
func TestOpenAuthenticatesFromEnv(t *testing.T) {
	addr := startRedis(t, "--user", "default", "off", "--user", "bench", "on", ">bench-secret", "~*", "&*", "+@all")
	ctx := context.Background()

	// An inherited NOVA_SPRINT_REDIS_PASSWORD_ENV would redirect the default path
	// below to another seat's variable; clear it so the test is deterministic.
	t.Setenv(store.PasswordEnvEnv, "")
	t.Setenv(store.UserEnv, "")
	t.Setenv(store.DefaultPasswordEnv, "bench-secret")
	if _, err := store.Open(ctx, addr); err == nil || !strings.Contains(err.Error(), "NOAUTH") {
		t.Fatalf("Open without %s = %v; want NOAUTH (the password alone never picks a user)", store.UserEnv, err)
	}

	t.Setenv(store.UserEnv, "bench")
	st, err := store.Open(ctx, addr)
	if err != nil {
		t.Fatalf("Open as bench with %s: %v", store.DefaultPasswordEnv, err)
	}
	if err := st.Client().Set(ctx, "auth:probe", "1", 0).Err(); err != nil {
		t.Fatalf("authenticated write: %v", err)
	}
	_ = st.Close()

	t.Setenv(store.PasswordEnvEnv, "NOVA_REDIS_OTHER_SEAT")
	t.Setenv("NOVA_REDIS_OTHER_SEAT", "")
	if _, err := store.Open(ctx, addr); err == nil || !strings.Contains(err.Error(), "NOVA_REDIS_OTHER_SEAT is empty") {
		t.Fatalf("Open with an empty named password variable = %v; want a refusal naming it", err)
	}
	t.Setenv("NOVA_REDIS_OTHER_SEAT", "wrong")
	if _, err := store.Open(ctx, addr); err == nil || !strings.Contains(err.Error(), "WRONGPASS") {
		t.Fatalf("Open with the wrong password = %v; want WRONGPASS", err)
	}
}
