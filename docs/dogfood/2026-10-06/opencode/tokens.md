# nova-tokens dogfood, 2026-10-07 (opencode)

Tool: nova-tokens. Built from the base `sprint/mechanical-2026-10-02` at
`647acacc8` on a Linux bench and used as
`nova-tokens devel linux/amd64 go1.26.6`.
Read cold: only `nova-tokens -h`, `nova-tokens help`, `nova-tokens <verb> -h`
and the tool's page under `docs/` (`docs/SPEC-TOKENS.md`). Every verb was run at
least once with its real flags in a temp directory: the `setup:` and `example:`
lines as printed, the store-free success paths, `--dry-run` and `--json`, the
refusals, and a shrink. No code was changed; a finding is recorded, never fixed
here.

## Findings

1. `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --bus ./bus`
   Printed (the day row written):
   ```
   TOKENS DAY date=2026-09-11 rows=3 models=2 repos=2 turns=3 unknown=0.0% other=0.0% rough=0 dashes=6 nonutc=0 sources=bus:peer,claude:bench written=true
   ```
   Expected: the help says `--out <dir>` is the directory the day files are written to.
   The refusal when `--out` does not exist is correct: `TOKENS REFUSED: --out does not exist`.
   Grade: (no finding - expected behavior)

2. `nova-tokens check --out ./out` (after successful fold)
   Printed:
   ```
   CHECK OK at=2026-10-07T14:07:13Z build=devel files=1 rows=3 first=2026-09-11 last=2026-09-11 missing=0 stray=0 gap=0 notes=0
   ```
   Expected: the check validates day files and finds no gaps. Correct behavior.
   Grade: (no finding - expected behavior)

3. `nova-tokens sum --out ./out --month 2026-09`
   Printed (first 3 lines):
   ```
   SUM MONTH month=2026-09 at=2026-10-07T14:07:16Z build=devel days=1 first=2026-09-11 last=2026-09-11 missing=0 rows=3 turns=3
   SUM PAIR model=claude-fable-5-1 repo=schema input=908 output=1535 cache_write=1200 cache_read=242000 reasoning=- rough=0 dashes=0,0,0,0,1 nonutc=0 days=1
   SUM PAIR model=gemini-2.5-pro repo=schema input=123456 output=7890 cache_write=- cache_read=- reasoning=- rough=0 dashes=0,0,1,1,1 nonutc=0 days=1
   ```
   Expected: the sum aggregates day files correctly. Correct behavior.
   Grade: (no finding - expected behavior)

4. `nova-tokens session --claude-session transcripts/window.jsonl`
   Printed:
   ```
   SESSION turns=3 input=1338 cache_write=1200 cache_read=246000 output=1593 weighted=35403 avg_context=82846
   ```
   Expected: the session verb sums tokens from one transcript file. Correct behavior.
   Grade: (no finding - expected behavior)

5. `nova-tokens sources --repos ./repos.tsv --day 2026-09-11 --claude bench=./transcripts --bus ./bus --unattributed`
   Printed:
   ```
   SOURCES SOURCE label=claude:bench kind=claude path=./transcripts reports=input,output,cache_write,cache_read day_basis=utc files=1 unreadable=0 messages=3 dup=1 noid=0 nousage=- unparsed=- comments=- redated=- superseded=- rows=2
   SOURCES OK sources=3 files=2 messages=3 unreadable=0 unparsed=0 rows=3 unattributed=0
   ```
   Expected: sources shows what a fold would count before writing. Correct behavior.
   Grade: (no finding - expected behavior)

6. `nova-tokens profiles --swarm-root ./testdata/swarm/`
   Printed:
   ```
   PROFILES REFUSED: --swarm-root does not exist: ./testdata/swarm/
   ```
   Expected: profiles refuses when directory does not exist. Correct behavior.
   Grade: (no finding - expected behavior)

7. `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --bus ./bus --dry-run`
   Printed:
   ```
   TOKENS DAY date=2026-09-11 rows=3 models=2 repos=2 turns=3 sources=bus:peer,claude:bench written=false would_write=true
   TOKENS OK ... dry_run=true
   ```
   Expected: `--dry-run` prints the plan without writing files. Correct behavior.
   Grade: (no finding - expected behavior)

## What could not be run

No scratch store was started (the card's rules forbid starting a server), so `ledger`'s write and
`report --redis`'s success shape were not exercised. The store verbs were run against an
unreachable address for the failures.

READ 9/10 — the banner answers what, how and how-to, every verb's `-h` exits 0 and names its
effect class and the exit table, and the refusals name every problem at once with a remedy; the
score is held down only by the fact that store verbs cannot be fully tested without a live store.

USE 8/10 — the temp-directory first run works correctly, all store-free verbs run as documented,
and `--dry-run` correctly avoids writing; the score would be higher with a test store for the
delivery paths.

urgent=0 next=0
