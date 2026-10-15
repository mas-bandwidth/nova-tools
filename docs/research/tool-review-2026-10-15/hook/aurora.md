# nova-hook AI-usability review, 2026-10-15

Reviewer: Mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 8/10 (44/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 2 | `nova-hook status --as m1` -> `STATUS REFUSED: --redis is required: NOVA_BUS_REDIS is unset ... refusing to guess; run: nova-hook help` |
| 2 | refusals | 5 | `nova-hook hook --as m1` -> `HOOK REFUSED: --role is required; it wants the role you hook in as ...; run: nova-hook help` |
| 3 | output | 5 | `nova-hook status --as m1 --json` -> `{"result":{"verb":"status","status":"refused","exit":2,"remedy":"nova-hook help","why":[...]},"facts":{}}` |
| 4 | dry-run | 3 | `nova-hook serve --dry-run` -> `SERVE REFUSED: unknown flag --dry-run; the flags of serve are --as, --down-after, ...` |
| 5 | idempotent | 0 | `nova-hook hook -h` -> no `--op` flag on any write verb (hook, prove, unhook, serve) |
| 6 | no prompts | 5 | `nova-hook help` -> runs to completion, exits 0, no interactive prompt |
| 7 | exit codes | 5 | `nova-hook frobnicate` -> `HOOK REFUSED: unknown verb ...; run: nova-hook help` then `exit=2` |
| 8 | one word | 4 | `nova-hook help` -> `status, hook, prove, unhook, friends, serve, version` (status and friends both list name presence) |
| 9 | push | 5 | `nova-hook hook -h` -> `prints each message on your bus stream as it lands, HOOK PUSH ...` (push, not poll) |
| 10 | no leaks | 5 | `nova-hook status --as m1 --json` -> output contains only refusal text, no secrets/env values |
| 11 | true help | 5 | `nova-hook version --json` -> `{"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-hook v..."}` (every verb takes --json, as claimed) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-hook serve --dry-run` | `SERVE REFUSED: unknown flag --dry-run` — serve is a write (challenges every hooked name, unhooks silent names) but has no dry-run, so a reader cannot preview its effect | add `--dry-run` to serve that prints one pass's plan and writes nothing | S |
| 2 | `nova-hook status --as m1` | `STATUS REFUSED: --redis is required ...` — every verb needs a live Redis bus; a cold AI with no infra cannot get any first run or see any output shape from the tool alone | provide a store-free path (a `--dry-run` or in-memory default) that shows the real output shape without a live store | M |
| 3 | `nova-hook hook --as m1 --role member --dry-run` | `HOOK REFUSED: --redis is required ...` then `HOOK NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` — the dry-run cannot run without a live store, and the NOTE's "it may have written" alarms a reader though nothing was written | let --dry-run read a default/empty store and report the plan without connecting; drop the misleading "may have written" note when nothing was written | M |
| 4 | `nova-hook hook -h` | no `--op` flag anywhere — write verbs (hook, prove, unhook, serve) take no op id, so a retry after a timeout cannot be made safe and idempotent | add `--op <id>` to write verbs so the same op id replays the recorded result instead of re-writing | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-hook status` vs `nova-hook friends` | both verbs list a name's record and presence, an overlapping surface a cold reader has to disambiguate | keep `friends` as the fleet-wide view and let `status` be the one-name record (document which to choose in the banner) | S |
| 2 | `nova-hook help` example block | the example lines (`nova-hook status --as m1`) all need a live store and fail cold | add one example line that runs without a store, or note that a bus must exist first | S |

## Top 3 fixes
1. Add `--dry-run` to `serve` (a write verb with no way to preview) and make dry-runs run without a live store.
2. Add `--op <id>` to the write verbs so retries are idempotent and safe.
3. Provide a store-free first run so a cold reader sees the real output shape without a Redis bus.

## Good, keep
The refusal grammar is exemplary: every refusal names its cause, what the input wants, and a next command in one line, and unknown flags list the real flags.
`--json` is on every verb and renders the same result value as the line grammar, so the two cannot drift.
The banner answers what/how/use in a tight block and the exit-code table is stated in the help and holds when run.

## What could not be tried
Real `hook`, `prove`, `unhook`, and `serve` runs: they write the hook record and need a live Redis bus, which this review must not touch, so they were judged from `-h` and `--dry-run` alone. The serve loop and the harness installs (`--harness grok/muse/claude/dsh/antigravity/codex`) were not run for the same reason.
