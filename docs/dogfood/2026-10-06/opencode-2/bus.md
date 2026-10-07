# nova-bus dogfood, 2026-10-06 (opencode-2)

Tool: nova-bus. Build: `nova-bus v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6`
(the tree's `sprint/mechanical-2026-10-02` tip). Run cold, from the binary's own
help, `docs/CLI.md` and `docs/SPEC-BUS.md` only, with no code read. Every verb ran
against one scratch Redis on the loopback (127.0.0.1:16517): an open store (no
users, so every write says `login=none`) whose `friends` set named ada, bob and
carol and whose `bus2:push` held a fresh proven push for each, so `send` and `recv`
were heard; a machine row stayed `push=none`. The proof JSON was hand-written
because no verb writes one, which is finding 8. No file was changed here, only
this report.

## 1. `--dry-run` on a writing verb reports FAILED and "may have written" when the store is absent or unreachable — URGENT

**Command:**

    nova-bus send --as ada --to bob --subject s --body b --dry-run --redis 127.0.0.1:1

**Printed:**

    SEND FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1. `recv --as bob --dry-run --redis 127.0.0.1:1` and `ack --as bob --id x
--dry-run --redis 127.0.0.1:1` print the same line under their own word, and with
no `--redis` at all the same line stands where the `--redis is required` refusal
belongs. Against a reachable store the same flags work: `send --dry-run` prints
`SEND OK id=- ... dry_run=true` and `recv --dry-run` prints
`RECV OK pending=<n> new=<n> next_new=<id> ... dry_run=true`.

**Expected:** `send -h` says `--dry-run` "checks the message as send does (every
problem named) and prints the line with no id, writing nothing", and `docs/CLI.md`
lists `--dry-run` on send, recv and ack. With an unreachable store I expected the
store's `unreachable` refusal at exit 2; with no store, the `--redis is required`
refusal at exit 2. Instead the failure that actually happened is never named, the
status is FAILED (the worst of the three) with no remedy, and the line asserts the
verb "may have written" when it read nothing and wrote nothing. A refusal found
before the flag is read is masked by the unconsumed `--dry-run` guard.

**Grade:** URGENT

## 2. `wait --json` is a different object than every other verb's, and says `"status":"ok"` at exit 1 — NEXT

**Command:**

    nova-bus wait --as bob --timeout 1s --json --redis 127.0.0.1:16517

**Printed:**

    {"status":"ok","word":"NONE","after":"1791388192628-0","messages":[]}

exit 1. Compare the same nothing-waiting result from `recv`:

    nova-bus recv --as bob --kind blocker --json --redis 127.0.0.1:16517
    {"result":{"verb":"recv","status":"failed","exit":1,"word":"NONE","why":["nothing for bob"]},"facts":{}}

**Expected:** the banner says "Every verb takes `--json`: the same result as one
JSON object", and every other verb renders one envelope (`result`, `facts`,
`items`, `more`, `notes`); `wait` instead returns its own payload object with no
verb or exit, so a caller that parses the envelope has one special case. The two
verbs also disagree on the same exit 1: `recv` says `"status":"failed"` while
`wait` says `"status":"ok"` with `"word":"NONE"`, though both ran and said no.

**Grade:** NEXT

## 3. `help --json` (and a bare `--json`) is read as an unknown verb — NEXT

**Command:**

    nova-bus help --json

**Printed:**

    {"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-bus help","why":["unknown verb \"--json\"; the verbs are wait, send, peek, recv, ack, receipts, overdue, log, names, version"]},"facts":{}}

exit 2, and `nova-bus --json` refuses the same way.

**Expected:** the banner says "Every verb takes `--json`", and `help` stands in
the usage list as `nova-bus help [<verb>]`; a bare flag after `help` should be
accepted as the flag (as `nova-bus help send --json` already is) rather than read
as the verb argument and refused.

**Grade:** NEXT

## 4. An unknown verb leads with `BUS REFUSED`, not `nova-bus REFUSED`, and `overdue -h` names a status the verb never prints — NEXT

**Command:**

    nova-bus bogus

**Printed:**

    BUS REFUSED: unknown verb "bogus"; the verbs are wait, send, peek, recv, ack, receipts, overdue, log, names, version; run: nova-bus help

exit 2; the bare command prints `BUS REFUSED: no verb given; ...` the same way.

**Expected:** the standard's onboarding point 1 is `<tool>[ <verb>] REFUSED:
<what was wrong>; run: <tool> help`, and the tool's own name in that slot is
`nova-bus`, as line 1 of its banner says. The `BUS` prefix is a different name a
reader never chose. The same short name is in `overdue -h`'s exit table, which
says `1 BUS OVERDUE, one or more`, while the verb's real line is
`OVERDUE OVERDUE count=<n>` as `docs/SPEC-BUS.md` states; a script matching the
help's status word finds nothing.

**Grade:** NEXT

## 5. `ack --id` accepts any shape and answers `acked=false` at exit 0 — NEXT

**Command:**

    nova-bus ack --as bob --id "not a ulid" --redis 127.0.0.1:16517

**Printed:**

    ACK OK acked=0 asked=1 login=none
    ACK ID id=not\x20a\x20ulid acked=false

exit 0.

**Expected:** `ack -h` says `--id` is "the message ids, comma-separated, as recv
printed them", and a message id has a shape: `wait --after <message id>` refuses
one before the store. I expected the same refusal for a garbage id, so a typo is
named on the first turn instead of being reported as "not pending" forever. The
escaped echo of the input (`not\x20a\x20ulid`) shows the tool knows it is not an
id.

**Grade:** NEXT

## 6. `wait --after` wants a stream entry id, but every other verb prints a message's ULID under the same name `id` — NEXT

**Command:**

    nova-bus wait --as bob --after 01M4BGT6GRPQA4V389PG9B3TEE --timeout 2s --redis 127.0.0.1:16517

**Printed:**

    WAIT REFUSED: --after wants a stream entry id, <ms>-<seq> as WAIT ARMED and WAIT OK print it; "01M4BGT6GRPQA4V389PG9B3TEE" is not one; run: nova-bus help

exit 2. The argument is exactly the `id=` that `recv`, `peek`, `log` and
`WAIT MESSAGE id=` printed for that message.

**Expected:** two different identifiers share the name `id`: the message's ULID
and the stream's `<ms>-<seq>` cursor. A wait re-armed from the message a reader
just saw should be the common case; instead it is refused, and the caller must
know that `WAIT OK after=` is a different vocabulary from `WAIT MESSAGE id=`. If
the cursor is a stream id, name it `--after-stream` (or print a `stream_id=` in
the message lines) so the two are not interchangeable by name alone.

**Grade:** NEXT

## 7. `receipts` and `overdue` are in the banner but not in `docs/CLI.md` or the tool's README — NEXT

**Command:**

    nova-bus overdue -h

**Printed:**

    usage: nova-bus overdue [flags]
    from `nova-bus help`:
      nova-bus overdue [--older <duration>] [--max <n>] [--timeout <duration>] [--redis <addr>]

exit 0; `nova-bus receipts -h` likewise.

**Expected:** `nova-bus help` lists `receipts` and `overdue` among the verbs, and
`docs/SPEC-BUS.md` documents both, so the command reference a stranger reaches
for (`docs/CLI.md`, its `## nova-bus` Commands table) and the tool's own
`cmd/nova-bus/README.md` verb list should name them too; today both end at
`names`. The page under `docs/` is where a cold reader learns the verb set, and
it is missing two of them.

**Grade:** NEXT

## 8. A scratch store's names cannot be made heard from the docs alone: no verb writes a push proof — NEXT

**Command:**

    nova-bus send --as ada --to bob --subject hello --body "are you there?" --redis 127.0.0.1:16517

on a store whose `friends` set names ada and bob and whose `bus2:push` is empty.

**Printed:**

    SEND REFUSED: deaf: ada has no proven push since never: no daemon has recorded one; the remedy: ada runs its friend daemon with a deliver adapter for its harness (nova-friend install --as ada --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help

exit 2 (the same refusal names bob as well; `recv --as bob` refuses the same way),
and `names` prints `NAMES OK count=4 proven=0` with every name `push=none
age=never`.

**Expected:** the example block's first run needs a store "naming ada and bob",
but a store made by `nova-config apply` alone leaves every `send` and `recv`
refused until each name has a proof only the friend daemon can make. `docs/CLI.md`
and `docs/SPEC-BUS.md` list the proof's fields but not the JSON a hand-seeded
scratch store needs, and there is no verb to write or check one. I could only run
the writing verbs after reading the source for the exact `bus2:push` value. A
`quickstart` or a documented fixture that writes a proven push for ada and bob,
needing no daemon, would let a stranger run the example as printed.

**Grade:** NEXT

READ 8/10 — the banner answers what, how and how-to; every verb's `-h` exits 0 and
names its effect class and exit codes; the refusals are one line, name every
problem at once and carry a remedy a cold reader can act on. The score is held
down by the `--dry-run` help that does not hold on the no-store path, the "every
verb takes `--json`" promise that `help --json` breaks, `wait`'s second JSON
shape, and the command reference that omits two verbs.

USE 8/10 — with a scratch store every verb ran for real, and the paths the spec
promises held: the token retry returned the first send's exact line and refused
other arguments or a life past its end (a cleanup below the life was raised to
the life, never shorter); `recv --exec` acked on exit 0 and left the message
pending on exit 1; `--kind`, `--max`, `--all`, `--forever`, `--re`, the receipt
stages delivered/read/acted, the deaf gate, `wait --wake-file` and the bounded
`MORE` lines all behaved as documented. The score is held down by the same
`--dry-run` line, which turns the one path that should need no live state into a
false FAILED, and by the `BUS REFUSED`/`overdue` status names a script has to
special-case.

urgent=1 next=7
