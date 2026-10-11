//go:build darwin

// The cover tests for the two functions in runexec_darwin.go that the unit tier
// never reached: startInOwnGroup (the run verb's executor) and statusOfRun (the
// wrapped command's env(1)-style status mapper). Both carried 0.0% in the
// per-function coverage table, because every existing run test replaces the
// runExec seam with a fake and never starts the real executor, and nothing
// exercised the status mapper at all.
//
// statusOfRun is a pure function of (waitErr, state), so it is fed forged
// *os.ProcessStates (no subprocess) for every branch. startInOwnGroup's main
// path is a real sandbox-exec child in a disposable volume, so it stays the
// property of run_e2e_darwin_test.go behind the novadisk tag; this file instead
// pins each refusal branch the function can make BEFORE it would start a child,
// driven through the only seam the function exposes here: the *sandbox.Policy
// handed to the profile generator.

package main

import (
	"errors"
	"io"
	"os"
	"reflect"
	"syscall"
	"testing"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forgedProcessState plants a syscall.WaitStatus into an *os.ProcessState
// without starting a child: statusOfRun reads only Sys() and ExitCode(), so the
// test supplies the state the function would otherwise receive from a real Wait.
// os.ProcessState.status is unexported, so the value is written through
// reflect+unsafe; only the documented accessors are then read, exactly as
// startInOwnGroup does. No store, no network, no clock.
func forgedProcessState(ws syscall.WaitStatus) *os.ProcessState {
	ps := &os.ProcessState{}
	f := reflect.ValueOf(ps).Elem().FieldByName("status")
	*(*syscall.WaitStatus)(unsafe.Pointer(f.UnsafeAddr())) = ws
	return ps
}

// waitStatusExited packs a BSD-family (darwin) exit-code status: low seven bits
// clear (the "exited" marker) and the code in the high byte, as the kernel does.
func waitStatusExited(code int) syscall.WaitStatus { return syscall.WaitStatus(code << 8) }

// waitStatusSignaled packs a BSD-family signal-terminated status: the low seven
// bits carry the signal, which is neither 0 nor the 0x7f "stopped" marker.
func waitStatusSignaled(sig syscall.Signal) syscall.WaitStatus { return syscall.WaitStatus(int(sig)) }

// waitStatusStopped packs a BSD-family stopped status: low seven bits 0x7f (the
// stopped marker) and a non-SIGSTOP signal in the high byte, so Exited() is
// false, Signaled() is false, and ExitCode() answers -1 -- the arm that drives
// statusOfRun's waitErr/nil fallthrough.
func waitStatusStopped(sig syscall.Signal) syscall.WaitStatus {
	return syscall.WaitStatus(0x7f | (int(sig) << 8))
}

// TestRunexecDarwinCoverStatusOfRun pins every branch of statusOfRun: a nil
// state (with and without a wait error), a clean exit and a coded exit, a signal
// death mapped as 128+signal, and the two arms for a state with no usable exit
// code -- one carrying a wait error (not executed) and one without (zero).
func TestRunexecDarwinCoverStatusOfRun(t *testing.T) {
	t.Parallel()
	errWait := errors.New("wait: context canceled")
	sigTerm := syscall.SIGTERM
	stopped := waitStatusStopped(syscall.SIGTSTP)
	cases := []struct {
		name    string
		waitErr error
		state   *os.ProcessState
		want    int
	}{
		{name: "nil_state_is_not_executed", state: nil, want: sandbox.ExitNotExecuted},
		{name: "nil_state_with_wait_error_is_not_executed", waitErr: errWait, state: nil, want: sandbox.ExitNotExecuted},
		{name: "clean_exit_zero", state: forgedProcessState(waitStatusExited(0)), want: 0},
		{name: "exit_code_propagated", state: forgedProcessState(waitStatusExited(7)), want: 7},
		{name: "signal_death_is_128_plus_signal", state: forgedProcessState(waitStatusSignaled(sigTerm)), want: 128 + int(sigTerm)},
		{name: "stopped_with_wait_error_is_not_executed", waitErr: errWait, state: forgedProcessState(stopped), want: sandbox.ExitNotExecuted},
		{name: "stopped_without_wait_error_is_zero", state: forgedProcessState(stopped), want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, statusOfRun(tc.waitErr, tc.state))
		})
	}
}

// TestRunexecDarwinCoverStartInOwnGroupRefused pins startInOwnGroup's refusal
// branch. The function refuses in order: root, then no sandbox backend, then a
// profile that cannot be generated -- and the branch that fires is
// environment-determined, so the test asserts the one the host actually reaches
// and stays green on every machine while pinning whichever guard tripped. The
// policy here has no --write, so on a normal host (non-root, sandbox-exec
// present) DarwinProfile errors and the function returns the profile-generation
// refusal BEFORE any child is started: the path under test needs no subprocess.
//
// What this does NOT cover, and is out of scope for a unit test: the main path
// itself (a real sandbox-exec child in a disposable volume, owned by
// run_e2e_darwin_test.go) and the root/no-sandbox guards, which are not
// injectable from this package -- they read os.Geteuid and the package-level
// sandbox backend lookup directly.
func TestRunexecDarwinCoverStartInOwnGroupRefused(t *testing.T) {
	t.Parallel()
	_, hasBackend := sandbox.Available()
	// A policy with no --write: DarwinProfile refuses it before any exec.
	p := &sandbox.Policy{}
	_, err := startInOwnGroup(p, nil, nil, io.Discard, io.Discard)
	require.Error(t, err, "a policy with no --write must be refused, not started")
	var r sandbox.Refusal
	require.True(t, errors.As(err, &r), "want a sandbox.Refusal, got %T: %v", err, err)
	switch {
	case os.Geteuid() == 0:
		assert.Equal(t, "sandbox_failed", r.Reason)
		assert.Contains(t, r.Text, "does not run as root")
	case hasBackend:
		assert.Equal(t, "sandbox_failed", r.Reason)
		assert.Contains(t, r.Text, "profile could not be generated")
	default:
		assert.Equal(t, "no_sandbox", r.Reason)
		assert.Contains(t, r.Text, "sandbox-exec is on no PATH entry")
	}
}
