// Unit coverage for Listen, the production answer to a caller that wants a
// proxy on an address (delayproxy.go). The per-function coverage table showed it
// at 0.0%: no unit test reached it. Listen is net.Listen followed by Serve, and
// no seam wraps net.Listen, so its main path needs one loopback listener -- the
// package's own unit tests already open those; no sleep, no real time, no
// external network, no subprocess, no Redis or Postgres. Its two refusals are
// reached without a client: an address net.Listen refuses, and a target Serve
// refuses after the listener was opened, which is the branch that closes it.
package delayproxy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDelayproxyCoverListen pins every branch of Listen: the main path, where a
// loopback address is listened on and served; the refusal when the address is
// not a host:port, which net.Listen refuses before any socket is opened; and the
// refusal when the target is not a host:port, which Serve refuses after the
// listener was opened, so Listen closes it and returns Serve's error.
func TestDelayproxyCoverListen(t *testing.T) {
	t.Parallel()

	const target = "127.0.0.1:7000"
	rows := []struct {
		name    string
		addr    string
		target  string
		wantErr string
	}{
		{name: "a loopback address is listened on and served", addr: "127.0.0.1:0", target: target},
		{name: "an address that is not a host:port is refused", addr: "not an address", target: target, wantErr: "listening on"},
		{name: "a target that is not a host:port is refused after the listener opens", addr: "127.0.0.1:0", target: "not an address", wantErr: "target"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			p, err := Listen(row.addr, row.target, delay, Options{})
			if row.wantErr != "" {
				require.Error(t, err, "Listen accepted %q -> %q", row.addr, row.target)
				assert.Nil(t, p, "a refusal returns no proxy")
				assert.Contains(t, err.Error(), row.wantErr, "the refusal names what refused it")
				return
			}
			require.NoError(t, err, "Listen refused %q -> %q", row.addr, row.target)
			require.NotNil(t, p, "the main path returns a proxy")
			t.Cleanup(p.Stop)
			assert.True(t, strings.HasPrefix(p.Addr(), "127.0.0.1:"), "Addr = %q; want the loopback address it was given", p.Addr())
			assert.Zero(t, p.Writes(), "a new proxy counts %d writes; want none", p.Writes())
			assert.Equal(t, 1, int(p.live.Load()), "live = %d; want the accept loop alone", p.live.Load())
		})
	}
}
