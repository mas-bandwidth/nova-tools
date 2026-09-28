# Test durations

The two-minute rule: anything we call out to answers in one minute ideally, two at
most. This file is a MEASUREMENT of that rule, evidence and not a budget: the one time
budget is `nova-ci slowtests` over a live run's `go test -json`, enforced only on the
nightly whole-tree run on the space legs, and nothing here fails a change.

Regenerate it after any change to a test's cost:

    for p in $(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...); do \
      go test -json -count=1 $p; done | go run ./tools/testdur

`testdur` heads each table it prints with the `<goos>/<goarch>` the run was measured
on, so a paste carries its platform and cannot land under another bench's heading.

One package at a time: `./...` runs packages in parallel and the totals then measure
the bench's contention rather than the package. Tests over five seconds are listed by
name; a test whose cost cannot come down goes behind `//go:build slow`, which the PR
jobs do not build and `.github/workflows/nightly-slow.yml` does.

**One `## Bench:` section per machine.** Name the bench, its platform, its load and
the commit when you regenerate: a recording is a fact about a machine under a load,
and the same core at load 15-18 is the whole difference between `cmd/nova-bus` at
38.5 s and at 30.4 s. `TestEveryRecordedBenchIsNamedAndHasRows` checks that every
section names its platform and holds rows.

The package table for a bench is the one DIRECTLY UNDER its heading. A table under a
`###` inside the section -- the tests over five seconds, the ratios against Space --
is read as prose, not as a second measurement of the same packages.

## Bench: Space, linux/amd64, 16 cores

One package at a time pinned to one core with `taskset -c 12` and `nice -n 10`,
at dev `53023587`, load average 3.0 falling to 1.2 on the other fifteen.
It is the platform CI's space legs run on, the machine the nightly enforcing run's
times come from.

| package | total seconds | slowest test |
| --- | --- | --- |
| cmd/nova-bus | 30.4 | - |
| internal/ci | 17.9 | TestEveryToolPrintsTheOneVersionLine 5.3 |
| internal/swarm | 12.7 | - |
| internal/bus | 6.8 | - |
| cmd/nova-secrets | 5.7 | - |
| internal/update | 5.1 | - |
| cmd/nova-tokens | 2.4 | - |
| internal/merge | 0.9 | - |
| cmd/nova-sandbox | 0.6 | - |
| cmd/nova-self-talk | 0.5 | - |
| internal/wake | 0.5 | - |
| internal/tokens | 0.2 | - |
| cmd/nova-fuse | 0.2 | - |
| cmd/nova-check | 0.2 | - |
| internal/records | 0.2 | - |
| internal/secrets | 0.1 | - |
| internal/docs | 0.1 | - |
| cmd/nova-memory | 0.1 | - |
| internal/check | 0.0 | - |
| internal/dogfood | 0.0 | - |
| internal/memindex | 0.0 | - |
| internal/release | 0.0 | - |
| cmd/nova-version | 0.0 | - |
| internal/sandbox | 0.0 | - |
| cmd/nova-update | 0.0 | - |
| internal/decide | 0.0 | - |
| internal/selftalk | 0.0 | - |
| cmd/nova-cairn | 0.0 | - |
| internal/oneline | 0.0 | - |
| internal/fuse | 0.0 | - |
| internal/fleet | 0.0 | - |
| internal/cairn | 0.0 | - |
| cmd/nova-ci | 0.0 | - |
| internal/bounded | 0.0 | - |
| internal/safepath | 0.0 | - |
| internal/worklang | 0.0 | - |
| internal/goenv | 0.0 | - |
| internal/jobs | 0.0 | - |
| internal/buildinfo | 0.0 | - |
| internal/oneline/audit | 0.0 | - |
| internal/ci/slowtests | 0.0 | - |
| internal/log | 0.0 | - |
| tools/testdur | 0.0 | - |

### TESTS OVER FIVE SECONDS (linux/amd64)

One test is over the line, and it is over it because it runs every tool binary in the
repo for its version line: the cost is real `exec`, and it grows with the number of
tools rather than with anything that can be tuned. `internal/ci` sits at under a third
of its budget, so the test is listed here rather than tagged, and the tag is the answer
if it grows.

| package | test | seconds |
| --- | --- | --- |
| internal/ci | TestEveryToolPrintsTheOneVersionLine | 5.3 |

## Bench: the Air, darwin/arm64, 8 cores

MacBook Air (M-series, 8 cores, go1.27.1), one package at a time at `nice -n 10`, no
pinning -- darwin has no `taskset` and the scheduler is the machine's -- at
dev `0edc81b7`, load average 2.3 at the start and 3.7 at the end, both self-hosted
runners idle throughout. Seven minutes for the 59 packages.

Not a CI bench, and not a candidate to be one: it is a laptop, and its numbers
are here to answer what the same suite costs where a process spawn is dear.

**The whole suite costs 2.2x what it does on Space** (about 380 s against 170 s), and
the cost is not spread evenly -- it lands almost entirely on the packages that spawn
git and child processes, the same shape the Studio shows. `internal/*` packages that only compute are within noise of the Linux bench.

| package | total seconds | slowest test |
| --- | --- | --- |
| cmd/nova-bus | 47.2 | TestRetryAfterAPartialResumesAtNext 11.4 |
| internal/swarm | 22.0 | - |
| internal/bus | 18.9 | - |
| internal/ci | 14.2 | TestEveryToolPrintsTheOneVersionLine 5.6 |
| internal/update | 13.8 | - |
| cmd/nova-secrets | 6.6 | - |
| cmd/nova-sandbox | 6.1 | - |
| internal/merge | 5.6 | - |
| cmd/nova-tokens | 4.3 | - |
| internal/secrets | 3.8 | - |
| cmd/nova-fuse | 2.6 | - |
| internal/wake | 2.0 | - |
| cmd/nova-check | 1.4 | - |
| internal/cairn | 0.8 | - |
| cmd/nova-self-talk | 0.8 | - |
| cmd/nova-version | 0.8 | - |
| internal/dogfood | 0.7 | - |
| cmd/nova-cairn | 0.7 | - |
| cmd/nova-memory | 0.6 | - |
| internal/tokens | 0.6 | - |
| internal/release | 0.4 | - |
| internal/check | 0.4 | - |
| internal/docs | 0.4 | - |
| internal/records | 0.4 | - |
| cmd/nova-update | 0.3 | - |
| internal/buildinfo | 0.3 | - |
| internal/bounded | 0.3 | - |
| internal/decide | 0.3 | - |
| internal/fuse | 0.3 | - |
| internal/sandbox | 0.3 | - |
| internal/memindex | 0.3 | - |
| internal/safepath | 0.3 | - |
| internal/oneline/audit | 0.3 | - |
| internal/fleet | 0.2 | - |
| internal/ci/slowtests | 0.2 | - |
| internal/worklang | 0.2 | - |
| internal/selftalk | 0.2 | - |
| internal/oneline | 0.2 | - |
| internal/jobs | 0.2 | - |
| internal/log | 0.2 | - |
| internal/goenv | 0.2 | - |
| cmd/nova-ci | 0.2 | - |
| tools/testdur | 0.2 | - |

### The biggest, against Space

The ratio is the reading, not the absolute number: a package whose darwin cost is close
to its Linux cost is compute, and one several times over is paying for process spawns.

| package | darwin/arm64 | linux/amd64 | ratio |
| --- | --- | --- | --- |
| cmd/nova-bus | 47.2 | 30.4 | 1.6x |
| internal/swarm | 22.0 | 12.7 | 1.7x |

### TESTS OVER FIVE SECONDS (darwin/arm64)

Many more on this bench than on Space, and none of them is a wait: every one is
a package that shells out. They are listed rather than tagged because the tag is a
property of the TEST, and none of these is over the line on Space, where CI runs -- a
`//go:build slow` added for a laptop would take coverage away from CI.

| package | test | seconds |
| --- | --- | --- |
| cmd/nova-bus | TestRetryAfterAPartialResumesAtNext | 11.4 |
| cmd/nova-bus | TestSnapshotTokenValidationAndBound | 8.7 |
| cmd/nova-bus | TestTwoClonesOfOneLaneRacingTenRoundsAllLand | 8.0 |
| cmd/nova-bus | TestEarlierGapSurvivesLaterPages | 7.7 |
| cmd/nova-bus | TestBodiesModeCapsTheNewSummaryLinesToo | 7.1 |
| cmd/nova-bus | TestBrokenOutputCannotAcknowledgeUnprintedBodies | 6.0 |
| internal/ci | TestEveryToolPrintsTheOneVersionLine | 5.6 |
| cmd/nova-bus | TestBodiesWithoutAdvanceMovesNoCursor | 5.6 |
| cmd/nova-bus | TestContinuationSurvivesOrdinaryCursorAdvance | 5.4 |
| cmd/nova-bus | TestTwoNotesInOneCommitWithMaxNotesOneLosesNeither | 5.3 |
| cmd/nova-bus | TestRetryAfterAPartialResumesAtNext/WithAdvance | 5.2 |
| cmd/nova-bus | TestInboxParsesOnlyWhatIsNewSinceTheCursor | 5.0 |
