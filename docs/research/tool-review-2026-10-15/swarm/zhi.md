# nova-swarm AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 8.5/10 (46/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-swarm template --name card` -> `RESULT: <label> sha=<sha12>` (a whole usable card from the help alone, no other doc) |
| 2 | refusals | 4 | `nova-swarm template --name bogus` -> `nova-swarm template: --name wants one of ... got "bogus"` (names the cause but no `run:` next command; some refusals use `REFUSED: ...; run:` and others plain prose) |
| 3 | output | 4 | `nova-swarm lint --card goodcard.md --child-rules --json` -> `nova-swarm lint REFUSED: unknown flag --json; ... run: nova-swarm help lint` (line grammar is stable and parseable, but no verb accepts `--json`) |
| 4 | dry-run | 4 | `nova-swarm uninstall disk-guard --dry-run --dir ./no-unit-here` -> `UNINSTALL DISK-GUARD DRY-RUN unit=... present=false; nothing was unloaded or removed` (present on the write verbs; slots and native/member have no dry-run surface) |
| 5 | idempotent | 1 | `nova-swarm install -h` -> no `--op` flag on any verb (a identical, write is not repeatable under an op id) |
| 6 | no prompts | 5 | `nova-swarm lint --rules` -> runs to completion with no interactive wait; every input is a flag |
| 7 | exit codes | 5 | `nova-swarm lint --card goodcard.md --typed` -> `LINT DRIFT ...` exit 1; clean lint exits 0, refusals exit 2, as help states |
| 8 | one word | 5 | `nova-swarm help` -> lists 15 single-token verbs (template, lint, member, native, step, disk-guard, mirror, install, uninstall, worker, verify, doctor, profile, slots, version), no duplicates |
| 9 | push | 3 | `nova-swarm member -h` -> `Each tick beats, reads the queue` (the member polls the sprint server; waits are polled, not pushed) |
| 10 | no leaks | 5 | `nova-swarm worker -h` -> `secret names an environment variable` (key_file read as data, never sourced; no observed output line carried a secret) |
| 11 | true help | 5 | `nova-swarm lint --card card.template.md --child-rules` -> `LINT OK ... checks=45` exit 0 (help's claim that `template --name card` passes lint --child-rules holds) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-swarm template --name bogus` | `nova-swarm template: --name wants one of capacity, card, ... got "bogus"` — names the cause but no `run:` next command, and the missing-flag/bad-value refusals drop the `REFUSED:` prefix the unknown-flag/unknown-verb refusals use. Carry-over from the 2026-10-02 ratings. | append `; run: nova-swarm help template` and use the one `REFUSED: <why>; run: <remedy>` grammar | S |
| 2 | `nova-swarm lint --card goodcard.md --child-rules --json` | `nova-swarm lint REFUSED: unknown flag --json` — no verb accepts `--json`, though the repository's own standard says every verb does; an AI that asks for JSON is refused. Carry-over from the 2026-10-02 ratings. | route every verb through the shared JSON encoder in `pkg/tool` | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-swarm install -h` | no write verb takes `--op`, so a retried write (install, disk-guard, step, slots take) is not idempotent under an op id | add `--op` to the write verbs | M |
| 2 | `nova-swarm lint --bogus` | `the flags of lint are --base-check, --card, ... and 1 more` — the flag list is truncated, hiding the last flag (`--typed`) | list every flag without the `and 1 more` elision | S |
| 3 | `nova-swarm install disk-guard --dry-run` | prints the unit as one raw escaped line (`"[Unit]\nDescription=..."`) instead of a readable block | format the dry-run plan as a multi-line block | S |
| 4 | `nova-swarm member -h` | the member polls the sprint queue each tick; waits are polled rather than pushed | consider push-based notification where a queue is waited on | L |

## Top 3 fixes
1. Add `--json` to every verb (moves criterion 3 toward 5; the largest single gap).
2. Make every refusal use the one `REFUSED: <why>; run: <remedy>` grammar and always name the next command (moves criterion 2 toward 5).
3. Add `--op` to the write verbs for idempotent retry (moves criterion 5).

## Good, keep
- The cold first run is genuinely smooth: a bare invocation names the door and the verbs, and `template --name card` plus `lint --child-rules` work exactly as help says, with no setup.
- The line grammar is stable and parseable (`LINT OK card=... checks=...`, `LINT DRIFT ... remedy=...`, `DRY-RUN` lines), and exit codes 0/1/2 match the documented table.
- The dry-runs on the write verbs are honest: they print the exact plan and write nothing, and the tool refuses to guess (a missing flag names what it wants).

## What could not be tried
- `native`, `member`: need a harness, model and credentials; not in scope and never launched.
- `step`: writes commits into a checkout; not in scope (no run).
- `disk-guard`, `install`, `uninstall`, `mirror`: write to the machine or the network; only their `--dry-run` was run.
- `slots take/release/run`: take leases on a store; not run (no lease, no slot taken).
- `worker`, `verify`, `doctor`, `profile`: need an input description, a RESULT.md, a binary pair or timeline files that the scope did not fabricate.
