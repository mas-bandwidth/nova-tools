# nova-bus dogfood, 2026-10-06 (antigravity)

Reviewer: Johnny Grok. Binary: `nova-bus v1.2.0-dev.d165b531 linux/amd64 go1.27.1` (commit `d165b531`, an ancestor of this tip; later commits on this tip touch `cmd/nova-bus`). Read only `nova-bus -h`, `nova-bus help`, `nova-bus <verb> -h`, the nova-bus section of `docs/CLI.md`, and `docs/SPEC-BUS.md`. Every verb was run, refusals included, against a scratch git checkout in the job directory. No live bus, no Redis opened, no server started. Not reached: a successful send, peek, recv, ack, log, names, or wait, and the deaf-name refusal, because those need a store this binary will not find in a git checkout.

## Findings

1. `--dry-run` on the writing verbs hides the real refusal and says a write may have happened.

Command: `nova-bus send --as ada --to bob --subject hi --body there --dry-run`

Printed:

```
SEND FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
```

Expected: `send -h` says `--dry-run` checks the message as send does, names every problem, prints the line with no id, and writes nothing. The same command without `--dry-run` prints `SEND REFUSED: --redis is required: ... refusing to guess` at exit 2. I expected that refusal, at exit 2, with no claim of a write. The same `FAILED` / `may have written` line, at exit 1 and with no remedy, is what `recv --dry-run`, `ack --dry-run`, `--dry-run` pointed at the scratch checkout, and `--dry-run --redis 8.8.8.8:6379` all print. `ack --dry-run --json` is `status=failed` and has no remedy field. Nothing was written.

Grade: URGENT

2. `help` does not take `--json`, and `help send --json` exits 0 with the text page.

Command: `nova-bus help send --json`

Printed:

```
usage: nova-bus send [flags]
from `nova-bus help`:
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--token <t>] [--redis <addr>] [--dry-run]
```

Expected: the banner and SPEC-BUS ("Every verb takes `--json`") and `send -h` (`--json` prints one JSON object instead of lines). `nova-bus version --json` does that and exits 0. I expected one JSON object. This command printed the text help and exited 0, so the flag was dropped. `nova-bus help --json` and `nova-bus --json version` refuse `unknown verb "--json"` at exit 2, and that list of verbs omits `help`.

Grade: URGENT

3. The banner says a verb that lists takes `--max`. `names` and `peek` reject it.

Command: `nova-bus names --max 5`

Printed:

```
NAMES REFUSED: unknown flag --max; the flags of names are --json, --redis; run: nova-bus names -h
```

Expected: the banner says a verb that lists takes `--max <n>` (default 20, 0 lists all) and says MORE for the rest. SPEC-BUS says `log` takes `--max`, which matches `log -h`. `names` and `peek` both list, and `nova-bus peek --as bob --max 1` is the same unknown-flag refusal. I expected the banner to name only `log`, or the flag to limit those two.

Grade: NEXT

4. Send does not name every problem at once, and several checks never run without a store.

Command: `nova-bus send --as Ada --to Bob --subject "" --body ""`

Printed:

```
SEND REFUSED: --subject is required; it wants one line saying what the message is; refusing to guess; run: nova-bus help
```

Expected: SPEC-BUS says send refuses, naming every problem at once, including a bad name, an empty body, an empty subject, and a body from both or neither source. This command has a bad `--as`, a bad `--to`, an empty subject, and an empty body. I expected all of those named, at exit 2. Only the empty subject was named. With a subject present, `nova-bus send --as ada --to bob --subject hi --body ""`, `--kind nope`, and `--token 'bad token!'` each print only `--redis is required`, and `nova-bus peek --as bob --kind bogus` does the same, so the kind list, the token shape, the empty body, and the bad name are not reported until a store answers.

Grade: NEXT

5. A scratch git checkout is dialed as a Unix socket. There is no way to run the example.

Command: `nova-bus send --as ada --to bob --subject hello --body "are you there?" --redis /home/nova/rowan-working/friends/johnny/jobs/dogfood-antigravity-bus-b.w1~15.g5/scratch-bus`

Printed:

```
SEND REFUSED: redis at /home/nova/rowan-working/friends/johnny/jobs/dogfood-antigravity-bus-b.w1~15.g5/scratch-bus as the default user, no password: unreachable: dial unix /home/nova/rowan-working/friends/johnny/jobs/dogfood-antigravity-bus-b.w1~15.g5/scratch-bus: connect: connection refused; next: start the store or correct the address, which was given to this tool; run: nova-bus help
```

Expected: `send -h` says `--redis` is `host:port`. SPEC-BUS says there is no git mode and no mode that works without a server, and that a path is a Unix socket on this machine. I expected a refusal that a directory is not `host:port`, before a dial. The checkout was dialed as a Unix socket. `wait`, `peek`, `recv`, `ack`, `log`, and `names` with the same `--redis` printed the same dial line. `nova-bus send --bus <that checkout>` is `unknown flag --bus`. Each line of the example block, with no `--redis`, prints `<VERB> REFUSED: --redis is required: ... refusing to guess` at exit 2. The first-run block in `docs/CLI.md` cannot be pasted, and no verb's success path ran.

Grade: NEXT

6. The nova-bus command table in `docs/CLI.md` is behind `send -h`.

Command: `nova-bus send -h`

Printed:

```
usage: nova-bus send [flags]
from `nova-bus help`:
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--token <t>] [--redis <addr>] [--dry-run]
```

Expected: the command table on the nova-bus page stops at `--re` and does not name `--kind`, `--token`, or `--dry-run`. Those three are real flags in this help and are not in that section of `docs/CLI.md`. I expected the page a stranger reads and `send -h` to name the same flags.

Grade: NEXT

7. The page on this tip names `receipts` and `overdue`. This binary does not have them.

Command: `nova-bus overdue`

Printed:

```
BUS REFUSED: unknown verb "overdue"; the verbs are wait, send, peek, recv, ack, log, names, version; run: nova-bus help
```

Expected: SPEC-BUS, message-receipts, says `nova-bus overdue [--older <d>]` and `nova-bus receipts --as <name> [--id <id,...>]`. I expected those verbs, or a help line for them. `nova-bus receipts --as bob` is the same unknown-verb refusal, at exit 2. This binary is `d165b531`; this tip has later `cmd/nova-bus` commits, including message-receipts, so the page and the installed binary are not the same build. The refusal itself is clear.

Grade: NEXT

READ 6/10. The address rule, a missing flag, and `--forever` without `--exec` match the page and name a remedy, but the banner and SPEC both say every verb takes `--json`, which `help` does not, and one send did not name every problem at once as SPEC says.

USE 3/10. No store verb's success path ran: a scratch git checkout is dialed as a Unix socket, the pasted example refuses with no store, and `--dry-run` reports a write that did not happen instead of that refusal.

urgent=2 next=5
