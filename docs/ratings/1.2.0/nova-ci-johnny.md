# nova-ci READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card; the rating is this worker's, not the friend's
Build: 193a6f8002d0
READ: 7/10
USE: 6.5/10

No v1.2.0 tag exists yet. The build rated is the head of sprint/mechanical-2026-10-02, built and run on a Linux bench in a scratch module (example.com/trial): no live store, no server, no real bench dial.

## Reasons

READ. The banner opens with one line saying what the tool is, splits the verbs into "in any Go module" and "in a nova-tools checkout", and gives every verb a `(inspection)`, `(local write)` or `(delivery)` tag and its own exit row. Every verb answers `-h` with its usage paragraph, its flags, its own exit codes and an `effect:` line. The slowtests paragraph now gives an allowlist row as a worked example and says plainly that a cached run never trips a package budget. docs/CLI.md now shows the own-module pipe, `go test -json ./cmd/mytool | nova-ci slowtests --budget 60`, and says the test run's exit has to be checked separately. Both were 1.1.0 findings. docs/SPEC-CI.md is now titled as the class-test spec and no longer passes class tests off as verbs.

What costs the score. `bench run -h` and `bench -h` print the whole banner where their exit codes should be (cmd/nova-ci/bench.go:40), so the one new verb has the worst help in the tool. The slowtests paragraph says `--max` twice in a row (cmd/nova-ci/main.go:65 and :70). The banner's first-run paragraph says "In your own module ... the commands under example: are what runs", but no example under `example:` reads your own module; the pipe that docs/CLI.md shows is not in the banner, and it is not in `slowtests -h` either. The verbs say "any Go module", but the allowlist and SLEEPS ledgers accept only packages under `cmd/`, `internal/` or `tools/`, and a bench label only if `.github/workflows/ci.yml` names it, and no help line says so. The spec's numbered red test still says exit 2 under `--enforce` (docs/SPEC-CI.md:297); the engine, the banner and docs/SPEC-CI.md:220 say 1. The receipt paragraph's "the password in the variable NOVA_SPRINT_REDIS_PASSWORD_ENV names" needs a second read. Flag placeholders are `<string>` for files.

A 10 would give every verb's `-h` only its own exit row, print the own-module pipe as the banner's second example with the caveat on the same line, say on `slowtests -h` which package paths and where-labels a ledger accepts, and keep the spec's exit numbers equal to the engine's.

USE. The banner's two examples run as written: at `--budget 60` they print one CI-SLOW line and CI-LOAD, exit 0, and at 120 they print `CI-SLOW OK packages=2`. On a real module I ran `go test -json -count=1` (with a 1.5 s test, a `SLEEPS:` skip and a `t.Fatal` test) and piped the output into slowtests. The unledgered skip printed CI-SLEEPS, exit 1. `--test-budget 1` printed CI-SLOW for the test. A good allowlist row (`run12`) cleared it under `--enforce`, and a 30 s row on a 1.5 s measurement was refused with the 3-times rule. `--package-budget 0.001 --test-budget 0.5 --max 1` printed one line and `CI-SLOW MORE kind=finding shown=1 total=2`. `--json` printed one object with typed items. Every refusal I tried was one line ending in a next command: garbage stdin (whose remedy is the pipe itself), an extra argument, a ledger row, a functional directory that does not exist, a functional flag, and seven missing receipt fields named at once. The `github receipt --dry-run` with good fields printed CI RECEIPT with `ev=-` and dialled nothing. `local --dry-run` in a nova-tools checkout printed its two packages and the exact make line. new-verb refused a tool with no `func main` and explained why.

What costs the score. The 1.1.0 traps are still there. With a `t.Fatal` test in the stream, `go test` exited 1 and slowtests printed `CI-SLOW OK packages=1` with exit 0 (pipestatus `1 0`). `functional ./...` still prints `store`, and `go test -tags functional -run '^(TestStoreRound)$' store` fails with `package store is not in std`. `new-rule --dry-run demo-rule` in a module that is not nova-tools still lists three files with exit 0. New in 1.2.0: the ledgers reject a stranger's layout. `example.com/trial	TestSleepy	...` was refused as "must be the full module-relative path", and a root-package test cannot be ledgered at all. A `where` of a bench name was refused against `ci.yml` with an empty list printed as `()`. With `NOVA_TEST_NO_HOST=1` exported (the environment the bench rule and the briefs set), `bench run` died with a Go panic and a 40-line stack trace saying "a test reached a host" instead of a REFUSED line. An empty stdin, for example from a pipe whose `go test` never started, prints `CI-SLOW OK packages=0`, exit 0. `bench run` uses a third refusal prefix (`BENCH-RUN REFUSED`, and `CI REFUSED` for a bare `bench`), its flag refusals point at `nova-ci help`, not `bench run -h`, and its fallback note `CI BENCH PASSED host=...` reads as a test pass when it means the host was passed over. The bad-flags line still names only `--nope` when `--budget -3 --load Inf extra` sit beside it. The CI-SLEEPS remedy still prints `the\x20SLEEPS\x20ledger`.

Not tried: a real `bench run` against an answering bench, a `local` run that runs tests, a scaffold write, and a receipt dial (no store, by the card's rules).

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-ci/bench.go:40 | `bench run -h` and `bench -h` print the whole banner after `exit codes:`. `exitTable` returns the banner with the verb's row spliced in, and `tool.Verb.ExitTable` prints it as if it were only the exit paragraph (also bench.go:35). | Pass only the exit lines (the table's first line plus the `bench run:` row) to `tool.Tool.ExitTable` and `tool.Verb.ExitTable`, and test that `bench run -h` contains `exit codes:` exactly once. | S |
| 2 | `go test -json ./internal/w \| nova-ci slowtests --sleeps s.txt` | With a `t.Fatal` test in the stream, go test exits 1 and slowtests prints `CI-SLOW OK packages=1`, exit 0. The caveat is in docs/CLI.md but not on the OK line or in the banner (carried from 1.1.0). | Count `"Action":"fail"` events and print `tests_failed=<n>` on the OK line, or the caveat on the banner's pipe example. | M |
| 3 | cmd/nova-ci/functional.go:57 | `functional ./...` prints `store`, and `go test -tags functional -run ... store` exits 1 (`package store is not in std`). `. ./store` prints `./store`, which works (carried from 1.1.0). | Print every directory with a `./` prefix. | S |
| 4 | internal/ci/slowtests/slowtests.go:404 | Allowlist and SLEEPS rows must start with `cmd/`, `internal/` or `tools/`. A row for `example.com/trial` (the root package) is refused, so a module laid out any other way cannot ledger a skip, although the banner says slowtests works in any Go module. | Accept the module's own import path or any module-relative path that matches an event's package, and say which forms are accepted on `slowtests -h`. | M |
| 5 | internal/ci/slowtests/slowtests.go:346 | An allowlist `where` that is not `run<id>` is checked against `.github/workflows/ci.yml`. In another module the refusal ends `names ()`, an empty list, and help never mentions ci.yml. | Say on `-h` that a where is run<id> or a runner label in ci.yml, and when no ci.yml exists, say so instead of printing `()`. | S |
| 6 | internal/bench/exec.go:55 | With `NOVA_TEST_NO_HOST=1` in the environment, `bench run --host x --dir . -- go version` panics with a 40-line stack trace ("a test reached a host through an unfaked seam"). The shipped binary runs the test guard on a real call. | Check the guard before dispatch in the binary and print one `REFUSED` line naming the variable, or arm the guard only under `go test`. | S |
| 7 | `nova-ci slowtests < /dev/null` | An empty stream prints `CI-SLOW OK packages=0 slowest=none`, exit 0, so a pipe whose go test never ran reads as green. | Print a NOTE (`no TestEvents read`) on an empty stream, or refuse it unless `--allow-empty` is given. | S |
| 8 | cmd/nova-ci/main.go:65 | The slowtests paragraph describes `--max` twice in a row (main.go:65 and :70), on the banner and on `-h`. | Delete the second sentence. | S |
| 9 | cmd/nova-ci/main.go:34 | The first-run paragraph says the own-module command is "under example:", and the example block (main.go:145) has only `--example` lines. | Add `go test -json -count=1 ./... \| nova-ci slowtests --budget 60` as the banner's own-module example, with the note that go test's exit is separate. | S |
| 10 | docs/SPEC-CI.md:297 | The numbered red test still says exit 2 under `--enforce`; docs/SPEC-CI.md:220, the banner and the engine say 1 (carried from 1.1.0). | Change it to 1. | S |
| 11 | docs/CLI.md:2184 | The nova-ci section still has no `### First run` heading, which its neighbours have (carried from 1.1.0). | Open the section with `### First run` and the two lines that run. | S |
| 12 | `nova-ci new-rule --dry-run demo-rule` | In the scratch module example.com/trial, it lists three files under internal/ci and make/ with exit 0. The checkout check is only that a go.mod exists (carried from 1.1.0). | Refuse a root whose go.mod module is not github.com/mas-bandwidth/nova-tools. | S |
| 13 | `nova-ci bench run`; `nova-ci bench` | A third and fourth refusal prefix: `BENCH-RUN REFUSED: ... run: nova-ci help` and `CI REFUSED: bench wants one of its verbs`. Every other verb prints `nova-ci <verb> REFUSED: ...; run: nova-ci <verb> -h`. | Make the skeleton print `nova-ci bench run REFUSED` with the remedy `nova-ci bench run -h`. | S |
| 14 | internal/bench/bench.go:199 | The fallback note reads `CI BENCH PASSED host=... next=...`; PASSED reads as a test result, and here it means the host was skipped. | Use `CI BENCH FALLBACK host=... reason=... next=...`. | S |
| 15 | `nova-ci slowtests --budget -3 --load Inf --nope extra` | Only `unknown flag --nope` is named; the bad budget, the Inf and the extra argument wait for later runs (carried from 1.1.0). | Collect every problem before refusing, as github receipt already does. | M |
| 16 | internal/ci/slowtests/slowtests.go:558 | The CI-SLEEPS remedy prints `not on the\x20SLEEPS\x20ledger\x20(no\x20--sleeps\x20given)` (carried from 1.1.0). | Escape only control characters in the remedy, not spaces. | S |
| 17 | `nova-ci slowtests --budget 5 --package-budget 3` | The usage shows `--budget <seconds> \| --package-budget <s>` as alternatives; giving both is accepted silently. | Refuse both together, or print the alternatives as `[--budget <s>] [--package-budget <s>]`. | S |
| 18 | cmd/nova-ci/main.go:120 | The receipt paragraph says "the password in the variable NOVA_SPRINT_REDIS_PASSWORD_ENV names", which is an indirection a reader has to parse twice. Flag placeholders are `<string>` for files (`--allowlist`, `--sleeps`). | Say "NOVA_SPRINT_REDIS_PASSWORD_ENV holds the name of the variable that holds the password", and use `<file>`. | S |
| 19 | cmd/nova-ci/version.go | Built from the 1.2.0 candidate, `version` prints `v1.0.1-0.20261006150140-193a6f8002d0`, a pseudo-version that names neither 1.1 nor 1.2. | When unstamped, print `unreleased <sha12>`, or stamp from the release. | S |

## Good, keep

The banner's examples print real lines with no module and no store, and the CI-LOAD line says outright that it is measured, not a verdict.

A slowtests refusal of garbage stdin names the line and gives the working pipe as its remedy: `run: go test -json <packages> | nova-ci slowtests --budget 60`.

`github receipt` names every bad field in one line (seven at once from empty flags), and `--dry-run` prints the exact line with `ev=-` and dials nothing.

The allowlist bound is enforced with a sentence a reader can act on ("budget 30s is not between its measurement 1.5s and 3 times it"), and `--max` caps findings with a MORE line naming the flag.

`local --dry-run` prints the selected packages and the exact make line, and `new-verb` refuses a tool with no `func main` and says why.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a red test still prints an OK line | STILL THERE | go test exited 1 (a `t.Fatal`); `nova-ci slowtests --sleeps s.txt` printed `CI-SLOW OK packages=1`, exit 0. docs/CLI.md now states the caveat; the banner and the OK line do not |
| functional ./... prints a path the runner rejects | STILL THERE | `nova-ci functional ./...` printed `store`; `go test -tags functional -run '^(TestStoreRound)$' store` exited 1, `package store is not in std` |
| new-rule accepts any root with a go.mod | STILL THERE | `new-rule --dry-run demo-rule` in example.com/trial listed three files, exit 0 |
| skip string and 3-times rule absent from -h; remedy escapes spaces | CHANGED | the SLEEPS ledger format and an allowlist example row are on `-h`; the skip string `SLEEPS:` and the 3-times bound are still not; the remedy still prints `\x20` |
| a flag parse failure hides the other problems | STILL THERE | `--budget -3 --load Inf --nope extra` named only `--nope` |
| spec says over-budget exits 2 | STILL THERE | docs/SPEC-CI.md:297 says exit 2 under `--enforce`; docs/SPEC-CI.md:220 says 1 |
| spec presents class tests as verbs | CHANGED | docs/SPEC-CI.md:1 is titled "the class tests that read this repository's own CI path" |
| banner pipe line lacks the test-exit caveat | CHANGED | the banner no longer has a pipe line at all; docs/CLI.md:2193 has the pipe and the caveat |
| CLI.md section lacks a First run heading | STILL THERE | docs/CLI.md:2184 to its next `## ` has only `### bench run` and `### github receipt` |
| five success dialects | CHANGED | functional now has `CI FUNCTIONAL OK`, local and the scaffolds print `NOTE` lines, and receipt prints `CI RECEIPT`; the functional selection is still bare lines and bench adds a third refusal prefix |
