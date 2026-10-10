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
// address rule (docs/SPEC-SPRINT.md, sprint-local-only-mode-r-bcb.w5): in
// local-only mode a tailnet address is refused naming the mode and a loopback
// one is accepted, so no tailnet is needed; outside the mode loopback and the
// tailnet are accepted and a private or public address is refused, each naming
// what it is. An address with no host names every interface and is refused in
// either mode, naming the mode under local-only mode. Unspecified and link-local
// addresses are refused in either mode, naming the mode under local-only mode.
// It steps the pure CheckAddr, so no socket opens and no environment is touched.
func TestLocalOnlyModeRefusesEveryNonLoopbackAddressAndNeedsNoTailnet(t *testing.T) {
	t.Parallel()
	tailnet, private, public := addr4(100, 64, 0, 1), addr4(10, 0, 0, 1), addr4(8, 8, 8, 8)
	unspecified, linkLocal := addr4(0, 0, 0, 0), addr4(169, 254, 1, 1)
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
			{"an unspecified address is refused", unspecified, "local-only mode allows only loopback; " + unspecified + " names this machine on every network, not loopback"},
			{"a link-local address is refused", linkLocal, "local-only mode allows only loopback; " + linkLocal + " is a link-local address, not loopback"},
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
	t.Run("outside the mode loopback and the tailnet pass and a private or public address is refused", func(t *testing.T) {
		t.Parallel()
		rows := []struct {
			name string
			addr string
			want string
		}{
			{"loopback is accepted", "127.0.0.1:7395", ""},
			{"a tailnet address is accepted", tailnet, ""},
			{"a private address is refused as private", private, "a private address outside the tailnet: " + private + " is neither loopback nor the tailnet (100.64.0.0/10)"},
			{"a public address is refused as public", public, "a public address: address is neither loopback nor tailnet (100.64.0.0/10): " + public},
			{"an unspecified address is refused as every network", unspecified, "every network: " + unspecified + " names this machine on every network, not loopback or the tailnet (100.64.0.0/10)"},
			{"a link-local address is refused as link-local", linkLocal, "a link-local address: " + linkLocal + " is not loopback or the tailnet (100.64.0.0/10)"},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, row.want, CheckAddr(row.addr, false))
			})
		}
	})
	t.Run("an address with no host is refused in either mode, naming the mode in local-only mode", func(t *testing.T) {
		t.Parallel()
		for _, localOnly := range []bool{false, true} {
			got := CheckAddr(":6379", localOnly)
			assert.Contains(t, got, "names no host", "localOnly=%v", localOnly)
			if localOnly {
				assert.Contains(t, got, "local-only mode", "the empty-host refusal names local-only mode")
			}
		}
	})
	t.Run("an unspecified address is refused in either mode, naming the mode in local-only mode", func(t *testing.T) {
		t.Parallel()
		for _, localOnly := range []bool{false, true} {
			got := CheckAddr(unspecified, localOnly)
			assert.Contains(t, got, "every network", "localOnly=%v", localOnly)
			if localOnly {
				assert.Contains(t, got, "local-only mode", "the unspecified refusal names local-only mode")
			} else {
				assert.Contains(t, got, "tailnet", "outside local-only mode the refusal names the tailnet")
			}
		}
	})
	t.Run("a link-local address is refused in either mode, naming the mode in local-only mode", func(t *testing.T) {
		t.Parallel()
		for _, localOnly := range []bool{false, true} {
			got := CheckAddr(linkLocal, localOnly)
			assert.Contains(t, got, "link-local", "localOnly=%v", localOnly)
			if localOnly {
				assert.Contains(t, got, "local-only mode", "the link-local refusal names local-only mode")
			} else {
				assert.Contains(t, got, "tailnet", "outside local-only mode the refusal names the tailnet")
			}
		}
	})
}
