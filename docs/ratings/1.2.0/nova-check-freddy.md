# nova-check READ and USE rating, nova-tools 1.2.0

Rater: freddy (inception/mercury-2.5 with opencode)
Build: 169a7eef0e30
READ: 9/10
USE: 9/10

## Reasons
READ. All seventeen verbs' `-h` were read cold, then `nova-check help` and the SPEC sections. The banner is honest: it names what the tool does, which verbs write state (`dogfood record`, `spelling --write`, `convergence --state`), and shows `quickstart` that runs as printed. Every verb's `-h` lists its flags with what each wants. The first confusion is `convergence`: it reads from `gh` and `git` but the help does not state those external dependencies clearly. The first boredom is the global help excerpt in `internal/nsprint/verbflag/verbflag.go`: it re-indents descriptions flush left, breaking the right column.

USE. Used on a throwaway `/tmp/nova-check-test` with no Redis, no `gh`, no `git`. `quickstart` ran, `links` and `nocode` passed. `convergence` refused with `gh` missing (as expected, but the help should say `gh` is required). `hygiene` refused without `--identity`. Every refusal named missing flags together. Exit codes held: 0 pass, 1 fail, 2 could-not-run.

The score is held below 10 by: help not stating `gh`/`git` are required for `convergence`; the `--max` paragraph omitting `hygiene` and `dogfood`; and `-h` excerpt indentation breaking the description column.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-check/main.go:294 | `convergence --help` does not state that `gh` and `git` are required executables; it only says they are used | add "(required)" to the flag help or state it in the usage | S |
| 2 | cmd/nova-check/main.go:32 | `--max` paragraph lists attest, links, nocode, corpus, quickstart, spelling but omits `hygiene` and `dogfood` which also take `--max` | list every verb that takes `--max` | S |
| 3 | internal/nsprint/verbflag/verbflag.go:313 | `-h` excerpts re-indent the right-hand column flush left, breaking the description into short lines | preserve the original help's indentation or join description lines | S |
| 4 | cmd/nova-check/kernel.go:34 | `kernel` requires `--max-bytes` or `--max-tokens` but neither flag is marked required in `-h` | mark both flags as "(required)" | S |
| 5 | cmd/nova-check/attest.go:42 | `attest` requires `--home` and `--manifest` but only `--home` is marked "(required)" | mark `--manifest` as "(required)" | S |
| 6 | cmd/nova-check/floors.go:41 | `floors` requires `--core` and `--source` but neither is marked "(required)" | mark both flags as "(required)" | S |
| 7 | cmd/nova-check/corpus.go:44 | `corpus` requires `--ledger`, `--root`, `--min-anchors` but only `--ledger` is marked "(required)" | mark `--root` and `--min-anchors` as "(required)" | S |

## Good, keep
All refusals name missing flags together with what each wants. Exit law holds everywhere (0 pass, 1 fail, 2 could-not-run). One-line output guarantee met. `quickstart` example runs as printed.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| `--fail-max` vs `--max` inconsistency (1.1.0) | FIXED | all verbs now use `--max`; `--fail-max` is an alias |
| `README.md` says 1.0.0 (1.1.0) | FIXED | README.md updated to 1.2.0 |
| unclear `convergence` external deps | STILL THERE | help does not clearly state `gh`/`git` required |
