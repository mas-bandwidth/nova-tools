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


**The bench:** Space (x64 Linux, 16 cores), one package at a time pinned to one core
with `taskset -c 12` and `nice -n 10`, 2026-09-18 at dev `53023587`, load average 3.0
falling to 1.2 on the other fifteen. Name the bench AND its load when you regenerate:
the recording this one replaces (2026-09-15) was taken on the same core with load
15-18 beside it, and a quiet bench is the whole difference between `cmd/nova-bus` at
38.5 s and at 30.4 s. The same suite on the Studio (macOS) costs three to four times this,
almost all of it system time, across some eight hundred git and process spawns. The
budget below is this bench's.

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

## Tests over five seconds

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
