//go:build darwin || linux

package yield

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// behindHelperEnv names the role this test binary plays when it is the helper
// process below: "child" steps behind CI and starts a grandchild through sh,
// "grand" only reports.
const behindHelperEnv = "NOVA_YIELD_BEHIND_HELPER"

// behindState is this process's own reading of the class Behind puts it in:
// darwin's background state (1 when set), or Linux's cgroup and its cpu.idle.
func behindState() string {
	if runtime.GOOS == "darwin" {
		v, err := syscall.Getpriority(4, 0) // PRIO_DARWIN_PROCESS: 1 when the background state is set
		if err != nil {
			return "darwin_bg=err:" + err.Error()
		}
		return "darwin_bg=" + strconv.Itoa(v)
	}
	cg, _ := os.ReadFile("/proc/self/cgroup")
	p := unifiedPath(string(cg))
	idle, _ := os.ReadFile("/sys/fs/cgroup" + p + "/cpu.idle")
	return "cgroup=" + p + " idle=" + strings.TrimSpace(string(idle))
}

// TestBehindHelperProcess is not a test of its own: it is the child the test
// below starts (this binary, run with behindHelperEnv set), and returns at once
// otherwise.
func TestBehindHelperProcess(t *testing.T) {
	t.Parallel()
	switch os.Getenv(behindHelperEnv) {
	case "child":
		if note := Behind("yieldtest"); note != "" {
			os.Stdout.WriteString("note=" + note + "\n")
			os.Exit(0)
		}
		os.Stdout.WriteString("child " + behindState() + "\n")
		sub := exec.Command("sh", "-c", `"$0" -test.run='^TestBehindHelperProcess$'; true`, os.Args[0])
		sub.Env = append(os.Environ(), behindHelperEnv+"=grand")
		out, _ := sub.Output()
		os.Stdout.Write(out)
		os.Exit(0)
	case "grand":
		os.Stdout.WriteString("grand " + behindState() + "\n")
		os.Exit(0)
	}
}

// TestBehindReachesTheChildAndItsDescendants: a process that steps behind CI is
// in the class itself, and a process it starts through sh is too (darwin: the
// background state; Linux: the idle scope, where a user manager is there). The
// step runs in a child of this test, never in the test binary, which is a CI leg.
// Where Linux has no user manager the step's reason is logged and nothing is
// asserted of the class.
func TestBehindReachesTheChildAndItsDescendants(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "linux" {
		if note := BehindNote(); note != "" {
			t.Skipf("no idle scope from here: %s", note)
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestBehindHelperProcess$")
	cmd.Env = append(os.Environ(), behindHelperEnv+"=child")
	out, err := cmd.Output()
	require.NoError(t, err, "helper: %s", out)
	got := string(out)
	require.NotContains(t, got, "note=", "the step was refused: %s", got)
	for _, who := range []string{"child ", "grand "} {
		line := ""
		for _, l := range strings.Split(got, "\n") {
			if strings.HasPrefix(l, who) {
				line = l
			}
		}
		require.NotEmpty(t, line, "no %sline in %q", who, got)
		if runtime.GOOS == "darwin" {
			assert.Contains(t, line, "darwin_bg=1", "%s is not in the background state", who)
		} else {
			assert.Contains(t, line, "/nova-card-yieldtest-", "%s is not in the card's scope", who)
			assert.Contains(t, line, " idle=1", "%s's scope is not idle", who)
		}
	}
}
