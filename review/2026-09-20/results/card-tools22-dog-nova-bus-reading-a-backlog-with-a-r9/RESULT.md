RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r9 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus VERB Reading a backlog with a typed decision DOC docs/CLI.md:556-559 REPLICA 9 of 24 BUILD blocked by sandbox (nova-version: Permission denied)
| n | command | exit | verdict |
|---|---------|------|---------|
RAN 0 SKIPPED 0
Left owed: nova-version could not be executed — the Landlock sandbox did not grant read access to /home/glenn/.local/bin/nova-version (git rev-parse HEAD matched 5298f6be). The section is pure prose with zero fenced command blocks, so nothing to compare.
git status --short: (empty)