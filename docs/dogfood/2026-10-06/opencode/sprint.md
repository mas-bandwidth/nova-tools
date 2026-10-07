# nova-sprint dogfood — opencode, 2026-10-06

A Linux bench built from commit 62d1a251aa84857342114f90e3f5a58a40131e00, version `nova-sprint devel linux/amd64 go1.26.6`. Read help for `init`, `add`, `queue`, `where`, `log`, `run`, `help`, `selftest`, and `version`. Ran `where` and `log` against an in-memory twin (`mem:/tmp/sprint.twin`), and tested `selftest` which requires seat setup. Used actor `--actor boss`.

## Findings

1. `nova-sprint where`
   Printed:
   ```
   SPRINT TABLE  coordinator boss
   
   0/1 0.0% -> ETA -
   backup: merges (merging 1 > review 0 + working 0), 0 reads waiting
   ```
   I expected a summary of the sprint state including cards, members, and fleet. This output showed the coordinator, stream status, and member fleet.
   Grade: NEXT

2. `nova-sprint selftest`
   Printed:
   ```
   SELFTEST FAILED step=add why=nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded
   ```
   I expected selftest to verify basic operations work. It failed because seat/push configuration is required even with in-memory store.
   Grade: NEXT

3. `nova-sprint help init`
   Printed:
   ```
   usage: nova-sprint init [--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--owner <name>] [--rules <file>]
   ```
   I expected usage information for init. The help shows flags and examples clearly.
   Grade: NEXT

4. `nova-sprint add --stream s1 --count 1 --one`
   Printed:
   ```
   nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded
   ```
   I expected add to create cards. It requires seat installation even with in-memory store.
   Grade: NEXT

5. `nova-sprint run --help`
   Printed:
   ```
   usage: nova-sprint run [--answer-rules=false] [--idle-alarm=false] [--listen <address:port>] [--land] [--decide <dir>]
   ```
   I expected run options. The help shows server and land configuration flags.
   Grade: NEXT

## What held

The `where`, `log`, and `version` verbs work without full setup. The `queue` verb showed its help correctly.

## READ 7/10 — The help outputs are complete but lack quick-start guidance for testing

## USE 6/10 — Basic inspection works but write operations require seat setup that complicates testing

urgent=0 next=5
