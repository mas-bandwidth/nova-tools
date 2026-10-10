package testguard

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// panicMessage runs f and returns the string it panicked with, failing the row
// when f returns instead. RefuseAddr panics with a string naming the network and
// the address (nova-tools#4193), so the rows below pin that message and not just
// the panic.
func panicMessage(t *testing.T, f func()) string {
	t.Helper()
	var said any
	func() {
		defer func() { said = recover() }()
		f()
	}()
	require.NotNil(t, said, "an armed guard must refuse the dial")
	msg, ok := said.(string)
	require.True(t, ok, "the refusal must be a string naming the address; got %v", said)
	return msg
}

func TestRefuseAddr(t *testing.T) {
	t.Parallel()

	unarmed := NewGuard(false)
	unarmed.RefuseAddr("tcp", "example.com:6379")
	unarmed.RefuseAddr("unix", "/var/run/redis.sock")

	armed := NewGuard(true)

	armed.RefuseAddr("tcp", "127.0.0.1:0")
	armed.RefuseAddr("tcp", "[::1]:6379")
	armed.RefuseAddr("tcp", "localhost:6379")

	tmp := t.TempDir()
	unixPath := filepath.Join(tmp, "redis.sock")
	armed.RefuseAddr("unix", unixPath)

	t.Run("OffLoopbackTCP", func(t *testing.T) {
		t.Parallel()
		msg := panicMessage(t, func() {
			armed.RefuseAddr("tcp", "example.com:6379")
		})
		assert.Contains(t, msg, "tcp", "the refusal must name the network; got %q", msg)
		assert.Contains(t, msg, "example.com:6379", "the refusal must name the address; got %q", msg)
	})

	t.Run("UnixOutsideTemp", func(t *testing.T) {
		t.Parallel()
		msg := panicMessage(t, func() {
			armed.RefuseAddr("unix", "/var/run/redis.sock")
		})
		assert.Contains(t, msg, "unix", "the refusal must name the network; got %q", msg)
		assert.Contains(t, msg, "/var/run/redis.sock", "the refusal must name the address; got %q", msg)
	})
}
