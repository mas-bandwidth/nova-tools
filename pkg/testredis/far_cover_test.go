package testredis

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFarCoverFar reaches the exported wrapper Far. Its main path stands up a
// delayproxy on a loopback port and returns that address; its refusal is the
// target Loopback turns away before a listener is opened. No byte crosses the
// proxy, so no clock is asked and the assertion is on the address alone.
func TestFarCoverFar(t *testing.T) {
	t.Parallel()

	t.Run("main path returns a loopback address on a port the kernel chose", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name   string
			target string
			delay  time.Duration
		}{
			{"the test's delay", "127.0.0.1:1", farDelay},
			{"a zero delay is still a proxy", "127.0.0.1:1", 0},
			{"a named loopback target", "localhost:1", farDelay},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				addr := Far(t, tt.target, tt.delay)
				host, port, err := net.SplitHostPort(addr)
				require.NoError(t, err, "Far returned %q", addr)
				assert.Equal(t, "127.0.0.1", host)
				assert.NotEmpty(t, port)
				assert.NotEqual(t, "0", port)
				assert.NotEqual(t, tt.target, addr)
			})
		}
	})

	t.Run("refusal names the target that is not a store of this test's own", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name   string
			target string
			want   string
		}{
			{"a store off the machine", "example.com:80", "not one"},
			{"a target with no port", "127.0.0.1", "not one"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				r := provoke(t, func(tb testing.TB) { Far(tb, tt.target, farDelay) })
				assert.Contains(t, r.fatal, tt.want)
			})
		}
	})
}

// TestFarCoverFarLink reaches FarLink, the wrapper that hands the test the
// proxy beside the address. Its main path returns a proxy that has forwarded
// nothing; its refusal is the same target rule, reached one call later.
func TestFarCoverFarLink(t *testing.T) {
	t.Parallel()

	t.Run("main path returns the proxy whose ledger the test reads", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name   string
			target string
			delay  time.Duration
		}{
			{"the test's delay", "127.0.0.1:1", farDelay},
			{"a zero delay", "127.0.0.1:1", 0},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				p := FarLink(t, tt.target, tt.delay)
				require.NotNil(t, p)
				host, port, err := net.SplitHostPort(p.Addr())
				require.NoError(t, err, "FarLink's address is %q", p.Addr())
				assert.Equal(t, "127.0.0.1", host)
				assert.NotEmpty(t, port)
				assert.NotEqual(t, "0", port)
				assert.Zero(t, p.Writes(), "nothing was written through the proxy")
			})
		}
	})

	t.Run("refusal names the target that is not a store of this test's own", func(t *testing.T) {
		t.Parallel()
		r := provoke(t, func(tb testing.TB) { FarLink(tb, "example.com:80", farDelay) })
		assert.Contains(t, r.fatal, "not one")
	})
}
