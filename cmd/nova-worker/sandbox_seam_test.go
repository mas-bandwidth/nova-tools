package main

import (
	"runtime"
	"testing"
)

// THE LAUNCH SEAM: every job runs inside nova-sandbox (docs/SPEC-SANDBOX.md, "the two
// callers": nova-worker, at its launch seam). These are the tests that spec demands of the
// DISPATCHER caller, and each one names the platform it runs on and is skipped with a
// reason, never silently.
//
// Two kinds of test live here, and the difference matters:
//
//   - the SEAM tests, which are about the argv the dispatcher builds, the probe it runs
//     before the first worker and the one loud line of --no-sandbox. They run on every
//     platform against the fake sandbox on PATH, because the seam is the same argv on a
//     machine whose body is built and on one whose is not.
//   - the WALL tests, which ask the operating system whether a job can write outside its
//     job directory or read the key file. They run against the REAL nova-sandbox, on
//     darwin, which is the platform whose body this repository has built.

// wallOnly skips a test that needs a real wall, by name, on a platform whose body is not
// built. Rule 1 of SPEC-SANDBOX is that such a platform REFUSES rather than pretends, and a
// test that asserted a denial there would be asserting the refusal of the run, not the wall.
func wallOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skipf("the wall is asked about the operating system itself, and only the darwin body (sandbox-exec) is built in this repository; on %s nova-sandbox REFUSES and there is no wall to question", runtime.GOOS)
	}
}
