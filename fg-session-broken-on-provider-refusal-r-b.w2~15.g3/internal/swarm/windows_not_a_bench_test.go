package swarm

import (
	"runtime"
	"testing"
)

// windowsIsNotABench skips a test of the bench, native-wall or publish path on Windows.
// SPEC-SWARM: the remote bench command line (ssh, taskset, rsync, scp) and the native
// wall (sandbox-exec, Landlock) are POSIX by construction; Windows is a client of the
// swarm (add, status, cost, verify, template), never a bench. Main went red on the
// hosted windows leg on 2026-09-16 with exactly these tests; the rule is now written.
func windowsIsNotABench(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("SPEC-SWARM: the bench command line and the native wall are POSIX by construction; Windows is a client, not a bench")
	}
}
