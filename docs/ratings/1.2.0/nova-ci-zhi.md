# nova-ci READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card; the rating is this worker's, not the friend's
Build: 193a6f8002d0
READ: 7/10
USE: 7/10

No v1.2.0 tag exists yet. The build rated is the head of sprint/mechanical-2026-10-02, built and run on a Linux bench in a scratch directory: a throwaway Go module with a 2.1 s test, a SLEEPS skip, a red test and a functional-tagged test, and a throwaway copy of the checkout for the scaffolders. No live store, no server.

## Reasons

READ. The banner says what the tool is in one line, splits the verbs into the two that work in any Go module and the five that need this checkout, and gives each verb an effect word and its own exit-code row. Each `-h` quotes the banner's paragraph for that verb, its flags, its exit row and an `effect:` line. Refusals name every problem in one line and end with `run:`. The spec's SLEEPS exit code now agrees with the code (docs/SPEC-CI.md:270, exit 1).

What costs the score. `bench run -h` and `bench -h` print the whole banner inside their `exit codes:` line (cmd/nova-ci/bench.go:35 passes `exitTable("bench run")`, a full banner, where the tool skeleton wants one line), so the one verb with a remote effect has the noisiest help. The slowtests paragraph says what `--max` does twice, back to back (cmd/nova-ci/main.go:66 and :70). The banner says "the commands under example: are what runs" for your own module, and its examples are only `--example` runs: the one pipeline a stranger needs, `go test -json ./... | nova-ci slowtests`, and the warning that slowtests judges time and not pass or fail, are in docs/CLI.md:2192-2199 and not in the binary. The help names "the SLEEPS marker" and never says it is the literal `SLEEPS:` at the start of a skip message. It says an allowlist `where` is "run<id> or a bench", and a bench name is refused. Flag placeholders say `<string>` for files and addresses, and no flag line shows its default (`--budget` 60 is only in prose). `functional -h` has no example; `slowtests -h` prints its examples with no `example:` label. The README still installs v1.0.0 (README.md:54-59).

A 10 is help where every verb's exit line is one line, the pipeline and the time-not-pass caveat are on the banner, and every word a stranger must type (the marker, the `where`, the defaults) is printed.

USE. The first run lands: `slowtests --example --budget 60 --load 4 --cpus 16` prints a CI-SLOW and a CI-LOAD line, exit 0. On the scratch module, `go test -json -count=1 ./... | nova-ci slowtests` with `--budget 1`, `--test-budget 1`, `--enforce`, `--max 1` and `--json` each did what the help says, and `--enforce` turned the same lines into exit 1. An allowlist row `internal/slow TestSlow 5 2.1s@run1` cleared the test; a row of 50 s was refused as over three times its measurement. Garbage on stdin, plain `go test` output, `--budget 0`, `--budget 1.5`, `--load NaN`, a missing allowlist, an unknown flag, a positional argument, and a receipt with four bad fields were each refused in one line naming every problem. `functional ./...` named the functional test and its `-run` pattern. `new-rule` and `new-verb` with `--dry-run` listed their files, wrote them without it, refused a second run and a bad name, and `new-verb` printed the dispatch case to add. `local --dry-run` printed the package selection and the make line. `github receipt --dry-run` printed the line with `ev=-`.

What costs the score. A run with a red test and its SLEEPS skip ledgered prints `CI-SLOW OK packages=2` and exits 0; an empty stdin prints `CI-SLOW OK packages=0` and exits 0, so a `go test` that never started passes. The SLEEPS ledger and the allowlist want the package as `internal/slow`, the output prints `example.com/scratch/internal/slow`, and the refusal for the printed form says the package "must be the full module-relative path", which is what it was given. The CI-SLEEPS line escapes its own prose: `not on the\x20SLEEPS\x20ledger\x20(no\x20--sleeps\x20given)`. An allowlist `where` other than `run<id>` must be a runner label from this repository's .github/workflows/ci.yml, and in another module the refusal lists them as `()`. `new-rule --root <a module that is not nova-tools>` wrote three files into it, where the help says a non-checkout is exit 2. `bench run` with `NOVA_TEST_NO_HOST=1` set, which `bench run` itself sets on the bench, panics with a stack trace instead of refusing. `bench run` refuses as `BENCH-RUN REFUSED` and bare `bench` as `CI REFUSED`, beside `nova-ci <verb> REFUSED` everywhere else, its missing-flag remedy is `nova-ci help`, and a host it skipped for the fallback is reported as `CI BENCH PASSED`. A receipt against a closed port prints four go-redis pool log lines before its one FAILED line. `local --dry-run` against origin/dev listed 88 packages twice with no cap. `version` prints `v1.0.1-0.20261006150140-193a6f8002d0`.

Not tried: `bench run` reaching a bench (localhost refused the host key, and no other bench was used for it), `local` without `--dry-run` (it runs the unit tier), a receipt write to a real store, and building the scaffolded files.

A 10 keeps this slowtests loop and its refusals, prints the package form the ledgers want, refuses an empty stream, and makes `bench run` refuse like the other verbs.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-ci/bench.go:35 | `bench run -h` and `bench -h` print the whole banner after `exit codes:`: `exitTable("bench run")` returns the full banner and the tool skeleton prints it as one exit line. | Pass only the `bench run:` row (and its continuation lines) as ExitTable, and add a test that `bench run -h` does not contain the banner's first line. | S |
| 2 | cmd/nova-ci/main.go:66 | The slowtests paragraph states the `--max` cap twice (lines 66-69 and 70-72). | Delete the second statement. | S |
| 3 | cmd/nova-ci/main.go:145 | The banner says the commands under `example:` are what runs in your own module, and every example is an `--example` run; the pipeline and the "checks timing, not pass or fail" caveat live only in docs/CLI.md:2192-2199. | Add `go test -json ./... \| nova-ci slowtests --budget 60` to `example:` and one banner line saying to check the test run's exit status separately. | S |
| 4 | internal/ci/slowtests/slowtests.go:130 | The help says "the SLEEPS marker" and never says the marker is the literal `SLEEPS:` that starts the skip message. | Say on `slowtests -h`: a test skipped with `t.Skip("SLEEPS: <why>")`. | S |
| 5 | internal/ci/slowtests/slowtests.go:258 | An allowlist `where` that is not `run<id>` must be a runner label read from this repository's .github/workflows/ci.yml; the help says "run<id> or a bench", a bench name is refused, and in another module the refusal lists the labels as `()`. | Say on `slowtests -h` that `where` is `run<id>` or a runner label in .github/workflows/ci.yml, and when that file is absent say so in the refusal instead of `()`. | S |
| 6 | internal/ci/slowtests/slowtests.go:559 | The CI-SLEEPS line passes its own prose through `oneline.Field`, printing `not on the\x20SLEEPS\x20ledger\x20(no\x20--sleeps\x20given)`. | Escape only the values (test, package, ledger path), not the sentence around them. | S |
| 7 | internal/ci/slowtests/slowtests.go:408 | The SLEEPS ledger and the allowlist take `internal/slow`; the CI-SLEEPS and CI-SLOW lines print `example.com/scratch/internal/slow`; the full form is refused as "must be the full module-relative path". | Print the module-relative path the ledgers take (or accept both), and make the refusal say `want internal/slow, the path relative to the module`. | M |
| 8 | cmd/nova-ci/slowtests.go:111 | An empty stdin prints `CI-SLOW OK packages=0 slowest=none`, exit 0: a `go test` that never ran passes the check. | Refuse, exit 2, a stream with no TestEvent, as garbage on stdin is refused. | S |
| 9 | cmd/nova-ci/newrule.go:14 | `new-rule --root <a Go module that is not nova-tools> x-rule` wrote internal/ci/x-rule_class_test.go, its fixture and make/rule_x-rule.mk; the exit row says not a checkout is exit 2. | Check the root's go.mod module path is nova-tools before writing, and refuse otherwise. | S |
| 10 | internal/tool/tool.go:323 | With `NOVA_TEST_NO_HOST=1` in the environment (which `bench run` sets on the bench), `bench run --host <h>` panics with a stack trace from the test guard instead of refusing. | Recover the guard's panic in the verb and refuse in one line naming the variable, or read the guard only under `go test`. | S |
| 11 | internal/tool/tool.go:619 | `bench run` refuses as `BENCH-RUN REFUSED:` and bare `bench` as `CI REFUSED:`, beside `nova-ci <verb> REFUSED:` from every other verb; a missing `--host` or a bad `--dir` ends `run: nova-ci help`, not `run: nova-ci bench run -h`. | Use the `nova-ci bench run` prefix and the verb's own help as the remedy. | S |
| 12 | internal/bench/bench.go:199 | A host skipped for the fallback is reported as `CI BENCH PASSED host=... reason=...`; PASSED reads as success. | Name it `CI BENCH NOTE skipped host=... next=...`. | S |
| 13 | cmd/nova-ci/receipt.go:53 | A write to a closed port prints four `redis: ... pool.go:762` log lines on stderr before the one `nova-ci github receipt FAILED` line. | Silence the go-redis logger in the verb so the FAILED line is the only line. | S |
| 14 | cmd/nova-ci/local.go:148 | `local --dry-run` against origin/dev prints all 88 packages on the selection line and again on the make line, with no `--max`. | Cap the listing with a MORE line and print the make line with `PKGS=<n packages>`. | S |
| 15 | internal/nsprint/verbflag/verbflag.go:327 | The `-h` flag lines show `<string>` for files and addresses (`--allowlist`, `--sleeps`, `--redis`, `--root`, `--dir`) and no flag line shows its default; `--budget`'s 60 is only in the banner prose. | Name placeholders `<file>`, `<host:port>`, `<dir>` and print each flag's default. | S |
| 16 | cmd/nova-ci/functional.go:22 | `functional -h` has no example, and the printed package form follows the input (`./internal/slow` for a directory, `internal/slow` for `./...`). | Add an example and print one form. | S |
| 17 | `nova-ci slowtests -h` | The help still does not state the allowlist bound the code enforces (a row's budget between its measurement and three times it); it is learned from the refusal. | Add one line naming the bound. | S |
| 18 | `nova-ci slowtests --json` | The JSON facts carry load and cpus but not the per-cpu figure the CI-LOAD line prints. | Add per_cpu to the facts. | S |
| 19 | README.md:54 | The README still says "the Nova Tools 1.0.0 commands" and installs `@v1.0.0`. | Point at the latest release. | S |
| 20 | cmd/nova-ci/version.go:25 | `version` on the 1.2.0 candidate prints `v1.0.1-0.20261006150140-193a6f8002d0`, a pseudo-version that names neither 1.1 nor 1.2. | When unstamped, print `unreleased <sha12>` and the last release. | S |

## Good, keep

The refusal line that names every problem at once (four bad receipt fields in one line) and lists the flags or verbs after an unknown one.

`slowtests --example` as a first run with nothing to set up, and `--enforce` as the one switch between a measurement and a verdict, with the same lines either way.

The allowlist's three-times bound, refused with both numbers, and the `--json` object carrying the same verdict and `why` as the lines.

`--dry-run` on `new-rule`, `new-verb`, `local` and `github receipt`, each ending with a NOTE that says nothing was written and what to run next; `new-verb` printing the dispatch case it will not edit.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the spec says an unledgered SLEEPS skip exits 2 | FIXED | docs/SPEC-CI.md:270 says exit 1; the scratch run with an unledgered skip exited 1 |
| the README installs 1.0.0 | STILL THERE | README.md:54-59 `Nova Tools 1.0.0`, `@v1.0.0` |
| timing.go is a second entry point that is not a verb | STILL THERE | cmd/nova-ci/timing.go:1 `//go:build ignore`, run as `go run cmd/nova-ci/timing.go` (line 23) |
| the allowlist's three-times bound is not in the help | STILL THERE | `slowtests -h` has no bound; a 50 s row for a 2.1 s test is refused `budget 50s is not between its measurement 2.1s and 3 times it` |
| the JSON facts omit per-cpu | STILL THERE | `slowtests --example --budget 60 --json` facts: `load`, `cpus`, no per-cpu |
| the functional -run pattern is unbounded | STILL THERE | `functional` has no `--max`; `local --dry-run` likewise lists 88 packages uncapped |
| the refusal grammar names every problem with a remedy | STILL THERE (good) | `github receipt` with four bad fields refused in one line naming all four |
