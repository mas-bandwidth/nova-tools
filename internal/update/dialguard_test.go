package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
)

// TestUnitTierDialsNoSocket: no unit test of this package opens a TCP
// connection (docs/STANDARD.md section 8: unit tests own no sockets; the post
// step's wall denies TCP, loopback included). Real sockets are the functional
// tier (`//go:build functional`); a unit test reaches a store or a server over
// an in-memory transport (pipeStore, a transport that calls its handler) or a
// seam. This reads every test file the unit tier builds and names each use of
// what dials or listens: net's Dial and Listen family and net.Dialer,
// tls.Dial, an httptest server, http.Get and its kin, the store openers
// redisconn.Open and store.Open, testredis's listeners, and a miniredis
// started anywhere but the helpers whose cleanup fails a test that dialled
// the fake over TCP (dialTraps).
func TestUnitTierDialsNoSocket(t *testing.T) {
	t.Parallel()
	found, err := testkit.UnitTierDials(".", dialTraps)
	require.NoError(t, err)
	assert.Empty(t, found, "the unit tier dials or listens; move the test behind //go:build functional or give it an in-memory transport")
}

// dialTraps are this package's helpers allowed to start a miniredis: each
// fails its test at cleanup when the fake took a TCP connection.
var dialTraps = map[string]bool{"pipeStore": true}
