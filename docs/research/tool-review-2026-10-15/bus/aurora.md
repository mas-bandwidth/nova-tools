# nova-bus AI-usability review, 2026-10-15

Reviewer: grok-4.7
Build: 4540d7fd37ca
Score: 7/10 (39/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 3 | `nova-bus wait --as bob --timeout 1s` -> `WAIT REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help` |
| 2 | refusals | 4 | `nova-bus send --kind nope --as ada --to bob --subject hello --body hi` -> `SEND REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help` |
| 3 | output | 4 | `nova-bus version --json` -> `{"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-bus v1.2.26-0.20261015160522-4540d7fd37ca darwin/arm64 go1.26.6"}` |
| 4 | dry-run | 2 | `nova-bus ack --dry-run --id 01ARZ3NDEKTSV4RRFFQ69G5FAV` -> `ACK NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` |
| 5 | idempotent | 1 | `nova-bus send --op abc --as ada --to bob --subject hello --body hi` -> `SEND REFUSED: unknown flag --op; the flags of send are --as, --body, --cc, --deadline, --dry-run, --json, --kind, --re, --redis, --stdin, --subject, --timeout, --to, --token, --token-cleanup, --token-life; run: nova-bus send -h` |
| 6 | no prompts | 5 | `nova-bus send --stdin --redis 127.0.0.1:1 --timeout 2s --as ada --to bob --subject hello` -> `SEND REFUSED: redis at 127.0.0.1:1 as the default user, no password: unreachable: dial tcp 127.0.0.1:1: connect: connection refused; next: start the store or correct the address, which was given to this tool; run: nova-bus help` |
| 7 | exit codes | 4 | `nova-bus` -> `BUS REFUSED: no verb given; the verbs are wait, send, peek, recv, ack, receipts, overdue, asks, log, names, version; run: nova-bus help` (exit 2, as help states for a bad invocation) |
| 8 | one word | 5 | `nova-bus` -> `BUS REFUSED: no verb given; the verbs are wait, send, peek, recv, ack, receipts, overdue, asks, log, names, version; run: nova-bus help` |
| 9 | push | 4 | `nova-bus wait -h` -> `The wait takes nothing: it reads your stream past the cursor with XREAD, never the consumer group,` |
| 10 | no leaks | 5 | `nova-bus names --redis 127.0.0.1:1 --timeout 2s --as bob` -> `NAMES REFUSED: redis at 127.0.0.1:1 as user probeuser (password from NOVA_BUS_PROBE_PW): unreachable: dial tcp 127.0.0.1:1: connect: connection refused; next: start the store or correct the address, which was given to this tool; run: nova-bus help` |
| 11 | true help | 2 | `nova-bus peek --max 1` -> `PEEK REFUSED: unknown flag --max; the flags of peek are --as, --json, --kind, --redis, --timeout; run: nova-bus peek -h` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus ack --dry-run --id 01ARZ3NDEKTSV4RRFFQ69G5FAV` | `ACK NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` (same NOTE on `nova-bus recv --dry-run --as bob`, exit 2). The 2026-10-02 tripwire is still there on ack and recv; send no longer prints it. | Read the dry-run flag before the store opens, keep the real refusal, and never say a write may have happened. | S |
| 2 | `nova-bus send --op abc --as ada --to bob --subject hello --body hi` | `SEND REFUSED: unknown flag --op` while the retry id is `--token`, which this run could not exercise. | Accept `--op` as that retry id on every write. | S |
| 3 | `nova-bus wait --as bob --timeout 1s` | The banner example exits 2: `WAIT REFUSED: --redis is required`. `--dry-run` still dials and prints no plan. | Put `--redis` in the example, and make a dry-run print the plan with nothing dialled and nothing written. | M |
| 4 | `nova-bus overdue --dry-run` | `OVERDUE REFUSED: unknown flag --dry-run` while `overdue -h` says effect delivery: it sends a timeout message. | Add `--dry-run` that lists the timeouts it would send and sends none. | S |
| 5 | `nova-bus help --json` | Banner says every verb takes `--json`. Output is `unknown verb "--json"` (exit 2). `nova-bus --json version` does the same. | Accept `--json` before the verb, and on help. | S |
| 6 | `nova-bus peek --max 1` | Banner says a verb that lists takes `--max`. peek and names refuse `--max`. `nova-bus recv --max 0 --dry-run` says `--max wants a count of at least 1`, not that 0 lists all. Still the 2026-10-02 defect. | Say `--max` only on the verbs that take it, and say recv's `--max` is a take count. | S |
| 7 | `nova-bus send --kind nope --as ada --to bob --subject hello --body hi` | Only the missing store is named. The bad kind is not. send `-h` says dry-run names every problem. | Validate kind, token, and names before the dial, and print every problem in one refusal. | S |
| 8 | `nova-bus peek --max 1 --json` | Text on stderr, not JSON, because `--json` followed the unknown flag. `nova-bus peek --json --max 1` is JSON. | Apply `--json` wherever it sits, including a flag-parse refusal. | S |
| 9 | `nova-bus recv -h` | `effect: delivery: sends beyond this machine` for a take that sends nothing. ack's `-h` says the same. Still the 2026-10-02 wording. | Call recv and ack store writes, and say what each writes. | S |
| 10 | `docs/CLI.md` nova-bus commands | The table omits receipts. It says names prints push=proven, stale, down, or none. `nova-bus names -h` says state is proven, down, live, partial, expired, unhooked, or none, plus role and reason. | Rewrite that section from the binary. | M |
| 11 | `nova-bus send -h` (`--redis`) | Flag text is `host:port` only. A socket path is dialed. The password variable is not named. The banner's first-run line also skips `NOVA_SPRINT_REDIS`, which the refusal names. | One chain, the same words: host:port or an absolute socket path, and the password variable. | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus version --json` | `facts` is empty; the version is only a payload string. | Put version, os, arch, and go in facts. | S |
| 2 | `nova-bus names --also` | The second unknown flag says `did you mean --as?`. | Suggest a flag only when the edit distance is real. | S |
| 3 | `nova-bus send -h` (`--kind`) | The flag lists kinds and does not say an absent kind is status. recv's `-h` does. | Add the default on send's flag, as recv already states it. | S |
| 4 | no quickstart | The only store-free success is `version`. A trial roster would be a new verb. | Leave it out of this round. | L |

## Top 3 fixes
1. Read `--dry-run` before ack and recv open the store, drop the "may have written" note, and give overdue a `--dry-run` that sends nothing.
2. Accept `--op` as the retry id send already has under `--token`, on every write.
3. Make the printed example run (name `--redis`), and make the banner's `--json` and `--max` sentences true of the verbs that exist.

## Good, keep
Missing flags are named together, each saying what it wants (`nova-bus send` names `--to`, `--subject`, and the body, exit 2). An unknown verb names the nearest (`sned` -> send). A store off loopback is refused before a dial. A password error names the variable and not the value. `--forever` without `--exec` is refused with the reason a loop would hand out the same message. send `--dry-run` against a closed port keeps the store refusal and does not add the 2026-10-02 "may have written" note.

## What could not be tried
The environment sets neither `NOVA_BUS_REDIS` nor `NOVA_SPRINT_REDIS`, and no server was started. Live `names`, `peek`, `log`, `asks`, `receipts`, and `wait` all refused before a dial. A real send, ack, or recv was not run (a claim or a send would interrupt a peer); only `--dry-run`, which never reached a store. `overdue` was not run: it sends timeout messages and has no `--dry-run`. Exit 1 (nothing waiting, not hooked in, overdue count above zero) was not seen. `--token` retry, wake-file end of wait, and `--json` success objects for store verbs were not seen. The 2026-10-02 hold-versus-`RECV NONE` and wait-JSON-shape defects could not be re-checked without a store.
