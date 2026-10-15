# nova-doctor AI-usability review summary, 2026-10-15

Reviews: 4 (R1..R4, in byte order of file name)
Build: 09339b539c97, b4f484c84916, 3c4034532400, cc2f2f8c8148
Score: 9.5/10 (range 8.5-9.5)

## Rubric
| # | criterion | median | range | the evidence that decided it |
|---|---|---|---|---|
| 1 | cold | 5 | 4-5 | `nova-doctor run` -> `DOCTOR self ok 22 tools on PATH...`; R2 needed `--check harness` as the first cold run |
| 2 | refusals | 5 | 3-5 | `nova-doctor magicverb` -> `DOCTOR REFUSED: "magicverb" is no verb and no file; ... run: nova-doctor help`; R2 scored 3 on the bogus "no file" clause and the `help` pointer |
| 3 | output | 4.5 | 3-5 | `nova-doctor run --check harness --json` -> `{"exit":0,"results":[{"check":"harness",...}]}` is a bespoke envelope, not the standard `result`/`facts`/`items` one |
| 4 | dry-run | 5 | 5-5 | n/a: read-only diagnostic that writes nothing (help: "effect: inspection: reads, writes nothing") |
| 5 | idempotent | 5 | 5-5 | n/a: no write verb exists; every verb is inspection with no state |
| 6 | no prompts | 5 | 5-5 | `nova-doctor run` and all variants returned without waiting on any input |
| 7 | exit codes | 5 | 5-5 | fail -> `EXIT 2`; `--strict` warn -> `EXIT 1`; ok -> `EXIT 0`, matching the help table |
| 8 | one word | 5 | 5-5 | verbs run/version/help; each check is one token; no duplicates |
| 9 | push | 5 | 5-5 | n/a: a pure probe; each check reads once and reports, waits on no state |
| 10 | no leaks | 5 | 4-5 | `nova-doctor run --check harness` -> evidence prints the store's absolute path instead of only dependency state |
| 11 | true help | 3 | 3-3 | `nova-doctor help` says "Every verb but run takes --json" yet `run --json` works; "A verb that lists takes --max" names a flag no verb accepts |

## Consensus 10/10 blockers
| # | where | what an AI meets | fix | size | named by |
|---|---|---|---|---|
| 1 | `nova-doctor help` | says "Every verb but run takes --json" while run accepts and prints `--json`; false and self-contradictory | reword to "Every verb takes --json: run prints one DOCTOR line per check; --json prints results as one object" | S | R1,R2,R3,R4 |
| 2 | `nova-doctor help` | claims "A verb that lists takes --max <n> (default 20, 0 lists all)" though no verb of nova-doctor lists nor accepts `--max` | drop the boilerplate sentence from this tool's help | S | R2,R3,R4 |
| 3 | `nova-doctor run -h`, `nova-doctor run --check` | `--check <help run>` malformed placeholder appears in help and is echoed in the missing-value refusal | set the flag placeholder to `<name>` so help prints `--check <name>` | S | R2,R3,R4 |
| 4 | `nova-doctor run --check boguscheck` | refusal ends "run: nova-doctor help"; the specific next command for a run-flag misuse is `run -h` | point run flag/value refusals at `nova-doctor run -h` | S | R1,R2,R3,R4 |
| 5 | `nova-doctor run --local` | the line `DOCTOR local skipped=dashboard,providers,secrets,ssh,tailnet (...)` has no ok/warn/fail field and breaks the per-check grammar | render the skip as a `NOTE` continuation so one grammar holds | S | R3 |
| 6 | `nova-doctor run` | evidence and fix share one prose string split on a literal " fix: " token a parser must find | print `fix:` as a `NOTE fix: <line>` continuation instead of inline prose | S | R3 |
| 7 | `nova-doctor run --check harness --json` | `run --json` emits a bespoke top-level object, not the standard `result`/`facts`/`items` envelope `version --json` uses; `version --json` leaves `facts` empty | emit the standard envelope on `run --json` and populate version `facts` | S | R2 |
| 8 | `nova-doctor run --check harness` | evidence prints the store's absolute path (a machine home path) instead of only dependency state | keep evidence to the dependency state; move the path into the JSON `facts` | S | R3 |
| 9 | `nova-doctor magicverb` | unknown-verb refusal names a "file" positional this tool does not have ("and no file ... (./magicverb)") | drop the "no file" clause for tools with no file argument | S | R2 |

## Top 3 fixes
1. Rewrite the false help claims (`Every verb but run takes --json`, `A verb that lists takes --max`) so criterion 11 is true for every reviewer.
2. Emit `run --json` in the standard `result`/`facts`/`items` envelope and populate `version --json` facts, so all JSON output has one shape.
3. Render the `--local` skip summary as a `NOTE` continuation and move `fix:` out of the evidence prose, so every output line keeps the `DOCTOR <check> ok|warn|fail` grammar.

## Disagreements
- Criterion 2 (refusals): R2 scored 3, the rest 5. Rerunning `nova-doctor magicverb` does reproduce the bogus "no file" clause and the `help` pointer, so R2's evidence holds.
- Criterion 3 (output): R2 scored 3, R1/R4 5. Rerunning `nova-doctor run --check harness --json` confirms the bespoke envelope, so R2's evidence holds.
- Blocker "plists check leaves a trailing colon with an empty reason" (R2) was dropped: it needs a malformed systemd unit present on the machine to reproduce, and none exists in the read-only checkout, so the exact command (`nova-doctor run --local`) prints `DOCTOR plists ok` cleanly here.

## Nice-to-haves
- Bare `nova-doctor` (usage shows `[run]` as optional) refuses with exit 2 instead of running the default `run` (R1).
- The `gosdk` fix line should name `--dir <repo>` when run outside a repository checkout (R2).
