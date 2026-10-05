//go:build functional

package update

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessesAreBoundedAndRawSurvivesFailure(t *testing.T) {
	// Each case carries its own timeout because they measure two different
	// things. The hang needs a timeout SHORT enough to fire; the others need one
	// long enough that starting a race-instrumented child on a loaded box is not
	// mistaken for a hang -- at 100ms for all four, the exit-3 case read
	// "timeout" on a busy machine and the assertion it was making was lost.
	// Boundedness is the event: every read returns, and a read that never does
	// fails at the NOVA_TEST_WAIT poll bound instead of being timed against a
	// wall clock (docs/SPEC-CI.md, `waits`; the allowlist header in
	// internal/ci/testdata/fixed-waits-allowlist.txt).
	for _, tc := range []struct {
		cmd, want string
		timeout   time.Duration
	}{
		{command(t, "fail"), "exit 3", 5 * time.Second},
		{command(t, "huge"), "output", 5 * time.Second},
		{command(t, "hang"), "timeout", 20 * time.Millisecond},
		{"nova-version-no-such-binary", "not_found", 5 * time.Second},
	} {
		a, _ := argv(tc.cmd)
		done := make(chan Read, 1)
		go func() {
			done <- Installed(context.Background(), Entry{Kind: "tool", Installed: a}, tc.timeout, true)
		}()
		var r Read
		select {
		case r = <-done:
		case <-time.After(testWait()):
			require.FailNowf(t, "read never returned", "%s: no result within %s", tc.want, testWait())
		}
		if r.Reason != tc.want {
			require.EqualValuesf(t, tc.want, r.Reason, "%s: %+v", tc.want, r)
		}
		if tc.want == "exit 3" && r.Raw != "v9.9.9" {
			require.Fail(t, fmt.Sprintln(r))
		}
	}
	a, _ := argv(command(t, "stderr", base64.StdEncoding.EncodeToString([]byte("v1.2.3\n"))))
	if r := Installed(context.Background(), Entry{Kind: "tool", Installed: a}, time.Second, true); r.Version != "1.2.3" {
		require.EqualValues(t, "1.2.3", r.Version, r)
	}
	a, _ = argv(command(t, "args", ";", "&&", "|", "$(x)", "`x`", "*"))
	p := process(context.Background(), a, nil, ChildCap)
	if p.Stdout != ";|&&|||$(x)|`x`|*" {
		require.EqualValuesf(t, ";|&&|||$(x)|`x`|*", p.Stdout, "shell interpretation: %+v", p)
	}
}

func TestHealthyCommandWithLingeringGrandchildStillReads(t *testing.T) {
	e := Entry{Name: "x", Kind: "tool", Installed: mustArgv(t, command(t, "linger", base64.StdEncoding.EncodeToString([]byte("x 1.2.3\n")), "100ms"))}
	r := Installed(context.Background(), e, 5*time.Second, false)
	if !r.Known() || r.Version != "1.2.3" {
		require.Failf(t, "", "healthy read refused: reason=%q version=%q raw=%q", r.Reason, r.Version, r.Raw)
	}
}

func TestSnapshotLockWaitsForBudget(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "snapshot.json")
	unlock, err := lockSnapshot(context.Background(), path)
	require.NoError(t, err)
	// A held lock is the event: a writer whose budget is already spent is
	// refused as busy, and one whose budget is open waits until the holder
	// releases, then acquires. Neither is timed against a wall clock.
	spent, cancel := context.WithCancel(t.Context())
	cancel()
	release, err := lockSnapshot(spent, path)
	if release != nil {
		release()
	}
	require.Error(t, err, "two snapshot writers acquired lock")
	require.ErrorIs(t, spent.Err(), context.Canceled)

	ctx, stop := context.WithTimeout(t.Context(), testWait())
	defer stop()
	type got struct {
		release func()
		err     error
	}
	waiter := make(chan got, 1)
	go func() {
		release, err := lockSnapshot(ctx, path)
		waiter <- got{release, err}
	}()
	unlock()
	select {
	case g := <-waiter:
		require.NoError(t, g.err, "the waiting writer did not acquire the released lock")
		g.release()
	case <-time.After(testWait()):
		require.FailNow(t, "the waiting writer never acquired the released lock")
	}
}
