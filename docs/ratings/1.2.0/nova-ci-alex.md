# nova-ci READ and USE rating, nova-tools 1.2.0

Rater: openai/gpt-5.6-luna in OpenCode, a sprint worker on a friend's re-rate card
Build: 1b2c8aea22f1
READ: 8/10
USE: 8/10

Read cold from `nova-ci help`, every verb and group verb's `-h`, `docs/SPEC-CI.md`, and the nova-ci sections of `docs/CLI.md` and `docs/TESTS.md`. No v1.2.0 tag exists yet, so this rates the release candidate at the head of `sprint/mechanical-2026-10-02`. Built the binary and used it from a throwaway Go module containing a real test, with no live store and no server. The staged checkout's documented shared cache was used for the gate.

## Reasons

READ. The banner is unusually useful for a small model: its first line says what the tool does, the how paragraph names the event stream, package selection, checkout and receipt nouns, the effects are explicit, and the example commands are runnable from the binary alone. Every shipped verb has a help page with flags, effects and exit codes. The spec is close to the binary and explains the important distinction that slowtests measures timing while the producer's test exit must be carried separately. The strongest cold-read feature is that `slowtests --example` gives a no-setup trial and that the functional, local, scaffold and receipt verbs say what they read or write.

The remaining reading cost is cross-verb rather than basic syntax. The top-level help is long, and the nested `bench -h` and `github -h` pages reproduce a large banner instead of giving a compact group index. The functional verb's output contract says it prints a package line and one run pattern, but does not state what a consumer should do when that pattern becomes very large. The one-output-value standard is stated for the family, while only slowtests actually accepts `--json`; a reader has to notice this from each help page rather than from a family-level warning.

USE. The first run worked without a module: `slowtests --example --budget 60 --load 4 --cpus 16` printed a CI-SLOW measurement and CI-LOAD line and exited 0. In a throwaway module, `go test -json -count=1 . | nova-ci slowtests --budget 60 --load 4 --cpus 2` printed `CI-SLOW OK` and `CI-LOAD`, and `functional .` cleanly reported no functional packages. From the checkout, `local --dry-run` printed its selected packages and exact Make command without running tests. The receipt dry run validated all fields, printed the event line and explicitly said nothing was dialled. Scaffold dry runs listed files and said nothing was written. Refusals generally name the wanted shape and a runnable help command; multiple bad slowtests inputs were reported together.

The score is held at 8 by output drift and scale. `slowtests --json` omits `per-cpu`, although the line rendering prints it, so a JSON consumer cannot consume the same facts. `functional ./...` printed 39 package paths followed by a 34,765-character `-run` expression naming 829 tests, with no bound, continuation or note explaining that the expression must be passed as one argument. An unknown verb lists the available verbs but does not offer a nearest-name hint, unlike a close unknown flag. These are recoverable, but each makes an AI spend an avoidable turn or parse an avoidably fragile output.

A 10 needs every verb on the shared result value with both line and JSON renderings, a bounded functional selection contract, and nearest-name recovery for unknown verbs. It should also keep the nested group help compact and make the family-level output law visible at the point where a verb does not implement it.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci slowtests --example --json --load 4 --cpus 16` | the line output carries `per-cpu=0.25` but the JSON `facts` carry only `load` and `cpus`, so the two renderings of one result drift and a consumer must recompute a published fact | include `per_cpu` in the JSON facts from the same result value | S |
| 2 | `nova-ci functional ./...` | the command prints a 34,765-character `-run` argument naming 829 tests and gives no stated bound, continuation, or note that the whole expression must be passed as one argument; the output is hard to safely transport or inspect | bound the pattern or split it under a documented limit, and print a note describing the complete-set contract | S |
| 3 | `nova-ci frobnicate` | an unknown top-level verb lists the verbs and remedy but names no nearest verb, while close unknown flags provide a nearest suggestion | report the nearest verb when the edit distance is useful, and retain the full verb list for far guesses | S |
| 4 | `nova-ci bench -h` and `nova-ci github -h` | group help expands into the full top-level banner instead of a compact group usage and verb index, making a cold reader reread unrelated slowtests/local material before reaching the nested verb | print a short group usage, its verbs and their effects, then direct the reader to `<group> <verb> -h` | S |

## Good, keep

The no-infrastructure `slowtests --example` first run is excellent: it is a real result, not a placeholder, and its output teaches the distinction between a timing finding and a failing verdict. The refusal grammar usually names the input's wanted shape and ends in a command that runs. `--dry-run` on the local, scaffold and receipt paths makes writes and test execution safe to inspect. The checkout's `local --dry-run` also prints the exact selected package set and Make invocation, which is valuable evidence for an AI deciding whether to run the real tier.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| functional output printed package arguments without `./` | FIXED | `functional ./...` now prints `./cmd/.../` and `./internal/.../`, which are pasteable Go package directories |
| slowtests JSON omitted the per-CPU figure | STILL THERE | current `--json` facts have `load` and `cpus`, while current line output has `per-cpu=0.25` |
| unknown verbs lacked nearest-name recovery | STILL THERE | `frobnicate` lists verbs but prints no `did you mean` hint |
| functional selection emitted an oversized unbounded pattern | STILL THERE | current `functional ./...` emits 829 names in a 34,765-character expression |
| slowtests help did not state the allowlist 3x ceiling | FIXED | current `slowtests -h` explicitly says the budget is between the measurement and three times it |
| five output dialects made nova-ci unlike the shared result standard | STILL THERE | only slowtests accepts `--json`; functional, local, scaffold, bench and receipt each retain line-only shapes |
