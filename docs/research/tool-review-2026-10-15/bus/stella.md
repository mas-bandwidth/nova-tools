# nova-bus AI-usability review, 2026-10-15

Reviewer: gpt-6-luna
Build: c0b5300f55cd
Score: 7.5/10 (41/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 3 | `nova-bus names --as <me> --redis 127.0.0.1:6381 --timeout 5s` -> `NAMES OK count=21 proven=3` |
| 2 | refusals | 5 | `nova-bus wait --timeout nope` -> `WAIT REFUSED: invalid value for --timeout: it wants a duration such as 30s or 5m (how long to wait before WAIT NONE, a Go duration (1s, 2m); 0 is for ever); run: nova-bus wait -h` |
| 3 | output | 4 | `nova-bus peek --as <me> --redis 127.0.0.1:6381 --timeout 5s --json` -> `{"facts":{"pending":0,"new":3}}` |
| 4 | dry-run | 2 | `nova-bus overdue --dry-run` -> `OVERDUE REFUSED: unknown flag --dry-run; the flags of overdue are --as, --json, --max, --older, --redis, --timeout; run: nova-bus overdue -h` |
| 5 | idempotent | 2 | `nova-bus help` -> `send ... [--token <t>]`; writes were not executed |
| 6 | no prompts | 5 | `nova-bus help` -> `Every verb takes --json` |
| 7 | exit codes | 4 | `nova-bus names --as <me> --redis 127.0.0.1:6381 --timeout 5s` -> `NAMES OK count=21 proven=3` (exit 0); invalid flag/value and bare invocation exit 2 |
| 8 | one word | 5 | `nova-bus help` -> `the verbs are wait, send, peek, recv, ack, receipts, overdue, asks, log, names, version` |
| 9 | push | 4 | `nova-bus help wait` -> `reads your stream past the cursor with XREAD` |
| 10 | no leaks | 4 | `nova-bus names --as <me> --redis 127.0.0.1:6381 --timeout 5s` -> `NAMES OK count=21 proven=3`; no secret appeared in captured safe output |
| 11 | true help | 3 | `nova-bus overdue --dry-run` -> `OVERDUE REFUSED: unknown flag --dry-run; the flags of overdue are --as, --json, --max, --older, --redis, --timeout; run: nova-bus overdue -h`; help says expiry sends timeout messages |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus overdue --dry-run` | Help describes overdue as expiring asks by sending a timeout message, but this write-capable verb refuses `--dry-run` as an unknown flag. A safe rehearsal cannot establish what it would change. | Add a dry-run mode that reports timeout messages without writing them, and pin that no-write behavior in a test. | S |
| 2 | `nova-bus help` first-run example | The older 2026-10-02 cold-start finding remains: help shows a send loop but does not explain the roster and hook setup needed before a name can send. Current help says every verb needs a hooked identity, but gives no setup path. | Add a safe, complete first-run path that provisions and verifies a test identity before the send example. | S |
| 3 | `nova-bus names` | With no configured login user, the first attempt refuses because `--as` is required; the remedy is generic help, which does not identify how to discover the required bus identity. | Include a direct, safe identity-discovery command in the refusal. | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus help` | The first successful read required manually supplying both an acting name and store address. | Add a short first-run path that explains how to obtain these values without guessing. | S |
| 2 | `nova-bus wait` | Help describes cursor and wake-file rearming in depth, but the read-only probes could not exercise the blocking and restart paths. | Add focused examples covering a timeout and a rearm from returned cursors. | S |

## Top 3 fixes
1. Add and test `overdue --dry-run` so the ask-expiry write can be inspected safely.
2. Document and verify the complete first-run roster and hook setup before the send example.
3. Make missing `--as` errors name a safe identity-discovery command.

## Good, keep
Refusals consistently identify the cause and a relevant next help command; the tested parser failures exit 2.
The names and peek reads produced concise text and JSON summaries without requiring a write.
Wait documents cursor rearming and uses XREAD rather than a consumer-group read that would consume later delivery.

## What could not be tried
`send` was tested only as a parser refusal with missing `--to` and `--dry-run`; no send was attempted against the live store. `recv`, `ack`, and `overdue` were not run in their write-capable forms because they can change delivery or send timeout messages; overdue's `--dry-run` refusal was tested. `wait`, `receipts`, `asks`, and `log` were not run against live state because the authorized live-read scope was names and peek only. The successful live probes used the explicitly authorized store and an acting-name placeholder; the acting name had to be supplied because no login user was configured. Source was built and gated on Linux; the copied binary was run for permitted read-only probes on the local host.
