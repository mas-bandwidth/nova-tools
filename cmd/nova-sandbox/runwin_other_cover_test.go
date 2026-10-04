//go:build !windows

// Unit coverage for cmd/nova-sandbox/runwin_other.go: the non-windows body of the
// run verb's windows half. Off windows there is no Job Object and no per-run
// scratch, so this placer is a single refusal and winWallAvailable is always
// false (SPEC-SANDBOX.md "Windows -- the disposable place", rules W1 and W11).
//
// These tests call the production stubs directly. No seam is swapped, no clock
// is replaced and no file is touched, so the file runs green on any non-windows
// host and never races with the seam-swapping tests in runwin_test.go. Each
// function's only path is its own refusal, so every case pins both the zero
// value it hands back (the main path) and the noWinBody() refusal it carries.
package main

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refuseMsg is the sentence noWinBody() prints on this host. The stubs return
// noWinBody() unchanged, so a refusal case compares against the seam's own answer
// rather than a hard-coded string that a wording edit would silently stop matching.
func refuseMsg() string { return noWinBody().Error() }

// assertNoWinRefusal pins the one refusal this placer ever makes: every method
// returns noWinBody(), which names the missing windows body and the host that has
// none. A placer that returned nil here would let a non-windows build pretend it
// could make a windows place -- the exact hygine gap W11 refuses.
func assertNoWinRefusal(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err, "off windows the placer must refuse with noWinBody, not return nil")
	assert.Equal(t, refuseMsg(), err.Error(),
		"the refusal is not noWinBody's; a windows-only place must refuse with the same sentence wherever it is reached")
	assert.Contains(t, err.Error(), runtime.GOOS+" has no body for them",
		"the refusal does not name the host that has no body for the windows place")
}

// TestRunwinOtherCoverWinWallAvailable covers rule 1 off windows: there is no
// AppContainer on a machine that is not windows, so the wall is never available.
func TestRunwinOtherCoverWinWallAvailable(t *testing.T) {
	t.Parallel()

	backend, ok := winWallAvailable()
	t.Run("main path", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "appcontainer", backend,
			"winWallAvailable named %q, want the wall this binary would build; the place is built but the wall is not", backend)
	})
	t.Run("refusal", func(t *testing.T) {
		t.Parallel()
		assert.False(t, ok,
			"winWallAvailable answered true off windows; the AppContainer body is not built here, so the wall is refused")
	})
}

// TestRunwinOtherCoverNoWinBody covers noWinBody itself, the single error every
// noWinPlace method returns.
func TestRunwinOtherCoverNoWinBody(t *testing.T) {
	t.Parallel()

	t.Run("main path", func(t *testing.T) {
		t.Parallel()
		err := noWinBody()
		require.Error(t, err)
	})
	t.Run("refusal", func(t *testing.T) {
		t.Parallel()
		err := noWinBody()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the Job Object and the per-run scratch are windows's",
			"the refusal does not name the windows-only place it has no body for")
		assert.Contains(t, err.Error(), runtime.GOOS+" has no body for them",
			"the refusal does not name the host that has no body for the windows place")
	})
}

// TestRunwinOtherCoverNoWinPlaceMethods covers the ten noWinPlace methods. Each
// row pins the zero value the method returns on its main path and the noWinBody()
// refusal it carries; there is no other path for a stub.
func TestRunwinOtherCoverNoWinPlaceMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		call     func() (main string, err error)
		wantMain string
	}{
		{
			name: "Exists",
			call: func() (string, error) {
				ok, err := noWinPlace{}.Exists("nova-j1")
				return fmt.Sprintf("exists=%v", ok), err
			},
			wantMain: "exists=false",
		},
		{
			name: "MakeScratch",
			call: func() (string, error) {
				return "no-panic", noWinPlace{}.MakeScratch("nova-j1")
			},
			wantMain: "no-panic",
		},
		{
			name: "CreateJob",
			call: func() (string, error) {
				j, err := noWinPlace{}.CreateJob(winLimits{MemoryBytes: 4 << 30, CPUPercent: 50})
				return fmt.Sprintf("job-nil=%v", j == nil), err
			},
			wantMain: "job-nil=true",
		},
		{
			name: "Start",
			call: func() (string, error) {
				s, err := noWinPlace{}.Start(nil, winStartSpec{})
				return fmt.Sprintf("done-nil=%v pid=%d", s.done == nil, s.pid), err
			},
			wantMain: "done-nil=true pid=0",
		},
		{
			name: "CloseJob",
			call: func() (string, error) {
				return "no-panic", noWinPlace{}.CloseJob(nil)
			},
			wantMain: "no-panic",
		},
		{
			name: "Used",
			call: func() (string, error) {
				n, err := noWinPlace{}.Used("nova-j1")
				return fmt.Sprintf("used=%d", n), err
			},
			wantMain: "used=0",
		},
		{
			name: "RemoveTree",
			call: func() (string, error) {
				return "no-panic", noWinPlace{}.RemoveTree("root", "nova-j1", time.Second)
			},
			wantMain: "no-panic",
		},
		{
			name: "WSBAvailable",
			call: func() (string, error) {
				ed, ok, err := noWinPlace{}.WSBAvailable()
				return fmt.Sprintf("edition=%q available=%v", ed, ok), err
			},
			wantMain: `edition="" available=false`,
		},
		{
			name: "WSBRunning",
			call: func() (string, error) {
				who, running, err := noWinPlace{}.WSBRunning()
				return fmt.Sprintf("who=%q running=%v", who, running), err
			},
			wantMain: `who="" running=false`,
		},
		{
			name: "StartWSB",
			call: func() (string, error) {
				return "no-panic", noWinPlace{}.StartWSB("run.wsb", "<Configuration/>")
			},
			wantMain: "no-panic",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			main, err := tc.call()
			assert.Equal(t, tc.wantMain, main,
				"the main path of %s does not return the expected zero value", tc.name)
			assertNoWinRefusal(t, err)
		})
	}
}
