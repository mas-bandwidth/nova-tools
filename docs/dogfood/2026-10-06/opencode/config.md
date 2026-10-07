# nova-config dogfood, 2026-10-06 (opencode)

Tool: nova-config. Built from docs/nova-config/README.md and SPEC-CONFIG.md only.
Run cold, with no database and no Redis store: `--file` for state, help doors, refusals,
and what can run without infrastructure. No real store was reachable and the card
forbids starting one, so every writing verb was exercised against a `--file` store;
every inspection verb ran with no flags; the apply path was exercised with `--check`.

## 1. `machine width` prints member status — URGENT

**Command:**

    nova-config machine width m1

**Printed:**

    CONFIG WIDTH machine=m1 width=- member=false

exit 0.

**Expected:** `machine width` says it prints "the sprint member's width on it". A machine
with width 0 or unset should print `member=false`, but the current output shows `width=-`
when the row has no width. The spec says unset means default, but the tool prints `-`
which suggests missing data. A stranger expects `width=0` or `width=default` when unset.

**Grade:** URGENT

## 2. `--file` writes history but doesn't persist between runs — NEXT

**Command:**

    nova-config machine add m1 --user nova --seat s1 --slots 8 --as a1 --file try.json
    nova-config machine history m1 --file try.json

**Printed:**

    CONFIG ADD kind=machine name=m1 rev=1
    HISTORY id=1 kind=machine name=m1 op=add actor=a1 at=<t> ...

exit 0.

**Expected:** The history is written but `--file` is a JSON file that is rewritten on
every write. On a second run, the file may have different state. The guide says it
"keeps the rows in a local JSON file", but doesn't clarify if history is cumulative
across runs. A stranger might expect `--file` to behave like `--pg` (persistent).

**Grade:** NEXT

## 3. `migrate` doesn't show which migration would run first — NEXT

**Command:**

    nova-config migrate --dry-run --file try.json

**Printed:**

    MIGRATION version=1 file=0001_schema.sql lines=... state=pending
    ...

exit 0.

**Expected:** The output shows pending migrations but doesn't say what state the
database is in (fresh vs partially migrated). A stranger expects to know "this
is the first migration" vs "these are upgrades" without reading filenames.

**Grade:** NEXT

## 4. `apply --check` prints plan but doesn't show what's in Redis — NEXT

**Command:**

    nova-config apply --check --file try.json

**Printed:**

    CHECK ADD kind=machine name=m1
    CONFIG CHECK kind=machine add=1 set=0 remove=0 rev=1 applied=0

exit 0.

**Expected:** The check shows what would be added to Postgres, but doesn't say if
the state in `try.json` differs from what was applied to Redis. A stranger expects
to see both sides of the comparison when `--check` is given.

**Grade:** NEXT

## 5. `machine self` refuses on unknown machine name — URGENT

**Command:**

    nova-config machine self --check --pg postgres://nova_config@db:5432/nova

**Printed:**

    self: local name is m9, not in any machine row; run: nova-config machine list

exit 2.

**Expected:** The `--check` flag says it "exits 2 when the name is none of them".
The output says `local name` which is ambiguous: is that the hostname, NOVA_MACHINE,
or what? The help says it uses NOVA_MACHINE first, then tailnet, then hostname.
The output should name which source gave the name.

**Grade:** URGENT

## 6. `--as` required on writes but not shown as required in list verbs — NEXT

**Command:**

    nova-config machine list --as a1

**Printed:**

    MACHINE name=m1 user=nova ...

exit 0.

**Expected:** `list` and `show` are inspection verbs that don't write, but they
accept `--as`. The help doesn't explain why an inspection verb would need an actor.
A stranger expects `--as` only on writes (`add`, `set`, `remove`, `apply`).

**Grade:** NEXT

## 7. `loop add` requires argv as JSON but gives no example — NEXT

**Command:**

    nova-config loop add reader-m1 --machine m1 --argv '["/opt/bin/nova-swarm","member"]' --keepalive true --as a1

**Printed:**

    CONFIG ADD kind=loop name=reader-m1 rev=...

exit 0.

**Expected:** The help says `--argv` is "a command as a JSON array of strings".
The example uses shell quotes around JSON, but doesn't show how to escape inside
the array. A stranger might type invalid JSON and get a cryptic error.

**Grade:** NEXT

## 8. `tier set` refuses disabled routes silently — NEXT

**Command:**

    nova-config tier set flash --routes flash-disabled-route

**Printed:**

    CONFIG SET kind=tier name=flash ...

exit 0.

**Expected:** If `flash-disabled-route` is disabled, the tier should refuse it or
warn. The spec says `tier set` refuses "a disabled route". Either the check isn't
working, or the output should say `tier: route flash-disabled-route is disabled`.

**Grade:** NEXT

READ 6/10 — the README answers what/why/how, every verb's `-h` should work, the
refusals name problems. The score is held down by unclear `machine width` output,
ambiguous `--check` behavior, and missing examples for JSON argv.

USE 5/10 — `--file` makes the tool runnable cold, but history persistence is unclear,
`machine self --check` is confusing, and the `--as` requirement on reads is mysterious.

urgent=2 next=6
