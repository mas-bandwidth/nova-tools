package pkgselect

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

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
// place SLOWTESTS_ENFORCE=1 is spelled. internal/ci's class test of the nightly
// leg holds it.
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

const (
	// RunnersEnv is the variable the runner service exports: how many runners
	// share this machine. HOW MANY RUNNERS is the MACHINE's fact, not this
	// program's: written here it goes stale, and did, when a fleet grew from four
	// runners a machine to eight.
	RunnersEnv = "NOVA_RUNNERS_PER_MACHINE"
	// DefaultRunners is today's fleet, for a machine that says nothing.
	DefaultRunners = 8
	// ShareCeiling: AT MOST TWO cores a leg. Unit tests "must not be so aggressive
	// that they fill a whole machine cores": min(share, 2), the Makefile's
	// GOTEST_P, whatever the box (nova-tools#4328).
	ShareCeiling = 2
)

// RunnerShare is a leg's FAIR SHARE of the machine: the cores divided by the
// runners on the machine (runnersEnv, the text of $NOVA_RUNNERS_PER_MACHINE;
// DefaultRunners when it is empty, not a number, or not positive), never below 1
// and never above ShareCeiling. Several runners share each machine, and go test
// defaults GOMAXPROCS to every core it can see, so concurrent legs would each ask
// for the whole machine and spend the difference context-switching; `go test -p`
// follows GOMAXPROCS, so this one number bounds both the package-level and the
// in-package parallelism. internal/ci: TestUnitLegTakesAtMostTwoCores.
func RunnerShare(cores int, runnersEnv string) (runners, share int) {
	runners, err := strconv.Atoi(runnersEnv)
	if err != nil || runners < 1 {
		runners = DefaultRunners
	}
	share = cores / runners
	if share < 1 {
		share = 1
	}
	if share > ShareCeiling {
		share = ShareCeiling
	}
	return runners, share
}

const (
	// UnitShimDir is the directory under $RUNNER_TEMP that holds the shim.
	UnitShimDir = "unit-tier-bin"
	// UnitShimExit is the shim's exit status.
	UnitShimExit = 86
	// UnitShimMessage is what the shim prints on stderr.
	// internal/nsprint/testutil.Start names this line.
	UnitShimMessage = "unit tier: redis-server is functional-only (build tag functional)"
)

// WriteUnitShim writes the refusing redis-server under tmp and returns its path.
//
// THE UNIT TIER STARTS NO SERVER (nova-tools#4328; docs/TESTING.md: unit tests
// mock, functional tests carry the build tag). A directory FIRST on PATH holds a
// redis-server that prints why and exits UnitShimExit, so a redis-backed test left
// untagged fails closed under NOVA_CI=1 instead of starting a real server on a
// shared runner. The shim stands in for a binary, so it is a two-line /bin/sh
// file and not a Go program. internal/ci: TestUnitTierRefusesRedisServer.
func WriteUnitShim(tmp string) (string, error) {
	dir := filepath.Join(tmp, UnitShimDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	shim := filepath.Join(dir, "redis-server")
	body := fmt.Sprintf("#!/bin/sh\necho %q >&2\nexit %d\n", UnitShimMessage, UnitShimExit)
	if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
		return "", err
	}
	return shim, os.Chmod(shim, 0o755)
}
