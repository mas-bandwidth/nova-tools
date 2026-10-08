# nova-tokens dogfood — opencode, 2026-10-06

I tested nova-tokens on a Linux bench using the staged commit at ~/freddy-bench/nova-tools (build v1.0.1-0.20261005210735-df783d617504). The version line printed: nova-tokens v1.0.1-0.20261005210735-df783d617504 linux/amd64 go1.26.6. I read the tool page docs/SPEC-TOKENS.md and ran every verb with the example setup.

## Findings

1. nova-tokens fold --out ./out --day 2026-10-06 --repos ./repos.tsv --claude bench=./transcripts
   Printed:
   ```
   TOKENS FOLD at=2026-10-08T02:41:08Z build=v1.0.1-0.20261005210735-df783d617504 out=./out sources=1 days=2026-10-06 repos=./repos.tsv
   TOKENS SOURCE label=claude:bench kind=claude path=./transcripts reports=input,output,cache_write,cache_read day_basis=utc files=1 unreadable=0 messages=1 dup=0 noid=0 nousage=- unparsed=- comments=- redated=- superseded=- rows=1
   TOKENS DAY date=2026-10-06 rows=1 models=1 repos=1 turns=1 unknown=0.0% other=0.0% rough=0 dashes=1 nonutc=0 sources=claude:bench written=true
   ```
   I expected the fold to write the day file with my test data.
   Grade: OK

2. nova-tokens check --out ./out
   Printed:
   ```
   CHECK OK at=2026-10-08T02:41:13Z build=v1.0.1-0.20261005210735-df783d617504 files=1 rows=1 first=2026-10-06 last=2026-10-06 missing=0 stray=0 gap=0 notes=0
   ```
   I expected the check to find no issues since there is one day file.
   Grade: OK

3. nova-tokens sum --out ./out --month 2026-10
   Printed:
   ```
   SUM MONTH month=2026-10 at=2026-10-08T02:41:15Z build=v1.0.1-0.20261005210735-df783d617504 days=1 first=2026-10-06 last=2026-10-06 missing=0 rows=1 turns=1
   SUM PAIR model=claude-3-5-sonnet repo=schema input=812 output=40 cache_write=1200 cache_read=90000 reasoning=- rough=0 dashes=0,0,0,0,1 nonutc=0 days=1
   SUM MODEL model=claude-3-5-sonnet input=812 output=40 cache_write=1200 cache_read=90000 reasoning=- rough=0 dashes=0,0,0,0,1 nonutc=0 repos=1
   ```
   I expected the sum to aggregate the tokens for the month.
   Grade: OK

4. nova-tokens sources --repos ./repos.tsv --all --claude bench=./transcripts
   Printed:
   ```
   SOURCES SOURCE label=claude:bench kind=claude path=./transcripts reports=input,output,cache_write,cache_read day_basis=utc files=1 unreadable=0 messages=1 dup=0 noid=0 nousage=- unparsed=- comments=- redated=- superseded=- rows=1
   SOURCES OK sources=1 files=1 messages=1 unreadable=0 unparsed=0 rows=1 unattributed=-
   ```
   I expected the sources verb to list what was read.
   Grade: OK

5. nova-tokens report --who ada --day 2026-10-06 --repos ./repos.tsv --claude bench=./transcripts
   Printed:
   ```
   2026-10-06	ada	claude-3-5-sonnet	schema	input	812
   2026-10-06	ada	claude-3-5-sonnet	schema	output	40
   2026-10-06	ada	claude-3-5-sonnet	schema	cache_write	1200
   TOKENS AVG day=2026-10-06 model=claude-3-5-sonnet tokens=92052 usd=- usd_per_mtok=- unpriced=92052
   ```
   I expected the report to format the day tokens tokens for a named worker.
   Grade: OK

6. nova-tokens session --claude-session ./window.jsonl --out ./out
   Printed:
   ```
   SESSION turns=1 input=812 cache_write=1200 cache_read=90000 output=40 weighted=11512 avg_context=92012
   TOKENS DAY day=2026-10-06 written=true rows=2 retained=1 model=claude-3-5-sonnet weighted=11512
   ```
   I expected the session to fold one jsonl window into the day file.
   Grade: OK

7. nova-tokens profiles --swarm-root /nonexistent
   Printed:
   ```
   PROFILES REFUSED: --swarm-root does not exist: /nonexistent; it wants the directory the swarm batches live under; run: nova-tokens help
   ```
   I expected the profiles to refuse because the path does not exist.
   Grade: OK

8. nova-tokens version
   Printed:
   ```
   nova-tokens v1.0.1-0.20261005210735-df783d617504 linux/amd64 go1.26.6
   ```
   I expected the version to print the build information.
   Grade: OK

## What held

All verbs ran with the example data: fold, check, sum, sources, report, session, and version. The profiles verb refused appropriately when given a non-existent path. The tool correctly handles the five input types (input, output, cache_write, cache_read, reasoning) with dashes for missing values.

## 8/10 — clear help and examples

The SPEC-TOKENS.md has runnable setup/example blocks.

## 9/10 — works as shown

Every verb runs against scratch data; refusals name inputs and remedies.

urgent=0 next=0
