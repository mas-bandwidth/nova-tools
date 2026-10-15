# nova-doctor AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 10/10 (55/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-doctor run` -> `DOCTOR friend-login ok no friend daemons here` |
| 2 | refusals | 5 | `nova-doctor run --bogus` -> `RUN REFUSED: unknown flag --bogus; the flags of run are --check, --dir, --json, --local, --strict; run: nova-doctor run -h` |
| 3 | output | 5 | `nova-doctor run --json` -> `{"exit":2,"results":[{"check":"dashboard",...` |
| 4 | dry-run | 5 | `n/a: nova-doctor is read-only; help states "effect: inspection: reads, writes nothing"` |
| 5 | idempotent | 5 | `n/a: no write verb exists; every verb is inspection with no state` |
| 6 | no prompts | 5 | `nova-doctor run` -> completed immediately with no interactive wait |
| 7 | exit codes | 5 | `nova-doctor run` -> `EXIT=2` (fail); `nova-doctor version` -> `EXIT=0` |
| 8 | one word | 5 | `nova-doctor help` -> `the verbs are run, version`, single-word verbs, no duplicates |
| 9 | push | 5 | `n/a: doctor is a pure probe; it waits on no state` |
| 10 | no leaks | 5 | `nova-doctor run` -> fix lines quote commands and paths, never secrets |
| 11 | true help | 5 | `nova-doctor run --local` -> `DOCTOR local skipped=dashboard,providers,secrets,ssh,tailnet ...`, matching help's `--local ... says which` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-doctor help` | the line says `Every verb but run takes --json` while the same sentence then describes run's `--json`, which is self-contradictory | reword to `Every verb takes --json: run prints one DOCTOR line per check; --json prints the results as one object (refusals as the standard envelope)` | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-doctor` | bare invocation (usage shows `[run]` as optional) refuses with exit 2 instead of running the default `run` | accept bare invocation as `run`, as the `[run]` usage implies | S |
| 2 | `nova-doctor run --dir /nonexistent` | refusal says `run: nova-doctor help`; the more specific next command is `nova-doctor run -h` | point the refuse to `run -h` where the misuse is a run flag | S |

## Top 3 fixes
1. Reword the self-contradictory `Every verb but run takes --json` help sentence so run's `--json` is not presented as both absent and present.
2. Make bare `nova-doctor` run the default `run` verb, matching the `[run]` optional brace in usage.
3. Have run's refusals name `nova-doctor run -h` (not the more general `help`) as the next command.

## Good, keep
- Every refusal names the cause, lists every valid option at once (flags or checks), and gives the exact next command; exit code 2 on usage is consistent.
- `--local` genuinely says which checks it skipped (`DOCTOR local skipped=...`), and each check line has a stable grammar with an actionable `fix:` line.
- The tool is fully read-only, runs every check even when one fails, and offers `--json` as a single parseable object.

## What could not be tried
- A fully green run (exit 0 with all checks ok) could not be produced: this checkout has no seat env (`NOVA_SECRETS_*`, `~/nova/seat.env`), so `providers`, `secrets`, `ssh`, and `tailnet` necessarily report fail; their behavior under a healthy seat is trusted from the fix lines and `--local` skip list.
- `--strict` (exit 1 on warn) was not exercised to completion: a warn exists (`gosdk`) but fails are also present, so exit 2 dominates; the warn-only exit code path was not isolated.
