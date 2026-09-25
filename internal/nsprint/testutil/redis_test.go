package testutil

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestStartLostBindIsNotReady (#4027): a server started on a port another
// redis-server already holds must not count as ready because the holder
// answers its PING; startOnce reports the lost bind so Start takes a new port.
func TestStartLostBindIsNotReady(t *testing.T) {
	bin := Program(t)
	held := Start(t)
	_, port, err := net.SplitHostPort(held)
	if err != nil {
		t.Fatal(err)
	}
	if addr, lost := startOnce(t, bin, port, nil); !lost {
		t.Fatalf("startOnce on %s (held by another redis-server) = ready; want the lost bind reported", addr)
	}
	// The same under NOAUTH: the holder's NOAUTH is not this server's log.
	authed := Start(t, "--requirepass", "x")
	_, port, _ = net.SplitHostPort(authed)
	if addr, lost := startOnce(t, bin, port, []string{"--requirepass", "x"}); !lost {
		t.Fatalf("startOnce on %s (held, NOAUTH) = ready; want the lost bind reported", addr)
	}

	// Start's own server is the one that answers.
	addr := Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if info, err := c.Info(context.Background(), "server").Result(); err != nil || !strings.Contains(info, "process_id:") {
		t.Fatalf("INFO from Start's server: %v %q", err, info)
	}
}
