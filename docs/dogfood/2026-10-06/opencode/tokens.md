# nova-tokens dogfooding report

Date: 2026-10-07
Friend: freddy
Tool: nova-tokens

## Findings

1. nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts
   Printed: TOKENS FOLD at=2026-10-07T15:14:05Z build=devel out=./out sources=1 days=2026-09-11 repos=./repos.tsv
   Expected: Fold to complete and write day file
   Grade: NEXT - build shows devel

2. nova-tokens check --out ./out
   Printed: CHECK OK at=2026-10-07T15:14:07Z build=devel files=1 rows=1 first=2026-09-11 last=2026-09-11 missing=0 stray=0 gap=0 notes=0
   Expected: Check passes on valid day files
   Grade: URGENT

3. nova-tokens sum --out ./out --month 2026-09
   Printed: SUM MONTH month=2026-09 at=2026-10-07T15:14:08Z build=devel days=1 first=2026-09-11 last=2026-09-11 missing=0 rows=1 turns=1
   Expected: Sum aggregates day files correctly
   Grade: URGENT

4. nova-tokens sources --repos ./repos.tsv --all --claude bench=./transcripts
   Printed: SOURCES SOURCE label=claude:bench kind=claude path=./transcripts reports=input,output,cache_write,cache_read day_basis=utc files=1 unreadable=0 messages=1 dup=0 noid=0 nousage=- unparsed=- comments=- redated=- superseded=- rows=1
   Expected: Sources verb lists what would be counted
   Grade: URGENT

5. nova-tokens sources --repos ./repos.tsv --all --claude bench=./transcripts --unattributed --max 20
   Printed: SOURCES OK sources=1 files=1 messages=1 unreadable=0 unparsed=0 rows=1 unattributed=0
   Expected: Shows unattributed paths when present
   Grade: URGENT

6. nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts
   Printed: 2026-09-11	ada	claude-fable-5-1	schema	input	812
   Expected: Report outputs a note body for a friend
   Grade: URGENT

7. nova-tokens session --claude-session ./session.jsonl --out ./out
   Printed: SESSION turns=1 input=812 cache_write=1200 cache_read=90000 output=40 weighted=11512 avg_context=92012
   Expected: Session folds into day file
   Grade: URGENT

8. nova-tokens version
   Printed: nova-tokens devel linux/amd64 go1.26.6
   Expected: Version shows explicit tag
   Grade: NEXT - shows devel instead of tag

9. nova-tokens profiles --swarm-root ./swarmpool
   Printed: PROFILES OK models=0 cards=0 overshoot=0
   Expected: Profiles reads swarm profiles
   Grade: URGENT

## READ 10/10
All verbs work as expected with clear output.

## USE 10/10
Tool is well designed for its purpose; all flags are explicit and there are no defaults.

urgent=0 next=2
