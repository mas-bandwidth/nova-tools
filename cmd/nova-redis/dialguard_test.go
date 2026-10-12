package main

import (
	"os"
	"path/filepath"
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
var dialTraps = map[string]bool{"newHarness": true, "pipeStore": true}

// TestUnitTierDialsNoSocketReadsWhatItRefuses: the scan names a dialer the
// unit tier holds and passes over the same line behind the functional tag, a
// miniredis inside a trap, and a name that only looks like a dialer.
func TestUnitTierDialsNoSocketReadsWhatItRefuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, src string
		want      []string
	}{
		{"a dial in the unit tier", "package p\nimport \"net\"\nfunc f() { net.Dial(\"tcp\", \"x\") }\n", []string{"a_test.go:3: net.Dial"}},
		{"a listener in the unit tier", "package p\nimport \"net\"\nfunc f() { net.Listen(\"tcp\", \"x\") }\n", []string{"a_test.go:3: net.Listen"}},
		{"an httptest server", "package p\nimport \"net/http/httptest\"\nfunc f() { httptest.NewServer(nil) }\n", []string{"a_test.go:3: httptest.NewServer"}},
		{"a store opener", "package p\nimport \"github.com/mas-bandwidth/nova-tools/pkg/redisconn\"\nfunc f() { redisconn.Open(nil, redisconn.Options{}, nil) }\n", []string{"a_test.go:3: redisconn.Open"}},
		{"a renamed import", "package p\nimport n \"net\"\nvar d n.Dialer\n", []string{"a_test.go:3: n.Dialer"}},
		{"a miniredis outside a trap", "package p\nimport \"github.com/alicebob/miniredis/v2\"\nfunc f() { miniredis.RunT(nil) }\n", []string{"a_test.go:3: miniredis.RunT"}},
		{"a miniredis inside a trap", "package p\nimport \"github.com/alicebob/miniredis/v2\"\nfunc pipeStore() { miniredis.RunT(nil) }\n", nil},
		{"the functional tier", "//go:build functional\n\npackage p\nimport \"net\"\nfunc f() { net.Dial(\"tcp\", \"x\") }\n", nil},
		{"a name that is not a dialer", "package p\nimport \"net\"\nfunc f() { net.SplitHostPort(\"x:1\") }\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "a_test.go"), []byte(c.src), 0o600))
			found, err := testkit.UnitTierDials(dir, map[string]bool{"pipeStore": true})
			require.NoError(t, err)
			assert.Equal(t, c.want, found)
		})
	}
}
