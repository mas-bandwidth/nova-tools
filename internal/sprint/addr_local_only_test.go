package sprint

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

// addr4 is an address:port from its octets: the cases below are built, not
// spelled, because nothing here is dialled and the ci net rule reads a spelled
// host:port as a host a test could reach (internal/bus/addr_test.go).
func addr4(a, b, c, d byte) string {
	return net.JoinHostPort(net.IPv4(a, b, c, d).String(), strconv.Itoa(7395))
}

// TestLocalOnlyModeRefusesEveryNonLoopbackAddressAndNeedsNoTailnet pins the
// address rule (docs/SPEC-SPRINT.md, section 14, The server) on both sides of a
// socket: in local-only mode a tailnet address is refused naming the mode and a
// loopback one is accepted, so no tailnet is needed; outside the mode both are
// accepted and a public address is refused. The listen side is the same one
// rule (ListenRefused) and the mode's refusal is one sentence
// (LocalOnlyRefusal). It steps the pure functions, so no socket opens and no
// environment is touched.
func TestLocalOnlyModeRefusesEveryNonLoopbackAddressAndNeedsNoTailnet(t *testing.T) {
	t.Parallel()
	tailnet, private, public := addr4(100, 64, 0, 1), addr4(10, 0, 0, 1), addr4(8, 8, 8, 8)
	t.Run("local-only mode takes loopback and refuses the rest naming the mode", func(t *testing.T) {
		t.Parallel()
		rows := []struct {
			name string
			addr string
			want string
		}{
			{"loopback is accepted", "127.0.0.1:7395", ""},
			{"a tailnet address is refused", tailnet, "local-only mode allows only loopback; " + tailnet + " is not loopback"},
			{"a private address is refused", private, "local-only mode allows only loopback; " + private + " is not loopback"},
			{"a public address is refused", public, "local-only mode allows only loopback; " + public + " is not loopback"},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				t.Parallel()
				got := CheckAddr(row.addr, true)
				assert.Equal(t, row.want, got)
				if row.want != "" {
					assert.Contains(t, got, "local-only mode")
				}
			})
		}
	})
	t.Run("outside the mode loopback and tailnet pass and a public address is refused", func(t *testing.T) {
		t.Parallel()
		rows := []struct {
			name string
			addr string
			want string
		}{
			{"loopback is accepted", "127.0.0.1:7395", ""},
			{"a tailnet address is accepted", tailnet, ""},
			{"a public address is refused", public, "address is neither loopback nor private nor tailnet (100.64.0.0/10): " + public},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, row.want, CheckAddr(row.addr, false))
			})
		}
	})
	t.Run("the listen rule is the same one rule: loopback and the tailnet bind, a public address does not", func(t *testing.T) {
		t.Parallel()
		rows := []struct {
			name string
			ip   net.IP
			want string
		}{
			{"loopback binds", net.IPv4(127, 0, 0, 1), ""},
			{"the tailnet binds", net.IPv4(100, 64, 1, 2), ""},
			{"a private address binds", net.IPv4(10, 0, 0, 2), ""},
			{"a public address is refused", net.IPv4(8, 8, 8, 8), "a public address"},
			{"every network is refused", net.IPv4zero, "every network"},
			{"a link-local address is refused", net.IPv4(169, 254, 1, 1), "a link-local address"},
			{"a name is refused", nil, "never a name"},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				t.Parallel()
				got := ListenRefused(row.ip)
				if row.want == "" {
					assert.Equal(t, "", got)
					return
				}
				assert.Contains(t, got, row.want)
			})
		}
	})
	t.Run("local-only mode refuses every non-loopback bind naming the mode", func(t *testing.T) {
		t.Parallel()
		rows := []struct {
			name string
			ip   net.IP
			addr string
			want string
		}{
			{"loopback binds in the mode", net.IPv4(127, 0, 0, 1), "127.0.0.1:7395", ""},
			{"localhost binds in the mode (it is loopback)", nil, "localhost:7395", ""},
			{"a tailnet bind is refused naming the mode", net.IPv4(100, 64, 0, 1), tailnet,
				"local-only mode allows only loopback; " + tailnet + " is not loopback"},
			{"a private bind is refused naming the mode", net.IPv4(10, 0, 0, 1), private,
				"local-only mode allows only loopback; " + private + " is not loopback"},
			{"a public bind is refused naming the mode", net.IPv4(8, 8, 8, 8), public,
				"local-only mode allows only loopback; " + public + " is not loopback"},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, row.want, LocalOnlyRefusal(row.ip, row.addr))
			})
		}
	})
}
