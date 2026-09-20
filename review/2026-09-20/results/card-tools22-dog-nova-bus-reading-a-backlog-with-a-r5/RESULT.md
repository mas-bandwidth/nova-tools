RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r5 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 5 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea+dirty linux/amd64 go1.26.6

# Command Table
n | command | exit | status
---|---|---|---
- | (no command blocks in section) | - | CLEAN

# DRIFT findings
(none)

# Summary
RAN 0
SKIPPED 0

# Left owed
Section 556-559 contains only descriptive text about `--decide` behavior with no fenced command blocks to execute.

# git status --short
?? RESULT.md
?? harness-output.log
?? opencode.json
?? repo/
?? .nova-sandbox-tmp/