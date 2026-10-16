# nova-doctor AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: d7e47f037ea8
Score: 10/10 (54/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-doctor -h` -> `first run: nova-doctor run; nothing is changed, no fix is run for you.` |
| 2 | refusals | 5 | `nova-doctor frobnicate` -> `DOCTOR REFUSED: "frobnicate" is no verb and no file; the verbs are run, version, and a file is given by its path (./frobnicate); run: nova-doctor help` |
| 3 | output | 5 | `nova-doctor run --check harness --json` -> `{"exit":0,"results":[{"check":"harness","dependency":"the friend harnesses","status":"ok","evidence":"no friend rows at ..."}]}` |
| 4 | dry-run | 5 | `n/a: read-only inspection tool; every verb's effect is "reads, writes nothing"` |
| 5 | idempotent | 5 | `n/a: no write verbs, no --op surface` |
| 6 | no prompts | 5 | `nova-doctor run` -> `DOCTOR dashboard warn no loop record's unit runs nova-sprint dashboard ...` (runs to completion, nothing waits) |
| 7 | exit codes | 5 | `nova-doctor run --local --strict` -> exit 1 (matches help: "1 a warn under --strict") |
| 8 | one word | 5 | `nova-doctor help` -> `the verbs are run, version, and a file is given by its path` |
| 9 | push | 5 | `n/a: read-only, waits on no state` |
| 10 | no leaks | 5 | `nova-doctor run` -> `DOCTOR ssh fail ... fix: source the seat file (set -a; . ~/nova/seat.env; set +a), then run nova-doctor run again` (names the fix, prints no secret) |
| 11 | true help | 4 | `nova-doctor run --json` works, but help says "Every verb but run takes --json" |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `docs/CLI.md` (the whole tool is absent) | a new AI reading the command reference finds no nova-doctor section and no `### First run`; `grep -n "nova-doctor" docs/CLI.md` returns nothing | add the `clidoc` block and a `### First run` section for nova-doctor, like the other tools | M |
| 2 | `nova-doctor help` (line "Every verb but run takes --json") | the claim is false: `nova-doctor run --json` prints the JSON object exactly as `--json` does for other verbs | reword to "every verb takes --json" | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-doctor` (bare) vs `nova-doctor run` | the same refusal prefixes differently: bare says `DOCTOR REFUSED:`, explicit `run` says `RUN REFUSED:` | use one verb prefix for the refusal line | S |
| 2 | `nova-doctor help` (usage block) | the line "A verb that lists takes --max <n>" is boilerplate: no nova-doctor verb takes `--max` | drop or qualify the listing sentence | S |

## Top 3 fixes
1. Add the nova-doctor section (clidoc block + First run) to docs/CLI.md.
2. Fix the false "--json" claim in help ("Every verb but run takes --json" -> "every verb takes --json").
3. Unify the refusal prefix between bare and explicit `run`.

## Good, keep
- The banner is complete: what it does, how it works, and a `first run:` and `example:` that run as printed.
- Every refusal names the cause and the exact next command (`run: nova-doctor help` or `run: nova-doctor run -h`), and unknown flags list the flags there are.
- The output grammar is stable (`DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]`) and `--json` renders the same result as one object; exit codes match what help states.
- `--local` skips the fleet-only checks and names them (`skipped=dashboard,providers,secrets,ssh,tailnet`), so a first run on a lone machine is honest about what it did not check.

## What could not be tried
- The fleet-scoped checks (`dashboard`, `providers`, `secrets`, `ssh`, `tailnet`) were run but each returned a `fail` because no seat file / fleet inventory is configured here; following their remedies (nova-config, seat env, sops, nova-secrets) would touch live config and was not done. They were judged from their help and their `fail` lines.
- The repository-scoped `--dir` check was exercised only with `--check self`; the full repository-scoped set would need a configured fleet.
- No prior nova-doctor rating exists in docs/ratings (the 2026-10-02 round covers other tools), so no old defect could be compared.
