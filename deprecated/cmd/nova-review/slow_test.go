//go:build slow

// The tests of this package that cost more than the per-commit run can pay:
// over five seconds each on the Linux bench, or a deadline, wedge or wall-clock
// bound proved by waiting it out. They are behind the `slow` build tag, so
// go-test-cmd and go-test-internal do not build them, and
// .github/workflows/nightly-slow.yml (and `make test-slow`) runs them whole,
// every night. Each carries the measurement that moved it. Nothing here is
// skipped or weakened.

package main

import (
	"bytes"
	"strings"
	"testing"
)

// SLOW: 1.0 s on hetzner at dev 64b9bec48, a deadline/wedge/wall bound proved by waiting it out.
// mutate-seed-timeout-is-not-a-pass: the deadline kills `go test` before any unit
// reports, and a deadline is a could-not-run -- exit 2 -- never a mutant that died.
//
// This is the end-to-end of it, and the only way to have it end to end is to let a
// real second pass: the deadline is `--timeout`, in whole seconds, on the wall clock.
// So it is behind `-short`, and `internal/review`'s
// TestSeedTimeoutIsACouldNotRunAndNeverAKill holds the same rule at the function that
// decides it, instantly, on every run. The two-minute law is not a thing to spend a
// second of on a branch that already has the proof.
func TestMutateSeedTimeoutIsNotAPass(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("a real --timeout is a real second; internal/review holds this rule without the clock")
	}
	dir := slowSeedLab(t)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, oneEditSeed), "--tests", "sign", "--timeout", "1"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if out.String() != "" {
		t.Fatalf("a verdict printed for a run that was killed: %q", out.String())
	}
	if !strings.Contains(errb.String(), "deadline") {
		t.Fatalf("stderr = %q, want a refusal naming the deadline", errb.String())
	}
}
