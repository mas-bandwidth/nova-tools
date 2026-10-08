# nova-self-talk dogfood — opencode, 2026-10-06

Built from commit bba7b0ca8 on a Linux bench.
Read the tool help (-h) and docs, then ran every verb against example pages.

## Findings

1. nova-self-talk (no arguments)
   Printed:
   ```
   nova-self-talk REFUSED: no files named; run: nova-self-talk help
   ```
   I expected a clear error telling what the tool wants; it delivers.
   Grade: NEXT (minor friction — hint could suggest simpler shell glob)

2. nova-self-talk version
   Printed:
   ```
   nova-self-talk devel linux/amd64 go1.26.6
   ```
   I expected commit and timestamp; missing those fields.
   Grade: NEXT (missing commit/date for reproducibility)

3. nova-self-talk shapes
   Printed:
   ```
   SHAPES OK rows=22 standing=1 installation=16 licensed=5
   ```
   I expected a paginated output; 22 lines is too long.
   Grade: URGENT (no --max or --json flag for shapes verb)

4. nova-self-talk example ./pages
   Printed:
   ```
   EXAMPLE OK dir=./pages wrote=RULES.md,journal.md
   ```
   I expected files in ./pages but hint said pages/.
   Grade: NEXT (hint path inconsistency)

5. nova-self-talk testpages/journal.md
   Printed:
   ```
   SELFTALK DATED n=1 files=1
   SELFTALK FAIL files=1 claims=2
   SELFTALK NOTE catches known SHAPES only
   ```
   I expected findings before summary; summary printed first.
   Grade: URGENT (findings printed after summary — hard to scan)

## What held

scan ran with --skip --rule-doc --json -h
shapes ran but output is unwieldy
example ran and wrote pages
version ran and printed build identity
help ran and printed usage

READ 7/10 — clear output but summary-before-findings hurts readability
USE 7/10 — verbs work but shapes needs pagination

urgent=2 next=3
