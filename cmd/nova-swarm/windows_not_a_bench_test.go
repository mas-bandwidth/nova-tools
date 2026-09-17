package main

import (
	"runtime"
	"testing"
)

// windowsIsNotABench skips a test of the bench, native-wall, legacy auth copy or publish path on Windows.
// SPEC-SWARM: the remote bench command line (ssh, taskset, rsync, scp), the native
// wall (sandbox-exec, Landlock) and 0600 secret auth file copies are POSIX by construction;
// Windows is a client of the swarm (add, status, cost, verify, template), never a bench (#915).
// Main went red on the hosted windows leg on 2026-09-16 with exactly these tests; the rule is now written.
func windowsIsNotABench(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("SPEC-SWARM: the bench command line, native wall and 0600 auth copies are POSIX by construction; Windows is a client, not a bench (#915)")
	}
}
