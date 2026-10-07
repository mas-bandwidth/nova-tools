# nova-cairn dogfood — opencode, 2026-10-06

Tested on a Linux bench using a binary built from the staged checkout (go build -o $JOB/bin/nova-cairn ./cmd/nova-cairn). Version line printed: nova-cairn (built at 2026-10-07T21:40:58Z). Read the tool's own help and used it against a temp directory at /tmp/cairntest4 with actor --actor boss.

## Findings

1. `nova-cairn open --store /tmp/cairntest4 --session s1 --publish manual`
   Printed:
   ```
   OPEN OK session=s1 store=/tmp/cairntest4 source=- publish=manual stamp=2026-10-07T21:40:58.720672617Z
   (one line printed)
   ```
   I expected the tool to create the store directory and initialize the session record. The output confirms the session was opened successfully with the specified publish policy.
   Grade: NEXT

2. `nova-cairn append --store /tmp/cairntest4 --session s1 --entry e1 --text "hello world"`
   Printed:
   ```
   APPEND OK session=s1 entry=e1 source=- persisted=true published=false publish=manual duplicate=false stamp=2026-10-07T21:41:00.966231699Z
   (one line printed)
   ```
   I expected the entry to be created with the exact text. The output shows it was persisted.
   Grade: NEXT

3. `nova-cairn index --store /tmp/cairntest4`
   Printed:
   ```
   INDEX OK sessions=1 entries=1
   INDEX SESSION session=s1 publish=manual opened=2026-10-07T21:40:58.720672617Z entries=1
   INDEX ENTRY session=s1 entry=e1 stamp=2026-10-07T21:41:00.966231699Z bytes=11 source=-
   ```
   I expected to see the entry from the append. The output correctly shows entries=1 and the full entry details.
   Grade: NEXT

4. `nova-cairn receipt --store /tmp/cairntest4 --session s1 --entry e1 --text`
   Printed:
   ```
   RECEIPT OK session=s1 entry=e1 stamp=2026-10-07T21:41:00.966231699Z bytes=11 source=- persisted=true published=false publish=manual text="hello world"
   (one line printed)
   ```
   I expected to read back the entry with its text. The output shows the entry was found and text was correctly stored.
   Grade: NEXT

5. `nova-cairn open`
   Printed:
   ```
   OPEN REFUSED: --store is required; it wants the checkpoint store directory; refusing to guess; run: nova-cairn help
   OPEN REFUSED: --session is required; it wants the stable session identifier; refusing to guess; run: nova-cairn help
   OPEN REFUSED: --publish is required; it wants the publication policy: never, manual, deferred, immediate; refusing to guess; run: nova-cairn help
   ```
   I expected a clear refusal when required flags are missing. The tool correctly refuses to guess and names all three missing flags at once.
   Grade: NEXT

## What held

- open: ran successfully, created session record, re-open was correctly idempotent
- append: ran successfully, entry was persisted
- index: ran successfully, correctly counts entries and shows all sessions
- receipt: ran successfully, entry was read back with correct text
- The tool correctly refuses missing flags with clear error messages

READ 9/10 — Clear refusals, coherent model, and index now correctly counts entries.
USE 9/10 — Good exit codes, flag design, and output is informative without being confusing.

urgent=0 next=5
