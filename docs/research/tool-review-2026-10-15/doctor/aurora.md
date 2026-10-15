# nova-doctor AI-usability review, 2026-10-15

Reviewer: grok-4.7
Build: 09339b539c97
Score: 9.5/10 (53/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-doctor run --check plists` -> `DOCTOR plists ok 5 nova launch agents lint clean` |
| 2 | refusals | 5 | `nova-doctor run --bogus` -> `RUN REFUSED: unknown flag --bogus; the flags of run are --check, --dir, --json, --local, --strict; run: nova-doctor run -h` |
| 3 | output | 5 | `nova-doctor run --check plists --json` -> `{"exit":0,"results":[{"check":"plists","dependency":"the com.nova launchd plists and nova systemd units this machine loads","status":"ok","evidence":"5 nova launch agents lint clean"}]}` |
| 4 | dry-run | 5 | `n/a: nova-doctor is read-only; help states "effect: inspection: reads, writes nothing"` |
| 5 | idempotent | 5 | `n/a: no write verb exists; every verb is inspection with no state` |
| 6 | no prompts | 5 | `nova-doctor run --check plists` -> `DOCTOR plists ok 5 nova launch agents lint clean` |
| 7 | exit codes | 5 | `nova-doctor run --check gosdk` -> `DOCTOR gosdk fail no go.mod here: the bench's toolchain is not named fix: run nova-doctor run in a checkout of this repository, whose go.mod names the bench's toolchain (docs/SETUP.md, dep-go-sdk-b.w2)` (`$?` -> 2); `nova-doctor version` -> `nova-doctor v1.2.26-0.20261015162535-09339b539c97 darwin/amd64 go1.26.6` (`$?` -> 0) |
| 8 | one word | 5 | `nova-doctor help` -> `nova-doctor [run] [--check <name>]... [--dir <path>] [--local] [--strict] [--json]` |
| 9 | push | 5 | `n/a: doctor is a pure probe; it waits on no state` |
| 10 | no leaks | 5 | `nova-doctor run --check gosdk` -> `DOCTOR gosdk fail no go.mod here: the bench's toolchain is not named fix: run nova-doctor run in a checkout of this repository, whose go.mod names the bench's toolchain (docs/SETUP.md, dep-go-sdk-b.w2)` |
| 11 | true help | 3 | `nova-doctor help` -> `Every verb but run takes --json` (false when run: `nova-doctor run --check plists --json` prints one JSON object, exit 0). Other run claims held (`--local` skip list, exit 2 on fail). |

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
- `--local` genuinely says which checks it skipped (`DOCTOR local skipped=dashboard,providers,secrets,ssh,tailnet (checks only a fleet needs; run without --local to include them)`), and each check line has a stable grammar with an actionable `fix:` line.
- The tool is fully read-only, runs every check even when one fails, and offers `--json` as a single parseable object. docs/ratings has no nova-doctor file, so no earlier defect from that set remains to carry.

## What could not be tried
- A fully green run (exit 0 with all checks ok) could not be produced: this checkout has no seat env, so the fleet checks (`providers`, `secrets`, `ssh`, `tailnet`) were not run to a healthy seat; their skip list is what `--local` prints.
- `--strict` (exit 1 on warn) was not exercised to completion: a warn exists (`gosdk`, exit 0 without `--strict`) but a fail is also present on a full local run, so exit 2 dominates; the warn-only exit code path was not isolated.
