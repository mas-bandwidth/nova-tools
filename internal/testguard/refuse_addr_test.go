package testguard

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refuseAddrMsg calls RefuseAddr and returns the panic text, failing when the
// armed guard did not panic at all.
func refuseAddrMsg(t *testing.T, g *Guard, network, addr string) (msg string) {
	t.Helper()
	defer func() {
		r := recover()
		require.NotNil(t, r, "RefuseAddr(%q, %q) must panic under an armed guard", network, addr)
		msg, _ = r.(string)
	}()
	g.RefuseAddr(network, addr)
	return ""
}

// TestRefuseAddrArmedNamesAnOffLoopbackAddress pins that an armed guard refuses
// a store the fleet might hold, and that the panic carries the address, the
// variable that arms the guard, and the remedy a test acts on (nova-tools#4193).
func TestRefuseAddrArmedNamesAnOffLoopbackAddress(t *testing.T) {
	t.Parallel()
	g := NewGuard(true)
	const addr = "store.invalid:6379"
	msg := refuseAddrMsg(t, g, "tcp", addr)
	for _, want := range []string{EnvNoHost, addr, "loopback", "127.0.0.1", "t.TempDir()"} {
		assert.Contains(t, msg, want, "the refusal must carry %q; got %q", want, msg)
	}
}

// TestRefuseAddrArmedPassesLoopback pins that every loopback spelling a test
// legitimately uses is a store the guard lets through: the loopback range, the
// IPv6 loopback, and the localhost name, on any port including the OS-assigned 0.
func TestRefuseAddrArmedPassesLoopback(t *testing.T) {
	t.Parallel()
	g := NewGuard(true)
	for _, addr := range []string{"127.0.0.1:0", "127.0.0.1:6379", "[::1]:6379", "localhost:6379"} {
		assert.NotPanics(t, func() { g.RefuseAddr("tcp", addr) },
			"RefuseAddr(%q) must pass on the loopback", addr)
	}
}

// TestRefuseAddrArmedSplitsUnixByTempRoot pins the unix half: a socket under a
// temp root is the test's own throwaway store and passes, one outside every temp
// root (a named /var/run path here) may be the fleet's and is refused.
func TestRefuseAddrArmedSplitsUnixByTempRoot(t *testing.T) {
	t.Parallel()
	g := NewGuard(true)
	assert.NotPanics(t, func() { g.RefuseAddr("unix", filepath.Join(t.TempDir(), "redis.sock")) },
		"a unix socket under a temp root is a test's own store")
	assert.Panics(t, func() { g.RefuseAddr("unix", "/var/run/redis.sock") },
		"a unix socket outside every temp root can be the fleet's")
}

// TestRefuseAddrUnarmedPassesEverything pins the production cost: an unarmed
// guard is a load and a return, so a real store's address, loopback or not, is
// never refused.
func TestRefuseAddrUnarmedPassesEverything(t *testing.T) {
	t.Parallel()
	g := NewGuard(false)
	for _, c := range []struct{ network, addr string }{
		{"tcp", "store.invalid:6379"},
		{"unix", "/var/run/redis.sock"},
	} {
		assert.NotPanics(t, func() { g.RefuseAddr(c.network, c.addr) },
			"RefuseAddr(%q, %q) must pass with the guard unset", c.network, c.addr)
	}
}

// TestRefuseAddrDefaultIsTheProcessGuard pins that Default answers the same
// guard the package-level RefuseAddr and RefuseHosts read, so a store seam that
// holds it and a shell under NOVA_TEST_NO_HOST are armed together.
func TestRefuseAddrDefaultIsTheProcessGuard(t *testing.T) {
	t.Parallel()
	require.Same(t, defaultGuard, Default(), "Default returns the process-wide guard the package seams read")
}
