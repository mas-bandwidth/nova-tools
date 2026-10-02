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
	"time"

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

	// Verify Probe reports StateHeld by Process A
	state, stamp, err := filelock.Probe(path)
	if err != nil {
		require.NoError(t, err, "Probe failed: %v", err)
	}
	if state != filelock.StateHeld {
		assert.Equal(t, filelock.StateHeld, state, "state = %s, want %s", state, filelock.StateHeld)
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

// TestFunctional_KilledHolderLockFreedByKernelAndPreviousNoteObserved tests that when
// a lock holder process is killed, the kernel releases the OS lock immediately,
// and the next taker acquires without delay while observing Previous() *Stamp.
func TestFunctional_KilledHolderLockFreedByKernelAndPreviousNoteObserved(t *testing.T) {
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

	// Verify StateHeld while alive
	state, stamp, err := filelock.Probe(path)
	if err != nil {
		require.NoError(t, err, "Probe failed: %v", err)
	}
	if state != filelock.StateHeld || stamp.PID != holderPID {
		require.Fail(t, fmt.Sprintf("Probe while alive = (%s, %+v), want StateHeld for PID %d", state, stamp, holderPID))
	}

	// KILL the holder abruptly (SIGKILL) so it cannot run Unlock()
	if err := cmd.Process.Kill(); err != nil {
		require.NoError(t, err, "failed to kill child: %v", err)
	}
	_ = stdin.Close()
	_ = cmd.Wait()

	// The kernel releases the OS lock immediately upon death.
	// Probe can now acquire a shared lock and report StateFree!
	stateAfter, _, err := filelock.Probe(path)
	if err != nil {
		require.NoError(t, err, "Probe after kill failed: %v", err)
	}
	if stateAfter != filelock.StateFree {
		require.Equal(t, filelock.StateFree, stateAfter, "Probe state after kill = %s, want %s (kernel drops lock)", stateAfter, filelock.StateFree)
	}

	// New taker acquires immediately and observes previous unreleased holder
	lock, err := filelock.Lock(path, "recovery-taker", time.Second)
	if err != nil {
		require.NoError(t, err, "Lock failed after holder died: %v", err)
	}
	defer lock.Unlock()

	if lock.Previous() == nil {
		require.Fail(t, fmt.Sprintf("lock.Previous() = nil, want killed holder stamp"))
	}
	if lock.Previous().PID != holderPID {
		assert.Fail(t, fmt.Sprintf("Previous().PID = %d, want %d", lock.Previous().PID, holderPID))
	}
	if lock.Previous().Label != "killed-worker" {
		assert.Fail(t, fmt.Sprintf("Previous().Label = %q, want killed-worker", lock.Previous().Label))
	}

	// When recovery-taker unlocks cleanly:
	if err := lock.Unlock(); err != nil {
		require.NoError(t, err, "Unlock failed: %v", err)
	}

	// Next taker sees nil Previous() because Unlock cleanly truncated the note
	nextLock, err := filelock.TryLock(path, "clean-taker")
	if err != nil {
		require.NoError(t, err, "TryLock failed: %v", err)
	}
	defer nextLock.Unlock()

	if nextLock.Previous() != nil {
		assert.Fail(t, fmt.Sprintf("nextLock.Previous() = %+v, want nil after clean unlock", nextLock.Previous()))
	}
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

// H1 witness in its concurrent form: a Probe holds the shared lock for an
// instant, and a taker landing in that instant must not be told held, because
// nobody holds (tla/FileLock.tla, HeldIsTrue). Busy is a true answer and is
// counted, not failed. Functional tier: a take and release cost about 10 ms on
// macOS (two full fsyncs), so 300 of them are 3 s; the unit-tier witness is
// TestTryLock_AskerIsNotAHolder. At ff635f74f this refused 45 and 19 of 2,000
// takes as held on macOS and 108 of 2,000 on Linux.
func TestFunctional_ProbeDoesNotDisturbTaker(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "probed.lock")
	if err := os.WriteFile(path, nil, 0666); err != nil {
		require.NoError(t, err, err)
	}
	stop := make(chan struct{})
	done := make(chan int)
	go func() {
		probes := 0
		for {
			select {
			case <-stop:
				done <- probes
				return
			default:
			}
			if _, _, err := filelock.Probe(path); err != nil {
				assert.NoError(t, err, "Probe: %v", err)
			}
			probes++
		}
	}()

	const takes = 300
	held, busy := 0, 0
	var firstHeld error
	for i := 0; i < takes; i++ {
		lock, err := filelock.TryLock(path, "taker")
		switch {
		case err == nil:
			lock.Unlock()
		case errors.Is(err, filelock.ErrBusy):
			busy++
		case errors.Is(err, filelock.ErrHeld):
			held++
			if firstHeld == nil {
				firstHeld = err
			}
		default:
			require.Fail(t, fmt.Sprintf("TryLock: %v", err))
		}
	}
	close(stop)
	probes := <-done
	t.Logf("%d takes beside %d probes: %d told held, %d told busy", takes, probes, held, busy)
	if held > 0 {
		assert.LessOrEqual(t, held, 0, "told held %d times of %d with nobody holding, only a prober; first: %v", held, takes, firstHeld)
	}
}
