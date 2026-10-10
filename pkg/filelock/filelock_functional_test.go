//go:build functional && (unix || windows)

package filelock_test

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helperCmd is this test binary run as a lock helper in the given mode on path under label.
func helperCmd(mode, path, label string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestFunctional_HelperProcess$")
	cmd.Env = append(os.Environ(),
		"FILELOCK_TEST_HELPER=1",
		"FILELOCK_HELPER_MODE="+mode,
		"FILELOCK_HELPER_PATH="+path,
		"FILELOCK_HELPER_LABEL="+label,
	)
	return cmd
}

// startHold runs a holder to its HELD line and returns its cmd and stdin pipe;
// closing stdin releases it, and the test end kills and reaps whatever remains.
func startHold(t *testing.T, path, label string) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	cmd := helperCmd("hold", path, label)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err, "helper line = %q", line)
	require.Equal(t, "HELD", strings.TrimSpace(line), "the helper did not report holding")
	return cmd, stdin
}

// runTry runs a try-mode helper to completion and returns its trimmed combined output.
func runTry(t *testing.T, path, label string) (string, error) {
	t.Helper()
	out, err := helperCmd("try", path, label).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// TestFunctional_TwoProcessesContend pins that when two distinct operating
// system processes contend for a lock, exactly one holds it, and the other is refused.
func TestFunctional_TwoProcessesContend(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "contend.lock")
	cmdA, stdinA := startHold(t, path, "process-A")

	stamp, err := filelock.ReadStamp(path)
	require.NoError(t, err)
	assert.Equal(t, cmdA.Process.Pid, stamp.PID, "the file does not name process A as its holder: %+v", stamp)

	out, err := runTry(t, path, "process-B")
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr) && exitErr.ExitCode() == 2, "process B should be refused held with exit 2, got out %q, err %v", out, err)

	_ = stdinA.Close()
	_ = cmdA.Wait()

	out, err = runTry(t, path, "process-B2")
	require.NoError(t, err, "output: %q", out)
	require.Equal(t, "ACQUIRED", out, "process B2 did not acquire after the release")
}

// TestFunctional_KilledHolderLockFreedByKernel pins that when a lock holder
// process is killed, the kernel releases the OS lock immediately, and the next
// taker acquires without delay and writes its own note over the dead holder's.
func TestFunctional_KilledHolderLockFreedByKernel(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "kill_release.lock")
	cmd, stdin := startHold(t, path, "killed-worker")
	holderPID := cmd.Process.Pid

	stamp, err := filelock.ReadStamp(path)
	require.NoError(t, err)
	require.Equal(t, holderPID, stamp.PID, "stamp while alive = %+v, want PID %d", stamp, holderPID)

	require.NoError(t, cmd.Process.Kill())
	_ = stdin.Close()
	_ = cmd.Wait()

	// The kernel released the OS lock at death; the dead holder's note is still
	// in the file: nothing released it.
	left, err := filelock.ReadStamp(path)
	require.NoError(t, err)
	assert.Equal(t, holderPID, left.PID, "the killed holder's note: %+v", left)

	lock, err := filelock.TryLock(path, "recovery-taker")
	require.NoError(t, err, "TryLock failed after holder died")
	now, err := filelock.ReadStamp(path)
	require.NoError(t, err)
	assert.Equal(t, "recovery-taker", now.Label, "the file does not name the new holder: %+v", now)

	// A clean unlock clears the note.
	require.NoError(t, lock.Unlock())
	cleared, err := filelock.ReadStamp(path)
	require.NoError(t, err)
	assert.True(t, cleared.IsZero(), "the note after a clean unlock: %+v", cleared)
}

// TestFunctional_HelperProcess is invoked by the functional tests above via exec.Command(os.Args[0]).
func TestFunctional_HelperProcess(t *testing.T) {
	t.Parallel()

	if os.Getenv("FILELOCK_TEST_HELPER") != "1" {
		return
	}

	mode := os.Getenv("FILELOCK_HELPER_MODE")
	path := os.Getenv("FILELOCK_HELPER_PATH")
	label := os.Getenv("FILELOCK_HELPER_LABEL")

	switch mode {
	case "hold":
		l, err := filelock.TryLock(path, label)
		if err != nil {
			os.Stderr.WriteString("ERR: " + err.Error() + "\n")
			os.Exit(1)
		}
		os.Stdout.WriteString("HELD\n")
		// Block until stdin closes (e.g. parent closes pipe)
		_, _ = io.Copy(io.Discard, os.Stdin)
		_ = l.Unlock()
		os.Exit(0)

	case "try":
		l, err := filelock.TryLock(path, label)
		if err == nil {
			_ = l.Unlock()
			os.Stdout.WriteString("ACQUIRED\n")
			os.Exit(0)
		}
		if errors.Is(err, filelock.ErrHeld) {
			os.Stdout.WriteString("HELD\n")
			os.Exit(2)
		}
		os.Stderr.WriteString("ERR: " + err.Error() + "\n")
		os.Exit(1)

	default:
		os.Stderr.WriteString("unknown mode: " + mode + "\n")
		os.Exit(1)
	}
}
