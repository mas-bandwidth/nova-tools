RESULT tools22-dog-nova-bus-the-cursor-r18 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
BLOCKED installed build is /usr/bin/bash: line 1: /home/glenn/.local/bin/nova-version: Permission denied, not the card's base

- TOOL nova-bus
- VERB The cursor
- DOC docs/CLI.md:522-527
- REPLICA 18 of 24
- BUILD /usr/bin/bash: line 1: /home/glenn/.local/bin/nova-version: Permission denied (exit 126)

STEP 1 gate:
- `git rev-parse HEAD` printed `5298f6be12eaa0f7e6622334d2b6a1eb427649e3` (matches the pinned base).
- `nova-version` did NOT run. It printed `/usr/bin/bash: line 1: /home/glenn/.local/bin/nova-version: Permission denied` with exit status 126. The sha it printed is therefore not `5298f6be`; the installed build cannot be confirmed, and every `nova-*` binary under `/home/glenn/.local/bin` (the only `nova-version`/`nova-bus` on PATH) is refused by the sandbox the same way. A reading of docs/CLI.md against a binary I could not execute would prove nothing, so per the card's STEP 1 instruction I stop here and do not run the section's commands.

- RAN 0
- SKIPPED 0

Table of section command blocks: none executed.

Left owed: every fenced command block in docs/CLI.md:522-527 (`nova-bus The cursor` and anything it builds on) is unjudged — the tool could not run on this machine at all, so there is nothing to compare against the document.

`git status --short` at the job repo (pasted):
(empty — no output, exit 0)