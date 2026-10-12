//go:build functional && linux

package sandbox

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The net half of the wall's own tests is functional, not unit, because it opens a
// real loopback listener (unit-sockets): Landlock denies connect(2) before a SYN is
// sent, so the refusal is the wall and not a closed port, and the control is a
// connect the parent's listener actually answers. The unit tier owns no sockets
// (docs/STANDARD.md rule 2), so this lives behind //go:build functional.

const wallPortEnv = "NOVA_TEST_SANDBOX_WALL_PORT"
const wallNetEnv = "NOVA_TEST_SANDBOX_WALL_NETDENY"

// Rule 7 on this platform at ABI >= 4: with NetDeny, a TCP connect to 127.0.0.1 on a
// port the parent listens on is refused, and the parent's listener sees no connection.
// Landlock denies connect(2) before a SYN is sent, so the refusal is the wall and not a
// closed port; the control (the same connect without NetDeny) proves the listener would
// have answered.
func TestWallNetDenyRefusesATCPConnect(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		port := os.Getenv(wallPortEnv)
		in := standardInput([]string{"bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/" + port})
		if os.Getenv(wallNetEnv) == "1" {
			in.NetDeny = true
		}
		os.Exit(runWall(in))
	}
	// bash's /dev/tcp is how a connect is made without a second tool, and abi 4 is where
	// Landlock denies TCP connect.
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("no bash on this machine; /dev/tcp is how a connect is made without a second tool: %v", err)
	}
	abi, ok := landlockABI()
	if !ok {
		t.Skip("this kernel has no landlock; the wall cannot be built here")
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root (rule 2)")
	}
	if abi < 4 {
		t.Skipf("this kernel reports landlock abi %d; --net-deny needs abi 4 for TCP connect", abi)
	}

	base := t.TempDir()
	mkWallDirs(t, base)

	// A listener this test owns, so a refused connect is the wall and not an empty port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() {
		// ignored: a closed listener is the teardown; the accept loop's return is the close
		_ = ln.Close()
	}()
	var mu sync.Mutex
	accepted := 0
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// ignored: a connection the listener accepted only to count; its close cannot fail meaningfully
			_ = c.Close()
			mu.Lock()
			accepted++
			mu.Unlock()
		}
	}()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return accepted
	}

	// THE CONTROL: without NetDeny the same connect succeeds, so the denial below is the
	// wall and not a broken listener or a closed port.
	code, out := walledNetRun(t, base, port, false)
	require.Equal(t, 0, code, "the control connect failed (exit %d), so a denial would prove nothing: %s", code, out)
	require.Eventually(t, func() bool { return count() == 1 }, 5*time.Second, 25*time.Millisecond,
		"the control connect was never accepted by the parent's listener")

	// Under NetDeny the same connect is refused, and the parent's listener saw nothing:
	// the denied child has exited and only the control connection was ever accepted.
	code, out = walledNetRun(t, base, port, true)
	require.NotEqual(t, 0, code, "the walled command CONNECTED under --net-deny: %s", out)
	assert.Equal(t, 1, count(), "the parent's listener accepted a connection from the --net-deny walled child")
}

// walledNetRun re-runs this test binary as the walled child for the net test, with the
// port and the NetDeny flag in the environment, and returns the child's exit code and
// combined output. The caller has already skipped when this machine cannot build the wall.
func walledNetRun(t *testing.T, base, port string, netDeny bool) (int, string) {
	t.Helper()
	netVal := "0"
	if netDeny {
		netVal = "1"
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		wallChildEnv+"=1", wallWorkEnv+"="+base, wallPortEnv+"="+port, wallNetEnv+"="+netVal)
	out, err := cmd.CombinedOutput()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	} else {
		require.NoError(t, err, "the walled child did not run at all: %s", out)
	}
	return code, string(out)
}
