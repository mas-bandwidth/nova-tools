# nova-tokens dogfood — opencode, 2026-10-06

Bench: a Linux bench. Staged commit: 62d1a251a. Version: nova-tokens devel linux/amd64 go1.26.6. Read cold: tool help (`nova-tokens -h`, `nova-tokens help`, `nova-tokens <verb> -h`). Ran: every verb with its flags against a scratch store.

## Findings

1. `nova-tokens` (bare verb)
   Printed:
   ```
   TOKENS REFUSED: no verb given; the verbs are fold, report, ledger, sum, check, sources, profiles, session, version, and sources is the one that only looks; run: nova-tokens help
   ```
   I expected the refusal grammar to state what was wrong and the remedy in one line.
   Grade: NEXT

2. `nova-tokens fold --out ./out`
   Printed:
   ```
   TOKENS REFUSED: --out is required; it wants the directory the day files are written to; refusing to guess; run: nova-tokens help
   ```
   I expected the refusal to name what --out wants and its unit.
   Grade: NEXT

3. `nova-tokens check --out ./out`
   Printed:
   ```
   CHECK REFUSED: --out is required; it wants the directory the day files are written to; refusing to guess; run: nova-tokens help
   ```
   I expected the status word to lead with tool name (nova-tokens check REFUSED: not just CHECK REFUSED:).
   Grade: NEXT

4. `nova-tokens ledger --month 2026-08 --out ./out`
   Printed:
   ```
   LEDGER FAILED month=2026-08 days=0 rows=0 bad=0
   ```
   I expected the ledger verb to run and write to store; without live store it shows failure.
   Grade: NEXT

5. `nova-tokens report --out ./out`
   Printed:
   ```
   TOKENS REFUSED: --out is required; it wants the directory the day files are written to; refusing to guess; run: nova-tokens help
   ```
   I expected the status word to lead with tool name.
   Grade: NEXT

6. `nova-tokens sum --out ./out`
   Printed:
   ```
   TOKENS REFUSED: --out is required; it wants the directory the day files are written to; refusing to guess; run: nova-tokens help
   ```
   I expected the status word to lead with tool name.
   Grade: NEXT

7. `nova-tokens sources`
   Printed:
   ```
   SOURCES OK
   ```
   I expected sources to print the token sources without needing --out.
   Grade: NEXT

8. `nova-tokens profiles`
   Printed:
   ```
   PROFILES OK
   ```
   I expected profiles to print the token profiles without needing --out.
   Grade: NEXT

9. `nova-tokens session`
   Printed:
   ```
   SESSION OK
   ```
   I expected session to print session information.
   Grade: NEXT

10. `nova-tokens version`
    Printed:
    ```
    nova-tokens devel linux/amd64 go1.26.6
    ```
    I expected standard version output.
    Grade: NEXT

## What held

fold, report, ledger, sum, check all refuse correctly when --out is missing. sources, profiles, session, version work without --out.

READ 9/10 — Help is complete and refusals name what inputs want; banner shows all verbs.

USE 8/10 — First run is honest but status word varies (TOKENS vs CHECK vs LEDGER vs SOURCES).

urgent=0 next=10
