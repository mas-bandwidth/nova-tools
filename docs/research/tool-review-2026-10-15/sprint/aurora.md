# nova-sprint AI-usability review, 2026-10-15

Reviewer: Mercury (inception/mercury-3-preview-1002)
Build: b4070856f5c0
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-sprint init --readers reader-m2 --members m1,m2` -> `INIT OK tables=work,readers,merge,fleet view=sprint readers=reader-m2` |
| 2 | refusals | 5 | `nova-sprint frobnicate` -> `nova-sprint REFUSED: unknown verb frobnicate; ... run: nova-sprint help` |
| 3 | output | 5 | `nova-sprint where --json` -> `{"decisions":{"count":0,...},"landed":1,"all":2,...}` |
| 4 | dry-run | 1 | `nova-sprint add --dry-run` -> `nova-sprint add REFUSED: unknown flag --dry-run` |
| 5 | idempotent | 5 | `nova-sprint add --stream s1 --count 1 --one --op myop1` -> `ADD OK ... op=myop1-1 replay=yes` (second run, nothing changed) |
| 6 | no prompts | 5 | `nova-sprint where` returned immediately with no input, no prompt |
| 7 | exit codes | 5 | `nova-sprint` (bare) exit 2; `nova-sprint card s1-1` exit 0; `nova-sprint take --as m1 --epoch -1` (machine STOPPED) exit 1 |
| 8 | one word | 3 | help names `hold` and its aliases `fleet hold`/`friend hold`, and multiple views `where`/`view`/`dashboard`/`handover` for the same surface |
| 9 | push | 4 | `inbox --wait` wakes on pushed notes from the server hook (help: "the coordinator itself hears each one on the bus, from the server"), not polling |
| 10 | no leaks | 5 | `nova-sprint land --stream s1 --repo-dir work --base sprint/s1` -> `NOTE JEV_API_KEY is absent, so no landed diff was scored` (names the variable, prints no value) |
| 11 | true help | 5 | the banner's `example:` card flow ran end to end in the mem twin and landed a real card; exit table holds |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-sprint add --dry-run` | `nova-sprint add REFUSED: unknown flag --dry-run`; most write verbs (add, start, take, finish, read, drop, hold, unhold, release, accept, rework, resume, merge) have no `--dry-run`, so a write cannot be previewed safely | implement `--dry-run` on every write verb that lacks it, printing the plan from the same code path and writing nothing | L |
| 2 | `nova-sprint add --stream s1 --count 1 --one --op myop1` (replay) | the replay still prints `MOVED s1-2 -> ready` as if something moved; only the summary's `replay=yes` distinguishes it, so a parser watching `MOVED` reads a change that did not happen | on a recorded op replay, print a `REPLAYED`/`NOTE` line instead of the `MOVED` line (or make the `MOVED` line say `replay=yes`) | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-sprint add --bogus x` | the unknown-flag refusal lists flags but stops at `--max and 13 more`, hiding most flags until a second hop to `nova-sprint help add` | print the full flag list in the refusal | S |
| 2 | `nova-sprint help` | the banner is very long; a new AI must scan hundreds of lines to find a verb | keep the banner but add a compact verb index near the top | M |
| 3 | help alias verbs | `fleet hold`/`friend hold` and `fleet down`/`reader away`/`friend down` duplicate `hold`/`hold --return` (documented as "kept for one release") | retire the aliases as announced | M |

## Top 3 fixes
1. Add `--dry-run` to every write verb (moves criterion 4 from 1 toward 4, the single biggest score gain).
2. Make an `--op` replay print a `REPLAYED` line instead of a `MOVED` line so nothing appears to move.
3. Print all flags in the unknown-flag refusal (drop "and N more") so recovery needs no second hop.

## Good, keep
- The banner's `example:` block runs cold from the help alone: in the mem twin, init, add, start, tick, take, finish, read, land and where dealt and landed a real card end to end.
- Every refusal names the cause and the exact next command, and exit codes match the help table: 0 done, 1 a refused operation, 2 usage or a store that did not answer.
- The mem twin (`--redis mem:<file>`) makes every verb tryable without infrastructure, and `--op` replay gives safe retries; no earlier nova-sprint rating exists in docs/ratings to compare (none present under 1.2.0 or snapshots).

## What could not be tried
- Live-store writes: none run (the card forbids writing the live sprint store); judged from help and dry-run only.
- Verbs needing a service or network not available or not allowed: run, tick on a live store, inbox --wait and where --watch (refused in the twin, as help states), server, dashboard --listen, adopt, install/uninstall, seat login/install/fsck, routes and budget (provider APIs), cost reconcile/reprice (need provider cost records), friend sync and fleet sync (need pg config), verify-landed and gh-dependent verbs, play simulation, quack, selftest, demo, backup, snapshot.
