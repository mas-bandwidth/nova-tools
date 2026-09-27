//go:build functional && !windows

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

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
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
		t.Fatal(err)
	}
	stdoutA, err := cmdA.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmdA.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdinA.Close()
		_ = cmdA.Process.Kill()
		_ = cmdA.Wait()
	}()

	readerA := bufio.NewReader(stdoutA)
	lineA, err := readerA.ReadString('\n')
	if err != nil || strings.TrimSpace(lineA) != "HELD" {
		t.Fatalf("Process A failed to hold lock: line=%q, err=%v", lineA, err)
	}

	// Verify Probe reports StateHeld by Process A
	state, stamp, err := filelock.Probe(path)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if state != filelock.StateHeld {
		t.Errorf("state = %s, want %s", state, filelock.StateHeld)
	}
	if stamp.PID != cmdA.Process.Pid {
		t.Errorf("stamp PID = %d, want %d", stamp.PID, cmdA.Process.Pid)
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
		t.Fatalf("Process B should have failed with exit code 2 (held), got output %q, err %v", string(outB), errB)
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
		t.Fatalf("Process B2 failed to acquire after release: out=%q, err=%v", string(outB2), errB2)
	}
}

// TestFunctional_KilledHolderLeavesStaleLock tests that killing a lock holder leaves
// behind a stale lock that Probe reports, and that TryLock refuses to silently steal.
func TestFunctional_KilledHolderLeavesStaleLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "kill_stale.lock")

	cmd := exec.Command(os.Args[0], "-test.run=^TestFunctional_HelperProcess$")
	cmd.Env = append(os.Environ(),
		"FILELOCK_TEST_HELPER=1",
		"FILELOCK_HELPER_MODE=hold",
		"FILELOCK_HELPER_PATH="+path,
		"FILELOCK_HELPER_LABEL=doomed-process",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "HELD" {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("Child failed to take lock: line=%q, err=%v", line, err)
	}

	holderPID := cmd.Process.Pid

	// Verify StateHeld while alive
	state, stamp, err := filelock.Probe(path)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if state != filelock.StateHeld || stamp.PID != holderPID {
		t.Fatalf("Probe while alive = (%s, %+v), want StateHeld for PID %d", state, stamp, holderPID)
	}

	// KILL the holder abruptly (SIGKILL) so it cannot run Unlock()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill child: %v", err)
	}
	_ = stdin.Close()
	_ = cmd.Wait()

	// Child is dead. Verify filelock.ProcessAlive(holderPID) is false
	if filelock.ProcessAlive(holderPID) {
		t.Fatalf("holder PID %d is still reported alive after kill", holderPID)
	}

	// Probe MUST now report StateStale
	stateAfter, stampAfter, err := filelock.Probe(path)
	if err != nil {
		t.Fatalf("Probe after kill failed: %v", err)
	}
	if stateAfter != filelock.StateStale {
		t.Fatalf("Probe state after kill = %s, want %s", stateAfter, filelock.StateStale)
	}
	if stampAfter.PID != holderPID || stampAfter.Label != "doomed-process" {
		t.Fatalf("Probe stamp after kill = %+v, want PID %d and label doomed-process", stampAfter, holderPID)
	}

	// TryLock MUST refuse to steal the stale lock silently
	_, errLock := filelock.TryLock(path, "opportunist")
	if !errors.Is(errLock, filelock.ErrStale) {
		t.Fatalf("TryLock on stale lock returned err = %v, want ErrStale", errLock)
	}

	// ClearStale removes it explicitly
	if err := filelock.ClearStale(path); err != nil {
		t.Fatalf("ClearStale failed: %v", err)
	}

	// Probe now reports StateAbsent
	stateClean, _, err := filelock.Probe(path)
	if err != nil {
		t.Fatalf("Probe after ClearStale failed: %v", err)
	}
	if stateClean != filelock.StateAbsent {
		t.Fatalf("state after ClearStale = %s, want %s", stateClean, filelock.StateAbsent)
	}

	// Can now acquire fresh lock
	l, err := filelock.TryLock(path, "clean-run")
	if err != nil {
		t.Fatalf("TryLock after ClearStale failed: %v", err)
	}
	_ = l.Unlock()
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
		if errors.Is(err, filelock.ErrStale) {
			os.Stdout.WriteString("STALE\n")
			os.Exit(3)
		}
		os.Stderr.WriteString("ERR: " + err.Error() + "\n")
		os.Exit(1)

	default:
		os.Stderr.WriteString("unknown mode: " + mode + "\n")
		os.Exit(1)
	}
}

func TestFunctional_RealClock(t *testing.T) {
	t.Parallel()

	rc := filelock.RealClock{}
	now := rc.Now()
	if now.IsZero() {
		t.Fatal("RealClock.Now() is zero")
	}
	rc.Sleep(0)
}
