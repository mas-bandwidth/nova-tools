# nova-tokens Dogfood Report

Run: 2026-10-07
By: Freddy (inception/mercury-2.5, opencode)

## Findings

1. **nova-tokens version**
   - Output:
     nova-tokens devel linux/amd64 go1.26.6
   - Expected: Build string with version info
   - Grade: NEXT (minor: should show explicit version tag rather than "devel" for release builds)

2. **nova-tokens fold --out /tmp/tokens-test/out --day 2026-09-11 --repos cmd/nova-tokens/testdata/example-bench/repos.tsv --claude bench=cmd/nova-tokens/testdata/example-bench/transcripts --bus cmd/nova-tokens/testdata/example-bench/bus**
   - Output:
     TOKENS FOLD at=2026-10-07T14:50:50Z build=devel out=/tmp/tokens-test/out sources=3 days=2026-09-11
     TOKENS SOURCE label=claude:bench kind=claude path=cmd/nova-tokens/testdata/example-bench/transcripts reports=input,output,cache_write,cache_read files=1 unreadable=0 messages=3 rows=2
     TOKENS SOURCE label=bus:emma kind=bus path=cmd/nova-tokens/testdata/example-bench/bus/from-emma reports=input,output files=1 unreadable=0 comments=1 rows=1
   - Expected: Day file created with token counts
   - Grade: OK

3. **nova-tokens check --out /tmp/tokens-test/out**
   - Output:
     CHECK OK at=2026-10-07T14:51:50Z build=devel files=1 rows=3 first=2026-09-11 last=2026-09-11 missing=0 stray=0 gap=0 notes=0
   - Expected: Gate passes when day files are valid
   - Grade: OK

4. **nova-tokens sum --out /tmp/tokens-test/out --month 2026-09**
   - Output:
     SUM MONTH month=2026-09 at=2026-10-07T14:51:52Z build=devel days=1 rows=3 turns=3
     SUM PAIR model=claude-fable-5-1 repo=schema input=908 output=1535 cache_write=1200 cache_read=242000
     SUM PAIR model=gemini-2.5-pro repo=schema input=123456 output=7890
   - Expected: Monthly summary from day files
   - Grade: OK

5. **nova-tokens sources --repos cmd/nova-tokens/testdata/example-bench/repos.tsv --day 2026-09-11 --claude bench=cmd/nova-tokens/testdata/example-bench/transcripts**
   - Output:
     SOURCES SOURCE label=claude:bench kind=claude path=cmd/nova-tokens/testdata/example-bench/transcripts files=1 messages=3 rows=2
     SOURCES OK sources=1 files=1 messages=3 unreadable=0 unparsed=0 rows=2 unattributed=-
   - Expected: Source preview before folding
   - Grade: OK

6. **nova-tokens profiles --swarm-root cmd/nova-tokens/testdata/example-bench/**
   - Output:
     PROFILES OK models=0 cards=0 overshoot=0
   - Expected: Profile summary for swarm models
   - Grade: OK

7. **nova-tokens session --claude-session cmd/nova-tokens/testdata/example-bench/transcripts/window.jsonl --out /tmp/tokens-test/out**
   - Output:
     SESSION turns=3 input=1338 cache_write=1200 cache_read=246000 output=1593 weighted=35403 avg_context=82846
     TOKENS DAY day=2026-09-11 written=true rows=4 retained=3 model=claude-fable-5-1 weighted=35403
   - Expected: Session tokens folded into day file
   - Grade: OK

8. **nova-tokens report --who ada --day 2026-09-11 --repos cmd/nova-tokens/testdata/example-bench/repos.tsv --claude bench=cmd/nova-tokens/testdata/example-bench/transcripts**
   - Output:
     2026-09-11	ada	claude-fable-5-1	schema	input	908
     2026-09-11	ada	claude-fable-5-1	schema	output	1535
     2026-09-11	ada	claude-fable-5-1	schema	cache_write	1200
   - Expected: Report body for sharing
   - Grade: OK

9. **nova-tokens ledger --out /tmp/tokens-test/out --day 2026-09-11 --redis unreachable:6379**
   - Output:
     LEDGER FAILED day=2026-09-11 err=tokens:ledger:2026-09-11: dial tcp: lookup unreachable on 127.0.0.53:53: server misbehaving
     LEDGER FAILED day=2026-09-11 days=0 rows=0 bad=1
   - Expected: Refusal when Redis unreachable (exit code 1)
   - Grade: OK

## READ Score

READ 8/10

Documentation is extensive in README.md and CLI help. The examples work well. Missing: a quickstart guide for new users and a glossary of terminology.

## USE Score

USE 9/10

Tool works reliably with clear output. All verbs perform as expected. Minor friction: ledger needs to be able to run against a store even if day files are not present yet.

urgent=0 next=1
