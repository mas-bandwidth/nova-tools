RESULT tools22-dog-nova-bus-the-cursor-r12 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB The cursor
DOC docs/CLI.md:522-527
REPLICA 12 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/amd64 go1.27.1

| n | command | exit | verdict |
|---|---------|------|---------|

RAN 0
SKIPPED 0

The section docs/CLI.md:522-527 is prose only. It contains no fenced command
blocks, so there were no commands to run per STEP 3. Its claims were verified
against the tool built at the pinned base (sha 5298f6be12ea, matching the card):
- the verbs `inbox` and `check` exist in `nova-bus help`;
- the flags `--full` and `--advance` both exist (inbox takes `[--full]` and
  `[--advance --remote <name> --branch <name>]`);
- the lane state files CURSOR, OPEN and INDEX exist in the codebase
  (internal/bus/bus.go:171, internal/bus/attributes.go, internal/bus/conflict.go).

Nothing runnable disagreed with the document, so the section is CLEAN.

Left owed: none — the section has no runnable command blocks. The behavioural
claim that a cursor refusal is resolved by `--full --advance` was not exercised
end-to-end because the section gives no bus, lane or fixture with which to
produce a refused cursor, and per the card's rules I did not invent one.

git status --short (from repo): (empty — nothing printed)