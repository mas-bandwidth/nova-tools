package testkit

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/stretchr/testify/assert"
)

// PipeStore is a miniredis fake reached in memory: the client's dialer hands
// it one end of a net.Pipe and the fake serves the other (ServeConn), so no
// TCP connection is made. miniredis binds a loopback listener when it starts;
// the cleanup fails the test when anything dialled it.
func PipeStore(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	var piped atomic.Int64
	client := redis.NewClient(&redis.Options{
		Addr:            "127.0.0.1:0", // never dialled (Dialer is the transport); an IP so nothing resolves it
		DisableIdentity: true,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			near, far := net.Pipe()
			piped.Add(1)
			mr.Server().ServeConn(far)
			return near, nil
		},
	})
	t.Cleanup(func() {
		// ignored: a best-effort close of the in-memory client; the test's own assertions are the check that matters, and cleanup has nothing to report it to
		_ = client.Close()
		assert.Equal(t, piped.Load(), int64(mr.TotalConnectionCount()), "the fake took a TCP connection beside its pipes")
	})
	return mr, client
}
