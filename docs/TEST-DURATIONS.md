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


**The bench:** Space (x64 Linux, 16 cores), one package at a time pinned to one quiet
core with `taskset -c 12`, 2026-09-15, load average 15-18 on the other fifteen. Name the
bench when you regenerate: the same suite on the Studio (macOS) costs three to four
times this, almost all of it system time -- `cmd/nova-bus` is 38 s here and 133 s there,
for 813 s of `sys` across some eight hundred git and process spawns. The budget below is
this bench's.

| package | total seconds | slowest test |
| --- | --- | --- |
| cmd/nova-bus | 38.5 | - |
| cmd/nova-merge | 33.2 | TestAPassWhoseOwnStateWriteIsRefusedIsNotSilent 5.1 |
| cmd/nova-swarm | 29.2 | - |
| internal/swarm | 18.1 | - |
| cmd/nova-wake | 16.7 | - |
| internal/bus | 8.2 | - |
| internal/update | 5.0 | - |
| internal/ci | 4.5 | - |
| cmd/nova-tokens | 2.6 | - |
| internal/merge | 2.1 | - |
| cmd/nova-board | 2.1 | - |
| cmd/nova-review | 1.7 | - |
| cmd/nova-secrets | 1.1 | - |
| cmd/nova-fuse | 0.8 | - |
| internal/wake | 0.7 | - |
| cmd/nova-self-talk | 0.5 | - |
| cmd/nova-check | 0.3 | - |
| internal/tokens | 0.2 | - |
| cmd/nova-memory | 0.2 | - |
| internal/records | 0.2 | - |
| internal/check | 0.1 | - |
| internal/pulse | 0.1 | - |
| cmd/nova-sandbox | 0.0 | - |
| internal/board | 0.0 | - |
| internal/memindex | 0.0 | - |
| internal/secrets | 0.0 | - |
| internal/sandbox | 0.0 | - |
| cmd/nova-update | 0.0 | - |
| cmd/nova-version | 0.0 | - |
| internal/fuse | 0.0 | - |
| cmd/nova-pulse | 0.0 | - |
| internal/oneline | 0.0 | - |
| internal/selftalk | 0.0 | - |
| internal/review | 0.0 | - |
| internal/bounded | 0.0 | - |
| internal/buildinfo | 0.0 | - |
| tools/testdur | 0.0 | - |

## Tests over five seconds

Both are a tenth of a second over the line, their cost is real git rather than a wait,
and `cmd/nova-merge` sits at half its budget: they are listed here rather than tagged,
and the tag is the answer if either grows.

| package | test | seconds |
| --- | --- | --- |
| cmd/nova-merge | TestAPassWhoseOwnStateWriteIsRefusedIsNotSilent | 5.1 |
| cmd/nova-merge | TestAFoldWhoseStateWriteIsRefusedIsNotSilent | 5.1 |
