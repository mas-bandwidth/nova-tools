# nova-sprint AI-usability review, 2026-10-15

Reviewer: mercury-3-preview
Build: b4070856f5c0
Score: 9/10 (49/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-sprint help` -> the example block runs end to end on the mem twin (init, add, start, tick, take, finish, read, land, where all exit 0) |
| 2 | refusals | 5 | `nova-sprint bogusverb` -> `REFUSED: unknown verb bogusverb; ... run: nova-sprint help` |
| 3 | output | 5 | `nova-sprint where --json --redis mem:sprint.mem` -> `{"version": 1, "tables": {}, ...}` one JSON object |
| 4 | dry-run | 2 | `nova-sprint init -h`, `nova-sprint add -h`, `nova-sprint clear -h` -> no `--dry-run` flag; only a subset of writes (land, install, uninstall, cost, stats, adopt, answer, fleet clean, lane) take it |
| 5 | idempotent | 5 | `nova-sprint add --stream s2 --count 1 --one --op mytestop1` twice -> second run: `ADD OK ... op=mytestop1-1 replay=yes` and changes nothing |
| 6 | no prompts | 5 | `nova-sprint init --readers reader-m2 --members m1,m2` -> `INIT OK tables=...` never waits for input |
| 7 | exit codes | 5 | `nova-sprint bogusverb` -> exit 2; `nova-sprint where --redis mem:x.mem` on empty store -> exit 1; `nova-sprint version` -> exit 0, matching the stated table |
| 8 | one word | 4 | `nova-sprint help` -> `fleet down` and `reader away` and `friend down` are described as "the old words ... kept for one release", minor duplicate verbs |
| 9 | push | 4 | `nova-sprint help` -> `inbox --push <dir>` and the server "hears each one on the bus, from the server (nova-hook pushes it in)"; designed around push, but watch/inbox fully not run (server needed) |
| 10 | no leaks | 5 | full run outputs (`FINISH OK ... head=...`, `LAND OK ... tip=...`) print hashes only, no key or secret |
| 11 | true help | 5 | the help example flow ran exactly as printed and exit codes and refusals matched the help's claims |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-sprint add -h`, `nova-sprint init -h`, `nova-sprint clear -h`, `nova-sprint start -h` | `--dry-run` is absent from most write verbs (init, add, clear, start, stop, take, finish, read, merge, accept, stream remove, reader add, set): an AI cannot preview a write before committing it | add a `--dry-run` flag to every write verb that prints the same plan the real run would take and writes nothing | L |
| 2 | `nova-sprint help` | help is one monolithic wall: `90+ verb usage lines print before any flag or example, so a cold AI scrolls heavily to find one verb | add a compact verb index and an `example:` block near the top, or a `help <verb>` pointer in the first lines | S |
| 3 | `nova-sprint add --stream s9 --plan .` | exits 0 and prints nothing (silent plan), so an AI gets no feedback that the preview ran | always print at least one summary or `PLAN OK` line | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-sprint help` | legacy alias verbs (`fleet down`, `reader away`, `friend down`) duplicate `hold --return` / `unhold` for "one release" | remove the old words once the migration window closes | S |

## Top 3 fixes
1. Add `--dry-run` to every write verb so an AI can preview any change.
2. Restructure `help` with a verb index and earlier grep, cutting the cold-start scroll.
3. Make `add --plan` always emit a status line instead of silent exit 0.

## Good, keep
Every refusal names its cause and the next command in one line with a single grammar, which is exactly what an AI needs for one-turn recovery. The `--op` idempotency (`replay=yes`) makes retries safe, and the `mem:` twin lets a new AI run the full write flow with no infrastructure. `--json` renders the same value as the typed lines from one result.

## What could not be tried
Live-store writes (take, start, finish, add, merge, clear, stream remove, etc.) were not run against a real Redis because the card forbids touching live state; they were judged through the mem twin and their `-h`. Verbs needing a server or a fleet (`run`, `watch`, `inbox --wait`, `dashboard`, `server`, `install`/`uninstall`, `adopt`, `live`, full `play`, `fsck seat`, `routes`) were judged from help and `--dry-run` only. There is no prior nova-sprint rating in `docs/ratings` (the 2026-10-02 set), so no old defect comparison was possible.
