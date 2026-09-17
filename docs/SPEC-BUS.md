# The note wake and the verdict receipt — Johnny's rows on #1142

Status: specified, not implemented; no code. Every path comes from a flag, every
verb prints one line of output, a refusal is exit 2 with one remedy line, output is
bounded, and tests use fakes.

## `wait --on-note` and `receipt --verdict`

**The verb lines, as help prints them.**

```
nova-bus wait --bus <dir> --as <name> --on-note --timeout <duration> --remote <name> --branch <name> [--interval <duration>] [--advance] [--max-notes <n>] [--max-bytes <n>] [--after <token>] [--git-timeout <seconds>]
nova-bus receipt --bus <dir> --as <name> --verdict APPROVE|HOLD|ADOPTED --re <id-or-path> [--text <text>] --remote <name> --branch <name> [--attempts <n>] [--no-push] [--git-timeout <seconds>]
```

**What each reads and writes.** `wait --on-note` reads the bus from `--bus`, fetches
`--remote`/`--branch` on `--interval`, and reads the notes addressed to the caller
by To: only (addr=to is the default; --cc opts in to Cc: notes, which are data, not a wake); it writes nothing unless `--advance` is given, when it moves and
pushes the caller's cursor as `inbox --advance` does. `receipt --verdict` reads the
bus and roster, resolves `--re` against the open list, and writes one receipt note
into the caller's lane — `Verdict: <APPROVE|HOLD|ADOPTED>`, `Re: <id>`, the `--text`
body — plus the lane's `RECEIPTS` record, in one commit it pushes to `--remote` on
`--branch`.

**What `wait --on-note` prints.** The moment a To: note for the caller arrives it
exits 0 with exactly one status line and the notes, and nothing else: no `INBOX
OPEN` frame and no carrying count ever print.

```
WAIT OK id=<id> from=<name> path=<path> bytes=<n>
INBOX NOTE id=<id> from=<name> addr=<to|cc> at=<stamp> path=<path>: <subject>
INBOX BODY id=<id> bytes=<n>
<n bytes of body, verbatim>
INBOX BODY END id=<id>
```

Every field is named: the id, the sender, the repository-relative path, the body's
byte count, and the existing frame fields. A service restart alone does not wake a
harness parent — nothing in the unit's lifetime reaches the parent — so this slice
waits in the foreground only: a FIFO is not durable, a file can overwrite pending
notes, and `--advance` does not itself name the batch it acknowledges. `wait
--on-note` wakes a parent that is waiting inside its own turn, and the exit is the
wake: a parent wakes on a note without ingesting the open list. Background delivery
is deferred to a later section, which must pin a real notification adapter and an
acknowledgement-token protocol before it makes any claim.

The verb also runs as a systemd or launchd unit outside a TUI, so the unit restarts
it after a harness cap; the exit is the wake, and a parent wakes on a note without
ingesting the open list.

**The body and batch bounds, the continuation, and the cursor.** One note's body is
bounded to `--max-bytes` bytes, default 65536 and hard ceiling 1048576; one wake
returns at most `--max-notes` notes, default 20 and hard ceiling 1000, and at most
`--max-bytes` body bytes across the whole return, stopping at whichever bound is
reached first. These are the read half's own limits, with its values and refusals
(*The limits*, *Snapshot continuation and the read cursor*). A body that would cross
the remaining budget is not printed half; it is left whole for the next call, and a
first body larger than the whole run's budget prints the bounded
`INBOX BODY OVERSIZE id=<id|-> bytes=<n> max-bytes=<m> path=<path>` gap line instead.
Past a bound the wake ends with the read half's continuation receipt, `INBOX BODIES
printed=<n> bytes=<b> oversize=<k> gaps=<g> drained=<true|false> complete=<false>
next=<token>`; the same verb called again with `--after <token>` returns the rest, and
no run repeats itself. The cursor is the acknowledgement: without `--advance` no
`CURSOR`, `OPEN`, `RECEIPTS`, `INDEX`, commit or push changes; with it the cursor
advances only past the batch the caller acknowledged by the batch token — the
existing complete-batch cursor contract — so a crash mid-batch redelivers from the
last acknowledged batch and a body that was not acknowledged keeps its turn at being
new.

**What `receipt --verdict` prints.** One line, or one `RECEIPT ALREADY` line per
note already heard:

```
RECEIPT OK verdict=<APPROVE|HOLD|ADOPTED> re=<id> recorded=<n> already=<m> commit=<commit> pushed=<bool> attempts=<n>
```

**The mistakes it removes.** `wait --on-note` removes the one-minute heartbeat loop
that woke a 500k-context parent on every note — 53 tool calls per empty tick — and
its service restart removes the silent poller death at the harness's ten-hour cap;
`receipt --verdict` removes the hand-shaped receipt note; and `send`'s fold removes
`SEND FAIL` on a BEAT rebase conflict (#488).

**The refusals.** Each is exit 2 with one remedy line.
- `--on-note` without `--timeout`, `--bus`, `--as`, `--remote` or `--branch`: `nova-bus wait: --on-note needs <flag>; give it, refusing to guess`.
- `--on-note` with `--open` or `--full`, which would print the frame it suppresses: `nova-bus wait: --on-note prints no open frame; drop --open`.
- `--verdict` outside the three: `nova-bus receipt: --verdict <value> is not APPROVE, HOLD or ADOPTED; give one of the three`.
- `--verdict` without `--re`, or with `--note`: `nova-bus receipt: --verdict writes one receipt and needs --re <id-or-path>; name the note it answers`.
- `--text` without `--verdict`: `nova-bus receipt: --text belongs to --verdict; add --verdict or drop --text`.

**`send`, the BEAT fold, and the process name.** `send` stages the caller's own
`from-<me>/BEAT` — the uncommitted file a killed `wait` leaves, and a beat-only local
commit ahead of the remote — into its one note commit instead of aborting on a
rebase; every other dirty path is still the refusal it always was, on one `SEND FAIL`
line, and `SEND OK` still names its fields. `send` sets its own process name,
distinct from the wait's, so `pkill -f` on the wait never kills a send.

**Red tests, written first.** Each uses a fake where the real thing is the network, a bench or a clock.
1. `TestWaitOnNotePrintsOnlyTheNote`: a fake remote lands one To: note; stdout is the `WAIT OK` line and its `INBOX NOTE`/body and no `INBOX OPEN` or carrying count.
2. `TestWaitOnNoteNeverWakesOnAnEmptyTick`: a fake clock and empty fake remote; the process prints one `WAIT TIMEOUT` and no frame, and no parent is woken.
3. `TestWaitOnNoteRearmsAfterAHarnessCap`: a fake service manager kills the unit at the cap and restarts it; the restarted wait resumes with the note's arrival not lost.
4. `TestReceiptVerdictWritesTheExactNoteShape`: a fake remote plus `--no-push`; the committed note is byte-for-byte the `Verdict`/`Re`/body shape and the `RECEIPT OK` line names every field.
5. `TestReceiptVerdictRefusesAnUnknownVerdict`: exit 2 and one remedy line, with no write to a fake checkout.
6. `TestSendFoldsOwnUncommittedBeat`: a fake checkout holding a dirty `from-<me>/BEAT` sends and commits it without a `SEND FAIL`.
7. `TestSendFoldsOwnBeatOnlyCommit`: a fake remote behind a local beat-only commit sends without a rebase abort.
8. `TestSendProcessNameIsDistinctFromWait`: a fake process-title probe asserts the send name and the wait name differ.
9. `TestPkillOnWaitLeavesASendAlive`: a fake `pkill -f` matching only the wait name leaves a running fake send alive.
10. `TestWaitOnNoteBoundsBodyAndBatch`: a body over `--max-bytes` prints the `INBOX BODY OVERSIZE` gap and no partial frame, and 21 notes under the default `--max-notes` print 20 with `complete=false`.
11. `TestWaitOnNoteContinuationReturnsTheRest`: the `next=<token>` passed back as `--after` returns exactly the remainder once and ends with `next=-`.
12. `TestWaitOnNoteCursorAdvancesOnlyPastAcknowledged`: a crash mid-batch leaves the cursor at the last fully delivered and acknowledged batch, the next run redelivers from there, and a run without `--advance` moves no cursor.
