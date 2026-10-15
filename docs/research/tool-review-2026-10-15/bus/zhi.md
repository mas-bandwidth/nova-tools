# nova-bus AI-usability review, 2026-10-15

Reviewer: Mercury (mercury-3-preview-1002)
Build: cc2f2f8c8148
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-bus help` -> `usage:` lists every verb, then `exit codes: 0 done, ... 2 could not run` |
| 2 | refusals | 5 | `nova-bus frobnicate` -> `BUS REFUSED: unknown verb "frobnicate"; the verbs are wait, send, peek, recv, ack, receipts, overdue, asks, log, names, version; run: nova-bus help` |
| 3 | output | 4 | `nova-bus version --json` -> `{"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-bus v1.2.26-0.20261015145010-cc2f2f8c8148 darwin/amd64 go1.26.6"}` |
| 4 | dry-run | 4 | `nova-bus send --as ada --to bob --subject hello --body "are you there?" --dry-run` -> `SEND REFUSED: --redis is required: NOVA_BUS_REDIS is unset ...` |
| 5 | idempotent | 4 | `nova-bus ack -h` -> `false when the id is not pending for you ... so acking twice is safe and exits 0`; `nova-bus send -h` -> `--token <t> makes the send safe to retry` |
| 6 | no prompts | 5 | `nova-bus help` -> all verbs are flag-driven; no verb waits on an interactive answer |
| 7 | exit codes | 4 | `nova-bus names --redis 127.0.0.1:6379` -> `NAMES REFUSED: redis at ... unreachable ...` then `EXIT=2` (0 and 2 verified; 1 not reachable without a live store) |
| 8 | one word | 5 | `nova-bus help` -> verbs are wait, send, peek, recv, ack, receipts, overdue, asks, log, names, version, help: all one word, none duplicate |
| 9 | push | 5 | `nova-bus wait -h` -> `it reads your stream past the cursor with XREAD, never the consumer group`; `recv --forever` blocks, no polling |
| 10 | no leaks | 4 | `nova-bus names --redis 127.0.0.1:6379` -> refusal prints only the user-given address and `default user, no password`, no secret |
| 11 | true help | 3 | `nova-bus peek --as ada --max 1` -> `PEEK REFUSED: unknown flag --max; the flags of peek are --as, --json, --kind, --redis, --timeout` while the banner claims `A verb that lists takes --max <n>` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus send --as ada --to bob --subject hello --body "are you there?" --dry-run` | `SEND REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row ... cannot be read either; refusing to guess; run: nova-bus help` — even the dry run of the one write verb cannot run and a live Redis, a nova-config roster and a hook, so a cold AI cannot see any real verb's plan before adopting the tool. a 2026-10-02 finding that is still here | let `send --dry-run` (and the other write verbs' dry-runs) validate and print the plan without a live store, so the first try needs no infrastructure | M |
| 2 | `nova-bus send --as ada --to bob --subject s --body b --kind bogus` | `SEND REFUSED: --redis is required ...` — the bad `--kind` value is not named; the connection refusal masks it, so a typo passes the parser and is only discovered (if at all) after connecting. a 2026-10-02 finding that is still here | validate `--kind` and the other enumerated flags at parse time, name the bad value and the door (`nova-bus send -h`) | S |
| 3 | `nova-bus peek --as ada --max 1` (and `nova-bus names --as ada --max 1`) | `PEEK REFUSED: unknown flag --max; the flags of peek are --as, --json, --kind, --redis, --timeout` — the banner says `A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest`, but peek and names list and refuse `--max`. a 2026-10-02 finding that is still here | give `peek` and `names` `--max` and a `MORE` line, or say in the banner which listing verbs take it | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus send -h` | write verbs use `--token` (send) and idempotent `ack` instead of the family's `--op <id>` | document the equivalence in `help`, or accept `--op` as an alias so the retry guarantee is discoverable | S |
| 2 | `nova-bus help` | there is no store-free `quickstart`/`sample` verb; the first run always needs a Redis, config rows and hooks | add a `sample` verb that runs against an ephemeral in-process store | L |
| 3 | `nova-bus recv --forever` | `--forever` requires `--exec`; the help explains why but a loop that acks nothing would be useful for preview | consider a documented no-ack preview mode | S |

## Top 3 fixes
1. Make every write verb's `--dry-run` runnable without a live store, so a cold AI sees the plan before adopting (moves criterion 1 and 4 and the cold first run).
2. Validate enumerated flag values (`--kind` and the like) at parse time, naming the bad value and the door, so a typo is refused immediately instead of masked by the connection refusal.
3. Align the banner's `--max` claim with reality for the listing verbs (`peek`, `names`), so help is true as run (moves criterion 11).

## Good, keep
The refusal grammar is exemplary: every refusal names its cause and the next command, in one line, and one run reports the first problem it finds. The help banner is dense but complete: what the tool does, how it works, a first run, every verb's usage, the exit table and worked examples. Output is consistently `VERB status key=value` with a matching `--json` rendering of the same one value. Three 2026-10-02 defects are still here (the store-free dry run, the kind not checked at parse, the `--max` claim on listing verbs), all three now blockers above.

## What could not be tried
The live verbs (wait, a real send, peek, a real recv, a real ack, receipts, overdue, asks, log, names) could not run against a real store: this environment names no bus store (no `NOVA_BUS_REDIS`, no `NOVA_SPRINT_REDIS`, no fleet bus config, no Redis listening) and starting a server is forbidden. Only parse-time behavior, help, version, refusals and the store-unreachable refusal were exercised; the exit-1 path and the write-dry-run "writes nothing" guarantee could not be verified against a live store.
