# nova-local AI-usability review, 2026-10-15

Reviewer: Mercury
Build: 17b017339e72
Score: 8.5/10 (47/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-local help` -> `nova-local: run local models: what an engine has, one model served at a chosen context, and a worker description nova-swarm accepts`; status and refusals run cold, but the real job (serve/worker) needs a running ollama engine and pulled weights |
| 2 | refusals | 5 | `nova-local status --engine foo` -> `STATUS REFUSED: --engine wants one of ollama (got "foo"); run: nova-local help`; unknown verb lists the verbs and the next command |
| 3 | output | 5 | `nova-local status --json` -> `{"result":{"verb":"status","status":"failed","exit":1,"remedy":"ollama serve","why":[...]},"facts":{}}`; stable `VERB STATUS/REFUSED/FAILED` line grammar on every verb |
| 4 | dry-run | 3 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7 --dry-run` -> `SERVE FAILED: no engine answers`; both writes offer --dry-run but it fails before rendering the plan when no engine answers, with `NOTE --dry-run was given and the verb never read it` |
| 5 | idempotent | 1 | `nova-local serve -h` lists no `--op` flag, and `nova-local worker -h` lists none; serve reuses a matching tag but has no explicit op id |
| 6 | no prompts | 5 | every command above returned immediately with no interactive prompt |
| 7 | exit codes | 5 | `nova-local status` -> `EXIT=1` (ran, said no) and `nova-local nope` -> `EXIT=2` (could not run), matching the help table `0 done, 1 it ran and said no, 2 could not run` |
| 8 | one word | 5 | verbs are `status, serve, worker, version`, each one word, no duplicates |
| 9 | push | 5 | `n/a: no state is waited on; status reads engines, serve sends one warm-up, no polling wait exists` |
| 10 | no leaks | 5 | no output line in any command above leaked a secret; `--env-var` takes a NAME, never a value |
| 11 | true help | 4 | verified exit-code table, `--json` on every verb, and the refusal grammar from help; serve/worker success paths unverifiable without an engine, and usage shows `--seed <n>` while the flag list says `--seed <string>` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7 --dry-run` | `SERVE FAILED: no engine answers; ... run: ollama serve` then `SERVE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written`; a new reader cannot preview the plan without a live engine, and the NOTE is confusing | render the dry-run plan from the flags before dialling the engine, so `--dry-run` always prints what the verb would do | M |
| 2 | `nova-local serve -h`, `nova-local worker -h` | no `--op <id>` flag on either write verb, so a timed-out serve/worker cannot be retried safely under one recorded op id | add the standard `--op <id>` idempotent-write flag to serve and worker | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-local help` usage line vs serve flag list | usage says `--seed <n>`, the flag list says `--seed <string>` for the same flag | make both name the same type | S |

## Top 3 fixes
1. Make `--dry-run` print the plan before contacting the engine (moves criterion 4).
2. Add `--op <id>` to serve and worker for idempotent retry (criterion 5).
3. Unify the `--seed` type in usage and flag list (true help).

## Good, keep
- Every refusal names its cause and the next command (`run: nova-local help` or `run: nova-local status -h`) in one turn.
- `--json` on every verb returns one structured `{result, facts}` object that matches the documented shape.
- Exit codes are truthful and stated in help: 1 for "ran and said no", 2 for usage.

## What could not be tried
- `serve` (full), `serve --stop`, and `worker` (full) need a running ollama engine with a pulled model; no engine is started on this machine, so their success paths and real writes were not run.
- The write-nothing proof of `--dry-run` for serve/worker could not be observed, since both refuse before the plan when no engine answers.
