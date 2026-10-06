# nova-config dogfood, 2026-10-06 (dsh)

Tool: nova-config. Build: `nova-config devel darwin/arm64 go1.26.6`, from the
staged checkout at 6ec8bb02edc83283630f2e10e21bd035b3336f65, run from opencode.
Read as a stranger: only `nova-config -h`, `nova-config help`,
`nova-config <verb> -h` and the page under `docs/nova-config/README.md`. Every
verb ran at least once with its real flags: the whole store against a scratch
`--file` JSON store, `apply`, `status --redis`, `inventory --redis` and
`machine list --redis` against a scratch Redis stood up on a bench machine
(never on this one), `login`/`logout` against a scratch nova-secrets store, and
the refusals too — about 20 minutes of use, no code changes.

## 1. A dead Redis prints raw client log lines before the one-line refusal — NEXT

**Command:**

    nova-config status --file try.json --redis 127.0.0.1:1

**Printed (first 3 lines, stderr):**

    redis: 2026/10/06 16:25:28 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
    redis: 2026/10/06 16:25:29 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
    redis: 2026/10/06 16:25:29 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused

then the tool's own line, `nova-config status REFUSED: redis: read machines:
dial tcp 127.0.0.1:1: connect: connection refused; run: nova-config status -h`,
exit 2. Four such `redis:` lines print first, and `inventory` against the same
dead store prints its own four.

**Expected:** the refusal is right and carries its remedy, and per
docs/STANDARD.md ("the status word leads every line") I expected it to be the
only line on the stream; instead the Go client's internal log prints first, with
timestamps and `pool.go` line numbers a cold reader cannot act on, and the
client retries the dial for about five seconds before the tool gets to refuse.

**Grade:** NEXT

## 2. `apply` with two missing inputs names one per run — NEXT

**Command:**

    nova-config apply --file try.json --dry-run

**Printed:**

    nova-config apply REFUSED: --redis is required: host:port (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat); run: nova-config apply -h

exit 2, with `--as` also missing and not named; the next run, given `--redis`,
refuses `--as is required: the name the write is recorded under (or
NOVA_FRIEND)`.

**Expected:** both are missing inputs the tool can see at once, and onboarding
point 2 asks one run to report every problem it can find; naming `--as` beside
`--redis` would let a caller fix the call in one turn instead of two.

**Grade:** NEXT

## 3. `help --json` is read as an unknown verb — NEXT

**Command:**

    nova-config help --json

**Printed:**

    nova-config REFUSED: unknown verb "--json"; want kinds, migrate, status, apply, inventory, login, logout, or a kind (machine, fleet, friend, sprint, loop, route, tier) then add|set|remove|list|show|history; run: nova-config help

exit 2.

**Expected:** every store verb takes `--json` and renders the same value, and
the standard's "one shape across the set" reads as covering the `help` door; I
expected `help --json` to answer the banner as JSON (or the banner to scope the
promise). The same shape was found in nova-bus
(docs/dogfood/2026-10-06/opencode/bus.md, finding 5), so it is a shared corner,
not this tool's alone.

**Grade:** NEXT

## What the tool got right (no finding, kept short)

The `example:` block runs as printed. `--file` is a real store twin: every
kind's add, set, remove, list, show and history ran against it, `kinds`,
`migrate --print`/`--dry-run`, `status`, `login`/`--check`/`logout` (the login
round trip resolved a password through a scratch seat and never printed it),
`machine width`, `machine self`/`self --check` (exit 2 for no row, exit 3 for
an unreadable store, both as the exit table says). Every refusal probed was one
line in the documented grammar and pasted the `--file` it was given: existing
row, missing row, bad name shape, bad values, missing required fields named all
at once each with what it wants, unknown verb and flag with the nearest, the
loop `--width`-in-argv rule, `--every` with `--keepalive`, `--keys` without
`--seat`, coordinator machine and friend removals, route in a tier's array,
`--enabled false` without `--note` and clearing the note of a disabled route,
cross-tier `tier set`, `tier remove`, `--pg` carrying a password, the empty
variable named by `NOVA_PG_PASSWORD_ENV`, and `--pg` with `--file`. `--dry-run`
on the writing verbs printed the plan and wrote nothing. `apply` against a
scratch Redis: `--dry-run` the plan, the CEILING refusal with its remedy, the
roles refusal for an actor who is no friend, the full apply as the
coordinator, and the second apply a no-op (`add=0 set=0 remove=0`), exactly as
the page says; `inventory --fixture` and `--host` answered the Ansible shape.
The README matched the binary on every claim checked, including `--check` as
`--dry-run`'s alias on apply.

The card's own mechanics: the named test `TestDocsTreeIsConsistent` is not in
./internal/docs at this tip (already recorded in
docs/dogfood/2026-10-06/opencode/ci.md), so the gate below ran the packages
whole; the report path needed no catalog row or `make map` (the docs/dogfood
row covers it).

READ 9/10 — the banner answers what, how and first-run, every verb's `-h`
carries its flags with what each wants, its effect class, an example that runs
and the exit table, and the refusals paste back the store I gave them; only the
`help --json` door and the one-problem-per-run `apply` refusal keep it off a
10.

USE 9/10 — the `--file` twin let a stranger use every verb but apply's write
with no database at all, and apply itself needed only a scratch Redis before the
whole round trip, machines to tiers, worked first try; the raw `redis:` lines
on a dead store are the one stumble a cold caller meets unaided.

urgent=0 next=3
