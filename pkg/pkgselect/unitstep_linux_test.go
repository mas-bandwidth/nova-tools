package pkgselect

import (
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// TestWriteUnitShimGuardsFork observes the lock used by Go's fork, in an
// isolated invocation with no other tests taking that process's ForkLock.
// Removing the writer guard makes TryLock succeed during the write and fails
// deterministically; this does not assert the cause of a historical CI failure.
func TestWriteUnitShimGuardsFork(t *testing.T) {
	t.Parallel()
	const child = "NOVA_UNIT_SHIM_GUARD_CHILD"
	if os.Getenv(child) != "1" {
		bin, err := os.Executable()
		require.NoError(t, err)
		cmd, cancel := subproc.Command(t.Context(), subproc.Tool, bin, "-test.run=^TestWriteUnitShimGuardsFork$")
		defer cancel()
		cmd.Env = append(os.Environ(), child+"=1")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "isolated guard probe: %s", out)
		return
	}
	for _, tc := range []struct {
		name     string
		writeErr error
	}{
		{"written shim", nil},
		{"writer failure", os.ErrPermission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forkDuringWrite := false
			shim, err := writeUnitShim(t.TempDir(), func(path string, data []byte, perm os.FileMode) error {
				forkDuringWrite = syscall.ForkLock.TryLock()
				if forkDuringWrite {
					syscall.ForkLock.Unlock()
				}
				if tc.writeErr != nil {
					return tc.writeErr
				}
				return os.WriteFile(path, data, perm)
			})
			assert.False(t, forkDuringWrite, "Go's fork must not acquire its lock while the executable writer runs")
			released := syscall.ForkLock.TryLock()
			if released {
				syscall.ForkLock.Unlock()
			}
			require.True(t, released, "the writer must release the guard on success and failure")
			if tc.writeErr != nil {
				assert.ErrorIs(t, err, tc.writeErr)
				assert.Empty(t, shim)
				return
			}
			require.NoError(t, err)
			cmd, cancel := subproc.Command(t.Context(), subproc.Tool, shim)
			defer cancel()
			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, UnitShimExit, exitErr.ExitCode())
			assert.Equal(t, UnitShimMessage+"\n", string(out))
		})
	}
}
