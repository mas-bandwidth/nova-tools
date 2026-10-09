package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const inheritedFDProbe = "NOVA_FUNCTIONAL_FD_PROBE"

// The functional runner is a subprocess of a coordinator's flock. Its Go
// children and their Redis grandchildren must not keep that lock after the
// coordinator has finished the gate.
func TestFunctionalRunClosesInheritedGateFDForChildren(t *testing.T) {
	t.Parallel()
	if os.Getenv(inheritedFDProbe) == "1" {
		flags, err := unix.FcntlInt(3, unix.F_GETFD, 0)
		require.NoError(t, err)
		require.Zero(t, flags&unix.FD_CLOEXEC, "the injected lock descriptor starts inheritable")
		var out, errb bytes.Buffer
		e := env{stdout: &out, stderr: &errb}
		r := &fakeCmdRunner{answer: func(cmdSpec) (string, int, error) {
			return "CI FUNCTIONAL OK packages=0 reason=probe\n", 0, nil
		}}
		require.Zero(t, functionalRun(e, r, nil), errb.String())
		flags, err = unix.FcntlInt(3, unix.F_GETFD, 0)
		require.NoError(t, err)
		require.NotZero(t, flags&unix.FD_CLOEXEC, "a child exec must close the coordinator's lock descriptor")
		return
	}

	gate, err := os.OpenFile(filepath.Join(t.TempDir(), "gate.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	defer gate.Close() // ignored: a test fixture's cleanup; the test's assertions are the report
	cmd := exec.Command(os.Args[0], "-test.run=^TestFunctionalRunClosesInheritedGateFDForChildren$")
	cmd.ExtraFiles = []*os.File{gate}
	cmd.Env = append(os.Environ(), inheritedFDProbe+"=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
}
