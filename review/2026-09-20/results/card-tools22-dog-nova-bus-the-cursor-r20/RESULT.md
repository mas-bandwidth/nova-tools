RESULT tools22-dog-nova-bus-the-cursor-r20 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB The cursor
DOC docs/CLI.md:522-527
REPLICA 20 of 24
BUILD Permission denied (nova-version could not be executed on this machine)
| # | command | exit | verdict |
|---|---------|------|---------|
RAN 0
SKIPPED 0
Left owed: nova-version could not execute (Permission denied from /home/nova/.local/bin/nova-version under AppArmor with no_new_privs). The section at lines 522-527 is purely descriptive prose with zero fenced command blocks, so there was nothing to run.
git status --short: (no output)