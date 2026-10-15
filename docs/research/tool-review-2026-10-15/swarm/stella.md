# nova-swarm AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: d7e47f037ea8
Score: 9.5/10 (53/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-swarm help` -> `first run: the examples print two templates and the lint rules; no setup is needed.` |
| 2 | refusals | 4 | `nova-swarm template` -> `nova-swarm template: --name is required; ... refusing to guess` (cause named, but no `run:` next-command line) |
| 3 | output | 5 | `nova-swarm lint --card card.md --child-rules` -> `LINT OK card=card.md checks=45 bytes=3068 cap=12000` |
| 4 | dry-run | 5 | `n/a: template and lint are inspection verbs, effect says "writes nothing"; no --dry-run offered or needed` |
| 5 | idempotent | 5 | `nova-swarm lint --card card.md --child-rules` twice -> `LINT OK card=card.md checks=45` (identical both runs) |
| 6 | no prompts | 5 | `nova-swarm template --name card` -> completed to EOF with no input awaited |
| 7 | exit codes | 5 | `nova-swarm lint --card badcard.md --child-rules` -> exit 1 with `LINT DRIFT`; missing flag -> exit 2; clean lint -> exit 0 |
| 8 | one word | 5 | `nova-swarm help` -> verbs list, all 15 single words, pairwise distinct |
| 9 | push | 5 | `n/a: nothing in this card's read scope waits on state; slots store out of scope (no run, no lease, no slot taken)` |
| 10 | no leaks | 5 | `nova-swarm lint --rules` -> no output line carried a credential or secret |
| 11 | true help | 4 | `nova-swarm lint --card card.md --child-rules --max 1` -> still prints all 10 NOTE lines, against `lint -h` `--max ... at most this many item lines` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-swarm template` (missing --name), also `nova-swarm lint` (missing --card) | `nova-swarm template: --name is required; ... refusing to guess` names the cause and the choices but no verbatim next command, unlike the unknown-verb/flag refusals that end `run: nova-swarm help <verb>` | send the missing-flag refusals through the same `refuse` path so they end `run: nova-swarm help <verb>` | S |
| 2 | `nova-swarm lint --card card.md --child-rules --max 1` | with ten NOTE placeholder lines, `--max 1` prints all ten and no `LINT MORE` line, against `lint -h`'s "at most this many item lines" | count NOTE lines under `--max` and print `LINT MORE` when cut | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-swarm lint` output | line grammar is key=value but no machine-readable emission | add `--json` flag emitting one JSON object per LINT line | S |

## Top 3 fixes
1. Unify refusal wording: every refusal names the next command verbatim (missing-flag/`-file refusals omit the `run: nova-swarm help <verb>` line).
2. Make `lint --max` actually cap NOTE lines, so help's claim is true.
3. Add `--json` to lint output for non-interactive consumers.

## Good, keep
The bare invocation and every refusal exit 2 and name the cause; the unknown-verb/flag refusals also name the next command, so a cold AI can recover. Lint output is a stable `LINT OK|NOTE|DRIFT` line grammar with `card= checks= bytes= cap=` fields, identical across runs (idempotent). Help is complete per verb (usage, flags, example, exit codes, `effect:` line) and matched what ran, apart from the `--max` claim above. Old 2026-10-02 findings #8 (missing-flag refusal lacks `run:` line) and #6 (`lint --max` does not cap NOTE lines) are both confirmed still present in this build.

## What could not be tried
- `native`, `member`, `step`, `slots`, `worker`, `verify`, `doctor`, `profile`, `mirror`, `install`, `uninstall`, `disk-guard`: not run, per this card's instruction `no run, no lease, no slot taken`; they need a harness, sprint server, store, or system state this review must not touch.
- `--dry-run` and `--json` were verified to exist only on verbs outside this card's scope (`install`, `uninstall`, `disk-guard` offer `--dry-run`; no verb in scope offers `--json`).
