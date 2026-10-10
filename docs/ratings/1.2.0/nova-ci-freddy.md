# nova-ci READ and USE rating, nova-tools 1.2.0

Rater: friend (deepseek-v4.1-flash), harness dsh
Build: e49a14260c88
READ: 7/10
USE: 7/10

## Reasons

The build is the sprint base tip e49a14260c88, built from source on a Linux
bench (`nova-ci v1.0.1-0.20261007201537-e49a14260c88`); no v1.2.0 tag exists on
the remote yet. Read cold from `nova-ci help`, every verb's `-h` (including the
groups `bench` and `github` and their verbs) and docs/SPEC-CI.md, then used in
a throwaway directory: a scratch Go module with a 1.2 s test, a sleeps-marked
test and a functional-tagged test, plus the nova-tools checkout itself for the
checkout-bound verbs. No store was started (the card forbids a server on the
machine), so `github receipt` ran `--dry-run` only and `bench run` met only an
unanswerable bench; `local` was used through `--dry-run`, since a real run is
145 packages.

READ 7. The banner answers the three questions in the house shape, groups its
usage by what a verb needs (any module, a checkout, a store), and its example
block runs as printed — the first run needs nothing but the binary. Every verb
answers `-h` at exit 0 with flags, its effect line and its exit codes, and the
scaffolds' help is honest about what they do not do (`new-verb never edits`
the dispatch). What keeps it from 10: `nova-ci bench -h` and `bench run -h`
paste the entire tool banner after `exit codes:` (the banner is 126 lines; the
verb's own codes end at line 104 and line 119) (finding 1); github receipt is a
store write in the banner and a delivery in its effect line (finding 2); every
verb but `bench run` still runs on a hand-rolled dispatch beside pkg/tool's
(finding 3); docs/TESTS.md still names the wrong exit for `--enforce`
(finding 4); the nova-ci section of docs/CLI.md still opens without
`### First run` (finding 5); an unknown verb gets no nearest-name hint while an
unknown flag does (finding 6); and the README row still says "reports their
timings" beside its own "test-time budgets" (finding 7).

USE 7. slowtests did two real jobs end to end in the scratch module:
`go test -json -count=1 ./... | nova-ci slowtests --budget 1 --enforce` caught
the 1.2 s test as one line naming the package, the budget and the two slowest
tests, at exit 1; `--test-budget 1` turned the same stream into two per-test
lines at exit 0; `--max 1` cut the findings and printed the `MORE shown=1 total=2` line; `--json` carried the same verdict as one object; a malformed
line was refused naming its line number, an empty stream said `looked at nothing` and named `--allow-empty`. The scaffolds wrote for real in a copy of
the checkout: both make targets ran through `-include make/*.mk` and passed out
of the box, and every refusal measured named every problem at once with a
remedy that runs — `github receipt` with no flags names seven missing fields in
one line. What keeps it from 10: the allowlist and `--sleeps` ledgers are
unusable in any module but this one, under a banner that claims "usage, in any
Go module" (finding 8); the CI-LOAD line's per-cpu figure is still missing from
the JSON facts (finding 9); the pair `functional` prints pastes into
`go test`, which answers 0 while running nothing, because the tag is not on the
line (finding 10); and in this checkout that pattern is one argument of 28,679
characters naming 689 tests (finding 11).

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-ci/bench.go:35 | `ExitTable` is handed `exitTable("bench run")`, which is the whole banner, the paragraph and the example (main.go:210 cuts `head` from `usage` at :211 and returns head + rows + tail), where pkg/tool's field wants the bare paragraph (pkg/tool/tool.go:56); `nova-ci bench -h` (114 lines) and `bench run -h` (130 lines) therefore print `exit codes:` followed by the entire 126-line banner, and the verb's own codes end at line 104 and line 119 | pass only the exit rows: the head line plus the verb's own lines, without the banner or the example | S |
| 2 | cmd/nova-ci/main.go:117 | github receipt is classified two ways: the banner block says `(store write)` (main.go:117) while its effect line says `delivery: writes one row of a CI run to a Redis store` (`github receipt -h` line 28, from main.go:184); the standard's classes are inspection, local write, store write and external delivery, and writing one row to the store at `--redis` is the second | make both say store write | S |
| 3 | cmd/nova-ci/main.go:259 | every verb but `bench run` still runs on the hand-rolled dispatch (`switch args[0]` at main.go:259), refusal printer and exit-table extraction beside pkg/tool's copies; only `bench run` rides the skeleton, and the reason written for that (bench.go:3-8) is nowhere at the switch that keeps the others off it | write the reason at the switch in one line, or move the verbs onto pkg/tool | M |
| 4 | docs/TESTS.md:958 | the first-run transcript says "only `--enforce` makes it exit 2"; measured, `--enforce` with a CI-SLOW line exits 1 (the check said no) and 2 is input it could not read, as the banner's own table says | change "exit 2" to "exit 1" | S |
| 5 | docs/CLI.md:2366 | the nova-ci section (2366 to 2504) still opens without `### First run` (docs/STANDARD.md section 3, point 3), holding only `### bench run` (2449) and `### github receipt` (2475) | add the `### First run`: the two example lines and how to read CI-SLOW and CI-LOAD | S |
| 6 | cmd/nova-ci/main.go:282 | `nova-ci helpp` names no nearest verb (`unknown verb "helpp"; the verbs are ...` at :282-283), though an unknown flag does (`--budg` yields "did you mean --budget?") | name the nearest verb the same way an unknown flag does | S |
| 7 | README.md:43 | the row's prose still says "Reads Go test events ... and reports their timings" beside the row's own "test-time budgets", the measure-or-gate ambiguity the 1.1.0 read named; the truth is a measurement that `--enforce` alone turns into a gate | say the verdict in the prose: printed and measured everywhere, a failure only under `--enforce` | S |
| 8 | internal/ci/slowtests/slowtests.go:439 | the `--allowlist` and `--sleeps` ledgers are unusable in any module but this one: the package column must start `cmd/`, `internal/` or `tools/` (the check at :440, refusing with "must be the full module-relative path" at :443), and a bench `where` must be a label the `.github/workflows/ci.yml` of the working directory names (refusal at :381), while the banner claims "usage, in any Go module (no state, no store)" | accept the import path the events already carry (`matches` compares by suffix at :448), and say in the help that bench labels come from this repository's ci.yml | M |
| 9 | cmd/nova-ci/slowtests.go:386 | the `--json` facts carry load and cpus (slowtests.go:386-392) but not the per-cpu figure the CI-LOAD line prints (`per-cpu=0.63` on the line, absent from facts), so the two renderings of one run drift and a JSON consumer recomputes it | add per-cpu to the facts beside load and cpus | S |
| 10 | cmd/nova-ci/functional.go:61 | the two lines `functional` prints paste into `go test` and answer 0 while running nothing: `go test -count=1 -run '^(TestRealThing)$' ./store/` prints `ok example.com/scratch/store [no tests to run]` at exit 0, because the tests it named are built only under `-tags functional` (internal/ci/functional/functional.go:117-130) and the tag is on no line it prints | print the whole command with the tag as the next command, or a NOTE saying the tests need `-tags functional` | S |
| 11 | cmd/nova-ci/functional.go:61 | `functional ./...` in this checkout prints one `-run` argument of 28,679 characters naming 689 tests that an AI must pass to `go test` without reading it; nothing bounds it or says what it is | print a NOTE naming the count, or bound and split the pattern | S |

## Good, keep

- slowtests is runnable from the binary alone: `--example` reads a built-in
  over-budget stream, so the first run shows a CI-SLOW finding and a CI-LOAD
  line with no module, no store and no pipe.
- A refusal names every problem at once and ends in a command that runs:
  `github receipt` with no flags lists seven missing fields, each with what it
  wants; `bench run` against an unanswerable bench names the ssh error and the
  remedy `ssh <host> true`, at exit 2 with no CI BENCH line, exactly as its
  help says to read the difference.
- The scaffolds write for real and their make targets run at once through
  `-include make/*.mk`: `new-rule frobnicate` laid down three files whose
  `test-rule-frobnicate` passed first try, and `new-verb` prints the dispatch
  snippet it deliberately does not edit.
- `--json` renders the same verdict as one object, refusals included (remedy
  and why in the object), and `--max` cuts a listing with a `MORE shown= total=`
  line that names the flag.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| `functional` prints a package name `go test` rejects (USE 1.1.0) | FIXED | from the scratch module it printed `./store/`; `go test -tags functional -count=1 -run '^(TestRealThing)$' ./store/` exits 0; the pair still runs nothing without the tag (finding 10) |
| the allowlist row's bound is not in the help (USE 1.1.0) | FIXED | `nova-ci slowtests -h` says "bound: budget sits between its measurement and three times it, the 3x headroom ceiling" |
| only slowtests takes `--json`; five output dialects (READ 1.1.0) | STILL THERE | `local`, `functional`, `new-rule`, `new-verb` and `github receipt` -h list no `--json`; their lines lead `nova-ci local: base=...`, `./store/`, `wrote ...`, `CI RECEIPT ...` |
| a leftover script with no written reason (READ 1.1.0) | STILL THERE, the reason now written | cmd/nova-ci/timing.go:1 is still `//go:build ignore`, its purpose and run line now at the top and TestTimingTableReproduces behind it; the banner and docs/CLI.md:2366 still do not name it |
| the dispatch stands off the skeleton with no reason at the switch (READ 1.1.0) | PARTLY | `bench run` is on pkg/tool now (bench.go:3-8), the first verb to move; every other verb still runs on the hand-rolled switch (finding 3) |
| docs/TESTS.md names the wrong exit for `--enforce` (READ 1.1.0) | STILL THERE | docs/TESTS.md:958, now "makes it exit 2"; measured exit 1 (finding 4) |
| docs/CLI.md has no First run for nova-ci (READ 1.1.0, both raters) | STILL THERE | docs/CLI.md:2366 (finding 5) |
| an unknown verb names no nearest (USE 1.1.0) | STILL THERE | `nova-ci helpp` lists the verbs only; `--budg` yields "did you mean --budget?" (finding 6) |
| per-cpu is not in the JSON facts (USE 1.1.0) | STILL THERE | the line prints `per-cpu=0.63`, the facts carry load and cpus only (finding 9) |
| the functional `-run` pattern is one huge argument (USE 1.1.0) | STILL THERE, smaller | 689 names, 28,679 characters, from `nova-ci functional ./...` in this checkout (finding 11) |
| the README row fights itself over measure or gate (READ 1.1.0) | STILL THERE | README.md:43, "reports their timings" beside "test-time budgets" (finding 7) |
