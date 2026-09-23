package swarm

import (
	"strings"
	"testing"
)

// A TEST: LINE NAMES NO GATE THAT RUNS IT (ideas #796).
//
// A v2 card names its reproducing test on `TEST: <package> <TestName>` and names the
// gate that runs it on `RUN:` / the `## Run` region (docs/SPEC-CARD.md §5, §6). The
// `test-named` token checks the SHAPE of the TEST: line; nothing checked that a card's
// gate actually runs the package the TEST: line names. A card could carry
// `TEST: ./internal/pulse TestX` beside `RUN: go test ./internal/swarm/` -- a shape the
// gate reads, a package no command ever runs -- and the anchor test it claims would
// never be executed by the card's own gate.
//
// `LintCardTestGate` closes that: a card whose TEST: names a package no gate command
// runs draws `test-gate`; a card whose gate runs it draws nothing. This file was red
// before the check existed; every red case has an accept case beside it.

// gateCard renders a card from its header lines plus an optional `## Run` region. The
// header lines go directly under the contract line; when runRegion is non-empty it is
// appended as the clause-6 single Run section after the prose's first line, else the card
// carries no region and `RUN:` (when present) is the only gate echo.
func gateCard(runRegion string, header ...string) []byte {
	lines := []string{"RESULT: CARD-0796 do the thing"}
	lines = append(lines, header...)
	lines = append(lines, "", "You are a Go engineer.", "STEP 1. cd repo")
	if runRegion != "" {
		lines = append(lines, "", "## Run")
		lines = append(lines, strings.Split(runRegion, "\n")...)
	}
	lines = append(lines, "")
	return []byte(strings.Join(lines, "\n"))
}

// testGateDrew says whether LintCardTestGate drew the test-gate token for one card.
func testGateDrew(fs []CardHeaderFinding) bool {
	for _, f := range fs {
		if f.Check == "test-gate" {
			return true
		}
	}
	return false
}

// RED: TEST names ./internal/pulse TestX, but the gate (header echo) runs only
// ./internal/swarm -- no gate runs the named test, so the lint refuses.
func TestLintRefusesATestNoGateRuns(t *testing.T) {
	raw := gateCard("",
		"KIND: fix-red",
		"PATHS: internal/pulse/thing.go, internal/pulse/thing_test.go",
		"TEST: ./internal/pulse/ TestThing",
		"RUN: gofmt -l . && go vet ./... && go test ./internal/swarm/ -count=1",
	)
	if !testGateDrew(LintCardTestGate(raw)) {
		t.Fatalf("a TEST: naming ./internal/pulse run by no gate must draw test-gate, got %v", LintCardTestGate(raw))
	}
}

// NEGATIVE CONTROL for the red above: the same card whose gate runs the package the
// TEST: line names draws nothing.
func TestLintAcceptsATestItsGateRuns(t *testing.T) {
	raw := gateCard("",
		"KIND: fix-red",
		"PATHS: internal/pulse/thing.go, internal/pulse/thing_test.go",
		"TEST: ./internal/pulse/ TestThing",
		"RUN: gofmt -l . && go vet ./... && go test ./internal/pulse/ -count=1",
	)
	if fs := LintCardTestGate(raw); testGateDrew(fs) {
		t.Fatalf("a gate that runs the named package must draw nothing, got %v", fs)
	}
}

// The `## Run` region is the clause-6 sole authority: when the region runs the package
// but a stale header echo does not, the card is still accepted.
func TestLintAcceptsWhenTheRunRegionRunsIt(t *testing.T) {
	raw := gateCard("RUN:\n```sh\ngo test ./internal/pulse/ -run TestThing -count=1\n```",
		"KIND: fix-red",
		"PATHS: internal/pulse/thing.go, internal/pulse/thing_test.go",
		"TEST: ./internal/pulse/ TestThing",
		"RUN: go test ./internal/swarm/ -count=1",
	)
	if fs := LintCardTestGate(raw); testGateDrew(fs) {
		t.Fatalf("the Run region is the authority and it runs the package; got %v", fs)
	}
}

// `./...` is a gate that runs every package, so it runs the named one.
func TestLintAcceptsADotDotDotGate(t *testing.T) {
	raw := gateCard("",
		"KIND: fix-red",
		"PATHS: internal/pulse/thing.go, internal/pulse/thing_test.go",
		"TEST: ./internal/pulse/ TestThing",
		"RUN: go test ./... -count=1",
	)
	if fs := LintCardTestGate(raw); testGateDrew(fs) {
		t.Fatalf("`go test ./...` runs the named package; got %v", fs)
	}
}

// A parent directory run with `/...` covers a package under it.
func TestLintAcceptsAParentEllipsisGate(t *testing.T) {
	raw := gateCard("",
		"KIND: fix-red",
		"PATHS: internal/pulse/thing.go, internal/pulse/thing_test.go",
		"TEST: ./internal/pulse/ TestThing",
		"RUN: go test ./internal/... -count=1",
	)
	if fs := LintCardTestGate(raw); testGateDrew(fs) {
		t.Fatalf("`go test ./internal/...` runs ./internal/pulse; got %v", fs)
	}
}

// An opaque `make` gate could run any package, so it is not refused.
func TestLintAcceptsAMakeGate(t *testing.T) {
	raw := gateCard("",
		"KIND: fix-red",
		"PATHS: internal/pulse/thing.go, internal/pulse/thing_test.go",
		"TEST: ./internal/pulse/ TestThing",
		"RUN: make test",
	)
	if fs := LintCardTestGate(raw); testGateDrew(fs) {
		t.Fatalf("a make gate runs an unknown set and is not refused; got %v", fs)
	}
}

// RED: a card with a valid TEST: line but no gate at all -- no `RUN:` header and no
// `## Run` region -- names a test no gate runs.
func TestLintRefusesACardWithNoGateAtAll(t *testing.T) {
	raw := gateCard("",
		"KIND: fix-red",
		"PATHS: internal/pulse/thing.go, internal/pulse/thing_test.go",
		"TEST: ./internal/pulse/ TestThing",
	)
	if !testGateDrew(LintCardTestGate(raw)) {
		t.Fatalf("a card with no gate names a test nothing runs; got %v", LintCardTestGate(raw))
	}
}

// `TEST: none` declares no Go anchor and so no gate obligation: never refused here.
func TestLintAcceptsTestNone(t *testing.T) {
	raw := gateCard("",
		"KIND: read",
		"PATHS: none",
		"TEST: none",
	)
	if fs := LintCardTestGate(raw); testGateDrew(fs) {
		t.Fatalf("`TEST: none` carries no gate obligation; got %v", fs)
	}
}

// A malformed TEST: line is `test-named`'s finding, not this check's: it must not draw a
// second, confusing token for the same line.
func TestLintLeavesMalformedTestToTestNamed(t *testing.T) {
	raw := gateCard("",
		"KIND: fix-red",
		"PATHS: internal/pulse/thing.go",
		"TEST: TestOnlyAName",
		"RUN: go test ./internal/swarm/ -count=1",
	)
	if fs := LintCardTestGate(raw); testGateDrew(fs) {
		t.Fatalf("a one-field TEST: is test-named's, not test-gate's; got %v", fs)
	}
}

// A card with no typed header at all is left alone by this check.
func TestLintIgnoresACardWithoutATestLine(t *testing.T) {
	raw := gateCard("",
		"KIND: fix-red",
		"PATHS: internal/pulse/thing.go, internal/pulse/thing_test.go",
		"RUN: go test ./internal/pulse/ -count=1",
	)
	if fs := LintCardTestGate(raw); len(fs) != 0 {
		t.Fatalf("no TEST: line means nothing to check; got %v", fs)
	}
}

// RED (Stella's HOLD on #2878): a detached `-run` value that looks like the TEST package
// is the -run regexp, not a package; `go test -run ./internal/pulse ./internal/swarm/`
// runs ./internal/swarm only, so no gate runs ./internal/pulse and the lint refuses.
func TestLintRefusesADetachedFlagValueAsPackage(t *testing.T) {
	for _, run := range []string{
		"RUN: go test -run ./internal/pulse ./internal/swarm/",
		"RUN: go test -count 1 -run ./internal/pulse/ ./internal/swarm/",
		"RUN: go test -test.run ./internal/pulse ./internal/swarm/",
		"RUN: go test -coverpkg ./internal/pulse ./internal/swarm/",
		"RUN: go test ./internal/swarm/ -args ./internal/pulse",
	} {
		raw := gateCard("",
			"KIND: fix-red",
			"PATHS: internal/pulse/thing.go, internal/pulse/thing_test.go",
			"TEST: ./internal/pulse TestX",
			run,
		)
		if !testGateDrew(LintCardTestGate(raw)) {
			t.Errorf("%s runs only ./internal/swarm; a flag value is not a package and must draw test-gate, got %v", run, LintCardTestGate(raw))
		}
	}
}

// NEGATIVE CONTROL for the red above: a boolean flag takes no value, a `=`-joined value
// consumes nothing, and a detached value is followed by the real package -- each runs it.
func TestLintAcceptsAPackageAfterFlags(t *testing.T) {
	for _, run := range []string{
		"RUN: go test -v ./internal/pulse/",
		"RUN: go test -run=TestX ./internal/pulse/",
		"RUN: go test -run TestX ./internal/pulse/",
		"RUN: go test -count 1 -race -run TestX ./internal/pulse/ -args -foo",
	} {
		raw := gateCard("",
			"KIND: fix-red",
			"PATHS: internal/pulse/thing.go, internal/pulse/thing_test.go",
			"TEST: ./internal/pulse TestX",
			run,
		)
		if fs := LintCardTestGate(raw); testGateDrew(fs) {
			t.Errorf("%s runs ./internal/pulse; got %v", run, fs)
		}
	}
}
