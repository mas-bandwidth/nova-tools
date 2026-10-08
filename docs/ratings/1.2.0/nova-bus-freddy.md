# nova-bus READ and USE rating, nova-tools 1.2.0

Rater: inception/mercury-2.5, harness opencode
Build: 24e5b7120198
READ: 7.5/10
USE: 7.5/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. `nova-bus version` prints the build stamp. Read cold from `nova-bus help`, every verb's `-h` and docs/SPEC-BUS.md, then built and used against a throwaway Redis store in a scratch directory. No live store was touched. The nova-bus rated at 1.1.0 was the git bus; it was removed on 2026-10-04, and this is the Redis-streams bus that took its name.

## Reasons

READ 7.5. The banner says what the tool is in one line, then gives the loop a harness runs in three lines, then the keys, the exit table, the `--json` contract and example lines. Every verb's `-h` carries its banner usage line, a paragraph naming each line it prints and what each field means, the flags, the exit codes and an `effect:` line. Wait's and recv's paragraphs are among the best in the family: they say who is handed a message again and when, and that wait reads past a cursor without taking anything. The refusal for an unknown verb or flag names the whole list and points at `-h`. That is the shape an AI wants.

What keeps READ at 7.5. The banner's `first run:` line says "a Redis naming ada and bob" and nothing anywhere in the help says what naming is: the sets `friends` and `machines` (only in docs/SPEC-BUS.md), plus a fresh push proof on `bus2:push` for every sender and recipient, without which every send and recv is refused as `deaf:`. A cold reader with a scratch Redis cannot get the banner's example past its first send from the help alone. The banner says "A verb that lists takes --max <n> (default 20, 0 lists all)", and peek and names, which list, refuse `--max`; the spec says it plainly. Every `--redis` flag says "host:port", and the tool also takes the absolute path of a Unix socket. Every verb's `-h` repeats the same generic exit line naming recv and wait, so send's `-h` explains recv's exit 1.

USE 7.5. With the roster and proofs seeded by hand, the whole loop ran: send, peek, wait (cursor, timeout, wake file, PING skipping, a message from oneself skipped), recv plain, `--dry-run`, `--exec`, `--all`, `--ack`, `--kind`, `--forever` stopped by SIGTERM with exit 0, ack (twice is safe, `--dry-run` says what is pending), log with `--max`, a MORE line and `--bodies`, names with proven, stale, down and none. Every result has `--json`, refusals included. Every send line carries bytes and a sha256 of the body as stored, and `login=none` makes an open store's weakness visible. With a login user, `--as` is pinned to it and a spoof is refused; a wrong password and an empty password variable each get a refusal naming the variable and what to do. Unknown names, bad names, a body over 1 MiB and a body from both sources are refused before anything is written, and deaf names are listed all at once.

What keeps USE at 7.5. Inputs that should be refused pass: `--re` with a bad id sends, a two-line `--subject` is stored as `two\nlines` though the flag says one line, peek for a name no roster holds is `PEEK OK pending=0 new=0`, and a `--wake-file` under a missing directory waits silently to its timeout. A push proof that is not JSON reads as `push=none age=never`, the same as a name no daemon ever wrote. After `recv --exec false` prints `RECV FAILED ... stays pending`, the message is hidden from every recv for fifteen minutes and nothing on the line says so. recv's `--json` payload begins with the blank line that separates header from body in the text form. `recv --all --json` prints one object per message where the banner says one object. An empty `--subject` stops at "--subject is required" and does not name the empty body and the bad `--kind` in the same run.

A 10 would make the banner's first run work cold (say in the banner how to name ada and bob and prove their push on a scratch store, or give a trial mode), make the `--max` line and the `--redis` flag text true, give each verb its own exit line and example, refuse a malformed `--re`, a multi-line subject and a wake file whose directory is missing, tell a garbage proof from no proof, say on RECV FAILED when the message comes back, and carry the body alone in the JSON payload.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus help` | `first run: a Redis naming ada and bob` does not say what naming is; on a scratch store every banner example fails. The sets `friends`/`machines` and the `bus2:push` proof are only in the spec. | Say in the banner what a trial store needs (the two sets and a push proof per name), or add a verb that seeds a scratch store. | M |
| 2 | `nova-bus peek --as bob --max 1`, `nova-bus names --max 1` | `unknown flag --max`; the banner says "A verb that lists takes --max <n>" and both verbs list. | Add `--max` and a MORE line to peek and names, or print the sentence only for verbs that take it. | S |
| 3 | `nova-bus send -h` and every verb | `--redis` says "the Redis address, host:port"; an absolute Unix socket path works. | Say `host:port or the absolute path of a Unix socket` in the flag text. | S |
| 4 | every verb's `-h` | The exit line is the banner's generic one on send, ack, log, names and version, which have no exit 1 of their own. | Give each verb its own exit line, as wait's `-h` already does. | S |
| 5 | `nova-bus send -h`, `recv -h`, `peek -h`, `ack -h`, `log -h`, `names -h` | Not all verbs have an `example:` line in their `-h`. Wait's has one; receipts and overdue now have `example:` lines in their Detail text (cmd/nova-bus/main.go:373, 395); send, recv, peek, ack, log and names still lack them. | Add an `example:` line to every verb's `-h`. | S |
| 6 | `nova-bus send --as bob --to ada --subject x --body y --re not-an-id` | `SEND OK`, and log shows `re=not-an-id`; `--re re` sends too. | Refuse a `--re` that is not a ULID, and say whether it must name a message on the log. | S |
| 7 | `nova-bus send --as ada --to bob --subject $'two\nlines' --body b` | `SEND OK`; log, peek and recv print `subject="two\nlines"`, though the flag says "one line". | Refuse a subject holding a newline, with the other problems of the message. | S |
| 8 | `nova-bus send --as ada --to bob --subject "" --body "" --kind nope` | One refusal, `--subject is required`; the empty body and the bad kind are not named. | Let the flag check fall through to the message's own checks so one run names all three. | S |
| 9 | `nova-bus peek --as nobody` | For a name no roster holds, `PEEK OK pending=0 new=0` exit 0; wait, send and recv refuse the same name as `no known name`. | Refuse an unknown name on peek and ack as the other verbs do. | S |
| 10 | `nova-bus wait --as bob --timeout 1s --wake-file nope/missing.txt` | A wake file under a directory that does not exist is accepted and the wait runs to `WAIT NONE`. | Say in `-h` that the file may appear later; refuse when its directory is missing. | S |
| 11 | `nova-bus names` with `bus2:push` field `box1` = `garbage` | Prints `push=none age=never harness=-`, the line for a name no daemon ever wrote. | Report a value that is no proof as its own state (`push=bad`) with the remedy to rewrite it. | S |
| 12 | `nova-bus recv --as bob --all --exec false`, then `recv --as bob --all --ack` | The first prints `RECV FAILED ... --exec exited 1, so the message stays pending`; the second is `RECV NONE` because the failed message is held for fifteen minutes, and neither line says when it comes back. | Add `retry_after=15m` (or the time) to the RECV FAILED line, and say it in recv's `-h`. | S |
| 13 | `nova-bus recv --as ada --all --json` | `"payload":"\ny"`: the JSON payload starts with the text form's separating blank line. | Carry the body alone in the payload. | S |
| 14 | `nova-bus recv --as ada --all --json` | Prints one JSON object per message; the banner says "the same result as one JSON object on stdout". | Say in the banner that a batch prints one object per line, or wrap a batch in one object. | S |
| 15 | `nova-bus recv --as bob` with nothing waiting | `RECV NONE` goes to standard error; recv's `-h` says only "RECV NONE at exit 1", while wait's `-h` says its NONE is on standard error. | Say the stream in recv's `-h`. | S |
| 16 | `nova-bus send --as ada --to bob,cy ...` with cy's proof `up:false` written now | `deaf: cy has no proven push since 0s`: "since 0s" reads as proven a moment ago. | Print `since never` for a name never up, or the time of the last up. | S |
| 17 | `nova-bus version --json` | `"facts":{}` and the version only as a free-text payload. | Add `version`, `os`, `arch` and `go` facts. | S |
| 18 | docs/SPEC-BUS.md | send's usage omits `--kind` and `--dry-run`, which `-h` lists; line 4 says the store is "reached over the tailnet" while line 307 allows a Unix socket. | Paste the banner's usage lines into the spec and say backback, tailnet or a Unix socket in the opening. | S |

## Good, keep

The loop in three lines at the top of the banner, and recv's and wait's `-h` paragraphs, which say who gets a message again and when, and that wait takes nothing. Every send line carries the stored body's byte count and sha256, so a sender can check a file arrived whole. `login=none` on every write to an open store, and a refused `--as` that is not the login user. Deaf names, unknown names and bad names are listed all at once, writing nothing. `ack` twice is safe and says which ids were pending. wait's `WAIT ARMED after=` cursor, and its refusal of a ULID as `--after` that names the id shape it wants. `--json` on every verb, refusals included, with a `remedy` and `why`.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| no verb takes --json | FIXED | `nova-bus names --json` prints `{"result":{"verb":"names","status":"ok","exit":0},"facts":{"count":4,"proven":2},"items":[...]}`; a refusal prints `"status":"refused","exit":2` with `why` |
| a 466-line function on the read path | FIXED | the git bus is gone; recv, the longest verb, is ~120 lines |
| REFUSED printed at exit 1 | FIXED | every REFUSED line seen exits 2; exit 1 is `RECV NONE`, `RECV FAILED` and `WAIT NONE` |
| inbox reads a stale checkout | GONE | no checkout: the bus is Redis streams |
| receipt path scope | GONE | no receipt verb on a path; `ack --id` takes the id recv prints |
| the help example is not runnable cold | STILL THERE | `nova-bus send --as ada --to bob --subject hello --body "are you there?"` on a scratch store: `SEND REFUSED: ada is no known name`, then `deaf: ada has no proven push since never` |
| `--host` prints as `<host=>` | CHANGED | `--redis <string>`, described as host:port only though a socket path works |
| retired flags still declared | GONE | `nova-bus send --nope` lists eleven flags, none retired |
| receipt-max-words has no default | GONE | the flag no longer exists |
