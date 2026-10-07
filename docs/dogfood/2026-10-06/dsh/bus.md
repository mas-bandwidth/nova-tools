# Dogfood: nova-bus — 2026-10-06, dsh

One friend, one tool, cold. Before and during the run I read only the tool's own
surfaces — `nova-bus -h`, `nova-bus help`, every verb's `-h`, and its pages
under docs/ (`docs/CLI.md`, `docs/SPEC-BUS.md`) — then used every verb at least
once with its real flags, refusals included, against one scratch Redis on a
bench at loopback (`127.0.0.1:16399`, an open store, so every write says
`login=none`) whose `friends` set named ada and bob and whose `machines` set
named bench1, with a hand-written proven push on `bus2:push` for ada and bob
(finding 4 is the remedy the row that cannot have a proof gets). The binary was
built from this checkout at 1dc3dd9cdf2d. About 30 minutes end to end. Every
finding is recorded, not fixed.

## Findings

1. `nova-bus recv --as bob --kind request` with a status message and a request
   message waiting, then `nova-bus peek --as bob`

        PEEK OK pending=2 new=0
        PEEK MESSAGE state=pending id=01M4BDW5JVBYA4PYYYG0A7WE2X from=ada at=2026-10-07T14:56:55Z subject="one"
        PEEK MESSAGE state=pending id=01M4BDW5K1PZ5737SZV9ADB88Q from=ada kind=request at=2026-10-07T14:56:55Z subject="two"

   Only the request was delivered. The status message was skipped by the filtered
   read and handed back — `XPENDING bus2:to:bob bob - + 10` shows its idle at
   900007 ms, the `ClaimAfter` hand-back — it holds no `delivered` receipt
   (`nova-bus receipts --as bob` lists only the request), and a plain
   `nova-bus recv --as bob` after the peek still delivers it as new. Expected
   `PEEK OK pending=1 new=1`: the verb's own help says "pending is delivered and
   not acked, new is never delivered", and a skipped message is neither. As
   printed, a reader cannot tell a message actually handed to a session from one
   a filter only walked past, and the count is wrong by the verb's own
   definition. Grade: URGENT.

2. `nova-bus help` publishes ten verbs; `cmd/nova-bus/README.md` and the
   `## nova-bus` Commands table in `docs/CLI.md` stop at nine.

        nova-bus receipts [--as <me>] [--id <id,...>] [--max <n>] [--timeout <duration>] [--redis <addr>]
        nova-bus overdue [--older <duration>] [--max <n>] [--timeout <duration>] [--redis <addr>]
        nova-bus log [--bodies] [--max <n>] [--timeout <duration>] [--redis <addr>]

   Those are three usage lines of `nova-bus help`, and the first names a verb the
   README's verb list never reaches: it ends `log`, `names`, `version`, `help`
   and calls the command reference the place that "documents every verb's
   flags", while the reference's table has no `receipts` and no `overdue` — the
   word `overdue` does not appear anywhere in the nova-bus section at all. Two
   verbs the tool runs, one of them the coordinator's alarm, are invisible to a
   stranger who reads the page he is sent to. Expected the page and the
   reference to name every verb the binary has. Grade: NEXT.

3. `nova-bus overdue --older 1s` with one unread message waiting

        OVERDUE OVERDUE count=1 older=1s
        OVERDUE MESSAGE name=bob id=01M4BDP5N25EPNXRH1K309BD0R state=new from=ada age=1s subject="unread"
        exit=1

   `nova-bus overdue -h`'s exit table says "1 BUS OVERDUE, one or more", but the
   verb prints the word `OVERDUE` twice and its `--json` `word` is `OVERDUE`; the
   word `BUS OVERDUE` is printed nowhere. Expected the exit table to name the
   word the verb prints. Grade: NEXT.

4. `nova-bus send --as ada --to bench1 --subject hi --body x`

        SEND REFUSED: deaf: bench1 has no proven push since never: no daemon has recorded one;
        the remedy: bench1 runs its friend daemon with a deliver adapter for its harness
        (nova-friend install --as bench1 --harness <h> --dir <d>) and its session answers the
        daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's
        push; run: nova-bus help

   The three printed lines (the refusal is one long line, wrapped). bench1 is a
   machine row from the `machines` set, not a friend: it has no session to answer
   a SESSION CHECK, and `docs/SPEC-BUS.md` says a name with no friend daemon (a
   machine row) "has no way to a proof and is refused until it runs one" — the
   remedy asks the impossible and names none of that. Expected a refusal that
   says a machine row cannot be heard and what that means for the send, or a
   remedy a machine's owner can act on. Grade: NEXT.

5. `nova-bus help --json`

        {"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-bus help","why":["unknown verb \"--json\"; the verbs are wait, send, peek, recv, ack, receipts, overdue, log, names, version"]},"facts":{}}

   One printed line; `nova-bus help send --json` prints the text help and
   silently ignores `--json` at exit 0. The banner promises "Every verb takes
   `--json`", `help` is listed as a verb in the usage, and `--json` is answered
   as an unknown verb. Expected either `help --json` to render the help as one
   JSON object, or the promise to except `help`. Grade: NEXT.

6. `nova-bus send --as ada --to bob --subject hi --body x --token-cleanup 1h`

        SEND OK id=01M4BDX8QBXY8SAVEWGDXF4KWQ to=bob cc=- at=2026-10-07T14:57:31Z bytes=1 sha256=2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881 login=none

   `--token-life` and `--token-cleanup` are the token's two settings; with no
   `--token` they change nothing, and the call is accepted as if they applied
   (`--token-life 1h` alone behaves the same). Expected a refusal naming `--token`
   as what the pair wants, the way `--forever` without `--exec` is refused rather
   than accepted. Grade: NEXT.

7. `nova-bus version -h`

        usage: nova-bus version [flags]
        from `nova-bus help`:
          nova-bus version
        exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).

   (The third line is the last line, after the flag list.) `version` takes no
   store and has no "ran and said no" case, yet its help carries the copy of the
   recv/wait exit table; `send`, `peek`, `ack`, `receipts`, `log` and `names`
   carry the same line. Expected each verb's `-h` to quote the shared table, or
   the verb's own. Grade: NEXT.

READ 7/10 — the banner, every verb's `-h` and the spec page answered what it
does, how it works and what each flag wants before my first command, and every
refusal but one carried a remedy I could paste; the score is held down by help
that lies (`help --json` refused under a promise that every verb takes `--json`,
the stale verb lists in the README and the command reference, and the copied
exit table on verbs it does not fit).

USE 7/10 — with one scratch store every verb's real path ran first try (send,
wait, peek, recv, ack, receipts, overdue, log, names, plus the `--kind`, `--re`,
`--token`, `--stdin`, `--cc`, `--wake-file`, `--forever`, `--max`/`--all`,
`--dry-run` and `--json` variations and the refusals), the token retry, the
receipt stages and the at-least-once loop behaved exactly as the help says; the
cost is the `peek` count that is wrong by its own definition, the machine-row
remedy that cannot be followed, and two token flags that silently do nothing.

urgent=1 next=6
