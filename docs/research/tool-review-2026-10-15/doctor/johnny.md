# nova-doctor AI-usability review, 2026-10-15

Reviewer: Mercury (mercury-3-preview-1002)
Build: cc2f2f8c8148
Score: 10/10 (55/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-doctor help` -> `first run: nova-doctor run; nothing is changed, no fix is run for you.` then `nova-doctor run` printed all checks |
| 2 | refusals | 5 | `nova-doctor run --check nosuchcheck` -> `RUN REFUSED: no check named "nosuchcheck"; the checks are ...; run: nova-doctor help` |
| 3 | output | 5 | `nova-doctor run --json` -> `{"exit":2,"results":[{"check":"dashboard","status":"warn",...` stable `DOCTOR <check> ok|warn|fail` per check |
| 4 | dry-run | 5 | n/a: read-only tool; every verb's help says `effect: inspection: reads, writes nothing` |
| 5 | idempotent | 5 | n/a: no write surface; no --op offered, nothing to rerun |
| 6 | no prompts | 5 | every command returned without waiting on input; no interactive answer observed |
| 7 | exit codes | 5 | `nova-doctor run --check harness; echo $?` -> `0`; `--check dashboard --strict` -> `1`; `--check gosdk` in scratch -> `2`; matching help |
| 8 | one word | 5 | verbs run/version/help distinct; check names are single tokens (dashboard, gosdk, tailnet, ...), no duplicates |
| 9 | push | 5 | n/a: nothing is waited or polled on; each check reads once and reports |
| 10 | no leaks | 5 | `nova-doctor run` -> `DOCTOR secrets fail sops is not on PATH ...`; no secret or key in any line |
| 11 | true help | 5 | help's exit table `0 every check ok ..., 1 a warn under --strict, 2 a fail` reproduced exactly when run |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-doctor run --local` | summary line breaks the per-check grammar: `DOCTOR local skipped=dashboard,providers,secrets,ssh,tailnet` has no ok|warn|fail field | render the skip as a `NOTE local skipped=...` continuation so one grammar holds | S |
| 2 | `nova-doctor run` | evidence and remedy share one prose string separated by a literal ` fix: ` token; a parser must split on it and evidence could contain the token | print `fix` as a `NOTE fix: <line>` continuation instead of inline prose | S |
| 3 | `nova-doctor run` harness check | evidence leaks a machine path `no friend rows at /home/nova/.../data/.nova/friends`, machine-specific noise | keep evidence to the dependency state, move the path into the JSON `facts` | S |

## Top 3 fixes
1. Emit the --local skip summary as a `NOTE` line so every output line keeps the `DOCTOR <check> ok|warn|fail` grammar.
2. Move `fix:` out of the evidence prose into its own `NOTE fix: <line>` continuation for unambiguous parsing.
3. Drop machine paths from default-line evidence; keep them in `facts` under --json.

## Good, keep
Read-only tool with every verb's help stating `effect: inspection: reads, writes nothing`; no write surface, so no dry-run needed.
Every refusal names its cause, the valid alternatives, and the exact next command (`run: nova-doctor run -h`, `run: nova-doctor help`), and the JSON refusal envelope carries `remedy` and `why` as arrays.
The help talks the banner's sentence, the `first run:` line, the example block, and the exit table all reproduce exactly when run.
## What could not be tried
The ssh, tailnet, providers, secrets, dashboard checks reach fleet and seat state and legitimately fail here for lack of a seat environment; each names its remedy, and no service was started or touched to complete them. No verb requires a service, so run, version and help were all exercised cold.
