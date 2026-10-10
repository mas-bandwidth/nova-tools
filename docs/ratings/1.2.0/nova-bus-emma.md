# nova-bus READ and USE rating, nova-tools 1.2.0

Rater: deepseek/deepseek-v4.1-flash, harness dsh
Build: 0389f76634ec
READ: 7.5/10
USE: 7/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of `sprint/mechanical-2026-10-02`, the build named in `Build`. It was read cold from `nova-bus help`, every verb's `-h` and docs/SPEC-BUS.md, then built with go1.26.6 and used for real against a throwaway Redis 8 bound to a Unix socket in the job's scratch directory on a Linux bench, stopped at the end; no live store was touched and no server ran on the workstation. The nova-bus rated at 1.1.0 was the git bus; it was removed and this is the Redis-streams bus that took its name.

## Reasons

READ 7.5. The banner answers the three questions an AI needs before it runs anything: one line for what it does, a three-line `how it works` naming the stream per recipient, the consumer group and the log, and a `first run` line with where the store comes from. Every verb's `-h` carries its usage from the banner, a paragraph naming each field of each line it prints, the flags with what each wants, an `effect:` line (inspection, delivery or store write) and an exit line. Refusals name every problem in one run with a `run:` remedy: an unknown verb and an unknown flag both list the whole set and point at `-h`. `--json` is a first-class rendering of the same result value on every verb, refusals and dry runs included. `send --token` is documented to the retry: the same token and arguments answer the first `SEND OK` line byte for byte and a changed argument is refused. That is the shape an AI wants.

What keeps READ at 7.5. The `first run:` line says "a Redis naming ada and bob" and nothing in the help says what naming is; on a scratch store the banner's own example cannot get past its first send, because the sets `friends` and `machines` and a `bus2:push` proof per name are named only in the spec. The banner says "A verb that lists takes `--max`", yet `peek` and `names`, which list, refuse `--max` as unknown. Every `--redis` flag says "host:port", while an absolute Unix socket path is what a trial store uses and is accepted. The exit line on send, peek, ack, receipts, log, names and version is the banner's generic one, so send's `-h` explains recv's exit 1. The spec's opening still says the store is reached over the tailnet, and its config section writes `--redis <host:port>`, though the same spec and the tool accept a Unix socket. Checks the help promises are absent: `--re` takes `not-an-id`, a `--subject` takes a newline, an `ack --id` takes any word.

USE 7. With the roster and the push proofs seeded by hand, the whole loop ran for real: send (plain, `--dry-run`, `--cc`, `--token` retry, `--stdin`), peek (plain, `--kind`), recv (plain, `--max`, `--all`, `--ack`, `--exec`, `--forever` ended by SIGTERM, `--kind`, `--dry-run`), ack (real, twice, `--dry-run`), receipts (`--id`, `--max`), overdue (both outcomes), log (`--max`, `--bodies`, `MORE`), names, wait (`--after`, timeout, `--json`, `--wake-file`) and version. Every send line carries the stored body's byte count and sha256, so a caller can check a file arrived whole. The token retry printed the first line again and refused a changed body. A `--dry-run` writes nothing and says `dry_run=true`. `recv --exec` acks only on exit 0 and reports `RECV FAILED` otherwise. The push gate lists every deaf name at once with the remedy, writing nothing. Unknown verbs and flags answer with the whole list.

What keeps USE at 7. A name no roster holds answers `PEEK OK`, `ACK OK` and `RECEIPTS OK` at exit 0 while send, recv and wait refuse the same name, so a typo reads as an empty inbox. Inputs that should be refused pass: `--re not-an-id` sends and is stored, a two-line `--subject` is stored as `two\nlines`, and `ack --id not-an-id` is `ACK OK acked=false`. `recv --json` carries the payload with the text form's leading blank line, and `recv --all --json` prints one object per message where the banner says one object. `wait --json` is a bare object with `"status":"ok"` at exit 1, and its NONE goes to stdout in JSON while the text NONE goes to standard error. After `recv --exec false`, the message is held out of every recv for fifteen minutes and no line says when it comes back. `recv --max 0` is refused though the banner says 0 lists all. A flag-parse refusal prints text even when `--json` is given.

A 10 would make the banner's first run work cold (say what a trial store needs, or add a verb that seeds one), make the `--max` sentence and the `--redis` flag text true, give every verb its own exit line and an example, check the roster in peek, ack and receipts, refuse a malformed `--re` and a multi-line subject, carry the body alone and one object per batch in `recv --json`, render wait through the one envelope with the right status and stream, name the fifteen-minute hold on `RECV FAILED`, give `version --json` real facts, and render flag refusals through `--json`.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus help` (first run and example) | `first run: a Redis naming ada and bob` does not say what naming is; on a scratch store the example's first send is `SEND REFUSED: ada is no known name`. The sets `friends`/`machines` and the `bus2:push` proof are only in docs/SPEC-BUS.md, and no verb writes a trial store. | Say in the banner what a trial store needs (the two sets and a push proof per name), or add a verb that seeds a scratch store. | M |
| 2 | `nova-bus help` and `nova-bus peek --max 1`, `nova-bus names --max 1` | The banner says "A verb that lists takes `--max <n>`", but peek and names, which list, refuse `unknown flag --max`. | Add `--max` and a `MORE` line to peek and names, or print the sentence only for the verbs that take it. | S |
| 3 | `nova-bus send -h` (`--redis`) and every verb's `-h` | `--redis` is described as "the Redis address, host:port"; an absolute Unix socket path works and is what the trial store used. | Say `host:port or the absolute path of a Unix socket` in the flag text. | S |
| 4 | `nova-bus send -h`, `peek -h`, `ack -h`, `receipts -h`, `log -h`, `names -h`, `version -h` | The exit line is the banner's generic one naming recv and wait; only wait and overdue carry their own. | Give each verb its own exit line, as wait's and overdue's `-h` already do. | S |
| 5 | `nova-bus send --re not-an-id` | `SEND OK`, and log stores `re=not-an-id`; the flag says it wants the id of the message this one answers. | Refuse a `--re` that is not an id, naming the shape it wants. | S |
| 6 | `nova-bus ack --id not-an-id` | `ACK OK acked=0` at exit 0: any word is accepted as an id, the same line as an id already acked. | Refuse an `--id` that is not the shape recv prints, naming the bad id, or add a NOTE saying it named no id. | S |
| 7 | `nova-bus send --subject $'two\nlines'` | `SEND OK`; log, peek and recv print `subject="two\nlines"`, against the flag's "one line". | Refuse a subject holding a newline or another control character. | S |
| 8 | `nova-bus send --subject "" --body "" --kind nope` | One refusal, `--subject is required`; the empty body and the bad kind are not named in the same run. | Let the flag check fall through to the message's own checks so one run names all the problems. | S |
| 9 | `nova-bus peek --as nobody` | `PEEK OK pending=0 new=0` at exit 0 for a name no roster holds, while send, recv and wait refuse the same name. | Read the roster in peek and refuse as recv does. | S |
| 10 | `nova-bus ack --as nobody --id <id>` | `ACK OK acked=0` at exit 0 for a name no roster holds. | Read the roster in ack, or say in `-h` that an unknown name acks nothing. | S |
| 11 | `nova-bus receipts --as nobody` | `RECEIPTS OK count=0` at exit 0 for a name no roster holds. | Read the roster in receipts and refuse as recv does. | S |
| 12 | `nova-bus wait --json` (WAIT NONE) | A bare `{"status":"ok","word":"NONE",...}` at exit 1; the other verbs render the one envelope with a result and a status that matches the exit. | Render wait through the one envelope, with `status` none or failed at exit 1. | M |
| 13 | `nova-bus wait --json` | The JSON `WAIT NONE` goes to stdout while the text `WAIT NONE` goes to standard error. | Print the JSON result on the same stream as the text result. | S |
| 14 | `nova-bus wait --wake-file nope/missing.txt` | A wake file under a directory that does not exist is accepted and the wait runs to `WAIT NONE`; `-h` does not say a missing file is waited for. | Refuse when the file's directory is absent, and say in `-h` that a missing file is waited for. | S |
| 15 | `nova-bus recv --json` | `"payload":"\nhello bob"`: the text form's separating blank line is inside the payload. | Carry the body alone in the payload. | S |
| 16 | `nova-bus recv --all --json` | Prints one JSON object per message; the banner says `--json` is "the same result as one JSON object on stdout". | Say a batch prints one object per line, or wrap the batch in one object. | S |
| 17 | `nova-bus recv --exec false`, then `nova-bus recv --all` | The message whose `--exec` failed is held out of every recv for fifteen minutes and neither the `RECV FAILED` line nor `recv -h` says when it comes back; peek still shows it pending. | Add `retry_after=15m` (or the time) to the `RECV FAILED` line and say it in recv's `-h`. | S |
| 18 | `nova-bus version --json` | `"facts":{}` and the version only as a free-text payload. | Add `version`, `os`, `arch` and `go` facts. | S |
| 19 | `nova-bus overdue -h` | The exit table says `1 BUS OVERDUE`, but the line printed at exit 1 is `OVERDUE OVERDUE count=<n> older=<d>`. | Write the exit table as the line prints it. | S |
| 20 | `nova-bus recv --max 0` against `nova-bus help` | The banner says a listing verb's `--max` has "default 20, 0 lists all", while recv refuses `--max 0`. | Scope the sentence to the listing flags that take a ceiling, and say recv's `--max` is a count of messages. | S |
| 21 | `nova-bus peek --max 1 --json` | A flag-parse refusal prints text even with `--json`, while a message refusal prints the JSON envelope. | Render flag refusals through `--json` too, or say in `-h` that a flag error is text. | S |
| 22 | docs/SPEC-BUS.md:4 and docs/SPEC-BUS.md:430 | The opening says the store is reached over the tailnet, and the config section writes `--redis <host:port>`, while line 440 and the tool accept a Unix socket. | Name loopback, tailnet or a Unix socket in the opening and in the flag text. | S |
| 23 | `nova-bus send -h`, `peek -h`, `ack -h`, `log -h`, `names -h`, `version -h` | No `example:` line, while wait, receipts and overdue have one. | Add an `example:` line to each verb's `-h`. | S |

## Good, keep

The banner's three-line loop and its `effect:` line on every verb. Refusals that name every problem at once with a `run:` remedy, and an unknown verb or flag that lists the whole set and points at `-h`. `--json` on every verb from one result value, refusals and dry runs included (wait is the exception). `send --token`: the same token and arguments answer the first `SEND OK` line byte for byte and write nothing, and a changed argument is refused naming the message that went. Every send line carries the stored body's byte count and sha256. `--dry-run` on every write verb prints `dry_run=true` and writes nothing. `recv --exec` acks only on exit 0, holds on failure and says `acked=true exec_exit=0`. `wait` takes nothing (XREAD, never the group), prints `WAIT ARMED after=` so a caller re-arms without a clock, and ends on a wake-file line. `recv --kind` hands a skipped message back for the next reader. The push gate refuses a deaf name with the remedy and lists every deaf name at once. `names` shows each name's push state, age and harness. The `MORE` line on log, receipts and overdue names the flag that raises the ceiling.

## Compared with earlier ratings

The 1.1.0 ratings describe the git bus that was removed; this rates the Redis-streams bus that took the name, so most of the old findings are gone with the old machine.

| earlier (1.1.0) | now | evidence |
|---|---|---|
| no verb takes `--json` | FIXED | send, recv, peek, ack, receipts, overdue, log, names and version all render one result value; only wait uses its own bare object |
| REFUSED printed at exit 1 | FIXED | every `REFUSED` line seen exits 2; exit 1 is `RECV NONE`, `RECV FAILED`, `WAIT NONE` and `OVERDUE OVERDUE` |
| a 466-line function on the read path | GONE | the git bus is gone; recv is a short command over pkg/bus |
| inbox reads a stale checkout | GONE | no checkout: the bus is Redis streams |
| the receipt path scope is undocumented | GONE | receipts takes `--id`, not a note path |
| the help example is not runnable cold | STILL THERE | on a scratch store the example's send is `SEND REFUSED: ada is no known name` |
| a checking verb before adopting | STILL MISSING | no verb writes a trial store or checks a message without one |
| a name the roster does not hold is refused | REGRESSED | send, recv and wait refuse it; peek, ack and receipts answer `OK` |
| refusals say what the input wants | KEPT | `ack --id` wants "the message ids, comma-separated, as recv printed them" |
