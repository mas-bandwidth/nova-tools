package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLocalOnlyModeFromSelectsTheMode pins that local-only mode is the
// process setting NOVA_SPRINT_LOCAL=1, read through the caller's getenv so no
// test touches the environment (docs/SPEC-SPRINT.md, section 14, The server).
func TestLocalOnlyModeFromSelectsTheMode(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name  string
		value string
		want  bool
	}{
		{"unset is not the mode", "", false},
		{"1 is the mode", "1", true},
		{"0 is not the mode", "0", false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(string) string { return row.value }
			assert.Equal(t, row.want, LocalOnlyModeFrom(getenv))
		})
	}
	t.Run("a nil getenv is not the mode", func(t *testing.T) {
		t.Parallel()
		assert.False(t, LocalOnlyModeFrom(nil))
	})
}

// TestCheckAddrHoldsTheOneAddressRule pins CheckAddr (internal/sprint/addr.go):
// loopback, a private range or the tailnet's address passes outside the mode,
// loopback alone passes inside it, and the refusals name the rule or the mode.
// No socket opens and no environment is touched.
func TestCheckAddrHoldsTheOneAddressRule(t *testing.T) {
	t.Parallel()
	private, tailnet, tailHigh := addr4(10, 0, 0, 1), addr4(100, 64, 0, 1), addr4(100, 127, 255, 255)
	lan, public, every := addr4(192, 168, 1, 1), addr4(8, 8, 8, 8), addr4(0, 0, 0, 0)
	rows := []struct {
		name      string
		addr      string
		localOnly bool
		want      string
	}{
		{"loopback passes", "127.0.0.1:7395", false, ""},
		{"loopback passes in local-only mode", "127.0.0.1:7395", true, ""},
		{"ipv6 loopback passes", "[::1]:7395", false, ""},
		{"a private address passes", private, false, ""},
		{"a tailnet address passes", tailHigh, false, ""},
		{"a tailnet address is refused in local-only mode", tailnet, true, "local-only mode allows only loopback; " + tailnet + " is not loopback"},
		{"a private address is refused in local-only mode", lan, true, "local-only mode allows only loopback; " + lan + " is not loopback"},
		{"a public address is refused", public, false, "address is neither loopback nor private nor tailnet (100.64.0.0/10): " + public},
		{"a public address is refused in local-only mode", public, true, "local-only mode allows only loopback; " + public + " is not loopback"},
		{"every network is refused", every, false, "address is neither loopback nor private nor tailnet (100.64.0.0/10): " + every},
		{"a name passes outside the mode", "store.example:6379", false, ""},
		{"a name is refused in local-only mode", "store.example:6379", true, "local-only mode allows only loopback; store.example:6379 is not loopback: what a name would dial is unknown"},
		{"localhost passes", "localhost:6379", false, ""},
		{"localhost passes in local-only mode", "localhost:6379", true, ""},
		{"a socket passes", "/tmp/redis.sock", false, ""},
		{"a twin passes", "mem:bench", true, ""},
		{"an address with no port is refused", "127.0.0.1", false, "address wants host:port, found 127.0.0.1"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, row.want, CheckAddr(row.addr, row.localOnly))
		})
	}
}
