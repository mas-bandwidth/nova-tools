1. Command: `nova-check links -h`
   Output (first 3 lines):
   ```
   usage: nova-check links [flags]
   from `nova-check help`:
     nova-check links --dir <dir> [--file <path>] [--exclude <prefix>] [--max <n>]
   ```
   Expected: the docs page and help should identify the same primary listing-limit flag. `docs/SPEC.md` shows `--fail-max` in the links syntax, while help shows `--max` and later calls `--fail-max` the old spelling for one release; both flags work, but the discrepancy makes the documented interface unclear.
   Grade: NEXT

READ 8/10 — The help and nova-check section explain the flags, effects, and refusals well, though their listing-limit syntax differs.
USE 8/10 — I ran every advertised verb and dogfood subverb against scratch files and a scratch repository, including write, dry-run, refusal, and recovery cases; the flag mismatch was the only issue worth recording.
urgent=0 next=1
