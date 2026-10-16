# nova-doctor AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.5/10 (53/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-doctor run` -> `DOCTOR self ok 22 tools on PATH, all v1.2.26-0.20261015145010-cc2f2f8c8148` |
| 2 | refusals | 5 | `nova-doctor --bogus` -> `RUN REFUSED: unknown flag --bogus; ...; run: nova-doctor run -h` |
| 3 | output | 5 | `nova-doctor run --local --json` -> `{"exit":0,"results":[{"check":"friend-login",...}]}` |
| 4 | dry-run | 5 | `n/a: inspection-only; help run states "effect: inspection: reads, writes nothing"` |
| 5 | idempotent | 5 | `n/a: no write verb exists; run writes nothing` |
| 6 | no prompts | 5 | `nova-doctor run` (all variants) returned without waiting on any input |
| 7 | exit codes | 5 | `nova-doctor run` (fail) -> `EXIT 2`; `run --local --strict` (warn) -> `EXIT 1`; ok -> `EXIT 0` |
| 8 | one word | 5 | verbs are run/version/help; each check is one word; no duplicate verbs or tools |
| 9 | push | 5 | `n/a: nothing waits on state; read-only checks return immediately` |
| 10 | no leaks | 5 | output names env vars (NOVA_SECRETS_STORE, NOVA_SECRETS_SEAT) but never prints their values |
| 11 | true help | 3 | `nova-doctor help` -> `Every verb but run takes --json` but `nova-doctor run --json` works |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-doctor help` | `Every verb but run takes --json: run prints one ... line per check` is false; run accepts and prints --json. | reword to `run and version take --json` (or state run prints lines by default and --json switches it) | S |
| 2 | `nova-doctor help` | `A verb that lists takes --max <n> (default 20, 0 lists all)` claims nonsense: no verb of nova-doctor lists, and none accepts --max. | drop the boilerplate sentence from this tool's help | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-doctor run --check nosuchcheck` | refusal ends `run: nova-doctor help`; the more useful next step is `run -h` to see checks | point the run-flag refusal at `nova-doctor run -h` | S |
| 2 | `nova-doctor help run` | flag help shows `--check <help run>` as a placeholder, which reads oddly next to the description | use `<name>` as the placeholder | S |

## Top 3 fixes
1. Correct the false `Every verb but run takes --json` help line (fixes criterion 11).
2. Remove the stray `a verb that lists takes --max` boilerplate from help (fixes criterion 11).
3. Make the bad-`--check` refusal name `nova-doctor run -h` instead of `help` (criterion 2 polish).

## Good, keep
- One `DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]` line per check, with a concrete fix line on every fail; ideal for an AI to follow.
- `--json` returns a stable object with `exit`, `results`, and `skipped`; easy to parse.
- Every refusal names its cause and a next command, and exit codes match the documented 0/1/2 contract exactly.
- The tool is genuinely read-only and prompt-free: it inspects, writes nothing, and never blocks on input, so it is safe to run in any context.

## What could not be tried
- The fleet-only checks (dashboard, providers, secrets, ssh, tailnet) were not run to pass/fail against a live seat: they require a seat environment (a seat.env file, NOVA_SECRETS_SEAT/STORE) that is not present here, so they were judged only through `--local` (which skips them) and the check names in help.
- No verb is service-backed beyond these checks; run, version, and help were all exercised end to end. docs- (the 2026-10-02 ratings) contain no prior nova-doctor review, so no old defect needed carrying forward.
