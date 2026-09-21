# Test durations

Glenn's two-minute rule (2026-09-10, reaffirmed 2026-09-15): anything we call out to
answers in one minute ideally, two at most. This file is the MEASUREMENT that rule is
enforced against -- `tools/testdur`'s `TestFastSuiteUnderOneMinute` reads it and fails
when a package's total crosses 60 s, so a package that slows down is a red on the
change that slowed it rather than a CI everyone waits on.

Regenerate it after any change to a test's cost:

    for p in $(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...); do \
      go test -json -count=1 $p; done | go run ./tools/testdur

One package at a time: `./...` runs packages in parallel and the totals then measure
the bench's contention rather than the package. Tests over five seconds are listed by
name; a test whose cost cannot come down goes behind `//go:build slow`, which the PR
jobs do not build and `.github/workflows/nightly-slow.yml` does.

**One `## Bench:` section per machine, and exactly one of them marked `[budget]`.**
Name the bench, its platform and its load when you regenerate: a recording is a fact
about a machine under a load, and the 2026-09-15 recording on the same core with load
15-18 beside it is the whole difference between `cmd/nova-bus` at 38.5 s and at 30.4 s.
Only the `[budget]` bench's numbers are enforced. Every other bench is EVIDENCE --
what a package costs where the system calls are dear, which packages are slow for a
reason that is not the test, and what ratio to expect on the Windows bench when it
arrives -- and enforcing a budget against it would make a change answerable for
whichever machine somebody happened to measure on (`TestEveryRecordedBenchIsNamedAndHasRows`).

## Bench: Space, linux/amd64, 16 cores [budget]

One package at a time pinned to one core with `taskset -c 12` and `nice -n 10`,
2026-09-18 at dev `53023587`, load average 3.0 falling to 1.2 on the other fifteen.
This is the bench the budget belongs to: it is the platform CI runs on, so a red here
is a red a change can answer for.

| package | total seconds | slowest test |
| --- | --- | --- |
| cmd/nova-bus | 30.4 | - |
| internal/ci | 17.9 | TestEveryToolPrintsTheOneVersionLine 5.3 |
| cmd/nova-swarm | 16.8 | - |
| cmd/nova-merge | 16.7 | - |
| internal/swarm | 12.7 | - |
| cmd/nova-wake | 12.4 | - |
| internal/bus | 6.8 | - |
| cmd/nova-secrets | 5.7 | - |
| internal/update | 5.1 | - |
| cmd/nova-review | 3.8 | - |
| internal/pulse | 3.0 | - |
| cmd/nova-tokens | 2.4 | - |
| cmd/nova-board | 1.9 | - |
| internal/review | 1.0 | - |
| internal/merge | 0.9 | - |
| cmd/nova-sandbox | 0.6 | - |
| cmd/nova-self-talk | 0.5 | - |
| cmd/nova-pulse | 0.5 | - |
| internal/wake | 0.5 | - |
| internal/tokens | 0.2 | - |
| cmd/nova-fuse | 0.2 | - |
| cmd/nova-check | 0.2 | - |
| internal/records | 0.2 | - |
| internal/secrets | 0.1 | - |
| internal/docs | 0.1 | - |
| cmd/nova-memory | 0.1 | - |
| internal/check | 0.0 | - |
| internal/board | 0.0 | - |
| internal/dogfood | 0.0 | - |
| internal/memindex | 0.0 | - |
| cmd/nova-work | 0.0 | - |
| internal/release | 0.0 | - |
| cmd/nova-decide | 0.0 | - |
| cmd/nova-version | 0.0 | - |
| cmd/nova-post | 0.0 | - |
| internal/sandbox | 0.0 | - |
| cmd/nova-update | 0.0 | - |
| internal/decide | 0.0 | - |
| internal/selftalk | 0.0 | - |
| cmd/nova-cairn | 0.0 | - |
| internal/specwork | 0.0 | - |
| internal/oneline | 0.0 | - |
| internal/fuse | 0.0 | - |
| internal/fleet | 0.0 | - |
| internal/cairn | 0.0 | - |
| internal/friends | 0.0 | - |
| cmd/nova-ci | 0.0 | - |
| internal/bounded | 0.0 | - |
| internal/safepath | 0.0 | - |
| internal/worklang | 0.0 | - |
| internal/chat | 0.0 | - |
| internal/goenv | 0.0 | - |
| internal/jobs | 0.0 | - |
| internal/buildinfo | 0.0 | - |
| internal/dispatch | 0.0 | - |
| internal/oneline/audit | 0.0 | - |
| internal/ci/slowtests | 0.0 | - |
| internal/log | 0.0 | - |
| tools/testdur | 0.0 | - |

### Tests over five seconds

One test is over the line, and it is over it because it runs every tool binary in the
repo for its version line: the cost is real `exec`, and it grows with the number of
tools rather than with anything that can be tuned. `internal/ci` sits at under a third
of its budget, so the test is listed here rather than tagged, and the tag is the answer
if it grows. The two `cmd/nova-merge` tests listed on 2026-09-15 at 5.1 s each now
measure 0.09 s and 0.06 s -- they waited on a clock and now have a sync point -- so they
have dropped off this list entirely.

| package | test | seconds |
| --- | --- | --- |
| internal/ci | TestEveryToolPrintsTheOneVersionLine | 5.3 |

## Bench: the Air, darwin/arm64, 8 cores

MacBook Air (M-series, 8 cores, go1.27.1), one package at a time at `nice -n 10`, no
pinning -- darwin has no `taskset` and the scheduler is the machine's -- 2026-09-18 at
dev `0edc81b7`, load average 2.3 at the start and 3.7 at the end, both self-hosted
runners idle throughout. Seven minutes for the 59 packages.

Not the budget bench, and not a candidate to be one: it is a laptop, and its numbers
are here to answer what the same suite costs where a process spawn is dear.

**The whole suite costs 2.2x what it does on Space** (about 380 s against 170 s), and
the cost is not spread evenly -- it lands almost entirely on the packages that spawn
git and child processes, which is the same shape the 2026-09-15 note recorded for the
Studio. `internal/*` packages that only compute are within noise of the Linux bench.

**`cmd/nova-wake` is over budget here at 62.9 s**, against 12.4 s on Space -- a 5.1x
ratio, the worst in the file, and the only package over 60 s on any recorded bench. It
is not enforced (this is not the budget bench) and it is not a red on this change, but
it is the package to look at first if darwin ever becomes a bench that gates anything:
five of its tests are over 5 s and every one of them is a real `exec`.

| package | total seconds | slowest test |
| --- | --- | --- |
| cmd/nova-wake | 62.9 | TestTheRecoveryReadsTheCountAndNeverAConstant 7.5 |
| cmd/nova-merge | 51.4 | TestTwoReadsAndAGateFromOneStartingBranchReachTheCoordinator 6.5 |
| cmd/nova-bus | 47.2 | TestRetryAfterAPartialResumesAtNext 11.4 |
| cmd/nova-swarm | 34.8 | - |
| internal/swarm | 22.0 | - |
| cmd/nova-pulse | 20.0 | TestBenchStandardDriftNamesBinary 7.6 |
| internal/bus | 18.9 | - |
| cmd/nova-review | 16.6 | - |
| internal/ci | 14.2 | TestEveryToolPrintsTheOneVersionLine 5.6 |
| internal/update | 13.8 | - |
| internal/pulse | 13.4 | - |
| cmd/nova-secrets | 6.6 | - |
| cmd/nova-sandbox | 6.1 | - |
| cmd/nova-board | 5.9 | - |
| internal/merge | 5.6 | - |
| cmd/nova-tokens | 4.3 | - |
| internal/review | 3.9 | - |
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
| internal/chat | 0.4 | - |
| internal/check | 0.4 | - |
| internal/docs | 0.4 | - |
| cmd/nova-work | 0.4 | - |
| internal/board | 0.4 | - |
| internal/records | 0.4 | - |
| cmd/nova-decide | 0.3 | - |
| cmd/nova-update | 0.3 | - |
| internal/buildinfo | 0.3 | - |
| internal/bounded | 0.3 | - |
| internal/decide | 0.3 | - |
| cmd/nova-post | 0.3 | - |
| internal/fuse | 0.3 | - |
| internal/sandbox | 0.3 | - |
| internal/memindex | 0.3 | - |
| internal/safepath | 0.3 | - |
| internal/friends | 0.3 | - |
| internal/oneline/audit | 0.3 | - |
| internal/fleet | 0.2 | - |
| internal/ci/slowtests | 0.2 | - |
| internal/dispatch | 0.2 | - |
| internal/worklang | 0.2 | - |
| internal/selftalk | 0.2 | - |
| internal/oneline | 0.2 | - |
| internal/jobs | 0.2 | - |
| internal/log | 0.2 | - |
| internal/goenv | 0.2 | - |
| internal/specwork | 0.2 | - |
| cmd/nova-ci | 0.2 | - |
| tools/testdur | 0.2 | - |

### The five biggest, against Space

The ratio is the reading, not the absolute number: a package whose darwin cost is close
to its Linux cost is compute, and one several times over is paying for process spawns.

| package | darwin/arm64 | linux/amd64 | ratio |
| --- | --- | --- | --- |
| cmd/nova-wake | 62.9 | 12.4 | 5.1x |
| cmd/nova-merge | 51.4 | 16.7 | 3.1x |
| cmd/nova-bus | 47.2 | 30.4 | 1.6x |
| cmd/nova-swarm | 34.8 | 16.8 | 2.1x |
| internal/swarm | 22.0 | 12.7 | 1.7x |

`cmd/nova-pulse` is the outlier worth naming separately: 20.0 s here against 0.5 s on
Space, a 40x ratio, almost all of it in `TestBenchStandardDriftNamesBinary` at 7.6 s.

### Tests over five seconds

Twenty-one on this bench against one on Space, and none of them is a wait: every one is
a package that shells out. They are listed rather than tagged because the tag is a
property of the TEST, and none of these is over the line on the budget bench -- a
`//go:build slow` added for a laptop would take coverage away from CI.

| package | test | seconds |
| --- | --- | --- |
| cmd/nova-bus | TestRetryAfterAPartialResumesAtNext | 11.4 |
| cmd/nova-bus | TestSnapshotTokenValidationAndBound | 8.7 |
| cmd/nova-bus | TestTwoClonesOfOneLaneRacingTenRoundsAllLand | 8.0 |
| cmd/nova-bus | TestEarlierGapSurvivesLaterPages | 7.7 |
| cmd/nova-pulse | TestBenchStandardDriftNamesBinary | 7.6 |
| cmd/nova-wake | TestTheRecoveryReadsTheCountAndNeverAConstant | 7.5 |
| cmd/nova-bus | TestBodiesModeCapsTheNewSummaryLinesToo | 7.1 |
| cmd/nova-merge | TestTwoReadsAndAGateFromOneStartingBranchReachTheCoordinator | 6.5 |
| cmd/nova-merge | TestThePacketIsPointersNotDiff | 6.5 |
| cmd/nova-bus | TestBrokenOutputCannotAcknowledgeUnprintedBodies | 6.0 |
| cmd/nova-merge | TestAHoldBeatsThreeApproves | 5.9 |
| cmd/nova-merge | TestAPacketIsHandedOverCorrectlyWhileAFlushRuns | 5.9 |
| internal/ci | TestEveryToolPrintsTheOneVersionLine | 5.6 |
| cmd/nova-bus | TestBodiesWithoutAdvanceMovesNoCursor | 5.6 |
| cmd/nova-merge | TestAStaleApproveIsKeptCountedAndAuthorizesNothing | 5.5 |
| cmd/nova-bus | TestContinuationSurvivesOrdinaryCursorAdvance | 5.4 |
| cmd/nova-wake | TestTheRealBusAdvanceRecoversAnInterruptedRead | 5.3 |
| cmd/nova-bus | TestTwoNotesInOneCommitWithMaxNotesOneLosesNeither | 5.3 |
| cmd/nova-bus | TestRetryAfterAPartialResumesAtNext/WithAdvance | 5.2 |
| cmd/nova-wake | TestTheBoundedCorrelationRead | 5.0 |
| cmd/nova-bus | TestInboxParsesOnlyWhatIsNewSinceTheCursor | 5.0 |

### One package does not pass on darwin

`cmd/nova-swarm`'s `TestBenchProbeNeverReadsAuth` FAILS on this bench, and it is a
defect in the test rather than in the tool. The test asserts that the probe stats the
auth file and never reads it, by scanning each logged remote command for `cat`, `head`
or `cp` anywhere in the LINE -- and on darwin `$TMPDIR` is `/var/folders/<two>/<random>`,
so a temporary directory called `dgdj_cpn55177y7hyx0qyx_r0000gn` carries `cp` inside it
and the probe's own `stat -c %a <that path>` matches. Only macOS can produce it, because
only macOS puts a random string in the temporary path. The check wants the COMMAND token,
not the line. The totals above are otherwise a clean run; this package's 34.8 s includes
the failing test's 1.25 s.
