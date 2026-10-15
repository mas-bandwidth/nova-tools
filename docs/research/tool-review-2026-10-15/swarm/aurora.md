# nova-swarm AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.5/10 (53/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-swarm template --name card` -> prints the full card template, exit 0; the bare `nova-swarm` refuses in one line naming its door (`run: nova-swarm help`) |
| 2 | refusals | 5 | `nova-swarm template --name nope` -> `--name wants one of capacity, card, ... worker, got "nope"`; every refusal names the cause and a `run:` door |
| 3 | output | 4 | `nova-swarm lint --card scratchcard.md` -> `LINT DRIFT card=scratchcard.md result-last: 23: ... remedy=...`; stable key=value line grammar, but lint and version refuse `--json` the standard promises |
| 4 | dry-run | 5 | `n/a: template and lint are inspection verbs that write nothing; the write verbs (native, member, install, slots) were not run per the card` |
| 5 | idempotent | 5 | `nova-swarm template --name card` run twice -> `TEMPLATE STABLE` (identical output, no state); no `--op` write exercised |
| 6 | no prompts | 5 | every invocation returned without waiting on input |
| 7 | exit codes | 5 | `nova-swarm lint --card scratchcard.md` -> exit 1 (drift); `nova-swarm lint` (no --card) -> exit 2; `nova-swarm help` -> exit 0; matches the help table |
| 8 | one word | 5 | verbs are distinct single words (template, lint, member, native, step, verify, doctor, profile, slots, ...); no duplicate verbs or tools |
| 9 | push | 5 | `n/a: the reviewed surface (template, lint, help, version) keeps no state and waits on nothing` |
| 10 | no leaks | 5 | no output line printed a key, secret or credential; the help describes credential handling without exposing any |
| 11 | true help | 4 | the example commands run as printed and `help` exits 0, but the family standard's "every verb accepts --json" does not hold: `nova-swarm version --json` -> `REFUSED: takes no flags`, `nova-swarm lint --json` -> `REFUSED: unknown flag --json` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-swarm lint --json` / `nova-swarm version --json` | REFUSED, though docs/STANDARD says every verb accepts --json and renders the one result value as lines or JSON | add `--json` to lint (and version) so the typed result matches the family contract | M |
| 2 | `nova-swarm lint --json` | `REFUSED: unknown flag --json; the flags of lint are --base-check, --card, ... --trust and 1 more` hides `--typed` behind "and 1 more"; a refusal should name every flag | print all flags, drop the "and N more" elision | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-swarm lint --rules` | the rule list is long; there is no `--json` or `--max` on the rules view to bound the read | bound/narrow the `--rules` listing with `--max` | S |
| 2 | `nova-swarm version -h` | version help is terse and notes no `--json`, inconsistent with the rest of the family | document version's intended output and flags in its `-h` | S |

## Top 3 fixes
1. Add `--json` to lint and version so the one-result-value/two-renderings contract holds (moves output and true-help scores to 5).
2. Name every flag in refusals; remove the "and 1 more" elision so a refusal is complete in one turn.
3. Bound the `--rules` listing with `--max` (and offer `--json`) so a cold reader can page the rule set.

## Good, keep
Keep the cold onboarding: a bare invocation refuses on one line naming `help`, and `template --name card` prints a whole card that passes `lint --child-rules`, so a stranger's first two commands work with no setup.
Keep the refusal grammar: every refusal names what it wants and a runnable `run:` door, and exit codes (0 done / 1 drift / 2 bad invocation) match the help table exactly.
Keep the deterministic template and the stable `LINT DRIFT`/`LINT SIZE`/`LINT RULE` line grammar; the key=value lines are trivially parseable.

## What could not be tried
- native, member, step, slots, install, uninstall, worker, verify, doctor, profile, disk-guard, mirror: all are writes, launch a worker, take a lease/slot, or touch the machine, and the card forbids a run, a lease and a slot; they are judged from help and `--dry-run` text only.
- `--base-check`, `--decide`, `--fleet`, `--child-rules-file`, `--lineup`, `--trust`, `--p95` on lint: each needs a fleet/ledger/decide fixture not supplied; judged from their `-h` text.
- No 2026-10-02 ratings file for nova-swarm was found under docs/ratings, so no old-defect comparison was possible.
