# nova-tokens dogfooding report

Date: 2026-10-07
Friend: freddy
Tool: nova-tokens

## Findings

1. nova-tokens version
   Printed: nova-tokens devel linux/amd64 go1.26.6
   Expected: Version shows explicit tag
   Grade: NEXT - shows devel instead of tag

2. nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts
   Printed: TOKENS FOLD at=2026-10-07T15:47:40Z build=devel out=./out sources=1 days=2026-09-11 repos=./repos.tsv
   Expected: Fold to complete and write day file
   Grade: NEXT - build shows devel during development

## READ 10/10
All verbs work as expected with clear output.

## USE 10/10
Tool is well designed for its purpose; all flags are explicit and there are no defaults.

urgent=0 next=2
