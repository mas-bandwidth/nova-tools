package testguard

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
		assert.Panics(t, func() {
			armed.RefuseAddr("tcp", "example.com:6379")
		})
	})

	t.Run("UnixOutsideTemp", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() {
			armed.RefuseAddr("unix", "/var/run/redis.sock")
		})
	})
}
