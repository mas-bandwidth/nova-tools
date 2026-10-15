# nova-bus AI-usability review, 2026-10-15

Reviewer: Claude Sonnet 5.5
Build: cc2f2f8c8148
Score: 8/10 (45/55)

How it was run: the live bus store needs a login this review does not hold (`NOVA_BUS_REDIS=127.0.0.1:6380 nova-bus names --as ada` -> `NAMES REFUSED: ... login refused: NOAUTH Authentication required.`), so every real command ran against a throwaway Redis started for this card, with two names hooked in and two friend rows added from a local file. Nothing touched a live store.

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 3 | `nova-bus help` -> `first run: a Redis at --redis (else NOVA_BUS_REDIS, else fleet:bus)`; the help's own example `nova-bus send --as ada --to bob --subject hello --body "are you there?"` -> `SEND REFUSED: ada is no known name; ...` on a fresh store, and getting hooked in took five steps (hook, prove, serve, config friend add, config apply) that the help does not list |
| 2 | refusals | 4 | `nova-bus send --as ada` -> `SEND REFUSED: --to is required; ...` then `--subject is required` then `the body comes from exactly one of --body <text> or --stdin` (all three at once, exit 2); the remedy is the generic `run: nova-bus help`, not `nova-bus send -h`, except on an unknown flag |
| 3 | output | 4 | `nova-bus log --as ada --max 1 --json` -> `{"result":{"verb":"log","status":"ok","exit":0},"facts":{"total":2},"items":[...],"more":[...]}`; one verb family, one grammar, but `wait --json` has its own envelope and `WAIT MESSAGE` escapes the subject (`HOOK\x20CHALLENGE`) where every other verb quotes it |
| 4 | dry-run | 4 | `nova-bus send --as ada --to bob --subject hello --body "are you there?" --dry-run` -> `SEND OK id=- to=bob ... dry_run=true` (and the log stayed at `total=0`); send, recv and ack have one, `overdue` writes ask timeouts and has none |
| 5 | idempotent | 4 | `nova-bus send ... --token t1` twice -> the same `SEND OK id=01M503BEEH6WEZ7HD0MNP01P20` and one message; the same token with other arguments -> `SEND REFUSED: the token "t1" already sent 01M503BEEH6WEZ7HD0MNP01P20 ... with other arguments`; a send with no `--token` is a new message every time, and the flag is not `--op` |
| 6 | no prompts | 5 | `nova-bus recv --as bob --forever --exec true` run with stdin from /dev/null -> read, ran and acked each message, never asked anything |
| 7 | exit codes | 4 | `nova-bus recv --as bob` with nothing waiting -> `RECV NONE: nothing for bob` exit 1, `nova-bus peek --redis 127.0.0.1:1 --as ada` -> `PEEK REFUSED: ... unreachable` exit 2, as help says; but help says `1 BUS OVERDUE` where the verb prints `OVERDUE OVERDUE count=8` |
| 8 | one word | 4 | `nova-bus help` -> `nova-bus peek`, `recv --dry-run`, `receipts`, `wait` each answer "what waits for me" from a different side, and `asks` and `overdue` both own ask expiry; no duplicate verb, no second tool |
| 9 | push | 5 | `nova-bus wait --as bob --after 0-0 --timeout 1s` -> `WAIT MESSAGE id=01M503BEEH6WEZ7HD0MNP01P20 from=ada subject=hello bytes=14`, a blocking read from a cursor that loses nothing between runs; `recv --forever --exec` delivers on arrival |
| 10 | no leaks | 5 | `nova-bus peek --as ada --redis redis://fakeuser:fakepass123@127.0.0.1:6391` -> `PEEK REFUSED: redis at the address given to this tool (not shown): unreachable: not an address: it is a URL or carries a login` |
| 11 | true help | 3 | `nova-bus names --as ada` with no store named -> `NAMES REFUSED: --redis is required: ... refusing to guess` where help says `else fleet:bus`; `overdue` prints `OVERDUE OVERDUE` where help says `BUS OVERDUE`; the help example names are unknown on a fresh store |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus recv --as bob --all --ack` after an earlier plain `nova-bus recv --as bob --max 2` | `RECV NONE: nothing for bob` exit 1, while `nova-bus peek --as bob` says `PEEK OK pending=3 new=0`: the three are held pending for fifteen minutes and only `ack --id` frees them, which the NONE line never says | when pending is more than 0, print `RECV NONE: nothing new for bob; <n> pending, held until <time>; ack them with: nova-bus ack --as bob --id <id,...>` | S |
| 2 | `nova-bus send --as ada --to bob --subject s --body x` on a store with no friend rows | `SEND REFUSED: ada is no known name; ... add one with nova-config friend add ada --width 1 --tiers flash --as <you>, then nova-config apply`: the flag is the old spelling (`nova-config friend add -h` says `--actor`), no store (`--file` or `--pg`) is named, and it is printed once per name | print `--actor <you>` with the store flag, say it once for every missing name, and make the help example run (name that it needs two rows first) | S |
| 3 | `nova-bus help` (first run line) and `nova-bus names --as ada` | help says `a Redis at --redis (else NOVA_BUS_REDIS, else fleet:bus)`; the run says `NAMES REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row ... cannot be read either; refusing to guess` | reword help to `--redis, else NOVA_BUS_REDIS, else NOVA_SPRINT_REDIS, else the fleet's bus row; no other default`, and the same in each verb's `--redis` flag text | S |
| 4 | `nova-bus peek --as ada` on a store where ada was never hooked | `PEEK FAILED reason=unhooked ... run: nova-hook status --as ada`, then the note `nova-hook hook --as ada --role comms` which on its own ends `HOOK FAILED ... not proven` and is `armed`, not hooked: it also needs `nova-hook prove --as ada --nonce <n>` and a server pass (`nova-hook serve`), none named by the bus help, whose line says only "needs <me> hooked in (nova-hook hook)" | add one line to `nova-bus help` how-it-works: `hooked in = nova-hook hook (long-running) + prove the challenge + a serve pass; see nova-hook help` | S |
| 5 | `nova-bus send --as ada --to bob --subject s --body x --re notanid` | `SEND OK id=01M503CJZ88PM3NM6AGAFZR1HC ...` and the message is stored with `re=notanid`; an ask answered with a wrong `--re` stays `state=open` until it times out, with nobody told; the 2026-10-02 ratings named this unchecked `--re` and it is still there | refuse a `--re` that is not an id in the log: `SEND REFUSED: --re notanid is no message in the log; ids come from nova-bus log or recv` (also under `--dry-run`, which accepts it today) | S |
| 6 | `nova-bus overdue --as ada --older 1s` | prints `OVERDUE OVERDUE count=8 older=1s` (the verb twice) at exit 1, while help says `exit codes: ... 1 BUS OVERDUE, one or more`; the help text a script would match on is not what runs | make the help and the output say the same word (`OVERDUE FOUND`), and keep the `BUS OVERDUE` text out of help | S |
| 7 | `nova-bus wait --as bob --timeout 1s --wake-after 5` | two REFUSED lines, the second ending `run: nova-bus help wait; run: nova-bus help` (two remedies in one line, the first a different command than the second) | one REFUSED line per cause, one remedy: `run: nova-bus help wait` | S |
| 8 | `nova-bus wait --as bob --after 0-0 --skip-subject x` against a subject with spaces and `=` | `WAIT MESSAGE id=... from=ada subject=HOOK\x20CHALLENGE\x20nonce\x3de9ec2b84941d2046 bytes=301`, where `recv`, `peek` and `log` print `subject="HOOK CHALLENGE nonce=e9ec..."` | print the subject as the other verbs do (quoted, `%q`-style) so one parser reads every verb | S |
| 9 | `nova-bus send --as ada --to bob` and `nova-bus ack --as bob` | `SEND REFUSED: --subject is required; ... run: nova-bus help` and `ACK REFUSED: --id is required; ... run: nova-bus help`: the next command is the whole-tool help, not `nova-bus send -h` / `nova-bus ack -h` which the unknown-flag refusal does name | make every flag-level refusal end `run: nova-bus <verb> -h` | S |
| 10 | `nova-bus wait --as bob --after 9999999999999-0 --timeout 1s --json` and `nova-bus recv --as bob --json` (nothing waiting) | `{"status":"ok","word":"NONE",...}` at exit 1 for wait, and `{"result":{"verb":"recv","status":"failed","exit":1,"word":"NONE",...}` for recv: two shapes and two statuses for the same "nothing came"; the 2026-10-02 ratings named wait's own bare object and it is still there | give `wait --json` the `result`/`facts` envelope every other verb uses, and one status word for NONE | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus overdue --as ada` | the verb is labelled inspection by its name but sends one timeout message per expired ask (help says so, `effect: delivery`), and has no `--dry-run` | add `--dry-run` that lists the timeouts it would send | M |
| 2 | `nova-bus asks --as bob --liveness` | `ASKS LIVE name=bob state=down ask=- age=-` for a name `names` reports `state=proven`; the two words disagree | print `state=unasked` when no ask has been sent to the name | S |
| 3 | `nova-bus ack --as bob --id bad --dry-run` | `ACK ID id=bad acked=false` for an id that is not an id at all, same as a real id acked yesterday | name the not-an-id case: `acked=false reason=malformed` | S |
| 4 | `nova-bus send ... --dry-run` | text prints `id=-`, JSON prints `"id":""` | print `null` or leave the key out | S |
| 5 | `nova-bus receipts --as bob --id zzz` | `RECEIPTS RECEIPT id=zzz state=none age=-` for an id that was never a message | same `reason=` as above | S |
| 7 | `nova-bus help wait` | the default `--skip-subject PING,PONG` is a prefix match, so a real subject such as `pinging about the deploy` is skipped silently and only moves the cursor (named in the 2026-10-02 ratings, still the help's wording) | match the whole word, or print the skipped count in `WAIT OK` | S |
| 6 | `nova-bus send --as ada --to bob` with no `--token` | a lost answer and a retry sends twice; the token is opt-in | an unattended send under a harness could mint a token from the arguments by default | M |

## Top 3 fixes
1. Make the first run work from the help alone: blockers 3, 4 and 2 together (the `--redis` truth, the hooked-in recipe, a roster remedy that runs), the three places a stranger stops on a fresh store.
2. Tell the reader what is held: blocker 1, a `RECV NONE` that names the pending messages and the ack command, because a plain `recv` is the one thing that leaves messages stranded.
3. Refuse the silent wrong input: blocker 5, a `--re` that matches nothing, because a mistyped answer id leaves an ask open with no word.

## Good, keep
Every refusal names its causes together and a command; `--dry-run` on send, recv and ack writes nothing and prints the line a real run would; `wait` returns a cursor that loses nothing between runs and takes nothing from the group; `send --token` answers a retry with the first send's line and refuses the same token on other arguments, naming the message that went; credentials in a `--redis` value are refused and "(not shown)".

## What could not be tried
The live bus store: it answers `NOAUTH` and this review holds no login for it, so `names`, `peek`, `log`, `asks`, `receipts` and `wait` were run on a throwaway Redis, not on the fleet's traffic. Nothing live was sent, claimed or acked. On the throwaway store `recv --exec`, `recv --forever`, `ack`, `overdue` (with its timeouts) and `send` ran for real. Not run: `recv --forever` past a signal (stopped by timeout at the pipe), `wait --wake-file` with a file that grows (only the missing-file and a bad cursor were run), `send --stdin` of a body larger than 1 MiB.
