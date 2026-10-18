//go:build functional && linux

package sandbox

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wallPortEnv = "NOVA_TEST_SANDBOX_WALL_PORT"

// walledNetRun re-runs this test binary as the walled child for the net test, with the
// port in the environment, and returns the child's exit code and combined output. The
// caller has already skipped when this machine cannot build the wall or cannot enforce
// --net-deny, so the child is only ever run where the wall can refuse the connect.
func walledNetRun(t *testing.T, base, port string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), wallChildEnv+"=1", wallWorkEnv+"="+base, wallPortEnv+"="+port)
	out, err := cmd.CombinedOutput()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	} else {
		require.NoError(t, err, "the walled child did not run at all: %s", out)
	}
	return code, string(out)
}

// TestWallNetDenyRefusesATCPConnect pins rule 7's linux half: with --net-deny on a
// kernel at ABI >= 4, a TCP connect to 127.0.0.1 is denied with EACCES, and the denial
// is the wall and not a closed port -- the same connect made without the wall to the
// same closed port is refused with ECONNREFUSED, not EACCES. The connect is bash's
// /dev/tcp (no second tool), and the wall applies to a re-executed copy of the test
// binary, so the parent only asserts on the child's exit code and output. The unit tier
// owns no socket (docs/STANDARD.md rule 2), so there is no listener here; the closed
// port's ECONNREFUSED is the control that tells the wall's EACCES apart from a port that
// is simply closed.
func TestWallNetDenyRefusesATCPConnect(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		in := standardInput([]string{"bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/" + os.Getenv(wallPortEnv)})
		in.NetDeny = true
		os.Exit(runWall(in))
	}
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
	const port = "45678"

	// THE CONTROL: the same connect made without the wall is refused with ECONNREFUSED
	// ("Connection refused"), proving the port is closed -- so the walled child's
	// "Permission denied" below is the wall and not a closed port.
	ctl := exec.Command("bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/"+port)
	// ignored: the control's non-zero exit is the refused connect itself; the output text is what proves the port is closed
	ctlOut, _ := ctl.CombinedOutput()
	require.Contains(t, string(ctlOut), "Connection refused",
		"the control connect to the closed port %s did not say the port is closed (something listens there?): %s", port, ctlOut)

	// Under --net-deny the same connect is denied by the wall with EACCES, and the walled
	// command never connects.
	code, out := walledNetRun(t, base, port)
	require.NotEqual(t, 0, code, "the walled command CONNECTED under --net-deny: %s", out)
	assert.Contains(t, out, "Permission denied", "the denial was not EACCES: %s", out)
}
