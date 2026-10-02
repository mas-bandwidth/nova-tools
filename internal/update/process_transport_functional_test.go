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
	for _, tc := range []struct {
		cmd, want string
		timeout   time.Duration
		bound     time.Duration
	}{
		{command(t, "fail"), "exit 3", 5 * time.Second, 6 * time.Second},
		{command(t, "huge"), "output", 5 * time.Second, 6 * time.Second},
		{command(t, "hang"), "timeout", 20 * time.Millisecond, time.Second + killGrace},
		{"nova-version-no-such-binary", "not_found", 5 * time.Second, time.Second},
	} {
		a, _ := argv(tc.cmd)
		start := time.Now()
		r := Installed(context.Background(), Entry{Kind: "tool", Installed: a}, tc.timeout, true)
		if r.Reason != tc.want {
			require.EqualValuesf(t, tc.want, r.Reason, "%s: %+v", tc.want, r)
		}
		if took := time.Since(start); took > tc.bound {
			require.LessOrEqualf(t, took, tc.bound, "%s: %s is past the %s bound", tc.want, took, tc.bound)
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
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	release, err := lockSnapshot(ctx, path)
	if release != nil {
		release()
	}
	require.Error(t, err, "two snapshot writers acquired lock")
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}
