# nova-tokens dogfooding report

Date: 2026-10-07
Friend: freddy
Tool: nova-tokens

## Findings

1. `nova-tokens` (bare verb)
   Printed: TOKENS REFUSED: no verb given; the verbs are fold, report, ledger, sum, check, sources, profiles, session, version, and sources is the one that only looks; run: nova-tokens help
   Expected: door grammar states `<tool>[ <verb>] REFUSED:` with what was wrong and remedy
   Grade: NEXT

2. `nova-tokens fold --out ./out`
   Printed: TOKENS REFUSED: --out is required; it wants the directory the day files are written to; refusing to guess; run: nova-tokens help
   Expected: names what --out wants and its unit
   Grade: NEXT

3. `nova-tokens check --out ./out`
   Printed: CHECK REFUSED: --out is required; it wants the directory the day files are written to; refusing to guess; run: nova-tokens help
   Expected: status word leads line with tool name (nova-tokens check REFUSED: not just CHECK REFUSED:)
   Grade: NEXT

4. `nova-tokens ledger --month 2026-08 --out ./out`
   Printed: LEDGER FAILED month=2026-08 days=0 rows=0 bad=0
   Expected: ledger verb runs and writes to store; without live store shows failure
   Grade: NEXT

## READ 8/10
Help is complete and refusals name what inputs want; example is near end of banner.

## USE 8/10
First run is honest but status word varies (TOKENS vs CHECK vs LEDGER).

urgent=0 next=4
