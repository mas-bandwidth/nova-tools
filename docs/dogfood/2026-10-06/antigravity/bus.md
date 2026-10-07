# nova-bus dogfood — antigravity, 2026-10-06

Cold, as a stranger: I read only `nova-bus -h`, `nova-bus help`, `nova-bus help <verb>`, every verb's `-h`, and the nova-bus page in `docs/CLI.md`. The binary was built from the staged checkout with `go build -o $JOB/bin/nova-bus ./cmd/nova-bus` on a Linux bench (`<bench>`), never the installed binary; the staged commit is `1b559077e0cb9ee9e14efe2910743cc4cff45ab9`, and the version line it printed is `nova-bus v1.0.1-0.20261007225308-1b559077e0cb linux/amd64 go1.26.6`. Every verb then ran at least once with its real flags, the refusals too, against a scratch Redis on the bench's loopback (`127.0.0.1:16555`, an open store, so every write says `login=none`) whose `friends` set named ada and bob with a fresh proven push each and whose `machines` set named bench1; the store was stopped after the run. Every finding is recorded, not fixed.

## Findings

1. `nova-bus send --as ada --to bob --subject hello --body "are you there?" --dry-run`
   Printed:
   ```
   SEND FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   (one line printed)
   ```
   I expected the refusal the same command prints without `--dry-run`, `SEND REFUSED: --redis is required ... refusing to guess` at exit 2, because `send -h` says `--dry-run` "checks the message as send does (every problem named) and prints the line with no id, writing nothing". Instead the real problem is never named, the line says the verb "may have written" when it wrote nothing, and no remedy is given. `recv --as bob --dry-run` and `ack --as bob --id 01M4C9BY36FVEKWESAQJ8E9GRM --dry-run` print the same FAILED line under their own verb word, `--dry-run --redis 127.0.0.1:1` prints it too, and with `--redis 127.0.0.1:16555` the same command succeeds.
   Grade: URGENT (a refusal with no remedy, and a claim that a write may have happened)

2. `nova-bus peek --as bob`
   Printed:
   ```
   PEEK OK pending=2 new=0
   PEEK MESSAGE state=pending id=01M4C9CY0YWJV65FDEFPVXG6GV from=ada at=2026-10-07T22:57:56Z subject="one"
   PEEK MESSAGE state=pending id=01M4C9CY1BB36RBE90XY583VRS from=ada kind=request at=2026-10-07T22:57:56Z subject="two"
   ```
   I expected `PEEK OK pending=1 new=1`, with the status message still new, after `nova-bus recv --as bob --kind request` took only the request out of two waiting messages. `recv -h` says a message of another kind "is skipped, neither acked nor held", and `peek -h` says pending is "delivered and not acked"; `XPENDING bus2:to:bob bob - + 10` lists the skipped status as pending and a later plain `nova-bus recv --as bob` hands it back as a normal delivery, so the filter held a message the reader never saw and the peek counts it as delivered.
   Grade: URGENT (wrong result)

3. `nova-bus names --max 5`
   Printed:
   ```
   NAMES REFUSED: unknown flag --max; the flags of names are --json, --redis, --timeout; run: nova-bus names -h
   (one line printed)
   ```
   I expected `--max 5` to bound the listing, as the banner says "A verb that lists takes `--max <n>` (default 20, 0 lists all) and says MORE for the rest". `names` lists and has no `--max`; `nova-bus peek --as bob --max 1` refuses it the same way, and `log` is the only listing verb whose help has it.
   Grade: NEXT (unclear help)

4. `nova-bus help --json`
   Printed:
   ```
   {"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-bus help","why":["unknown verb \"--json\"; the verbs are wait, send, peek, recv, ack, receipts, overdue, log, names, version"]},"facts":{}}
   (one line printed)
   ```
   I expected one JSON object, as the banner says "Every verb takes `--json`" and `help` stands in the usage list. The flag was read as the verb argument and refused; `nova-bus help send --json` prints the text help and exits 0 with the flag dropped, while `nova-bus version --json` is the object I expected.
   Grade: NEXT (unclear help)

5. `nova-bus overdue --older 1s`
   Printed:
   ```
   OVERDUE OVERDUE count=1 older=1s
   OVERDUE MESSAGE name=bob id=01M4C9BV1Q3BFTPS6K5BME589A state=new from=ada age=4s subject="old"
   ```
   I expected the word the exit table names. `overdue -h`'s table says "1 BUS OVERDUE, one or more", but the verb prints `OVERDUE OVERDUE` and its JSON carries `"word":"OVERDUE"`; the words `BUS OVERDUE` are printed nowhere.
   Grade: NEXT (unclear help)

6. `nova-bus ack --as bob --id not-a-ulid`
   Printed:
   ```
   ACK OK acked=0 asked=1 login=none
   ACK ID id=not-a-ulid acked=false
   ```
   I expected a refusal naming the id's shape before the store answers, as `--id` is documented as "the message ids, comma-separated, as recv printed them". A malformed id is knowable with no store; instead the verb exits 0 after acknowledging nothing, so a caller cannot tell a typo from an id that was never pending.
   Grade: NEXT (friction)

7. `nova-bus send --as ada --to bench1 --subject hi --body x --redis 127.0.0.1:16555`
   Printed:
   ```
   SEND REFUSED: deaf: bench1 has no proven push since never: no daemon has recorded one; the remedy: bench1 runs its friend daemon with a deliver adapter for its harness (nova-friend install --as bench1 --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
   (one line printed)
   ```
   I expected a remedy a machine row's owner can act on. `bench1` is a machine row, not a friend: it has no session to answer a SESSION CHECK, and the same remedy is printed whether the name is a friend or a machine. The refusal names the state clearly but asks the impossible.
   Grade: NEXT (a refusal whose remedy cannot be followed)

8. `nova-bus send --as ada --to bob --subject hi --body x --redis /tmp/somedir`
   Printed:
   ```
   SEND REFUSED: redis at /tmp/somedir as the default user, no password: unreachable: dial unix /tmp/somedir: connect: no such file or directory; next: start the store or correct the address, which was given to this tool; run: nova-bus help
   (one line printed)
   ```
   I expected a refusal that a path is not `host:port` before any dial, as `send -h` says `--redis` is "the Redis address, host:port". The path was dialed as a Unix socket; `peek`, `recv`, `ack`, `log`, and `names` with the same `--redis` print the same dial line, and each of them with no `--redis` prints the `--redis is required` refusal at exit 2.
   Grade: NEXT (friction)

## What held

`wait`, `log`, `receipts`, and `version` ran clean end to end, and the plain success paths of `send`, `peek`, `recv`, `ack`, `names`, `overdue`, and `help` printed what their help says; the refusals for an unknown verb, an unknown flag, a missing `--redis`, an off-loopback store, a bad `--token`, a bad `--kind`, a body from both sources, and `--forever` without `--exec` each named the problem and a remedy.

READ 6/10 — the banner, each verb's `-h`, and the `docs/CLI.md` page explain the bus, its outputs, and its refusals in one line with a remedy, but the banner's `--json` and `--max` promises and the `overdue` exit-table word do not match what the verbs do, and `--redis` is documented as `host:port` while a path is dialed.

USE 6/10 — every store verb's success path ran on a scratch Redis, including send, recv, ack, receipts, overdue, log, names, and wait, but `--dry-run` with no reachable store reports FAILED and a write that did not happen, and a filtered `recv` holds a message the reader never saw and makes `peek` over-count it.

urgent=2 next=6
