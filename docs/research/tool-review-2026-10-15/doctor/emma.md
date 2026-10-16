# nova-doctor AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: b4f484c84916
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-doctor run --check harness` -> `DOCTOR harness ok no friend rows at /home/user/.nova/friends` |
| 2 | refusals | 3 | `nova-doctor magicverb` -> `DOCTOR REFUSED: "magicverb" is no verb and no file; the verbs are run, version, and a file is given by its path (./magicverb); run: nova-doctor help` |
| 3 | output | 3 | `nova-doctor run --check harness --json` -> `{"exit":0,"results":[{"check":"harness","dependency":"the friend harnesses","status":"ok","evidence":"no friend rows at /home/user/.nova/friends"}]}` |
| 4 | dry-run | 5 | `n/a: read-only diagnostic checker that writes nothing ("first run: nova-doctor run; nothing is changed, no fix is run for you")` |
| 5 | idempotent | 5 | `n/a: read-only diagnostic checker with no write verbs` |
| 6 | no prompts | 5 | `nova-doctor run --local` -> `DOCTOR local skipped=dashboard,providers,secrets,ssh,tailnet (checks only a fleet needs; run without --local to include them)` |
| 7 | exit codes | 5 | `nova-doctor run --check harness` -> `DOCTOR harness ok no friend rows at /home/user/.nova/friends` |
| 8 | one word | 5 | `nova-doctor version` -> `nova-doctor 20261015T165143Z-b4f484c84916 linux/amd64 go1.26.6` |
| 9 | push | 5 | `n/a: one-shot diagnostic checker with no state watches or polling loops` |
| 10 | no leaks | 5 | `nova-doctor run --local` -> `DOCTOR friend-login ok no friend daemons here` |
| 11 | true help | 3 | `nova-doctor run --max 5` -> `RUN REFUSED: unknown flag --max; the flags of run are --check, --dir, --json, --local, --strict; run: nova-doctor run -h` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-doctor --help` | States `Every verb but run takes --json: run prints one DOCTOR <check> ok/warn/fail <evidence> [fix: <line>] line per check; --json prints results as one object`, directly contradicting itself in the same sentence (`run` does take `--json`), and also states `A verb that lists takes --max <n>` when no verb in `nova-doctor` accepts `--max` | Change `Every verb but run takes --json` to `Every verb takes --json` and remove the inapplicable `--max <n>` sentence from `nova-doctor --help` | S |
| 2 | `nova-doctor run -h` and `nova-doctor run --check` | `nova-doctor run -h` prints `--check <help run>  run only this check (repeatable); the names are in help run`, and `nova-doctor run --check` echoes that malformed placeholder in its refusal (`--check needs a value: it wants run only this check (repeatable); the names are in help run`) | Set `--check`'s placeholder/type in its flag description to `<name>` so `-h` prints `--check <name>` and the missing-value refusal reads cleanly | S |
| 3 | `nova-doctor run --local` (`plists` check on Linux) | Prints `DOCTOR plists fail /home/user/.config/systemd/user/nova-redis-store.service: fix: rewrite the agent with the tool that owns it (nova-friend install, nova-config loop) and remove a hand-written stopgap`, leaving a trailing colon after the unit path with an empty reason string | Include the specific validation failure reason after `<path>: ` in `plists` check evidence | S |
| 4 | `nova-doctor magicverb`, `nova-doctor run --check boguscheck`, `nova-doctor run --dir /nonexistent-dir-xyz` | Unknown-verb refusal says `"magicverb" is no verb and no file; the verbs are run, version, and a file is given by its path (./magicverb)` even though `nova-doctor` accepts no file positional argument, and bad-value refusals on `run` end with `run: nova-doctor help` instead of `run: nova-doctor run -h` | Remove the `no file` clause from unknown-verb refusals on tools with no file argument and point `run` flag/value refusals to `run: nova-doctor run -h` | S |
| 5 | `nova-doctor run --check harness --json` and `nova-doctor version --json` | `run --json` emits a bespoke top-level object (`{"exit":0,"results":[...]}`) instead of the standard `{"result":{"verb":"run","status":"ok","exit":0},"facts":...,"items":...}` envelope used by `version --json` and refusals, while `version --json` leaves `facts` empty | Include the standard `result`/`facts`/`items` envelope fields on `run --json` and structured version `facts` on `version --json` | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-doctor run --local` (`gosdk` check outside a repository) | Running `nova-doctor run --local` from a scratch directory fails `gosdk` (`no go.mod here: the bench's toolchain is not named fix: run nova-doctor run in a checkout of this repository`), exiting `2` unless `--dir <repo>` is passed | Mention `--dir <repo>` directly in the `gosdk` fix line (`fix: pass --dir <repo> or run nova-doctor run in a checkout of this repository`) | S |

## Top 3 fixes
1. Fix `nova-doctor --help` so it states `Every verb takes --json` (instead of `Every verb but run takes --json`) and drops the nonexistent `--max <n>` claim, and fix the `--check <name>` placeholder in `nova-doctor run -h`.
2. Populate the specific failure reason after `<unit-path>: ` in the `plists` check and include `--dir <repo>` in the `gosdk` fix line.
3. Remove the bogus `and no file ... (./magicverb)` hint from `nova-doctor` unknown-verb refusals and point `run` value refusals to `run: nova-doctor run -h`.

## Good, keep
- Every check runs to completion without stopping on the first failure, and `--local` clearly lists the exact fleet-only checks it skipped (`skipped=dashboard,providers,secrets,ssh,tailnet`).
- Each failing check prints a concrete `fix:` command on the same line (and in `--json` `results[].fix`), making it trivial for an AI to read the remedy without consulting external docs.
- `--dir <path>` scopes repository checks (`gosdk`) cleanly without requiring the caller to change working directories.

## What could not be tried
- Fleet-wide checks (`dashboard`, `providers`, `secrets`, `ssh`, `tailnet`) and executing the `fix:` commands (`nova-update apply`, `nova-friend install`, `nova-config loop`): touch live fleet services or system units outside `<job>/scratch`, which the card rules forbid (`Its local run, which only checks; read every line it prints and follow one remedy it names as far as reading goes`).
