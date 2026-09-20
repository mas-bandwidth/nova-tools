RESULT tools22-dog-nova-bus-for-harnesses-that-do-no-r1 sha=5298f6be12ea — read `nova-bus For harnesses that do not wake you` against docs/CLI.md:528-541 and say CLEAN or DRIFT
SKIP section needs a pre-existing git-backed bus with participants, cursors, and INDEX — no creation instructions exist in the document

TOOL nova-bus | VERB For harnesses that do not wake you | DOC docs/CLI.md:528-541 | REPLICA 1 of 24 | BUILD nova-version devel linux/amd64 go1.26.6
n | command | exit | status
---|---|---|---
1 | nova-bus wait --bus ~/bus --as Ada --receipt-max-words 40 --timeout 25m --advance --remote origin --branch main | 2 | SKIP

SKIPPED 1 (all):
  1. `nova-bus wait --bus ~/bus --as Ada --receipt-max-words 40 --timeout 25m --advance --remote origin --branch main` — SKIP: needs a pre-existing git-backed bus directory (~//bus) containing participants.json, per-user lanes (from-ada/, from-bo/) with INDEX, CURSOR, OPEN, RECEIPTS files, and at least one git remote named origin pointing to branch main; docs/CLI.md:528-541 provides zero instructions for creating any of this infrastructure, so the command cannot be exercised on its own.

RAN 0
SKIPPED 1

Left owed: nothing beyond the skip above — the entire section consists of a single fenced command block whose prerequisites are entirely outside what the document tells you to make.
