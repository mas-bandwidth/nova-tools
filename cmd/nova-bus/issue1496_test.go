package main

import (
	"github.com/stretchr/testify/require"
	"testing"
)

// A bare send names --branch, --bus and --remote together, in that fixed
// order, on every run. The skeleton prints each as SEND REFUSED and says
// what the flag wants; the flags and their order are the fact this pins.

func TestIssue1496ABareVerbNamesEveryMissingRequiredFlagInAFixedOrder(t *testing.T) {
	t.Parallel()

	const want = "SEND REFUSED: --branch is required; it wants the branch the bus lives on; refusing to guess; run: nova-bus help\n" +
		"SEND REFUSED: --bus is required; it wants the bus's repository root; refusing to guess; run: nova-bus help\n" +
		"SEND REFUSED: --remote is required; it wants the git remote to push to; refusing to guess; run: nova-bus help\n"
	first := ""
	for i := 0; i < 20; i++ {
		r := invoke(t, "", "send")
		require.Equalf(t, 2, r.code, "run %d: exit = %d, want 2\nstderr: %s", i, r.code, r.stderr)
		if first == "" {
			first = r.stderr
		} else {
			require.Equalf(t, first, r.stderr, "run %d stderr differs from run 0:\n run 0: %q\n run %d: %q", i, first, i, r.stderr)
		}
	}
	require.Equalf(t, want, first, "stderr = %q, want %q", first, want)
}
