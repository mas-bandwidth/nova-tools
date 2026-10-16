//go:build functional && linux

package main

// The landlock wall's network denial, driven over a real loopback listener: its subject
// is a real connect refused by the kernel, so it is a functional test (docs/STANDARD.md
// section 8: unit tests own no sockets). It moved here from wall_linux_test.go, whose
// helpers it still uses.

import (
	"net"
	"os/exec"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rule 7: --net-deny is an ENFORCED denial on this platform or it is a refusal, and at
// abi 4 and up it is enforced for TCP.
func TestLandlockWallBlocksNetworkWhenNotAllowed(t *testing.T) {
	t.Parallel()

	needLandlock(t)
	j := newJob(t)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("skipped: no bash on this machine, and /dev/tcp is how a connect is made here without a second tool")
	}
	// A listener this test owns, so that a refused connect is the WALL and not an empty
	// port. The listener is outside the wall; the wrapped command is what is walled.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	dial := "exec 3<>/dev/tcp/127.0.0.1/" + port

	connect := func(extra ...string) (int, string) {
		args := append([]string{"--read", j.read, "--write", j.write}, extra...)
		args = append(args, "--", bash, "-c", dial)
		code, _, errOut := j.runTool(t, j.env(), args...)
		return code, errOut
	}
	// THE CONTROL: without --net-deny the same connect succeeds, so the denial below is
	// the wall and not a broken listener.
	if code, errOut := connect(); code != 0 {
		t.Skipf("skipped: the control connect failed (exit %d), so a denial would prove nothing: %s", code, errOut)
	}
	code, errOut := connect("--net-deny")
	require.NotEqual(t, 0, code, "the wrapped command CONNECTED under --net-deny: %s", errOut)
	assert.Contains(t, errOut, "net=denied", "the run did not announce net=denied: %q", errOut)
}
