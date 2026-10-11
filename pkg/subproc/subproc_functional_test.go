//go:build functional

package subproc_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/stretchr/testify/require"
)

// soon is the deadline the test injects: short, because it is meant to fire.
var soon = 150 * time.Millisecond

// TestEveryKindKillsASlowChildAtItsDeadline is the deadline test per kind, against a real
// child: a script that sleeps, with a grandchild holding its output pipe. Command is
// CommandFor(k.Budget()) (the unit test pins each budget), so the deadline is injected
// through CommandFor's budget, and WaitDelay is shortened for the run (the constant itself
// is asserted by the unit test). A regression that let the pipe hold the call would run to
// the functional tier's own deadline and fail there.
func TestEveryKindKillsASlowChildAtItsDeadline(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the fake slow child is a shell script")
	}
	script := filepath.Join(t.TempDir(), "slow-child")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nsleep 30 &\nsleep 30\n"), 0o755))
	for _, k := range []subproc.Kind{subproc.Git, subproc.GH, subproc.SSH, subproc.Go, subproc.Tool} {
		t.Run(strconv.Itoa(int(k)), func(t *testing.T) {
			t.Parallel()

			require.Greater(t, k.Budget(), soon, "%v: the named budget %s is not longer than the injected %s", k, k.Budget(), soon)
			ctx := t.Context()
			cmd, cancel := subproc.CommandFor(ctx, soon, script)
			defer cancel()
			cmd.WaitDelay = soon
			_, err := cmd.CombinedOutput()
			require.Error(t, err, "a child that sleeps 30 s returned without an error")
		})
	}
}
