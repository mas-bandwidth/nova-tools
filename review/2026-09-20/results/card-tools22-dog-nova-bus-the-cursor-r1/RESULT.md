RESULT tools22-dog-nova-bus-the-cursor-r1 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
SKIP the installed nova-* tools (/home/nova/.local/bin/nova-version and /home/nova/.local/bin/nova-bus) are outside this sandbox's permitted paths — every invocation exits 126 "Permission denied" — so the build could not be confirmed and no nova-bus behavior could be exercised; the section itself contains no fenced command blocks

TOOL nova-bus
VERB The cursor
DOC docs/CLI.md:522-527
REPLICA 1 of 24
BUILD unconfirmed — `nova-version` did not run. It printed to stderr: `/usr/bin/bash: line 1: /home/nova/.local/bin/nova-version: Permission denied` and exited 126. It printed no sha, so the installed build could not be verified against the base 5298f6be12eaa0f7e6622334d2b6a1eb427649e3.

Command table (docs/CLI.md:522-527, `### The cursor`):
Lines 522-527 contain no fenced command blocks. The two nearest blocks — `nova-bus names --bus ~/bus` (lines 517-520) and the `nova-bus wait ...` block (lines 531-537) — belong to the neighboring sections (`names`, and `For harnesses that do not wake you`), not to `### The cursor`. STEP 3 therefore produced 0 blocks to run, in order.

DRIFT lines: none.

RAN 0
SKIPPED 0

Verification attempts (STEP 1, run from repo/):
  `git rev-parse HEAD` -> 5298f6be12eaa0f7e6622334d2b6a1eb427649e3 (matches the card's base) | exit 0
  `nova-version` -> `/usr/bin/bash: line 1: /home/nova/.local/bin/nova-version: Permission denied` | exit 126

Probe of the subject tool (the section's own verb, run from scratch/ to confirm exercisability):
  `nova-bus check --bus ~/bus --full` -> `/usr/bin/bash: line 1: /home/nova/.local/bin/nova-bus: Permission denied` | exit 126

Why this is SKIP, not CLEAN/DRIFT/BROKEN:
  The card conditions the whole reading on confirming the installed build ("a reading of a document against a different binary proves nothing"). `nova-version` cannot run here, so the build is unconfirmed; and the subject tool `nova-bus` also cannot run (both at /home/nova/.local/bin, which the nova-sandbox landlock whitelist does not include — the whitelist is the job root, the harness dir, /home/nova/sdk, and gomodcache). Nothing runnable ran, so nothing could agree with the document (not CLEAN) and no printed output disagreed with it (not DRIFT). The failure is environmental (sandbox denies the binary's directory), not a fault of the tool itself, so it is not BROKEN.

Left owed:
  - Build confirmation: could not confirm the installed binary is the card's base build (nova-version never ran).
  - Every claim of the section is unverifiable here: `inbox` and `check` not walking the bus, the lane files CURSOR/OPEN/INDEX and their temp-file-and-rename write pattern, the `--full --advance` read that replaces all three on a refused cursor, and the re-showing of edited, long-answered notes. None could be tested because nova-bus cannot be executed in this sandbox.
  - The card's STEP 4 flag/verb checks (does the tool have `--full --advance`, `inbox`, `check`, ...) could not be made, for the same reason.

`git status --short` at the end (run in repo/): printed nothing (empty; exit 0).