# nova-config dogfood, 2026-10-06 (opencode)

Tool: nova-config. Built from docs/nova-config/README.md and SPEC-CONFIG.md only.
Run cold, with no database and no Redis store: --file for state, help doors, refusals,
and what can run without infrastructure. No real store was reachable and the card
forbids starting one, so every writing verb was exercised against a --file store;
every inspection verb ran with no flags; the apply path was exercised with --check.

## 1. machine width prints member status - NEXT

Command: nova-config machine width m1 --file testdogfood.json

Printed: CONFIG WIDTH machine=m1 width=default member=true
exit 0.

Expected: machine width says it prints the sprint members width on it. A machine
with width 0 or unset should print member=false, but when unset the output shows
width=default which is correct per spec. However, machine list and machine show
print width=- for the same machine, which is inconsistent.

Grade: NEXT

## 2. --file writes history but does not persist between runs - NEXT

Command: nova-config machine add m1 --user nova --seat s1 --slots 8 --as a1 --file try.json
        nova-config machine history m1 --file try.json

Printed: CONFIG ADD kind=machine name=m1 rev=1
        HISTORY id=1 kind=machine name=m1 op=add actor=a1 at=<t> ...
exit 0.

Expected: The history is written but --file is a JSON file that is rewritten on
every write. On a second run, the file may have different state. The guide says it
keeps the rows in a local JSON file, but does not clarify if history is cumulative
across runs. A stranger might expect --file to behave like --pg (persistent).

Grade: NEXT

## 3. migrate does not show which migration would run first - NEXT

Command: nova-config migrate --dry-run --file try.json

Printed: MIGRATION version=1 file=0001_schema.sql lines=... state=pending
exit 0.

Expected: The output shows pending migrations but does not say what state the
database is in (fresh vs partially migrated). A stranger expects to know this is
the first migration vs these are upgrades without reading filenames.

Grade: NEXT

## 4. apply --check requires --as and --redis - NEXT

Command: nova-config apply --check --file testdogfood.json

Printed: nova-config apply REFUSED: --as is required: the name the write is recorded under (or NOVA_FRIEND); --redis is required: host:port (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat); run: nova-config apply -h
exit 2.

Expected: The check shows what would be added to Postgres, but requires --redis
even with --file. A stranger might expect --file to bypass redis requirements.

Grade: NEXT

## 5. machine self --check refuses on unknown machine name - URGENT

Command: nova-config machine self --check --file testdogfood.json

Printed: nova-config machine self REFUSED: vision is no machine row; run: nova-config machine add vision --user <login> --seat <seat> --slots <n> --width <n> --as <name> --file testdogfood.json
exit 2.

Expected: The output says vision is no machine row but does not explain what
vision is (hostname? NOVA_MACHINE? tailnet name?). The output should name which
source gave the name for clarity.

Grade: URGENT

## 6. tier set refuses routes that do not exist - URGENT

Command: nova-config tier set flash --routes flash-route1,flash-route2 --as a1 --file testdogfood.json

Printed: nova-config tier set REFUSED: --routes flash-route1 names no route row; run: nova-config route list --file testdogfood.json
exit 1.

Expected: This is correct behavior - it should refuse when routes do not exist.
However, it only names the first missing route. A stranger might want to know all
missing routes at once.

Grade: URGENT

## 7. loop add requires argv as JSON but gives no example - NEXT

Command: nova-config loop add reader-m1 --machine m1 --argv "["/opt/bin/nova-swarm","member"]" --keepalive true --as a1 --file testdogfood.json

Printed: CONFIG ADD kind=loop name=reader-m1 rev=3
exit 0.

Expected: The help says --argv is a command as a JSON array of strings.
The example uses shell quotes around JSON, but does not show how to escape inside
the array. A stranger might type invalid JSON and get a cryptic error.

Grade: NEXT

## 8. route set with disabled route requires note - NEXT

Command: nova-config route add r1 --tier flash --provider test --model m1 --deadline 300 --enabled false --as a1 --file testdogfood.json

Printed: nova-config route add REFUSED: a disabled route needs a note: say why: --note "<the measured reason>"
exit 1.

Expected: This is correct behavior per spec. The refusal is clear and
gives a remedy.

Grade: NEXT

READ 8/10 - the README answers what/why/how, every verbs -h should work, the
refusals name problems. The score is held down by unclear machine self error,
inconsistent machine list vs machine width output, and missing examples for JSON argv.

USE 6/10 - --file makes the tool runnable cold, but history persistence is unclear,
machine self --check output is confusing, and --file still requires --redis for apply.

urgent=2 next=6
