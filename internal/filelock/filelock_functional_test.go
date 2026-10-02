//go:build functional && (unix || windows)

package filelock_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFunctional_TwoProcessesContend tests that when two distinct operating system
// processes contend for a lock, exactly one holds it, and the other is refused.
func TestFunctional_TwoProcessesContend(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "contend.lock")

	// Start Process A to take and hold the lock
	cmdA := exec.Command(os.Args[0], "-test.run=^TestFunctional_HelperProcess$")
	cmdA.Env = append(os.Environ(),
		"FILELOCK_TEST_HELPER=1",
		"FILELOCK_HELPER_MODE=hold",
		"FILELOCK_HELPER_PATH="+path,
		"FILELOCK_HELPER_LABEL=process-A",
	)
	stdinA, err := cmdA.StdinPipe()
	if err != nil {
		require.NoError(t, err, err)
	}
	stdoutA, err := cmdA.StdoutPipe()
	if err != nil {
		require.NoError(t, err, err)
	}
	if err := cmdA.Start(); err != nil {
		require.NoError(t, err, err)
	}
	defer func() {
		_ = stdinA.Close()
		_ = cmdA.Process.Kill()
		_ = cmdA.Wait()
	}()

	readerA := bufio.NewReader(stdoutA)
	lineA, err := readerA.ReadString('\n')
	if err != nil || strings.TrimSpace(lineA) != "HELD" {
		require.Fail(t, fmt.Sprintf("Process A failed to hold lock: line=%q, err=%v", lineA, err))
	}

	// The file names Process A as its holder
	stamp, err := filelock.ReadStamp(path)
	if err != nil {
		require.NoError(t, err, "ReadStamp failed: %v", err)
	}
	if stamp.PID != cmdA.Process.Pid {
		assert.Equal(t, cmdA.Process.Pid, stamp.PID, "stamp PID = %d, want %d", stamp.PID, cmdA.Process.Pid)
	}

	// Process B attempts to acquire the lock immediately via TryLock
	cmdB := exec.Command(os.Args[0], "-test.run=^TestFunctional_HelperProcess$")
	cmdB.Env = append(os.Environ(),
		"FILELOCK_TEST_HELPER=1",
		"FILELOCK_HELPER_MODE=try",
		"FILELOCK_HELPER_PATH="+path,
		"FILELOCK_HELPER_LABEL=process-B",
	)
	outB, errB := cmdB.CombinedOutput()
	// Helper exits with 2 when ErrHeld is returned
	var exitErr *exec.ExitError
	if !errors.As(errB, &exitErr) || exitErr.ExitCode() != 2 {
		require.Fail(t, fmt.Sprintf("Process B should have failed with exit code 2 (held), got output %q, err %v", string(outB), errB))
	}

	// Now release Process A by closing its stdin
	_ = stdinA.Close()
	_ = cmdA.Wait()

	// Process B attempts to acquire again
	cmdB2 := exec.Command(os.Args[0], "-test.run=^TestFunctional_HelperProcess$")
	cmdB2.Env = append(os.Environ(),
		"FILELOCK_TEST_HELPER=1",
		"FILELOCK_HELPER_MODE=try",
		"FILELOCK_HELPER_PATH="+path,
		"FILELOCK_HELPER_LABEL=process-B2",
	)
	outB2, errB2 := cmdB2.CombinedOutput()
	if errB2 != nil || strings.TrimSpace(string(outB2)) != "ACQUIRED" {
		require.Fail(t, fmt.Sprintf("Process B2 failed to acquire after release: out=%q, err=%v", string(outB2), errB2))
	}
}

// TestFunctional_KilledHolderLockFreedByKernel tests that when a lock holder
// process is killed, the kernel releases the OS lock immediately, and the next
// taker acquires without delay and writes its own note over the dead holder's.
func TestFunctional_KilledHolderLockFreedByKernel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "kill_release.lock")

	cmd := exec.Command(os.Args[0], "-test.run=^TestFunctional_HelperProcess$")
	cmd.Env = append(os.Environ(),
		"FILELOCK_TEST_HELPER=1",
		"FILELOCK_HELPER_MODE=hold",
		"FILELOCK_HELPER_PATH="+path,
		"FILELOCK_HELPER_LABEL=killed-worker",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		require.NoError(t, err, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		require.NoError(t, err, err)
	}
	if err := cmd.Start(); err != nil {
		require.NoError(t, err, err)
	}

	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "HELD" {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		require.Fail(t, fmt.Sprintf("Child failed to take lock: line=%q, err=%v", line, err))
	}

	holderPID := cmd.Process.Pid

	// The file names the holder while it is alive
	stamp, err := filelock.ReadStamp(path)
	if err != nil {
		require.NoError(t, err, "ReadStamp failed: %v", err)
	}
	if stamp.PID != holderPID {
		require.Fail(t, fmt.Sprintf("stamp while alive = %+v, want PID %d", stamp, holderPID))
	}

	// KILL the holder abruptly (SIGKILL) so it cannot run Unlock()
	if err := cmd.Process.Kill(); err != nil {
		require.NoError(t, err, "failed to kill child: %v", err)
	}
	_ = stdin.Close()
	_ = cmd.Wait()

	// The kernel releases the OS lock immediately upon death: a try, with no
	// wait, takes it.
	// The dead holder's note is still in the file: nothing released it.
	left, err := filelock.ReadStamp(path)
	require.NoError(t, err)
	assert.Equal(t, holderPID, left.PID, "the killed holder's note: %+v", left)

	lock, err := filelock.TryLock(path, "recovery-taker")
	if err != nil {
		require.NoError(t, err, "TryLock failed after holder died: %v", err)
	}
	now, err := filelock.ReadStamp(path)
	require.NoError(t, err)
	assert.Equal(t, "recovery-taker", now.Label, "the file does not name the new holder: %+v", now)

	// When recovery-taker unlocks cleanly the note is cleared.
	if err := lock.Unlock(); err != nil {
		require.NoError(t, err, "Unlock failed: %v", err)
	}
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
