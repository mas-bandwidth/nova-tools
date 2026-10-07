package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalOnlyModeFrom(t *testing.T) {
	t.Parallel()
	assert.False(t, LocalOnlyModeFrom(nil))
	assert.False(t, LocalOnlyModeFrom(func(string) string { return "" }))
	assert.False(t, LocalOnlyModeFrom(func(string) string { return "0" }))
	assert.True(t, LocalOnlyModeFrom(func(string) string { return "1" }))
	assert.True(t, LocalOnlyModeFrom(func(string) string { return " 1 " }))
}

func TestLocalOnlyMode(t *testing.T) {
	t.Parallel()
	// Cannot directly test LocalOnlyMode without changing env; rely on LocalOnlyModeFrom tests
	_ = LocalOnlyMode
}

func TestCheckAddr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		addr     string
		localOnly bool
		want     string
	}{
		// Local: and mem: twins pass in both modes
		{"mem twin", "mem", false, ""},
		{"mem twin with path", "mem:/tmp/redis.sock", false, ""},
		{"mem twin with host", "mem:localhost:6379", false, ""},
		{"mem twin", "mem", true, ""},
		{"mem twin with path", "mem:/tmp/redis.sock", true, ""},
		{"mem twin with host", "mem:localhost:6379", true, ""},

		// Unix sockets pass in both modes
		{"unix socket", "/tmp/redis.sock", false, ""},
		{"unix socket", "/var/run/redis/redis.sock", true, ""},

		// localhost passes in both modes
		{"localhost no port", "localhost", false, ""},
		{"localhost with port", "localhost:6379", false, ""},
		{"localhost no port", "localhost", true, ""},
		{"localhost with port", "localhost:6379", true, ""},

		// Loopback addresses pass in both modes
		{"127.0.0.1", "127.0.0.1:6379", false, ""},
		{"::1", "[::1]:6379", false, ""},
		{"127.0.0.1", "127.0.0.1:6379", true, ""},
		{"::1", "[::1]:6379", true, ""},

		// Outside local-only mode: private and tailnet pass
		{"private 10.x", "10.0.0.1:6379", false, ""},
		{"private 172.16.x", "172.16.0.1:6379", false, ""},
		{"private 192.168.x", "192.168.1.1:6379", false, ""},
		{"tailnet", "100.64.0.1:6379", false, ""},
		{"tailnet upper", "100.127.255.255:6379", false, ""},

		// Outside local-only mode: names (non-IP) pass
		{"name", "redis.example.com:6379", false, ""},
		{"localhost", "localhost", false, ""},

		// Outside local-only mode: public addresses are refused
		{"public", "8.8.8.8:6379", false, "address is neither loopback nor private nor tailnet (100.64.0.0/10): 8.8.8.8:6379"},
		{"public 203.0.113.1", "203.0.113.1:6379", false, "address is neither loopback nor private nor tailnet (100.64.0.0/10): 203.0.113.1:6379"},

		// Local-only mode: non-loopback are refused
		{"private refused in local-only", "10.0.0.1:6379", true, "local-only mode allows only loopback; 10.0.0.1:6379 is not loopback"},
		{"tailnet refused in local-only", "100.64.0.1:6379", true, "local-only mode allows only loopback; 100.64.0.1:6379 is not loopback"},
		{"public refused in local-only", "8.8.8.8:6379", true, "local-only mode allows only loopback; 8.8.8.8:6379 is not loopback"},
		{"name refused in local-only", "redis.example.com:6379", true, "local-only mode allows only loopback; redis.example.com:6379 is not loopback: what a name would dial is unknown"},

		// Invalid formats
		{"no port", "127.0.0.1", false, "address wants host:port, found 127.0.0.1"},
		{"no port in local-only", "127.0.0.1", true, "address wants host:port, found 127.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := CheckAddr(tt.addr, tt.localOnly)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAddrOK(t *testing.T) {
	t.Parallel()
	// AddrOK depends on env; test the non-local-only path via mock
	got := CheckAddr("127.0.0.1:6379", false)
	assert.Equal(t, "", got)
}

func TestLocalOnlyModeRefusesEveryNonLoopbackAddressAndNeedsNoTailnet(t *testing.T) {
	t.Parallel()
	// When local-only mode is enabled via env, AddrOK should refuse non-loopback
	os.Setenv("NOVA_SPRINT_LOCAL", "1")
	defer os.Unsetenv("NOVA_SPRINT_LOCAL")

	// Loopback addresses pass in local-only mode
	assert.Equal(t, "", AddrOK("127.0.0.1:6379"))
	assert.Equal(t, "", AddrOK("localhost:6379"))
	assert.Equal(t, "", AddrOK("127.0.0.1"))

	// Non-loopback addresses are refused with the mode name
	require.Contains(t, AddrOK("100.64.0.1:6379"), "local-only mode")
	require.Contains(t, AddrOK("10.0.0.1:6379"), "local-only mode")
	require.Contains(t, AddrOK("8.8.8.8:6379"), "local-only mode")
	require.Contains(t, AddrOK("redis.example.com:6379"), "local-only mode")

	// mem: twins and unix sockets pass (no network needed)
	assert.Equal(t, "", AddrOK("mem"))
	assert.Equal(t, "", AddrOK("mem:/tmp/redis.sock"))
	assert.Equal(t, "", AddrOK("/tmp/redis.sock"))
}
