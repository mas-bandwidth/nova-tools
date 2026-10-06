# nova-bus dogfood, 2026-10-06 (opencode)

Tool: nova-bus. Build: `nova-bus v1.0.1-0.20261006184220-4b29da81ad74 darwin/amd64 go1.26.6`.
Run cold, from the binary's own help and the tool's page in `docs/CLI.md` only. No Redis
store was reachable and the card forbids starting one, so every verb was exercised against
an absent or unreachable store: `version` and the help doors ran, the refusals ran, and no
writing verb's success path could be run at all. That limitation is itself finding 2.

## 1. `--dry-run` on the writing verbs fails on the refusal path and says it may have written — URGENT

**Command:**

    nova-bus send --as ada --to bob --subject hi --body there --dry-run

**Printed:**

    SEND FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1. `recv --dry-run` and `ack --id <id> --dry-run` print the same line under their own verb word.

**Expected:** `send -h` says `--dry-run` "checks the message as send does (every problem named)
and prints the line with no id, writing nothing", and `docs/CLI.md` lists `--dry-run` on send,
recv and ack. With no store I expected the `SEND REFUSED: --redis is required ...` line at exit 2;
with one, a `SEND OK ... dry_run=true` line at exit 0. Instead the real problem is never named,
the status is FAILED (the worst of the three) at exit 1 with no remedy, and the line asserts the
verb "may have written" when nothing was written. A refusal that happens before the verb reads
the flag is masked by the unconsumed `--dry-run` guard.

**Grade:** URGENT

## 2. No store-free form: the example block cannot run cold — NEXT

**Command:**

    nova-bus wait --as bob --timeout 1s

**Printed:**

    WAIT REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help

exit 2. Every line of the `example:` block (`wait`, `send`, `peek`, `recv`, `ack`, `log`,
`names`) prints a refusal like this on a machine with no store, as does every other invocation
of every verb.

**Expected:** the standard's first property is that a tool is "runnable on its own small input
with no infrastructure"; onboarding point 1 says an example that exits 2 is a broken example,
and point 4 asks for a `quickstart` verb where the tool has a natural first run. nova-bus has a
natural first run (send a note, peek, recv, ack) but no `quickstart` and no fixture or in-memory
store, so nothing in the banner can be pasted and run, and a stranger cannot try a single verb's
success path before standing up Redis plus a nova-config roster. The banner does name the
prerequisite (`first run: a Redis naming ada and bob`), so this is friction, not a lie.

**Grade:** NEXT

## 3. Pure flag values are validated only after the store dial — NEXT

**Command:**

    nova-bus peek --as bob --kind bogus

**Printed:**

    PEEK REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help

exit 2.

**Expected:** `peek -h` says `--kind` is "one of report, ack, status, request, blocker"; an
invalid kind is knowable with no store, so I expected a `--kind bogus` refusal naming the allowed
kinds at exit 2, before any dial. The same holds for a bad `--token`:

    nova-bus send --as ada --to bob --subject hi --body there --token 'bad token!' --redis 127.0.0.1:1

prints the store's `unreachable` line, never the token shape the help states. Onboarding point 2
asks one run to report every problem it can find; with a bad value the run instead reports only the
missing store.

**Grade:** NEXT

## 4. `ack --id` accepts any shape until the store answers — NEXT

**Command:**

    nova-bus ack --as bob --id not-a-ulid

**Printed:**

    ACK REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help

exit 2.

**Expected:** `ack -h` says `--id` is "the message ids, comma-separated, as recv printed them",
and ids have a shape (`wait --after bad-id` is refused before the store without one). I expected
the same refusal for a garbage id, not a store message, so a caller learns the id is wrong on the
first turn rather than after a live store answers.

**Grade:** NEXT

## 5. `help --json` is read as an unknown verb — NEXT

**Command:**

    nova-bus help --json

**Printed:**

    {"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-bus help","why":["unknown verb \"--json\"; the verbs are wait, send, peek, recv, ack, log, names, version"]},"facts":{}}

exit 2.

**Expected:** the banner says "Every verb takes `--json`", and `help` stands in the usage list as
`nova-bus help [<verb>]`. I expected `help --json` to accept the flag (or the banner to scope the
promise to the store verbs), not to read `--json` as the verb argument and refuse it.

**Grade:** NEXT

READ 7/10 — the banner answers what, how and how-to, every verb's `-h` exits 0 and names its
effect class and exit codes, and the refusals and the nearest-verb suggestion are one line with a
remedy; the score is held down by the example block that cannot run without infrastructure and by
the `--dry-run` and `help --json` help that does not hold.

USE 4/10 — no store was reachable and the card forbids starting one, so only `version` and the
refusals could be run, and the one path that should be safe with no live state (`--dry-run`)
reported FAILED and a false "may have written"; with a scratch Redis the score would be much
higher, but a cold stranger meets exactly this wall.

urgent=1 next=4
