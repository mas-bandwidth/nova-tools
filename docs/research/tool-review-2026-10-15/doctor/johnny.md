# nova-doctor AI-usability review, 2026-10-15

Reviewer: Mercury (mercury-3-preview-1002)
Build: 3c4034532400
Score: 9.5/10 (51/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-doctor help` -> `first run: nova-doctor run; nothing is changed, no fix is run for you.` then `nova-doctor run` printed all checks |
| 2 | refusals | 5 | `nova-doctor run --check invalid_check` -> `RUN REFUSED: no check named "invalid_check"; the checks are ...; run: nova-doctor help` |
| 3 | output | 4 | `nova-doctor run --local` -> `DOCTOR local skipped=dashboard,providers,secrets,ssh,tailnet (checks only a fleet needs...)` breaks the `DOCTOR <check> ok/warn/fail` grammar; `DOCTOR gosdk fail no go.mod here fix: run ...` puts fix inline with evidence |
| 4 | dry-run | 5 | n/a: read-only tool; every verb help states `effect: inspection: reads, writes nothing` |
| 5 | idempotent | 5 | n/a: no write surface; no --op offered, nothing to rerun |
| 6 | no prompts | 5 | every command returned without waiting on input; no interactive answer observed |
| 7 | exit codes | 5 | `nova-doctor run --check plists; echo $?` -> `0`; `--check dashboard --strict` -> `1`; `--check gosdk` in scratch -> `2`; matching help |
| 8 | one word | 5 | verbs run/version/help distinct; check names are single tokens (dashboard, gosdk, tailnet, ...), no duplicates |
| 9 | push | 5 | n/a: nothing is waited or polled on; each check reads once and reports |
| 10 | no leaks | 4 | `nova-doctor run` harness evidence `no friend rows at /home/user/.../data/.nova/friends` leaks a machine path |
| 11 | true help | 3 | `nova-doctor help` claims `Every verb but run takes --json` but `run --json` works; claims `A verb that lists takes --max` but no verb lists nor accepts --max |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-doctor help` | `Every verb but run takes --json: run prints one DOCTOR <check> ok/warn/fail <evidence> [fix: <line>] line per check; --json prints results as one object and refusals as the standard JSON envelope` is false; run accepts --json | reword to `run and version take --json; run prints lines by default and --json switches to one object` | S |
| 2 | `nova-doctor help` | `A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest` claims nonsense; no verb of nova-doctor lists and none accepts --max | drop the boilerplate sentence from this tool help | S |
| 3 | `nova-doctor run --local` | summary line `DOCTOR local skipped=dashboard,providers,secrets,ssh,tailnet (checks only a fleet needs; run without --local to include them)` has no ok/warn/fail field and breaks the per-check grammar | render the skip as a `NOTE local skipped=...` continuation so one grammar holds | S |
| 4 | `nova-doctor run` | evidence and fix share one prose string separated by literal ` fix: ` token; a parser must split on it and evidence could contain the token | print `fix` as a `NOTE fix: <line>` continuation instead of inline prose | S |
| 5 | `nova-doctor run` harness check | evidence `no friend rows at /home/user/.../data/.nova/friends` leaks machine-specific path | keep evidence to the dependency state, move the path into the JSON `facts` | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-doctor run --check nosuchcheck` | refusal ends `run: nova-doctor help`; the more useful next step is `run -h` to see the checks list | point the run-flag refusal at `nova-doctor run -h` | S |
| 2 | `nova-doctor help run` | flag help shows `--check <help run>` as a placeholder, which reads oddly next to the description | use `<name>` as the placeholder | S |

## Top 3 fixes
1. Correct the false `Every verb but run takes --json` and `A verb that lists takes --max` help lines (fixes criterion 11, moves score to 55/55).
2. Emit the --local skip summary as a `NOTE` line so every output line keeps the `DOCTOR <check> ok/warn/fail` grammar (fixes criterion 3).
3. Move `fix:` out of the evidence prose into its own `NOTE fix: <line>` continuation for unambiguous parsing (fixes criterion 3).

## Good, keep
Read-only tool with every verb help stating `effect: inspection: reads, writes nothing`; no write surface, so no dry-run needed.
Every refusal names its cause, the valid alternatives, and the exact next command (`run: nova-doctor run -h`, `run: nova-doctor help`), and the JSON refusal envelope carries `remedy` and `why` as arrays.
The help banner sentence, the `first run:` line, the example block, and the exit table all reproduce exactly when run.

## What could not be tried
The ssh, tailnet, providers, secrets, dashboard checks reach fleet and seat state and legitimately fail here for lack of a seat environment; each names its remedy, and no service was started or touched to complete them. No verb requires a service, so run, version and help were all exercised cold. The 2026-10-02 ratings contain no prior nova-doctor review, so no old defect needed carrying forward.
