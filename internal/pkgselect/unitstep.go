package pkgselect

// ShardGoTestTimeout is `go test -timeout` for a unit shard. It ends a hung run
// with a Go stack naming the test before the two-minute job cap kills the leg
// silently: a timeout at or over the cap would be no timeout at all.
// internal/ci: TestShardGoTestTimeoutIsUnderTheJobCap.
const ShardGoTestTimeout = "110s"

// UnitMakeArgs is the `make test` a unit shard runs, as argv.
//
// The default: Go's test cache on (GOTEST_COUNT_FLAG empty), so a shard whose
// packages did not change since the last run on this machine is served from the
// cache instead of recompiled and re-executed, and test binaries linked without
// DWARF (-ldflags=-w: no dsymutil per binary on darwin, less to scan).
//
// wholeTreeSlowtests is slowtests' flags for a run of the whole tree (a push or
// a manual run), which keeps the old 60 s package budget with the SLEEPS ledger;
// when it is not empty it replaces the Makefile's default budgets. nightly is the
// nightly reference leg: -count=1, so every time is a run and none a cached pass,
// and the budgets ENFORCED (SLOWTESTS_ENFORCE=1). THE VERDICT IS THE SAME ON ANY
// MACHINE otherwise: the CI-SLOW lines and a CI-LOAD line print and exit 0, and a
// CI-SLEEPS line (a SLEEPS skip off the ledger) is exit 2. The nightly leg on the
// Linux shards is the one place a CI-SLOW line fails the run, and this is the only
// place SLOWTESTS_ENFORCE=1 is spelled. internal/ci:
// TestNightlySpaceLegIsTheOnlyEnforcingLeg.
func UnitMakeArgs(packages, wholeTreeSlowtests string, nightly bool) []string {
	args := []string{"make", "test", "PKGS=" + packages, "GOTEST_TIMEOUT=" + ShardGoTestTimeout}
	switch {
	case wholeTreeSlowtests != "":
		return append(args, "GOTEST_COUNT_FLAG=", "GOTEST_LDFLAGS=-ldflags=-w", "SLOWTESTS_FLAGS="+wholeTreeSlowtests)
	case nightly:
		return append(args, "GOTEST_COUNT_FLAG=-count=1", "GOTEST_LDFLAGS=-ldflags=-w", "SLOWTESTS_ENFORCE=1")
	}
	return append(args, "GOTEST_COUNT_FLAG=", "GOTEST_LDFLAGS=-ldflags=-w")
}
