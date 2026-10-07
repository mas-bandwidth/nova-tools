# nova-ci rating, nova-tools v1.2.0

Rater: friend reviewer (opencode/gpt-5.6-luna via OpenCode)
Build: a65b5cba44ab
READ: 8.5/10
USE: 8/10

## Reasons

Read cold from the binary help and `docs/SPEC-CI.md`. The banner answers the
purpose, state model, first run, verbs, effects, and exit-code meanings before
the command reference. The verb help is unusually useful for an AI: flags say
what they want, slowtests explains the allowlist row and its 3x ceiling, and
the refusal grammar gives a runnable recovery command. The spec makes the
slowtests contract concrete: package totals decide the budget, test timings
explain findings, load is measurement only, and malformed input is refused.
The main read cost is the very large `nova-ci help` output, with slowtests'
full contract repeated in the banner and its own help. `bench run -h` is also
not a focused help page, which makes the remote-delivery verb harder to trust
when encountered cold.

Use was on a throwaway directory only. `slowtests --example` ran without setup
and produced the documented CI-SLOW and CI-LOAD lines. A fresh Go module was
fed through `go test -json` and slowtests, and the functional, local dry-run,
new-rule dry-run, new-verb dry-run, JSON, and github receipt dry-run paths were
all exercised. The output is bounded and actionable, dry-runs clearly state
that nothing was written or dialled, and no server or live store was needed.
The score is held below 10 because the JSON rendering drops the per-cpu value
that the line rendering prints, and because the bench help path is misleading.

## Findings

| # | where | finding | fix |
|---|---|---|---|
| 1 | `nova-ci bench run -h` | prints the complete top-level banner instead of focused `bench run` help with its flags and exit table | preserve the compound verb name while handling help so `bench run -h` renders the bench verb's own help |
| 2 | `nova-ci slowtests --example` versus `--json` | line output prints `CI-LOAD ... per-cpu=...`, but JSON facts contain `load` and `cpus` without `per_cpu`, so the two renderings do not carry the same measured data | add the per-cpu measurement to the JSON facts from the same load value used by the line renderer |

## Good, keep

Keep the store-free `slowtests --example` first run, the one-line refusal
grammar with a runnable remedy, explicit effect labels, and dry-run behavior
for every write or delivery path. The help's allowlist example and stated
3x ceiling let an AI construct valid input without trial-and-error, while the
JSON result gives a machine-readable verdict for the same run.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| functional selection printed bare package paths | FIXED | the current help and implementation describe package directories with `./` paths, and the throwaway invocation is accepted |
| allowlist bound absent from help | FIXED | slowtests help states the measurement-to-3x headroom ceiling |
| JSON omitted per-cpu measurement | STILL THERE | the current `--example` line has `per-cpu=0.43`-style data while the JSON facts have only `load` and `cpus` |
| compound bench help needed a focused page | STILL THERE | `nova-ci bench run -h` printed the top-level banner; `help bench run` is the focused workaround |
