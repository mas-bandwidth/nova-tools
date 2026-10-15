# nova-bus AI-usability review summary, 2026-10-15

Reviews: 4 (R1..R4, in byte order of file name)
Build: 4540d7fd37ca, c0b5300f55cd, cc2f2f8c8148, de16887455af
Score: 7.75/10 (range 7-8.5)

## Rubric
| # | criterion | median | range | the evidence that decided it |
|---|---|---|---|---|
| 1 | cold | 3 | 2-5 | `nova-bus wait --as bob --timeout 1s` -> `WAIT REFUSED: --redis is required ...; run: nova-bus help` (exit 2); the printed example does not run store-free |
| 2 | refusals | 5 | 4-5 | `nova-bus send` -> `SEND REFUSED: --to is required; it wants the recipients ...; run: nova-bus help`, one problem per line |
| 3 | output | 4 | 4-5 | `nova-bus version --json` -> `{"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":...}`; stable line/JSON pair |
| 4 | dry-run | 2.5 | 2-4 | `nova-bus ack --dry-run --id ...` -> `ACK NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` |
| 5 | idempotent | 2 | 1-4 | `nova-bus send --op 123` -> `SEND REFUSED: unknown flag --op`, the family standard retry id is absent |
| 6 | no prompts | 5 | 5-5 | `nova-bus help` runs to completion; every verb is flag-driven, none waits on an answer |
| 7 | exit codes | 4 | 4-5 | bare invocation and bad flags exit 2 as help states (`BUS REFUSED: no verb given`, exit 2) |
| 8 | one word | 5 | 4-5 | `nova-bus help` -> `the verbs are wait, send, peek, recv, ack, receipts, overdue, asks, log, names, version`; all one word, none duplicate |
| 9 | push | 4.5 | 4-5 | `nova-bus wait -h` -> `reads your stream past the cursor with XREAD, never the consumer group`; blocking, no polling |
| 10 | no leaks | 4.5 | 4-5 | store refusal prints only the user-given address and `default user, no password`; no secret value leaks |
| 11 | true help | 3 | 2-3 | `nova-bus peek --max 1` -> `PEEK REFUSED: unknown flag --max; the flags of peek are ...` while the banner says `A verb that lists takes --max <n>` |

## Consensus 10/10 blockers
| # | where | what an AI meets | fix | size | named by |
|---|---|---|---|---|
| 1 | `nova-bus wait --as bob --timeout 1s` and every write verb's `--dry-run` | the cold first run fails exit 2 needing a live store and a roster and hook; a new AI cannot run a single demonstration command | let the write verbs' `--dry-run` validate and print the plan without a live store, and give the printed example a runnable store-free form | M | R1,R2,R4 |
| 2 | `nova-bus ack --dry-run --id 01ARZ3NDEKTSV4RRFFQ69G5FAV` | `ACK NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` (same NOTE on `recv --dry-run`) | read `--dry-run` before the store opens, keep the real refusal, and never say a write may have happened | S | R1,R2 |
| 3 | `nova-bus send --op abc --as ada --to bob --subject hello --body hi` | `SEND REFUSED: unknown flag --op; the flags of send are ...` while the family standard retry id is `--op <id>` (send uses `--token`) | accept `--op` as the retry id on every write verb (alias `--token`) | S | R1,R2 |
| 4 | `nova-bus overdue --dry-run` | `OVERDUE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written`; there is no preview of the timeouts it would send | make `overdue --dry-run` list the timeouts it would send and send none | S | R1,R2 |
| 5 | `nova-bus help --json` | the banner says every verb takes `--json`, but `--json` before the verb is `unknown verb "--json"` (exit 2) | accept `--json` at the root before the verb, and on help | S | R1,R2 |
| 6 | `nova-bus peek --max 1` (and `nova-bus names --max 1`) | `PEEK REFUSED: unknown flag --max; the flags of peek are --as, --json, --kind, --redis, --timeout` while the banner says `A verb that lists takes --max <n>` | say `--max` only on the verbs that take it, or give peek/names `--max` and a `MORE` line | S | R1,R4 |
| 7 | `nova-bus send --kind nope --as ada --to bob --subject hello --body hi` | only the missing store is named; the bad `--kind` value is masked by the connection refusal | validate enumerated flag values (`--kind` and the like) at parse time and name the bad value and the door | S | R1,R4 |
| 8 | `nova-bus help` send example | the send example needs a roster and hook that help does not set up or explain; the first successful read requires guessing an acting name and store address | add a safe, complete first-run path that provisions and verifies a test identity before the send example | S | R3 |
| 9 | `docs/CLI.md` nova-bus commands | the table omits receipts and says names prints `push=proven|stale|down|none`; the binary reports `proven, down, live, partial, expired, unhooked, none` plus role and reason | rewrite that section from the binary | M | R1 |
| 10 | `nova-bus send` (required flags) | required-flag refusals end `run: nova-bus help` instead of `run: nova-bus send -h` | end per-verb required-flag refusals with `run: nova-bus <verb> -h` | S | R2 |
| 11 | `nova-bus send -h` (`--redis`) | flag text is `host:port` only; a socket path is dialed and the password variable is not named | one chain, the same words: host:port or an absolute socket path, and the password variable | S | R1 |
| 12 | `nova-bus recv -h` | `effect: delivery: sends beyond this machine` for a take that sends nothing (ack's `-h` says the same) | call recv and ack store writes and say what each writes | S | R1 |
| 13 | `nova-bus version -h` | the exit-code summary is shared verbatim across verbs, citing `recv: nothing waiting` and `wait: nothing came` even for inspection verbs | tailor the exit-code description to each verb | S | R2 |
| 14 | `nova-bus names` | with no configured login user the first attempt refuses because `--as` is required; the remedy is generic help and does not name how to discover the required bus identity | include a direct, safe identity-discovery command in the refusal | S | R3 |
| 15 | `nova-bus peek --max 1 --json` | text, not JSON, when `--json` follows the unknown flag in the argument order | apply `--json` wherever it sits, including a flag-parse refusal | S | R1 |

## Top 3 fixes
1. Make every write verb's `--dry-run` runnable without a live store: validate, print the plan and write nothing, and never print the "may have written" note (blockers 1, 2, 4; moves cold, dry-run and the cold first run).
2. Accept the standard `--op <id>` retry id on every write verb, aliasing send's `--token` (blocker 3; moves idempotent).
3. Make help true as run: align the banner's `--max` and `--json` claims with the verbs that exist, and validate enumerated flag values at parse time (blockers 5, 6, 7; moves true help).

## Disagreements
cold (2-5), idempotent (1-4) and dry-run (2-4) were scored two or more points apart. Rerun holds the lower readings: `nova-bus wait --as bob --timeout 1s` still exits 2 because `--redis is required` and the printed example does not run store-free; `nova-bus send --op 123` still refuses `unknown flag --op`; `ack`/`recv`/`overdue --dry-run` still print `may have written`, and  `--dry-run` is not read. `overdue --dry-run` changed since the reviews: it no longer refuses with `unknown flag --dry-run`; the flag is now accepted but unread, printing the same `may have written` NOTE, so that blocker folds into blocker 2. Blockers 1, 2, 3, 4, 5, 6, 7, 9, 10, 11, 12, 13, 14, 15 were rerun from the head build and reproduce; blocker 8 (the first-run help gap) is a documentation/onboarding judgment and could not be rerun as a failing command.

## Nice-to-haves
- `version --json`: `facts` is empty; version/os/arch/go belong in facts.
- Unknown-flag suggestion (`names --also`) suggests `--as` regardless of edit distance.
- `send -h` `--kind` does not state an absent kind is a status (recv does).
- The overlap between `names` and `nova-hook friends` is not reconciled in help.
- `wait -h` `--wake-after` uses internal jargon (`No replay is an LLM receipt`).
- Refusals for a missing acting name could name a safe identity-discovery command.
- Consider a store-free `sample`/`quickstart` verb against an ephemeral in-process store.
- `recv --forever` could offer a documented no-ack preview mode.
