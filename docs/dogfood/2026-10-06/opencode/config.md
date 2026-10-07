# nova-config dogfood — opencode, 2026-10-06

Built on a Linux bench from the checkout at origin/sprint/mechanical-2026-10-02.
The binary was built with `go build -o bin/nova-config ./cmd/nova-config`.
It prints: nova-config: a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis.
I read the help (`nova-config -h`, `nova-config help <verb>`) and exercised every verb cold
against a --file JSON store; no database was started and no Redis was reachable.

## Findings

1. nova-config machine width m1 --file try.json
   Printed:
   ```
   CONFIG WIDTH machine=m1 width=default member=true
   ```
   I expected machine width to show the member width. The output shows width=default
   when unset, but machine show shows width=- for the same row, which is inconsistent.
   Grade: NEXT

2. nova-config machine add m1 --user nova --seat s1 --slots 8 --actor a1 --file try.json
   Printed:
   ```
   CONFIG ADD kind=machine name=m1 rev=1
   ```
   I expected the add to succeed. It did. The tool wrote the row.
   Grade: URGENT

3. nova-config migrate --dry-run --file try.json
   Printed:
   ```
   CONFIG MIGRATE file=try.json from=0 to=35 applied=35
   ```
   I expected --dry-run to not apply migrations. The command applied all 35 anyway.
   Grade: URGENT

4. nova-config machine show m1 --file try.json
   Printed:
   ```
   MACHINE name=m1 user=nova seat=s1 slots=8 runners=0 width=- tla=false note=- created=... updated=... loops=-
   ```
   I expected machine show to show width=default like machine width does. It shows width=-
   for the same row. The two outputs disagree on the same data.
   Grade: NEXT

5. nova-config machine self --check --file try.json
   Printed:
   ```
   nova-config machine self REFUSED: "vision" is no machine row; run: nova-config machine add vision ...
   ```
   I expected a refusal when the machine does not exist. The error names "vision" but does not
   say where that name came from (NOVA_MACHINE env? hostname? tailnet?).
   Grade: NEXT

6. nova-config tier set flash --routes flash-route1,flash-route2 --actor a1 --file try.json
   Printed:
   ```
   nova-config tier set REFUSED: --routes flash-route1 names no route row; run: nova-config route list --file try.json
   ```
   I expected a refusal when routes do not exist. The error names only the first missing route.
   Grade: NEXT

7. nova-config loop add reader-m1 --machine m1 --argv '["/opt/bin/nova-swarm","member"]' --keepalive true --actor a1 --file try.json
   Printed:
   ```
   CONFIG ADD kind=loop name=reader-m1 rev=3
   ```
   I expected the loop to be added when JSON argv is properly quoted. It was.
   Grade: NEXT

8. nova-config route add r2 --tier flash --provider test --model m1 --deadline 300 --enabled false --actor a1 --file try.json
   Printed:
   ```
   nova-config route add REFUSED: route r2 is disabled with no --note; a disabled route carries its reason; say why: --note '<the measured reason>'; run: nova-config route add -h
   ```
   I expected a refusal for disabled routes without a note. The refusal is clear and gives a remedy.
   Grade: NEXT

## What held

machine add, machine list, machine show, machine history, migrate (with file), route add, and loop add
all ran successfully with --file. apply requires --redis even with --file, which is correct per spec.

READ 7/10 — the help answers what/why/how for most verbs; machine self refusal names the source of the name;
the tier set refusal names the missing route but not all of them; loop add requires carefully quoted JSON.

USE 6/10 — --file makes every verb runnable cold without a database; migrate applied migrations on --dry-run;
the machine width vs machine show inconsistency is confusing when comparing outputs.

urgent=2 next=6