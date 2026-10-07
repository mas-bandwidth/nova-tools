# nova-ci READ and USE rating, nova-tools 1.2.0

Rater: GLM (z-ai/glm-5.3-flash), harness opencode
Build: c76fcb249cc1
READ: 7/10
USE: 7/10

## Reasons

The build is the sprint base tip c76fcb249cc1, built from source on a Linux
bench (`nova-ci v1.0.1-0.20261007015429-c76fcb249cc1`); no v1.2.0 tag exists on
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
the dispatch). What keeps it from 10: the slowtests help states its `--max`
rule twice back to back (finding 1); `nova-ci bench -h` and `bench run -h`
paste the entire tool banner after `exit codes:` and bury the verb's own codes
at line 121 of 131 (finding 2); github receipt is a store write in the banner
and a delivery in its effect line (finding 3); seven of nine verbs still run
on a hand-rolled dispatch beside internal/tool's (finding 4); docs/TESTS.md
still names the wrong exit for `--enforce` (finding 5); the nova-ci section of
docs/CLI.md still opens without `### First run` (finding 6); an unknown verb
gets no nearest-name hint while an unknown flag does (finding 7); and the
README row still says "reports their timings" beside its own "test-time
budgets" (finding 8).

USE 7. slowtests did two real jobs end to end in the scratch module:
`go test -json -count=1 ./... | nova-ci slowtests --budget 1 --enforce` caught
the 1.2 s test as one line naming the package, the budget and the two slowest
tests, at exit 1; `--test-budget 1` turned the same stream into two per-test
lines at exit 0; `--max 1` cut the findings and printed the `MORE shown=1
total=2` line; `--json` carried the same verdict as one object; a malformed
line was refused naming its line number, an empty stream said `looked at
nothing` and named `--allow-empty`. The scaffolds wrote for real in a copy of
the checkout: both make targets ran through `-include make/*.mk` and passed out
of the box, and every refusal measured named every problem at once with a
remedy that runs — `github receipt` with no flags names seven missing fields in
one line. What keeps it from 10: the allowlist and `--sleeps` ledgers are
unusable in any module but this one, under a banner that claims "usage, in any
Go module" (finding 9); the CI-LOAD line's per-cpu figure is still missing from
the JSON facts (finding 10); the pair `functional` prints pastes into
`go test`, which answers 0 while running nothing, because the tag is not on the
line (finding 11); and in this checkout that pattern is one argument of 28,419
characters naming 683 tests (finding 12).

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-ci/main.go:73 | the slowtests help states `--max` twice back to back: "A run with more finding lines than --max prints the first --max and one CI-SLOW MORE shown=\<n\> total=\<n\> line..." (:69) and then "--max prints at most that many finding lines, then one CI-SLOW MORE shown=\<n\> total=\<n\> line..." (:73), so `nova-ci slowtests -h` reads one rule as two | delete the second sentence | S |
| 2 | cmd/nova-ci/bench.go:35 | `ExitTable` is handed `exitTable("bench run")`, which is the whole banner, the paragraph and the example (main.go:195 returns head + rows + tail), where internal/tool's field wants the bare paragraph (internal/tool/tool.go:56); `nova-ci bench -h` and `bench run -h` therefore print `exit codes:` followed by the entire tool banner (131 lines, tool.go:297 and :357 paste the string whole) and the verb's own codes sit at line 121 | pass only the exit rows: the head line plus the verb's own lines, without the banner or the example | S |
| 3 | cmd/nova-ci/main.go:169 | github receipt is classified two ways: the banner block says `(store write)` (main.go:120) while its effect line says `delivery: writes one row of a CI run to a Redis store`; the standard's classes are inspection, local write, store write and external delivery, and writing one row to the store at `--redis` is the second | make both say store write | S |
| 4 | cmd/nova-ci/main.go:244 | seven of nine verbs still run on the hand-rolled dispatch, refusal printer and exit-table extraction beside internal/tool's copies; only `bench run` rides the skeleton, and the reason written for that (bench.go:9) is nowhere at the switch that keeps the others off it | write the reason at the switch in one line, or move the verbs onto internal/tool | M |
| 5 | docs/TESTS.md:958 | the first-run transcript says "only \`--enforce\` makes it exit 2"; measured, `--enforce` with a CI-SLOW line exits 1 (the check said no) and 2 is input it could not read, as the banner's own table says | change "exit 2" to "exit 1" | S |
| 6 | docs/CLI.md:2267 | the nova-ci section (2267 to 2405) still opens without `### First run`; the neighbours open with one, and the section holds only `### bench run` and `### github receipt` | add the `### First run`: the two example lines and how to read CI-SLOW and CI-LOAD | S |
| 7 | cmd/nova-ci/main.go:268 | `nova-ci helpp` names no nearest verb, though an unknown flag does (`--budg` yields "did you mean --budget?") | name the nearest verb the same way an unknown flag does | S |
| 8 | README.md:43 | the row's prose still says "Reads Go test events ... and reports their timings" beside the row's own "test-time budgets", the measure-or-gate ambiguity the 1.1.0 read named; the truth is a measurement that `--enforce` alone turns into a gate | say the verdict in the prose: printed and measured everywhere, a failure only under `--enforce` | S |
| 9 | internal/ci/slowtests/slowtests.go:408 | the `--allowlist` and `--sleeps` ledgers are unusable in any module but this one: the package column must start `cmd/`, `internal/` or `tools/` (a scratch module's `example.com/scratch` row is refused with "must be the full module-relative path", a remedy no foreign module can follow), and a bench `where` must be a label the `.github/workflows/ci.yml` of the working directory names (slowtests.go:346; away from the checkout the refusal prints an empty list, `names ()`), while the banner claims "usage, in any Go module (no state, no store)" | accept the import path the events already carry (matches() compares by suffix), and say in the help that bench labels come from this repository's ci.yml | M |
| 10 | cmd/nova-ci/slowtests.go:385 | the `--json` facts carry load and cpus but not the per-cpu figure the CI-LOAD line prints (`per-cpu=0.63` on the line, absent from facts), so the two renderings of one run drift and a JSON consumer recomputes it | add per-cpu to the facts beside load and cpus | S |
| 11 | cmd/nova-ci/functional.go:61 | the two lines `functional` prints paste into `go test` and answer 0 while running nothing: `go test -count=1 -run '^(TestFunctionalThing)$' ./store/` prints `? example.com/scratch/store [no test files]` at exit 0, because the tests it named are built only under `-tags functional` and the tag is on no line it prints | print the whole command with the tag as the next command, or a NOTE saying the tests need `-tags functional` | S |
| 12 | cmd/nova-ci/functional.go:61 | `functional ./...` in this checkout prints one `-run` argument of 28,419 characters naming 683 tests that an AI must pass to `go test` without reading it; nothing bounds it or says what it is | print a NOTE naming the count, or bound and split the pattern | S |

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
| `functional` prints a package name `go test` rejects (USE 1.1.0) | FIXED | from the scratch module it printed `./store/`; `go test -tags functional -count=1 -run '^(TestFunctionalThing)$' ./store/` exits 0; the pair still runs nothing without the tag (finding 11) |
| the allowlist row's bound is not in the help (USE 1.1.0) | FIXED | `nova-ci slowtests -h` says "bound: budget sits between its measurement and three times it, the 3x headroom ceiling" |
| only slowtests takes `--json`; five output dialects (READ 1.1.0) | STILL THERE | `local`, `functional`, `new-rule`, `new-verb` and `github receipt` -h list no `--json`; their lines lead `nova-ci local: base=...`, `./store/`, `wrote ...`, `CI RECEIPT ...` |
| a leftover script with no written reason (READ 1.1.0) | STILL THERE, the reason now written | cmd/nova-ci/timing.go:1 is still `//go:build ignore`, its purpose and run line now at the top and TestTimingTableReproduces behind it; the banner and docs/CLI.md:2267 still do not name it |
| the dispatch stands off the skeleton with no reason at the switch (READ 1.1.0) | PARTLY | `bench run` is on internal/tool now (bench.go:9), the first verb to move; the other seven verbs still run on the hand-rolled switch (finding 4) |
| docs/TESTS.md names the wrong exit for `--enforce` (READ 1.1.0) | STILL THERE | docs/TESTS.md:958, now "makes it exit 2"; measured exit 1 (finding 5) |
| docs/CLI.md has no First run for nova-ci (READ 1.1.0, both raters) | STILL THERE | docs/CLI.md:2267 (finding 6) |
| an unknown verb names no nearest (USE 1.1.0) | STILL THERE | `nova-ci helpp` lists the verbs only; `--budg` yields "did you mean --budget?" (finding 7) |
| per-cpu is not in the JSON facts (USE 1.1.0) | STILL THERE | the line prints `per-cpu=0.63`, the facts carry load and cpus only (finding 10) |
| the functional `-run` pattern is one huge argument (USE 1.1.0) | STILL THERE, smaller | 683 names, 28,419 characters, from `nova-ci functional ./...` in this checkout (finding 12) |
| the README row fights itself over measure or gate (READ 1.1.0) | STILL THERE | README.md:43, "reports their timings" beside "test-time budgets" (finding 8) |
