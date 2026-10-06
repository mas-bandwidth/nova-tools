# nova-bus dogfood, 2026-10-06 (grok)

Tool: nova-bus. Build: `nova-bus v0.0.0-20261006204015-cb5fb8d4c329 darwin/arm64 go1.26.6`
(the tree's `sprint/mechanical-2026-10-02` tip). Run cold, from the binary's own
help, `docs/CLI.md` and `docs/SPEC-BUS.md` only, with no code read. Every verb ran
against one scratch Redis on the tailnet at loopback through a tunnel: an
open store (no users, so every write says `login=none`) whose `friends` set named
ada, bob, carol and whose `bus2:push` held a fresh proven push for each, so `send`
and `recv` were heard. That seeding is finding 9's subject: without a friend
daemon a stranger cannot make a name heard. No file was changed here, only this
report.

## 1. `--dry-run` on a writing verb turns every earlier refusal into a false FAILED — URGENT

**Command:**

    nova-bus send --as ada --to bob --subject s --body b --dry-run

**Printed:**

    SEND FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1. With an address that does not answer, `send --dry-run --redis 127.0.0.1:1`,
`recv --as bob --dry-run --redis 127.0.0.1:1` and `ack --as bob --id x --dry-run
--redis 127.0.0.1:1` print the same line under their own verb word, each at exit 1.

**Expected:** `send -h` says `--dry-run` "checks the message as send does (every
problem named) and prints the line with no id, writing nothing", and the banner's
first run reaches a store before this. With no store I expected the `SEND REFUSED:
--redis is required ...` line at exit 2, and with an unreachable one the store's
`unreachable` refusal at exit 2. Instead the failure that actually happened is
never named, the status is FAILED (the worst of the three) with no remedy, and the
line asserts the verb "may have written" when it read nothing and wrote nothing.
A refusal that is found before the flag is read is masked by the unconsumed
`--dry-run` guard. `send --dry-run` against a reachable store (a deaf name, a bad
kind, a good send) does read the flag and refuses or plans correctly, so the bug
is the no-store and unreachable-store paths.

## 2. `help --json` (and a bare `--json`) is read as an unknown verb — NEXT

**Command:**

    nova-bus help --json

**Printed:**

    {"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-bus help","why":["unknown verb \"--json\"; the verbs are wait, send, peek, recv, ack, receipts, overdue, log, names, version"]},"facts":{}}

exit 2; `nova-bus --json` gives the same object.

**Expected:** the banner says "Every verb takes `--json`" and `help` stands in the
usage list, so I expected `help --json` to answer as JSON (or the banner to scope
the promise to the store verbs and say help is the exception), not to read `--json`
as the verb argument of `help`. `nova-bus help send --json` does print the help,
so the flag is simply ignored there and refused here.

## 3. `send --re` accepts any string, where `wait --after` enforces the id shape — NEXT

**Command:**

    nova-bus send --as ada --to bob --re nope --subject s --body b --redis 127.0.0.1:26400

**Printed:**

    SEND OK id=01M49FYD0TWKR7VHS90K18NR4B to=bob cc=- at=2026-10-06T20:54:37Z bytes=1 sha256=3e23e8160039594a33894f6564e1b1348bbd7a0088d42c4acb73eeaed59c009d login=none

exit 0.

**Expected:** `--re <id>` is "the id of the message this one answers", and the same
shape is refused elsewhere: `wait --as bob --after not-an-id` says `--after wants a
stream entry id, <ms>-<seq> as WAIT ARMED and WAIT OK print it; "not-an-id" is not
one`. A `re` of `nope` is written to the log and a receipt can never name it, so I
expected `send` to refuse it in the same one line, before writing.

## 4. `ack --id` accepts a malformed id and says OK at exit 0 — NEXT

**Command:**

    nova-bus ack --as bob --id not-a-ulid --redis 127.0.0.1:26400

**Printed:**

    ACK OK acked=0 asked=1 login=none
    ACK ID id=not-a-ulid acked=false

exit 0.

**Expected:** `ack -h` says `--id` is "the message ids, comma-separated, as recv
printed them", and the id shape is knowable with no store (as `wait --after`
shows). I expected a refusal naming the shape, not an OK result that makes a
mistyped id look like a message that was simply never pending.

## 5. The JSON status word is not one shape across verbs that say no — NEXT

**Command:**

    nova-bus recv --as dave --json --redis 127.0.0.1:26400

**Printed:**

    {"result":{"verb":"recv","status":"failed","exit":1,"word":"NONE","why":["nothing for dave"]},"facts":{}}

exit 1; `nova-bus overdue --older 0s --json --redis 127.0.0.1:26400` prints
`{"result":{"verb":"overdue","status":"failed","exit":1,"word":"OVERDUE"},...}`
(exit 1), while `nova-bus wait --as bob --timeout 1s --json --redis
127.0.0.1:26400` prints `{"status":"ok","word":"NONE","after":"...","messages":[]}`
at exit 1.

**Expected:** the text forms all ran and said no (`RECV NONE`, `OVERDUE`, `WAIT
NONE`), the same outcome the exit table calls "the verb ran and said no"; the
standard's one result value has `status ok|refused|failed`, and `wait` already
calls this `ok`. I expected the three to render one status, so a reader's JSON
branch does not treat a healthy `recv`/`overdue` as a failure.

## 6. `peek` and `names` list without a bound and have no `--max` — NEXT

**Command:**

    nova-bus peek --as dave --redis 127.0.0.1:26400

**Printed:**

    PEEK OK pending=2 new=7
    PEEK MESSAGE state=pending id=01M49G0BWFF3JK6NC5KWRV84TM from=ada at=2026-10-06T20:55:41Z subject="jm"
    PEEK MESSAGE state=pending id=01M49G0C0ZNWQYEN95ZQHTCHCZ from=ada at=2026-10-06T20:55:41Z subject="js"

every message follows; `peek -h` lists `--as`, `--json`, `--kind`, `--redis` and
no `--max`, and `names -h` lists `--json` and `--redis` only.

**Expected:** the banner says "A verb that lists takes `--max <n>` (default 20, 0
lists all) and says MORE for the rest". `log`, `receipts` and `overdue` do; `peek`
(whose backlog grows without bound) and `names` (one line per roster row) do not,
so a reader has no ceiling flag and no MORE total.

## 7. `peek` answers for an unknown name where every other reader refuses it — NEXT

**Command:**

    nova-bus peek --as nobody --redis 127.0.0.1:26400

**Printed:**

    PEEK OK pending=0 new=0

exit 0.

**Expected:** `recv --as nobody` says `RECV REFUSED: nobody is no known name; the
names are nova-config's friend and machine rows (nova-bus names lists them); add
one with nova-config friend add ...`, and `send`, `wait` and `receipts` refuse the
same. I expected `peek`, the reader a stranger tries first, to refuse too, so a
name typo reads as "no such name" instead of an empty inbox.

## 8. An address with surrounding spaces is refused as "does not resolve" — NEXT

**Command:**

    nova-bus names --redis ' 127.0.0.1:26400 '

**Printed:**

    NAMES REFUSED: nova-bus reaches a store over loopback or the tailnet (100.64.0.0/10) only:  127.0.0.1:26400  does not resolve, so what it would dial is unknown; run: nova-bus help

exit 2.

**Expected:** the address is a valid loopback `host:port` with a leading and
trailing space (a paste from a row with padding). I expected the tool to trim it,
or to say the address has surrounding whitespace; the line instead names the
loopback/tailnet rule and says it "does not resolve", which blames resolution for
a spelling the reader can fix in one turn.

## 9. The proof a name must carry is not the one the spec's field list describes — NEXT

**Command:**

    redis-cli -p 26400 hset bus2:push ada '{"up":true,"harness":"grok","nonce":"n1","proven":true,"at":1791320000}'
    nova-bus names --redis 127.0.0.1:26400

**Printed:**

    NAMES OK count=4 proven=0
    NAMES NAME name=ada push=none age=never harness=-
    NAMES NAME name=bob push=none age=never harness=-

and `send --as ada --to bob` is refused `deaf: ada has no proven push since never`.

**Expected:** `SPEC-BUS.md` says `bus2:push` holds the proof "as JSON (`harness`,
`nonce`, `proven`, `up`, `reason`, `at`)" and that "freshness is read against the
store's time"; from that list I built the obvious value (`up` a boolean, `proven` a
boolean, `at` the store's Unix seconds) and `names` read `none`. The value the tool
accepts is `{"harness":"grok","nonce":"...","proven":"<RFC 3339 with a zone>","up":true,"at":"<RFC 3339 UTC>"}`:
`proven` is a timestamp, not the boolean its name suggests, and the two times are
RFC 3339 strings while the spec calls the field the store's time. With no
store-free form and every write gated on this value, a reader following the page
cannot reach a single success path; the exact encoding belongs in the data
section.

READ 7/10 — the banner answers what, how and how-to in its first lines, every
verb's `-h` exits 0 and names its effect class and exit codes, and the refusals
are one line with a remedy that names the rule and the next command; the score is
held down by `--dry-run` whose line lies about writing, `help --json` refused
under a banner that promises `--json` to every verb, and the spec's proof value
that is not the one the tool reads.

USE 6/10 — with a scratch store every verb's real path ran (send, wait, peek, recv,
ack, receipts, overdue, log, names, the `--kind`, `--re`, `--token`, `--wake-file`,
`--forever` and `--max` variations) and the token retry, the receipt stages, the
filters and the at-least-once looping behaved as the help says, but a cold
stranger meets a wall first: no store-free form, a roster and a hand-written push
proof the page does not describe exactly, and a `--dry-run` that reports FAILED
when the store is absent.

urgent=1 next=8
